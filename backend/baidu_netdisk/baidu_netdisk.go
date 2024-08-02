package baidu_netdisk

import (
	"context"
	"crypto/tls"
	"fmt"
	"github.com/pkg/errors"
	"github.com/rclone/rclone/backend/baidu_netdisk/api"
	"github.com/rclone/rclone/fs"
	"github.com/rclone/rclone/fs/config/configmap"
	"github.com/rclone/rclone/fs/config/configstruct"
	"github.com/rclone/rclone/fs/config/obscure"
	"github.com/rclone/rclone/fs/fshttp"
	"github.com/rclone/rclone/fs/hash"
	"github.com/rclone/rclone/lib/dircache"
	"github.com/rclone/rclone/lib/encoder"
	"github.com/rclone/rclone/lib/oauthutil"
	"github.com/rclone/rclone/lib/pacer"
	"github.com/rclone/rclone/lib/persistjar"
	"github.com/rclone/rclone/lib/rest"
	"golang.org/x/net/publicsuffix"
	"golang.org/x/oauth2"
	"io"
	"net/http"
	"net/url"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	rcloneClientID              = "b15665d9-eda6-4092-8539-0eec376afd59"
	rcloneEncryptedClientSecret = "_JUdzh3LnKNqSPcf4Wu5fgMFIQOI8glZu_akYgR8yf6egowNBg-R"
	minSleep                    = 10 * time.Millisecond
	maxSleep                    = 2 * time.Second
	decayConstant               = 2 // bigger for slower decay, exponential
	configDriveID               = "drive_id"
	configDriveType             = "drive_type"
	driveTypePersonal           = "personal"
	driveTypeBusiness           = "business"
	driveTypeSharepoint         = "documentLibrary"
	defaultChunkSize            = 10 * fs.Mebi
	chunkSizeMultiple           = 320 * fs.Kibi

	regionGlobal = "global"
	regionUS     = "us"
	regionDE     = "de"
	regionCN     = "cn"
)

// Globals
var (
	authPath  = "/common/oauth2/v2.0/authorize"
	tokenPath = "/common/oauth2/v2.0/token"

	scopeAccess             = fs.SpaceSepList{"Files.Read", "Files.ReadWrite", "Files.Read.All", "Files.ReadWrite.All", "Sites.Read.All", "offline_access"}
	scopeAccessWithoutSites = fs.SpaceSepList{"Files.Read", "Files.ReadWrite", "Files.Read.All", "Files.ReadWrite.All", "offline_access"}

	// Description of how to auth for this app for a business account
	oauthConfig = &oauth2.Config{
		Scopes:       scopeAccess,
		ClientID:     rcloneClientID,
		ClientSecret: obscure.MustReveal(rcloneEncryptedClientSecret),
		RedirectURL:  oauthutil.RedirectLocalhostURL,
	}

	graphAPIEndpoint = map[string]string{
		"global": "https://graph.microsoft.com",
		"us":     "https://graph.microsoft.us",
		"de":     "https://graph.microsoft.de",
		"cn":     "https://microsoftgraph.chinacloudapi.cn",
	}

	authEndpoint = map[string]string{
		"global": "https://login.microsoftonline.com",
		"us":     "https://login.microsoftonline.us",
		"de":     "https://login.microsoftonline.de",
		"cn":     "https://login.chinacloudapi.cn",
	}

	// QuickXorHashType is the hash.Type for OneDrive
	QuickXorHashType hash.Type
)

// Register with Fs
func init() {
	fs.Register(&fs.RegInfo{
		Name:        "baidu_netdisk",
		Description: "百度网盘",
		NewFs:       NewFs,
		Config:      Config,
		Options: []fs.Option{{
			Name:      "BDUSS",
			Help:      "cookie BDUSS",
			Default:   "",
			Advanced:  false,
			Sensitive: true,
		}, {
			Name:      "PTOKEN",
			Help:      "cookie PTOKEN",
			Default:   "",
			Advanced:  false,
			Sensitive: true,
		}, {
			Name: "chunk_size",
			Help: `Chunk size to upload files with - must be multiple of 320k (327,680 bytes).

Above this size files will be chunked - must be multiple of 320k (327,680 bytes) and
should not exceed 250M (262,144,000 bytes) else you may encounter \"Microsoft.SharePoint.Client.InvalidClientQueryException: The request message is too big.\"
Note that the chunks will be buffered into memory.`,
			Default:  defaultChunkSize,
			Advanced: true,
		}},
	})
}
func Config(ctx context.Context, name string, m configmap.Mapper, config fs.ConfigIn) (*fs.ConfigOut, error) {
	bduss, _ := m.Get("BDUSS")
	ptoken, _ := m.Get("PTOKEN")
	fmt.Print(bduss, ptoken)
	client := fshttp.NewClient(ctx)
	cookieJar, _ := persistjar.New(&persistjar.Options{PublicSuffixList: publicsuffix.List}, m, "")

	cookieURL, _ := url.Parse("https://passport.baidu.com")
	var cookies = []*http.Cookie{
		{Name: "BDUSS", Value: bduss, Domain: ".baidu.com", Path: "/"},
		{Name: "PTOKEN", Value: ptoken, Domain: ".passport.baidu.com", Path: "/"},
	}
	// 这里我们模拟了一个HTTP请求响应的过程，直接将一个cookie添加到cookieJar中
	cookieJar.SetCookies(cookieURL, cookies)
	client.Jar = cookieJar

	resp, _ := client.Get("https://pan.baidu.com/login")
	defer resp.Body.Close()
	if strings.Contains(resp.Request.URL.String(), "https://pan.baidu.com/disk/main") {
		// 读取响应体
		bodyBytes, _ := io.ReadAll(resp.Body)
		bodyString := string(bodyBytes)
		fmt.Println(bodyString)
		nowCookies := cookieJar.Cookies(cookieURL)
		for _, cookie := range nowCookies {
			if cookie.Name == "BDUSS" {
				m.Set("BDUSS", cookie.Value)
			} else if cookie.Name == "PTOKEN" {
				m.Set("PTOKEN", cookie.Value)
			} else if cookie.Name == "BAIDUID" {
				m.Set("BAIDUID", cookie.Value)
			}
		}
		fmt.Println(" baidu pan login success!")
	} else {
		fmt.Println(" baidu pan login fail! you may be needed to edit again!!")
	}
	fmt.Println()
	return nil, nil
}

// Options defines the configuration for this backend
type Options struct {
	Region                  string               `config:"region"`
	ChunkSize               fs.SizeSuffix        `config:"chunk_size"`
	UserID                  int64                `config:"drive_id"`
	VipType                 string               `config:"drive_type"`
	RootFolderID            string               `config:"root_folder_id"`
	DisableSitePermission   bool                 `config:"disable_site_permission"`
	AccessScopes            fs.SpaceSepList      `config:"access_scopes"`
	ExposeOneNoteFiles      bool                 `config:"expose_onenote_files"`
	ServerSideAcrossConfigs bool                 `config:"server_side_across_configs"`
	ListChunk               int64                `config:"list_chunk"`
	NoVersions              bool                 `config:"no_versions"`
	LinkScope               string               `config:"link_scope"`
	LinkType                string               `config:"link_type"`
	LinkPassword            string               `config:"link_password"`
	HashType                string               `config:"hash_type"`
	AVOverride              bool                 `config:"av_override"`
	Delta                   bool                 `config:"delta"`
	Enc                     encoder.MultiEncoder `config:"encoding"`
}

// Fs represents a remote OneDrive
type Fs struct {
	name         string             // name of this remote
	root         string             // the path we are working on
	opt          Options            // parsed options
	ci           *fs.ConfigInfo     // global config
	features     *fs.Features       // optional features
	srv          *BaiduClient       // the connection to the OneDrive server
	unAuth       *rest.Client       // no authentication connection to the OneDrive server
	dirCache     *dircache.DirCache // Map of directory path to directory id
	pacer        *fs.Pacer          // pacer for API calls
	tokenRenewer *oauthutil.Renew   // renew the token on expiry
	UserId       int64              // ID to use for querying Microsoft Graph
	VipType      string             // https://developer.microsoft.com/en-us/graph/docs/api-reference/v1.0/resources/drive
	hashType     hash.Type          // type of the hash we are using
	api          *api.BaiduApi
	uk           string
}

func checkUploadChunkSize(cs fs.SizeSuffix) error {
	const minChunkSize = fs.SizeSuffixBase
	if cs%chunkSizeMultiple != 0 {
		return fmt.Errorf("%s is not a multiple of %s", cs, chunkSizeMultiple)
	}
	if cs < minChunkSize {
		return fmt.Errorf("%s is less than %s", cs, minChunkSize)
	}
	return nil
}

// errorHandler parses a non 2xx error response into an error
func errorHandler(resp *http.Response) error {
	//// Decode error response
	//errResponse := new(api.Error)
	//err := rest.DecodeJSON(resp, &errResponse)
	//if err != nil {
	//	fs.Debugf(nil, "Couldn't decode error response: %v", err)
	//}
	//if errResponse.ErrorInfo.Code == "" {
	//	errResponse.ErrorInfo.Code = resp.Status
	//}
	//return errResponse
	return fmt.Errorf("error response %v", resp)
}

// parsePath parses a OneDrive 'url'
func parsePath(path string) (root string) {
	root = strings.Trim(path, "/")
	return
}

// NewFs root is linux path ,maybe "" "/" "/1/2" is not be "\1" "\\1" .root may be file path. need to fix
func NewFs(ctx context.Context, name, root string, m configmap.Mapper) (fs.Fs, error) {
	// Parse config into Options struct
	opt := new(Options)
	err := configstruct.Set(m, opt)
	if err != nil {
		return nil, err
	}

	err = checkUploadChunkSize(opt.ChunkSize)
	if err != nil {
		return nil, fmt.Errorf("onedrive: chunk size: %w", err)
	}

	//if opt.DriveID == "" || opt.DriveType == "" {
	//	return nil, errors.New("unable to get drive_id and drive_type - if you are upgrading from older versions of rclone, please run `rclone config` and re-configure this backend")
	//}

	//rootURL := graphAPIEndpoint[opt.Region] + "/v1.0" + "/drives/" + opt.DriveID
	rootURL := "https://pan.baidu.com"
	//oauthConfig.Scopes = opt.AccessScopes
	//if opt.DisableSitePermission {
	//	oauthConfig.Scopes = scopeAccessWithoutSites
	//}
	//oauthConfig.Endpoint = oauth2.Endpoint{
	//	AuthURL:  authEndpoint[opt.Region] + authPath,
	//	TokenURL: authEndpoint[opt.Region] + tokenPath,
	//}

	client := fshttp.NewClient(ctx)
	cookieJar, _ := persistjar.New(&persistjar.Options{PublicSuffixList: publicsuffix.List}, m, "")
	client.Jar = cookieJar
	root = parsePath(root)
	//oAuthClient, ts, err := oauthutil.NewClientWithBaseClient(ctx, name, m, oauthConfig, client)
	//if err != nil {
	//	return nil, fmt.Errorf("failed to configure OneDrive: %w", err)
	//}

	ci := fs.GetConfig(ctx)
	value, ok := m.Get("BAIDUID")
	if !ok {
		value = ""
	}

	transport := client.Transport.(*fshttp.Transport)
	//netdisk;7.0.1.1;PC;PC-Windows;10.0.22621;WindowsBaiduYunGuanJia
	//netdisk;12.8.1;23043RP34C;android-android;13;JSbridge4.4.0;jointBridge;1.1.0;
	transport.SetUserAgent("netdisk;7.0.1.1;PC;PC-Windows;10.0.22621;WindowsBaiduYunGuanJia")
	//transport.SetUserAgent("netdisk;12.8.1;23043RP34C;android-android;13;JSbridge4.4.0;jointBridge;1.1.0;")
	transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	f := &Fs{
		name:     name,
		root:     root,
		opt:      *opt,
		ci:       ci,
		UserId:   opt.UserID,
		VipType:  opt.VipType,
		srv:      NewBaiduClient(rest.NewClient(client).SetRoot(rootURL), value),
		unAuth:   rest.NewClient(client),
		pacer:    fs.NewPacer(ctx, pacer.NewDefault(pacer.MinSleep(minSleep), pacer.MaxSleep(maxSleep), pacer.DecayConstant(decayConstant))),
		hashType: QuickXorHashType,
		api:      &api.BaiduApi{},
	}
	f.features = (&fs.Features{
		CaseInsensitive:         false,
		ReadMimeType:            true,
		CanHaveEmptyDirectories: true,
		ServerSideAcrossConfigs: opt.ServerSideAcrossConfigs,
	}).Fill(ctx, f)
	f.srv.Client.SetErrorHandler(errorHandler)

	templateInfo, err := f.GetTemplateVariable(ctx)

	if err != nil {
		return nil, err
	}
	f.srv.Bdstoken = templateInfo.Bdstoken
	f.UserId = templateInfo.Uk
	m.Set("UserId", strconv.FormatInt(f.UserId, 10))

	// Set the user defined hash
	if opt.HashType == "auto" || opt.HashType == "" {
		opt.HashType = QuickXorHashType.String()
	}
	err = f.hashType.Set(opt.HashType)
	if err != nil {
		return nil, err
	}

	// Disable change polling in China region
	// See: https://github.com/rclone/rclone/issues/6444
	if f.opt.Region == regionCN {
		f.features.ChangeNotify = nil
	}

	//// Renew the token in the background
	//f.tokenRenewer = oauthutil.NewRenew(f.String(), ts, func() error {
	//	_, _, err := f.readMetaDataForPath(ctx, "")
	//	return err
	//})

	//// Get rootID
	//var rootID = opt.RootFolderID
	//if rootID == "" {
	//	rootInfo, _, err := f.GetFileMeta(ctx, root, false, false)
	//	if err != nil {
	//		return nil, fmt.Errorf("failed to get root: %w", err)
	//	}
	//	rootID = strconv.Itoa(rootInfo.FsID)
	//}
	//if rootID == "" {
	//	return nil, errors.New("failed to get root: ID was empty")
	//}
	//f.dirCache = dircache.New(root, rootID, f)

	//if root is file path,fix root to dir path
	item, _, err := f.GetFileMeta(ctx, "/"+f.root, false, false)
	if err != nil {
		return nil, err
	}
	if item.IsDir == 0 {
		dir, _ := SplitPath(f.root)
		item, _, err = f.GetFileMeta(ctx, "/"+dir, false, false)
		if err != nil {
			return nil, err
		}
		if item.IsDir == 0 {
			return nil, fmt.Errorf("path is not a right path.root:(%s)", f.root)
		} else {
			f.root = dir
			return f, fs.ErrorIsFile
		}
	}
	return f, nil
}

// Name of the remote (as passed into NewFs)
func (f *Fs) Name() string {
	return f.name
}

// Root of the remote (as passed into NewFs)
func (f *Fs) Root() string {
	return f.root
}

// String converts this Fs to a string
func (f *Fs) String() string {
	return fmt.Sprintf("OneDrive root '%s'", f.root)
}

// Features returns the optional features of this Fs
func (f *Fs) Features() *fs.Features {
	return f.features
}

// Hashes returns the supported hash sets.
func (f *Fs) Hashes() hash.Set {
	return hash.Set(f.hashType)
}

// Precision return the precision of this Fs
func (f *Fs) Precision() time.Duration {
	return time.Second
}

func (f *Fs) FindLeaf(ctx context.Context, pathID, leaf string) (pathIDOut string, found bool, err error) {
	//TODO implement me
	panic("implement me")
}

func (f *Fs) CreateDir(ctx context.Context, pathID, leaf string) (newID string, err error) {
	//TODO implement me
	panic("implement me")
}

// List entries normal need to implement fs.Directory or fs.Object ,dir is relative path.
func (f *Fs) List(ctx context.Context, dir string) (entries fs.DirEntries, err error) {
	absolutePath := f.ToAbsolutePath(dir)
	itemList, err := f.ListDirAllFiles(ctx, absolutePath)
	if err != nil {
		return nil, err
	}
	for _, item := range itemList {
		entry, err := f.itemToDirOrObject(ctx, dir, item)
		if err != nil {
			return nil, err
		}
		entries = append(entries, entry)
	}
	return entries, nil
}

// NewObject finds the Object at remote.  If it can't be found
// it returns the error fs.ErrorObjectNotFound.
func (f *Fs) NewObject(ctx context.Context, remote string) (fs.Object, error) {
	item, _, err := f.GetFileMeta(ctx, f.ToAbsolutePath(remote), true, true)
	if err != nil {
		return nil, err
	}
	entry, err := f.itemToDirOrObject(ctx, "", item)
	if err != nil {
		return nil, err
	}
	object, ok := entry.(*Object)
	if !ok {
		return nil, errors.WithStack(fs.ErrorObjectNotFound)
	}
	return object, nil
}

// Put in to the remote path with the modTime given of the given size
//
// When called from outside an Fs by rclone, src.Size() will always be >= 0.
// But for unknown-sized objects (indicated by src.Size() == -1), Put should either
// return an error or upload it properly (rather than e.g. calling panic).
//
// May create the object even if it returns an error - if so
// will return the object and the error, otherwise will return
// nil and the error
func (f *Fs) Put(ctx context.Context, in io.Reader, src fs.ObjectInfo, options ...fs.OpenOption) (fs.Object, error) {
	remote := src.Remote()
	size := src.Size()
	modTime := src.ModTime(ctx)

	o := f.buildObject(ctx, remote, modTime, size)

	return o, o.Update(ctx, in, src, options...)
}

func (f *Fs) buildObject(ctx context.Context, remote string, modTime time.Time, size int64) (o *Object) {
	return &Object{
		fs:            f,
		remote:        remote,
		hasMetaData:   true,
		isOneNoteFile: false,
		size:          size,
		modTime:       modTime,
		id:            "",
		hash:          "md5",
		mimeType:      "json",
	}
}

func (f *Fs) Mkdir(ctx context.Context, dir string) error {
	err := f.CreateDirForce(ctx, f.ToAbsolutePath(dir))
	return err
}

func (f *Fs) Rmdir(ctx context.Context, dir string) error {
	err := f.DeleteDirOrFile(ctx, f.ToAbsolutePath(dir))
	return err
}

// Move src to this remote using server-side move operations.
//
// This is stored with the remote path given.
//
// It returns the destination Object and a possible error.
//
// Will only be called if src.Fs().Name() == f.Name()
//
// If it isn't possible then return fs.ErrorCantMove
func (f *Fs) Move(ctx context.Context, src fs.Object, remote string) (fs.Object, error) {
	srcObj, ok := src.(*Object)
	if ok {
		//need to sure same account
		if srcObj.fs.UserId == 0 || f.UserId == 0 || srcObj.fs.UserId != f.UserId {
			fs.Debugf(f, "Can't move files between drives (%q != %q)", srcObj.fs.UserId, srcObj.fs.UserId)
			return nil, fs.ErrorCantMove
		}
		//now is same account
		scrAbsolutePath := srcObj.fs.ToAbsolutePath(srcObj.remote)
		srcParentFile, _ := SplitPath(scrAbsolutePath)
		dstParentFile, dstDirName := SplitPath(f.ToAbsolutePath(remote))
		if srcParentFile == dstParentFile {
			// need to rename
			err := f.RenameDirOrFile(ctx, api.FileManagerParam{Path: scrAbsolutePath, NewName: dstDirName})
			if err != nil {
				return nil, err
			}
			srcObj.remote = remote
			return srcObj, nil
		} else {
			fileManagerParam := api.FileManagerParam{
				Path:    scrAbsolutePath,
				Dest:    dstParentFile,
				NewName: dstDirName,
			}
			err := f.MoveOrCopyDirOrFile(ctx, fileManagerParam, api.MoveOperate)
			if err != nil {
				return nil, err
			}
			srcObj.remote = remote
			return srcObj, nil
		}
	} else {
		fs.Debugf(src, "Can't move - not same remote type")
		return nil, fs.ErrorCantMove
	}
}

// DirMove moves src, srcRemote to this remote at dstRemote
// using server-side move operations.
//
// Will only be called if src.Fs().Name() == f.Name()
//
// If it isn't possible then return fs.ErrorCantDirMove
//
// # If destination exists then return fs.ErrorDirExists
//
// srcRemote is absolute path,dstRemote is absolute path,can not end with "/"
func (f *Fs) DirMove(ctx context.Context, src fs.Fs, srcRemote, dstRemote string) error {
	srcFs, ok := src.(*Fs)
	if ok {
		//need to sure same account
		if srcFs.UserId == 0 || f.UserId == 0 || srcFs.UserId != f.UserId {
			fs.Debugf(f, "Can't move files between drives (%q != %q)", srcFs.UserId, f.UserId)
			return fs.ErrorCantDirMove
		}
		srcAbsolutePath := srcFs.ToAbsolutePath(srcRemote)
		srcParentDir, _ := SplitPath(srcAbsolutePath)
		dstParentDir, dstDirName := SplitPath(f.ToAbsolutePath(dstRemote))
		if srcParentDir == dstParentDir {
			// need to rename
			err := f.RenameDirOrFile(ctx, api.FileManagerParam{Path: srcAbsolutePath, NewName: dstDirName})
			if err != nil {
				return err
			}
		} else {
			fileManagerParam := api.FileManagerParam{
				Path:    srcAbsolutePath,
				Dest:    dstParentDir,
				NewName: dstDirName,
			}
			err := f.MoveOrCopyDirOrFile(ctx, fileManagerParam, api.MoveOperate)
			if err != nil {
				return err
			}
			return nil
		}
		return nil
	} else {
		fs.Debugf(srcFs, "Can't move directory - not same remote type")
		return fs.ErrorCantDirMove
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
	//need to sure same account
	if srcObj.fs.UserId == 0 || f.UserId == 0 || srcObj.fs.UserId != f.UserId {
		fs.Debugf(f, "Can't move files between drives (%q != %q)", srcObj.fs.UserId, srcObj.fs.UserId)
		return nil, fs.ErrorCantMove
	}
	scrAbsolutePath := srcObj.fs.ToAbsolutePath(srcObj.remote)
	//srcParentFile, _ := path.Split(scrAbsolutePath)
	dstParentFile, dstDirName := SplitPath(f.ToAbsolutePath(remote))
	fileManagerParam := api.FileManagerParam{
		Path:    scrAbsolutePath,
		Dest:    dstParentFile,
		NewName: dstDirName,
	}
	err := f.MoveOrCopyDirOrFile(ctx, fileManagerParam, api.CopyOperate)
	if err != nil {
		return nil, err
	}
	srcObj.remote = remote
	return srcObj, nil
}

// Convert a list item into fs.Directory or fs.Object
func (f *Fs) itemToDirOrObject(ctx context.Context, dir string, info *api.Item) (entry fs.DirEntry, err error) {
	if dir != "" {
		dir = dir + "/"
	}
	if info.IsDir == 1 {
		entry = fs.NewDir(dir+info.ServerFilename, time.Unix(info.LocalMtime, 0)).SetID(strconv.Itoa(info.FsID)).SetItems(-1).SetSize(-1)
	} else if info.IsDir == 0 {
		entry = &Object{
			fs:            f,
			remote:        dir + info.ServerFilename,
			hasMetaData:   true,
			isOneNoteFile: false,
			size:          info.Size,
			modTime:       time.Unix(info.LocalMtime, 0),
			id:            strconv.Itoa(info.FsID),
			hash:          "md5",
			mimeType:      "json",
		}
	}

	return entry, nil
}

func (f *Fs) ToAbsolutePath(relativePath string) string {
	return strings.ReplaceAll(filepath.Join(f.root, relativePath), "\\", "/")
}
func (f *Fs) ToAbsoluteFilePath(relativePath string, fileName string) string {
	return strings.ReplaceAll(filepath.Join(f.root, relativePath, fileName), "\\", "/")
}
func (f *Fs) ToRelativeFilePath(relativePath string, fileName string) string {
	if relativePath != "" {
		relativePath = relativePath + "/"
	}
	return relativePath + fileName
}
func SplitPath(pathStr string) (dir, file string) {
	dir, file = path.Split(pathStr)
	if dir == "" {
		return "", file
	} else {
		return strings.TrimRight(dir, "/"), file
	}
}
