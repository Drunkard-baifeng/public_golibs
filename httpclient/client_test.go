package httpclient

import (
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestDefaultTransportPoolConfig(t *testing.T) {
	c := New()

	if c.transport.MaxIdleConns != defaultMaxIdleConns {
		t.Fatalf("MaxIdleConns=%d want=%d", c.transport.MaxIdleConns, defaultMaxIdleConns)
	}
	if c.transport.IdleConnTimeout != defaultIdleConnTimeout {
		t.Fatalf("IdleConnTimeout=%s want=%s", c.transport.IdleConnTimeout, defaultIdleConnTimeout)
	}
	if c.transport.DisableKeepAlives {
		t.Fatal("DisableKeepAlives should default to false")
	}
	if !c.verify {
		t.Fatal("TLS verification should default to true")
	}
	if c.maxRedirects != defaultMaxRedirects {
		t.Fatalf("maxRedirects=%d want=%d", c.maxRedirects, defaultMaxRedirects)
	}
}

func TestNewWithConfigTLSVerification(t *testing.T) {
	secureClient := NewWithConfig(Config{Timeout: time.Second})
	if !secureClient.verify {
		t.Fatal("omitted Verify should preserve secure default")
	}
	if secureClient.transport.TLSClientConfig != nil && secureClient.transport.TLSClientConfig.InsecureSkipVerify {
		t.Fatal("omitted Verify should not set InsecureSkipVerify")
	}

	insecureClient := NewWithConfig(Config{Verify: BoolPtr(false)})
	if insecureClient.verify {
		t.Fatal("explicit Verify=false should disable verification")
	}
	if insecureClient.transport.TLSClientConfig == nil || !insecureClient.transport.TLSClientConfig.InsecureSkipVerify {
		t.Fatal("explicit Verify=false should set InsecureSkipVerify")
	}
}

func TestNewWithConfigAppliesTransportPoolConfig(t *testing.T) {
	c := NewWithConfig(Config{
		MaxIdleConns:      17,
		IdleConnTimeout:   42 * time.Second,
		DisableKeepAlives: true,
	})

	if c.transport.MaxIdleConns != 17 {
		t.Fatalf("MaxIdleConns=%d want=17", c.transport.MaxIdleConns)
	}
	if c.transport.IdleConnTimeout != 42*time.Second {
		t.Fatalf("IdleConnTimeout=%s want=%s", c.transport.IdleConnTimeout, 42*time.Second)
	}
	if !c.transport.DisableKeepAlives {
		t.Fatal("DisableKeepAlives should be true")
	}
}

func TestRebuildTransportKeepsPoolConfig(t *testing.T) {
	c := NewWithConfig(Config{
		MaxIdleConns:      17,
		IdleConnTimeout:   42 * time.Second,
		DisableKeepAlives: true,
	})

	c.SetSocks5Proxy("127.0.0.1:1080")
	if err := c.ProxyError(); err != nil {
		t.Fatalf("SetSocks5Proxy() error: %v", err)
	}

	if c.transport.MaxIdleConns != 17 {
		t.Fatalf("MaxIdleConns=%d want=17", c.transport.MaxIdleConns)
	}
	if c.transport.IdleConnTimeout != 42*time.Second {
		t.Fatalf("IdleConnTimeout=%s want=%s", c.transport.IdleConnTimeout, 42*time.Second)
	}
	if !c.transport.DisableKeepAlives {
		t.Fatal("DisableKeepAlives should remain true after rebuild")
	}
}

func TestDefaultRedirectLimit(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		http.Redirect(w, r, "/next", http.StatusFound)
	}))
	defer server.Close()

	resp, err := New().Get(server.URL, nil)
	if err != nil {
		t.Fatalf("Get() error: %v", err)
	}
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("StatusCode=%d want=%d", resp.StatusCode, http.StatusFound)
	}
	if got := requests.Load(); got != int32(defaultMaxRedirects) {
		t.Fatalf("requests=%d want=%d", got, defaultMaxRedirects)
	}
}

func TestRequestOptionsDoNotMutateSharedClient(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(40 * time.Millisecond)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	client := New().SetTimeout(time.Second)
	var wg sync.WaitGroup
	errors := make(chan error, 20)
	for i := 0; i < 10; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			if _, err := client.Get(server.URL, &Options{Timeout: 5 * time.Millisecond}); err == nil {
				errors <- fmt.Errorf("short request unexpectedly succeeded")
			}
		}()
		go func() {
			defer wg.Done()
			if _, err := client.Get(server.URL, &Options{Timeout: 500 * time.Millisecond}); err != nil {
				errors <- fmt.Errorf("long request failed: %w", err)
			}
		}()
	}
	wg.Wait()
	close(errors)
	for err := range errors {
		t.Error(err)
	}
	if client.timeout != time.Second {
		t.Fatalf("client timeout changed to %s", client.timeout)
	}
}

func TestConcurrentCookieUpdates(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := "cookie_" + r.URL.Query().Get("id")
		http.SetCookie(w, &http.Cookie{Name: name, Value: "value", Path: "/"})
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	client := New()
	var wg sync.WaitGroup
	for i := 0; i < 30; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			if _, err := client.Get(fmt.Sprintf("%s?id=%d", server.URL, id), nil); err != nil {
				t.Errorf("Get(%d) error: %v", id, err)
			}
		}(i)
	}
	wg.Wait()

	if got := len(client.GetCookies()); got != 30 {
		t.Fatalf("GetCookies() returned %d cookies, want 30", got)
	}
}

func TestResponseCookiesStayScopedToHost(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/set" {
			http.SetCookie(w, &http.Cookie{Name: "session", Value: "secret", Path: "/"})
			return
		}
		if _, err := r.Cookie("session"); err == nil {
			_, _ = io.WriteString(w, "leaked")
			return
		}
		_, _ = io.WriteString(w, "clean")
	}))
	defer server.Close()

	client := New()
	if _, err := client.Get(server.URL+"/set", nil); err != nil {
		t.Fatalf("set cookie: %v", err)
	}
	otherHostURL := strings.Replace(server.URL, "127.0.0.1", "localhost", 1) + "/check"
	resp, err := client.Get(otherHostURL, nil)
	if err != nil {
		t.Fatalf("check cookie: %v", err)
	}
	if resp.Text() != "clean" {
		t.Fatalf("cookie crossed host boundary: %q", resp.Text())
	}
	if client.GetCookies()["session"] != "secret" {
		t.Fatal("response cookie should remain visible through GetCookies")
	}
}

func TestRequestCookieOverridesJarWithoutDuplication(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/set" {
			http.SetCookie(w, &http.Cookie{Name: "token", Value: "jar", Path: "/"})
			return
		}
		cookies := r.Cookies()
		count := 0
		value := ""
		for _, cookie := range cookies {
			if cookie.Name == "token" {
				count++
				value = cookie.Value
			}
		}
		_, _ = fmt.Fprintf(w, "%d:%s", count, value)
	}))
	defer server.Close()

	client := New().AddCookie("token", "default")
	if _, err := client.Get(server.URL+"/set", nil); err != nil {
		t.Fatalf("set cookie: %v", err)
	}
	resp, err := client.Get(server.URL+"/check", &Options{
		Cookies: map[string]string{"token": "request"},
	})
	if err != nil {
		t.Fatalf("check cookie: %v", err)
	}
	if resp.Text() != "1:request" {
		t.Fatalf("cookie header=%q want=%q", resp.Text(), "1:request")
	}
}

func TestCrossHostRedirectUsesDestinationJarCookie(t *testing.T) {
	var destinationBaseURL string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/set-destination":
			http.SetCookie(w, &http.Cookie{Name: "token", Value: "destination", Path: "/"})
		case "/redirect":
			http.Redirect(w, r, destinationBaseURL+"/final", http.StatusFound)
		case "/final":
			cookie, err := r.Cookie("token")
			if err != nil {
				_, _ = io.WriteString(w, "missing")
				return
			}
			_, _ = io.WriteString(w, cookie.Value)
		}
	}))
	defer server.Close()
	destinationBaseURL = strings.Replace(server.URL, "127.0.0.1", "localhost", 1)

	client := New()
	if _, err := client.Get(destinationBaseURL+"/set-destination", nil); err != nil {
		t.Fatalf("set destination cookie: %v", err)
	}
	resp, err := client.Get(server.URL+"/redirect", &Options{
		Cookies: map[string]string{"token": "initial"},
	})
	if err != nil {
		t.Fatalf("follow cross-host redirect: %v", err)
	}
	if resp.Text() != "destination" {
		t.Fatalf("destination cookie=%q want=%q", resp.Text(), "destination")
	}
}

func TestRedirectSetCookieReplacesRequestCookie(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/redirect" {
			http.SetCookie(w, &http.Cookie{Name: "token", Value: "updated", Path: "/"})
			http.Redirect(w, r, "/final", http.StatusFound)
			return
		}
		cookie, err := r.Cookie("token")
		if err != nil {
			_, _ = io.WriteString(w, "missing")
			return
		}
		_, _ = io.WriteString(w, cookie.Value)
	}))
	defer server.Close()

	client := New()
	resp, err := client.Get(server.URL+"/redirect", &Options{
		Cookies: map[string]string{"token": "initial"},
	})
	if err != nil {
		t.Fatalf("follow redirect: %v", err)
	}
	if resp.Text() != "updated" {
		t.Fatalf("redirect cookie=%q want=%q", resp.Text(), "updated")
	}
	if client.GetCookies()["token"] != "updated" {
		t.Fatal("GetCookies should include cookies from redirect responses")
	}
}

func TestManualGzipDecompressionUpdatesHeaders(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var compressed bytes.Buffer
		writer := gzip.NewWriter(&compressed)
		_, _ = writer.Write([]byte("decompressed"))
		_ = writer.Close()
		w.Header().Set("Content-Encoding", "gzip")
		w.Header().Set("Content-Length", fmt.Sprint(compressed.Len()))
		_, _ = w.Write(compressed.Bytes())
	}))
	defer server.Close()

	resp, err := New().Get(server.URL, &Options{
		Headers: map[string]string{"Accept-Encoding": "gzip"},
	})
	if err != nil {
		t.Fatalf("Get() error: %v", err)
	}
	if resp.Text() != "decompressed" {
		t.Fatalf("Text()=%q", resp.Text())
	}
	if encoding := resp.GetHeader("Content-Encoding"); encoding != "" {
		t.Fatalf("Content-Encoding=%q after decompression", encoding)
	}
	if length := resp.GetHeader("Content-Length"); length != "" {
		t.Fatalf("Content-Length=%q after decompression", length)
	}
}

func TestHeadWithGzipMetadataDoesNotAttemptDecompression(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Encoding", "gzip")
		w.Header().Set("Content-Length", "123")
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	resp, err := New().Head(server.URL, &Options{
		Headers: map[string]string{"Accept-Encoding": "gzip"},
	})
	if err != nil {
		t.Fatalf("Head() error: %v", err)
	}
	if resp.GetHeader("Content-Encoding") != "gzip" {
		t.Fatalf("HEAD Content-Encoding=%q", resp.GetHeader("Content-Encoding"))
	}
	if len(resp.Body) != 0 {
		t.Fatalf("HEAD body length=%d", len(resp.Body))
	}
}

func TestInvalidProxyIsReportedAndCleared(t *testing.T) {
	client := New().SetProxy("bad-proxy", "http")
	if client.ProxyError() == nil {
		t.Fatal("invalid proxy should record an error")
	}
	if _, err := client.Get("http://example.test", nil); err == nil {
		t.Fatal("request should report invalid proxy configuration")
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	client.ClearProxy()
	if client.ProxyError() != nil {
		t.Fatalf("ClearProxy() error: %v", client.ProxyError())
	}
	if _, err := client.Get(server.URL, nil); err != nil {
		t.Fatalf("direct request after ClearProxy(): %v", err)
	}
}

func TestParseProxySupportsIPv6AndEscapedCredentials(t *testing.T) {
	ipv6Proxy, proxyType, err := parseProxy("[::1]:1080", "socks5")
	if err != nil {
		t.Fatalf("parse IPv6 proxy: %v", err)
	}
	if ipv6Proxy.Host != "[::1]:1080" || proxyType != "socks5" {
		t.Fatalf("IPv6 proxy=%q type=%q", ipv6Proxy.Host, proxyType)
	}

	authProxy, _, err := parseProxy("127.0.0.1:8080:user:p@ss", "http")
	if err != nil {
		t.Fatalf("parse authenticated proxy: %v", err)
	}
	password, _ := authProxy.User.Password()
	if authProxy.User.Username() != "user" || password != "p@ss" {
		t.Fatalf("proxy credentials were not preserved: %s", authProxy.String())
	}
}

func TestMultipartUsesFileContentTypeAndDoesNotMutateOptions(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		part, err := r.MultipartReader()
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		filePart, err := part.NextPart()
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		_, _ = io.WriteString(w, filePart.Header.Get("Content-Type"))
	}))
	defer server.Close()

	opts := &Options{Headers: map[string]string{"X-Test": "value"}}
	resp, err := New().PostMultipart(server.URL, nil, []FileField{{
		FieldName:   "document",
		FileName:    "data.json",
		ContentType: "application/json",
		Data:        []byte(`{"ok":true}`),
	}}, opts)
	if err != nil {
		t.Fatalf("PostMultipart() error: %v", err)
	}
	if resp.Text() != "application/json" {
		t.Fatalf("part Content-Type=%q", resp.Text())
	}
	if _, mutated := opts.Headers["Content-Type"]; mutated {
		t.Fatal("PostMultipart mutated caller options")
	}
}
