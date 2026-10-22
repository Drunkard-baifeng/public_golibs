package httpclienttls

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func tunnelFixture(t *testing.T, destination string) string {
	t.Helper()
	p := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodConnect || r.Host != destination || r.Header.Get("Proxy-Authorization") != "Basic "+base64.StdEncoding.EncodeToString([]byte("user:secret")) {
			t.Error("invalid CONNECT authentication or target")
			w.WriteHeader(407)
			return
		}
		remote, err := net.DialTimeout("tcp", destination, time.Second)
		if err != nil {
			t.Error(err)
			return
		}
		defer remote.Close()
		conn, rw, err := w.(http.Hijacker).Hijack()
		if err != nil {
			t.Error(err)
			return
		}
		defer conn.Close()
		conn.SetDeadline(time.Now().Add(5 * time.Second))
		remote.SetDeadline(time.Now().Add(5 * time.Second))
		rw.WriteString("HTTP/1.1 200 Connection Established\r\n\r\n")
		rw.Flush()
		done := make(chan struct{})
		go func() { io.Copy(remote, rw); remote.(*net.TCPConn).CloseWrite(); close(done) }()
		io.Copy(conn, remote)
		conn.Close()
		<-done
	}))
	t.Cleanup(p.Close)
	return strings.Replace(p.URL, "http://", "http://user:secret@", 1)
}

func TestProxyTunnelStreamLifetime(t *testing.T) {
	for _, secure := range []bool{false, true} {
		t.Run(map[bool]string{false: "http", true: "https"}[secure], func(t *testing.T) {
			payload := strings.Repeat("chunk", 30000)
			origin := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Proxy-Authorization") != "" {
					t.Error("proxy credentials leaked to origin")
				}
				w.Header().Set("Content-Type", "text/event-stream")
				w.WriteHeader(200)
				w.(http.Flusher).Flush()
				time.Sleep(250 * time.Millisecond)
				io.WriteString(w, payload)
			}))
			if secure {
				origin.StartTLS()
			} else {
				origin.Start()
			}
			defer origin.Close()
			destination := strings.TrimPrefix(strings.TrimPrefix(origin.URL, "https://"), "http://")
			c, err := NewClient(Config{Proxy: tunnelFixture(t, destination), Profile: "chrome_146", Verify: BoolPtr(false), Timeout: 100 * time.Millisecond, ProxyHandshakeTimeout: 100 * time.Millisecond})
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			r, err := c.DoStream(context.Background(), "GET", origin.URL, nil, &Options{DisableTimeout: true, HeaderTimeout: time.Second})
			if err != nil {
				t.Fatal(err)
			}
			defer r.Body.Close()
			got, err := io.ReadAll(r.Body)
			if err != nil || string(got) != payload {
				t.Fatalf("tunnel stream truncated: %d, %v", len(got), err)
			}
		})
	}
}

func TestProxyHandshakeCancellation(t *testing.T) {
	for _, scheme := range []string{"http", "https", "socks5", "socks5h"} {
		t.Run(scheme, func(t *testing.T) {
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			defer listener.Close()
			entered, closed := make(chan struct{}), make(chan struct{})
			go func() {
				c, e := listener.Accept()
				if e != nil {
					return
				}
				defer c.Close()
				c.SetDeadline(time.Now().Add(2 * time.Second))
				close(entered)
				io.Copy(io.Discard, c)
				close(closed)
			}()
			d, err := newProxyDialer(scheme+"://"+listener.Addr().String(), time.Second)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			go func() { <-entered; cancel() }()
			conn, err := d.DialContext(ctx, "tcp", "fixture.invalid:443")
			if conn != nil {
				conn.Close()
			}
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("cancellation lost: %v", err)
			}
			select {
			case <-closed:
			case <-time.After(time.Second):
				t.Fatal("proxy socket left open")
			}
		})
	}
}

func TestProxyHandshakeDeadline(t *testing.T) {
	p := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() }))
	defer p.Close()
	c, err := NewClient(Config{Proxy: p.URL, Profile: "chrome_146", ProxyHandshakeTimeout: 70 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	start := time.Now()
	_, err = c.DoStream(context.Background(), "GET", "http://fixture.invalid", nil, &Options{DisableTimeout: true})
	if err == nil || time.Since(start) > time.Second {
		t.Fatalf("handshake deadline ineffective: %v", err)
	}
}

func TestProxyHeaderTimeout(t *testing.T) {
	p := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() }))
	defer p.Close()
	c, err := NewClient(Config{Proxy: p.URL, Profile: "chrome_146", ProxyHandshakeTimeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	_, err = c.DoStream(context.Background(), "GET", "http://fixture.invalid", nil, &Options{DisableTimeout: true, HeaderTimeout: 70 * time.Millisecond})
	if !errors.Is(err, ErrHeaderTimeout) {
		t.Fatalf("header timeout lost: %v", err)
	}
}

func TestHTTPSProxyCertificateVerification(t *testing.T) {
	p := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { t.Error("untrusted proxy accepted") }))
	defer p.Close()
	c, err := NewClient(Config{Proxy: p.URL, Profile: "chrome_146", Verify: BoolPtr(false)})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if _, err = c.DoStream(context.Background(), "GET", "http://fixture.invalid", nil, nil); err == nil {
		t.Fatal("destination insecure option disabled proxy verification")
	}
}

func TestProxyAcceptsSuccessfulConnectResponses(t *testing.T) {
	for _, status := range []int{200, 201, 204, 299, 407} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			p := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				conn, rw, err := w.(http.Hijacker).Hijack()
				if err != nil {
					t.Error(err)
					return
				}
				defer conn.Close()
				fmt.Fprintf(rw, "HTTP/1.1 %d Proxy response\r\n\r\ntunnel bytes", status)
				rw.Flush()
			}))
			defer p.Close()
			d, err := newProxyDialer(p.URL, time.Second)
			if err != nil {
				t.Fatal(err)
			}
			conn, err := d.DialContext(context.Background(), "tcp", "fixture.invalid:443")
			if status == 407 {
				if err == nil {
					conn.Close()
					t.Fatal("proxy rejection accepted as a tunnel")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			body, err := io.ReadAll(conn)
			if err != nil || string(body) != "tunnel bytes" {
				t.Fatalf("buffered tunnel bytes lost: %q, %v", body, err)
			}
		})
	}
}

func TestSOCKS5Tunnel(t *testing.T) {
	for _, scheme := range []string{"socks5", "socks5h"} {
		t.Run(scheme, func(t *testing.T) {
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			defer listener.Close()
			done := make(chan struct{})
			go func() {
				defer close(done)
				conn, e := listener.Accept()
				if e != nil {
					return
				}
				defer conn.Close()
				conn.SetDeadline(time.Now().Add(3 * time.Second))
				reader := bufio.NewReader(conn)
				head := make([]byte, 2)
				if _, e = io.ReadFull(reader, head); e != nil {
					t.Error(e)
					return
				}
				if head[0] != 5 {
					t.Error("wrong SOCKS version")
					return
				}
				io.CopyN(io.Discard, reader, int64(head[1]))
				conn.Write([]byte{5, 0})
				request := make([]byte, 4)
				if _, e = io.ReadFull(reader, request); e != nil {
					t.Error(e)
					return
				}
				if request[3] != 3 {
					t.Error("expected remote DNS")
					return
				}
				n, _ := reader.ReadByte()
				host := make([]byte, int(n))
				io.ReadFull(reader, host)
				port := make([]byte, 2)
				io.ReadFull(reader, port)
				if string(host) != "fixture.invalid" || binary.BigEndian.Uint16(port) != 80 {
					t.Error("wrong SOCKS target")
					return
				}
				conn.Write([]byte{5, 0, 0, 1, 127, 0, 0, 1, 0, 80})
				r, e := http.ReadRequest(reader)
				if e != nil {
					t.Error(e)
					return
				}
				r.Body.Close()
				io.WriteString(conn, "HTTP/1.1 200 OK\r\nContent-Length: 2\r\nConnection: close\r\n\r\nok")
			}()
			c, err := NewClient(Config{Proxy: scheme + "://" + listener.Addr().String(), Profile: "chrome_146"})
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			r, err := c.DoStream(context.Background(), "GET", "http://fixture.invalid", nil, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer r.Body.Close()
			b, err := io.ReadAll(r.Body)
			if err != nil || string(b) != "ok" {
				t.Fatal(string(b), err)
			}
			<-done
		})
	}
}
