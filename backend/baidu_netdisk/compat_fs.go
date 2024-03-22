package baidu_netdisk

import (
	"context"
	"fmt"
	"github.com/pkg/errors"
	"github.com/rclone/rclone/backend/baidu_netdisk/api"
	"github.com/rclone/rclone/fs"
	"net/http"
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
	opts, err := f.api.DownFileDisguiseBaiduClient()
	opts.Options = options
	err = f.pacer.Call(func() (bool, error) {
		f.unAuth.SetRoot(item.Dlink)
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
