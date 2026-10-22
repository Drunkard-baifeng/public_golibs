package httpclienttls

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRequestBodyClosedOnSetupError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "upload.txt")
	if err := os.WriteFile(path, []byte("payload"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"configuration", "closed", "url", "method", "browser-mode", "content-length"} {
		t.Run(kind, func(t *testing.T) {
			c := streamClient(t)
			method, target := http.MethodPost, "http://fixture.invalid/upload"
			opts := &Options{}
			switch kind {
			case "configuration":
				c = NewWithConfig(Config{Profile: "missing-profile"})
				t.Cleanup(c.Close)
			case "closed":
				c.Close()
			case "url":
				target = "://invalid"
			case "method":
				method = "bad method"
			case "browser-mode":
				opts.BrowserMode = "invalid"
			case "content-length":
				length := int64(-2)
				opts.ContentLength = &length
			}
			body, err := os.Open(path)
			if err != nil {
				t.Fatal(err)
			}
			defer body.Close()
			if _, err := c.DoStream(context.Background(), method, target, body, opts); err == nil {
				t.Fatal("expected request setup error")
			}
			if _, err := body.Read(make([]byte, 1)); !errors.Is(err, os.ErrClosed) {
				t.Fatalf("upload file remains open after setup failed: %v", err)
			}
		})
	}
}

func TestRedirectDropsBodyFor301302303(t *testing.T) {
	for _, status := range []int{301, 302, 303} {
		for _, method := range []string{http.MethodGet, http.MethodPost} {
			for _, replayable := range []bool{false, true} {
				t.Run(fmt.Sprintf("%d/%s/replay=%t", status, method, replayable), func(t *testing.T) {
					s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						body, err := io.ReadAll(r.Body)
						if err != nil {
							t.Error(err)
							return
						}
						if r.URL.Path == "/" {
							http.Redirect(w, r, "/end", status)
							return
						}
						fmt.Fprintf(w, "%s|%s|%s", r.Method, body, r.Header.Get("Content-Type"))
					}))
					defer s.Close()
					var body any = "payload"
					if !replayable {
						body = io.LimitReader(strings.NewReader("payload"), 7)
					}
					r, err := streamClient(t).Do(context.Background(), method, s.URL, body, &Options{Headers: map[string]string{"Content-Type": "text/plain"}})
					if err != nil {
						t.Fatal(err)
					}
					if r.StatusCode != http.StatusOK || r.Text() != "GET||" {
						t.Fatalf("redirect retained upload: status=%d body=%q", r.StatusCode, r.Text())
					}
				})
			}
		}
	}
}

func TestCookieJarRejectsPublicSuffixCookies(t *testing.T) {
	c := streamClient(t)
	for _, reset := range []bool{false, true} {
		if reset {
			c.ClearCookies()
		}
		if err := c.SetCookie("https://tenant-a.co.uk", "session", "injected", "co.uk", "/", true); err != nil {
			t.Fatal(err)
		}
		if got := c.CookieValue("https://tenant-b.co.uk", "session"); got != "" {
			t.Errorf("public-suffix cookie reached a different tenant (reset=%t): %q", reset, got)
		}
		if err := c.SetCookie("https://a.example.co.uk", "shared", "ok", "example.co.uk", "/", true); err != nil {
			t.Fatal(err)
		}
		if got := c.CookieValue("https://b.example.co.uk", "shared"); got != "ok" {
			t.Errorf("valid same-domain cookie missing: %q", got)
		}
	}
}

func TestBodyLimitMaxInt64DoesNotDiscardResponse(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "payload")
	}))
	defer s.Close()
	c, err := NewClient(Config{Profile: "chrome_146", MaxResponseBodyBytes: math.MaxInt64})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	r, err := c.Get(s.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	if r.Text() != "payload" {
		t.Fatalf("response discarded at MaxInt64 limit: %q", r.Text())
	}
}

func TestPostHelpersKeepContentTypeConsistent(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		io.WriteString(w, r.Header.Get("Content-Type"))
	}))
	defer s.Close()
	c := streamClient(t)
	for _, key := range []string{"Content-Type", "content-type"} {
		for _, kind := range []string{"json", "form", "multipart"} {
			t.Run(kind+"/"+key, func(t *testing.T) {
				opts := &Options{HeaderValues: http.Header{key: {"text/plain"}}}
				var r *Response
				var err error
				var want string
				switch kind {
				case "json":
					r, err = c.PostJSON(s.URL, map[string]string{"a": "b"}, opts)
					want = "application/json"
				case "form":
					r, err = c.PostForm(s.URL, map[string]string{"a": "b"}, opts)
					want = "application/x-www-form-urlencoded"
				case "multipart":
					r, err = c.PostMultipart(s.URL, map[string]string{"a": "b"}, nil, opts)
					want = "multipart/form-data; boundary="
				}
				if err != nil {
					t.Fatal(err)
				}
				if !strings.HasPrefix(r.Text(), want) {
					t.Fatalf("body and Content-Type mismatch: got %q want %q", r.Text(), want)
				}
				if opts.HeaderValues[key][0] != "text/plain" {
					t.Fatal("caller's headers mutated")
				}
			})
		}
	}
}
