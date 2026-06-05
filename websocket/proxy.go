package websocket

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"golang.org/x/net/proxy"
)

type normalizedProxy struct {
	proxyURL *url.URL
	typeName ProxyType
}

func normalizeProxyConfig(proxyConfig ProxyConfig) (*normalizedProxy, error) {
	address := strings.TrimSpace(proxyConfig.Address)
	if address == "" {
		return nil, fmt.Errorf("proxy address is empty")
	}

	proxyType := normalizeProxyType(proxyConfig.Type)
	if proxyType == ProxyNone {
		proxyType = ProxyHTTP
	}

	raw := address
	if !strings.Contains(raw, "://") {
		raw = string(proxyType) + "://" + raw
	}

	parsed, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("invalid proxy url: %w", err)
	}
	if parsed.Host == "" {
		return nil, fmt.Errorf("proxy URL must include host and port")
	}

	scheme := normalizeProxyType(ProxyType(parsed.Scheme))
	if scheme == ProxyNone {
		return nil, fmt.Errorf("unsupported proxy type: %s", parsed.Scheme)
	}
	if scheme != ProxyHTTP && scheme != ProxyHTTPS && scheme != ProxySocks5 && scheme != ProxySocks5H {
		return nil, fmt.Errorf("unsupported proxy type: %s", parsed.Scheme)
	}

	if parsed.User == nil && proxyConfig.Username != "" {
		if proxyConfig.Password == "" {
			parsed.User = url.User(proxyConfig.Username)
		} else {
			parsed.User = url.UserPassword(proxyConfig.Username, proxyConfig.Password)
		}
	}

	if _, _, err = net.SplitHostPort(parsed.Host); err != nil {
		return nil, fmt.Errorf("proxy address must be in host:port format: %w", err)
	}

	return &normalizedProxy{proxyURL: parsed, typeName: scheme}, nil
}

func normalizeProxyType(proxyType ProxyType) ProxyType {
	switch ProxyType(strings.ToLower(strings.TrimSpace(string(proxyType)))) {
	case ProxyHTTP:
		return ProxyHTTP
	case ProxyHTTPS:
		return ProxyHTTPS
	case ProxySocks5:
		return ProxySocks5
	case ProxySocks5H:
		return ProxySocks5H
	default:
		return ProxyNone
	}
}

func socks5DialContext(proxyURL *url.URL, timeout time.Duration) (func(context.Context, string, string) (net.Conn, error), error) {
	var auth *proxy.Auth
	if proxyURL.User != nil {
		password, _ := proxyURL.User.Password()
		auth = &proxy.Auth{
			User:     proxyURL.User.Username(),
			Password: password,
		}
	}

	baseDialer := &net.Dialer{Timeout: timeout}
	dialer, err := proxy.SOCKS5("tcp", proxyURL.Host, auth, baseDialer)
	if err != nil {
		return nil, fmt.Errorf("create socks5 proxy failed: %w", err)
	}

	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		type dialResult struct {
			conn net.Conn
			err  error
		}

		done := make(chan dialResult, 1)
		go func() {
			conn, err := dialer.Dial(network, addr)
			done <- dialResult{conn: conn, err: err}
		}()

		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case result := <-done:
			return result.conn, result.err
		}
	}, nil
}

func proxyFromConfig(proxyConfig ProxyConfig, timeout time.Duration) (func(*http.Request) (*url.URL, error), func(context.Context, string, string) (net.Conn, error), error) {
	normalized, err := normalizeProxyConfig(proxyConfig)
	if err != nil {
		return nil, nil, err
	}

	switch normalized.typeName {
	case ProxyHTTP, ProxyHTTPS:
		return http.ProxyURL(normalized.proxyURL), nil, nil
	case ProxySocks5, ProxySocks5H:
		dialContext, err := socks5DialContext(normalized.proxyURL, timeout)
		if err != nil {
			return nil, nil, err
		}
		return nil, dialContext, nil
	default:
		return nil, nil, fmt.Errorf("unsupported proxy type: %s", normalized.typeName)
	}
}
