package api

import "github.com/rclone/rclone/fs"

type BaseBaiduResponse struct {
	Errno     int   `json:"errno"`
	RequestId int64 `json:"request_id"`
}

type BaiduResponse struct {
	BaseBaiduResponse
	GuidInf string `json:"guid_inf"`
	Guid    int64  `json:"guid"`
}

func (b BaseBaiduResponse) GetErrno() int {
	return b.Errno
}

type Item struct {
	ExtentTinyint7 int    `json:"extent_tinyint7"`
	ExtentTinyint4 int    `json:"extent_tinyint4"`
	ExtentTinyint3 int64  `json:"extent_tinyint3"`
	ExtentTinyint2 int    `json:"extent_tinyint2"`
	ExtentTinyint1 int    `json:"extent_tinyint1"`
	Category       int    `json:"category"`
	IsDir          int    `json:"isdir"`
	Videotag       int    `json:"videotag"`
	Dlink          string `json:"dlink"`
	OperID         int64  `json:"oper_id"`
	PathMd5        int    `json:"path_md5"`
	WpFile         int    `json:"wpfile"`
	LocalMtime     int64  `json:"local_mtime"`
	Share          int    `json:"share"`
	FileKey        string `json:"file_key"`
	Errno          int    `json:"errno"`
	// 本地文件创建时间
	LocalCtime    int64  `json:"local_ctime"`
	OwnerType     int    `json:"owner_type"`
	Privacy       int    `json:"privacy"`
	RealCategory  string `json:"real_category"`
	Picdocpreview string `json:"picdocpreview"`
	Thumbs        struct {
		Url3 string `json:"url3"`
		Url2 string `json:"url2"`
		Url1 string `json:"url1"`
		Icon string `json:"icon"`
	} `json:"thumbs"`
	Docpreview string `json:"docpreview"`
	// 服务器文件创建时间
	ServerCtime    int64  `json:"server_ctime"`
	Lodocpreview   string `json:"lodocpreview"`
	OwnerID        int    `json:"owner_id"`
	TkbindID       int    `json:"tkbind_id"`
	Size           int64  `json:"size"`
	FsID           int    `json:"fs_id"`
	Md5            string `json:"md5"`
	ExtentInt3     int64  `json:"extent_int3"`
	Path           string `json:"path"`
	FromType       int    `json:"from_type"`
	ServerFilename string `json:"server_filename"`
	// 服务器文件修改时间，包括重命名
	ServerMtime int64 `json:"server_mtime"`
	Pl          int   `json:"pl"`

	DirEmpty    int `json:"dir_empty"`
	Empty       int `json:"empty"`
	ServerAtime int `json:"server_atime"`
	Unlist      int `json:"unlist"`
}

type InfoResponse struct {
	BaiduResponse
	Info []*Item `json:"info"`
}

type ListResponse struct {
	BaiduResponse
	List []*Item `json:"list"`
}

type TemplateResponse struct {
	BaiduResponse
	Result *TemplateInfo `json:"result"`
}

type TemplateInfo struct {
	Bdstoken   string `json:"bdstoken"`
	Token      string `json:"token"`
	Uk         int64  `json:"uk"`
	IsDocuser  int32  `json:"isdocuser"`
	ServerTime int64  `json:"servertime"`
}

type FileManagerParam struct {
	Path    string `json:"path,omitempty"`
	NewName string `json:"newname,omitempty"`
	Dest    string `json:"dest,omitempty"`
	Ondup   string `json:"ondup,omitempty"`
}

type RapidOffsetData struct {
	DataTime int64 `schema:"data_time"`
	//DataLength  int    `schema:"data_length"`
	DataOffset  int64  `schema:"data_offset"`
	DataContent string `schema:"data_content"`
}

type PreCreateFileData struct {
	Size       int64    `schema:"size"`
	BlockList  []string `schema:"-"`
	ContentMd5 string   `schema:"content-md5"`
	SliceMd5   string   `schema:"slice-md5"`
	LocalCtime int64    `schema:"local_ctime"`
	LocalMtime int64    `schema:"local_mtime"`
}

type FragmentDTO struct {
	Md5       string `json:"md5"`
	Partseq   string `json:"partseq"`
	RequestId int64  `json:"request_id"`
	UploadId  int32  `json:"uploadid"`
}

func (b FragmentDTO) GetErrno() int {
	return 0
}

type PreCreateDTO struct {
	BaseBaiduResponse
	Path       string           `json:"path"` //return_type为1时 才会有这一项
	ReturnType int              `json:"return_type"`
	BlockList  []fs.StringValue `json:"block_list"` //return_type为1时 才会有这一项
	Info       Item             `json:"info"`       //return_type为2时 才会有这一项
	UploadId   string           `json:"uploadid"`   //return_type为1时 才会有这一项
}

type CreateDTO struct {
	BaseBaiduResponse
	Ctime    int    `json:"ctime"`
	FromType int    `json:"from_type"`
	FsId     int64  `json:"fs_id"`
	Isdir    int    `json:"isdir"`
	Md5      string `json:"md5"`
	Mtime    int    `json:"mtime"`
	Path     string `json:"path"`
	Size     int    `json:"size"`
	Name     string `json:"name"`
	Category int    `json:"category"`
}
