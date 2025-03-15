package baidu_netdisk

import (
	"context"
	"encoding/base64"
	"fmt"
	"github.com/morecup/rclone/fs"
	"github.com/morecup/rclone/fs/fserrors"
	"github.com/morecup/rclone/lib/pacer"
	"github.com/morecup/rclone/lib/rest"
	"github.com/pkg/errors"
	"math"
	"math/rand"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type BaiduClient struct {
	*rest.Client
	Channel    string
	Web        string
	AppId      string
	Bdstoken   string
	LogId      string
	ClientType string
	DpLogId    string

	sessionId string
	userId    string
	countId   int
}

func generateRandomNumber(n int) string {
	rand.Seed(time.Now().UnixNano())
	min := int(math.Pow10(n - 1))
	max := int(math.Pow10(n)) - 1
	return fmt.Sprintf("%d", rand.Intn(max-min)+min)
}

func NewBaiduClient(client *rest.Client, logId string) *BaiduClient {
	//logId 为空字符串的情况时，对应js实现 (new Date).getTime() + '' + Math.random()
	if logId == "" {
		// 获取当前时间戳，单位为毫秒
		timestamp := time.Now().UnixNano() / int64(time.Millisecond)

		// 生成一个[0,1)之间的随机数
		randomNumber := rand.Float64()

		// 使用fmt.Sprintf将两个数值转换为字符串并连接
		logId = fmt.Sprintf("%d%s", timestamp, strconv.FormatFloat(randomNumber, 'g', 16, 64))
	}
	b := &BaiduClient{
		Client: client,
		//Channel: "00000000000000000000000000000000",
		Web:   "1",
		AppId: "250528",
		//Bdstoken:   "edc64747a74ff7c02c5f85e16c2af2d5",
		LogId: base64.StdEncoding.EncodeToString([]byte(logId)),
		//ClientType: "0",
		//DpLogId:    "83616500728872120038",
	}
	b.sessionId = generateRandomNumber(6)
	b.userId = "00" + generateRandomNumber(8)
	return b
}

type ErrnoResponse interface {
	GetErrno() int
}

func intToDigitString(i int, strLen int) string {
	s := strconv.Itoa(i)
	for len(s) < strLen {
		s = "0" + s
	}
	if len(s) > strLen {
		s = s[:strLen]
	}
	return s
}

func (b *BaiduClient) getCountId() string {
	if b.countId < 9999 {
		b.countId += 1
	}
	return intToDigitString(b.countId, 4)
}
func (b *BaiduClient) getDpLogId() string {
	return b.sessionId + b.userId + b.getCountId()
}

func (b *BaiduClient) CallJSONIgnore(ctx context.Context, opts *rest.Opts, request interface{}, response ErrnoResponse, ignoreList []int) (*http.Response, error) {
	resp, err := b.Client.CallJSON(ctx, b.AddParam(opts), request, response)
	if err != nil {
		return resp, err
	}
	//命中接口频控
	if response.GetErrno() == 31034 {
		duration := time.Second * time.Duration(2)
		time.Sleep(duration)
		return b.CallJSONIgnore(ctx, opts, request, response, ignoreList)
	}

	if response.GetErrno() != 0 && ignoreList != nil {
		for _, ignoreErrno := range ignoreList {
			if response.GetErrno() == ignoreErrno {
				return resp, nil
			}
		}
		return resp, errors.WithStack(fmt.Errorf("opts: %+v,response error %d ,resp: %+v ,response body: %+v", opts, response.GetErrno(), resp, response))
	}

	return resp, nil
}

func (b *BaiduClient) CallJSON(ctx context.Context, opts *rest.Opts, request interface{}, response ErrnoResponse) (*http.Response, error) {
	return b.CallJSONIgnore(ctx, opts, request, response, []int{})
}
func (b *BaiduClient) CallJSONBase(ctx context.Context, opts *rest.Opts, request interface{}, response interface{}) (*http.Response, error) {
	return b.Client.CallJSON(ctx, b.AddParam(opts), request, response)
}

func (b *BaiduClient) Call(ctx context.Context, opts *rest.Opts) (*http.Response, error) {
	res, err := b.Client.Call(ctx, b.AddParam(opts))
	return res, err
}

func (b *BaiduClient) AddParam(opts *rest.Opts) *rest.Opts {
	if opts.Parameters == nil {
		opts.Parameters = make(url.Values)
	}
	opts.Parameters.Set("app_id", b.AppId)
	//if strings.Contains(opts.RootURL, "pan.baidu.com") {
	if strings.Contains(b.Client.GetRoot(), "pan.baidu.com") {
		//opts.Parameters.Set("channel", b.Channel)
		opts.Parameters.Set("web", b.Web)
		if b.Bdstoken != "" {
			opts.Parameters.Set("bdstoken", b.Bdstoken)
		}
		opts.Parameters.Set("logid", b.LogId)
		//opts.Parameters.Set("clienttype", b.ClientType)
		opts.Parameters.Set("dp-logid", b.getDpLogId())
	}
	return opts
}

func shouldRetry(ctx context.Context, resp *http.Response, err error) (bool, error) {
	if fserrors.ContextError(ctx, &err) {
		return false, err
	}
	retry := false
	if resp != nil {
		switch resp.StatusCode {
		case 401:
			if len(resp.Header["Www-Authenticate"]) == 1 && strings.Contains(resp.Header["Www-Authenticate"][0], "expired_token") {
				retry = true
				fs.Debugf(nil, "Should retry: %v", err)
			} else if err != nil && strings.Contains(err.Error(), "Unable to initialize RPS") {
				retry = true
				fs.Debugf(nil, "HTTP 401: Unable to initialize RPS. Trying again.")
			}
		case 429: // Too Many Requests.
			// see https://docs.microsoft.com/en-us/sharepoint/dev/general-development/how-to-avoid-getting-throttled-or-blocked-in-sharepoint-online
			if values := resp.Header["Retry-After"]; len(values) == 1 && values[0] != "" {
				retryAfter, parseErr := strconv.Atoi(values[0])
				if parseErr != nil {
					fs.Debugf(nil, "Failed to parse Retry-After: %q: %v", values[0], parseErr)
				} else {
					duration := time.Second * time.Duration(retryAfter)
					retry = true
					err = pacer.RetryAfterError(err, duration)
					fs.Debugf(nil, "Too many requests. Trying again in %d seconds.", retryAfter)
				}
			}
		case 403:
			duration := time.Second * time.Duration(1)
			retry = true
			err = pacer.RetryAfterError(err, duration)
			fs.Debugf(nil, "Should retry: %v", err)
		case 507: // Insufficient Storage
			return false, fserrors.FatalError(err)
		}
	}
	return retry || fserrors.ShouldRetry(err), err
}
