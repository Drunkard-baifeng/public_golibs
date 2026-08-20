package httpclienttls

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	fhttp "github.com/bogdanfinn/fhttp"
)

// Options contains settings for one request.
type Options struct {
	Params         map[string]string
	Headers        map[string]string
	HeaderValues   http.Header
	HeaderOrder    []string
	Cookies        map[string]string
	Timeout        time.Duration
	AllowRedirects *bool
	BrowserMode    BrowserMode
}

// FileField describes one multipart file field.
type FileField struct {
	FieldName   string
	FileName    string
	ContentType string
	FilePath    string
	Data        []byte
}

func (c *Client) Get(target string, options *Options) (*Response, error) {
	return c.GetContext(context.Background(), target, options)
}

func (c *Client) GetContext(ctx context.Context, target string, options *Options) (*Response, error) {
	return c.doRequest(ctx, http.MethodGet, target, nil, options)
}

func (c *Client) Post(target string, body any, options *Options) (*Response, error) {
	return c.PostContext(context.Background(), target, body, options)
}

func (c *Client) PostContext(ctx context.Context, target string, body any, options *Options) (*Response, error) {
	return c.doRequest(ctx, http.MethodPost, target, body, options)
}

func (c *Client) Put(target string, body any, options *Options) (*Response, error) {
	return c.doRequest(context.Background(), http.MethodPut, target, body, options)
}

func (c *Client) Delete(target string, options *Options) (*Response, error) {
	return c.doRequest(context.Background(), http.MethodDelete, target, nil, options)
}

func (c *Client) Patch(target string, body any, options *Options) (*Response, error) {
	return c.doRequest(context.Background(), http.MethodPatch, target, body, options)
}

func (c *Client) Head(target string, options *Options) (*Response, error) {
	return c.doRequest(context.Background(), http.MethodHead, target, nil, options)
}

func (c *Client) Options(target string, options *Options) (*Response, error) {
	return c.doRequest(context.Background(), http.MethodOptions, target, nil, options)
}

func (c *Client) PostJSON(target string, data any, options *Options) (*Response, error) {
	raw, err := json.Marshal(data)
	if err != nil {
		return nil, fmt.Errorf("marshal JSON body: %w", err)
	}
	options = cloneOptions(options)
	options.Headers["Content-Type"] = "application/json"
	if options.BrowserMode == "" {
		options.BrowserMode = BrowserModeJSON
	}
	return c.doRequest(context.Background(), http.MethodPost, target, raw, options)
}

func (c *Client) PostForm(target string, data map[string]string, options *Options) (*Response, error) {
	values := make(url.Values, len(data))
	for key, value := range data {
		values.Set(key, value)
	}
	options = cloneOptions(options)
	options.Headers["Content-Type"] = "application/x-www-form-urlencoded"
	return c.doRequest(context.Background(), http.MethodPost, target, []byte(values.Encode()), options)
}

func (c *Client) PostBytes(target string, data []byte, options *Options) (*Response, error) {
	options = cloneOptions(options)
	if options.Headers["Content-Type"] == "" {
		options.Headers["Content-Type"] = "application/octet-stream"
	}
	return c.doRequest(context.Background(), http.MethodPost, target, data, options)
}

func (c *Client) PostMultipart(target string, fields map[string]string, files []FileField, options *Options) (*Response, error) {
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	for name, value := range fields {
		if err := writer.WriteField(name, value); err != nil {
			return nil, fmt.Errorf("write multipart field: %w", err)
		}
	}
	for _, file := range files {
		data := file.Data
		if file.FilePath != "" {
			var err error
			data, err = os.ReadFile(file.FilePath)
			if err != nil {
				return nil, fmt.Errorf("read multipart file: %w", err)
			}
			if file.FileName == "" {
				file.FileName = filepath.Base(file.FilePath)
			}
		}
		if file.FieldName == "" {
			file.FieldName = "file"
		}
		if file.FileName == "" {
			file.FileName = "file"
		}
		if file.ContentType == "" {
			file.ContentType = "application/octet-stream"
		}
		disposition := mime.FormatMediaType("form-data", map[string]string{
			"name": file.FieldName, "filename": file.FileName,
		})
		part, err := writer.CreatePart(map[string][]string{
			"Content-Disposition": {disposition},
			"Content-Type":        {file.ContentType},
		})
		if err != nil {
			return nil, fmt.Errorf("create multipart file field: %w", err)
		}
		if _, err := part.Write(data); err != nil {
			return nil, fmt.Errorf("write multipart file: %w", err)
		}
	}
	if err := writer.Close(); err != nil {
		return nil, fmt.Errorf("close multipart writer: %w", err)
	}
	options = cloneOptions(options)
	options.Headers["Content-Type"] = writer.FormDataContentType()
	return c.doRequest(context.Background(), http.MethodPost, target, body.Bytes(), options)
}

func (c *Client) PostFile(target, fieldName, filePath string, options *Options) (*Response, error) {
	return c.PostMultipart(target, nil, []FileField{{FieldName: fieldName, FilePath: filePath}}, options)
}

func cloneOptions(options *Options) *Options {
	if options == nil {
		return &Options{Headers: make(map[string]string)}
	}
	result := &Options{
		Params:       cloneStringMap(options.Params),
		Headers:      cloneHeaderMap(options.Headers),
		HeaderValues: options.HeaderValues.Clone(),
		HeaderOrder:  append([]string(nil), options.HeaderOrder...),
		Cookies:      cloneStringMap(options.Cookies),
		Timeout:      options.Timeout,
		BrowserMode:  options.BrowserMode,
	}
	if options.AllowRedirects != nil {
		allow := *options.AllowRedirects
		result.AllowRedirects = &allow
	}
	return result
}

func (c *Client) doRequest(ctx context.Context, method, target string, body any, options *Options) (*Response, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	options = cloneOptions(options)
	parsedTarget, err := addQueryParams(target, options.Params)
	if err != nil {
		return nil, err
	}
	bodyBytes, err := requestBodyBytes(body)
	if err != nil {
		return nil, err
	}

	c.mu.RLock()
	inner := c.inner
	jar := c.jar
	defaultHeaders := cloneHeaderMap(c.headers)
	defaultHeaderOrder := append([]string(nil), c.headerOrder...)
	customHeaderOrder := c.customHeaderOrder
	defaultCookies := cloneStringMap(c.cookies)
	userAgent := c.userAgent
	profile := c.profile
	browserHeaders := c.browserHeaders
	timeout := c.timeout
	maxRedirects := c.maxRedirects
	maxBodyBytes := c.maxResponseBodyBytes
	configErr := c.configErr
	closed := c.closed
	c.mu.RUnlock()
	if configErr != nil {
		return nil, fmt.Errorf("httpclienttls configuration: %w", configErr)
	}
	if closed {
		return nil, fmt.Errorf("httpclienttls: client is closed")
	}
	if inner == nil {
		return nil, fmt.Errorf("httpclienttls: client is not initialized")
	}
	if options.Timeout > 0 {
		timeout = options.Timeout
	}
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}

	headers := make(fhttp.Header, len(defaultHeaders)+len(options.HeaderValues)+8)
	for key, value := range defaultHeaders {
		headers.Set(key, value)
	}
	for key, value := range options.Headers {
		headers.Set(key, value)
	}
	for key, values := range options.HeaderValues {
		headers[http.CanonicalHeaderKey(key)] = append([]string(nil), values...)
	}
	profile.UserAgent = userAgent
	if browserHeaders {
		if err := applyBrowserHeaders(headers, profile, options.BrowserMode, parsedTarget); err != nil {
			return nil, err
		}
	} else if headers.Get("User-Agent") == "" && userAgent != "" {
		headers.Set("User-Agent", userAgent)
	}
	headerOrder := defaultHeaderOrder
	if !customHeaderOrder && browserHeaders && options.BrowserMode != BrowserModeOff {
		headerOrder = profileHeaderOrder(profile, options.BrowserMode, false)
	} else if !customHeaderOrder {
		headerOrder = nil
	}
	if configuredOrder, exists := headers[fhttp.HeaderOrderKey]; exists {
		headerOrder = normalizeHeaderOrder(configuredOrder)
	}
	if options.HeaderOrder != nil {
		headerOrder = normalizeHeaderOrder(options.HeaderOrder)
	}
	if len(headerOrder) > 0 {
		headers[fhttp.HeaderOrderKey] = append([]string(nil), headerOrder...)
	}
	explicitCookies := defaultCookies
	for name, value := range options.Cookies {
		explicitCookies[name] = value
	}
	allowRedirects := options.AllowRedirects == nil || *options.AllowRedirects

	currentURL := parsedTarget
	currentMethod := method
	currentBody := bodyBytes
	initialURL, _ := url.Parse(currentURL)
	redirects := 0
	for {
		requestURL, err := url.Parse(currentURL)
		if err != nil {
			return nil, fmt.Errorf("parse request URL: %w", err)
		}
		requestCookies := mergeRequestCookies(jar.Cookies(requestURL), explicitCookies)
		request, err := newFHTTPRequest(ctx, currentMethod, currentURL, currentBody, headers, requestCookies)
		if err != nil {
			return nil, err
		}
		response, err := inner.Do(request)
		if err != nil {
			return nil, fmt.Errorf("request %s %s: %w", currentMethod, currentURL, err)
		}
		responseCookies := response.Cookies()
		if len(responseCookies) > 0 {
			jar.SetCookies(request.URL, responseCookies)
			c.observeCookies(responseCookies)
			for _, cookie := range responseCookies {
				if cookie != nil {
					delete(explicitCookies, cookie.Name)
				}
			}
		}

		location := strings.TrimSpace(response.Header.Get("Location"))
		if !allowRedirects || !isRedirectStatus(response.StatusCode) || location == "" || redirects >= maxRedirects {
			return buildResponse(response, request, maxBodyBytes)
		}
		nextURL, err := resolveRedirectURL(currentURL, location)
		if err != nil {
			response.Body.Close()
			return nil, err
		}
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 1<<20))
		response.Body.Close()

		nextMethod, preserveBody := redirectMethod(currentMethod, response.StatusCode)
		if !preserveBody {
			currentBody = nil
			headers.Del("Content-Type")
			headers.Del("Content-Length")
		}
		nextParsed, _ := url.Parse(nextURL)
		if !canForwardSensitiveHeaders(initialURL, nextParsed) {
			headers.Del("Authorization")
			headers.Del("Cookie")
			explicitCookies = nil
		}
		setReferer(headers, currentURL, nextURL)
		if browserHeaders && options.BrowserMode != BrowserModeOff {
			updateRedirectBrowserHeaders(headers, options.BrowserMode, nextURL)
			if options.HeaderOrder == nil && !customHeaderOrder {
				headers[fhttp.HeaderOrderKey] = profileHeaderOrder(profile, options.BrowserMode, true)
			}
		}
		currentMethod = nextMethod
		currentURL = nextURL
		redirects++
	}
}

func newFHTTPRequest(ctx context.Context, method, target string, body []byte, headers fhttp.Header, cookies []*fhttp.Cookie) (*fhttp.Request, error) {
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	request, err := fhttp.NewRequestWithContext(ctx, method, target, reader)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	request.Header = headers.Clone()
	for _, cookie := range cookies {
		request.AddCookie(cookie)
	}
	return request, nil
}

func mergeRequestCookies(jarCookies []*fhttp.Cookie, overrides map[string]string) []*fhttp.Cookie {
	result := make([]*fhttp.Cookie, 0, len(jarCookies)+len(overrides))
	for _, cookie := range jarCookies {
		if cookie == nil {
			continue
		}
		if _, overridden := overrides[cookie.Name]; overridden {
			continue
		}
		clone := *cookie
		result = append(result, &clone)
	}
	for name, value := range overrides {
		result = append(result, &fhttp.Cookie{Name: name, Value: value})
	}
	return result
}

func buildResponse(response *fhttp.Response, request *fhttp.Request, maxBodyBytes int64) (*Response, error) {
	defer response.Body.Close()
	body, err := readResponseBody(response.Body, maxBodyBytes)
	if err != nil {
		return nil, err
	}
	requestSnapshot, _ := http.NewRequest(request.Method, request.URL.String(), nil)
	requestSnapshot.Header = toStandardHeader(request.Header)
	return &Response{
		StatusCode:       response.StatusCode,
		Status:           response.Status,
		Proto:            response.Proto,
		Headers:          toStandardHeader(response.Header),
		Cookies:          toStandardCookies(response.Cookies()),
		Body:             body,
		Request:          requestSnapshot,
		URL:              request.URL.String(),
		TransferEncoding: append([]string(nil), response.TransferEncoding...),
		Uncompressed:     response.Uncompressed,
	}, nil
}

func readResponseBody(reader io.Reader, maxBytes int64) ([]byte, error) {
	body, err := io.ReadAll(io.LimitReader(reader, maxBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read response body: %w", err)
	}
	if int64(len(body)) > maxBytes {
		return nil, fmt.Errorf("response body exceeds %d bytes", maxBytes)
	}
	return body, nil
}

func (c *Client) observeCookies(cookies []*fhttp.Cookie) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, cookie := range cookies {
		if cookie != nil {
			c.observedCookies[cookie.Name] = cookie.Value
		}
	}
}

func addQueryParams(target string, params map[string]string) (string, error) {
	parsedURL, err := url.Parse(target)
	if err != nil {
		return "", fmt.Errorf("parse request URL: %w", err)
	}
	query := parsedURL.Query()
	for key, value := range params {
		query.Set(key, value)
	}
	parsedURL.RawQuery = query.Encode()
	return parsedURL.String(), nil
}

func requestBodyBytes(body any) ([]byte, error) {
	switch value := body.(type) {
	case nil:
		return nil, nil
	case []byte:
		return append([]byte(nil), value...), nil
	case string:
		return []byte(value), nil
	case *bytes.Buffer:
		return append([]byte(nil), value.Bytes()...), nil
	case io.Reader:
		current, err := io.ReadAll(value)
		if err != nil {
			return nil, fmt.Errorf("read request body: %w", err)
		}
		return current, nil
	default:
		current, err := json.Marshal(value)
		if err != nil {
			return nil, fmt.Errorf("marshal request body: %w", err)
		}
		return current, nil
	}
}

func isRedirectStatus(statusCode int) bool {
	switch statusCode {
	case http.StatusMovedPermanently, http.StatusFound, http.StatusSeeOther,
		http.StatusTemporaryRedirect, http.StatusPermanentRedirect:
		return true
	default:
		return false
	}
}

func redirectMethod(method string, statusCode int) (string, bool) {
	switch statusCode {
	case http.StatusMovedPermanently, http.StatusFound, http.StatusSeeOther:
		if method != http.MethodGet && method != http.MethodHead {
			return http.MethodGet, false
		}
	}
	return method, true
}

func resolveRedirectURL(currentURL, location string) (string, error) {
	base, err := url.Parse(currentURL)
	if err != nil {
		return "", fmt.Errorf("parse redirect base URL: %w", err)
	}
	reference, err := url.Parse(location)
	if err != nil {
		return "", fmt.Errorf("parse redirect location: %w", err)
	}
	return base.ResolveReference(reference).String(), nil
}

func canForwardSensitiveHeaders(initial, target *url.URL) bool {
	if initial == nil || target == nil {
		return false
	}
	initialHost := strings.ToLower(strings.TrimSuffix(initial.Hostname(), "."))
	targetHost := strings.ToLower(strings.TrimSuffix(target.Hostname(), "."))
	return targetHost == initialHost || strings.HasSuffix(targetHost, "."+initialHost)
}

func setReferer(headers fhttp.Header, previousURL, nextURL string) {
	previous, err1 := url.Parse(previousURL)
	next, err2 := url.Parse(nextURL)
	if err1 != nil || err2 != nil || previous.Scheme == "https" && next.Scheme == "http" {
		headers.Del("Referer")
		return
	}
	previous.User = nil
	previous.Fragment = ""
	headers.Set("Referer", previous.String())
}

func toStandardHeader(header fhttp.Header) http.Header {
	result := make(http.Header, len(header))
	for key, values := range header {
		result[key] = append([]string(nil), values...)
	}
	return result
}

func toStandardCookies(cookies []*fhttp.Cookie) []*http.Cookie {
	result := make([]*http.Cookie, 0, len(cookies))
	for _, cookie := range cookies {
		if cookie == nil {
			continue
		}
		result = append(result, &http.Cookie{
			Name: cookie.Name, Value: cookie.Value, Path: cookie.Path, Domain: cookie.Domain,
			Expires: cookie.Expires, RawExpires: cookie.RawExpires, MaxAge: cookie.MaxAge,
			Secure: cookie.Secure, HttpOnly: cookie.HttpOnly, SameSite: http.SameSite(cookie.SameSite),
			Raw: cookie.Raw, Unparsed: append([]string(nil), cookie.Unparsed...),
		})
	}
	return result
}
