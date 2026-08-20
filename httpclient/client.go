package httpclient

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/proxy"
)

// Client HTTP客户端。
type Client struct {
	mu sync.RWMutex

	transport       *http.Transport
	headers         map[string]string
	cookies         map[string]string
	observedCookies map[string]string
	jar             http.CookieJar

	timeout           time.Duration
	maxRedirects      int
	verify            bool
	maxIdleConns      int
	idleConnTimeout   time.Duration
	disableKeepAlives bool
	proxyURL          *url.URL
	proxyType         string
	configErr         error
}

const (
	defaultMaxIdleConns    = 100
	defaultIdleConnTimeout = 5 * time.Minute
	defaultMaxRedirects    = 5
)

// Config 客户端配置选项。
type Config struct {
	Timeout           time.Duration
	MaxRedirects      int
	Verify            *bool // nil 表示使用安全默认值 true
	Proxy             string
	ProxyType         string // "http" 或 "socks5"
	MaxIdleConns      int
	IdleConnTimeout   time.Duration
	DisableKeepAlives bool
}

// BoolPtr 返回布尔值指针，便于设置 Config.Verify。
func BoolPtr(value bool) *bool {
	return &value
}

// New 创建新的HTTP客户端。
func New() *Client {
	jar, _ := cookiejar.New(nil)
	c := &Client{
		headers:           make(map[string]string),
		cookies:           make(map[string]string),
		observedCookies:   make(map[string]string),
		jar:               jar,
		timeout:           30 * time.Second,
		maxRedirects:      defaultMaxRedirects,
		verify:            true,
		maxIdleConns:      defaultMaxIdleConns,
		idleConnTimeout:   defaultIdleConnTimeout,
		disableKeepAlives: false,
	}

	c.transport, _ = c.buildTransportLocked()
	return c
}

// NewWithConfig 使用配置创建HTTP客户端。
func NewWithConfig(cfg Config) *Client {
	c := New()

	if cfg.Timeout > 0 {
		c.timeout = cfg.Timeout
	}
	if cfg.MaxRedirects > 0 {
		c.maxRedirects = cfg.MaxRedirects
	}
	if cfg.Verify != nil {
		c.verify = *cfg.Verify
	}
	if cfg.MaxIdleConns > 0 {
		c.maxIdleConns = cfg.MaxIdleConns
	}
	if cfg.IdleConnTimeout > 0 {
		c.idleConnTimeout = cfg.IdleConnTimeout
	}
	c.disableKeepAlives = cfg.DisableKeepAlives

	// Rebuild once after applying all non-proxy transport settings.
	c.transport.CloseIdleConnections()
	c.transport, _ = c.buildTransportLocked()

	if cfg.Proxy != "" {
		proxyType := cfg.ProxyType
		if proxyType == "" {
			proxyType = "http"
		}
		c.SetProxy(cfg.Proxy, proxyType)
	}

	return c
}

// SetProxy 设置代理。配置错误会由 ProxyError 和下一次请求返回。
func (c *Client) SetProxy(proxyStr string, proxyType string) *Client {
	_ = c.ConfigureProxy(proxyStr, proxyType)
	return c
}

// ConfigureProxy 设置代理并直接返回配置错误。
func (c *Client) ConfigureProxy(proxyStr string, proxyType string) error {
	proxyURL, normalizedType, err := parseProxy(proxyStr, proxyType)

	c.mu.Lock()
	defer c.mu.Unlock()

	if err != nil {
		c.proxyURL = nil
		c.proxyType = ""
		c.configErr = err
		_ = c.rebuildTransportLocked()
		return err
	}

	c.proxyURL = proxyURL
	c.proxyType = normalizedType
	c.configErr = c.rebuildTransportLocked()
	return c.configErr
}

// ProxyError 返回最近一次代理配置错误。
func (c *Client) ProxyError() error {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.configErr
}

// SetHTTPProxy 快捷方法：设置 HTTP 代理。
func (c *Client) SetHTTPProxy(proxyStr string) *Client {
	return c.SetProxy(proxyStr, "http")
}

// SetSocks5Proxy 快捷方法：设置 SOCKS5 代理。
func (c *Client) SetSocks5Proxy(proxyStr string) *Client {
	return c.SetProxy(proxyStr, "socks5")
}

// ClearProxy 清除代理。
func (c *Client) ClearProxy() *Client {
	return c.SetProxy("", "")
}

func parseProxy(proxyStr string, proxyType string) (*url.URL, string, error) {
	proxyStr = strings.TrimSpace(proxyStr)
	if proxyStr == "" {
		return nil, "", nil
	}

	normalizedType := strings.ToLower(strings.TrimSpace(proxyType))
	if normalizedType == "" {
		normalizedType = "http"
	}
	if normalizedType != "http" && normalizedType != "socks5" {
		return nil, "", fmt.Errorf("不支持的代理类型 %q", proxyType)
	}

	if strings.Contains(proxyStr, "://") {
		proxyURL, err := url.Parse(proxyStr)
		if err != nil {
			return nil, "", fmt.Errorf("解析代理地址失败: %w", err)
		}
		switch strings.ToLower(proxyURL.Scheme) {
		case "http", "https":
			normalizedType = "http"
		case "socks5":
			normalizedType = "socks5"
		default:
			return nil, "", fmt.Errorf("不支持的代理协议 %q", proxyURL.Scheme)
		}
		if err := validateProxyURL(proxyURL); err != nil {
			return nil, "", err
		}
		proxyURL.Scheme = strings.ToLower(proxyURL.Scheme)
		return proxyURL, normalizedType, nil
	}

	hostPort := proxyStr
	var user *url.Userinfo
	if _, _, err := net.SplitHostPort(proxyStr); err != nil {
		if parts := strings.Split(proxyStr, ":"); len(parts) == 4 {
			hostPort = net.JoinHostPort(parts[0], parts[1])
			user = url.UserPassword(parts[2], parts[3])
		}
	}

	proxyURL := &url.URL{Scheme: normalizedType, Host: hostPort, User: user}
	if err := validateProxyURL(proxyURL); err != nil {
		return nil, "", err
	}
	return proxyURL, normalizedType, nil
}

func validateProxyURL(proxyURL *url.URL) error {
	if proxyURL.Hostname() == "" {
		return fmt.Errorf("代理地址缺少主机名")
	}
	port := proxyURL.Port()
	if port == "" {
		return fmt.Errorf("代理地址缺少端口")
	}
	portNumber, err := strconv.Atoi(port)
	if err != nil || portNumber < 1 || portNumber > 65535 {
		return fmt.Errorf("代理端口无效: %q", port)
	}
	if proxyURL.Path != "" && proxyURL.Path != "/" {
		return fmt.Errorf("代理地址不能包含路径")
	}
	if proxyURL.RawQuery != "" || proxyURL.Fragment != "" {
		return fmt.Errorf("代理地址不能包含查询参数或片段")
	}
	return nil
}

func (c *Client) buildTransportLocked() (*http.Transport, error) {
	directDialer := &net.Dialer{
		Timeout:   30 * time.Second,
		KeepAlive: 30 * time.Second,
	}
	transport := &http.Transport{
		DialContext:           directDialer.DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          c.maxIdleConns,
		MaxIdleConnsPerHost:   100,
		MaxConnsPerHost:       100,
		IdleConnTimeout:       c.idleConnTimeout,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
		DisableKeepAlives:     c.disableKeepAlives,
	}

	if !c.verify {
		transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} //nolint:gosec
	}

	if c.proxyURL == nil {
		return transport, nil
	}

	switch c.proxyType {
	case "http":
		transport.Proxy = http.ProxyURL(c.proxyURL)
	case "socks5":
		var auth *proxy.Auth
		if c.proxyURL.User != nil {
			auth = &proxy.Auth{User: c.proxyURL.User.Username()}
			auth.Password, _ = c.proxyURL.User.Password()
		}

		dialer, err := proxy.SOCKS5("tcp", c.proxyURL.Host, auth, directDialer)
		if err != nil {
			return nil, fmt.Errorf("创建 SOCKS5 代理失败: %w", err)
		}
		contextDialer, ok := dialer.(proxy.ContextDialer)
		if !ok {
			return nil, fmt.Errorf("SOCKS5 代理不支持上下文取消")
		}
		transport.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
			return contextDialer.DialContext(ctx, network, addr)
		}
	default:
		return nil, fmt.Errorf("不支持的代理类型 %q", c.proxyType)
	}

	return transport, nil
}

func (c *Client) rebuildTransportLocked() error {
	transport, err := c.buildTransportLocked()
	if err != nil {
		return err
	}
	oldTransport := c.transport
	c.transport = transport
	if oldTransport != nil {
		oldTransport.CloseIdleConnections()
	}
	return nil
}

// SetVerify 设置是否验证SSL证书。
func (c *Client) SetVerify(verify bool) *Client {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.verify == verify {
		return c
	}
	c.verify = verify
	if err := c.rebuildTransportLocked(); err != nil {
		c.configErr = err
	}
	return c
}

// SetTimeout 设置超时时间。
func (c *Client) SetTimeout(timeout time.Duration) *Client {
	c.mu.Lock()
	c.timeout = timeout
	c.mu.Unlock()
	return c
}

// SetMaxRedirects 设置最大重定向次数。
func (c *Client) SetMaxRedirects(maxRedirects int) *Client {
	c.mu.Lock()
	c.maxRedirects = maxRedirects
	c.mu.Unlock()
	return c
}

// Close 关闭客户端，释放所有空闲连接资源。
func (c *Client) Close() {
	c.mu.RLock()
	transport := c.transport
	c.mu.RUnlock()
	if transport != nil {
		transport.CloseIdleConnections()
	}
}

// SetHeaders 设置默认请求头（覆盖）。
func (c *Client) SetHeaders(headers map[string]string) *Client {
	c.mu.Lock()
	c.headers = cloneHeaders(headers)
	c.mu.Unlock()
	return c
}

// AddHeader 添加单个请求头。
func (c *Client) AddHeader(key, value string) *Client {
	c.mu.Lock()
	c.headers[http.CanonicalHeaderKey(key)] = value
	c.mu.Unlock()
	return c
}

// UpdateHeaders 更新请求头（合并）。
func (c *Client) UpdateHeaders(headers map[string]string) *Client {
	c.mu.Lock()
	for key, value := range headers {
		c.headers[http.CanonicalHeaderKey(key)] = value
	}
	c.mu.Unlock()
	return c
}

func cloneHeaders(headers map[string]string) map[string]string {
	result := make(map[string]string, len(headers))
	for key, value := range headers {
		result[http.CanonicalHeaderKey(key)] = value
	}
	return result
}

// SetCookies 设置每次请求携带的默认 Cookie（覆盖）。
func (c *Client) SetCookies(cookies map[string]string) *Client {
	c.mu.Lock()
	c.cookies = cloneStringMap(cookies)
	c.mu.Unlock()
	return c
}

// AddCookie 添加单个默认 Cookie。
func (c *Client) AddCookie(name, value string) *Client {
	c.mu.Lock()
	c.cookies[name] = value
	c.mu.Unlock()
	return c
}

// UpdateCookies 更新默认 Cookie。
func (c *Client) UpdateCookies(cookies interface{}) *Client {
	c.mu.Lock()
	defer c.mu.Unlock()

	switch value := cookies.(type) {
	case string:
		for _, item := range strings.Split(value, ";") {
			item = strings.TrimSpace(item)
			if idx := strings.Index(item, "="); idx > 0 {
				key := strings.TrimSpace(item[:idx])
				c.cookies[key] = strings.TrimSpace(item[idx+1:])
			}
		}
	case map[string]string:
		for key, item := range value {
			c.cookies[key] = item
		}
	}
	return c
}

// GetCookies 获取默认 Cookie 和已观察到的响应 Cookie。
func (c *Client) GetCookies() map[string]string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	result := cloneStringMap(c.observedCookies)
	for key, value := range c.cookies {
		result[key] = value
	}
	return result
}

// GetHeaders 获取当前所有默认请求头。
func (c *Client) GetHeaders() map[string]string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return cloneStringMap(c.headers)
}

// ClearCookies 清空默认 Cookie 和 Cookie Jar。
func (c *Client) ClearCookies() *Client {
	jar, _ := cookiejar.New(nil)
	c.mu.Lock()
	c.cookies = make(map[string]string)
	c.observedCookies = make(map[string]string)
	c.jar = jar
	c.mu.Unlock()
	return c
}

// ClearHeaders 清空默认请求头。
func (c *Client) ClearHeaders() *Client {
	c.mu.Lock()
	c.headers = make(map[string]string)
	c.mu.Unlock()
	return c
}

func cloneStringMap(values map[string]string) map[string]string {
	result := make(map[string]string, len(values))
	for key, value := range values {
		result[key] = value
	}
	return result
}
