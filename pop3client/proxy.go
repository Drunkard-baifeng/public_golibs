package pop3client

import (
	"bufio"
	"encoding/base64"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"golang.org/x/net/proxy"
)

// ProxyType 代理协议类型。
type ProxyType string

const (
	ProxyNone   ProxyType = ""
	ProxyHTTP   ProxyType = "http"
	ProxySocks5 ProxyType = "socks5"
)

// ProxyConfig POP3 连接代理配置。
type ProxyConfig struct {
	Type     ProxyType
	Host     string
	Port     int
	Username string
	Password string
}

func (p ProxyConfig) enabled() bool {
	return strings.TrimSpace(p.Host) != "" && p.Port > 0
}

func (p ProxyConfig) address() string {
	return net.JoinHostPort(p.Host, strconv.Itoa(p.Port))
}

func (p ProxyConfig) normalized() ProxyConfig {
	p.Type = ProxyType(strings.ToLower(strings.TrimSpace(string(p.Type))))
	if p.Type == "" {
		p.Type = ProxySocks5
	}
	return p
}

func dialWithProxy(network, targetAddr string, timeout time.Duration, proxyConfig ProxyConfig) (net.Conn, error) {
	if !proxyConfig.enabled() {
		dialer := net.Dialer{Timeout: timeout}
		return dialer.Dial(network, targetAddr)
	}

	proxyConfig = proxyConfig.normalized()
	switch proxyConfig.Type {
	case ProxySocks5:
		return dialSocks5Proxy(network, targetAddr, timeout, proxyConfig)
	case ProxyHTTP:
		return dialHTTPProxy(network, targetAddr, timeout, proxyConfig)
	default:
		return nil, fmt.Errorf("不支持的代理类型: %s, 支持的类型: socks5, http", proxyConfig.Type)
	}
}

func dialSocks5Proxy(network, targetAddr string, timeout time.Duration, proxyConfig ProxyConfig) (net.Conn, error) {
	baseDialer := &net.Dialer{Timeout: timeout}
	var auth *proxy.Auth
	if proxyConfig.Username != "" || proxyConfig.Password != "" {
		auth = &proxy.Auth{
			User:     proxyConfig.Username,
			Password: proxyConfig.Password,
		}
	}

	dialer, err := proxy.SOCKS5(network, proxyConfig.address(), auth, baseDialer)
	if err != nil {
		return nil, fmt.Errorf("邮箱代理异常: %w", err)
	}

	conn, err := dialer.Dial(network, targetAddr)
	if err != nil {
		return nil, fmt.Errorf("邮箱代理异常: %w", err)
	}
	return conn, nil
}

func dialHTTPProxy(network, targetAddr string, timeout time.Duration, proxyConfig ProxyConfig) (net.Conn, error) {
	if network != "tcp" {
		return nil, fmt.Errorf("http proxy only supports tcp network, got %s", network)
	}

	dialer := net.Dialer{Timeout: timeout}
	conn, err := dialer.Dial(network, proxyConfig.address())
	if err != nil {
		return nil, fmt.Errorf("邮箱代理异常: %w", err)
	}

	if timeout > 0 {
		_ = conn.SetDeadline(time.Now().Add(timeout))
		defer conn.SetDeadline(time.Time{})
	}

	var builder strings.Builder
	builder.WriteString("CONNECT ")
	builder.WriteString(targetAddr)
	builder.WriteString(" HTTP/1.1\r\nHost: ")
	builder.WriteString(targetAddr)
	builder.WriteString("\r\n")
	if proxyConfig.Username != "" || proxyConfig.Password != "" {
		token := base64.StdEncoding.EncodeToString([]byte(proxyConfig.Username + ":" + proxyConfig.Password))
		builder.WriteString("Proxy-Authorization: Basic ")
		builder.WriteString(token)
		builder.WriteString("\r\n")
	}
	builder.WriteString("\r\n")

	if _, err := conn.Write([]byte(builder.String())); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("邮箱代理异常: %w", err)
	}

	reader := bufio.NewReader(conn)
	resp, err := http.ReadResponse(reader, &http.Request{Method: http.MethodConnect})
	if err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("邮箱代理异常: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		_ = conn.Close()
		return nil, fmt.Errorf("邮箱代理异常: http proxy connect failed: %s", resp.Status)
	}

	return &bufferedConn{Conn: conn, reader: reader}, nil
}

type bufferedConn struct {
	net.Conn
	reader *bufio.Reader
}

func (c *bufferedConn) Read(p []byte) (int, error) {
	if c.reader != nil && c.reader.Buffered() > 0 {
		return c.reader.Read(p)
	}
	return c.Conn.Read(p)
}
