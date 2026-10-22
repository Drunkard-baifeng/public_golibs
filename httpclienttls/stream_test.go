package httpclienttls

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func streamClient(t *testing.T) *Client {
	t.Helper()
	c, err := NewClient(Config{Profile: "chrome_146", Verify: BoolPtr(false), Timeout: 3 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(c.Close)
	return c
}

func TestStreamReturnsBeforeCompletionAndCloseCancels(t *testing.T) {
	for _, h2 := range []bool{false, true} {
		t.Run(fmt.Sprint("http2=", h2), func(t *testing.T) {
			stopped := make(chan struct{})
			s := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				defer close(stopped)
				w.Header().Set("Content-Type", "text/event-stream")
				io.WriteString(w, "data: first\n\n")
				w.(http.Flusher).Flush()
				<-r.Context().Done()
			}))
			s.EnableHTTP2 = h2
			s.StartTLS()
			defer s.Close()
			c := streamClient(t)
			r, err := c.DoStream(context.Background(), "GET", s.URL, nil, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer r.Body.Close()
			if h2 && r.Proto != "HTTP/2.0" {
				t.Fatalf("protocol: %s", r.Proto)
			}
			first := make([]byte, len("data: first\n\n"))
			if _, err := io.ReadFull(r.Body, first); err != nil {
				t.Fatal(err)
			}
			if string(first) != "data: first\n\n" {
				t.Fatalf("first=%q", first)
			}
			r.Body.Close()
			select {
			case <-stopped:
			case <-time.After(time.Second):
				t.Fatal("upstream remained active after Close")
			}
		})
	}
}

func TestStreamDeadlines(t *testing.T) {
	for _, mode := range []string{"header", "idle", "total", "parent"} {
		t.Run(mode, func(t *testing.T) {
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if mode != "header" {
					w.WriteHeader(200)
					w.(http.Flusher).Flush()
				}
				<-r.Context().Done()
			}))
			defer s.Close()
			c := streamClient(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			opts := &Options{DisableTimeout: true}
			want := error(context.Canceled)
			switch mode {
			case "header":
				opts.HeaderTimeout = 60 * time.Millisecond
				want = ErrHeaderTimeout
			case "idle":
				opts.ReadIdleTimeout = 60 * time.Millisecond
				want = ErrReadIdleTimeout
			case "total":
				opts.DisableTimeout = false
				opts.Timeout = 60 * time.Millisecond
				want = context.DeadlineExceeded
			}
			r, err := c.DoStream(ctx, "GET", s.URL, nil, opts)
			if mode != "header" {
				if err != nil {
					t.Fatal(err)
				}
				defer r.Body.Close()
				if mode == "parent" {
					cancel()
				}
				_, err = io.ReadAll(r.Body)
			}
			if !errors.Is(err, want) {
				t.Fatalf("error=%v, want %v", err, want)
			}
		})
	}
}

func TestIdleTimeoutDoesNotCountConsumerPause(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "ok") }))
	defer s.Close()
	r, err := streamClient(t).DoStream(context.Background(), "GET", s.URL, nil, &Options{ReadIdleTimeout: 25 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	defer r.Body.Close()
	time.Sleep(60 * time.Millisecond)
	b, err := io.ReadAll(r.Body)
	if err != nil || string(b) != "ok" {
		t.Fatalf("body=%q err=%v", b, err)
	}
}

func TestStreamingUploadStartsBeforeReaderEOF(t *testing.T) {
	seen := make(chan struct{})
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b := make([]byte, 5)
		if _, err := io.ReadFull(r.Body, b); err != nil {
			return
		}
		close(seen)
		io.Copy(io.Discard, r.Body)
		w.Write(b)
	}))
	defer s.Close()
	reader, writer := io.Pipe()
	defer reader.Close()
	go func() {
		defer writer.Close()
		io.WriteString(writer, "first")
		select {
		case <-seen:
		case <-time.After(time.Second):
			writer.CloseWithError(errors.New("upload was buffered"))
		}
	}()
	r, err := streamClient(t).Do(context.Background(), "POST", s.URL, reader, nil)
	if err != nil || r.Text() != "first" {
		t.Fatalf("response=%v err=%v", r, err)
	}
}

func TestStreamRedirectReplayPolicy(t *testing.T) {
	var hits atomic.Int32
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		io.Copy(io.Discard, r.Body)
		switch r.URL.Path {
		case "/":
			http.Redirect(w, r, "/again", 307)
		case "/again":
			http.Redirect(w, r, "/end", 308)
		default:
			io.WriteString(w, r.Method)
		}
	}))
	defer s.Close()
	c := streamClient(t)
	// An opaque reader cannot be replayed, even if its concrete data is small.
	r, err := c.Do(context.Background(), "POST", s.URL, io.LimitReader(strings.NewReader("payload"), 7), nil)
	if err != nil || r.StatusCode != 307 || hits.Load() != 1 {
		t.Fatalf("opaque: response=%v err=%v hits=%d", r, err, hits.Load())
	}
	hits.Store(0)
	r, err = c.Do(context.Background(), "POST", s.URL, "payload", nil)
	if err != nil || r.Text() != "POST" || hits.Load() != 3 {
		t.Fatalf("replay: response=%v err=%v hits=%d", r, err, hits.Load())
	}
}

func TestRedirectStripsCredentialsAcrossPortsAndDowngrade(t *testing.T) {
	dest := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "%s|%s", r.Header.Get("Authorization"), r.Header.Get("Cookie"))
	}))
	defer dest.Close()
	for _, tls := range []bool{false, true} {
		handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, dest.URL, 302) })
		var src *httptest.Server
		if tls {
			src = httptest.NewTLSServer(handler)
		} else {
			src = httptest.NewServer(handler)
		}
		r, err := streamClient(t).Get(src.URL, &Options{Headers: map[string]string{"Authorization": "Bearer fixture", "Cookie": "secret=fixture"}})
		src.Close()
		if err != nil || r.Text() != "|" {
			t.Fatalf("credentials leaked: response=%v err=%v", r, err)
		}
	}
}

func TestStreamCompressionAndBoundedDownload(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Encoding", "gzip")
		zw := gzip.NewWriter(w)
		io.WriteString(zw, "hello world")
		zw.Close()
	}))
	defer s.Close()
	c := streamClient(t)
	for _, limit := range []int64{5, 11, 20} {
		r, err := c.DoStream(context.Background(), "GET", s.URL, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		var dst bytes.Buffer
		n, err := r.CopyTo(&dst, limit)
		r.Body.Close()
		if limit == 5 {
			if err == nil || n != 5 || dst.String() != "hello" {
				t.Fatalf("limit failed: %d %q %v", n, dst.String(), err)
			}
		} else if err != nil || dst.String() != "hello world" {
			t.Fatalf("decode failed: %q %v", dst.String(), err)
		}
	}
}

func TestMultipartFileStreamingAndMissingFile(t *testing.T) {
	p := filepath.Join(t.TempDir(), "input.bin")
	if err := os.WriteFile(p, bytes.Repeat([]byte("x"), 256<<10), 0600); err != nil {
		t.Fatal(err)
	}
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mr, err := r.MultipartReader()
		if err != nil {
			http.Error(w, "multipart", 400)
			return
		}
		var n int64
		for {
			part, err := mr.NextPart()
			if err != nil {
				break
			}
			count, _ := io.Copy(io.Discard, part)
			n += count
		}
		fmt.Fprint(w, n)
	}))
	defer s.Close()
	c := streamClient(t)
	r, err := c.PostMultipartContext(context.Background(), s.URL, map[string]string{"prompt": "abc"}, []FileField{{FilePath: p}}, nil)
	if err != nil || r.Text() != "262147" {
		t.Fatalf("multipart=%v err=%v", r, err)
	}
	_, err = c.PostMultipartContext(context.Background(), s.URL, nil, []FileField{{FilePath: p + "missing"}}, nil)
	if err == nil {
		t.Fatal("missing file should fail")
	}
}

func TestSignedURLPreservesRawQuery(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, r.URL.RawQuery) }))
	defer s.Close()
	query := "z=1&a=%20&a=+&signature=abc%2fdef"
	r, err := streamClient(t).Get(s.URL+"?"+query, nil)
	if err != nil || r.Text() != query {
		t.Fatalf("query changed: response=%v err=%v", r, err)
	}
}

func TestCompressedStreamConcurrentCloseAndClientReuse(t *testing.T) {
	for _, h2 := range []bool{false, true} {
		t.Run(fmt.Sprint("http2=", h2), func(t *testing.T) {
			s := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Encoding", "gzip")
				zw := gzip.NewWriter(w)
				defer zw.Close()
				io.WriteString(zw, "first")
				zw.Flush()
				w.(http.Flusher).Flush()
				if r.URL.Path == "/stream" {
					<-r.Context().Done()
				}
			}))
			s.EnableHTTP2 = h2
			s.StartTLS()
			defer s.Close()
			c := streamClient(t)
			parent, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			for i := 0; i < 8; i++ {
				r, err := c.DoStream(parent, "GET", s.URL+"/stream", nil, nil)
				if err != nil {
					t.Fatal(err)
				}
				prefix := make([]byte, 5)
				if _, err := io.ReadFull(r.Body, prefix); err != nil {
					t.Fatal(err)
				}
				done := make(chan struct{})
				go func() { defer close(done); io.Copy(io.Discard, r.Body) }()
				var closers sync.WaitGroup
				for n := 0; n < 3; n++ {
					closers.Add(1)
					go func() { defer closers.Done(); r.Body.Close() }()
				}
				closers.Wait()
				select {
				case <-done:
				case <-time.After(time.Second):
					t.Fatal("Close did not unblock compressed Read")
				}
				if parent.Err() != nil {
					t.Fatalf("attempt closed parent: %v", parent.Err())
				}
				next, err := c.GetContext(parent, s.URL+"/complete", nil)
				if err != nil || next.Text() != "first" {
					t.Fatalf("reuse: %v %v", next, err)
				}
			}
		})
	}
}

func TestCompressedEOFKeepsConnectionReusable(t *testing.T) {
	var connections atomic.Int32
	s := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Encoding", "gzip")
		zw := gzip.NewWriter(w)
		io.WriteString(zw, "complete")
		zw.Close()
	}))
	s.Config.ConnState = func(_ net.Conn, state http.ConnState) {
		if state == http.StateNew {
			connections.Add(1)
		}
	}
	s.StartTLS()
	defer s.Close()
	c := streamClient(t)
	for i := 0; i < 5; i++ {
		r, err := c.Get(s.URL, nil)
		if err != nil || r.Text() != "complete" {
			t.Fatalf("body=%v err=%v", r, err)
		}
	}
	if connections.Load() != 1 {
		t.Fatalf("EOF/Close prevented connection reuse: %d connections", connections.Load())
	}
}

type timedReadCloser struct {
	read func([]byte) (int, error)
}

func (r timedReadCloser) Read(p []byte) (int, error) { return r.read(p) }
func (r timedReadCloser) Close() error               { return nil }

func TestStreamIdleTimeoutWaitsForCancellation(t *testing.T) {
	ctx, cancel := context.WithCancelCause(context.Background())
	defer cancel(nil)
	entered, release, canceled := make(chan struct{}), make(chan struct{}), make(chan struct{})
	readReturned := make(chan struct{})
	body := &streamBody{
		ctx: ctx, idle: time.Millisecond,
		cleanup: func() { cancel(context.Canceled) },
		cancel: func(err error) {
			close(entered)
			<-release
			cancel(err)
			close(canceled)
		},
		raw: timedReadCloser{read: func(p []byte) (int, error) {
			<-entered
			p[0] = 'x'
			close(readReturned)
			return 1, nil
		}},
	}
	type result struct {
		n   int
		err error
	}
	done := make(chan result, 1)
	go func() {
		n, err := body.Read(make([]byte, 1))
		done <- result{n, err}
	}()
	<-readReturned
	var got result
	premature := false
	select {
	case got = <-done:
		premature = true
	case <-time.After(20 * time.Millisecond):
	}
	close(release)
	<-canceled
	if !premature {
		got = <-done
	}
	body.Close()
	if premature || got.n != 1 || !errors.Is(got.err, ErrReadIdleTimeout) {
		t.Fatalf("Read escaped an expired callback: premature=%t n=%d err=%v", premature, got.n, got.err)
	}
}
