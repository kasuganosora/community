package httpclient

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/kasuganosora/community/config"
	"github.com/kasuganosora/community/log"
	"github.com/kasuganosora/community/utils"
	"github.com/pkg/errors"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"time"
)

const (
	DefaultTimeout  = 10 // seconds
	DefaultUA       = "Community/1.0"
	DefaultTryTimes = 0
)

type Client struct {
	http.Client
}

func NewClient() *Client {
	client := &Client{}

	if config.C.HttpProxy != "" {
		proxyURL, err := url.Parse(config.C.HttpProxy)

		if err == nil {
			client.Transport = &http.Transport{
				Proxy: http.ProxyURL(proxyURL),
			}
		} else {
			err = errors.WithMessage(err, "parse proxy url error")
			panic(err)
		}
	}

	return client
}

func NewNoProxyClient() *Client {
	return &Client{}
}

func (c *Client) NewRequest(method, urlStr string) *Request {
	urlObj, err := url.Parse(urlStr)
	if err != nil {
		err = errors.WithStack(err)
		panic(err)
	}

	// 去掉url中的query
	query := urlObj.Query()
	urlObj.RawQuery = ""

	return &Request{
		client: c,
		header: http.Header{
			"User-Agent": []string{DefaultUA},
		},
		timeout:   0,
		method:    method,
		url:       urlObj.String(),
		urlParams: query,
		tryTimes:  DefaultTryTimes,
	}
}

type Request struct {
	client    *Client
	header    http.Header
	timeout   int
	method    string
	url       string
	urlParams url.Values
	body      []byte
	tryTimes  int
}

func (r *Request) SetHeader(key, value string) *Request {
	r.header.Set(key, value)
	return r
}

func (r *Request) AddHeader(key, value string) *Request {
	r.header.Add(key, value)
	return r
}

func (r *Request) SetUserAgent(userAgent string) *Request {
	r.header.Set("User-Agent", userAgent)
	return r
}

func (r *Request) SetTimeout(timeout int) *Request {
	r.timeout = timeout
	return r
}

func (r *Request) SetUrlParams(key, value string) *Request {
	r.urlParams.Set(key, value)
	return r
}

func (r *Request) AddUrlParams(key, value string) *Request {
	r.urlParams.Add(key, value)
	return r
}

func (r *Request) SetBody(body []byte) *Request {
	r.body = body
	return r
}

func (r *Request) GetMethodAndURL() (string, string) {
	return r.method, r.url
}

func (r *Request) GetBody() []byte {
	return r.body
}

func (r *Request) SetJsonBody(v any) *Request {
	var err error
	r.body, err = json.Marshal(v)
	if err != nil {
		err = errors.WithStack(err)
		panic(err)
	}

	r.SetHeader("Content-Type", "application/json")

	return r
}

func (r *Request) SetMultipartBody(data map[string]any) *Request {
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	defer writer.Close()

	for key, value := range data {
		// 如果是文件
		switch value.(type) {
		case *multipart.FileHeader:
			fileHeader := value.(*multipart.FileHeader)
			file, err := fileHeader.Open()
			if err != nil {
				err = errors.WithStack(err)
				panic(err)
			}

			part, err := writer.CreateFormFile(key, fileHeader.Filename)
			if err != nil {
				err = errors.WithStack(err)
				panic(err)
			}

			_, err = io.Copy(part, file)
			if err != nil {
				err = errors.WithStack(err)
				panic(err)
			}
		case *os.File:
			file := value.(*os.File)
			part, err := writer.CreateFormFile(key, file.Name())
			if err != nil {
				err = errors.WithStack(err)
				panic(err)
			}

			_, err = io.Copy(part, file)
			if err != nil {
				err = errors.WithStack(err)
				panic(err)
			}
		default:
			// 如果是普通数据, 直接写入(转换为string)
			part, err := writer.CreateFormField(key)
			if err != nil {
				err = errors.WithStack(err)
				panic(err)
			}

			str := utils.ToString(value)
			_, err = part.Write([]byte(str))
		}
	}

	r.SetHeader("Content-Type", writer.FormDataContentType())
	r.SetBody(body.Bytes())

	return r
}

func (r *Request) SetFormUrlencoded(data url.Values) *Request {
	r.SetHeader("Content-Type", "application/x-www-form-urlencoded")
	r.SetBody([]byte(data.Encode()))

	return r
}

func (r *Request) SetTryTimes(tryTimes int) *Request {
	r.tryTimes = tryTimes
	return r
}

func (r *Request) Do() (resp *Response, err error) {
	var (
		req *http.Request
	)

	if r.body != nil {
		req, err = http.NewRequest(r.method, r.url, bytes.NewReader(r.body))
	} else {
		req, err = http.NewRequest(r.method, r.url, nil)
	}

	if err != nil {
		err = errors.WithStack(err)
		return
	}

	req.Header = r.header
	req.URL.RawQuery = r.urlParams.Encode()

	if r.timeout > 0 {
		ctx, cancel := context.WithTimeout(context.Background(), time.Duration(r.timeout)*time.Second)
		defer cancel()
		req = req.WithContext(ctx)
	}

	for i := 0; i <= r.tryTimes; i++ {
		var resp1 *http.Response
		resp1, err = r.client.Do(req)

		if err == nil {
			resp = &Response{
				Response: resp1,
			}

			if resp.StatusOk() {
				return
			}

			err = errors.New(fmt.Sprintf("status code error: %d", resp.StatusCode))
		}

		err = errors.WithMessagef(err, "try fetch %s %s %d times, error %+v", r.method, req.URL.String(), i, err)

		log.ErrorV(err)

		// 每次休息都是上一次的2倍, 默认为200ms
		time.Sleep(time.Duration(200<<i) * time.Millisecond)
	}

	return
}

type Response struct {
	*http.Response
	BodyData []byte
}

func (resp *Response) GetBodyData() (data []byte, err error) {
	if resp.BodyData != nil {
		return resp.BodyData, nil
	}

	data, err = io.ReadAll(resp.Body)
	if err != nil {
		err = errors.WithStack(err)
		return
	}

	resp.BodyData = data
	return
}

func (resp *Response) Json(v any) (err error) {
	data, err := resp.GetBodyData()
	if err != nil {
		return
	}

	err = json.Unmarshal(data, v)
	if err != nil {
		err = errors.WithStack(err)
		return
	}

	return
}

func (resp *Response) JsonMap() (m map[string]any, err error) {
	data, err := resp.GetBodyData()
	if err != nil {
		return
	}

	err = json.Unmarshal(data, &m)
	if err != nil {
		err = errors.WithStack(err)
		return
	}

	return
}

func (resp *Response) String() (s string, err error) {
	data, err := resp.GetBodyData()
	if err != nil {
		return
	}

	s = string(data)
	return
}

func (resp *Response) StatusOk() bool {
	return resp.StatusCode >= 200 && resp.StatusCode < 300
}
