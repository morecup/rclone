package api

import (
	"bytes"
	"encoding/json"
	"github.com/gorilla/schema"
	"github.com/rclone/rclone/fs"
	"github.com/rclone/rclone/lib/rest"
	"io"
	"math/rand"
	"net/url"
	"strconv"
	"strings"
	"time"
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
func (b *BaiduApi) ListDirFiles(cursor string) (opts *rest.Opts, err error) {

	opts = &rest.Opts{
		Method: "GET",
		Path:   "/youai/file/v1/list",
		Parameters: url.Values{
			"need_thumbnail":     []string{"1"},
			"need_filter_hidden": []string{"0"},
			"cursor":             []string{cursor},
		},
	}
	return opts, nil
}

func (b *BaiduApi) GetUserInfo() (opts *rest.Opts, err error) {
	opts = &rest.Opts{
		Method:     "POST",
		Path:       "/youai/user/v1/getuinfo",
		Parameters: url.Values{
			//"fields": []string{"[\"bdstoken\",\"token\",\"uk\",\"isdocuser\",\"servertime\"]"},
		},
	}
	return opts, nil
}

// Disguise as a Baidu client.can down all file but will be limit speed.
func (b *BaiduApi) DownFileDisguiseBaiduClient(dLink string) (opts *rest.Opts, err error) {
	opts = &rest.Opts{
		Method:  "GET",
		RootURL: dLink,
		//ExtraHeaders: map[string]string{"User-Agent": "pan.baidu.com"},
	}
	return opts, nil
}

// Disguise as a Baidu client.can down all file but will be limit speed.
func (b *BaiduApi) GetLocateDownloadUrl(path string) (opts *rest.Opts, err error) {
	opts = &rest.Opts{
		Method:  "POST",
		RootURL: "https://d.pcs.baidu.com",
		Path:    "/rest/2.0/pcs/file",
		Parameters: url.Values{
			"method": []string{"locatedownload"},
			"path":   []string{FixToBaiduPath(path)},
		},
		ContentType: "application/x-www-form-urlencoded",
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
func (b *BaiduApi) Precreate(path string, rapidOffsetData *RapidOffsetData, preCreateFileData *PreCreateFileData) (opts *rest.Opts, err error) {
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
	if preCreateFileData.BlockList != nil {
		data.Set("block_list", BodyList(preCreateFileData.BlockList))
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
func generateRandomNumberString() string {
	rand.Seed(time.Now().UnixNano())

	firstNum := rand.Intn(9) + 1 // 生成一个 1 到 9 的随机整数
	randomString := strconv.Itoa(firstNum)

	for i := 0; i < 28; i++ {
		randomString += strconv.Itoa(rand.Intn(10)) // 生成一个 0 到 9 的随机整数
	}

	return randomString
}
func (b *BaiduApi) Superfile2(path string, uploadId string, partseq int, realChunkSize int64, chunk io.Reader, options ...fs.OpenOption) (opts *rest.Opts, err error) {
	randNumber := generateRandomNumberString()
	beginByte := []byte("-----------------------------" + randNumber + "\r\nContent-Disposition: form-data; name=\"file\"; filename=\"blob\"\r\nContent-Type: application/octet-stream\r\n\r\n")
	endByte := []byte("\r\n-----------------------------" + randNumber + "--\r\n")
	bodyLen := int64(len(beginByte)) + realChunkSize + int64(len(endByte))
	opts = &rest.Opts{
		Method:  "POST",
		RootURL: "https://d.pcs.baidu.com/rest/2.0/pcs/superfile2",
		Parameters: url.Values{
			"method":   []string{"upload"},
			"type":     []string{"tmpfile"},
			"path":     []string{FixToBaiduPath(path)},
			"uploadid": []string{uploadId},
			"partseq":  []string{strconv.Itoa(partseq)},
		},
		Body: io.MultiReader(
			bytes.NewReader(beginByte),
			chunk,
			bytes.NewReader(endByte)),
		Options:     options,
		ContentType: "multipart/form-data; boundary=---------------------------" + randNumber,
		//TransferEncoding: []string{"identity"},
		ContentLength: &bodyLen,
	}
	return opts, nil
}

// 上传
func (b *BaiduApi) Create(path string, preCreateFileData *PreCreateFileData, uploadId string) (opts *rest.Opts, err error) {
	encoder := schema.NewEncoder()
	data := url.Values{}
	err = encoder.Encode(preCreateFileData, data)
	if err != nil {
		return nil, err
	}
	if preCreateFileData.BlockList != nil {
		data.Set("block_list", BodyList(preCreateFileData.BlockList))
	}
	data.Add("path", FixToBaiduPath(path))
	data.Add("isdir", "0")
	data.Add("uploadid", uploadId)
	data.Add("rtype", "1")

	opts = &rest.Opts{
		Method:      "POST",
		Path:        "/api/create",
		ContentType: "application/x-www-form-urlencoded",
		Body:        strings.NewReader(data.Encode()),
	}
	return opts, nil
}
