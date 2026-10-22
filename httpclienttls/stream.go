package httpclienttls

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	fhttp "github.com/bogdanfinn/fhttp"
)

var (
	ErrHeaderTimeout   = fmt.Errorf("response header timeout: %w", context.DeadlineExceeded)
	ErrReadIdleTimeout = fmt.Errorf("response read idle timeout: %w", context.DeadlineExceeded)
)

// Stop also joins an expired callback so it cannot cancel a later exchange or
// read after the caller has already checked the context's cancellation cause.
func startCancelTimer(timeout time.Duration, cancel context.CancelCauseFunc, cause error) func() {
	if timeout <= 0 {
		return func() {}
	}
	done := make(chan struct{})
	timer := time.AfterFunc(timeout, func() {
		defer close(done)
		cancel(cause)
	})
	return func() {
		if !timer.Stop() {
			<-done
		}
	}
}

// StreamResponse owns a live response body. Close it on every path, including
// when rejecting a status code. MaxResponseBodyBytes applies only to buffered
// APIs; streaming consumers must impose their own byte/event limits.
type StreamResponse struct {
	StatusCode       int
	Status           string
	Proto            string
	Headers          http.Header
	Cookies          []*http.Cookie
	Body             io.ReadCloser
	Request          *http.Request
	URL              string
	ContentLength    int64
	TransferEncoding []string
	Uncompressed     bool
}

// CopyTo writes at most maxBytes and detects oversized responses. On error the
// destination can contain a partial result; callers publishing files should
// use a temporary file and rename only on success. The caller still closes Body.
func (r *StreamResponse) CopyTo(dst io.Writer, maxBytes int64) (int64, error) {
	if maxBytes <= 0 {
		return 0, errors.New("maxBytes must be positive")
	}
	n, err := io.Copy(dst, io.LimitReader(r.Body, maxBytes))
	if err != nil {
		return n, err
	}
	var extra [1]byte
	_, err = io.ReadFull(r.Body, extra[:])
	if err == io.EOF {
		return n, nil
	}
	if err != nil {
		return n, err
	}
	return n, fmt.Errorf("response body exceeds %d bytes", maxBytes)
}

func buildStreamResponse(raw *fhttp.Response, req *fhttp.Request, ctx context.Context, cancel context.CancelCauseFunc, cleanup func(), idle time.Duration) *StreamResponse {
	snapshot, _ := http.NewRequestWithContext(ctx, req.Method, req.URL.String(), nil)
	snapshot.Header = toStandardHeader(req.Header)
	return &StreamResponse{
		StatusCode: raw.StatusCode, Status: raw.Status, Proto: raw.Proto,
		Headers: toStandardHeader(raw.Header), Cookies: toStandardCookies(raw.Cookies()),
		Body:    &streamBody{raw: raw.Body, ctx: ctx, cancel: cancel, cleanup: cleanup, idle: idle},
		Request: snapshot, URL: req.URL.String(), ContentLength: raw.ContentLength,
		TransferEncoding: append([]string(nil), raw.TransferEncoding...), Uncompressed: raw.Uncompressed,
	}
}

func (c *Client) bufferResponse(r *StreamResponse) (*Response, error) {
	defer r.Body.Close()
	c.mu.RLock()
	limit := c.maxResponseBodyBytes
	c.mu.RUnlock()
	body, err := readResponseBody(r.Body, limit)
	if err != nil {
		return nil, err
	}
	return &Response{
		StatusCode: r.StatusCode, Status: r.Status, Proto: r.Proto, Headers: r.Headers,
		Cookies: r.Cookies, Body: body, Request: r.Request, URL: r.URL,
		TransferEncoding: r.TransferEncoding, Uncompressed: r.Uncompressed,
	}, nil
}

// As with net/http, one goroutine reads Body; Close may run concurrently.
type streamBody struct {
	raw         io.ReadCloser
	ctx         context.Context
	cancel      context.CancelCauseFunc
	cleanup     func()
	idle        time.Duration
	once        sync.Once
	readMu      sync.Mutex
	closed      bool
	closeErr    error
	terminalErr error // accessed by the single reader only
}

func (b *streamBody) Read(p []byte) (int, error) {
	b.readMu.Lock()
	n, err := b.read(p)
	b.readMu.Unlock()
	if err != nil {
		b.Close()
	}
	return n, err
}

func (b *streamBody) read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	if b.terminalErr != nil {
		return 0, b.terminalErr
	}
	if b.closed {
		return 0, http.ErrBodyReadAfterClose
	}
	if err := context.Cause(b.ctx); err != nil {
		return 0, err
	}
	stopTimer := startCancelTimer(b.idle, b.cancel, ErrReadIdleTimeout)
	n, err := b.raw.Read(p)
	stopTimer()
	if cause := context.Cause(b.ctx); cause != nil {
		err = cause
	}
	if err != nil {
		b.terminalErr = err
	}
	return n, err
}

func (b *streamBody) Close() error {
	b.once.Do(func() {
		b.cleanup()
		// Cancel first so a blocked Read exits before a decompressor is closed.
		b.readMu.Lock()
		defer b.readMu.Unlock()
		b.closed = true
		b.closeErr = b.raw.Close()
	})
	return b.closeErr
}
