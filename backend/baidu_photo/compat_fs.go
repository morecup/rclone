package baidu_photo

import (
	"context"
	"crypto/md5"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"github.com/pkg/errors"
	"github.com/rclone/rclone/backend/baidu_photo/api"
	"github.com/rclone/rclone/fs"
	"github.com/rclone/rclone/lib/readers"
	"github.com/rclone/rclone/lib/rest"
	"io"
	"log"
	"net/http"
	"path"
	"strconv"
	"sync"
	"time"
)

// GetFileMetas 返回多个文件或文件夹信息（注意是path只能一一对应，并且无法获取到时不会报错，而是返回的info里没有对应的文件或文件夹时errno为-9，外层为0）
func (f *Fs) GetFileMetas(ctx context.Context, path []string, needDownLink bool, needTextLink bool) (itemList []*api.Item, resp *http.Response, err error) {
	opts, err := f.api.GetFileMetas(path, needDownLink, needTextLink)
	if err != nil {
		return nil, nil, err
	}
	info := new(api.InfoResponse)
	err = f.pacer.Call(func() (bool, error) {
		resp, err := f.srv.CallJSON(ctx, opts, nil, info)
		return shouldRetry(ctx, resp, err)
	})
	if err != nil {
		return info.Info, resp, err
	}
	return info.Info, resp, nil
}

// GetFileMeta 返回单个文件或文件夹信息，经过处理
func (f *Fs) GetFileMeta(ctx context.Context, filePath string) (item *api.Item, err error) {
	itemList, err := f.ListDirAllFiles(ctx)
	if err != nil {
		return nil, err
	}
	fileBasePath := path.Base(filePath)
	for _, itemR := range itemList {
		remoteFileBasePath := path.Base(itemR.Path)
		if fileBasePath == remoteFileBasePath {
			return itemR, nil
		}
	}
	return nil, fs.ErrorObjectNotFound
}

func (f *Fs) DownFileDisguiseBaiduClient(ctx context.Context, path string, options []fs.OpenOption) (resp *http.Response, err error) {
	item, err := f.GetFileMeta(ctx, path)
	if err != nil {
		return nil, err
	}
	if item.ThumburlStr == "" {
		return nil, errors.WithStack(fmt.Errorf("dlink is empty. path:(%s)", path))
	}
	opts, err := f.api.DownFileDisguiseBaiduClient(item.ThumburlStr)
	opts.Options = options
	err = f.pacer.Call(func() (bool, error) {
		resp, err = f.unAuth.Call(ctx, opts)
		return shouldRetry(ctx, resp, err)
	})
	if err != nil {
		return nil, err
	}
	return resp, nil
}

type multiReadCloser struct {
	io.Reader
	closers []io.Closer
}

func (m *multiReadCloser) Close() (err error) {
	for _, closer := range m.closers {
		if e := closer.Close(); e != nil {
			err = e
		}
	}
	return
}

// 有多少切片就开多少线程去下载
func (f *Fs) DownFile(ctx context.Context, path string, size int64, options []fs.OpenOption) (in io.ReadCloser, err error) {
	downUrl, err := f.GetDownUrl(ctx, path)
	if err != nil {
		return nil, err
	}

	var wg sync.WaitGroup // 使用 WaitGroup 确保所有 goroutine 完成

	// 计算任务数量
	taskNum := size / sliceMaxLen
	if size%sliceMaxLen != 0 {
		taskNum++
	}

	done := make(chan *http.Response, taskNum)
	responses := make([]*http.Response, 0, taskNum) // 存放所有的响应

	beginIndex := int64(0)
	for i := 0; i < int(taskNum); i++ {
		endIndex := min(beginIndex+sliceMaxLen-1, size-1)

		wg.Add(1) // 在启动 goroutine 之前计数加一
		go func(b int64, e int64) {
			defer wg.Done() // goroutine 完成后计数减一
			resp, err := f.DownFileResponse(ctx, downUrl, b, e)
			if err != nil {
				done <- nil
			} else {
				done <- resp
			}
		}(beginIndex, endIndex) // 注意这里将参数传进协程

		beginIndex = endIndex + 1
		if beginIndex >= size {
			break
		}
	}

	// 在另一个 goroutine 中收集所有响应，避免阻塞
	go func() {
		for response := range done {
			if response != nil {
				responses = append(responses, response)
			}
		}
	}()

	wg.Wait()   // 等待所有 goroutine 完成
	close(done) // 所有 goroutine 完成后，关闭channel

	readerList := make([]io.Reader, len(responses))
	closers := make([]io.Closer, len(responses))

	for i, resp := range responses {
		readerList[i] = resp.Body
		closers[i] = resp.Body
	}
	multi := &multiReadCloser{
		Reader:  io.MultiReader(readerList...),
		closers: closers,
	}
	return multi, nil
}

type TaskResult struct {
	taskId int
	resp   *http.Response // 你的任务返回类型
	err    error
}

var sliceMaxLen = int64(327680)

// 固定线程去下载
func (f *Fs) DownFileSe(ctx context.Context, path string, size int64, options []fs.OpenOption) (in io.ReadCloser, err error) {
	downUrl, err := f.GetDownUrl(ctx, path)
	if err != nil {
		return nil, err
	}

	var wg sync.WaitGroup // 使用 WaitGroup 确保所有 goroutine 完成

	// 计算任务数量
	taskNum := size / sliceMaxLen
	if size%sliceMaxLen != 0 {
		taskNum++
	}
	sem := make(chan struct{}, 1) // 最多同时执行2个任务
	done := make(chan TaskResult, taskNum)
	responses := make([]*http.Response, taskNum) // 存放所有的响应

	beginIndex := int64(0)
	for i := 0; i < int(taskNum); i++ {
		endIndex := min(beginIndex+sliceMaxLen-1, size-1)

		wg.Add(1) // 在启动 goroutine 之前计数加一
		go func(b int64, e int64, index int) {
			defer wg.Done() // goroutine 完成后计数减一
			sem <- struct{}{}
			resp, err1 := f.DownFileResponse(ctx, downUrl, b, e)
			log.Printf("任务1 taskResult: %v", resp)
			done <- TaskResult{
				taskId: index,
				resp:   resp,
				err:    err1,
			}
			<-sem
		}(beginIndex, endIndex, i) // 注意这里将参数传进协程

		beginIndex = endIndex + 1
		if beginIndex >= size {
			break
		}
	}

	// 在另一个 goroutine 中收集所有响应，避免阻塞
	go func() {
		for taskResult := range done {
			if taskResult.resp != nil {
				responses[taskResult.taskId] = taskResult.resp
			} else if taskResult.err != nil {
				log.Printf("任务err #%d taskResult: %v", taskResult.taskId, taskResult)
			}
		}
	}()

	wg.Wait()   // 等待所有 goroutine 完成
	close(done) // 所有 goroutine 完成后，关闭channel

	readerList := make([]io.Reader, len(responses))
	closers := make([]io.Closer, len(responses))

	for i, resp := range responses {
		readerList[i] = resp.Body
		closers[i] = resp.Body
	}
	multi := &multiReadCloser{
		Reader:  io.MultiReader(readerList...),
		closers: closers,
	}
	return multi, nil
}

// 串行执行
func (f *Fs) DownFileSerial(ctx context.Context, id string, size int64, options []fs.OpenOption) (in io.ReadCloser, err error) {
	downUrl, err := f.GetDownUrl(ctx, id)
	if err != nil {
		return nil, err
	}

	response, err := f.DownFileResponse(ctx, downUrl, -1, -1)
	if err != nil {
		return nil, err
	}
	return response.Body, nil
}

func (f *Fs) GetDownUrl(ctx context.Context, id string) (url string, err error) {
	opts, err := f.api.GetDownloadUrl(id)
	if err != nil {
		return "", err
	}
	info := new(api.DownloadVO)
	err = f.pacer.Call(func() (bool, error) {
		resp, err := f.srv.CallJSONBase(ctx, opts, nil, info)
		return shouldRetry(ctx, resp, err)
	})
	if err != nil {
		return "", err
	}
	return info.Dlink, nil
}

// DownFileBySlice 手机app设定的分片值为32768，或许可以设置更大一些，需要测试
func (f *Fs) DownFileResponse(ctx context.Context, downUrl string, beginIndex int64, endIndex int64) (resp *http.Response, err error) {
	header := make(map[string]string)
	if beginIndex != -1 && endIndex != -1 {
		header["Range"] = fmt.Sprintf("bytes=%d-%d", beginIndex, endIndex)
	}
	opts := &rest.Opts{
		Method:       "GET",
		RootURL:      downUrl,
		ExtraHeaders: header,
	}
	err = f.pacer.Call(func() (bool, error) {
		resp, err = f.srv.Call(ctx, opts)
		return shouldRetry(ctx, resp, err)
	})
	if err != nil {
		return nil, err
	}
	return resp, nil
}

func (f *Fs) GetUserInfo(ctx context.Context) (*api.UserInfo, error) {
	opts, err := f.api.GetUserInfo()
	if err != nil {
		return nil, err
	}
	info := new(api.UserInfoResponse)
	err = f.pacer.Call(func() (bool, error) {
		resp, err := f.srv.CallJSON(ctx, opts, nil, info)
		return shouldRetry(ctx, resp, err)
	})
	if err != nil {
		return nil, err
	}
	return info.UserInfo, nil
}

func (f *Fs) GetQuotaInfo(ctx context.Context) (*api.QuotaInfoResponse, error) {
	opts, err := f.api.GetQuotaInfo()
	if err != nil {
		return nil, err
	}
	info := new(api.QuotaInfoResponse)
	err = f.pacer.Call(func() (bool, error) {
		resp, err := f.srv.CallJSON(ctx, opts, nil, info)
		return shouldRetry(ctx, resp, err)
	})
	if err != nil {
		return nil, err
	}
	return info, nil
}

func (f *Fs) ListDirFilesPage(ctx context.Context, cursor string) (listRes *api.ListResponse, resp *http.Response, err error) {
	opts, err := f.api.ListDirFiles("")
	if err != nil {
		return nil, nil, err
	}
	info := new(api.ListResponse)
	err = f.pacer.Call(func() (bool, error) {
		resp, err := f.srv.CallJSON(ctx, opts, nil, info)
		return shouldRetry(ctx, resp, err)
	})
	if err != nil {
		return nil, nil, err
	}
	return info, resp, nil
}

func (f *Fs) ListDirAllFiles(ctx context.Context) (itemList []*api.Item, err error) {
	nowCursor := ""
	for {
		listRes, _, err := f.ListDirFilesPage(ctx, nowCursor)
		if err != nil {
			return nil, err
		}
		nowCursor = listRes.Cursor
		itemList = append(itemList, listRes.List...)
		if listRes.HasMore == 0 {
			break
		}
	}
	return itemList, nil
}

// CreateDirForce force to create file ,if exists , not do anything
func (f *Fs) CreateDirForce(ctx context.Context, path string) (err error) {
	opts, err := f.api.CreateDir(path, true)
	if err != nil {
		return err
	}
	info := new(api.BaiduResponse)
	err = f.pacer.Call(func() (bool, error) {
		resp, err := f.srv.CallJSONIgnore(ctx, opts, nil, info, []int{-8})
		return shouldRetry(ctx, resp, err)
	})
	if info.Errno == -8 {
		fs.Debugf(nil, "File already exists.(%s)", path)
	}
	return nil
}

func (f *Fs) DeleteDirsOrFiles(ctx context.Context, fileList []string) (err error) {
	opts, err := f.api.DeleteDirsOrFiles(fileList)
	info := new(api.BaiduResponse)
	err = f.pacer.Call(func() (bool, error) {
		resp, err := f.srv.CallJSON(ctx, opts, nil, info)
		return shouldRetry(ctx, resp, err)
	})
	if err != nil {
		return err
	}
	return nil
}

func (f *Fs) DeleteDirOrFile(ctx context.Context, filePath string) (err error) {
	return f.DeleteDirsOrFiles(ctx, []string{filePath})
}

func (f *Fs) RenameDirsOrFiles(ctx context.Context, fileParamList []api.FileManagerParam) (err error) {
	opts, err := f.api.RenameDirsOrFiles(fileParamList, 0)
	info := new(api.InfoResponse)
	err = f.pacer.Call(func() (bool, error) {
		resp, err := f.srv.CallJSON(ctx, opts, nil, info)
		return shouldRetry(ctx, resp, err)
	})
	if err != nil {
		return err
	}
	failPathList := make([]string, 0)
	successPathList := make([]string, 0)
	for _, item := range info.Info {
		if item.Errno != 0 {
			failPathList = append(failPathList, item.Path)
		} else {
			successPathList = append(successPathList, item.Path)
		}
	}
	if len(failPathList) == 0 {
		return nil
	} else {
		return errors.WithStack(fmt.Errorf("rename files not success. FileList:(%v)", failPathList))
	}
}

func (f *Fs) RenameDirOrFile(ctx context.Context, fileParamList api.FileManagerParam) (err error) {
	return f.RenameDirsOrFiles(ctx, []api.FileManagerParam{fileParamList})
}

func (f *Fs) MoveOrCopyDirsOrFiles(ctx context.Context, fileParamList []api.FileManagerParam, operate api.Operate) (err error) {
	opts, err := f.api.MoveOrCopyDirsOrFiles(fileParamList, 0, operate)
	info := new(api.InfoResponse)
	err = f.pacer.Call(func() (bool, error) {
		resp, err := f.srv.CallJSON(ctx, opts, nil, info)
		return shouldRetry(ctx, resp, err)
	})
	if err != nil {
		return err
	}
	failPathList := make([]string, 0)
	successPathList := make([]string, 0)
	for _, item := range info.Info {
		if item.Errno != 0 {
			failPathList = append(failPathList, item.Path)
		} else {
			successPathList = append(successPathList, item.Path)
		}
	}
	if len(failPathList) == 0 {
		return nil
	} else {
		return errors.WithStack(fmt.Errorf("rename files not success. FileList:(%v)", failPathList))
	}
}

func (f *Fs) MoveOrCopyDirOrFile(ctx context.Context, fileParamList api.FileManagerParam, operate api.Operate) (err error) {
	return f.MoveOrCopyDirsOrFiles(ctx, []api.FileManagerParam{fileParamList}, operate)
}

func (f *Fs) UploadFile(ctx context.Context, in io.Reader, localCtime int64, localMtime int64, size int64, path string) error {
	reader := readers.NewRepeatableReader(in)
	preCreateFileData, preCreateDTO, err := f.PreCreate(ctx, reader, localCtime, localMtime, size, path)
	if err != nil {
		return err
	}
	if preCreateDTO.ReturnType != 1 {
		fs.Debugf(f, "rapid upload success!(%s)", path)
		return nil
	}

	uploadId := preCreateDTO.UploadId

	// Upload the chunks
	remaining := size
	position := int64(0)
	partSeq := 0
	for remaining > 0 {
		n := int64(uploadBlockSize)
		if remaining < n {
			n = remaining
		}
		//seg := io.LimitReader(reader, n)
		fs.Debugf(f, "Uploading segment %d/%d size %d", position, size, n)
		_, err := f.uploadFragment(ctx, path, uploadId, partSeq, position, size, reader, n)
		//if(info.Md5)
		if err != nil {
			return nil
		}
		remaining -= n
		position += n
		partSeq += 1
	}

	_, err = f.Create(ctx, path, preCreateFileData, uploadId)
	if err != nil {
		return err
	}
	return nil
}

const uploadBlockSize = 1024 * 1024 * 4
const sliceSize = 1024 * 256
const offsetSize = 1024 * 256

// PreCreate {"path":"/test/999/111/1234.exe","return_type":1,"block_list":["5910a591dd8fc18c32a8f3df4fdc1761","a5fc157d78e6ad1c7e114b056c92821e"],"errno":0,"request_id":278285463311322051}
// { "return_type": 2, "errno": 0, "info": { "md5": "5ddc05b01g7f6ae7d6adc90d912c983d", "category": 6, "fs_id": 166064416325948, "request_id": 280244028406040000, "from_type": 1, "size": 112060240, "isdir": 0, "mtime": 1713672326, "ctime": 1713672326, "path": "/test/999/111/1234_20240421_120525.exe" }, "request_id": 280244028406040573 }
// return_type 1 无法秒传，准备上传 2 秒传成功 3 文件已存在（仅一刻相册，在百度网盘中只会返回2）
func (f *Fs) PreCreate(ctx context.Context, reader *readers.RepeatableReader, localCtime int64, localMtime int64, size int64, path string) (preCreateFileData *api.PreCreateFileData, info *api.PreCreateVO, err error) {
	//reader := readers.NewRepeatableReader(in)
	_, err = reader.Seek(0, io.SeekStart)
	if err != nil {
		return nil, nil, err
	}
	defer func(reader *readers.RepeatableReader, offset int64, whence int) {
		_, err := reader.Seek(offset, whence)
		if err != nil {
		}
	}(reader, 0, io.SeekStart)

	rapidOffsetData := &api.RapidOffsetData{}
	preCreateFileData = &api.PreCreateFileData{}

	buffer := make([]byte, uploadBlockSize)
	// 存储多个 md5 hash 的切片
	var blockList []string
	contentHash := md5.New()
	sliceMd5Hash := md5.New()

	var isFirst = true
	for {
		bytesRead, err := io.ReadFull(reader, buffer)
		if err != nil {
			if err == io.EOF {
				break
			}
		}
		if isFirst {
			sliceMd5Hash.Write(buffer[:min(bytesRead, sliceSize)])
			isFirst = false
		}
		uploadBlockHash := md5.New()
		uploadBlockHash.Write(buffer[:bytesRead])
		uploadBlockMd5 := uploadBlockHash.Sum(nil)
		blockList = append(blockList, hex.EncodeToString(uploadBlockMd5)) // 追加每个 4MB 片段的 md5 值到列表

		contentHash.Write(buffer[:bytesRead])
	}

	contentMd5 := hex.EncodeToString(contentHash.Sum(nil))
	preCreateFileData.ContentMd5 = contentMd5
	preCreateFileData.SliceMd5 = hex.EncodeToString(sliceMd5Hash.Sum(nil))
	if blockList == nil {
		blockList = []string{contentMd5}
	}
	preCreateFileData.BlockList = blockList
	preCreateFileData.LocalCtime = localCtime //src.ModTime(ctx).Unix()
	preCreateFileData.LocalMtime = localMtime //src.ModTime(ctx).Unix()
	preCreateFileData.Size = size

	nowTime := time.Now().Unix()

	var str = strconv.FormatInt(f.UserId, 10) + contentMd5 + strconv.FormatInt(nowTime, 10)
	h := md5.New()
	h.Write([]byte(str))
	md5Str := hex.EncodeToString(h.Sum(nil))[0:8]
	result, err := strconv.ParseInt(md5Str, 16, 64)
	if err != nil {
		return nil, nil, err
	}
	if size >= offsetSize {
		rapidOffsetData.DataOffset = result % (size - offsetSize + 1)
		rapidOffsetData.DataTime = nowTime
		_, err = reader.Seek(rapidOffsetData.DataOffset, io.SeekStart)
		if err != nil {
			return nil, nil, err
		}
		offsetBuffer := make([]byte, offsetSize)
		n, err := io.ReadFull(reader, offsetBuffer)
		if err != nil {
			return nil, nil, err
		}
		// 将读取的数据编码为base64
		rapidOffsetData.DataContent = base64.StdEncoding.EncodeToString(offsetBuffer[:n])
	}

	opts, err := f.api.Precreate(path, rapidOffsetData, preCreateFileData)
	if err != nil {
		return nil, nil, err
	}
	info = new(api.PreCreateVO)
	err = f.pacer.Call(func() (bool, error) {
		resp, err := f.srv.CallJSON(ctx, opts, nil, info)
		return shouldRetry(ctx, resp, err)
	})
	return preCreateFileData, info, err
}

func (f *Fs) uploadFragment(ctx context.Context, path string, uploadId string, partseq int, start int64, totalSize int64, chunk io.ReadSeeker, chunkSize int64, options ...fs.OpenOption) (info *api.FragmentVO, err error) {
	//var skip = int64(0)
	_, _ = chunk.Seek(start, io.SeekStart)
	realChunkSize := min(chunkSize, totalSize-start)
	opts, err := f.api.Superfile2(path, uploadId, partseq, realChunkSize, io.LimitReader(chunk, chunkSize), options...)
	if err != nil {
		return nil, err
	}
	//toSend := chunkSize - skip
	//opts.ContentLength = &toSend
	//opts.ContentRange = fmt.Sprintf("bytes %d-%d/%d", start+skip, min(start+chunkSize-1, totalSize-1), totalSize)

	info = new(api.FragmentVO)
	err = f.pacer.Call(func() (bool, error) {
		resp, err := f.srv.CallJSON(ctx, opts, nil, info)
		return shouldRetry(ctx, resp, err)
	})
	return info, err
}

// Create {"ctime":1713679787,"from_type":1,"fs_id":908199638643457,"isdir":0,"md5":"cd46789bbnf14a0f4de795dd13a70ca3","mtime":1713679787,"path":"/test/999/111/8e3dc4f3a75d1e13f428a1dd15e57fb7.png","size":30051726,"errno":0,"name":"\/test\/999\/111\/8e3dc4f3a75d1e13f428a1dd15e57fb7.png","category":3}
func (f *Fs) Create(ctx context.Context, path string, preCreateFileData *api.PreCreateFileData, uploadId string) (info *api.CreateVO, err error) {
	// todo 如果生成的又body，则需要在f.pacer.Call内生成，否则重试的时候body已经被读取过了，就会为空
	info = new(api.CreateVO)
	err = f.pacer.Call(func() (bool, error) {
		opts, err := f.api.Create(path, preCreateFileData, uploadId)
		if err != nil {
			return false, err
		}
		resp, err := f.srv.CallJSON(ctx, opts, nil, info)
		return shouldRetry(ctx, resp, err)
	})
	return info, err
}
