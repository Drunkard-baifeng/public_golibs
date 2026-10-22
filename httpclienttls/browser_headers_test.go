package httpclienttls

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	fhttp "github.com/bogdanfinn/fhttp"
	tlsclient "github.com/bogdanfinn/tls-client"
)

func TestBrowserModeOffNormalization(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/start" {
			http.Redirect(w, r, "/end", http.StatusFound)
			return
		}
		fmt.Fprintf(w, "%s|%s", r.UserAgent(), r.Header.Get("Sec-Fetch-Site"))
	}))
	defer s.Close()
	r, err := streamClient(t).Get(s.URL+"/start", &Options{BrowserMode: " OFF "})
	if err != nil {
		t.Fatal(err)
	}
	if r.Text() != "|" || len(r.Request.Header.Values(fhttp.HeaderOrderKey)) != 0 {
		t.Fatalf("off mode added browser defaults: body=%q order=%v", r.Text(), r.Request.Header.Values(fhttp.HeaderOrderKey))
	}
}

func TestExplicitEmptyHeadersSuppressBrowserDefaults(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/start" {
			http.Redirect(w, r, "/end", http.StatusFound)
			return
		}
		fmt.Fprintf(w, "%s|%s|%s", r.UserAgent(), r.Header.Get("Accept-Language"), r.Header.Get("Sec-CH-UA"))
	}))
	defer s.Close()
	for _, opts := range []*Options{
		{Headers: map[string]string{"user-agent": "", "ACCEPT-LANGUAGE": "", "sEc-CH-uA": ""}},
		{HeaderValues: http.Header{"user-agent": nil, "ACCEPT-LANGUAGE": nil, "sEc-CH-uA": nil}},
	} {
		opts.BrowserMode = BrowserModeNavigation
		r, err := streamClient(t).Get(s.URL+"/start", opts)
		if err != nil {
			t.Fatal(err)
		}
		if r.Text() != "||" {
			t.Fatalf("explicit empty headers replaced with defaults: %q", r.Text())
		}
	}
}

func TestHeaderOrderOverrideSurvivesRedirect(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/start" {
			http.Redirect(w, r, "/end", http.StatusFound)
			return
		}
		io.WriteString(w, "ok")
	}))
	defer s.Close()
	for _, tc := range []struct {
		name string
		opts *Options
		want []string
	}{
		{"explicit-empty", &Options{HeaderOrder: []string{}}, nil},
		{"multi-value", &Options{HeaderValues: http.Header{fhttp.HeaderOrderKey: {"x-check", "user-agent"}}}, []string{"x-check", "user-agent"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, err := streamClient(t).Get(s.URL+"/start", tc.opts)
			if err != nil {
				t.Fatal(err)
			}
			got := r.Request.Header.Values(fhttp.HeaderOrderKey)
			if len(got) != len(tc.want) || len(got) > 0 && !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("redirect changed header order: got=%v want=%v", got, tc.want)
			}
		})
	}
}

func TestFetchSiteNormalizesDefaultPorts(t *testing.T) {
	for _, tc := range []struct{ source, target, want string }{
		{"https://example.com:443/page", "https://example.com/api", "same-origin"},
		{"http://example.com/page", "http://example.com:80/api", "same-origin"},
		{"https://example.com:444/page", "https://example.com/api", "same-site"},
		{"https://example.com/page", "http://example.com/api", "cross-site"},
	} {
		if got := fetchSite(tc.source, tc.target, false); got != tc.want {
			t.Errorf("fetchSite(%q, %q)=%q want %q", tc.source, tc.target, got, tc.want)
		}
	}
}

func TestRedirectFetchSitePreservesInitiatorAndChain(t *testing.T) {
	var base string
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/start":
			http.Redirect(w, r, strings.Replace(base, "127.0.0.1", "localhost", 1)+"/hop", http.StatusFound)
		case "/hop":
			http.Redirect(w, r, base+"/return", http.StatusFound)
		case "/return":
			http.Redirect(w, r, "/end", http.StatusFound)
		default:
			io.WriteString(w, r.Header.Get("Sec-Fetch-Site"))
		}
	}))
	defer s.Close()
	base = s.URL
	for _, tc := range []struct {
		name         string
		mode         BrowserMode
		source, want string
	}{
		{"direct-navigation", BrowserModeNavigation, "", "none"},
		{"navigation", BrowserModeNavigation, s.URL + "/page", "cross-site"},
		{"fetch", BrowserModeFetch, s.URL + "/page", "cross-site"},
		{"script", BrowserModeScript, s.URL + "/page", "cross-site"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, err := streamClient(t).Get(s.URL+"/start", &Options{BrowserMode: tc.mode, Headers: map[string]string{"Referer": tc.source}})
			if err != nil {
				t.Fatal(err)
			}
			if r.Text() != tc.want {
				t.Fatalf("redirect lost initiator/chain: got=%q want=%q", r.Text(), tc.want)
			}
		})
	}
}

func TestRedirectClientHintsFollowTargetTrust(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/upgrade" {
			http.Redirect(w, r, "https://secure.example/end", http.StatusFound)
			return
		}
		if r.URL.Path == "/downgrade" {
			http.Redirect(w, r, "http://plain.example/end", http.StatusFound)
			return
		}
		fmt.Fprintf(w, "%s|%s|%s", r.Header.Get("Sec-CH-UA"), r.Header.Get("Sec-CH-UA-Mobile"), r.Header.Get("Sec-CH-UA-Platform"))
	})
	plain := httptest.NewServer(handler)
	defer plain.Close()
	secure := httptest.NewTLSServer(handler)
	defer secure.Close()
	c := streamClient(t)
	// Real transports with local routing let public hostnames exercise the
	// trust boundary without DNS lookups or requests to external services.
	routes := map[string]string{"plain.example:80": plain.Listener.Addr().String(), "secure.example:443": secure.Listener.Addr().String()}
	inner, err := tlsclient.NewHttpClient(tlsclient.NewNoopLogger(),
		tlsclient.WithClientProfile(c.profile.client), tlsclient.WithNotFollowRedirects(),
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
	c.inner.CloseIdleConnections()
	c.inner = inner
	for _, tc := range []struct{ target, want string }{
		{"http://plain.example/upgrade", c.Profile().SecCHUA + `|?0|"Windows"`},
		{"https://secure.example/downgrade", "||"},
	} {
		r, err := c.Get(tc.target, nil)
		if err != nil {
			t.Fatal(err)
		}
		if r.Text() != tc.want {
			t.Errorf("redirect client hints: got=%q want=%q", r.Text(), tc.want)
		}
	}
}

func TestBrowserMetadataRequiresTrustworthyTarget(t *testing.T) {
	profile := streamClient(t).Profile()
	for _, tc := range []struct {
		target string
		want   bool
	}{
		{"http://example.com", false},
		{"https://example.com", true},
		{"http://127.0.0.1", true},
		{"http://app.localhost", true},
	} {
		headers := make(fhttp.Header)
		if err := applyBrowserHeaders(headers, profile, BrowserModeNavigation, tc.target); err != nil {
			t.Fatal(err)
		}
		for _, key := range []string{"Sec-CH-UA", "Sec-Fetch-Mode", "Sec-Fetch-Site", "Sec-Fetch-Dest", "Sec-Fetch-User"} {
			if (headers.Get(key) != "") != tc.want {
				t.Errorf("%s: %s=%q want present=%t", tc.target, key, headers.Get(key), tc.want)
			}
		}
	}
}
