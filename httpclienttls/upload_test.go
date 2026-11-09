package httpclienttls

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type observedUpload struct {
	io.ReadCloser
	started chan struct{}
	closed  chan struct{}
	once    sync.Once
	closes  atomic.Int32
}

func (b *observedUpload) Read(p []byte) (int, error) {
	b.once.Do(func() { close(b.started) })
	return b.ReadCloser.Read(p)
}

func (b *observedUpload) Close() error {
	if b.closes.Add(1) == 1 && b.closed != nil {
		defer close(b.closed)
	}
	return b.ReadCloser.Close()
}

func TestUploadCloseIdempotent(t *testing.T) {
	for _, concurrent := range []bool{false, true} {
		t.Run(fmt.Sprintf("concurrent=%t", concurrent), func(t *testing.T) {
			raw := &observedUpload{ReadCloser: io.NopCloser(strings.NewReader("body"))}
			body := newUploadBody(context.Background(), raw, 4)
			done := make(chan struct{})
			go func() {
				defer close(done)
				if !concurrent {
					body.Close()
					body.Close()
					return
				}
				var wg sync.WaitGroup
				for i := 0; i < 16; i++ {
					wg.Add(1)
					go func() {
						defer wg.Done()
						body.Close()
					}()
				}
				wg.Wait()
			}()
			select {
			case <-done:
			case <-time.After(time.Second):
				t.Fatal("repeated upload Close blocked after stopping the cancellation callback")
			}
			if got := raw.closes.Load(); got != 1 {
				t.Fatalf("underlying body closed %d times, want exactly once", got)
			}
		})
	}
}

func TestResponseCloseCancelsBlockedUpload(t *testing.T) {
	for _, h2 := range []bool{false, true} {
		t.Run(fmt.Sprint("http2=", h2), func(t *testing.T) {
			s := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.ProtoMajor == 1 {
					if err := http.NewResponseController(w).EnableFullDuplex(); err != nil {
						t.Error(err)
						return
					}
				}
				w.WriteHeader(http.StatusOK)
				w.(http.Flusher).Flush()
				io.Copy(io.Discard, r.Body)
			}))
			s.EnableHTTP2 = h2
			s.StartTLS()
			defer s.Close()
			reader, writer := io.Pipe()
			defer reader.Close()
			defer writer.Close()
			body := &observedUpload{ReadCloser: reader, started: make(chan struct{}), closed: make(chan struct{})}
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			r, err := streamClient(t).DoStream(ctx, http.MethodPost, s.URL, body, &Options{DisableTimeout: true})
			if err != nil {
				t.Fatal(err)
			}
			r.Body.Close()
			select {
			case <-body.closed:
			case <-time.After(time.Second):
				reader.Close()
				t.Fatal("response Close left the full-duplex upload blocked")
			}
		})
	}
}

func TestRedirectClosesDiscardedUploadBeforeFinalResponse(t *testing.T) {
	for _, h2 := range []bool{false, true} {
		t.Run(fmt.Sprint("http2=", h2), func(t *testing.T) {
			reader, writer := io.Pipe()
			defer reader.Close()
			defer writer.Close()
			body := &observedUpload{ReadCloser: reader, started: make(chan struct{}), closed: make(chan struct{})}
			s := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/start" {
					if r.ProtoMajor == 1 {
						if err := http.NewResponseController(w).EnableFullDuplex(); err != nil {
							t.Error(err)
							return
						}
					}
					select {
					case <-body.started:
					case <-r.Context().Done():
						return
					}
					w.Header().Set("Location", "/end")
					w.WriteHeader(http.StatusFound)
					w.(http.Flusher).Flush()
					return
				}
				w.WriteHeader(http.StatusOK)
				w.(http.Flusher).Flush()
				<-r.Context().Done()
			}))
			s.EnableHTTP2 = h2
			s.StartTLS()
			defer s.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			r, err := streamClient(t).DoStream(ctx, http.MethodPost, s.URL+"/start", body, &Options{DisableTimeout: true})
			if err != nil {
				t.Fatal(err)
			}
			defer r.Body.Close()
			if r.StatusCode != http.StatusOK {
				t.Fatalf("redirect failed: status=%d", r.StatusCode)
			}
			select {
			case <-body.closed:
			case <-time.After(500 * time.Millisecond):
				t.Fatal("discarded upload is still open while final response is streaming")
			}
		})
	}
}

func TestBlockedUploadRespectsCancellation(t *testing.T) {
	for _, h2 := range []bool{false, true} {
		for _, mode := range []string{"header", "total", "parent"} {
			t.Run(fmt.Sprintf("http2=%t/%s", h2, mode), func(t *testing.T) {
				s := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					io.Copy(io.Discard, r.Body)
				}))
				s.EnableHTTP2 = h2
				s.StartTLS()
				defer s.Close()
				reader, writer := io.Pipe()
				defer reader.Close()
				defer writer.Close()
				body := &observedUpload{ReadCloser: reader, started: make(chan struct{})}
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				opts := &Options{DisableTimeout: true}
				want := error(context.Canceled)
				switch mode {
				case "header":
					opts.HeaderTimeout = 150 * time.Millisecond
					want = ErrHeaderTimeout
				case "total":
					opts.DisableTimeout = false
					opts.Timeout = 150 * time.Millisecond
					want = context.DeadlineExceeded
				}
				c := streamClient(t)
				done := make(chan error, 1)
				go func() {
					r, err := c.DoStream(ctx, http.MethodPost, s.URL, body, opts)
					if r != nil {
						r.Body.Close()
					}
					done <- err
				}()
				select {
				case <-body.started:
				case <-time.After(time.Second):
					cancel()
					reader.Close()
					<-done
					t.Fatal("upload did not begin reading")
				}
				if mode == "parent" {
					cancel()
				}
				select {
				case err := <-done:
					if !errors.Is(err, want) {
						t.Fatalf("error=%v want %v", err, want)
					}
				case <-time.After(time.Second):
					cancel()
					reader.Close()
					<-done
					t.Fatal("blocked upload ignored cancellation until manually closed")
				}
				if got := body.closes.Load(); got != 1 {
					t.Fatalf("upload closed %d times, want exactly once", got)
				}
			})
		}
	}
}

func TestHTTP2EarlyResponseStopsBlockedUpload(t *testing.T) {
	for _, status := range []int{302, 307, 308, 403, 413, 500} {
		for _, mode := range []string{"unknown-length", "known-length", "eof-probe"} {
			t.Run(fmt.Sprintf("status=%d/%s", status, mode), func(t *testing.T) {
				reader, writer := io.Pipe()
				defer writer.Close()
				defer reader.Close()
				body := &observedUpload{ReadCloser: reader, started: make(chan struct{}), closed: make(chan struct{})}
				release := make(chan struct{})
				s := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.ProtoMajor != 2 {
						t.Error("request did not use HTTP/2")
					}
					w.Header().Set("X-Connection", r.RemoteAddr)
					if r.URL.Path == "/next" {
						fmt.Fprint(w, "next")
						return
					}
					select {
					case <-body.started:
					case <-r.Context().Done():
						return
					}
					if status == 307 || status == 308 {
						w.Header().Set("Location", "/next")
					}
					w.WriteHeader(status)
					w.(http.Flusher).Flush()
					// The response must return before the server sends its body.
					select {
					case <-release:
						if mode == "unknown-length" {
							if _, err := io.Copy(io.Discard, r.Body); err != nil {
								t.Error(err)
								return
							}
						}
						fmt.Fprint(w, "early response body")
					case <-r.Context().Done():
					}
				}))
				s.EnableHTTP2 = true
				s.StartTLS()
				defer s.Close()
				ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
				defer cancel()
				c := streamClient(t)
				opts := &Options{DisableTimeout: true}
				var upload io.ReadCloser = body
				if mode != "unknown-length" {
					length := int64(10)
					opts.ContentLength = &length
				}
				if mode == "eof-probe" {
					// The first read matches Content-Length, then the EOF probe
					// blocks. Closing must not turn it into a response reset.
					upload = struct {
						io.Reader
						io.Closer
					}{io.MultiReader(strings.NewReader("0123456789"), body), body}
				}
				r, err := c.DoStream(ctx, http.MethodPost, s.URL, upload, opts)
				if err != nil {
					t.Fatal(err)
				}
				defer r.Body.Close()
				if r.StatusCode != status {
					t.Fatalf("status=%d, want %d (non-replayable upload must not follow 307/308)", r.StatusCode, status)
				}
				if got := body.closes.Load(); got != 1 {
					t.Fatalf("upload closed %d times before response, want once", got)
				}
				close(release)
				data, err := io.ReadAll(r.Body)
				if err != nil || string(data) != "early response body" {
					t.Fatalf("response body=%q err=%v", data, err)
				}
				r.Body.Close()
				next, err := c.Do(ctx, http.MethodGet, s.URL+"/next", nil, nil)
				if err != nil {
					t.Fatal(err)
				}
				if next.Headers.Get("X-Connection") != r.Headers.Get("X-Connection") {
					t.Fatal("early response prevented HTTP/2 connection reuse")
				}
				if got := body.closes.Load(); got != 1 {
					t.Fatalf("upload closed %d times, want once", got)
				}
			})
		}
	}
}

func TestHTTP2SuccessKeepsUploadWritable(t *testing.T) {
	s := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusEarlyHints)
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		data, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
			return
		}
		w.Write(data)
	}))
	s.EnableHTTP2 = true
	s.StartTLS()
	defer s.Close()
	reader, writer := io.Pipe()
	defer reader.Close()
	defer writer.Close()
	body := &observedUpload{ReadCloser: reader, started: make(chan struct{}), closed: make(chan struct{})}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	r, err := streamClient(t).DoStream(ctx, http.MethodPost, s.URL, body, &Options{DisableTimeout: true})
	if err != nil {
		t.Fatal(err)
	}
	defer r.Body.Close()
	if body.closes.Load() != 0 {
		t.Fatal("successful response closed an ongoing upload")
	}
	if _, err := io.WriteString(writer, "upload after response headers"); err != nil {
		t.Fatal(err)
	}
	writer.Close()
	data, err := io.ReadAll(r.Body)
	if err != nil || string(data) != "upload after response headers" {
		t.Fatalf("full duplex response=%q err=%v", data, err)
	}
}

func TestUploadContentLengthMatchesBody(t *testing.T) {
	for _, h2 := range []bool{false, true} {
		t.Run(fmt.Sprint("http2=", h2), func(t *testing.T) {
			s := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, err := io.ReadAll(r.Body)
				fmt.Fprintf(w, "%s|%v", body, err)
			}))
			s.EnableHTTP2 = h2
			s.StartTLS()
			defer s.Close()
			c := streamClient(t)
			for _, tc := range []struct {
				name      string
				body      any
				length    int64
				wantError bool
			}{
				{"nil", nil, 5, true},
				{"empty-string", "", 5, true},
				{"empty-buffer", bytes.NewBuffer(nil), 5, true},
				{"known-short", "abc", 5, true},
				{"known-long", "abcde", 3, true},
				{"opaque-short", io.LimitReader(strings.NewReader("abc"), 3), 5, true},
				{"opaque-long", io.LimitReader(strings.NewReader("abcde"), 5), 3, true},
				{"exact", io.LimitReader(strings.NewReader("abc"), 3), 3, false},
				{"unknown", "abc", -1, false},
			} {
				t.Run(tc.name, func(t *testing.T) {
					r, err := c.Do(context.Background(), http.MethodPost, s.URL, tc.body, &Options{ContentLength: &tc.length})
					if (err != nil) != tc.wantError {
						t.Fatalf("error=%v wantError=%t response=%v", err, tc.wantError, r)
					}
					if err == nil && r.Text() != "abc|<nil>" {
						t.Fatalf("upload corrupted: %q", r.Text())
					}
				})
			}
		})
	}
}

func TestMultipartUnicodeDisposition(t *testing.T) {
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	if err := writeMultipartFile(w, FileField{FieldName: "附件", FileName: "测试.txt", Data: []byte("payload")}); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	wire := body.String()
	if !strings.Contains(wire, `name="附件"`) || !strings.Contains(wire, `filename="测试.txt"`) || strings.Contains(wire, "filename*=") {
		t.Fatalf("non-form-data filename encoding: %s", wire)
	}
	part, err := multipart.NewReader(&body, w.Boundary()).NextPart()
	if err != nil {
		t.Fatal(err)
	}
	if part.FormName() != "附件" || part.FileName() != "测试.txt" {
		t.Fatal("Unicode names lost")
	}
}

func TestMultipartRejectsHeaderLineBreaks(t *testing.T) {
	var hits atomic.Int32
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		io.Copy(io.Discard, r.Body)
	}))
	defer s.Close()
	c := streamClient(t)
	for _, tc := range []struct {
		name   string
		fields map[string]string
		files  []FileField
	}{
		{"field", map[string]string{"field\r\nX-Injected: yes": "value"}, nil},
		{"file-field", nil, []FileField{{FieldName: "field\r\nX-Injected: yes"}}},
		{"filename", nil, []FileField{{FileName: "file\r\nX-Injected: yes"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := c.PostMultipart(s.URL, tc.fields, tc.files, nil); err == nil {
				t.Fatal("line breaks accepted in a multipart header parameter")
			}
		})
	}
	if hits.Load() != 0 {
		t.Fatal("invalid multipart metadata reached the network")
	}
}
