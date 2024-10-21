package api

import "github.com/morecup/rclone/fs"

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
	FsID          int64     `json:"fsid"`
	Path          string    `json:"path"`
	Md5           string    `json:"md5"`
	ServerMd5     string    `json:"server_md5"`
	Size          int64     `json:"size"`
	Category      int       `json:"category"`
	Status        int       `json:"status"`
	ShootTime     int64     `json:"shoot_time"`
	TaskStatus    int       `json:"task_status"`
	Ctime         int64     `json:"ctime"`
	Mtime         int64     `json:"mtime"`
	ExtStatus     int       `json:"ext_status"`
	Flag          int       `json:"flag"`
	CollectStatus int       `json:"collect_status"`
	ThumburlList  []*string `json:"thumburl"`
	ThumburlStr   string    `json:"thumburl1"`

	Errno int `json:"errno"`
	/*
		ExtentTinyint7 int   `json:"extent_tinyint7"`
		ExtentTinyint4 int   `json:"extent_tinyint4"`
		ExtentTinyint3 int64 `json:"extent_tinyint3"`
		ExtentTinyint2 int   `json:"extent_tinyint2"`
		ExtentTinyint1 int   `json:"extent_tinyint1"`

		IsDir      int    `json:"isdir"`
		Videotag   int    `json:"videotag"`
		Dlink      string `json:"dlink"`
		OperID     int64  `json:"oper_id"`
		PathMd5    int    `json:"path_md5"`
		WpFile     int    `json:"wpfile"`
		LocalMtime int64  `json:"local_mtime"`
		Share      int    `json:"share"`
		FileKey    string `json:"file_key"`
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
		ServerCtime  int64  `json:"server_ctime"`
		Lodocpreview string `json:"lodocpreview"`
		OwnerID      int    `json:"owner_id"`
		TkbindID     int    `json:"tkbind_id"`

		ExtentInt3 int64 `json:"extent_int3"`

		FromType       int    `json:"from_type"`
		ServerFilename string `json:"server_filename"`
		// 服务器文件修改时间，包括重命名
		ServerMtime int64 `json:"server_mtime"`
		Pl          int   `json:"pl"`

		DirEmpty    int `json:"dir_empty"`
		Empty       int `json:"empty"`
		ServerAtime int `json:"server_atime"`
		Unlist      int `json:"unlist"`

	*/
}

type InfoResponse struct {
	BaiduResponse
	Info []*Item `json:"info"`
}

type ListResponse struct {
	BaseBaiduResponse
	HasMore int32   `json:"has_more"`
	Cursor  string  `json:"cursor"`
	List    []*Item `json:"list"`
}

type UserInfoResponse struct {
	BaseBaiduResponse
	*UserInfo
}

type UserInfo struct {
	YouaId           string `json:"youa_id"`
	Nickname         string `json:"nickname"`
	Photo            string `json:"photo"`
	Ctime            int64  `json:"ctime"`
	SetNickname      int32  `json:"set_nickname"`
	IsAdmin          int32  `json:"is_admin"`
	PrivateStatus    int32  `json:"private_status"`
	SupportRaw       int32  `json:"support_raw"`
	UsedInfiniteCode string `json:"used_infinite_code"`
	IsDefNickname    int32  `json:"is_def_nickname"`
	IsNew            int32  `json:"is_new"`
	IsNewOfCurApp    int32  `json:"is_new_of_cur_app"`
	BdUn             string `json:"bd_un"`
	UserAuthState    int32  `json:"user_auth_state"`
}

type QuotaInfoResponse struct {
	BaseBaiduResponse
	Quota     int64 `json:"quota"`
	Used      int64 `json:"used"`
	IsUnlimit int   `json:"is_unlimit"`
}

type FileManagerParam struct {
	Path    string `json:"path,omitempty"`
	NewName string `json:"newname,omitempty"`
	Dest    string `json:"dest,omitempty"`
	Ondup   string `json:"ondup,omitempty"`
}

type RapidOffsetData struct {
	DataTime int64 `schema:"data_time,omitempty"`
	//DataLength  int    `schema:"data_length,omitempty"`
	DataOffset  int64  `schema:"data_offset,omitempty"`
	DataContent string `schema:"data_content,omitempty"`
}

type PreCreateFileData struct {
	Size       int64    `schema:"size"`
	BlockList  []string `schema:"-"`
	ContentMd5 string   `schema:"content-md5"`
	SliceMd5   string   `schema:"slice-md5"`
	LocalCtime int64    `schema:"local_ctime,omitempty"`
	LocalMtime int64    `schema:"local_mtime,omitempty"`
}

type FragmentVO struct {
	Md5       string `json:"md5"`
	Partseq   string `json:"partseq"`
	RequestId int64  `json:"request_id"`
	UploadId  int32  `json:"uploadid"`
}

func (b FragmentVO) GetErrno() int {
	return 0
}

type PreCreateVO struct {
	BaseBaiduResponse
	Path       string           `json:"path"` //return_type为1时 才会有这一项
	ReturnType int              `json:"return_type"`
	BlockList  []fs.StringValue `json:"block_list"` //return_type为1时 才会有这一项
	Data       *BaseItem        `json:"data"`       //return_type为2时 才会有这一项
	UploadId   string           `json:"uploadid"`   //return_type为1时 才会有这一项
}

type CreateVO struct {
	BaseBaiduResponse
	ReturnType int       `json:"return_type"` //不可靠，可能没有这个字段
	Data       *BaseItem `json:"data"`
}

type BaseItem struct {
	FsID           int64  `json:"fs_id"`
	Path           string `json:"path"`
	Md5            string `json:"md5"`
	FromType       int32  `json:"from_type"`
	ServerMd5      string `json:"server_md5"`
	Size           int64  `json:"size"`
	Category       int    `json:"category"`
	ShootTime      int64  `json:"shoot_time"`
	Ctime          int64  `json:"ctime"`
	Mtime          int64  `json:"mtime"`
	Errno          int32  `json:"errno"`
	IsDir          int32  `json:"isdir"`
	ServerFilename string `json:"server_filename"`
}

type DownloadVO struct {
	BaseBaiduResponse
	Dlink string `json:"dlink"`
}
