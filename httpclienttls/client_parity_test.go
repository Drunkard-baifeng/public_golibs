package httpclienttls

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
)

func TestNavigationUserActivationAcrossRedirect(t *testing.T) {
	for _, h2 := range []bool{false, true} {
		t.Run(fmt.Sprint(h2), func(t *testing.T) {
			seen := make(chan string, 2)
			server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				seen <- r.Header.Get("Sec-Fetch-User")
				if r.URL.Path == "/start" {
					http.Redirect(w, r, "/end", http.StatusFound)
				}
			}))
			server.EnableHTTP2 = h2
			server.StartTLS()
			defer server.Close()
			client := streamClient(t)
			for _, tc := range []struct{ name, config, want string }{
				{"legacy default", `{"BrowserMode":"navigation"}`, "?1"},
				{"programmatic navigation", `{"BrowserMode":"navigation","UserActivated":false}`, ""},
				{"user navigation", `{"BrowserMode":"navigation","UserActivated":true}`, "?1"},
				{"manual header wins", `{"BrowserMode":"navigation","UserActivated":false,"Headers":{"sec-fetch-user":"?1"}}`, "?1"},
				{"manual suppression wins", `{"BrowserMode":"navigation","UserActivated":true,"Headers":{"sec-fetch-user":""}}`, ""},
				{"fetch unaffected", `{"BrowserMode":"fetch","UserActivated":true}`, ""},
				{"off unaffected", `{"BrowserMode":"off","UserActivated":true}`, ""},
			} {
				t.Run(tc.name, func(t *testing.T) {
					var options Options
					if err := json.Unmarshal([]byte(tc.config), &options); err != nil {
						t.Fatal(err)
					}
					r, err := client.Get(server.URL+"/start", &options)
					if err != nil {
						t.Fatal(err)
					}
					if (r.Proto == "HTTP/2.0") != h2 {
						t.Fatalf("unexpected protocol %s", r.Proto)
					}
					for i := 0; i < 2; i++ {
						if got := <-seen; got != tc.want {
							t.Errorf("hop %d user activation=%q want=%q", i, got, tc.want)
						}
					}
				})
			}
		})
	}
}

func TestScopedCookieSnapshotsMatchURLAndPreserveDuplicateNames(t *testing.T) {
	client := streamClient(t)
	api, ok := any(client).(interface {
		CookiesForURL(string) ([]*http.Cookie, error)
		CookieHeader(string) (string, error)
	})
	if !ok {
		t.Fatal("client lacks scoped cookie snapshot/export API")
	}
	for _, cookie := range []struct {
		name, value, path string
		secure            bool
	}{
		{"session", "root", "/", false},
		{"session", "account", "/account", false},
		{"secure", "secret", "/", true},
		{"spaced", "a b", "/", false},
	} {
		if err := client.SetCookie("https://example.com", cookie.name, cookie.value, "", cookie.path, cookie.secure); err != nil {
			t.Fatal(err)
		}
	}
	// These unscoped request overrides are intentionally not jar cookies.
	client.AddCookie("manual", "override")
	for _, tc := range []struct {
		target string
		values []string
	}{
		{"https://example.com/account/page", []string{"session=account", "session=root", "secure=secret", "spaced=a b"}},
		{"http://example.com/account/page", []string{"session=account", "session=root", "spaced=a b"}},
		{"https://example.com/elsewhere", []string{"session=root", "secure=secret", "spaced=a b"}},
		{"https://other.example/", nil},
		{"https://sub.example.com/", nil},
	} {
		cookies, err := api.CookiesForURL(tc.target)
		if err != nil {
			t.Fatal(err)
		}
		var got []string
		for _, cookie := range cookies {
			got = append(got, cookie.Name+"="+cookie.Value)
		}
		if !reflect.DeepEqual(got, tc.values) {
			t.Errorf("%s cookies=%v want=%v", tc.target, got, tc.values)
		}
		header, err := api.CookieHeader(tc.target)
		if err != nil {
			t.Fatal(err)
		}
		request := &http.Request{Header: http.Header{"Cookie": {header}}}
		got = nil
		for _, cookie := range request.Cookies() {
			got = append(got, cookie.Name+"="+cookie.Value)
		}
		if !reflect.DeepEqual(got, tc.values) {
			t.Errorf("exported cookie header changed values/order: %q", header)
		}
		if len(cookies) > 0 {
			cookies[0].Value = "mutated"
		}
	}
	if client.CookieValue("https://example.com/account/page", "session") != "account" {
		t.Error("snapshot mutation changed jar")
	}
	for _, target := range []string{"relative/path", "ftp://example.com", "https://user:secret@example.com/%zz", "https://example.com:70000"} {
		if _, err := api.CookieHeader(target); err == nil || strings.Contains(err.Error(), "secret") {
			t.Errorf("invalid URL validation/redaction: %v", err)
		}
	}
	client.ClearCookies()
	if header, err := api.CookieHeader("https://example.com/account"); err != nil || header != "" {
		t.Fatalf("clear left scoped cookies: %q %v", header, err)
	}
}

func TestCooldownBlocksLaterRequestsWithoutRetryingResponse(t *testing.T) {
	var hits atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Header().Set("Retry-After", "60")
		http.SetCookie(w, &http.Cookie{Name: "limited", Value: "seen", Path: "/"})
		w.WriteHeader(http.StatusTooManyRequests)
		fmt.Fprint(w, "original response")
	}))
	defer server.Close()
	client, err := NewClient(identityConfig(t, `{"Profile":"chrome_146","Verify":false,"Cooldown":{}}`))
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	r, err := client.DoStream(context.Background(), http.MethodGet, server.URL, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	data, readErr := io.ReadAll(r.Body)
	r.Body.Close()
	if readErr != nil || r.StatusCode != 429 || string(data) != "original response" || hits.Load() != 1 {
		t.Fatalf("initial rate-limit response altered: %v body=%q hits=%d", readErr, data, hits.Load())
	}
	if client.CookieValue(server.URL, "limited") != "seen" {
		t.Error("rate-limit cookie was discarded")
	}
	if _, err := client.Get(server.URL+"/later", nil); err == nil {
		t.Error("request was dispatched during server-directed cooldown")
	}
	if hits.Load() != 1 {
		t.Errorf("cooldown leaked requests: hits=%d", hits.Load())
	}
}

func TestScopedCookieInputValidationAndErrorRedaction(t *testing.T) {
	client := streamClient(t)
	for _, target := range []string{
		"ftp://example.com", "https://example.com:70000",
		"https://user:test-secret@example.com",
		"https://user:test-secret@example.com/%zz?token=private",
	} {
		err := client.SetCookie(target, "session", "value", "", "/", false)
		if err == nil {
			t.Errorf("invalid cookie target accepted: %q", target)
		} else if strings.Contains(err.Error(), "test-secret") || strings.Contains(err.Error(), "private") {
			t.Errorf("cookie URL error contains credentials/query: %v", err)
		}
	}
	if client.CookieValue("https://example.com", "session") != "" {
		t.Error("invalid target altered the cookie jar")
	}
}
