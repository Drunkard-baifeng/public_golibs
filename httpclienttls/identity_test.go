package httpclienttls

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	tlsclient "github.com/Drunkard-baifeng/public_golibs/httpclienttls/internal/tlsclient"
	fhttp "github.com/bogdanfinn/fhttp"
)

// JSON fixtures also exercise configuration decoding without sharing the
// production identity builder or its expected header calculations.
func identityConfig(t *testing.T, raw string) Config {
	t.Helper()
	var config Config
	if err := json.Unmarshal([]byte(raw), &config); err != nil {
		t.Fatal(err)
	}
	return config
}

func TestDesktopIdentityReachesWire(t *testing.T) {
	for _, h2 := range []bool{false, true} {
		server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode(r.Header)
		}))
		server.EnableHTTP2 = h2
		server.StartTLS()
		for _, tc := range []struct{ profile, platform, uaPart, hint string }{
			{"chrome_146", "macOS", "Macintosh; Intel Mac OS X 10_15_7", `"macOS"`},
			{"chrome_144", "linux", "X11; Linux x86_64", `"Linux"`},
			{"brave_146", "macOS", "Macintosh; Intel Mac OS X 10_15_7", `"macOS"`},
			{"firefox_148", "macOS", "Macintosh; Intel Mac OS X 10.15", ""},
		} {
			config := identityConfig(t, `{"Profile":"`+tc.profile+`","Verify":false,"BrowserIdentity":{"Platform":"`+tc.platform+`"}}`)
			client, err := NewClient(config)
			if err != nil {
				t.Fatal(err)
			}
			response, err := client.Get(server.URL, nil)
			client.Close()
			if err != nil {
				t.Fatal(err)
			}
			var headers http.Header
			if err := response.JSON(&headers); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(headers.Get("User-Agent"), tc.uaPart) || headers.Get("Sec-CH-UA-Platform") != tc.hint {
				t.Errorf("h2=%t %s/%s: UA=%q platform=%q", h2, tc.profile, tc.platform, headers.Get("User-Agent"), headers.Get("Sec-CH-UA-Platform"))
			}
			if (response.Proto == "HTTP/2.0") != h2 {
				t.Errorf("protocol=%s h2=%t", response.Proto, h2)
			}
		}
		server.Close()
	}
}

const highEntropyConfig = `{"Profile":"chrome_146","HighEntropyClientHints":true,"BrowserIdentity":{"Platform":"macOS","PlatformVersion":"15.7.0","Architecture":"arm","Bitness":"64","FullVersion":"146.0.1.2"}}`

func TestHighEntropyHintsOnWireAndRedirect(t *testing.T) {
	for _, protocol := range []string{"http", "https", "h2"} {
		t.Run(protocol, func(t *testing.T) {
			server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/start" {
					http.Redirect(w, r, "/end", http.StatusFound)
					return
				}
				json.NewEncoder(w).Encode(r.Header)
			}))
			server.EnableHTTP2 = protocol == "h2"
			if protocol == "http" {
				server.Start()
			} else {
				server.StartTLS()
			}
			defer server.Close()
			config := identityConfig(t, highEntropyConfig)
			verify := false
			config.Verify = &verify
			config.StrictFingerprint = true
			client, err := NewClient(config)
			if err != nil {
				t.Fatal(err)
			}
			defer client.Close()
			response, err := client.Get(server.URL+"/start", nil)
			if err != nil {
				t.Fatal(err)
			}
			var headers http.Header
			if err := response.JSON(&headers); err != nil {
				t.Fatal(err)
			}
			for key, want := range map[string]string{
				"Sec-CH-UA-Platform":          `"macOS"`,
				"Sec-CH-UA-Platform-Version":  `"15.7.0"`,
				"Sec-CH-UA-Arch":              `"arm"`,
				"Sec-CH-UA-Bitness":           `"64"`,
				"Sec-CH-UA-Model":             `""`,
				"Sec-CH-UA-Full-Version-List": `"Google Chrome";v="146.0.1.2", "Chromium";v="146.0.1.2", "Not=A?Brand";v="24.0.0.0"`,
			} {
				if got := headers.Get(key); got != want {
					t.Errorf("%s=%q want=%q", key, got, want)
				}
			}
			if !strings.Contains(headers.Get("User-Agent"), "Chrome/146.0.0.0") {
				t.Errorf("reduced UA changed to full version: %q", headers.Get("User-Agent"))
			}
			if (response.Proto == "HTTP/2.0") != (protocol == "h2") {
				t.Errorf("unexpected wire protocol: %s", response.Proto)
			}
		})
	}
}

func TestHighEntropyHintsFollowRedirectTrustOnWire(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/downgrade" {
			http.Redirect(w, r, "http://plain.example/end", http.StatusFound)
			return
		}
		if r.URL.Path == "/upgrade" {
			http.Redirect(w, r, "https://secure.example/end", http.StatusFound)
			return
		}
		json.NewEncoder(w).Encode(r.Header)
	})
	plain := httptest.NewServer(handler)
	defer plain.Close()
	secure := httptest.NewUnstartedServer(handler)
	secure.EnableHTTP2 = true
	secure.StartTLS()
	defer secure.Close()
	config := identityConfig(t, highEntropyConfig)
	config.StrictFingerprint = true
	client, err := NewClient(config)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	routes := map[string]string{"plain.example:80": plain.Listener.Addr().String(), "secure.example:443": secure.Listener.Addr().String()}
	inner, err := tlsclient.NewHttpClient(tlsclient.NewNoopLogger(),
		tlsclient.WithClientProfile(client.profile.client), tlsclient.WithNotFollowRedirects(),
		tlsclient.WithDisableHttp3(), tlsclient.WithInsecureSkipVerify(),
		tlsclient.WithDialContext(func(ctx context.Context, network, address string) (net.Conn, error) {
			mapped, ok := routes[address]
			if !ok {
				return nil, fmt.Errorf("unexpected test destination %q", address)
			}
			return (&net.Dialer{}).DialContext(ctx, network, mapped)
		}))
	if err != nil {
		t.Fatal(err)
	}
	client.inner.CloseIdleConnections()
	client.inner = inner
	for _, tc := range []struct {
		target, protocol string
		hints            bool
	}{
		{"https://secure.example/downgrade", "HTTP/1.1", false},
		{"http://plain.example/upgrade", "HTTP/2.0", true},
	} {
		response, err := client.Get(tc.target, nil)
		if err != nil {
			t.Fatal(err)
		}
		var headers http.Header
		if err := response.JSON(&headers); err != nil {
			t.Fatal(err)
		}
		for key, value := range map[string]string{
			"Sec-CH-UA-Full-Version-List": `"Google Chrome";v="146.0.1.2", "Chromium";v="146.0.1.2", "Not=A?Brand";v="24.0.0.0"`,
			"Sec-CH-UA-Platform-Version":  `"15.7.0"`,
			"Sec-CH-UA-Arch":              `"arm"`,
			"Sec-CH-UA-Bitness":           `"64"`,
			"Sec-CH-UA-Model":             `""`,
		} {
			want := value
			if !tc.hints {
				want = ""
			}
			if got := headers.Get(key); got != want {
				t.Errorf("%s %s=%q, want %q", tc.target, key, got, want)
			}
		}
		if response.Proto != tc.protocol {
			t.Errorf("%s protocol=%s, want %s", tc.target, response.Proto, tc.protocol)
		}
	}
}

func TestHighEntropyHintsRespectTrustOptOutAndExplicitValues(t *testing.T) {
	client, err := NewClient(identityConfig(t, highEntropyConfig))
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	profile := client.Profile()
	for _, tc := range []struct {
		target  string
		mode    BrowserMode
		present bool
	}{
		{"https://example.com", BrowserModeIdentity, true},
		{"http://example.com", BrowserModeIdentity, false},
		{"http://127.0.0.1", BrowserModeIdentity, true},
		{"https://example.com", BrowserModeOff, false},
	} {
		headers := make(fhttp.Header)
		if err := applyBrowserHeaders(headers, profile, tc.mode, tc.target); err != nil {
			t.Fatal(err)
		}
		if (headers.Get("Sec-CH-UA-Arch") != "") != tc.present {
			t.Errorf("%s mode=%s arch=%q", tc.target, tc.mode, headers.Get("Sec-CH-UA-Arch"))
		}
	}
	headers := fhttp.Header{"Sec-Ch-Ua-Arch": {""}, "Sec-Ch-Ua-Bitness": {`"32"`}}
	state := newBrowserHeaderState(headers, BrowserModeIdentity, "https://example.com/start")
	if err := applyBrowserHeaders(headers, profile, BrowserModeIdentity, "https://example.com/start"); err != nil {
		t.Fatal(err)
	}
	state.redirect(headers, profile, BrowserModeIdentity, "http://plain.example/end")
	if headers.Get("Sec-CH-UA-Full-Version-List") != "" || headers.Get("Sec-CH-UA-Bitness") != `"32"` {
		t.Fatalf("downgrade did not distinguish generated and explicit hints: %v", headers)
	}
	state.redirect(headers, profile, BrowserModeIdentity, "https://example.com/end")
	if headers.Get("Sec-CH-UA-Full-Version-List") == "" || headers.Get("Sec-CH-UA-Arch") != "" {
		t.Fatalf("upgrade lost identity or explicit suppression: %v", headers)
	}
}

func TestInvalidIdentityRejectedAtConstruction(t *testing.T) {
	for _, raw := range []string{
		`{"Profile":"chrome_146","BrowserIdentity":{"Platform":"Android"}}`,
		`{"Profile":"safari_ios_18_5","BrowserIdentity":{"Platform":"macOS"}}`,
		`{"Profile":"chrome_146","BrowserIdentity":{"Platform":"iOS"}}`,
		`{"Profile":"chrome_146","BrowserIdentity":{"FullVersion":"145.0.1.2"}}`,
		`{"Profile":"chrome_146","BrowserIdentity":{"Architecture":"invalid"}}`,
		`{"Profile":"chrome_146","BrowserIdentity":{"Bitness":"128"}}`,
		`{"Profile":"chrome_146","BrowserIdentity":{"PlatformVersion":"15\r\nInjected: x"}}`,
		`{"Profile":"chrome_146","HighEntropyClientHints":true}`,
		`{"Profile":"firefox_148","HighEntropyClientHints":true}`,
		`{"Profile":"chrome_146","StrictFingerprint":true,"UserAgent":"Firefox/148.0"}`,
	} {
		client, err := NewClient(identityConfig(t, raw))
		if client != nil {
			client.Close()
		}
		if err == nil {
			t.Errorf("invalid identity accepted: %s", raw)
		}
	}
}

func TestStrictFingerprintChecksMergedHeadersBeforeDispatch(t *testing.T) {
	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	client, err := NewClient(identityConfig(t, `{"Profile":"chrome_146","StrictFingerprint":true}`))
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	for _, options := range []*Options{
		{Headers: map[string]string{"User-Agent": "Firefox/148.0"}},
		{Headers: map[string]string{"sec-ch-ua-platform": `"macOS"`}},
		{HeaderValues: http.Header{"Sec-Ch-Ua-Mobile": {"?1"}}},
		{HeaderValues: http.Header{"Sec-Ch-Ua-Platform": {`"Windows"`, `"Windows"`}}},
	} {
		if _, err := client.Get(server.URL, options); err == nil {
			t.Error("conflicting identity dispatched")
		}
	}
	client.SetHeaders(map[string]string{"Sec-CH-UA": `"Chromium";v="99"`})
	if _, err := client.Get(server.URL, nil); err == nil {
		t.Error("conflicting client default dispatched")
	}
	if hits.Load() != 0 {
		t.Errorf("origin saw %d invalid requests", hits.Load())
	}
	client.ClearHeaders()
	for _, options := range []*Options{nil, {BrowserMode: BrowserModeOff}, {Headers: map[string]string{"User-Agent": "", "Sec-CH-UA": ""}}} {
		if _, err := client.Get(server.URL, options); err != nil {
			t.Errorf("consistent or suppressed identity rejected: %v", err)
		}
	}
}

func TestStrictFingerprintRejectsUnmodeledClientHints(t *testing.T) {
	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	for _, browserHeaders := range []bool{false, true} {
		for _, strict := range []bool{false, true} {
			client, err := NewClient(Config{Profile: "chrome_146", BrowserHeaders: &browserHeaders, StrictFingerprint: strict})
			if err != nil {
				t.Fatal(err)
			}
			for _, tc := range []struct{ key, value string }{
				{"sec-ch-ua-full-version", `"999.0.0.0"`},
				{"Sec-CH-UA-WoW64", "?1"},
				{"sEc-Ch-Ua-FoRm-FaCtOrS", `"Mobile"`},
			} {
				before := hits.Load()
				_, err := client.Get(server.URL, &Options{Headers: map[string]string{tc.key: tc.value}})
				if strict {
					if err == nil || hits.Load() != before {
						t.Errorf("strict mode dispatched unmodeled %s (browser headers=%t)", tc.key, browserHeaders)
					}
				} else if err != nil || hits.Load() != before+1 {
					t.Errorf("non-strict override changed for %s: %v", tc.key, err)
				}
			}
			if _, err := client.Get(server.URL, &Options{Headers: map[string]string{"Sec-CH-UA-WoW64": "", "Sec-CH-Prefers-Color-Scheme": "dark"}}); err != nil {
				t.Errorf("suppressed identity or unrelated hint rejected: %v", err)
			}
			client.Close()
		}
	}
}

func TestSeededProfileSelectionIsStableAcrossAliasesAndPoolOrder(t *testing.T) {
	for _, tc := range []struct{ seed, want string }{
		{"fixture-1", "firefox_148"},
		{"fixture-2", "chrome_146"},
		{"fixture-4", "chrome_144"},
	} {
		for _, pool := range []string{
			`["chrome_144","chrome_146","firefox_148"]`,
			`["firefox148","chrome146","chrome-144","chrome_146"]`,
		} {
			for attempt := 0; attempt < 3; attempt++ {
				client, err := NewClient(identityConfig(t, `{"FingerprintSeed":"`+tc.seed+`","ProfilePool":`+pool+`}`))
				if err != nil {
					t.Fatal(err)
				}
				got := client.Profile().Name
				client.Close()
				if got != tc.want {
					t.Errorf("seed=%s pool=%s got=%s want=%s", tc.seed, pool, got, tc.want)
				}
			}
		}
	}
}

func TestHighEntropyHeadersHaveOrderAndRespectExplicitOrder(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/start" {
			http.Redirect(w, r, "/end", http.StatusFound)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	client, err := NewClient(identityConfig(t, highEntropyConfig))
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	for _, mode := range []BrowserMode{BrowserModeIdentity, BrowserModeNavigation, BrowserModeFetch, BrowserModeJSON, BrowserModeScript} {
		response, err := client.Get(server.URL+"/start", &Options{BrowserMode: mode})
		if err != nil {
			t.Fatal(err)
		}
		order := response.Request.Header.Values(fhttp.HeaderOrderKey)
		for _, name := range []string{"sec-ch-ua-full-version-list", "sec-ch-ua-platform-version", "sec-ch-ua-arch", "sec-ch-ua-bitness", "sec-ch-ua-model"} {
			count := 0
			for _, key := range order {
				if key == name {
					count++
				}
			}
			if count != 1 {
				t.Errorf("mode=%s header=%s count=%d", mode, name, count)
			}
		}
	}
	response, err := client.Get(server.URL+"/start", &Options{HeaderOrder: []string{}})
	if err != nil {
		t.Fatal(err)
	}
	if len(response.Request.Header.Values(fhttp.HeaderOrderKey)) != 0 {
		t.Error("high-entropy defaults replaced an explicit empty order")
	}
}

func TestIdentityIsolationAndOptInDefaults(t *testing.T) {
	config := identityConfig(t, highEntropyConfig)
	client, err := NewClient(config)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	// Mutating decoded caller configuration must not mutate existing clients.
	encoded, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(strings.ReplaceAll(string(encoded), "macOS", "Linux")), &config); err != nil {
		t.Fatal(err)
	}
	if client.Profile().Platform != "macOS" {
		t.Fatal("caller mutation changed client identity")
	}
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			other, err := NewClient(Config{Profile: "chrome_146"})
			if err != nil {
				t.Error(err)
				return
			}
			defer other.Close()
			headers := make(fhttp.Header)
			if err := applyBrowserHeaders(headers, other.Profile(), BrowserModeIdentity, "https://example.com"); err != nil {
				t.Error(err)
				return
			}
			if headers.Get("Sec-CH-UA-Platform") != `"Windows"` || headers.Get("Sec-CH-UA-Arch") != "" {
				t.Errorf("identity leaked into default clients: %v", headers)
			}
		}()
	}
	wg.Wait()
}

func TestIdentityPoolSelectionNeverRandomlyFails(t *testing.T) {
	// The implicit pool contains iOS and non-Chromium presets; incompatible
	// defaults must be filtered before drawing a sample, not fail afterward.
	for i := 0; i < 20; i++ {
		config := identityConfig(t, `{"BrowserIdentity":{"Platform":"macOS"}}`)
		client, err := NewClient(config)
		if err != nil {
			t.Fatalf("desktop default selection: %v", err)
		}
		if client.Profile().Mobile {
			t.Error("desktop identity selected mobile transport")
		}
		client.Close()
	}
	config := identityConfig(t, strings.Replace(highEntropyConfig, `"Profile":"chrome_146",`, "", 1))
	client, err := NewClient(config)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	if name := client.Profile().Name; name != "chrome_146" && name != "brave_146" {
		t.Fatalf("full version selected incompatible profile %s", name)
	}
	// An explicitly supplied pool is a contract: validate every entry before
	// selection instead of silently dropping user choices.
	config = identityConfig(t, `{"ProfilePool":["chrome_146","safari_ios_18_5"],"BrowserIdentity":{"Platform":"macOS"}}`)
	for i := 0; i < 10; i++ {
		client, err := NewClient(config)
		if client != nil {
			client.Close()
		}
		if err == nil {
			t.Fatal("partially incompatible explicit pool was accepted")
		}
	}
}
