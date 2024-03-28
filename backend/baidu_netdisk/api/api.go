package api

import (
	"encoding/json"
	"github.com/gorilla/schema"
	"github.com/rclone/rclone/lib/rest"
	"io"
	"net/url"
	"strconv"
	"strings"
)

type BaiduApi struct {
}

func (b *BaiduApi) GetFileMetas(path []string, dlink bool, text bool) (opts *rest.Opts, err error) {

	opts = &rest.Opts{
		Method:     "GET",
		Path:       "/api/filemetas",
		Parameters: url.Values{"target": {BodyList(FixToBaiduPathList(path))}, "dlink": {BoolToStr(dlink)}, "text": {BoolToStr(text)}},
	}
	return opts, nil
}

type OrderType string
type Operate string

const (
	OrderByName OrderType = "name"
	OrderByTime OrderType = "time"
	OrderBySize OrderType = "size"

	CopyOperate   Operate = "copy"
	MoveOperate   Operate = "move"
	RenameOperate Operate = "rename"
	DeleteOperate Operate = "delete"
)

// ListDirFiles order default time , desc default true ,showEmpty default false ,page begin from 1,num default 100
func (b *BaiduApi) ListDirFiles(dir string, order OrderType, desc bool, showEmpty bool, page int, num int) (opts *rest.Opts, err error) {
	if order == "" {
		order = "time"
	}
	if page == 0 {
		page = 1
	}
	if num == 0 {
		num = 100
	}

	opts = &rest.Opts{
		Method: "GET",
		Path:   "/api/list",
		Parameters: url.Values{
			"dir":       []string{FixToBaiduPath(dir)},
			"order":     []string{string(order)},
			"desc":      []string{BoolToStr(desc)},
			"showempty": []string{BoolToStr(showEmpty)},
			"page":      []string{strconv.Itoa(page)},
			"num":       []string{strconv.Itoa(num)},
		},
	}
	return opts, nil
}

func (b *BaiduApi) GetTemplateVariable() (opts *rest.Opts, err error) {
	opts = &rest.Opts{
		Method: "GET",
		Path:   "/api/gettemplatevariable",
		Parameters: url.Values{
			"fields": []string{"[\"bdstoken\",\"token\",\"uk\",\"isdocuser\",\"servertime\"]"},
		},
	}
	return opts, nil
}

// Disguise as a Baidu client.can down all file but will be limit speed.
func (b *BaiduApi) DownFileDisguiseBaiduClient() (opts *rest.Opts, err error) {
	opts = &rest.Opts{
		Method:       "GET",
		ExtraHeaders: map[string]string{"User-Agent": "pan.baidu.com"},
	}
	return opts, nil
}

func (b *BaiduApi) CreateDir(path string, isdir bool) (opts *rest.Opts, err error) {
	data := url.Values{}
	data.Add("path", FixToBaiduPath(path))
	data.Add("isdir", BoolToStr(isdir))
	data.Add("rtype", "0")
	//todo block_list

	opts = &rest.Opts{
		Method: "POST",
		Path:   "/api/create",
		Parameters: url.Values{
			"a": []string{"commit"},
		},
		ContentType: "application/x-www-form-urlencoded",
		Body:        strings.NewReader(data.Encode()),
	}
	return opts, nil
}

func (b *BaiduApi) DeleteDirsOrFiles(fileList []string, async int) (opts *rest.Opts, err error) {
	data := url.Values{}
	data.Add("filelist", BodyList(FixToBaiduPathList(fileList)))

	opts = &rest.Opts{
		Method: "POST",
		Path:   "/api/filemanager",
		Parameters: url.Values{
			"async":     []string{strconv.Itoa(async)},
			"onnest":    []string{"fail"},
			"opera":     []string{"delete"},
			"newVerify": []string{"1"},
		},
		ContentType: "application/x-www-form-urlencoded",
		Body:        strings.NewReader(data.Encode()),
	}
	return opts, nil
}

func (b *BaiduApi) RenameDirsOrFiles(fileList []FileManagerParam, async int) (opts *rest.Opts, err error) {
	for i := range fileList {
		fileList[i].Path = FixToBaiduPath(fileList[i].Path)
	}
	fileListJson, err := json.Marshal(fileList)
	if err != nil {
		return nil, err
	}
	data := url.Values{}
	data.Add("filelist", string(fileListJson))
	opts = &rest.Opts{
		Method: "POST",
		Path:   "/api/filemanager",
		Parameters: url.Values{
			"async":     []string{strconv.Itoa(async)},
			"onnest":    []string{"fail"},
			"opera":     []string{"rename"},
			"newVerify": []string{"1"},
		},
		ContentType: "application/x-www-form-urlencoded",
		Body:        strings.NewReader(data.Encode()),
	}
	return opts, nil
}

// MoveOrCopyDirsOrFiles Moving does not modify the fsid while copying generates a new fsid
func (b *BaiduApi) MoveOrCopyDirsOrFiles(fileList []FileManagerParam, async int, operate Operate) (opts *rest.Opts, err error) {
	for i := range fileList {
		fileList[i].Path = FixToBaiduPath(fileList[i].Path)
		fileList[i].Dest = FixToBaiduPath(fileList[i].Dest)
	}
	fileListJson, err := json.Marshal(fileList)
	if err != nil {
		return nil, err
	}
	data := url.Values{}
	data.Add("filelist", string(fileListJson))
	opts = &rest.Opts{
		Method: "POST",
		Path:   "/api/filemanager",
		Parameters: url.Values{
			"async":     []string{strconv.Itoa(async)},
			"onnest":    []string{"fail"},
			"opera":     []string{string(operate)},
			"newVerify": []string{"1"},
		},
		ContentType: "application/x-www-form-urlencoded",
		Body:        strings.NewReader(data.Encode()),
	}
	return opts, nil
}

// fixToBaiduPath baidu path need to begin with "/" and use "/" to split
func FixToBaiduPath(rclonePath string) string {
	if rclonePath == "" {
		return "/"
	}
	rclonePath = strings.ReplaceAll(rclonePath, "\\", "/")
	if !strings.HasPrefix(rclonePath, "/") {
		rclonePath = "/" + rclonePath
	}
	return rclonePath
}

func FixToBaiduPathList(rclonePath []string) []string {
	for i, _ := range rclonePath {
		rclonePath[i] = FixToBaiduPath(rclonePath[i])
	}
	return rclonePath
}

// 上传
func (b *BaiduApi) precreate(path string, rapidOffsetData RapidOffsetData, preCreateFileData PreCreateFileData) (opts *rest.Opts, err error) {
	encoder := schema.NewEncoder()
	data := url.Values{}
	err = encoder.Encode(rapidOffsetData, data)
	if err != nil {
		return nil, err
	}
	err = encoder.Encode(preCreateFileData, data)
	if err != nil {
		return nil, err
	}
	data.Add("path", FixToBaiduPath(path))
	data.Add("isdir", "0")
	data.Add("autoinit", "1")
	data.Add("rtype", "1")

	opts = &rest.Opts{
		Method:      "POST",
		Path:        "/api/precreate",
		ContentType: "application/x-www-form-urlencoded",
		Body:        strings.NewReader(data.Encode()),
	}
	return opts, nil
}
func (b *BaiduApi) superfile2(path string, uploadId string, partseq int, chunk io.ReadSeeker, size int64) (opts *rest.Opts, err error) {

	opts = &rest.Opts{
		Method:        "POST",
		Path:          "/api/precreate",
		ContentLength: &size,
		Parameters: url.Values{
			"method":   []string{"upload"},
			"type":     []string{"tmpfile"},
			"path":     []string{FixToBaiduPath(path)},
			"uploadid": []string{uploadId},
			"partseq":  []string{strconv.Itoa(partseq)},
		},
		Body: chunk,
	}
	return opts, nil
}
