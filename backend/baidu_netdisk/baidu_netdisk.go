package baidu_netdisk

import (
	"context"
	"fmt"
	"github.com/rclone/rclone/fs"
	"github.com/rclone/rclone/fs/config/configmap"
	"github.com/rclone/rclone/fs/fshttp"
	"github.com/rclone/rclone/fs/hash"
	"github.com/rclone/rclone/lib/dircache"
	"github.com/rclone/rclone/lib/oauthutil"
	"github.com/rclone/rclone/lib/persistjar"
	"github.com/rclone/rclone/lib/rest"
	"golang.org/x/net/publicsuffix"
	"io"
	"net/http"
	"net/url"
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

// Register with Fs
func init() {
	fs.Register(&fs.RegInfo{
		Name:        "baidu_netdisk",
		Description: "百度网盘",
		//NewFs:       NewFs,
		Config: Config,
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
	ServerSideAcrossConfigs bool `config:"server_side_across_configs"`
}

// Fs represents a remote OneDrive
type Fs struct {
	name         string             // name of this remote
	root         string             // the path we are working on
	opt          Options            // parsed options
	ci           *fs.ConfigInfo     // global config
	features     *fs.Features       // optional features
	srv          *rest.Client       // the connection to the OneDrive server
	unAuth       *rest.Client       // no authentication connection to the OneDrive server
	dirCache     *dircache.DirCache // Map of directory path to directory id
	pacer        *fs.Pacer          // pacer for API calls
	tokenRenewer *oauthutil.Renew   // renew the token on expiry
	driveID      string             // ID to use for querying Microsoft Graph
	driveType    string             // https://developer.microsoft.com/en-us/graph/docs/api-reference/v1.0/resources/drive
	hashType     hash.Type          // type of the hash we are using
}

//// NewFs constructs an Fs from the path, container:path
//func NewFs(ctx context.Context, name, root string, m configmap.Mapper) (fs.Fs, error) {
//	// Parse config into Options struct
//	opt := new(Options)
//	err := configstruct.Set(m, opt)
//	if err != nil {
//		return nil, err
//	}
//
//	client := fshttp.NewClient(ctx)
//	cookieJar, _ := persistjar.New(&persistjar.Options{PublicSuffixList: publicsuffix.List}, m, "")
//	client.Jar = cookieJar
//
//	ci := fs.GetConfig(ctx)
//	f := &Fs{
//		name:   name,
//		root:   root,
//		opt:    *opt,
//		ci:     ci,
//		srv:    rest.NewClient(client),
//		unAuth: rest.NewClient(client),
//		pacer:  fs.NewPacer(ctx, pacer.NewDefault(pacer.MinSleep(minSleep), pacer.MaxSleep(maxSleep), pacer.DecayConstant(decayConstant))),
//	}
//	f.features = (&fs.Features{
//		CaseInsensitive:         true,
//		ReadMimeType:            true,
//		CanHaveEmptyDirectories: true,
//		ServerSideAcrossConfigs: opt.ServerSideAcrossConfigs,
//	}).Fill(ctx, f)
//	//f.srv.SetErrorHandler(errorHandler)
//
//	return f, nil
//}

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
