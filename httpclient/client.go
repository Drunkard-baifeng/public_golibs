package httpclient

import (
	"context"
	"crypto/tls"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"time"

	"golang.org/x/net/proxy"
)

// Client HTTP客户端
type Client struct {
	httpClient        *http.Client
	transport         *http.Transport
	headers           map[string]string
	cookies           map[string]string
	timeout           time.Duration
	maxRedirects      int
	verify            bool
	maxIdleConns      int
	idleConnTimeout   time.Duration
	disableKeepAlives bool
	proxyURL          string
	proxyType         string // "http" 或 "socks5"
	jar               *cookiejar.Jar
}

const (
	defaultMaxIdleConns    = 100
	defaultIdleConnTimeout = 5 * time.Minute
)

// New 创建新的HTTP客户端
func New() *Client {
	jar, _ := cookiejar.New(nil)

	c := &Client{
		headers:           make(map[string]string),
		cookies:           make(map[string]string),
		timeout:           30 * time.Second,
		maxRedirects:      5,
		verify:            true,
		jar:               jar,
		proxyType:         "",
		maxIdleConns:      defaultMaxIdleConns,
		idleConnTimeout:   defaultIdleConnTimeout,
		disableKeepAlives: false,
	}

	// 创建 Transport，使用动态代理函数
	c.transport = &http.Transport{
		MaxIdleConns:          c.maxIdleConns,
		MaxIdleConnsPerHost:   100,
		MaxConnsPerHost:       100,
		IdleConnTimeout:       c.idleConnTimeout,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
		DisableKeepAlives:     false,
		Proxy:                 c.getProxyFunc(), // 动态代理函数
	}

	c.httpClient = &http.Client{
		Transport: c.transport,
		Jar:       jar,
		Timeout:   30 * time.Second,
	}

	return c
}

// getProxyFunc 返回动态代理函数（用于 HTTP 代理）
func (c *Client) getProxyFunc() func(*http.Request) (*url.URL, error) {
	return func(req *http.Request) (*url.URL, error) {
		if c.proxyURL == "" || c.proxyType == "socks5" {
			return nil, nil
		}
		return url.Parse(c.proxyURL)
	}
}

// Config 客户端配置选项
type Config struct {
	Timeout           time.Duration
	MaxRedirects      int
	Verify            bool
	Proxy             string
	ProxyType         string // "http" 或 "socks5"
	MaxIdleConns      int
	IdleConnTimeout   time.Duration
	DisableKeepAlives bool // 禁用连接复用（每次请求后立即关闭连接）
}

// NewWithConfig 使用配置创建HTTP客户端
func NewWithConfig(cfg Config) *Client {
	c := New()

	if cfg.Timeout > 0 {
		c.SetTimeout(cfg.Timeout)
	}
	if cfg.MaxIdleConns > 0 {
		c.maxIdleConns = cfg.MaxIdleConns
		c.transport.MaxIdleConns = cfg.MaxIdleConns
	}
	if cfg.IdleConnTimeout > 0 {
		c.idleConnTimeout = cfg.IdleConnTimeout
		c.transport.IdleConnTimeout = cfg.IdleConnTimeout
	}
	// 禁用 Keep-Alive（必须在设置代理之前，因为会影响 Transport）
	if cfg.DisableKeepAlives {
		c.disableKeepAlives = true
		c.transport.DisableKeepAlives = true
	}
	if cfg.MaxRedirects > 0 {
		c.SetMaxRedirects(cfg.MaxRedirects)
	}
	if !cfg.Verify {
		c.SetVerify(false)
	}
	if cfg.Proxy != "" {
		proxyType := cfg.ProxyType
		if proxyType == "" {
			proxyType = "http"
		}
		c.SetProxy(cfg.Proxy, proxyType)
	}

	return c
}

// SetProxy 设置代理
// proxyStr: 代理地址，格式: "ip:port" 或 "ip:port:user:pass" 或完整URL
// proxyType: 代理类型 "http" 或 "socks5"
func (c *Client) SetProxy(proxyStr string, proxyType string) *Client {
	oldProxyType := c.proxyType

	if proxyStr == "" {
		c.proxyURL = ""
		c.proxyType = ""
		// 如果之前是 SOCKS5，需要重建 Transport
		if oldProxyType == "socks5" {
			c.rebuildTransport()
		}
		// 关闭旧的空闲连接
		c.transport.CloseIdleConnections()
		return c
	}

	// 解析代理类型和地址
	newProxyType := strings.ToLower(proxyType)

	// 如果已经包含协议头，从中提取类型
	if strings.HasPrefix(proxyStr, "socks5://") {
		newProxyType = "socks5"
		c.proxyURL = proxyStr
	} else if strings.HasPrefix(proxyStr, "http://") || strings.HasPrefix(proxyStr, "https://") {
		newProxyType = "http"
		c.proxyURL = proxyStr
	} else {
		// 解析代理字符串
		parts := strings.Split(proxyStr, ":")

		if newProxyType == "socks5" {
			if len(parts) == 2 {
				c.proxyURL = "socks5://" + parts[0] + ":" + parts[1]
			} else if len(parts) == 4 {
				// ip:port:user:pass 格式
				c.proxyURL = "socks5://" + parts[2] + ":" + parts[3] + "@" + parts[0] + ":" + parts[1]
			}
		} else {
			newProxyType = "http"
			if len(parts) == 2 {
				c.proxyURL = "http://" + parts[0] + ":" + parts[1]
			} else if len(parts) == 4 {
				c.proxyURL = "http://" + parts[2] + ":" + parts[3] + "@" + parts[0] + ":" + parts[1]
			}
		}
	}

	c.proxyType = newProxyType

	// 判断是否需要重建 Transport
	// 只有在 HTTP <-> SOCKS5 切换时才需要重建
	needRebuild := (oldProxyType == "socks5" && newProxyType != "socks5") ||
		(oldProxyType != "socks5" && newProxyType == "socks5")

	if needRebuild {
		c.rebuildTransport()
	} else {
		// HTTP 代理切换：只需关闭旧连接，Proxy 函数会自动使用新地址
		c.transport.CloseIdleConnections()
	}

	return c
}

// SetHTTPProxy 快捷方法：设置 HTTP 代理
func (c *Client) SetHTTPProxy(proxyStr string) *Client {
	return c.SetProxy(proxyStr, "http")
}

// SetSocks5Proxy 快捷方法：设置 SOCKS5 代理
func (c *Client) SetSocks5Proxy(proxyStr string) *Client {
	return c.SetProxy(proxyStr, "socks5")
}

// ClearProxy 清除代理
func (c *Client) ClearProxy() *Client {
	return c.SetProxy("", "")
}

// rebuildTransport 重建Transport（仅在必要时调用）
func (c *Client) rebuildTransport() {
	// 关闭旧的连接
	if c.transport != nil {
		c.transport.CloseIdleConnections()
	}

	transport := &http.Transport{
		MaxIdleConns:          c.maxIdleConns,
		MaxIdleConnsPerHost:   100,
		MaxConnsPerHost:       100,
		IdleConnTimeout:       c.idleConnTimeout,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
		DisableKeepAlives:     c.disableKeepAlives,
	}

	// SSL验证配置
	if !c.verify {
		transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	}

	// 代理配置
	if c.proxyType == "socks5" && c.proxyURL != "" {
		// SOCKS5 代理需要自定义 DialContext
		proxyURL, err := url.Parse(c.proxyURL)
		if err == nil {
			var auth *proxy.Auth
			if proxyURL.User != nil {
				auth = &proxy.Auth{
					User: proxyURL.User.Username(),
				}
				auth.Password, _ = proxyURL.User.Password()
			}

			dialer, err := proxy.SOCKS5("tcp", proxyURL.Host, auth, proxy.Direct)
			if err == nil {
				transport.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
					return dialer.Dial(network, addr)
				}
			}
		}
	} else {
		// HTTP 代理使用动态 Proxy 函数
		transport.Proxy = c.getProxyFunc()
	}

	c.transport = transport
	c.httpClient.Transport = transport
}

// SetVerify 设置是否验证SSL证书
func (c *Client) SetVerify(verify bool) *Client {
	if c.verify == verify {
		return c // 没有变化，不需要更新
	}
	c.verify = verify

	// 更新 TLS 配置
	if c.transport.TLSClientConfig == nil {
		c.transport.TLSClientConfig = &tls.Config{}
	}
	c.transport.TLSClientConfig.InsecureSkipVerify = !verify

	return c
}

// SetTimeout 设置超时时间
func (c *Client) SetTimeout(timeout time.Duration) *Client {
	c.timeout = timeout
	c.httpClient.Timeout = timeout
	return c
}

// SetMaxRedirects 设置最大重定向次数
func (c *Client) SetMaxRedirects(maxRedirects int) *Client {
	c.maxRedirects = maxRedirects
	c.httpClient.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) >= maxRedirects {
			return http.ErrUseLastResponse
		}
		return nil
	}
	return c
}

// Close 关闭客户端，释放所有连接资源
func (c *Client) Close() {
	if c.transport != nil {
		c.transport.CloseIdleConnections()
	}
}

// SetHeaders 设置默认请求头（覆盖）
func (c *Client) SetHeaders(headers map[string]string) *Client {
	c.headers = make(map[string]string)
	for k, v := range headers {
		c.headers[normalizeHeaderKey(k)] = v
	}
	return c
}

// AddHeader 添加单个请求头
func (c *Client) AddHeader(key, value string) *Client {
	c.headers[normalizeHeaderKey(key)] = value
	return c
}

// UpdateHeaders 更新请求头（合并）
func (c *Client) UpdateHeaders(headers map[string]string) *Client {
	for k, v := range headers {
		c.headers[normalizeHeaderKey(k)] = v
	}
	return c
}

// normalizeHeaderKey 标准化header key
func normalizeHeaderKey(key string) string {
	parts := strings.Split(key, "-")
	for i, p := range parts {
		if len(p) > 0 {
			parts[i] = strings.ToUpper(p[:1]) + strings.ToLower(p[1:])
		}
	}
	return strings.Join(parts, "-")
}

// SetCookies 设置Cookie（覆盖）
func (c *Client) SetCookies(cookies map[string]string) *Client {
	c.cookies = make(map[string]string)
	for k, v := range cookies {
		c.cookies[k] = v
	}
	return c
}

// AddCookie 添加单个Cookie
func (c *Client) AddCookie(name, value string) *Client {
	c.cookies[name] = value
	return c
}

// UpdateCookies 更新Cookie
func (c *Client) UpdateCookies(cookies interface{}) *Client {
	switch v := cookies.(type) {
	case string:
		// 解析Cookie字符串
		for _, item := range strings.Split(v, ";") {
			item = strings.TrimSpace(item)
			if idx := strings.Index(item, "="); idx > 0 {
				key := strings.TrimSpace(item[:idx])
				value := strings.TrimSpace(item[idx+1:])
				c.cookies[key] = value
			}
		}
	case map[string]string:
		for k, v := range v {
			c.cookies[k] = v
		}
	}
	return c
}

// GetCookies 获取当前所有Cookie
func (c *Client) GetCookies() map[string]string {
	result := make(map[string]string)
	for k, v := range c.cookies {
		result[k] = v
	}
	return result
}

// GetHeaders 获取当前所有默认请求头
func (c *Client) GetHeaders() map[string]string {
	result := make(map[string]string)
	for k, v := range c.headers {
		result[k] = v
	}
	return result
}

// ClearCookies 清空Cookie
func (c *Client) ClearCookies() *Client {
	c.cookies = make(map[string]string)
	jar, _ := cookiejar.New(nil)
	c.jar = jar
	c.httpClient.Jar = jar
	return c
}

// ClearHeaders 清空默认请求头
func (c *Client) ClearHeaders() *Client {
	c.headers = make(map[string]string)
	return c
}
