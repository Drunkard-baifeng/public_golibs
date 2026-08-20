package httpclienttls

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	fhttp "github.com/bogdanfinn/fhttp"
)

func TestFixedProfileAndMatchingUserAgent(t *testing.T) {
	client, err := NewClient(Config{Profile: "firefox_148"})
	if err != nil {
		t.Fatalf("NewClient() error: %v", err)
	}
	defer client.Close()

	profile := client.Profile()
	if profile.Name != "firefox_148" {
		t.Fatalf("Profile().Name=%q", profile.Name)
	}
	if !strings.Contains(profile.UserAgent, "Firefox/148.0") {
		t.Fatalf("Profile().UserAgent=%q", profile.UserAgent)
	}
	if client.randomTLSExtensionOrder {
		t.Fatal("Firefox TLS extension order should remain profile-defined")
	}
}

func TestProfileSpecificTLSBehavior(t *testing.T) {
	tests := []struct {
		name              string
		randomExtensions  bool
		sessionResumption bool
	}{
		{name: "chrome_146", randomExtensions: true, sessionResumption: true},
		{name: "brave_146", randomExtensions: true, sessionResumption: true},
		{name: "firefox_147", sessionResumption: true},
		{name: "firefox_148"},
		{name: "safari_ios_18_5"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			client, err := NewClient(Config{Profile: test.name})
			if err != nil {
				t.Fatal(err)
			}
			defer client.Close()
			profile := client.Profile()
			if profile.RandomTLSExtensionOrder != test.randomExtensions {
				t.Fatalf("RandomTLSExtensionOrder=%v", profile.RandomTLSExtensionOrder)
			}
			if profile.SessionResumption != test.sessionResumption {
				t.Fatalf("SessionResumption=%v", profile.SessionResumption)
			}
		})
	}
}

func TestRandomProfileUsesConfiguredPool(t *testing.T) {
	client, err := NewClient(Config{ProfilePool: []string{"chrome_146"}})
	if err != nil {
		t.Fatalf("NewClient() error: %v", err)
	}
	defer client.Close()
	if got := client.Profile().Name; got != "chrome_146" {
		t.Fatalf("Profile().Name=%q", got)
	}

	available := AvailableProfiles()
	if len(available) < 5 {
		t.Fatalf("AvailableProfiles() returned only %d profiles", len(available))
	}
	for index := 1; index < len(available); index++ {
		if available[index-1] >= available[index] {
			t.Fatal("AvailableProfiles() is not sorted")
		}
	}
}

func TestProfileAliases(t *testing.T) {
	client, err := NewClient(Config{Profile: "chrome146"})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	if got := client.Profile().Name; got != "chrome_146" {
		t.Fatalf("Profile().Name=%q", got)
	}
}

func TestInvalidProfileIsReported(t *testing.T) {
	client := NewWithConfig(Config{Profile: "not-a-browser"})
	if client.ConfigError() == nil {
		t.Fatal("ConfigError() should report an unsupported profile")
	}
	if _, err := client.Get("http://example.test", nil); err == nil {
		t.Fatal("request should report the construction error")
	}
	if _, err := NewClient(Config{Profile: "not-a-browser"}); err == nil {
		t.Fatal("NewClient() should return an unsupported profile error")
	}
}

func TestHTTPClientSendsProfileUserAgentAndParams(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		_, _ = fmt.Fprintf(writer, "%s|%s|%s", request.UserAgent(), request.URL.Query().Get("q"), request.Header.Get("X-Test"))
	}))
	defer server.Close()

	client, err := NewClient(Config{Profile: "chrome_146"})
	if err != nil {
		t.Fatalf("NewClient() error: %v", err)
	}
	defer client.Close()
	response, err := client.Get(server.URL, &Options{
		Params:  map[string]string{"q": "value"},
		Headers: map[string]string{"X-Test": "header"},
	})
	if err != nil {
		t.Fatalf("Get() error: %v", err)
	}
	if !strings.Contains(response.Text(), "Chrome/146.0.0.0") || !strings.HasSuffix(response.Text(), "|value|header") {
		t.Fatalf("response=%q", response.Text())
	}
}

func TestBrowserIdentityAndNavigationHeaders(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		_, _ = fmt.Fprintf(writer, "%s|%s|%s|%s|%s|%s",
			request.Header.Get("Sec-CH-UA"), request.Header.Get("Sec-CH-UA-Platform"),
			request.Header.Get("Accept-Encoding"), request.Header.Get("Sec-Fetch-Mode"),
			request.Header.Get("Sec-Fetch-Site"), request.Header.Get("Priority"))
	}))
	defer server.Close()
	client, err := NewClient(Config{Profile: "chrome_146", Verify: BoolPtr(false)})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	response, err := client.Get(server.URL, &Options{BrowserMode: BrowserModeNavigation})
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{"Google Chrome", `"Windows"`, "gzip, deflate, br, zstd", "navigate", "none", "u=0, i"} {
		if !strings.Contains(response.Text(), value) {
			t.Fatalf("response %q does not contain %q", response.Text(), value)
		}
	}
	order := response.Request.Header.Values(fhttp.HeaderOrderKey)
	if len(order) == 0 || order[0] != "sec-ch-ua" {
		t.Fatalf("header order=%v", order)
	}
}

func TestProfileMatchedBrowserHeaders(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		_, _ = fmt.Fprintf(writer, "%s|%s|%s|%s",
			request.Header.Get("Sec-CH-UA"), request.Header.Get("Sec-CH-UA-Platform"),
			request.Header.Get("Accept-Language"), request.Header.Get("Accept-Encoding"))
	}))
	defer server.Close()
	tests := []struct {
		profile string
		want    string
	}{
		{profile: "chrome_146", want: `|"Windows"|en-US,en;q=0.9|gzip, deflate, br, zstd`},
		{profile: "firefox_148", want: "||en-US,en;q=0.5|gzip, deflate, br, zstd"},
		{profile: "safari_ios_18_5", want: "||en-US,en;q=0.9|gzip, deflate, br"},
	}
	for _, test := range tests {
		t.Run(test.profile, func(t *testing.T) {
			client, err := NewClient(Config{Profile: test.profile})
			if err != nil {
				t.Fatal(err)
			}
			defer client.Close()
			response, err := client.Get(server.URL, nil)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(response.Text(), test.want) || test.profile == "chrome_146" && !strings.Contains(response.Text(), "Google Chrome") {
				t.Fatalf("response=%q want substring=%q", response.Text(), test.want)
			}
		})
	}
}

func TestPostJSONUsesFetchHeaderOrder(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	client, err := NewClient(Config{Profile: "chrome_146"})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	response, err := client.PostJSON(server.URL, map[string]bool{"ok": true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	order := response.Request.Header.Values(fhttp.HeaderOrderKey)
	if len(order) == 0 || order[0] != "sec-ch-ua-platform" {
		t.Fatalf("header order=%v", order)
	}
	if response.Request.Header.Get("Accept") != "application/json" || response.Request.Header.Get("Sec-Fetch-Dest") != "empty" {
		t.Fatalf("headers=%v", response.Request.Header)
	}
}

func TestBrowserModeOffAndExplicitHeaderOrder(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	client, err := NewClient(Config{Profile: "chrome_146"})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	response, err := client.Get(server.URL, &Options{
		BrowserMode:  BrowserModeOff,
		HeaderValues: http.Header{"X-Multi": {"one", "two"}},
		HeaderOrder:  []string{"x-multi", "accept"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if response.Request.Header.Get("User-Agent") != "" {
		t.Fatalf("User-Agent=%q", response.Request.Header.Get("User-Agent"))
	}
	if got := response.Request.Header.Values("X-Multi"); len(got) != 2 {
		t.Fatalf("X-Multi=%v", got)
	}
	if got := response.Request.Header.Values(fhttp.HeaderOrderKey); len(got) != 2 || got[0] != "x-multi" {
		t.Fatalf("header order=%v", got)
	}
}

func TestTLS13SessionResumptionProfile(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		_, _ = fmt.Fprint(writer, request.TLS != nil && request.TLS.DidResume)
	}))
	defer server.Close()
	client, err := NewClient(Config{Profile: "chrome_146", Verify: BoolPtr(false)})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	first, err := client.Get(server.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	if first.Text() != "false" {
		t.Fatalf("first handshake resumed=%q", first.Text())
	}
	client.inner.CloseIdleConnections()
	second, err := client.Get(server.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	if second.Text() != "true" {
		t.Fatalf("second handshake resumed=%q", second.Text())
	}
}

func TestTLSVerificationCanBeExplicitlyDisabled(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		_, _ = io.WriteString(writer, "tls-ok")
	}))
	defer server.Close()

	secureClient, err := NewClient(Config{Profile: "chrome_146"})
	if err != nil {
		t.Fatalf("NewClient() error: %v", err)
	}
	defer secureClient.Close()
	if _, err := secureClient.Get(server.URL, nil); err == nil {
		t.Fatal("self-signed certificate unexpectedly passed verification")
	}

	insecureClient, err := NewClient(Config{Profile: "chrome_146", Verify: BoolPtr(false)})
	if err != nil {
		t.Fatalf("NewClient() error: %v", err)
	}
	defer insecureClient.Close()
	response, err := insecureClient.Get(server.URL, nil)
	if err != nil {
		t.Fatalf("Get() with Verify=false error: %v", err)
	}
	if response.Text() != "tls-ok" {
		t.Fatalf("response=%q", response.Text())
	}
}

func TestRequestTimeoutsAreConcurrentAndIsolated(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		time.Sleep(40 * time.Millisecond)
		writer.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	client, err := NewClient(Config{Profile: "chrome_146", Timeout: time.Second})
	if err != nil {
		t.Fatalf("NewClient() error: %v", err)
	}
	defer client.Close()

	var waitGroup sync.WaitGroup
	errors := make(chan error, 20)
	for index := 0; index < 10; index++ {
		waitGroup.Add(2)
		go func() {
			defer waitGroup.Done()
			if _, err := client.Get(server.URL, &Options{Timeout: 5 * time.Millisecond}); err == nil {
				errors <- fmt.Errorf("short request unexpectedly succeeded")
			}
		}()
		go func() {
			defer waitGroup.Done()
			if _, err := client.Get(server.URL, &Options{Timeout: 500 * time.Millisecond}); err != nil {
				errors <- fmt.Errorf("long request failed: %w", err)
			}
		}()
	}
	waitGroup.Wait()
	close(errors)
	for err := range errors {
		t.Error(err)
	}
}

func TestRedirectPolicyAndSensitiveHeaders(t *testing.T) {
	var destinationURL string
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/redirect":
			http.Redirect(writer, request, destinationURL+"/destination", http.StatusFound)
		case "/destination":
			_, _ = fmt.Fprintf(writer, "%s|%s", request.Header.Get("Authorization"), request.Header.Get("Referer"))
		}
	}))
	defer server.Close()
	destinationURL = strings.Replace(server.URL, "127.0.0.1", "localhost", 1)

	client, err := NewClient(Config{Profile: "chrome_146"})
	if err != nil {
		t.Fatalf("NewClient() error: %v", err)
	}
	defer client.Close()
	response, err := client.Get(server.URL+"/redirect", &Options{
		Headers: map[string]string{"Authorization": "Bearer secret"},
	})
	if err != nil {
		t.Fatalf("Get() error: %v", err)
	}
	if strings.HasPrefix(response.Text(), "Bearer") {
		t.Fatalf("authorization leaked across hosts: %q", response.Text())
	}
	if response.URL != destinationURL+"/destination" {
		t.Fatalf("response.URL=%q", response.URL)
	}

	allow := false
	response, err = client.Get(server.URL+"/redirect", &Options{AllowRedirects: &allow})
	if err != nil {
		t.Fatalf("Get() without redirects error: %v", err)
	}
	if response.StatusCode != http.StatusFound {
		t.Fatalf("StatusCode=%d", response.StatusCode)
	}
}

func TestNavigationRedirectRefreshesBrowserMetadata(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/start" {
			http.Redirect(writer, request, "/end", http.StatusFound)
			return
		}
		_, _ = fmt.Fprint(writer, request.Header.Get("Sec-Fetch-Site"))
	}))
	defer server.Close()
	client, err := NewClient(Config{Profile: "chrome_146"})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	response, err := client.Get(server.URL+"/start", &Options{BrowserMode: BrowserModeNavigation})
	if err != nil {
		t.Fatal(err)
	}
	order := response.Request.Header.Values(fhttp.HeaderOrderKey)
	if response.Text() != "same-origin" || len(order) == 0 || order[0] != "upgrade-insecure-requests" {
		t.Fatalf("response=%q order=%v", response.Text(), order)
	}
}

func TestCookieJarKeepsResponseCookiesScopedToHost(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/set" {
			http.SetCookie(writer, &http.Cookie{Name: "session", Value: "secret", Path: "/"})
			return
		}
		if _, err := request.Cookie("session"); err == nil {
			_, _ = io.WriteString(writer, "leaked")
			return
		}
		_, _ = io.WriteString(writer, "clean")
	}))
	defer server.Close()

	client, err := NewClient(Config{Profile: "chrome_146"})
	if err != nil {
		t.Fatalf("NewClient() error: %v", err)
	}
	defer client.Close()
	if _, err := client.Get(server.URL+"/set", nil); err != nil {
		t.Fatalf("set cookie: %v", err)
	}
	otherHost := strings.Replace(server.URL, "127.0.0.1", "localhost", 1)
	response, err := client.Get(otherHost+"/check", nil)
	if err != nil {
		t.Fatalf("check cookie: %v", err)
	}
	if response.Text() != "clean" {
		t.Fatalf("cookie crossed host boundary: %q", response.Text())
	}
	if client.GetCookies()["session"] != "secret" {
		t.Fatal("GetCookies() should include observed response cookies")
	}
}

func TestRequestCookieOverridesJarWithoutDuplication(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		cookies := request.Cookies()
		_, _ = fmt.Fprintf(writer, "%d|%s", len(cookies), request.Header.Get("Cookie"))
	}))
	defer server.Close()

	client, err := NewClient(Config{Profile: "chrome_146"})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	if err := client.SetCookie(server.URL, "session", "jar", "", "/", false); err != nil {
		t.Fatal(err)
	}
	response, err := client.Get(server.URL, &Options{Cookies: map[string]string{"session": "request"}})
	if err != nil {
		t.Fatal(err)
	}
	if response.Text() != "1|session=request" {
		t.Fatalf("response=%q", response.Text())
	}
}

func TestRedirectSetCookieReplacesExplicitCookie(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/start" {
			http.SetCookie(writer, &http.Cookie{Name: "session", Value: "updated", Path: "/"})
			http.Redirect(writer, request, "/end", http.StatusFound)
			return
		}
		cookie, err := request.Cookie("session")
		if err != nil {
			http.Error(writer, err.Error(), http.StatusBadRequest)
			return
		}
		_, _ = io.WriteString(writer, cookie.Value)
	}))
	defer server.Close()

	client, err := NewClient(Config{Profile: "chrome_146"})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	response, err := client.Get(server.URL+"/start", &Options{Cookies: map[string]string{"session": "initial"}})
	if err != nil {
		t.Fatal(err)
	}
	if response.Text() != "updated" {
		t.Fatalf("response=%q", response.Text())
	}
}

func TestPostJSONDoesNotMutateOptions(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		_, _ = io.WriteString(writer, request.Header.Get("Content-Type"))
	}))
	defer server.Close()
	client, err := NewClient(Config{Profile: "chrome_146"})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	options := &Options{Headers: map[string]string{"X-Test": "value"}}
	response, err := client.PostJSON(server.URL, map[string]any{"ok": true}, options)
	if err != nil {
		t.Fatalf("PostJSON() error: %v", err)
	}
	if response.Text() != "application/json" {
		t.Fatalf("Content-Type=%q", response.Text())
	}
	if _, exists := options.Headers["Content-Type"]; exists {
		t.Fatal("PostJSON() mutated caller options")
	}
}

func TestResponseBodyLimit(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		_, _ = io.WriteString(writer, "12345")
	}))
	defer server.Close()
	client, err := NewClient(Config{Profile: "chrome_146", MaxResponseBodyBytes: 4})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	if _, err := client.Get(server.URL, nil); err == nil || !strings.Contains(err.Error(), "exceeds 4 bytes") {
		t.Fatalf("Get() error=%v", err)
	}
}

func TestResponseContentLengthCompatibility(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Length", "5")
		_, _ = io.WriteString(writer, "12345")
	}))
	defer server.Close()
	client, err := NewClient(Config{Profile: "chrome_146"})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	response, err := client.Get(server.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	if response.ContentLength() != 5 {
		t.Fatalf("body length=%d", response.ContentLength())
	}
}

func TestMultipartContentType(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		reader, err := request.MultipartReader()
		if err != nil {
			http.Error(writer, err.Error(), http.StatusBadRequest)
			return
		}
		part, err := reader.NextPart()
		if err != nil {
			http.Error(writer, err.Error(), http.StatusBadRequest)
			return
		}
		_, _ = io.WriteString(writer, part.Header.Get("Content-Type"))
	}))
	defer server.Close()
	client, err := NewClient(Config{Profile: "chrome_146"})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	response, err := client.PostMultipart(server.URL, nil, []FileField{{
		FieldName: "data", FileName: "data.json", ContentType: "application/json", Data: []byte(`{"ok":true}`),
	}}, nil)
	if err != nil {
		t.Fatalf("PostMultipart() error: %v", err)
	}
	if response.Text() != "application/json" {
		t.Fatalf("multipart Content-Type=%q", response.Text())
	}
}

func TestContextCancellation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		<-request.Context().Done()
	}))
	defer server.Close()
	client, err := NewClient(Config{Profile: "chrome_146", Timeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := client.GetContext(ctx, server.URL, nil); err == nil {
		t.Fatal("canceled context request unexpectedly succeeded")
	}
}

func TestNormalizeProxyURL(t *testing.T) {
	tests := []struct {
		value     string
		proxyType string
		want      string
	}{
		{"127.0.0.1:8080", "http", "http://127.0.0.1:8080"},
		{"127.0.0.1:1080:user:p@ss", "socks5", "socks5://user:p%40ss@127.0.0.1:1080"},
		{"[::1]:1080", "socks5", "socks5://[::1]:1080"},
	}
	for _, test := range tests {
		got, err := normalizeProxyURL(test.value, test.proxyType)
		if err != nil {
			t.Fatalf("normalizeProxyURL(%q): %v", test.value, err)
		}
		if got != test.want {
			t.Fatalf("normalizeProxyURL(%q)=%q want=%q", test.value, got, test.want)
		}
	}
	for _, invalid := range []string{"missing-port", "http://host:99999", "ftp://host:21", "http://host:80/path"} {
		if _, err := normalizeProxyURL(invalid, "http"); err == nil {
			t.Fatalf("normalizeProxyURL(%q) unexpectedly succeeded", invalid)
		}
	}
}

func TestInvalidProfileCannotBeRebuiltByChainableSetters(t *testing.T) {
	client := NewWithConfig(Config{Profile: "not-a-browser"})
	client.ClearProxy().SetVerify(false)
	if client.ConfigError() == nil {
		t.Fatal("invalid profile error was unexpectedly cleared")
	}
}
