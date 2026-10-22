package httpclienttls

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/base64"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"time"

	"golang.org/x/net/proxy"
)

type proxyDialer struct {
	endpoint *url.URL
	timeout  time.Duration
	socks    proxy.ContextDialer
}

func newProxyDialer(value string, timeout time.Duration) (*proxyDialer, error) {
	u, err := url.Parse(value)
	if err != nil {
		return nil, fmt.Errorf("invalid proxy URL")
	}
	d := &proxyDialer{endpoint: u, timeout: timeout}
	if u.Scheme == "socks5" || u.Scheme == "socks5h" {
		var auth *proxy.Auth
		if u.User != nil {
			password, _ := u.User.Password()
			auth = &proxy.Auth{User: u.User.Username(), Password: password}
		}
		socks, err := proxy.SOCKS5("tcp", u.Host, auth, &net.Dialer{})
		if err != nil {
			return nil, fmt.Errorf("initialize SOCKS5 dialer: %w", err)
		}
		var ok bool
		d.socks, ok = socks.(proxy.ContextDialer)
		if !ok {
			return nil, fmt.Errorf("SOCKS5 dialer does not support cancellation")
		}
	}
	return d, nil
}

func (d *proxyDialer) DialContext(parent context.Context, network, address string) (conn net.Conn, err error) {
	ctx, cancel := context.WithTimeout(parent, d.timeout)
	defer cancel()
	if d.socks != nil {
		conn, err = d.socks.DialContext(ctx, network, address)
		if ctx.Err() != nil {
			if conn != nil {
				conn.Close()
			}
			return nil, ctx.Err()
		}
		return conn, err
	}
	conn, err = (&net.Dialer{}).DialContext(ctx, network, d.endpoint.Host)
	if err != nil {
		return nil, err
	}
	raw := conn
	defer func() {
		if err != nil {
			raw.Close()
		}
	}()
	deadline, _ := ctx.Deadline()
	if err = raw.SetDeadline(deadline); err != nil {
		return nil, err
	}
	// Stop and join cancellation before handing a reusable tunnel to the client.
	stopped := make(chan struct{})
	stop := context.AfterFunc(ctx, func() { raw.Close(); close(stopped) })
	defer func() {
		if !stop() {
			<-stopped
		}
		if ctx.Err() != nil {
			raw.Close()
			conn, err = nil, ctx.Err()
		}
	}()
	if d.endpoint.Scheme == "https" {
		// The proxy has its own verified TLS session. The destination retains
		// the selected browser fingerprint inside this HTTP/1.1 CONNECT tunnel.
		secure := tls.Client(raw, &tls.Config{ServerName: d.endpoint.Hostname(), NextProtos: []string{"http/1.1"}, MinVersion: tls.VersionTLS12})
		if err = secure.HandshakeContext(ctx); err != nil {
			return nil, err
		}
		conn = secure
	}
	req := &http.Request{Method: http.MethodConnect, URL: &url.URL{Host: address}, Host: address, Header: make(http.Header)}
	if d.endpoint.User != nil {
		password, _ := d.endpoint.User.Password()
		req.Header.Set("Proxy-Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(d.endpoint.User.Username()+":"+password)))
	}
	if err = req.Write(conn); err != nil {
		return nil, err
	}
	reader := bufio.NewReader(io.LimitReader(conn, 64<<10))
	var response *http.Response
	for {
		response, err = http.ReadResponse(reader, req)
		if err != nil {
			return nil, fmt.Errorf("read proxy CONNECT response: %w", err)
		}
		// Informational responses share the same byte budget and deadline.
		// 101 is a protocol switch, not a successful CONNECT tunnel.
		if response.StatusCode < 100 || response.StatusCode >= 200 || response.StatusCode == http.StatusSwitchingProtocols {
			break
		}
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("proxy CONNECT returned HTTP %d", response.StatusCode)
	}
	// CONNECT response bodies represent the tunnel; never close or drain them.
	if err = conn.SetDeadline(time.Time{}); err != nil {
		return nil, err
	}
	return &proxyBufferedConn{Conn: conn, reader: reader}, nil
}

// Preserve bytes read with the CONNECT response without limiting tunnel data.
type proxyBufferedConn struct {
	net.Conn
	reader *bufio.Reader
}

func (c *proxyBufferedConn) Read(p []byte) (int, error) {
	if n := c.reader.Buffered(); n > 0 {
		return c.reader.Read(p[:min(len(p), n)])
	}
	return c.Conn.Read(p)
}
