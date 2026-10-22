package httpclienttls

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestInvalidProxySurvivesTLSReconfiguration(t *testing.T) {
	var hits atomic.Int32
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		io.WriteString(w, "direct")
	}))
	defer s.Close()
	for _, mode := range []string{"construction", "setter"} {
		t.Run(mode, func(t *testing.T) {
			c := NewWithConfig(Config{Profile: "chrome_146", Proxy: "http://proxy.invalid:bad"})
			if mode == "setter" {
				c.Close()
				c = NewWithConfig(Config{Profile: "chrome_146"})
				if err := c.ConfigureProxy("http://proxy.invalid:bad", ""); err == nil {
					t.Fatal("invalid proxy accepted")
				}
			}
			defer c.Close()
			if c.ConfigError() == nil {
				t.Fatal("missing proxy configuration error")
			}
			c.SetVerify(false)
			if _, err := c.Get(s.URL, nil); err == nil {
				t.Error("TLS reconfiguration silently switched invalid proxy configuration to a direct connection")
			}
			// Explicitly clearing the proxy is the caller's choice to use direct requests.
			c.ClearProxy()
			r, err := c.Get(s.URL, nil)
			if err != nil || r.Text() != "direct" {
				t.Fatalf("explicit proxy recovery failed: response=%v err=%v", r, err)
			}
		})
	}
	if got := hits.Load(); got != 2 {
		t.Fatalf("origin received %d requests; want only the two explicitly enabled direct requests", got)
	}
}

func TestClearCookiesDiscardsInflightObservations(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/old" {
			close(started)
			<-release
			http.SetCookie(w, &http.Cookie{Name: "session", Value: "old", Path: "/"})
			return
		}
		http.SetCookie(w, &http.Cookie{Name: "session", Value: "new", Path: "/"})
	}))
	defer s.Close()
	c := streamClient(t)
	done := make(chan error, 1)
	go func() {
		_, err := c.Get(s.URL+"/old", nil)
		done <- err
	}()
	<-started
	c.ClearCookies()
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if got := c.GetCookies()["session"]; got != "" {
		t.Errorf("cleared cookies repopulated by old response: %q", got)
	}
	if got := c.CookieValue(s.URL, "session"); got != "" {
		t.Errorf("cleared jar repopulated by old response: %q", got)
	}
	if _, err := c.Get(s.URL+"/new", nil); err != nil {
		t.Fatal(err)
	}
	if c.GetCookies()["session"] != "new" || c.CookieValue(s.URL, "session") != "new" {
		t.Fatal("new responses must still update the cleared cookie store")
	}
}

func TestProxyConfigurationErrorsRedactCredentials(t *testing.T) {
	for _, value := range []string{
		"http://test-user:test-secret@127.0.0.1:bad",
		"http://test-user:test-secret@127.0.0.1:8080/%zz",
	} {
		c := NewWithConfig(Config{Profile: "chrome_146", Proxy: value})
		defer c.Close()
		if c.ConfigError() == nil {
			t.Fatal("expected invalid proxy error")
		}
		_, err := c.Get("http://origin.invalid", nil)
		for _, e := range []error{c.ConfigError(), err} {
			if msg := fmt.Sprint(e); strings.Contains(msg, "test-user") || strings.Contains(msg, "test-secret") {
				t.Errorf("proxy credentials appeared in error: %s", msg)
			}
		}
	}
}
