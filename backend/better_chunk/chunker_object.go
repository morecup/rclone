package better_chunk

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"github.com/rclone/rclone/fs"
	"github.com/rclone/rclone/lib/readers"
	"io"
	"path"
	"sort"
	"strconv"
	"strings"
)

// Object represents a composite file wrapping one or more data chunks
type Object struct {
	remoteReal string
	remote     string
	size       int64
	fs.Object
	fs Fs
}

func (o Object) Remote() string {
	if o.remoteReal == "" {
		parts := strings.SplitN(path.Base(o.remote), "￥}", 2)
		return path.Join(path.Dir(o.remote), parts[1])
	} else {
		return o.remoteReal
	}
}

func (o Object) Size() int64 {
	return o.size
}

func (o Object) Fs() fs.Info {
	return o.fs
}

type ChunkFileInfo struct {
	Name     string             `json:"name"`
	FileSize int64              `json:"fileSize"`
	List     []*fs.FileFragInfo `json:"list"`
}

// Open opens the file for read.  Call Close() on the returned io.ReadCloser
func (o Object) Open(ctx context.Context, options ...fs.OpenOption) (io.ReadCloser, error) {
	rangeStart := 0
	rangeEnd := o.Size() - 1
	for _, option := range options {
		if rangeOption, ok := option.(*fs.RangeOption); ok {
			rangeStart = rangeOption.Start
			rangeEnd = rangeOption.End
		}
	}
	baseFileRead, err := o.Object.Open(ctx, nil)
	if err != nil {
		return nil, err
	}
	data, err := io.ReadAll(baseFileRead)
	if err != nil {
		return nil, err
	}
	// 关闭 ReadCloser
	defer func(baseFileRead io.ReadCloser) {
		err := baseFileRead.Close()
		if err != nil {
			panic(err)
		}
	}(baseFileRead)

	// 将读取的内容反序列化为 JSON
	var chunkFileInfo ChunkFileInfo
	if err := json.Unmarshal(data, &chunkFileInfo); err != nil {
		return nil, err
	}
	fileStore := o.fs.FileStore
	fileFragList := chunkFileInfo.List
	sort.Slice(fileFragList, func(i, j int) bool {
		return fileFragList[i].Part < fileFragList[j].Part
	})
	var allReaderCloser []io.ReadCloser
	if fileIdOperator, ok := fileStore.(fs.FileIdOperator); ok {
		fileRapidOperator, isRepidMode := fileStore.(fs.FileRapidOperator)
		for _, fileFragInfo := range fileFragList {
			var readCloser io.ReadCloser
			if isRepidMode {
				readCloser, err = fileRapidOperator.DownFileRapid(ctx, *fileFragInfo, -1, -1)
				if readCloser != nil {
					allReaderCloser = append(allReaderCloser, readCloser)
				}
			}
			id := fileFragInfo.Id
			if id != "" {
				if !isRepidMode {
					readCloser, err = fileIdOperator.DownFileFromId(ctx, fileFragInfo.Id, -1, -1)
					if err != nil {
						return nil, err
					}
					allReaderCloser = append(allReaderCloser, readCloser)
				}
			} else {
				object, err := fileStore.NewObject(ctx, fileFragInfo.Path)
				if err != nil {
					return nil, err
				}
				readCloser1, err := object.Open(ctx, nil)
				if err != nil {
					return nil, err
				}
				allReaderCloser = append(allReaderCloser, readCloser1)
			}
		}
	} else {
		for _, fileFragInfo := range fileFragList {
			object, err := fileStore.NewObject(ctx, fileFragInfo.Path)
			if err != nil {
				return nil, err
			}
			readCloser, err := object.Open(ctx, nil)
			if err != nil {
				return nil, err
			}
			allReaderCloser = append(allReaderCloser, readCloser)
		}
	}
	readerList := make([]io.Reader, len(allReaderCloser))
	closerList := make([]io.Closer, len(allReaderCloser))
	for i, readerCloser := range allReaderCloser {
		// 跳过前54字节
		if _, err = io.CopyN(io.Discard, readerCloser, 54); err != nil {
			return nil, err
		}

		// 读取长度信息（4个字节）
		lenBuf := make([]byte, 4)
		if _, err = io.ReadFull(readerCloser, lenBuf); err != nil {
			return nil, err
		}
		length := int32(binary.LittleEndian.Uint32(lenBuf))
		readerList[i] = io.LimitReader(readerCloser, int64(length))
		closerList[i] = readerCloser
	}
	mutiReadCloser := readers.NewMutiReadCloser(readerList, closerList)
	return mutiReadCloser, nil
}

type ObjectInfoWrapper struct {
	fs.ObjectInfo
	remote string
	size   int64
}

func (ow ObjectInfoWrapper) Remote() string {
	return ow.remote
}

// Size returns the size of the file
func (ow ObjectInfoWrapper) Size() int64 {
	return ow.size
}
func NewObjectInfoWrapper(objectInfo fs.ObjectInfo, remote string, size int64) *ObjectInfoWrapper {
	return &ObjectInfoWrapper{
		ObjectInfo: objectInfo,
		remote:     remote,
		size:       size,
	}
}

// Update in to the object with the modTime given of the given size
//
// When called from outside an Fs by rclone, src.Size() will always be >= 0.
// But for unknown-sized objects (indicated by src.Size() == -1), Upload should either
// return an error or update the object properly (rather than e.g. calling panic).
func (o Object) Update(ctx context.Context, in io.Reader, src fs.ObjectInfo, options ...fs.OpenOption) error {
	panic("implement me")
}

// Remove Removes this object
func (o Object) Remove(ctx context.Context) (err error) {
	object, err := o.fs.FileStructure.NewObject(ctx, o.remote)
	if err != nil {
		return err
	}
	readCloser, err := object.Open(ctx, nil)
	if err != nil {
		return err
	}
	bytes, err := io.ReadAll(readCloser)
	if err != nil {
		return err
	}
	var chunkFileInfo ChunkFileInfo
	if err = json.Unmarshal(bytes, &chunkFileInfo); err != nil {
		return err
	}
	fragInfos := chunkFileInfo.List
	for _, info := range fragInfos {
		fileIdOperator, ok := o.fs.FileStore.(fs.FileIdOperator)
		if ok && info.Id != "" {
			err = fileIdOperator.RemoveFileFromId(ctx, info.Id, info.Size)
		} else {
			fileStoreObject, err1 := o.fs.FileStore.NewObject(ctx, info.Path)
			if err1 != nil {
				err = err1
			} else {
				err = fileStoreObject.Remove(ctx)
			}
		}
	}
	err = object.Remove(ctx)
	return err
}

func NewObject(o fs.Object, fs Fs) (*Object, error) {
	parts := strings.SplitN(path.Base(o.Remote()), "￥}", 2)
	atomic, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		return nil, err
	}
	return &Object{
		Object:     o,
		remote:     o.Remote(),
		remoteReal: path.Join(path.Dir(o.Remote()), parts[1]),
		size:       atomic,
		fs:         fs,
	}, nil
}
