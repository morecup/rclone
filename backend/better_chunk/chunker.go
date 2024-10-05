package better_chunk

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/google/uuid"
	"github.com/rclone/rclone/fs"
	"github.com/rclone/rclone/fs/cache"
	"github.com/rclone/rclone/fs/config/configmap"
	"github.com/rclone/rclone/fs/config/configstruct"
	"github.com/rclone/rclone/fs/fspath"
	"github.com/rclone/rclone/fs/hash"
	"github.com/rclone/rclone/fs/operations"
	"io"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	MaxSliceSize = 1024 * 1024 * 29
)

func init() {
	fs.Register(&fs.RegInfo{
		Name:        "better_chunk",
		Description: "better chunk",
		NewFs:       NewFs,
		Options: []fs.Option{{
			Name:     "file_structure_remote",
			Required: true,
			Help: `Remote to chunk/unchunk.

Normally should contain a ':' and a path, e.g. "myremote:path/to/dir",
"myremote:bucket" or maybe "myremote:" (not recommended).`,
		}, {
			Name:     "file_store_remote",
			Required: true,
			Help: `Remote to chunk/unchunk.

Normally should contain a ':' and a path, e.g. "myremote:path/to/dir",
"myremote:bucket" or maybe "myremote:" (not recommended).`,
		}},
	})
}

// Options defines the configuration for this backend
type Options struct {
	FileStructureRemote string `config:"file_structure_remote"`
	FileStoreRemote     string `config:"file_store_remote"`
}

// Fs represents a wrapped fs.Fs
type Fs struct {
	name          string
	root          string
	FileStructure fs.Fs
	FileStore     fs.Fs
	features      *fs.Features // optional features
}

func (f Fs) Name() string {
	return f.name
}

func (f Fs) Root() string {
	return f.root
}

func (f Fs) String() string {
	return fmt.Sprintf("better chunk Chunked '%s:%s'", f.name, f.root)
}

func (f Fs) Precision() time.Duration {
	return f.FileStructure.Precision()
}

func (f Fs) Hashes() hash.Set {
	//return hash.NewHashSet(hash.MD5)
	return hash.Set(hash.None)
}

func (f Fs) Features() *fs.Features {
	return f.features
}

func (f Fs) List(ctx context.Context, dir string) (entries fs.DirEntries, err error) {
	dirEntries, err := f.FileStructure.List(ctx, dir)
	if err != nil {
		return nil, err
	}
	realDirEntries := make([]fs.DirEntry, 0)
	for _, dirOrObject := range dirEntries {
		switch entry := dirOrObject.(type) {
		case fs.Object:
			remote := dirOrObject.Remote()
			if strings.Contains(remote, "￥}") {
				parts := strings.SplitN(remote, "￥}", 2)
				// 输出拆分后的两部分
				if len(parts) == 2 {
					object, err1 := NewObject(entry, f)
					if err1 != nil {
						return nil, err1
					}
					realDirEntries = append(realDirEntries, object)
				}
			}
		case fs.Directory:
			realDirEntries = append(realDirEntries, entry)
		default:
			fs.Debugf(f, "unknown object type %T", entry)
		}
	}
	return realDirEntries, nil
}

func (f Fs) NewObject(ctx context.Context, remote string) (fs.Object, error) {
	dir := path.Dir(remote)
	if dir == "." {
		dir = ""
	}
	entries, err := f.List(ctx, dir)
	if err != nil {
		return nil, err
	}
	for _, dirOrObject := range entries {
		object, ok := dirOrObject.(*Object)
		if ok && object.Remote() == remote {
			return object, nil
		}
	}
	return nil, fs.ErrorObjectNotFound
}

// src.Size()可能为-1
func (f Fs) PutStream(ctx context.Context, in io.Reader, src fs.ObjectInfo, options ...fs.OpenOption) (fs.Object, error) {
	return f.Put(ctx, in, src, options...)
}

func (f Fs) Put(ctx context.Context, in io.Reader, src fs.ObjectInfo, options ...fs.OpenOption) (fs.Object, error) {
	//	1.先收集所有信息
	//	将in拆分成很多片
	srcObject, readFromObject := src.(fs.Object)
	fileFragInfoList := make([]*fs.FileFragInfo, 0)
	var allSize int64
	if !readFromObject {
		allSize = 0
	} else {
		allSize = src.Size()
	}
	saveSize := int64(0)
	var wg sync.WaitGroup
	var mu sync.Mutex // 用于保护 fileFragInfoList 的互斥锁

	// 控制并发数量的channel
	concurrencyLimit := 8
	semaphore := make(chan struct{}, concurrencyLimit)

	var once sync.Once             // 用于确保错误只会被发送一次
	errChan := make(chan error, 1) // 用于错误传递的channel

	i := 0
	for {
		var sliceIn io.Reader
		var sliceSize int64
		if !readFromObject {
			sliceBuffer := make([]byte, MaxSliceSize)
			sliceSizeO, err := io.ReadFull(in, sliceBuffer)
			if err != nil && err != io.EOF && !errors.Is(err, io.ErrUnexpectedEOF) {
				return nil, err
			} else if err == io.EOF {
				break
			}
			sliceSize = int64(sliceSizeO)
			allSize += sliceSize
			fs.Debugf(allSize, "================="+strconv.FormatInt(sliceSize, 10)+"====================")
			sliceIn = bytes.NewBuffer(sliceBuffer)
		} else {
			if saveSize >= allSize {
				break
			}
			sliceSize = min(allSize-saveSize, MaxSliceSize)
			option := &fs.RangeOption{
				Start: saveSize,
				End:   saveSize + sliceSize - 1,
			}
			readCloser, err := srcObject.Open(ctx, option)
			if err != nil {
				return nil, err
			}
			sliceIn = readCloser
			saveSize += sliceSize
		}

		// 获取信号量的一个位置
		semaphore <- struct{}{}
		wg.Add(1)
		go func(i int, sliceIn io.Reader, sliceSize int64) {
			defer wg.Done()
			defer func() { <-semaphore }() // 释放信号量位置

			bmpReader, beforeSize, afterSize := ToBmpReader(sliceIn, sliceSize)
			remote := strings.Replace(uuid.New().String(), "-", "", -1)
			objectInfoWrapper := NewObjectInfoWrapper(src, remote, sliceSize+beforeSize+afterSize)
			var fileFragInfo *fs.FileFragInfo = nil
			//	每一片去上传到文件存储的文件中
			if fileRapidOperator, ok := f.FileStore.(fs.FileRapidOperator); ok {
				fileFragInfo1, object, _ := fileRapidOperator.UploadFileReturnRapidInfo(ctx, bmpReader, objectInfoWrapper, options...)
				fileFragInfo = fileFragInfo1
				if fileFragInfo != nil {
					fileFragInfo.Part = int32(i)
					fileFragInfo.Path = object.Remote()
				}
			}
			if fileIdOperator, ok := f.FileStore.(fs.FileIdOperator); ok && fileFragInfo == nil {
				object, id, err := fileIdOperator.UploadFileReturnId(ctx, bmpReader, objectInfoWrapper, options...)
				if err == nil {
					fileFragInfo = &fs.FileFragInfo{
						Size: object.Size(),
						Part: int32(i),
						Path: object.Remote(),
						Id:   id,
					}
				}
			}
			if fileFragInfo == nil {
				object, err := f.FileStore.Put(ctx, bmpReader, objectInfoWrapper, options...)
				if err != nil {
					// 发生错误时，只发送第一个错误
					once.Do(func() {
						errChan <- err
					})
					return
				}
				//	拿到需要的信息，实际路径以及fsid
				fileFragInfo = &fs.FileFragInfo{
					Size: object.Size(),
					Part: int32(i),
					Path: object.Remote(),
				}
			}
			mu.Lock()
			fileFragInfoList = append(fileFragInfoList, fileFragInfo)
			mu.Unlock()
		}(i, sliceIn, sliceSize)
		i += 1
	}
	// 关闭errChan通道
	wg.Wait()
	close(errChan)

	// 检查是否有错误发送到errChan
	if err, ok := <-errChan; ok {
		return nil, err
	}

	sort.Slice(fileFragInfoList, func(i, j int) bool {
		return fileFragInfoList[i].Part < fileFragInfoList[j].Part
	})
	//	2.在上传到文件架构的文件中（记得remote需要含有￥}）
	chunkFileInfo := &ChunkFileInfo{
		Name:     path.Base(src.Remote()),
		FileSize: src.Size(),
		List:     fileFragInfoList,
	}
	chunkFileJson, err := json.Marshal(chunkFileInfo)
	if err != nil {
		return nil, err
	}
	chunkRemote := path.Join(path.Dir(src.Remote()), strconv.FormatInt(allSize, 10)+"￥}"+path.Base(src.Remote()))
	structureObject, err := f.FileStructure.Put(ctx, bytes.NewReader(chunkFileJson), NewObjectInfoWrapper(src, chunkRemote, int64(len(chunkFileJson))))
	if err != nil {
		return nil, err
	}
	chunkObject := &Object{
		remote: structureObject.Remote(),
		size:   allSize,
		Object: structureObject,
		fs:     f,
	}
	return chunkObject, nil
}

func (f Fs) Mkdir(ctx context.Context, dir string) error {
	return f.FileStructure.Mkdir(ctx, dir)
}

// 只删除文件夹，不用删除文件夹内的文件
func (f Fs) Rmdir(ctx context.Context, dir string) error {
	return f.FileStructure.Rmdir(ctx, dir)
}

func (f *Fs) DirMove(ctx context.Context, src fs.Fs, srcRemote, dstRemote string) error {
	srcRealFs, ok := src.(*Fs)
	if !ok {
		return f.FileStructure.Features().DirMove(ctx, src, srcRemote, dstRemote)
	} else {
		return f.FileStructure.Features().DirMove(ctx, srcRealFs.FileStructure, srcRemote, dstRemote)
	}
}

// Copy src to this remote using server-side copy operations.
//
// This is stored with the remote path given.
//
// It returns the destination Object and a possible error.
//
// Will only be called if src.Fs().Name() == f.Name()
//
// If it isn't possible then return fs.ErrorCantCopy
func (f *Fs) Copy(ctx context.Context, src fs.Object, remote string) (fs.Object, error) {
	srcObj, ok := src.(*Object)
	if !ok {
		fs.Debugf(src, "Can't copy - not same remote type")
		return nil, fs.ErrorCantCopy
	}
	dir, fileName := path.Split(remote)
	fixedRemote := strings.ReplaceAll(filepath.Join(dir, strconv.FormatInt(srcObj.size, 10)+"￥}"+fileName), "\\", "/")
	object, err := f.FileStructure.Features().Copy(ctx, srcObj.Object, fixedRemote)
	if err != nil {
		return nil, err
	}
	newObject, err := NewObject(object, *f)
	if err != nil {
		return nil, err
	}

	return newObject, nil
}

func (f *Fs) Move(ctx context.Context, src fs.Object, remote string) (fs.Object, error) {
	srcObj, ok := src.(*Object)
	if !ok {
		fs.Debugf(src, "Can't copy - not same remote type")
		return nil, fs.ErrorCantCopy
	}
	dir, fileName := path.Split(remote)
	fixedRemote := strings.ReplaceAll(filepath.Join(dir, strconv.FormatInt(srcObj.size, 10)+"￥}"+fileName), "\\", "/")
	object, err := f.FileStructure.Features().Move(ctx, srcObj.Object, fixedRemote)
	if err != nil {
		return nil, err
	}
	newObject, err := NewObject(object, *f)
	if err != nil {
		return nil, err
	}

	return newObject, nil
}

// About gets quota information from the Fs
func (f *Fs) About(ctx context.Context) (*fs.Usage, error) {
	do := f.FileStore.Features().About
	if do == nil {
		return nil, errors.New("not supported by underlying remote")
	}
	return do(ctx)
}

func NewFs(ctx context.Context, name, rpath string, m configmap.Mapper) (fs.Fs, error) {
	// Parse config into Options struct
	opt := new(Options)
	err := configstruct.Set(m, opt)
	if err != nil {
		return nil, err
	}

	fileStructureRemote, isFile, err := getFsFromRemote(ctx, rpath, opt.FileStructureRemote)
	if err != nil {
		return nil, err
	}
	if !operations.CanServerSideMove(fileStructureRemote) {
		return nil, errors.New("can't use chunker on a backend which doesn't support server-side move or copy")
	}
	if isFile {
		rpath = filepath.Dir(rpath)
	}
	fileStoreRemote, err := getFsFromRemoteBase(ctx, rpath, opt.FileStoreRemote)
	if err != nil {
		return nil, err
	}

	f := &Fs{
		name:          name,
		root:          rpath,
		FileStructure: fileStructureRemote,
		FileStore:     fileStoreRemote,
	}
	//cache.PinUntilFinalized(f.FileStructure, f)
	//cache.PinUntilFinalized(fileStoreRemote, f)
	f.features = (&fs.Features{
		CaseInsensitive:         false,
		ReadMimeType:            true,
		CanHaveEmptyDirectories: true,
	}).Fill(ctx, f)

	if isFile {
		return f, fs.ErrorIsFile
	} else {
		return f, nil
	}
}
func getFsFromRemote(ctx context.Context, rpath string, remote string) (fs.Fs, bool, error) {
	baseName, basePath, err := fspath.SplitFs(remote)
	if err != nil {
		return nil, false, fmt.Errorf("failed to parse remote %q to wrap: %w", remote, err)
	}
	// Look for a file first
	remotePath := fspath.JoinRootPath(basePath, rpath)
	baseFs, err := cache.Get(ctx, baseName+path.Dir(remotePath))
	if err == nil {
		dirEntries, err := baseFs.List(ctx, "")
		if err == nil {
			for _, dirOrObject := range dirEntries {
				switch dirOrObject.(type) {
				case fs.Object:
					remote1 := path.Base(dirOrObject.Remote())
					if strings.Contains(remote1, "￥}") {
						parts := strings.SplitN(remote1, "￥}", 2)
						// 输出拆分后的两部分
						if len(parts) == 2 {
							if parts[1] == path.Base(rpath) {
								return baseFs, true, nil
							}
						}
					}
				}
			}
		}
	}

	baseFs, err = cache.Get(ctx, baseName+remotePath)
	if err != nil {
		return nil, false, fmt.Errorf("failed to make remote %q to wrap: %w", baseName+remotePath, err)
	} else {
		return baseFs, false, nil
	}
}
func getFsFromRemoteBase(ctx context.Context, rpath string, remote string) (fs.Fs, error) {
	baseName, basePath, err := fspath.SplitFs(remote)
	if err != nil {
		return nil, fmt.Errorf("failed to parse remote %q to wrap: %w", remote, err)
	}
	// Look for a file first
	remotePath := fspath.JoinRootPath(basePath, rpath)
	baseFs, err := cache.Get(ctx, baseName+remotePath)
	if err != nil {
		return nil, fmt.Errorf("failed to make remote %q to wrap: %w", baseName+remotePath, err)
	} else {
		return baseFs, nil
	}
}
