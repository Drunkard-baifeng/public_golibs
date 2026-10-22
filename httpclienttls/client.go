package httpclienttls

import (
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	fhttp "github.com/bogdanfinn/fhttp"
	"github.com/bogdanfinn/fhttp/cookiejar"
	tlsclient "github.com/bogdanfinn/tls-client"
	"golang.org/x/net/publicsuffix"
)

const (
	defaultTimeout               = 30 * time.Second
	defaultMaxRedirects          = 5
	defaultMaxResponseBodyBytes  = int64(16 << 20)
	defaultMaxIdleConns          = 100
	defaultMaxIdleConnsPerHost   = 100
	defaultMaxConnectionsPerHost = 100
)

// Config configures a Client. An empty Profile randomly selects a built-in
// browser profile once and keeps it stable for the lifetime of the Client.
type Config struct {
	Timeout time.Duration
	// ProxyHandshakeTimeout bounds tunnel setup only, never the response stream.
	ProxyHandshakeTimeout   time.Duration
	MaxRedirects            int
	Verify                  *bool
	Proxy                   string
	ProxyType               string
	Profile                 string
	ProfilePool             []string
	UserAgent               string
	AcceptLanguage          string
	BrowserHeaders          *bool
	RandomTLSExtensionOrder *bool
	EnableSessionResumption *bool
	EnableHTTP3             bool
	EnableProtocolRacing    bool
	MaxResponseBodyBytes    int64
	MaxIdleConns            int
	MaxIdleConnsPerHost     int
	MaxConnsPerHost         int
	IdleConnTimeout         time.Duration
	DisableKeepAlives       bool
}

// BoolPtr is a convenience helper for pointer-valued boolean options.
func BoolPtr(value bool) *bool { return &value }

// Client is a concurrent HTTP client backed by tls-client.
type Client struct {
	mu sync.RWMutex

	inner tlsclient.HttpClient
	jar   fhttp.CookieJar

	profile                 Profile
	timeout                 time.Duration
	proxyHandshakeTimeout   time.Duration
	maxRedirects            int
	verify                  bool
	proxyURL                string
	userAgent               string
	browserHeaders          bool
	randomTLSExtensionOrder bool
	enableSessionResumption bool
	enableHTTP3             bool
	enableProtocolRacing    bool
	maxResponseBodyBytes    int64
	maxIdleConns            int
	maxIdleConnsPerHost     int
	maxConnsPerHost         int
	idleConnTimeout         time.Duration
	disableKeepAlives       bool

	headers           map[string]string
	headerOrder       []string
	customHeaderOrder bool
	cookies           map[string]string
	observedCookies   map[string]string
	configErr         error
	closed            bool
}

// New creates a client with a randomly selected TLS/browser profile.
// Construction errors are returned by ConfigError and subsequent requests.
func New() *Client { return NewWithConfig(Config{}) }

// NewWithConfig creates a client and stores construction errors on the Client.
func NewWithConfig(config Config) *Client {
	client, err := newClient(config)
	if client == nil {
		client = newClientShell(config)
	}
	client.configErr = err
	return client
}

// NewClient creates a client and directly returns construction errors.
func NewClient(config Config) (*Client, error) {
	client, err := newClient(config)
	if err != nil {
		if client != nil {
			client.Close()
		}
		return nil, err
	}
	return client, nil
}

func newClient(config Config) (*Client, error) {
	client := newClientShell(config)
	profile, err := selectProfile(config.Profile, config.ProfilePool)
	if err != nil {
		return client, err
	}
	client.profile = profile
	if value := strings.TrimSpace(config.AcceptLanguage); value != "" {
		client.profile.AcceptLanguage = value
	}
	client.userAgent = strings.TrimSpace(config.UserAgent)
	if client.userAgent == "" {
		client.userAgent = profile.UserAgent
	}
	if config.RandomTLSExtensionOrder == nil {
		client.randomTLSExtensionOrder = profile.RandomTLSExtensionOrder
	}
	client.profile.RandomTLSExtensionOrder = client.randomTLSExtensionOrder
	client.profile.SessionResumption = client.enableSessionResumption && profile.supportsSessionResumption
	client.headerOrder = append([]string(nil), profile.headerOrder...)

	proxyURL, err := normalizeProxyURL(config.Proxy, config.ProxyType)
	if err != nil {
		return client, err
	}
	client.proxyURL = proxyURL

	jar, err := cookiejar.New(&cookiejar.Options{PublicSuffixList: publicsuffix.List})
	if err != nil {
		return client, fmt.Errorf("create cookie jar: %w", err)
	}
	client.jar = jar
	if err := client.rebuildLocked(); err != nil {
		return client, err
	}
	return client, nil
}

func newClientShell(config Config) *Client {
	verify := true
	if config.Verify != nil {
		verify = *config.Verify
	}
	randomExtensionOrder := false
	if config.RandomTLSExtensionOrder != nil {
		randomExtensionOrder = *config.RandomTLSExtensionOrder
	}
	browserHeaders := true
	if config.BrowserHeaders != nil {
		browserHeaders = *config.BrowserHeaders
	}
	enableSessionResumption := true
	if config.EnableSessionResumption != nil {
		enableSessionResumption = *config.EnableSessionResumption
	}
	timeout := config.Timeout
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	proxyHandshakeTimeout := config.ProxyHandshakeTimeout
	if proxyHandshakeTimeout <= 0 {
		proxyHandshakeTimeout = defaultTimeout
	}
	maxRedirects := config.MaxRedirects
	if maxRedirects <= 0 {
		maxRedirects = defaultMaxRedirects
	}
	maxBodyBytes := config.MaxResponseBodyBytes
	if maxBodyBytes <= 0 {
		maxBodyBytes = defaultMaxResponseBodyBytes
	}
	maxIdleConns := config.MaxIdleConns
	if maxIdleConns <= 0 {
		maxIdleConns = defaultMaxIdleConns
	}
	maxIdleConnsPerHost := config.MaxIdleConnsPerHost
	if maxIdleConnsPerHost <= 0 {
		maxIdleConnsPerHost = defaultMaxIdleConnsPerHost
	}
	maxConnsPerHost := config.MaxConnsPerHost
	if maxConnsPerHost <= 0 {
		maxConnsPerHost = defaultMaxConnectionsPerHost
	}
	idleConnTimeout := config.IdleConnTimeout
	if idleConnTimeout <= 0 {
		idleConnTimeout = 90 * time.Second
	}
	return &Client{
		timeout:                 timeout,
		proxyHandshakeTimeout:   proxyHandshakeTimeout,
		maxRedirects:            maxRedirects,
		verify:                  verify,
		browserHeaders:          browserHeaders,
		randomTLSExtensionOrder: randomExtensionOrder,
		enableSessionResumption: enableSessionResumption,
		enableHTTP3:             config.EnableHTTP3,
		enableProtocolRacing:    config.EnableProtocolRacing,
		maxResponseBodyBytes:    maxBodyBytes,
		maxIdleConns:            maxIdleConns,
		maxIdleConnsPerHost:     maxIdleConnsPerHost,
		maxConnsPerHost:         maxConnsPerHost,
		idleConnTimeout:         idleConnTimeout,
		disableKeepAlives:       config.DisableKeepAlives,
		headers:                 make(map[string]string),
		cookies:                 make(map[string]string),
		observedCookies:         make(map[string]string),
	}
}

func (c *Client) rebuildLocked() error {
	if c.jar == nil {
		jar, err := cookiejar.New(&cookiejar.Options{PublicSuffixList: publicsuffix.List})
		if err != nil {
			return fmt.Errorf("create cookie jar: %w", err)
		}
		c.jar = jar
	}
	if c.profile.Name == "" {
		return fmt.Errorf("httpclienttls: TLS profile is not initialized")
	}
	idleConnTimeout := c.idleConnTimeout
	clientProfile := c.profile.client
	if c.enableSessionResumption && c.profile.supportsSessionResumption {
		clientProfile = c.profile.resumptionClient
	}
	options := []tlsclient.HttpClientOption{
		tlsclient.WithTimeoutSeconds(0),
		tlsclient.WithClientProfile(clientProfile),
		tlsclient.WithNotFollowRedirects(),
		tlsclient.WithCatchPanics(),
		tlsclient.WithTransportOptions(&tlsclient.TransportOptions{
			IdleConnTimeout:     &idleConnTimeout,
			MaxIdleConns:        c.maxIdleConns,
			MaxIdleConnsPerHost: c.maxIdleConnsPerHost,
			MaxConnsPerHost:     c.maxConnsPerHost,
			DisableKeepAlives:   c.disableKeepAlives,
			DisableCompression:  false,
		}),
	}
	if c.randomTLSExtensionOrder {
		options = append(options, tlsclient.WithRandomTLSExtensionOrder())
	}
	// UDP/HTTP3 racing cannot use the TCP proxy tunnel. Never bypass it.
	if !c.enableHTTP3 || c.proxyURL != "" {
		options = append(options, tlsclient.WithDisableHttp3())
	} else if c.enableProtocolRacing {
		options = append(options, tlsclient.WithProtocolRacing())
	}
	if !c.verify {
		options = append(options, tlsclient.WithInsecureSkipVerify())
	}
	if c.proxyURL != "" {
		dialer, err := newProxyDialer(c.proxyURL, c.proxyHandshakeTimeout)
		if err != nil {
			return err
		}
		options = append(options, tlsclient.WithDialContext(dialer.DialContext))
	}

	inner, err := tlsclient.NewHttpClient(tlsclient.NewNoopLogger(), options...)
	if err != nil {
		return fmt.Errorf("create TLS client: %w", err)
	}
	old := c.inner
	c.inner = inner
	if old != nil {
		old.CloseIdleConnections()
	}
	return nil
}

// Profile returns the fingerprint selected for this Client.
func (c *Client) Profile() Profile {
	c.mu.RLock()
	defer c.mu.RUnlock()
	profile := publicProfile(c.profile)
	profile.UserAgent = c.userAgent
	return profile
}

// ConfigError returns the most recent construction or reconfiguration error.
func (c *Client) ConfigError() error {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.configErr
}

// ProxyError is kept for API compatibility with httpclient.
func (c *Client) ProxyError() error { return c.ConfigError() }

// ConfigureProxy validates and applies a proxy immediately.
func (c *Client) ConfigureProxy(proxyValue, proxyType string) error {
	proxyURL, err := normalizeProxyURL(proxyValue, proxyType)
	c.mu.Lock()
	defer c.mu.Unlock()
	if err != nil {
		c.configErr = err
		return err
	}
	if c.closed {
		return fmt.Errorf("httpclienttls: client is closed")
	}
	c.proxyURL = proxyURL
	c.configErr = c.rebuildLocked()
	return c.configErr
}

// SetProxy is the chainable proxy setter.
func (c *Client) SetProxy(proxyValue, proxyType string) *Client {
	_ = c.ConfigureProxy(proxyValue, proxyType)
	return c
}

func (c *Client) SetHTTPProxy(proxyValue string) *Client {
	return c.SetProxy(proxyValue, "http")
}

func (c *Client) SetSocks5Proxy(proxyValue string) *Client {
	return c.SetProxy(proxyValue, "socks5")
}

func (c *Client) ClearProxy() *Client { return c.SetProxy("", "") }

func (c *Client) SetVerify(verify bool) *Client {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed || c.verify == verify {
		return c
	}
	c.verify = verify
	c.configErr = c.rebuildLocked()
	return c
}

func (c *Client) SetTimeout(timeout time.Duration) *Client {
	c.mu.Lock()
	c.timeout = timeout
	c.mu.Unlock()
	return c
}

func (c *Client) SetMaxRedirects(maxRedirects int) *Client {
	c.mu.Lock()
	c.maxRedirects = maxRedirects
	c.mu.Unlock()
	return c
}

func (c *Client) SetHeaders(headers map[string]string) *Client {
	c.mu.Lock()
	c.headers = cloneHeaderMap(headers)
	c.mu.Unlock()
	return c
}

func (c *Client) AddHeader(key, value string) *Client {
	c.mu.Lock()
	c.headers[http.CanonicalHeaderKey(key)] = value
	c.mu.Unlock()
	return c
}

func (c *Client) UpdateHeaders(headers map[string]string) *Client {
	c.mu.Lock()
	for key, value := range headers {
		c.headers[http.CanonicalHeaderKey(key)] = value
	}
	c.mu.Unlock()
	return c
}

func (c *Client) GetHeaders() map[string]string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return cloneStringMap(c.headers)
}

func (c *Client) ClearHeaders() *Client {
	c.mu.Lock()
	c.headers = make(map[string]string)
	c.mu.Unlock()
	return c
}

// SetHeaderOrder sets the default order used for regular HTTP headers.
// HTTP/2 pseudo-header order remains controlled by the selected TLS profile.
func (c *Client) SetHeaderOrder(order []string) *Client {
	c.mu.Lock()
	c.headerOrder = normalizeHeaderOrder(order)
	c.customHeaderOrder = true
	c.mu.Unlock()
	return c
}

// ResetHeaderOrder restores the selected browser profile's request-aware order.
func (c *Client) ResetHeaderOrder() *Client {
	c.mu.Lock()
	c.headerOrder = append([]string(nil), c.profile.headerOrder...)
	c.customHeaderOrder = false
	c.mu.Unlock()
	return c
}

// GetHeaderOrder returns a copy of the configured regular HTTP header order.
func (c *Client) GetHeaderOrder() []string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return append([]string(nil), c.headerOrder...)
}

func (c *Client) SetCookies(cookies map[string]string) *Client {
	c.mu.Lock()
	c.cookies = cloneStringMap(cookies)
	c.mu.Unlock()
	return c
}

func (c *Client) AddCookie(name, value string) *Client {
	c.mu.Lock()
	c.cookies[name] = value
	c.mu.Unlock()
	return c
}

func (c *Client) UpdateCookies(cookies any) *Client {
	c.mu.Lock()
	defer c.mu.Unlock()
	switch value := cookies.(type) {
	case string:
		for _, item := range strings.Split(value, ";") {
			if index := strings.Index(item, "="); index > 0 {
				c.cookies[strings.TrimSpace(item[:index])] = strings.TrimSpace(item[index+1:])
			}
		}
	case map[string]string:
		for name, value := range value {
			c.cookies[name] = value
		}
	}
	return c
}

func (c *Client) GetCookies() map[string]string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	result := cloneStringMap(c.observedCookies)
	for name, value := range c.cookies {
		result[name] = value
	}
	return result
}

func (c *Client) ClearCookies() *Client {
	jar, err := cookiejar.New(&cookiejar.Options{PublicSuffixList: publicsuffix.List})
	c.mu.Lock()
	defer c.mu.Unlock()
	if err != nil {
		c.configErr = fmt.Errorf("create cookie jar: %w", err)
		return c
	}
	c.jar = jar
	c.cookies = make(map[string]string)
	c.observedCookies = make(map[string]string)
	return c
}

// SetCookie stores a scoped cookie directly in the Client cookie jar.
func (c *Client) SetCookie(targetURL, name, value, domain, path string, secure bool) error {
	parsedURL, err := url.Parse(strings.TrimSpace(targetURL))
	if err != nil || parsedURL.Host == "" {
		return fmt.Errorf("invalid cookie URL %q", targetURL)
	}
	if strings.TrimSpace(name) == "" {
		return fmt.Errorf("cookie name is empty")
	}
	if path == "" {
		path = "/"
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.jar == nil {
		return fmt.Errorf("httpclienttls: cookie jar is not initialized")
	}
	c.jar.SetCookies(parsedURL, []*fhttp.Cookie{{
		Name: name, Value: value, Domain: strings.TrimSpace(domain), Path: path, Secure: secure,
	}})
	return nil
}

func (c *Client) CookieValue(targetURL, name string) string {
	parsedURL, err := url.Parse(strings.TrimSpace(targetURL))
	if err != nil || parsedURL.Host == "" {
		return ""
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.jar == nil {
		return ""
	}
	for _, cookie := range c.jar.Cookies(parsedURL) {
		if cookie != nil && cookie.Name == name {
			return cookie.Value
		}
	}
	return ""
}

// Close releases idle connections. The Client must not be reused afterward.
func (c *Client) Close() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return
	}
	c.closed = true
	if c.inner != nil {
		c.inner.CloseIdleConnections()
	}
}

func normalizeProxyURL(proxyValue, proxyType string) (string, error) {
	proxyValue = strings.TrimSpace(proxyValue)
	if proxyValue == "" {
		return "", nil
	}
	proxyType = strings.ToLower(strings.TrimSpace(proxyType))
	if proxyType == "" {
		proxyType = "http"
	}
	if proxyType != "http" && proxyType != "https" && proxyType != "socks5" && proxyType != "socks5h" {
		return "", fmt.Errorf("unsupported proxy type %q", proxyType)
	}
	if !strings.Contains(proxyValue, "://") {
		if _, _, err := net.SplitHostPort(proxyValue); err == nil {
			proxyValue = proxyType + "://" + proxyValue
		} else {
			parts := strings.Split(proxyValue, ":")
			if len(parts) == 4 {
				proxyValue = (&url.URL{
					Scheme: proxyType,
					Host:   net.JoinHostPort(parts[0], parts[1]),
					User:   url.UserPassword(parts[2], parts[3]),
				}).String()
			} else {
				proxyValue = proxyType + "://" + proxyValue
			}
		}
	}
	parsedURL, err := url.Parse(proxyValue)
	if err != nil {
		return "", fmt.Errorf("parse proxy: %w", err)
	}
	switch strings.ToLower(parsedURL.Scheme) {
	case "http", "https", "socks5", "socks5h":
	default:
		return "", fmt.Errorf("unsupported proxy scheme %q", parsedURL.Scheme)
	}
	if parsedURL.Hostname() == "" {
		return "", fmt.Errorf("proxy host is empty")
	}
	port, err := strconv.Atoi(parsedURL.Port())
	if err != nil || port < 1 || port > 65535 {
		return "", fmt.Errorf("invalid proxy port %q", parsedURL.Port())
	}
	if parsedURL.RawQuery != "" || parsedURL.Fragment != "" || (parsedURL.Path != "" && parsedURL.Path != "/") {
		return "", fmt.Errorf("proxy URL cannot contain a path, query, or fragment")
	}
	parsedURL.Scheme = strings.ToLower(parsedURL.Scheme)
	parsedURL.Path = ""
	return parsedURL.String(), nil
}

func cloneStringMap(values map[string]string) map[string]string {
	result := make(map[string]string, len(values))
	for key, value := range values {
		result[key] = value
	}
	return result
}

func cloneHeaderMap(values map[string]string) map[string]string {
	result := make(map[string]string, len(values))
	for key, value := range values {
		result[http.CanonicalHeaderKey(key)] = value
	}
	return result
}
