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

	fhttp "github.com/bogdanfinn/fhttp"
)

const negotiatedHintsConfig = `{"Profile":"chrome_146","Verify":false,"ClientHints":{},"StrictFingerprint":true,"BrowserIdentity":{"Platform":"macOS","PlatformVersion":"15.7.0","Architecture":"arm","Bitness":"64","FullVersion":"146.0.1.2"}}`

func TestClientHintsLearnOnlyRequestedFields(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/learn":
			if r.Header.Get("Sec-CH-UA-Arch") != "" {
				t.Error("unsolicited high entropy hint")
			}
			w.Header().Add("Accept-CH", "Sec-CH-UA-Arch")
			w.Header().Add("Accept-CH", "Sec-CH-UA-Full-Version-List;example=1")
		case "/clear":
			w.Header()["Accept-Ch"] = []string{""}
		default:
			fmt.Fprintf(w, "%s|%s|%s", r.Header.Get("Sec-CH-UA-Arch"), r.Header.Get("Sec-CH-UA-Bitness"), r.Header.Get("Sec-CH-UA-Full-Version-List"))
		}
	}))
	defer server.Close()
	client, err := NewClient(identityConfig(t, negotiatedHintsConfig))
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	if _, err := client.Get(server.URL+"/learn", nil); err != nil {
		t.Fatal(err)
	}
	r, err := client.Get(server.URL+"/check", nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(r.Text(), `"arm"||"Google Chrome";v="146.0.1.2"`) {
		t.Errorf("negotiated hints=%q", r.Text())
	}
	if _, err := client.Get(server.URL+"/clear", nil); err != nil {
		t.Fatal(err)
	}
	r, err = client.Get(server.URL+"/check", nil)
	if err != nil || r.Text() != "||" {
		t.Fatalf("clear Accept-CH: %v %v", r, err)
	}
}

func TestClientHintsCriticalRetryIsBounded(t *testing.T) {
	var hits atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Header().Set("Accept-CH", "Sec-CH-UA-Arch")
		w.Header().Set("Critical-CH", "Sec-CH-UA-Arch")
		if r.Header.Get("Sec-CH-UA-Arch") == "" {
			http.SetCookie(w, &http.Cookie{Name: "critical", Value: "seen", Path: "/"})
			fmt.Fprint(w, "initial")
			return
		}
		cookie, err := r.Cookie("critical")
		if err != nil || cookie.Value != "seen" {
			t.Error("retry discarded response cookie")
		}
		fmt.Fprint(w, r.Header.Get("Sec-CH-UA-Arch"))
	}))
	defer server.Close()
	client, err := NewClient(identityConfig(t, negotiatedHintsConfig))
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	r, err := client.Get(server.URL, nil)
	if err != nil || r.Text() != `"arm"` || hits.Load() != 2 {
		t.Fatalf("critical retry response=%v err=%v hits=%d", r, err, hits.Load())
	}
}

func TestAdditionalDesktopClientHintsOnWire(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "%s|%s|%s", r.Header.Get("Sec-CH-UA-WoW64"), r.Header.Get("Sec-CH-UA-Form-Factors"), r.Header.Get("Sec-CH-UA-Full-Version"))
	}))
	defer server.Close()
	client, err := NewClient(identityConfig(t, highEntropyConfig))
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	r, err := client.Get(server.URL, nil)
	if err != nil || r.Text() != `?0|"Desktop"|"146.0.1.2"` {
		t.Fatalf("desktop hints: %v %v", r, err)
	}
}

func TestClientHintsCriticalRespectsResponsePolicy(t *testing.T) {
	var hits atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Header().Set("Accept-CH", "Sec-CH-UA-Arch")
		w.Header().Set("Critical-CH", "Sec-CH-UA-Arch")
		w.Header().Set("Permissions-Policy", "ch-ua-arch=()")
		fmt.Fprint(w, "restricted")
	}))
	defer server.Close()
	client, err := NewClient(identityConfig(t, negotiatedHintsConfig))
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	if _, err := client.Get(server.URL, nil); err != nil {
		t.Fatal(err)
	}
	if hits.Load() != 1 {
		t.Errorf("policy-denied hint triggered %d exchanges", hits.Load())
	}
}

func TestClientHintsCrossOriginInitiatorDoesNotReceiveUndelegatedHints(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Accept-CH", "Sec-CH-UA-Arch")
		fmt.Fprint(w, r.Header.Get("Sec-CH-UA-Arch"))
	}))
	defer server.Close()
	client, err := NewClient(identityConfig(t, negotiatedHintsConfig))
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	if _, err := client.Get(server.URL, nil); err != nil {
		t.Fatal(err)
	}
	r, err := client.Get(server.URL, &Options{BrowserMode: BrowserModeFetch, Headers: map[string]string{"Origin": "https://other.example"}})
	if err != nil || r.Text() != "" {
		t.Fatalf("cross-origin hint leaked: %v %v", r, err)
	}
}

func TestClientHintsOriginIsolationAndRedirect(t *testing.T) {
	for _, h2 := range []bool{false, true} {
		t.Run(fmt.Sprint(h2), func(t *testing.T) {
			other := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, r.Header.Get("Sec-CH-UA-Arch")) }))
			other.EnableHTTP2 = h2
			other.StartTLS()
			defer other.Close()
			server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/redirect" {
					http.Redirect(w, r, other.URL, http.StatusFound)
					return
				}
				w.Header().Set("Accept-CH", "Sec-CH-UA-Arch")
				fmt.Fprint(w, r.Header.Get("Sec-CH-UA-Arch"))
			}))
			server.EnableHTTP2 = h2
			server.StartTLS()
			defer server.Close()
			client, err := NewClient(identityConfig(t, negotiatedHintsConfig))
			if err != nil {
				t.Fatal(err)
			}
			defer client.Close()
			if _, err := client.Get(server.URL, nil); err != nil {
				t.Fatal(err)
			}
			r, err := client.Get(server.URL, nil)
			if err != nil || r.Text() != `"arm"` {
				t.Fatalf("learned hint: %v %v", r, err)
			}
			if (r.Proto == "HTTP/2.0") != h2 {
				t.Fatalf("unexpected protocol %s", r.Proto)
			}
			r, err = client.Get(server.URL+"/redirect", nil)
			if err != nil || r.Text() != "" {
				t.Fatalf("cross-origin redirect leaked: %v %v", r, err)
			}
			if len(client.AcceptedClientHints(other.URL)) != 0 {
				t.Error("origin port isolation failed")
			}
		})
	}
	plain := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Accept-CH", "Sec-CH-UA-Arch")
		w.Header().Set("Critical-CH", "Sec-CH-UA-Arch")
		fmt.Fprint(w, r.Header.Get("Sec-CH-UA-Arch"))
	}))
	defer plain.Close()
	client, err := NewClient(identityConfig(t, negotiatedHintsConfig))
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	for i := 0; i < 2; i++ {
		r, err := client.Get(plain.URL, nil)
		if err != nil || r.Text() != "" {
			t.Fatalf("plain HTTP opted in: %v %v", r, err)
		}
	}
	if len(client.AcceptedClientHints(plain.URL)) != 0 {
		t.Error("plain HTTP preferences cached")
	}
}

func TestClientHintsUnsafeOrSuppressedRequestsNeverRetry(t *testing.T) {
	for _, tc := range []struct {
		name, method string
		body         any
		options      *Options
		disable      bool
		status       int
	}{
		{name: "post", method: "POST", body: "payload"},
		{name: "put", method: "PUT", body: "payload"},
		{name: "head", method: "HEAD"},
		{name: "get with body", method: "GET", body: strings.NewReader("payload")},
		{name: "explicit suppression", method: "GET", options: &Options{Headers: map[string]string{"Sec-CH-UA-Arch": ""}}},
		{name: "browser mode off", method: "GET", options: &Options{BrowserMode: BrowserModeOff}},
		{name: "disabled retry", method: "GET", disable: true},
		{name: "rate limited", method: "GET", status: 429},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var hits atomic.Int32
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				hits.Add(1)
				io.Copy(io.Discard, r.Body)
				w.Header().Set("Accept-CH", "Sec-CH-UA-Arch")
				w.Header().Set("Critical-CH", "Sec-CH-UA-Arch")
				if tc.status != 0 {
					w.WriteHeader(tc.status)
				}
			}))
			defer server.Close()
			config := identityConfig(t, negotiatedHintsConfig)
			config.ClientHints.DisableCriticalRetry = tc.disable
			client, err := NewClient(config)
			if err != nil {
				t.Fatal(err)
			}
			defer client.Close()
			if _, err := client.Do(context.Background(), tc.method, server.URL, tc.body, tc.options); err != nil {
				t.Fatal(err)
			}
			if hits.Load() != 1 {
				t.Errorf("unsafe/suppressed request repeated %d times", hits.Load())
			}
		})
	}
}

func TestClientHintsChangingCriticalListRetriesOnlyOnce(t *testing.T) {
	var hits atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := "Sec-CH-UA-Arch"
		if hits.Add(1) > 1 {
			name = "Sec-CH-UA-Bitness"
		}
		w.Header().Set("Accept-CH", name)
		w.Header().Set("Critical-CH", name)
		fmt.Fprint(w, "done")
	}))
	defer server.Close()
	client, err := NewClient(identityConfig(t, negotiatedHintsConfig))
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	if _, err := client.Get(server.URL, nil); err != nil {
		t.Fatal(err)
	}
	if hits.Load() != 2 {
		t.Errorf("retry limit failed: hits=%d", hits.Load())
	}
}

func TestClientHintsMalformedPreferencePreservesPriorSelection(t *testing.T) {
	for _, raw := range []string{`"Sec-CH-UA-Arch"`, `Sec-CH-UA-Arch,,`, `(Sec-CH-UA-Arch)`, strings.Repeat("A", 9000)} {
		server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/learn" {
				w.Header().Set("Accept-CH", "Sec-CH-UA-Arch")
			} else {
				w.Header().Set("Accept-CH", raw)
			}
			fmt.Fprint(w, r.Header.Get("Sec-CH-UA-Arch"))
		}))
		client, err := NewClient(identityConfig(t, negotiatedHintsConfig))
		if err != nil {
			server.Close()
			t.Fatal(err)
		}
		if _, err := client.Get(server.URL+"/learn", nil); err != nil {
			t.Error(err)
		}
		for i := 0; i < 2; i++ {
			r, err := client.Get(server.URL, nil)
			if err != nil || r.Text() != `"arm"` {
				t.Errorf("malformed opt-in changed preference: %v %v", r, err)
			}
		}
		client.Close()
		server.Close()
	}
}

func TestClientHintsDocumentPolicyDelegationAndIsolation(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Accept-CH", "Sec-CH-UA-Arch, Sec-CH-UA-Bitness")
		fmt.Fprintf(w, "%s|%s|%s", r.Header.Get("Sec-CH-UA-Arch"), r.Header.Get("Sec-CH-UA-Bitness"), r.Header.Get("Sec-CH-UA"))
	}))
	defer server.Close()
	client, err := NewClient(identityConfig(t, negotiatedHintsConfig))
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	if _, err := client.Get(server.URL, nil); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ policy, want string }{
		{`ch-ua-arch=(), ch-ua-bitness=(), ch-ua=()`, "||"},
		{`ch-ua-arch=("` + server.URL + `"), ch-ua-bitness=(), ch-ua=()`, `"arm"||`},
		{`ch-ua-arch=*, ch-ua-bitness=*, ch-ua=()`, `"arm"|"64"|`},
	} {
		headers := http.Header{"Permissions-Policy": {tc.policy}, "Accept-Ch": {"Sec-CH-UA-Arch, Sec-CH-UA-Bitness"}}
		policy, err := NewClientHintContext("https://document.example/page", headers)
		if err != nil {
			t.Fatal(err)
		}
		headers.Set("Permissions-Policy", "ch-ua-arch=*")
		r, err := client.Get(server.URL, &Options{BrowserMode: BrowserModeFetch, ClientHintContext: policy, Headers: map[string]string{"Origin": "https://document.example"}})
		if err != nil || r.Text() != tc.want {
			t.Errorf("document policy: %v %v want %s", r, err, tc.want)
		}
	}
	// A document policy is not persisted as a server-wide setting for other pages.
	r, err := client.Get(server.URL, nil)
	if err != nil || !strings.HasPrefix(r.Text(), `"arm"|"64"|"Google Chrome"`) {
		t.Fatalf("policy leaked across requests: %v %v", r, err)
	}
}

func TestClientHintsDocumentAcceptCHControlsFreshAndWarmedDestinations(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/learn" {
			w.Header().Set("Accept-CH", "Sec-CH-UA-Bitness")
		}
		fmt.Fprintf(w, "%s|%s", r.Header.Get("Sec-CH-UA-Arch"), r.Header.Get("Sec-CH-UA-Bitness"))
	}))
	defer server.Close()
	client, err := NewClient(identityConfig(t, negotiatedHintsConfig))
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	headers := http.Header{
		"Accept-Ch":          {"Sec-CH-UA-Arch"},
		"Permissions-Policy": {"ch-ua-arch=*, ch-ua-bitness=*"},
	}
	policy, err := NewClientHintContext("https://document.example/page", headers)
	if err != nil {
		t.Fatal(err)
	}
	headers.Set("Accept-CH", "Sec-CH-UA-Bitness")
	for _, stage := range []string{"fresh", "warmed"} {
		if stage == "warmed" {
			if _, err := client.Get(server.URL+"/learn", nil); err != nil {
				t.Fatal(err)
			}
		}
		r, err := client.Get(server.URL, &Options{BrowserMode: BrowserModeFetch, ClientHintContext: policy})
		if err != nil {
			t.Fatal(err)
		}
		if r.Text() != `"arm"|` {
			t.Errorf("%s destination: hints=%q; want immutable document selection", stage, r.Text())
		}
	}
}

func TestClientHintsDocumentRequestsDoNotChangeNavigationPreferences(t *testing.T) {
	var hits atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Header().Set("Accept-CH", "Sec-CH-UA-Bitness")
		w.Header().Set("Critical-CH", "Sec-CH-UA-Bitness")
		fmt.Fprint(w, r.Header.Get("Sec-CH-UA-Bitness"))
	}))
	defer server.Close()
	client, err := NewClient(identityConfig(t, negotiatedHintsConfig))
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	policy, err := NewClientHintContext("https://document.example", http.Header{"Permissions-Policy": {"ch-ua-bitness=*"}})
	if err != nil {
		t.Fatal(err)
	}
	r, err := client.Get(server.URL, &Options{BrowserMode: BrowserModeFetch, ClientHintContext: policy})
	if err != nil {
		t.Fatal(err)
	}
	if hits.Load() != 1 || r.Text() != "" || len(client.AcceptedClientHints(server.URL)) != 0 {
		t.Fatalf("subresource altered navigation negotiation: hits=%d body=%q cache=%v", hits.Load(), r.Text(), client.AcceptedClientHints(server.URL))
	}
}

func TestClientHintsClearCookiesDetachesInFlightPreferences(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/pending" {
			close(started)
			select {
			case <-release:
			case <-r.Context().Done():
				return
			}
		}
		if r.URL.Path != "/check" {
			w.Header().Set("Accept-CH", "Sec-CH-UA-Arch")
			http.SetCookie(w, &http.Cookie{Name: "session", Value: "test", Path: "/"})
		}
		fmt.Fprintf(w, "%s|%s", r.Header.Get("Sec-CH-UA-Arch"), r.Header.Get("Cookie"))
	}))
	defer server.Close()
	client, err := NewClient(identityConfig(t, negotiatedHintsConfig))
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	if _, err := client.Get(server.URL+"/learn", nil); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { _, err := client.Get(server.URL+"/pending", nil); done <- err }()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("pending request did not reach server")
	}
	client.ClearCookies()
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if got := client.AcceptedClientHints(server.URL); len(got) != 0 {
		t.Errorf("preferences survived cookie clearing: %v", got)
	}
	r, err := client.Get(server.URL+"/check", nil)
	if err != nil {
		t.Fatal(err)
	}
	if r.Text() != "|" {
		t.Errorf("cleared state leaked on wire: %q", r.Text())
	}
}

func TestClientHintsPolicyRejectsScalarOrigin(t *testing.T) {
	if _, err := NewClientHintContext("https://document.example", http.Header{"Permissions-Policy": {`ch-ua-arch="https://cdn.example"`}}); err == nil {
		t.Fatal("scalar origin was accepted as a policy grant")
	}
}

func TestClientHintsPolicyOriginMatchingAndValidation(t *testing.T) {
	policy, err := NewClientHintContext("https://EXAMPLE.com/page", http.Header{"Permissions-Policy": {`ch-ua-arch=(self "https://*.cdn.example" "https://other.example:8443")`}})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		target  string
		allowed bool
	}{
		{"https://example.com:443/path", true}, {"https://a.cdn.example", true}, {"https://cdn.example", false},
		{"https://a.cdn.example:8443", false}, {"https://other.example:8443", true}, {"https://other.example", false},
	} {
		if got := policy.allows("Sec-Ch-Ua-Arch", tc.target); got != tc.allowed {
			t.Errorf("policy allowed %s=%t", tc.target, got)
		}
	}
	for _, raw := range []string{`ch-ua-arch=?1`, `ch-ua-arch=("https://example.com/path")`, `ch-ua-arch=(src)`, `ch-ua-arch=(`, strings.Repeat("x", 9000)} {
		if _, err := NewClientHintContext("https://example.com", http.Header{"Permissions-Policy": {raw}}); err == nil {
			t.Errorf("invalid policy accepted: %.60s", raw)
		}
	}
	if _, err := NewClientHintContext("http://example.com", nil); err == nil {
		t.Error("insecure document context accepted")
	}
}

func TestClientHintsCacheBoundsExpiryAndClear(t *testing.T) {
	config := identityConfig(t, negotiatedHintsConfig)
	config.ClientHints.MaxOrigins = 1
	config.ClientHints.CacheTTL = 30 * time.Millisecond
	client, err := NewClient(config)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	headers := fhttp.Header{"Accept-Ch": {"Sec-CH-UA-Arch"}}
	client.clientHints.observe("https://one.example", headers)
	client.clientHints.observe("https://two.example", headers)
	if len(client.AcceptedClientHints("https://one.example")) != 0 || len(client.AcceptedClientHints("https://two.example")) != 1 {
		t.Error("origin cache capacity failed")
	}
	time.Sleep(35 * time.Millisecond)
	if len(client.AcceptedClientHints("https://two.example")) != 0 {
		t.Error("expired preference retained")
	}
	old := client.clientHints
	client.ClearClientHints()
	old.observe("https://two.example", headers)
	if len(client.AcceptedClientHints("https://two.example")) != 0 {
		t.Error("old observation repopulated cleared cache")
	}
}

func TestClientHintsRetryUsesHeaderTimeout(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Sec-CH-UA-Arch") != "" {
			<-r.Context().Done()
			return
		}
		w.Header().Set("Accept-CH", "Sec-CH-UA-Arch")
		w.Header().Set("Critical-CH", "Sec-CH-UA-Arch")
	}))
	defer server.Close()
	client, err := NewClient(identityConfig(t, negotiatedHintsConfig))
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	if _, err := client.Get(server.URL, &Options{HeaderTimeout: 75 * time.Millisecond}); !errors.Is(err, ErrHeaderTimeout) {
		t.Fatalf("critical retry header timeout=%v", err)
	}
}

func TestClientHintsRejectsInvalidConfiguration(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(*Config)
	}{
		{"negative ttl", func(c *Config) { c.ClientHints.CacheTTL = -time.Second }},
		{"negative capacity", func(c *Config) { c.ClientHints.MaxOrigins = -1 }},
		{"eager conflict", func(c *Config) { c.HighEntropyClientHints = true }},
		{"missing identity", func(c *Config) { c.BrowserIdentity = nil }},
		{"missing full version", func(c *Config) { c.BrowserIdentity.FullVersion = "" }},
		{"firefox", func(c *Config) { c.Profile = "firefox_148" }},
		{"safari", func(c *Config) { c.Profile = "safari_16_0" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			config := identityConfig(t, negotiatedHintsConfig)
			tc.edit(&config)
			client := NewWithConfig(config)
			defer client.Close()
			if client.ConfigError() == nil {
				t.Fatal("invalid configuration accepted")
			}
			client.ClearProxy()
			if client.ConfigError() == nil {
				t.Error("clearing proxy erased identity/cache validation failure")
			}
		})
	}
}

func TestClientHintsCriticalRetryClosesUnfinishedBody(t *testing.T) {
	for _, h2 := range []bool{false, true} {
		t.Run(fmt.Sprint(h2), func(t *testing.T) {
			server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Sec-CH-UA-Arch") == "" {
					w.Header().Set("Accept-CH", "Sec-CH-UA-Arch")
					w.Header().Set("Critical-CH", "Sec-CH-UA-Arch")
					w.(http.Flusher).Flush()
					<-r.Context().Done()
					return
				}
				fmt.Fprint(w, "retried stream")
			}))
			server.EnableHTTP2 = h2
			server.StartTLS()
			defer server.Close()
			client, err := NewClient(identityConfig(t, negotiatedHintsConfig))
			if err != nil {
				t.Fatal(err)
			}
			defer client.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			r, err := client.DoStream(ctx, http.MethodGet, server.URL, nil, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer r.Body.Close()
			data, err := io.ReadAll(r.Body)
			if err != nil || string(data) != "retried stream" || (r.Proto == "HTTP/2.0") != h2 {
				t.Fatalf("stream retry protocol=%s body=%q err=%v", r.Proto, data, err)
			}
		})
	}
}

func TestClientHintsCriticalRetrySharesCancellationAndTotalDeadline(t *testing.T) {
	for _, parentCancel := range []bool{false, true} {
		t.Run(fmt.Sprint(parentCancel), func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var hits atomic.Int32
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				hits.Add(1)
				if r.Header.Get("Sec-CH-UA-Arch") == "" {
					w.Header().Set("Accept-CH", "Sec-CH-UA-Arch")
					w.Header().Set("Critical-CH", "Sec-CH-UA-Arch")
					return
				}
				if parentCancel {
					cancel()
				}
				<-r.Context().Done()
			}))
			defer server.Close()
			config := identityConfig(t, negotiatedHintsConfig)
			config.Timeout = 250 * time.Millisecond
			client, err := NewClient(config)
			if err != nil {
				t.Fatal(err)
			}
			defer client.Close()
			_, err = client.GetContext(ctx, server.URL, nil)
			want := context.DeadlineExceeded
			if parentCancel {
				want = context.Canceled
			}
			if !errors.Is(err, want) || hits.Load() != 2 {
				t.Fatalf("retry cancellation: hits=%d err=%v want=%v", hits.Load(), err, want)
			}
		})
	}
}

func TestClientHintsConcurrentObservationAndClearing(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Accept-CH", "Sec-CH-UA-Arch")
		fmt.Fprint(w, r.Header.Get("Sec-CH-UA-Arch"))
	}))
	defer server.Close()
	client, err := NewClient(identityConfig(t, negotiatedHintsConfig))
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	var workers sync.WaitGroup
	for i := 0; i < 4; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for j := 0; j < 5; j++ {
				r, err := client.Get(server.URL, nil)
				if err != nil {
					t.Error(err)
					return
				}
				if r.Text() != "" && r.Text() != `"arm"` {
					t.Errorf("inconsistent generated hint: %q", r.Text())
				}
				client.AcceptedClientHints(server.URL)
				client.ClearClientHints()
				client.ClearCookies()
			}
		}()
	}
	workers.Wait()
}

func TestClientHintsDocumentRedirectRechecksDelegation(t *testing.T) {
	target := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Accept-CH", "Sec-CH-UA-Arch")
		fmt.Fprint(w, r.Header.Get("Sec-CH-UA-Arch"))
	}))
	defer target.Close()
	firstHints := make(chan string, 1)
	start := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		firstHints <- r.Header.Get("Sec-CH-UA-Arch")
		http.Redirect(w, r, target.URL, http.StatusFound)
	}))
	defer start.Close()
	client, err := NewClient(identityConfig(t, negotiatedHintsConfig))
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	if _, err := client.Get(target.URL, nil); err != nil {
		t.Fatal(err)
	}
	policy, err := NewClientHintContext("https://document.example", http.Header{
		"Accept-Ch": {"Sec-CH-UA-Arch"}, "Permissions-Policy": {`ch-ua-arch=("` + start.URL + `")`},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, first, last string
		headers           map[string]string
	}{
		{name: "generated", first: `"arm"`, last: ""},
		{name: "suppressed", first: "", last: "", headers: map[string]string{"Sec-CH-UA-Arch": ""}},
		{name: "explicit", first: `"arm"`, last: `"arm"`, headers: map[string]string{"Sec-CH-UA-Arch": `"arm"`}},
	} {
		r, err := client.Get(start.URL, &Options{BrowserMode: BrowserModeFetch, ClientHintContext: policy, Headers: tc.headers})
		if err != nil {
			t.Fatal(err)
		}
		firstHint := <-firstHints
		if firstHint != tc.first || r.Text() != tc.last {
			t.Errorf("%s redirect hints=%q -> %q, want %q -> %q", tc.name, firstHint, r.Text(), tc.first, tc.last)
		}
	}
}
