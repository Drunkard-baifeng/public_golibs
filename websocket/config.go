package websocket

import (
	"crypto/tls"
	"net/http"
	"strings"
	"time"
)

const (
	defaultOpenTimeout          = 10 * time.Second
	defaultCloseTimeout         = 10 * time.Second
	defaultWriteTimeout         = 10 * time.Second
	defaultHeartbeatInterval    = 20 * time.Second
	defaultHeartbeatTimeout     = 20 * time.Second
	defaultAppHeartbeatInterval = 30 * time.Second
	defaultMaxMessageSize       = 1024 * 1024
)

// ProxyType 代理协议类型。
type ProxyType string

const (
	ProxyNone    ProxyType = ""
	ProxyHTTP    ProxyType = "http"
	ProxyHTTPS   ProxyType = "https"
	ProxySocks5  ProxyType = "socks5"
	ProxySocks5H ProxyType = "socks5h"
)

// ProxyConfig WebSocket 连接代理配置。
type ProxyConfig struct {
	Address  string
	Type     ProxyType
	Username string
	Password string
}

// HeartbeatConfig 是 WebSocket 协议层 ping 心跳配置。
type HeartbeatConfig struct {
	Enabled  bool
	Interval time.Duration
	Timeout  time.Duration
}

// AppHeartbeatMode 应用层心跳消息发送模式。
type AppHeartbeatMode string

const (
	AppHeartbeatAuto   AppHeartbeatMode = ""
	AppHeartbeatText   AppHeartbeatMode = "text"
	AppHeartbeatJSON   AppHeartbeatMode = "json"
	AppHeartbeatHex    AppHeartbeatMode = "hex"
	AppHeartbeatBinary AppHeartbeatMode = "binary"
)

// ApplicationHeartbeatConfig 应用层业务心跳配置。
type ApplicationHeartbeatConfig struct {
	Enabled   bool
	Message   any
	Interval  time.Duration
	Mode      AppHeartbeatMode
	Immediate bool
}

// Config WebSocket 客户端配置。
type Config struct {
	URL               string
	Headers           http.Header
	Subprotocols      []string
	Origin            string
	UserAgent         string
	OpenTimeout       time.Duration
	CloseTimeout      time.Duration
	ReadTimeout       time.Duration
	WriteTimeout      time.Duration
	MaxMessageSize    int64
	EnableCompression bool
	TLSConfig         *tls.Config
	Proxy             *ProxyConfig
	UseEnvProxy       bool
	Heartbeat         HeartbeatConfig
	AppHeartbeat      ApplicationHeartbeatConfig
	AutoConnect       bool
	ReadBufferSize    int
	WriteBufferSize   int
}

// DefaultConfig 返回适合 Go 使用习惯的默认配置。
func DefaultConfig(url string) Config {
	return Config{
		URL:               url,
		Headers:           make(http.Header),
		OpenTimeout:       defaultOpenTimeout,
		CloseTimeout:      defaultCloseTimeout,
		WriteTimeout:      defaultWriteTimeout,
		MaxMessageSize:    defaultMaxMessageSize,
		EnableCompression: true,
		Heartbeat: HeartbeatConfig{
			Enabled:  false,
			Interval: defaultHeartbeatInterval,
			Timeout:  defaultHeartbeatTimeout,
		},
		AppHeartbeat: ApplicationHeartbeatConfig{
			Message:  "ping",
			Interval: defaultAppHeartbeatInterval,
			Mode:     AppHeartbeatAuto,
		},
	}
}

// Option 用于链式调整客户端配置。
type Option func(*Config)

// WithHeaders 设置请求头。
func WithHeaders(headers map[string]string) Option {
	return func(cfg *Config) {
		if cfg.Headers == nil {
			cfg.Headers = make(http.Header)
		}
		for key, value := range headers {
			cfg.Headers.Set(key, value)
		}
	}
}

// WithHeader 设置单个请求头。
func WithHeader(key, value string) Option {
	return func(cfg *Config) {
		if cfg.Headers == nil {
			cfg.Headers = make(http.Header)
		}
		cfg.Headers.Set(key, value)
	}
}

// WithSubprotocols 设置 WebSocket 子协议。
func WithSubprotocols(protocols ...string) Option {
	return func(cfg *Config) {
		cfg.Subprotocols = append([]string(nil), protocols...)
	}
}

// WithOrigin 设置握手请求的 Origin。
func WithOrigin(origin string) Option {
	return func(cfg *Config) {
		cfg.Origin = origin
	}
}

// WithUserAgent 设置握手请求的 User-Agent。
func WithUserAgent(userAgent string) Option {
	return func(cfg *Config) {
		cfg.UserAgent = userAgent
	}
}

// WithTimeout 设置连接、读、写超时。读超时传 0 表示阻塞等待消息。
func WithTimeout(openTimeout, readTimeout, writeTimeout time.Duration) Option {
	return func(cfg *Config) {
		cfg.OpenTimeout = openTimeout
		cfg.ReadTimeout = readTimeout
		cfg.WriteTimeout = writeTimeout
	}
}

// WithReadTimeout 设置接收消息超时，传 0 表示阻塞等待消息。
func WithReadTimeout(timeout time.Duration) Option {
	return func(cfg *Config) {
		cfg.ReadTimeout = timeout
	}
}

// WithWriteTimeout 设置发送消息超时，传 0 使用默认值，传负数表示不设置写超时。
func WithWriteTimeout(timeout time.Duration) Option {
	return func(cfg *Config) {
		cfg.WriteTimeout = timeout
	}
}

// WithCloseTimeout 设置关闭连接时发送 close frame 的超时。
func WithCloseTimeout(timeout time.Duration) Option {
	return func(cfg *Config) {
		cfg.CloseTimeout = timeout
	}
}

// WithMaxMessageSize 设置最大接收消息大小，传 0 表示不限制。
func WithMaxMessageSize(size int64) Option {
	return func(cfg *Config) {
		cfg.MaxMessageSize = size
	}
}

// WithCompression 设置是否开启 permessage-deflate 压缩。
func WithCompression(enabled bool) Option {
	return func(cfg *Config) {
		cfg.EnableCompression = enabled
	}
}

// WithTLSConfig 设置 TLS 配置。
func WithTLSConfig(tlsConfig *tls.Config) Option {
	return func(cfg *Config) {
		cfg.TLSConfig = tlsConfig
	}
}

// WithProxy 设置代理，不传账号密码。
func WithProxy(address string, proxyType ProxyType) Option {
	return WithProxyAuth(address, proxyType, "", "")
}

// WithProxyAuth 设置代理和账号密码。
func WithProxyAuth(address string, proxyType ProxyType, username, password string) Option {
	return func(cfg *Config) {
		cfg.Proxy = &ProxyConfig{
			Address:  address,
			Type:     proxyType,
			Username: username,
			Password: password,
		}
		cfg.UseEnvProxy = false
	}
}

// WithProxyConfig 使用完整代理配置。
func WithProxyConfig(proxy ProxyConfig) Option {
	return func(cfg *Config) {
		cfg.Proxy = &proxy
		cfg.UseEnvProxy = false
	}
}

// WithoutProxy 禁用代理。
func WithoutProxy() Option {
	return func(cfg *Config) {
		cfg.Proxy = nil
		cfg.UseEnvProxy = false
	}
}

// WithEnvProxy 使用系统或环境变量代理。
func WithEnvProxy() Option {
	return func(cfg *Config) {
		cfg.Proxy = nil
		cfg.UseEnvProxy = true
	}
}

// WithHeartbeat 设置协议层 ping 心跳。
func WithHeartbeat(enabled bool, interval, timeout time.Duration) Option {
	return func(cfg *Config) {
		cfg.Heartbeat = HeartbeatConfig{
			Enabled:  enabled,
			Interval: interval,
			Timeout:  timeout,
		}
	}
}

// WithAppHeartbeat 设置应用层业务心跳。
func WithAppHeartbeat(message any, interval time.Duration, mode AppHeartbeatMode, immediate bool) Option {
	return func(cfg *Config) {
		cfg.AppHeartbeat = ApplicationHeartbeatConfig{
			Enabled:   true,
			Message:   message,
			Interval:  interval,
			Mode:      mode,
			Immediate: immediate,
		}
	}
}

// WithoutAppHeartbeat 禁用应用层业务心跳。
func WithoutAppHeartbeat() Option {
	return func(cfg *Config) {
		cfg.AppHeartbeat.Enabled = false
	}
}

// WithAutoConnect 设置创建客户端后是否自动连接。
func WithAutoConnect(enabled bool) Option {
	return func(cfg *Config) {
		cfg.AutoConnect = enabled
	}
}

func (cfg *Config) normalize() {
	if cfg.Headers == nil {
		cfg.Headers = make(http.Header)
	}
	if cfg.OpenTimeout == 0 {
		cfg.OpenTimeout = defaultOpenTimeout
	} else if cfg.OpenTimeout < 0 {
		cfg.OpenTimeout = 0
	}
	if cfg.CloseTimeout == 0 {
		cfg.CloseTimeout = defaultCloseTimeout
	} else if cfg.CloseTimeout < 0 {
		cfg.CloseTimeout = 0
	}
	if cfg.WriteTimeout == 0 {
		cfg.WriteTimeout = defaultWriteTimeout
	} else if cfg.WriteTimeout < 0 {
		cfg.WriteTimeout = 0
	}
	if cfg.MaxMessageSize < 0 {
		cfg.MaxMessageSize = 0
	}
	if cfg.Heartbeat.Enabled {
		if cfg.Heartbeat.Interval == 0 {
			cfg.Heartbeat.Interval = defaultHeartbeatInterval
		}
		if cfg.Heartbeat.Timeout == 0 {
			cfg.Heartbeat.Timeout = defaultHeartbeatTimeout
		}
	}
	if cfg.AppHeartbeat.Enabled {
		if cfg.AppHeartbeat.Interval == 0 {
			cfg.AppHeartbeat.Interval = defaultAppHeartbeatInterval
		}
		if cfg.AppHeartbeat.Message == nil {
			cfg.AppHeartbeat.Message = "ping"
		}
		cfg.AppHeartbeat.Mode = AppHeartbeatMode(strings.ToLower(strings.TrimSpace(string(cfg.AppHeartbeat.Mode))))
	}
}
