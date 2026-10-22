package httpclienttls

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"strings"
	"time"

	fhttp "github.com/bogdanfinn/fhttp"
)

// Options contains settings for one request.
type Options struct {
	Params       map[string]string
	Headers      map[string]string
	HeaderValues http.Header
	HeaderOrder  []string
	Cookies      map[string]string
	Timeout      time.Duration
	// ContentLength preserves a known reader upload size. Nil lets the request
	// infer its size; -1 means unknown. Zero with a non-nil body is unknown, as
	// in net/http. The transport checks a positive length against bytes sent.
	ContentLength *int64
	// DisableTimeout disables the client's total timeout; ctx still applies.
	DisableTimeout bool
	// HeaderTimeout bounds each exchange through receipt of response headers,
	// including connection establishment and request upload.
	HeaderTimeout time.Duration
	// ReadIdleTimeout bounds one blocked Body.Read, not consumer processing time.
	ReadIdleTimeout time.Duration
	AllowRedirects  *bool
	BrowserMode     BrowserMode
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
	return c.Do(ctx, http.MethodGet, target, nil, options)
}

func (c *Client) Post(target string, body any, options *Options) (*Response, error) {
	return c.PostContext(context.Background(), target, body, options)
}

func (c *Client) PostContext(ctx context.Context, target string, body any, options *Options) (*Response, error) {
	return c.Do(ctx, http.MethodPost, target, body, options)
}

func (c *Client) Put(target string, body any, options *Options) (*Response, error) {
	return c.Do(context.Background(), http.MethodPut, target, body, options)
}

func (c *Client) Delete(target string, options *Options) (*Response, error) {
	return c.Do(context.Background(), http.MethodDelete, target, nil, options)
}

func (c *Client) Patch(target string, body any, options *Options) (*Response, error) {
	return c.Do(context.Background(), http.MethodPatch, target, body, options)
}

func (c *Client) Head(target string, options *Options) (*Response, error) {
	return c.Do(context.Background(), http.MethodHead, target, nil, options)
}

func (c *Client) Options(target string, options *Options) (*Response, error) {
	return c.Do(context.Background(), http.MethodOptions, target, nil, options)
}

func (c *Client) PostJSON(target string, data any, options *Options) (*Response, error) {
	raw, err := json.Marshal(data)
	if err != nil {
		return nil, fmt.Errorf("marshal JSON body: %w", err)
	}
	options = cloneOptions(options)
	setContentType(options, "application/json")
	if options.BrowserMode == "" {
		options.BrowserMode = BrowserModeJSON
	}
	return c.Do(context.Background(), http.MethodPost, target, raw, options)
}

func (c *Client) PostForm(target string, data map[string]string, options *Options) (*Response, error) {
	values := make(url.Values, len(data))
	for key, value := range data {
		values.Set(key, value)
	}
	options = cloneOptions(options)
	setContentType(options, "application/x-www-form-urlencoded")
	return c.Do(context.Background(), http.MethodPost, target, []byte(values.Encode()), options)
}

func (c *Client) PostBytes(target string, data []byte, options *Options) (*Response, error) {
	options = cloneOptions(options)
	if options.Headers["Content-Type"] == "" {
		options.Headers["Content-Type"] = "application/octet-stream"
	}
	return c.Do(context.Background(), http.MethodPost, target, data, options)
}

func (c *Client) PostMultipart(target string, fields map[string]string, files []FileField, options *Options) (*Response, error) {
	return c.PostMultipartContext(context.Background(), target, fields, files, options)
}

func (c *Client) PostFile(target, fieldName, filePath string, options *Options) (*Response, error) {
	return c.PostMultipart(target, nil, []FileField{{FieldName: fieldName, FilePath: filePath}}, options)
}

func cloneOptions(options *Options) *Options {
	if options == nil {
		return &Options{Headers: make(map[string]string)}
	}
	result := &Options{
		Params:          cloneStringMap(options.Params),
		Headers:         cloneHeaderMap(options.Headers),
		HeaderValues:    options.HeaderValues.Clone(),
		HeaderOrder:     append([]string(nil), options.HeaderOrder...),
		Cookies:         cloneStringMap(options.Cookies),
		Timeout:         options.Timeout,
		DisableTimeout:  options.DisableTimeout,
		HeaderTimeout:   options.HeaderTimeout,
		ReadIdleTimeout: options.ReadIdleTimeout,
		BrowserMode:     options.BrowserMode,
	}
	if options.AllowRedirects != nil {
		allow := *options.AllowRedirects
		result.AllowRedirects = &allow
	}
	if options.ContentLength != nil {
		length := *options.ContentLength
		result.ContentLength = &length
	}
	return result
}

// Body-encoding helpers own Content-Type, including multi-value overrides.
// options must be cloned before calling this helper.
func setContentType(options *Options, value string) {
	for key := range options.HeaderValues {
		if strings.EqualFold(key, "Content-Type") {
			delete(options.HeaderValues, key)
		}
	}
	options.Headers["Content-Type"] = value
}

// Do performs a bounded, buffered request. For SSE or large files use DoStream.
func (c *Client) Do(ctx context.Context, method, target string, body any, options *Options) (*Response, error) {
	response, err := c.DoStream(ctx, method, target, body, options)
	if err != nil {
		return nil, err
	}
	return c.bufferResponse(response)
}

// DoStream returns after response headers arrive. The caller MUST close Body.
// Reader bodies are not buffered. A non-replayable body is not resent on 307/308;
// the redirect response is returned instead. No application-level retry occurs.
// An io.ReadCloser body is owned by the client and closed even on setup errors.
func (c *Client) DoStream(ctx context.Context, method, target string, body any, options *Options) (*StreamResponse, error) {
	// Until a request reaches the transport, this method owns its upload body.
	// Track replay bodies too, so a later request-construction error closes them.
	pendingBody, _ := body.(io.ReadCloser)
	defer func() {
		if pendingBody != nil {
			pendingBody.Close()
		}
	}()
	if ctx == nil {
		ctx = context.Background()
	}
	options = cloneOptions(options)
	if options.ContentLength != nil && *options.ContentLength < -1 {
		return nil, fmt.Errorf("content length must be -1 or non-negative")
	}
	parsedTarget, err := addQueryParams(target, options.Params)
	if err != nil {
		return nil, err
	}
	bodyReader, err := requestBodyReader(body)
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
	var cancelTotal context.CancelFunc = func() {}
	if timeout > 0 && !options.DisableTimeout {
		ctx, cancelTotal = context.WithTimeout(ctx, timeout)
	}
	ctx, cancelCause := context.WithCancelCause(ctx)
	transferred := false
	cleanup := func() { cancelCause(context.Canceled); cancelTotal() }
	defer func() {
		if !transferred {
			cleanup()
		}
	}()

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
	currentBody := bodyReader
	var replayBody func() (io.ReadCloser, error)
	var contentLength int64
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
		if redirects == 0 {
			if options.ContentLength != nil {
				request.ContentLength = *options.ContentLength
			}
			replayBody, contentLength = request.GetBody, request.ContentLength
		} else if currentBody != nil {
			request.GetBody, request.ContentLength = replayBody, contentLength
		}
		stopHeaderTimer := startCancelTimer(options.HeaderTimeout, cancelCause, ErrHeaderTimeout)
		pendingBody = nil // The transport now owns request.Body.
		response, err := inner.Do(request)
		stopHeaderTimer()
		if cause := context.Cause(ctx); cause != nil {
			err = cause
		}
		if err != nil {
			if response != nil && response.Body != nil {
				response.Body.Close()
			}
			return nil, fmt.Errorf("HTTP request failed: %w", err)
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
		nextMethod, preserveBody := redirectMethod(currentMethod, response.StatusCode)
		cannotReplay := preserveBody && request.Body != nil && request.Body != fhttp.NoBody && request.GetBody == nil
		if !allowRedirects || !isRedirectStatus(response.StatusCode) || location == "" || redirects >= maxRedirects {
			transferred = true
			return buildStreamResponse(response, request, ctx, cancelCause, cleanup, options.ReadIdleTimeout), nil
		}
		if cannotReplay {
			transferred = true
			return buildStreamResponse(response, request, ctx, cancelCause, cleanup, options.ReadIdleTimeout), nil
		}
		nextURL, err := resolveRedirectURL(currentURL, location)
		if err != nil {
			response.Body.Close()
			return nil, err
		}
		// Do not drain an untrusted redirect body: it can be an endless stream.
		response.Body.Close()

		if !preserveBody {
			currentBody = nil
			replayBody, contentLength = nil, 0
			headers.Del("Content-Type")
			headers.Del("Content-Length")
		} else if request.GetBody != nil {
			currentBody, err = request.GetBody()
			if err != nil {
				return nil, fmt.Errorf("reopen request body: %w", err)
			}
			pendingBody, _ = currentBody.(io.ReadCloser)
		} else {
			currentBody = nil
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

func newFHTTPRequest(ctx context.Context, method, target string, body io.Reader, headers fhttp.Header, cookies []*fhttp.Cookie) (*fhttp.Request, error) {
	request, err := fhttp.NewRequestWithContext(ctx, method, target, body)
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

func readResponseBody(reader io.Reader, maxBytes int64) ([]byte, error) {
	readLimit := maxBytes
	if readLimit < math.MaxInt64 {
		readLimit++
	}
	body, err := io.ReadAll(io.LimitReader(reader, readLimit))
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
	// Preserve signed URLs byte-for-byte when no query changes were requested.
	if len(params) == 0 {
		return target, nil
	}
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

func requestBodyReader(body any) (io.Reader, error) {
	switch value := body.(type) {
	case nil:
		return nil, nil
	case []byte:
		return bytes.NewReader(value), nil
	case string:
		return strings.NewReader(value), nil
	case io.Reader:
		return value, nil
	default:
		current, err := json.Marshal(value)
		if err != nil {
			return nil, fmt.Errorf("marshal request body: %w", err)
		}
		return bytes.NewReader(current), nil
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
		return method, false
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
	return targetHost == initialHost && initial.Scheme == target.Scheme && effectivePort(initial) == effectivePort(target)
}

func effectivePort(u *url.URL) string {
	if port := u.Port(); port != "" {
		return port
	}
	if u.Scheme == "https" {
		return "443"
	}
	return "80"
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
