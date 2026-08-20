package httpclient

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/net/idna"
)

// Options 请求选项
type Options struct {
	Params         map[string]string // URL查询参数
	Headers        map[string]string // 请求头
	Cookies        map[string]string // Cookie
	Timeout        time.Duration     // 超时时间
	AllowRedirects *bool             // 是否允许重定向
}

// Get 发送GET请求
func (c *Client) Get(urlStr string, opts *Options) (*Response, error) {
	return c.doRequest("GET", urlStr, nil, opts)
}

// Post 发送POST请求
func (c *Client) Post(urlStr string, body interface{}, opts *Options) (*Response, error) {
	return c.doRequest("POST", urlStr, body, opts)
}

// Put 发送PUT请求
func (c *Client) Put(urlStr string, body interface{}, opts *Options) (*Response, error) {
	return c.doRequest("PUT", urlStr, body, opts)
}

// Delete 发送DELETE请求
func (c *Client) Delete(urlStr string, opts *Options) (*Response, error) {
	return c.doRequest("DELETE", urlStr, nil, opts)
}

// Patch 发送PATCH请求
func (c *Client) Patch(urlStr string, body interface{}, opts *Options) (*Response, error) {
	return c.doRequest("PATCH", urlStr, body, opts)
}

// Head 发送HEAD请求
func (c *Client) Head(urlStr string, opts *Options) (*Response, error) {
	return c.doRequest("HEAD", urlStr, nil, opts)
}

// Options 发送OPTIONS请求
func (c *Client) Options(urlStr string, opts *Options) (*Response, error) {
	return c.doRequest("OPTIONS", urlStr, nil, opts)
}

// PostJSON 发送JSON数据
func (c *Client) PostJSON(urlStr string, data interface{}, opts *Options) (*Response, error) {
	jsonBytes, err := json.Marshal(data)
	if err != nil {
		return nil, fmt.Errorf("JSON序列化失败: %w", err)
	}

	opts = cloneOptions(opts)
	opts.Headers["Content-Type"] = "application/json"

	return c.doRequest("POST", urlStr, jsonBytes, opts)
}

// PostForm 发送表单数据
func (c *Client) PostForm(urlStr string, data map[string]string, opts *Options) (*Response, error) {
	formData := make(url.Values)
	for k, v := range data {
		formData.Set(k, v)
	}

	opts = cloneOptions(opts)
	opts.Headers["Content-Type"] = "application/x-www-form-urlencoded"

	return c.doRequest("POST", urlStr, []byte(formData.Encode()), opts)
}

// PostBytes 发送字节数据
func (c *Client) PostBytes(urlStr string, data []byte, opts *Options) (*Response, error) {
	opts = cloneOptions(opts)
	if opts.Headers["Content-Type"] == "" {
		opts.Headers["Content-Type"] = "application/octet-stream"
	}

	return c.doRequest("POST", urlStr, data, opts)
}

// FileField 文件字段定义
type FileField struct {
	FieldName   string // 表单字段名
	FileName    string // 文件名
	ContentType string // MIME类型（可选）
	FilePath    string // 本地文件路径（与Data二选一）
	Data        []byte // 文件内容（与FilePath二选一）
}

// PostMultipart 发送multipart表单数据
func (c *Client) PostMultipart(urlStr string, fields map[string]string, files []FileField, opts *Options) (*Response, error) {
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)

	// 添加普通字段
	for key, value := range fields {
		if err := writer.WriteField(key, value); err != nil {
			return nil, fmt.Errorf("写入字段失败: %w", err)
		}
	}

	// 添加文件
	for _, file := range files {
		var fileContent []byte
		var err error

		if file.FilePath != "" {
			fileContent, err = os.ReadFile(file.FilePath)
			if err != nil {
				return nil, fmt.Errorf("读取文件失败: %w", err)
			}
			if file.FileName == "" {
				file.FileName = filepath.Base(file.FilePath)
			}
		} else {
			fileContent = file.Data
		}

		if file.FieldName == "" {
			file.FieldName = "file"
		}
		if file.FileName == "" {
			file.FileName = "file"
		}

		contentType := file.ContentType
		if contentType == "" {
			contentType = "application/octet-stream"
		}
		disposition := mime.FormatMediaType("form-data", map[string]string{
			"name":     file.FieldName,
			"filename": file.FileName,
		})
		part, err := writer.CreatePart(map[string][]string{
			"Content-Disposition": {disposition},
			"Content-Type":        {contentType},
		})
		if err != nil {
			return nil, fmt.Errorf("创建文件字段失败: %w", err)
		}

		if _, err := part.Write(fileContent); err != nil {
			return nil, fmt.Errorf("写入文件内容失败: %w", err)
		}
	}

	if err := writer.Close(); err != nil {
		return nil, fmt.Errorf("关闭multipart失败: %w", err)
	}

	opts = cloneOptions(opts)
	opts.Headers["Content-Type"] = writer.FormDataContentType()

	return c.doRequest("POST", urlStr, body.Bytes(), opts)
}

// PostFile 上传单个文件
func (c *Client) PostFile(urlStr string, fieldName string, filePath string, opts *Options) (*Response, error) {
	return c.PostMultipart(urlStr, nil, []FileField{
		{FieldName: fieldName, FilePath: filePath},
	}, opts)
}

func cloneOptions(opts *Options) *Options {
	if opts == nil {
		return &Options{
			Headers: make(map[string]string),
		}
	}
	result := &Options{
		Params:  cloneStringMap(opts.Params),
		Headers: cloneHeaders(opts.Headers),
		Cookies: cloneStringMap(opts.Cookies),
		Timeout: opts.Timeout,
	}
	if opts.AllowRedirects != nil {
		allowRedirects := *opts.AllowRedirects
		result.AllowRedirects = &allowRedirects
	}
	return result
}

type filteringCookieJar struct {
	http.CookieJar
	overrides   map[string]struct{}
	previousURL *url.URL
	observe     func([]*http.Cookie)
}

func (j *filteringCookieJar) SetCookies(targetURL *url.URL, cookies []*http.Cookie) {
	j.CookieJar.SetCookies(targetURL, cookies)
	if j.observe != nil {
		j.observe(cookies)
	}
	for _, cookie := range cookies {
		delete(j.overrides, cookie.Name)
	}
}

func (j *filteringCookieJar) Cookies(targetURL *url.URL) []*http.Cookie {
	cookies := j.CookieJar.Cookies(targetURL)
	filterOverrides := canForwardCookieHeader(j.previousURL, targetURL)
	j.previousURL = targetURL
	if !filterOverrides {
		return cookies
	}

	filtered := make([]*http.Cookie, 0, len(cookies))
	for _, cookie := range cookies {
		if _, overridden := j.overrides[cookie.Name]; !overridden {
			filtered = append(filtered, cookie)
		}
	}
	return filtered
}

func canForwardCookieHeader(previousURL, targetURL *url.URL) bool {
	if previousURL == nil || targetURL == nil {
		return false
	}
	previousHost, err := idna.Lookup.ToASCII(strings.TrimSuffix(previousURL.Hostname(), "."))
	if err != nil {
		return false
	}
	targetHost, err := idna.Lookup.ToASCII(strings.TrimSuffix(targetURL.Hostname(), "."))
	if err != nil {
		return false
	}
	previousHost = strings.ToLower(previousHost)
	targetHost = strings.ToLower(targetHost)
	if net.ParseIP(previousHost) != nil || net.ParseIP(targetHost) != nil {
		return previousHost == targetHost
	}
	return targetHost == previousHost || strings.HasSuffix(targetHost, "."+previousHost)
}

// doRequest 执行HTTP请求
func (c *Client) doRequest(method, urlStr string, body interface{}, opts *Options) (*Response, error) {
	opts = cloneOptions(opts)

	c.mu.RLock()
	transport := c.transport
	jar := c.jar
	defaultHeaders := cloneStringMap(c.headers)
	defaultCookies := cloneStringMap(c.cookies)
	timeout := c.timeout
	maxRedirects := c.maxRedirects
	configErr := c.configErr
	c.mu.RUnlock()
	if configErr != nil {
		return nil, fmt.Errorf("客户端配置失败: %w", configErr)
	}

	// 构建URL参数
	if len(opts.Params) > 0 {
		parsedURL, err := url.Parse(urlStr)
		if err != nil {
			return nil, fmt.Errorf("解析URL失败: %w", err)
		}
		query := parsedURL.Query()
		for k, v := range opts.Params {
			query.Set(k, v)
		}
		parsedURL.RawQuery = query.Encode()
		urlStr = parsedURL.String()
	}

	// 构建请求体
	var bodyReader io.Reader
	if body != nil {
		switch v := body.(type) {
		case []byte:
			bodyReader = bytes.NewReader(v)
		case string:
			bodyReader = strings.NewReader(v)
		case io.Reader:
			bodyReader = v
		default:
			// 尝试JSON序列化
			jsonBytes, err := json.Marshal(v)
			if err != nil {
				return nil, fmt.Errorf("序列化请求体失败: %w", err)
			}
			bodyReader = bytes.NewReader(jsonBytes)
		}
	}

	if opts.Timeout > 0 {
		timeout = opts.Timeout
	}
	ctx := context.Background()
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}

	// 创建请求
	req, err := http.NewRequestWithContext(ctx, method, urlStr, bodyReader)
	if err != nil {
		return nil, fmt.Errorf("创建请求失败: %w", err)
	}

	// 设置默认headers
	for k, v := range defaultHeaders {
		req.Header.Set(k, v)
	}

	// 设置请求headers
	for k, v := range opts.Headers {
		req.Header.Set(k, v)
	}

	// 默认 Cookie 和单次请求 Cookie 优先于 Cookie Jar 中的同名值。
	cookieOverrides := make(map[string]struct{}, len(defaultCookies)+len(opts.Cookies))
	for k, v := range defaultCookies {
		req.AddCookie(&http.Cookie{Name: k, Value: v})
		cookieOverrides[k] = struct{}{}
	}
	for k, v := range opts.Cookies {
		if _, exists := defaultCookies[k]; exists {
			replaceRequestCookie(req, k, v)
		} else {
			req.AddCookie(&http.Cookie{Name: k, Value: v})
		}
		cookieOverrides[k] = struct{}{}
	}

	allowRedirects := opts.AllowRedirects == nil || *opts.AllowRedirects
	httpClient := &http.Client{
		Transport: transport,
		Jar: &filteringCookieJar{
			CookieJar:   jar,
			overrides:   cookieOverrides,
			previousURL: req.URL,
			observe: func(cookies []*http.Cookie) {
				c.mu.Lock()
				defer c.mu.Unlock()
				for _, cookie := range cookies {
					c.observedCookies[cookie.Name] = cookie.Value
				}
			},
		},
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if !allowRedirects || (maxRedirects >= 0 && len(via) >= maxRedirects) {
				return http.ErrUseLastResponse
			}
			return nil
		},
	}

	// 发送请求
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("请求失败: %w", err)
	}
	defer resp.Body.Close()

	// 读取响应体（处理调用方显式请求的gzip压缩）。
	var reader io.Reader = resp.Body
	manuallyDecompressed := false
	if responseCanHaveBody(method, resp.StatusCode) &&
		strings.EqualFold(strings.TrimSpace(resp.Header.Get("Content-Encoding")), "gzip") {
		gzReader, err := gzip.NewReader(resp.Body)
		if err != nil {
			return nil, fmt.Errorf("创建gzip解压器失败: %w", err)
		}
		defer gzReader.Close()
		reader = gzReader
		manuallyDecompressed = true
	}
	respBody, err := io.ReadAll(reader)
	if err != nil {
		return nil, fmt.Errorf("读取响应失败: %w", err)
	}

	responseHeaders := resp.Header.Clone()
	if manuallyDecompressed {
		responseHeaders.Del("Content-Encoding")
		responseHeaders.Del("Content-Length")
	}

	return &Response{
		StatusCode: resp.StatusCode,
		Status:     resp.Status,
		Headers:    responseHeaders,
		Cookies:    resp.Cookies(),
		Body:       respBody,
		Request:    req,
	}, nil
}

func responseCanHaveBody(method string, statusCode int) bool {
	if method == http.MethodHead {
		return false
	}
	return statusCode < 100 || statusCode >= 200 && statusCode != http.StatusNoContent && statusCode != http.StatusNotModified
}

func replaceRequestCookie(req *http.Request, name, value string) {
	cookies := req.Cookies()
	req.Header.Del("Cookie")
	for _, cookie := range cookies {
		if cookie.Name == name {
			cookie.Value = value
		}
		req.AddCookie(cookie)
	}
}
