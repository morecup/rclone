package better_chunk

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"github.com/pkg/errors"
	"github.com/rclone/rclone/fs"
	"github.com/rclone/rclone/fs/fspath"
	"github.com/rclone/rclone/lib/readers"
	"io"
	"path"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// Object represents a composite file wrapping one or more data chunks
type Object struct {
	remoteReal string
	remote     string
	size       int64
	mu         sync.RWMutex
	fs.Object
	fs Fs
}

func (o Object) String() string {
	return o.Remote()
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

var muMap sync.Map

// Lock 对给定键加锁
func Lock(key string) {
	// 尝试从sync.Map中获取锁，如果不存在，则创建一个新的
	actual, _ := muMap.LoadOrStore(key, &sync.Mutex{})
	mtx := actual.(*sync.Mutex)
	mtx.Lock()
	//fmt.Printf("Locking %s, Mutex address: %p\n", key, mtx)
	//fs.Infof(muMap, "Locking %s", key)
}

// Unlock 对给定键解锁
func Unlock(key string) {
	actual, exists := muMap.Load(key)
	if !exists {
		return // 如果锁不存在，则直接返回
	}
	mtx := actual.(*sync.Mutex)
	mtx.Unlock()
}

// Open opens the file for read.  Call Close() on the returned io.ReadCloser
func (o Object) Open(ctx context.Context, options ...fs.OpenOption) (io.ReadCloser, error) {
	var rangeStart int64 = 0
	var rangeEnd int64 = o.Size() - 1
	for _, option := range options {
		if rangeOption, ok := option.(*fs.RangeOption); ok {
			rangeStart = rangeOption.Start
			if rangeOption.End != -1 {
				rangeEnd = min(rangeOption.End, rangeEnd)
			}
		}
	}
	fs.Infof(o, "========== %d - %d ==========", rangeStart/1024/1024, rangeEnd/1024/1024)
	var objectKey = o.fs.Name() + ":" + fspath.JoinRootPath(o.fs.root, o.Remote())
	var chunkFileInfo ChunkFileInfo
	cacheValue, found := o.fs.ChunkInfoCache.Get(objectKey)
	if !found {
		Lock(objectKey)
		cacheValue, found = o.fs.ChunkInfoCache.Get(objectKey)
		if !found {
			baseFileRead, err := o.Object.Open(ctx, nil)
			// 关闭 ReadCloser
			defer func(baseFileRead io.ReadCloser) {
				err := baseFileRead.Close()
				if err != nil {
					panic(err)
				}
			}(baseFileRead)
			if err != nil {
				Unlock(objectKey)
				return nil, err
			}
			data, err := io.ReadAll(baseFileRead)
			if err != nil {
				Unlock(objectKey)
				return nil, err
			}
			if err := json.Unmarshal(data, &chunkFileInfo); err != nil {
				Unlock(objectKey)
				return nil, err
			}
			o.fs.ChunkInfoCache.SetDefault(objectKey, chunkFileInfo)
			Unlock(objectKey)
		} else {
			Unlock(objectKey)
			if chunk, ok := cacheValue.(ChunkFileInfo); ok {
				chunkFileInfo = chunk
			} else {
				return nil, errors.Errorf("error cache value: %v", cacheValue)
			}
		}
	} else {
		if chunk, ok := cacheValue.(ChunkFileInfo); ok {
			chunkFileInfo = chunk
		} else {
			return nil, errors.Errorf("error cache value: %v", cacheValue)
		}
	}
	fileStore := o.fs.FileStore
	fileFragList := chunkFileInfo.List
	sort.Slice(fileFragList, func(i, j int) bool {
		return fileFragList[i].Part < fileFragList[j].Part
	})
	startPart := rangeStart / CanUseSliceSize
	startOffset := rangeStart % CanUseSliceSize
	endPart := rangeEnd / CanUseSliceSize
	endOffset := rangeEnd % CanUseSliceSize

	// 使用context.WithCancel创建可取消的上下文
	ctxCancel, cancel := context.WithCancel(ctx)
	defer cancel() // 确保所有路径都调用cancel

	// 并发控制channel，限制同时下载的数量
	concurrencyLimit := 10 // 根据需要调整并发数量
	semaphore := make(chan struct{}, concurrencyLimit)

	var wg sync.WaitGroup
	var mu sync.Mutex // 用于保护共享资源allReaderCloser

	var once sync.Once             // 用于确保错误只会被发送一次
	errChan := make(chan error, 1) // 用于错误传递的channel

	j := -1
	minValue := min(int64(len(fileFragList)-1), endPart)
	var allReaderCloser = make([]io.ReadCloser, minValue-max(0, startPart)+1)
	if fileIdOperator, ok := fileStore.(fs.FileIdOperator); ok {
		fileRapidOperator, isRepidMode := fileStore.(fs.FileRapidOperator)
		for i, fileFragInfo := range fileFragList {
			if int64(i) < startPart || int64(i) > endPart {
				continue
			}
			//var fragOffsetBegin int64 = 0
			var fragOffsetEnd int64 = CanUseSliceSize - 1
			//if int64(i) == startPart {
			//	fragOffsetBegin = startOffset
			//}
			if int64(i) == endPart {
				fragOffsetEnd = endOffset
			}

			// 请求并发槽
			semaphore <- struct{}{}
			wg.Add(1)

			j += 1

			// 运行下载goroutine
			go func(fileFragInfo *fs.FileFragInfo, j int, fragOffsetEnd int64) {
				defer wg.Done()
				defer func() { <-semaphore }() // 释放并发槽

				// 检查上下文是否已被取消，如果是则提前返回
				select {
				case <-ctxCancel.Done():
					return
				default:
				}

				var goErr error

				defer func() {
					if goErr != nil {
						// 发生错误时，只发送第一个错误
						once.Do(func() {
							errChan <- goErr
							cancel() // 取消上下文，通知其他goroutine停止
						})
					}
				}()

				var readCloser io.ReadCloser
				if isRepidMode {
					readCloser, goErr = fileRapidOperator.DownFileRapid(ctx, *fileFragInfo, 0, 58+fragOffsetEnd)
					if goErr != nil {
						return
					}
					if readCloser != nil {
						mu.Lock()
						allReaderCloser[j] = readCloser
						mu.Unlock()
					}
					return
				}
				id := fileFragInfo.Id
				if id != "" {
					readCloser, goErr = fileIdOperator.DownFileFromId(ctx, fileFragInfo.Id, 0, 58+fragOffsetEnd)
					if goErr != nil {
						return
					}
					mu.Lock()
					allReaderCloser[j] = readCloser
					mu.Unlock()
				} else {
					object, goErr := fileStore.NewObject(ctx, fileFragInfo.Path)
					if goErr != nil {
						return
					}
					readCloser1, goErr := object.Open(ctx, nil)
					if goErr != nil {
						return
					}
					mu.Lock()
					allReaderCloser[j] = readCloser1
					mu.Unlock()
				}
			}(fileFragInfo, j, fragOffsetEnd)
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
	// 关闭errChan通道
	wg.Wait()
	close(errChan)
	// 检查是否有错误发送到errChan
	if err, ok := <-errChan; ok {
		return nil, err
	}
	readerList := make([]io.Reader, len(allReaderCloser))
	closerList := make([]io.Closer, len(allReaderCloser))
	for i, readerCloser := range allReaderCloser {
		// 跳过前54字节
		var err error
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
		if i == 0 {
			if _, err = io.CopyN(io.Discard, readerList[i], startOffset); err != nil {
				return nil, err
			}
		}
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
	//err := o.Remove(ctx)
	//if err != nil {
	//	return err
	//}
	object, err := o.fs.Put(ctx, in, src, options...)
	if err != nil {
		return err
	}
	chunkerObject := object.(*Object)
	o.size = chunkerObject.size
	o.remoteReal = chunkerObject.remoteReal
	o.remote = chunkerObject.remote
	o.fs = chunkerObject.fs
	o.Object = chunkerObject.Object
	return nil
}

// Remove Removes this object
func (o Object) Remove(ctx context.Context) (err error) {
	object, err := o.fs.FileStructure.NewObject(ctx, o.remote)
	if err != nil {
		return err
	}
	/*
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
	*/
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
