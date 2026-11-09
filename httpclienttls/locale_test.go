package httpclienttls

import (
	"context"
	"errors"
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

func TestExitLocaleAutoLanguageAndProbeIsolation(t *testing.T) {
	var probes atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/geo" {
			probes.Add(1)
			if r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" || r.Header.Get("X-Business") != "" {
				t.Error("business credentials sent to geolocation endpoint")
			}
			http.SetCookie(w, &http.Cookie{Name: "geo-cookie", Value: "ignore", Path: "/"})
			fmt.Fprint(w, `{"ip":"203.0.113.9","success":true,"country_code":"JP","city":"Tokyo","timezone":{"id":"Asia/Tokyo"}}`)
			return
		}
		if strings.Contains(r.Header.Get("Cookie"), "geo-cookie") {
			t.Error("geolocation cookie leaked into business session")
		}
		fmt.Fprint(w, r.Header.Get("Accept-Language"))
	}))
	defer server.Close()
	config := identityConfig(t, `{"Profile":"chrome_146","ExitLocale":{"Endpoints":["`+server.URL+`/geo"]}}`)
	client, err := NewClient(config)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	client.SetHeaders(map[string]string{"Authorization": "Bearer fixture", "X-Business": "private"})
	client.SetCookies(map[string]string{"business-cookie": "private"})
	if probes.Load() != 0 {
		t.Fatal("constructor performed unexpected network I/O")
	}
	for i := 0; i < 2; i++ {
		response, err := client.Get(server.URL+"/business", nil)
		if err != nil {
			t.Fatal(err)
		}
		if got := response.Text(); got != "ja-JP,ja;q=0.9,en-US;q=0.8,en;q=0.7" {
			t.Errorf("auto language=%q", got)
		}
	}
	if got := probes.Load(); got != 1 {
		t.Errorf("probe count=%d, want one cached lookup", got)
	}
}

func TestExitLocaleManualPrecedenceAndOptOut(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/geo" {
			fmt.Fprint(w, `{"ip":"203.0.113.9","country_code":"JP","timezone":{"id":"Asia/Tokyo"}}`)
			return
		}
		fmt.Fprint(w, r.Header.Get("Accept-Language"))
	}))
	defer server.Close()
	for _, tc := range []struct {
		name     string
		language string
		headers  map[string]string
		options  *Options
		off      bool
		want     string
	}{
		{name: "config", language: "fr-FR,fr;q=0.9", want: "fr-FR,fr;q=0.9"},
		{name: "client header", language: "fr-FR", headers: map[string]string{"accept-language": "de-DE"}, want: "de-DE"},
		{name: "request header", headers: map[string]string{"Accept-Language": "de-DE"}, options: &Options{Headers: map[string]string{"Accept-Language": "es-ES"}}, want: "es-ES"},
		{name: "suppressed", options: &Options{HeaderValues: http.Header{"accept-language": nil}}},
		{name: "mode off", options: &Options{BrowserMode: BrowserModeOff}},
		{name: "headers off", off: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client, err := NewClient(Config{Profile: "chrome_146", AcceptLanguage: tc.language, BrowserHeaders: BoolPtr(!tc.off), ExitLocale: &ExitLocaleConfig{Endpoints: []string{server.URL + "/geo"}}})
			if err != nil {
				t.Fatal(err)
			}
			defer client.Close()
			client.SetHeaders(tc.headers)
			response, err := client.Get(server.URL, tc.options)
			if err != nil {
				t.Fatal(err)
			}
			if response.Text() != tc.want {
				t.Errorf("language=%q want %q", response.Text(), tc.want)
			}
			info := client.Locale()
			if info.Country != "JP" || info.Timezone != "Asia/Tokyo" || info.IP != "203.0.113.9" || info.DetectedAt.IsZero() || info.LastError != "" {
				t.Errorf("invalid metadata: %+v", info)
			}
			if tc.language != "" && info.AcceptLanguage != tc.language {
				t.Errorf("metadata lost manual language: %+v", info)
			}
			if client.Profile().Name != "chrome_146" || client.Profile().Platform != "Windows" {
				t.Fatal("IP lookup changed transport/platform identity")
			}
		})
	}
}

func TestExitLocaleUsesProxyWithoutDirectFallback(t *testing.T) {
	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if r.URL.Path == "/geo" {
			fmt.Fprint(w, `{"ip":"203.0.113.9","country":"DE"}`)
			return
		}
		fmt.Fprint(w, r.Header.Get("Accept-Language"))
	}))
	defer server.Close()
	proxy := tunnelFixture(t, strings.TrimPrefix(server.URL, "http://"))
	client, err := NewClient(Config{Profile: "chrome_146", Proxy: proxy, ExitLocale: &ExitLocaleConfig{Endpoints: []string{server.URL + "/geo"}, Timeout: time.Second}})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	response, err := client.Get(server.URL, nil)
	if err != nil || response.Text() != "de-DE,de;q=0.9,en-US;q=0.8,en;q=0.7" {
		t.Fatalf("proxy lookup: response=%v err=%v", response, err)
	}
	if hits.Load() != 2 {
		t.Fatalf("unexpected origin hit count %d", hits.Load())
	}
	denied := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusProxyAuthRequired) }))
	defer denied.Close()
	if err := client.ConfigureProxy(denied.URL, "http"); err != nil {
		t.Fatal(err)
	}
	if client.Locale().Country != "" {
		t.Fatal("old route cache survived proxy change")
	}
	if _, err := client.Get(server.URL, nil); err == nil {
		t.Error("failed proxy silently used direct route")
	}
	if hits.Load() != 2 {
		t.Error("origin received a direct fallback request")
	}
	if client.Locale().LastError == "" {
		t.Error("missing lookup diagnostics")
	}
}

func TestExitLocaleConcurrentCacheAndRefresh(t *testing.T) {
	var probes atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/geo" {
			probes.Add(1)
			fmt.Fprint(w, `{"ip":"203.0.113.9","country":"JP"}`)
			return
		}
		fmt.Fprint(w, r.Header.Get("Accept-Language"))
	}))
	defer server.Close()
	settings := &ExitLocaleConfig{Endpoints: []string{server.URL + "/geo"}}
	client, err := NewClient(Config{Profile: "chrome_146", ExitLocale: settings})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	settings.Endpoints[0] = server.URL + "/invalid-caller-mutation"
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			response, err := client.Get(server.URL, nil)
			if err != nil {
				t.Error(err)
				return
			}
			if !strings.HasPrefix(response.Text(), "ja-JP,") {
				t.Errorf("missing locale: %q", response.Text())
			}
		}()
	}
	wg.Wait()
	if probes.Load() != 1 {
		t.Errorf("concurrent probes=%d, want 1", probes.Load())
	}
	info, err := client.RefreshLocale(context.Background())
	if err != nil || info.Country != "JP" || probes.Load() != 2 {
		t.Fatalf("forced refresh: info=%+v probes=%d err=%v", info, probes.Load(), err)
	}
}

func TestExitLocaleCacheExpiryAndFailureBackoff(t *testing.T) {
	var probes atomic.Int32
	var fail atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/geo" {
			probes.Add(1)
			if fail.Load() {
				w.WriteHeader(http.StatusTooManyRequests)
				return
			}
			fmt.Fprint(w, `{"ip":"203.0.113.9","country":"JP"}`)
			return
		}
		fmt.Fprint(w, r.Header.Get("Accept-Language"))
	}))
	defer server.Close()
	client, err := NewClient(Config{Profile: "chrome_146", ExitLocale: &ExitLocaleConfig{Endpoints: []string{server.URL + "/geo"}, CacheTTL: 40 * time.Millisecond, FailureTTL: time.Hour}})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	if _, err := client.RefreshLocale(context.Background()); err != nil {
		t.Fatal(err)
	}
	time.Sleep(time.Until(client.Locale().ExpiresAt))
	fail.Store(true)
	for i := 0; i < 2; i++ {
		r, err := client.Get(server.URL, nil)
		if err != nil {
			t.Fatal(err)
		}
		if r.Text() != "en-US,en;q=0.9" {
			t.Errorf("failure retained stale geo language %q", r.Text())
		}
	}
	if probes.Load() != 2 {
		t.Errorf("expiry/backoff probes=%d want 2", probes.Load())
	}
	if info := client.Locale(); info.LastError == "" || info.Country != "" || client.ConfigError() != nil {
		t.Errorf("invalid failure metadata: %+v", info)
	}
	fail.Store(false)
	if info, err := client.RefreshLocale(context.Background()); err != nil || info.Country != "JP" {
		t.Errorf("manual recovery: %+v %v", info, err)
	}
}

func TestExitLocaleCancellationDoesNotPoisonCache(t *testing.T) {
	var probes atomic.Int32
	entered := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/geo" {
			if probes.Add(1) == 1 {
				close(entered)
				<-r.Context().Done()
				return
			}
			fmt.Fprint(w, `{"ip":"203.0.113.9","country":"JP"}`)
			return
		}
		fmt.Fprint(w, r.Header.Get("Accept-Language"))
	}))
	defer server.Close()
	client, err := NewClient(Config{Profile: "chrome_146", ExitLocale: &ExitLocaleConfig{Endpoints: []string{server.URL + "/geo"}}})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { _, err := client.Do(ctx, "GET", server.URL, nil, nil); done <- err }()
	<-entered
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel error=%v", err)
	}
	if info := client.Locale(); !info.ExpiresAt.IsZero() || info.LastError != "" {
		t.Errorf("caller cancellation cached: %+v", info)
	}
	r, err := client.Get(server.URL, nil)
	if err != nil || !strings.HasPrefix(r.Text(), "ja-JP,") || probes.Load() != 2 {
		t.Fatalf("retry: %v %v probes=%d", r, err, probes.Load())
	}
}

func TestExitLocaleLateLookupCannotOverwriteNewProxy(t *testing.T) {
	var probes atomic.Int32
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if probes.Add(1) == 1 {
			close(entered)
			<-release
			fmt.Fprint(w, `{"ip":"203.0.113.1","country":"JP"}`)
			return
		}
		fmt.Fprint(w, `{"ip":"203.0.113.2","country":"DE"}`)
	}))
	defer server.Close()
	defer once.Do(func() { close(release) })
	client, err := NewClient(Config{Profile: "chrome_146", ExitLocale: &ExitLocaleConfig{Endpoints: []string{server.URL}}})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	done := make(chan error, 1)
	go func() { _, err := client.RefreshLocale(context.Background()); done <- err }()
	<-entered
	if err := client.ConfigureProxy(tunnelFixture(t, strings.TrimPrefix(server.URL, "http://")), "http"); err != nil {
		t.Fatal(err)
	}
	if info, err := client.RefreshLocale(context.Background()); err != nil || info.Country != "DE" {
		t.Fatalf("new route lookup: %+v %v", info, err)
	}
	once.Do(func() { close(release) })
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if info := client.Locale(); info.Country != "DE" || info.IP != "203.0.113.2" {
		t.Errorf("old result overwrote new route: %+v", info)
	}
}

func TestExitLocaleHTTP2AndProviderTimeout(t *testing.T) {
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.ProtoMajor != 2 {
			t.Error("lookup/business request did not use HTTP/2")
		}
		switch r.URL.Path {
		case "/slow":
			<-r.Context().Done()
		case "/geo":
			fmt.Fprint(w, `{"ip":"2001:db8::1","country":"FR","timezone":"Europe/Paris"}`)
		default:
			io.WriteString(w, r.Header.Get("Accept-Language"))
		}
	}))
	server.EnableHTTP2 = true
	server.StartTLS()
	defer server.Close()
	client, err := NewClient(Config{Profile: "chrome_146", Verify: BoolPtr(false), ExitLocale: &ExitLocaleConfig{Endpoints: []string{server.URL + "/slow", server.URL + "/geo"}, Timeout: 600 * time.Millisecond}})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	r, err := client.Get(server.URL, nil)
	if err != nil || r.Proto != "HTTP/2.0" || !strings.HasPrefix(r.Text(), "fr-FR,") {
		t.Fatalf("timed failover: %v %v", r, err)
	}
	if info := client.Locale(); info.Timezone != "Europe/Paris" || info.LastError != "" {
		t.Errorf("HTTP2 metadata: %+v", info)
	}
}

func TestExitLocaleProviderFailover(t *testing.T) {
	var probes atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/first":
			probes.Add(1)
			fmt.Fprint(w, `{"success":false,"message":"fixture failure"}`)
		case "/second":
			probes.Add(1)
			fmt.Fprint(w, `{"ip":"2001:db8::1","country":"DE","timezone":"Europe/Berlin"}`)
		default:
			fmt.Fprint(w, r.Header.Get("Accept-Language"))
		}
	}))
	defer server.Close()
	config := identityConfig(t, `{"Profile":"chrome_146","ExitLocale":{"Endpoints":["`+server.URL+`/first","`+server.URL+`/second"]}}`)
	client, err := NewClient(config)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	response, err := client.Get(server.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := response.Text(); got != "de-DE,de;q=0.9,en-US;q=0.8,en;q=0.7" {
		t.Errorf("failover language=%q", got)
	}
	if probes.Load() != 2 {
		t.Errorf("probe count=%d, want two providers", probes.Load())
	}
}

func TestExitLocaleInvalidConfigStaysInvalid(t *testing.T) {
	for _, settings := range []*ExitLocaleConfig{
		{Endpoints: []string{}},
		{Endpoints: []string{"http://public.example/geo"}},
		{Endpoints: []string{"https://user:password@example.com/geo"}},
		{Endpoints: []string{"https://example.com/geo#fragment"}},
		{Endpoints: []string{"file:///tmp/geo.json"}},
		{Endpoints: []string{"https://example.com:bad/geo"}},
		{Timeout: -time.Second}, {CacheTTL: -time.Second}, {FailureTTL: -time.Second},
	} {
		client := NewWithConfig(Config{Profile: "chrome_146", ExitLocale: settings})
		if client.ConfigError() == nil {
			t.Errorf("invalid settings accepted: %+v", settings)
		}
		client.ClearProxy().SetVerify(false)
		if client.ConfigError() == nil {
			t.Error("proxy/TLS reconfiguration erased invalid locale settings")
		}
		client.Close()
	}
}

func TestExitLocaleParsesProviderSchemas(t *testing.T) {
	for _, tc := range []struct{ body, country, timezone, language string }{
		{`{"ip":"203.0.113.1","country":"Japan","country_code":"jp","timezone":{"id":"Asia/Tokyo"}}`, "JP", "Asia/Tokyo", "ja-JP,ja;q=0.9,en-US;q=0.8,en;q=0.7"},
		{`{"ip":"2001:db8::1","country":"GB","timezone":"Europe/London","error":false}`, "GB", "Europe/London", "en-GB,en;q=0.9"},
		{`{"ip":"203.0.113.1","country":"XX","timezone":null}`, "XX", "", "en-US,en;q=0.9"},
		{`{"ip":"203.0.113.1","country":"HK","timezone":"Asia/Hong_Kong"}`, "HK", "Asia/Hong_Kong", "zh-HK,zh-TW;q=0.9,zh;q=0.8,en-US;q=0.7,en;q=0.6"},
	} {
		info, err := parseExitLocale([]byte(tc.body))
		if err != nil || info.Country != tc.country || info.Timezone != tc.timezone || info.AcceptLanguage != tc.language {
			t.Errorf("parse=%+v error=%v", info, err)
		}
	}
	for _, body := range []string{
		`null`, `[]`, `{`,
		`{"ip":"not an IP","country":"JP"}`,
		`{"ip":"203.0.113.1","country":"Japan"}`,
		`{"ip":"203.0.113.1","country":"JP","success":false}`,
		`{"ip":"203.0.113.1","country":"JP","error":true}`,
		`{"ip":"203.0.113.1","country":"JP","timezone":"Local"}`,
		`{"ip":"203.0.113.1","country":"JP","timezone":"../invalid"}`,
		`{"ip":"203.0.113.1","country":"JP","timezone":["Asia/Tokyo"]}`,
	} {
		if _, err := parseExitLocale([]byte(body)); err == nil {
			t.Errorf("malformed lookup accepted: %s", body)
		}
	}
}

func TestExitLocaleUntrustedResponsesFallBackWithoutRedirect(t *testing.T) {
	for _, mode := range []string{"redirect", "oversize", "invalid-json"} {
		t.Run(mode, func(t *testing.T) {
			var redirected atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/geo" {
					switch mode {
					case "redirect":
						http.Redirect(w, r, "/unexpected", http.StatusFound)
					case "oversize":
						fmt.Fprint(w, strings.Repeat(" ", 65<<10))
					case "invalid-json":
						fmt.Fprint(w, `{"private":"do-not-include-in-error"`)
					}
					return
				}
				if r.URL.Path == "/unexpected" {
					redirected.Add(1)
				}
				fmt.Fprint(w, r.Header.Get("Accept-Language"))
			}))
			defer server.Close()
			client, err := NewClient(Config{Profile: "chrome_146", ExitLocale: &ExitLocaleConfig{Endpoints: []string{server.URL + "/geo?token=private-query"}}})
			if err != nil {
				t.Fatal(err)
			}
			defer client.Close()
			r, err := client.Get(server.URL, nil)
			if err != nil || r.Text() != "en-US,en;q=0.9" {
				t.Fatalf("nonfatal lookup fallback: %v %v", r, err)
			}
			info := client.Locale()
			if info.LastError == "" || strings.Contains(info.LastError, "private") || redirected.Load() != 0 {
				t.Errorf("response/diagnostics isolation failed: %+v redirects=%d", info, redirected.Load())
			}
		})
	}
}

func TestExitLocaleProbeRespectsBusinessDeadline(t *testing.T) {
	var business atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/geo" {
			<-r.Context().Done()
			return
		}
		business.Add(1)
	}))
	defer server.Close()
	client, err := NewClient(Config{Profile: "chrome_146", Timeout: 50 * time.Millisecond, ExitLocale: &ExitLocaleConfig{Endpoints: []string{server.URL + "/geo"}, Timeout: time.Second}})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	if _, err := client.Get(server.URL, nil); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("request timeout error=%v", err)
	}
	if business.Load() != 0 {
		t.Error("expired request was dispatched")
	}
	if !client.Locale().ExpiresAt.IsZero() {
		t.Error("business deadline was cached as provider failure")
	}
}

func TestExitLocaleDisabledAndClosedClientNeverProbe(t *testing.T) {
	var probes atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { probes.Add(1) }))
	defer server.Close()
	client, err := NewClient(Config{Profile: "chrome_146"})
	if err != nil {
		t.Fatal(err)
	}
	if info, err := client.RefreshLocale(context.Background()); err == nil || info.AcceptLanguage != "en-US,en;q=0.9" {
		t.Errorf("disabled lookup: %+v %v", info, err)
	}
	client.Close()
	client, err = NewClient(Config{Profile: "chrome_146", ExitLocale: &ExitLocaleConfig{Endpoints: []string{server.URL}}})
	if err != nil {
		t.Fatal(err)
	}
	client.Close()
	if _, err := client.RefreshLocale(context.Background()); err == nil {
		t.Error("closed client lookup succeeded")
	}
	if _, err := client.Get(server.URL, nil); err == nil {
		t.Error("closed client request succeeded")
	}
	if probes.Load() != 0 {
		t.Errorf("unexpected probe count=%d", probes.Load())
	}
}

func TestExitLocaleCanceledWaiterDoesNotCancelLeader(t *testing.T) {
	var probes atomic.Int32
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/geo" {
			probes.Add(1)
			close(entered)
			<-release
			fmt.Fprint(w, `{"ip":"203.0.113.9","country":"JP"}`)
			return
		}
		fmt.Fprint(w, r.Header.Get("Accept-Language"))
	}))
	defer server.Close()
	defer once.Do(func() { close(release) })
	client, err := NewClient(Config{Profile: "chrome_146", ExitLocale: &ExitLocaleConfig{Endpoints: []string{server.URL + "/geo"}}})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	done := make(chan error, 1)
	go func() { _, err := client.RefreshLocale(context.Background()); done <- err }()
	<-entered
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer cancel()
	if _, err := client.Do(ctx, "GET", server.URL, nil, nil); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("waiter error=%v", err)
	}
	select {
	case err := <-done:
		t.Fatalf("waiter ended leader: %v", err)
	default:
	}
	once.Do(func() { close(release) })
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	response, err := client.Get(server.URL, nil)
	if err != nil || !strings.HasPrefix(response.Text(), "ja-JP,") || probes.Load() != 1 {
		t.Fatalf("leader/cache corrupted: %v %v probes=%d", response, err, probes.Load())
	}
}
