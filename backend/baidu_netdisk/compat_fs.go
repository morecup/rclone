package baidu_netdisk

import (
	"context"
	"crypto/md5"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"github.com/pkg/errors"
	"github.com/rclone/rclone/backend/baidu_netdisk/api"
	"github.com/rclone/rclone/fs"
	"github.com/rclone/rclone/lib/readers"
	"io"
	"net/http"
	"strconv"
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
func (f *Fs) GetFileMeta(ctx context.Context, path string, needDownLink bool, needTextLink bool) (item *api.Item, resp *http.Response, err error) {
	itemList, resp, err := f.GetFileMetas(ctx, []string{path}, needDownLink, needTextLink)
	item = itemList[0]
	if err != nil {
		if item.Errno == -9 {
			return item, resp, fs.ErrorObjectNotFound
		}
	}
	return item, resp, nil
}

func (f *Fs) DownFileDisguiseBaiduClient(ctx context.Context, path string, options []fs.OpenOption) (resp *http.Response, err error) {
	item, _, err := f.GetFileMeta(ctx, path, true, false)
	if err != nil {
		return nil, err
	}
	if item.Dlink == "" {
		return nil, errors.WithStack(fmt.Errorf("dlink is empty. path:(%s)", path))
	}
	opts, err := f.api.DownFileDisguiseBaiduClient(item.Dlink)
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

func (f *Fs) GetTemplateVariable(ctx context.Context) (*api.TemplateInfo, error) {
	opts, err := f.api.GetTemplateVariable()
	if err != nil {
		return nil, err
	}
	info := new(api.TemplateResponse)
	err = f.pacer.Call(func() (bool, error) {
		resp, err := f.srv.CallJSON(ctx, opts, nil, info)
		return shouldRetry(ctx, resp, err)
	})
	if err != nil {
		return nil, err
	}
	return info.Result, nil
}

func (f *Fs) ListDirFilesPage(ctx context.Context, path string, page int, num int) (itemList []*api.Item, resp *http.Response, err error) {
	opts, err := f.api.ListDirFiles(path, api.OrderByTime, true, true, page, num)
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
	return info.List, resp, nil
}

func (f *Fs) ListDirAllFiles(ctx context.Context, path string) (itemList []*api.Item, err error) {
	for {
		list, _, err := f.ListDirFilesPage(ctx, path, 1, 1000)
		itemList = append(itemList, list...)
		if err != nil {
			return nil, err
		}
		if len(list) != 1000 {
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
	opts, err := f.api.DeleteDirsOrFiles(fileList, 0)
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
		return errors.WithStack(fmt.Errorf("delete files not success. FileList:(%v)", failPathList))
	}
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
	preCreateFileData, preCreateDTO, err := f.PreCreate(ctx, in, localCtime, localMtime, size, path)
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
		n := int64(f.opt.ChunkSize)
		if remaining < n {
			n = remaining
		}
		seg := readers.NewRepeatableReader(io.LimitReader(in, n))
		fs.Debugf(f, "Uploading segment %d/%d size %d", position, size, n)
		_, err := f.uploadFragment(ctx, path, uploadId, partSeq, seg)
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
func (f *Fs) PreCreate(ctx context.Context, in io.Reader, localCtime int64, localMtime int64, size int64, path string) (preCreateFileData *api.PreCreateFileData, info *api.PreCreateDTO, err error) {
	reader := readers.NewRepeatableReader(in)

	rapidOffsetData := &api.RapidOffsetData{}
	//preCreateFileData := &api.PreCreateFileData{}

	buffer := make([]byte, uploadBlockSize)
	// 存储多个 md5 hash 的切片
	var blockList []string
	contentHash := md5.New()

	var isFirst = true
	for {
		bytesRead, err := reader.Read(buffer)
		if err != nil {
			if err != io.EOF {
				return nil, nil, err
			}
			break
		}
		if isFirst {
			sliceMd5Hash := md5.New()
			sliceMd5Hash.Write(buffer[:min(bytesRead, sliceSize)])
			preCreateFileData.SliceMd5 = hex.EncodeToString(sliceMd5Hash.Sum(nil))
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

	opts, err := f.api.Precreate(path, rapidOffsetData, preCreateFileData)
	if err != nil {
		return nil, nil, err
	}
	info = new(api.PreCreateDTO)
	err = f.pacer.Call(func() (bool, error) {
		resp, err := f.srv.CallJSON(ctx, opts, nil, info)
		return shouldRetry(ctx, resp, err)
	})
	return preCreateFileData, info, err
}

func (f *Fs) uploadFragment(ctx context.Context, path string, uploadId string, partseq int, chunk io.ReadSeeker, options ...fs.OpenOption) (info *api.FragmentDTO, err error) {
	opts, err := f.api.Superfile2(path, uploadId, partseq, chunk, options...)
	if err != nil {
		return nil, err
	}
	info = new(api.FragmentDTO)
	err = f.pacer.Call(func() (bool, error) {
		resp, err := f.unAuth.CallJSON(ctx, opts, nil, info)
		return shouldRetry(ctx, resp, err)
	})
	return info, err
}

// Create {"ctime":1713679787,"from_type":1,"fs_id":908199638643457,"isdir":0,"md5":"cd46789bbnf14a0f4de795dd13a70ca3","mtime":1713679787,"path":"/test/999/111/8e3dc4f3a75d1e13f428a1dd15e57fb7.png","size":30051726,"errno":0,"name":"\/test\/999\/111\/8e3dc4f3a75d1e13f428a1dd15e57fb7.png","category":3}
func (f *Fs) Create(ctx context.Context, path string, preCreateFileData *api.PreCreateFileData, uploadId string) (info *api.CreateDTO, err error) {
	opts, err := f.api.Create(path, preCreateFileData, uploadId)
	if err != nil {
		return nil, err
	}
	info = new(api.CreateDTO)
	err = f.pacer.Call(func() (bool, error) {
		resp, err := f.srv.CallJSON(ctx, opts, nil, info)
		return shouldRetry(ctx, resp, err)
	})
	return info, err
}
