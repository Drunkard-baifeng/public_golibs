package httpclienttls

import (
	"context"
	"fmt"
	"io"
	"sync"
)

// An upload can outlive receipt of response headers (HTTP/2 full duplex).
// Keep cancellation attached until the transport closes the upload itself.
type uploadBody struct {
	raw       io.ReadCloser
	remaining int64
	checked   bool
	stop      func() bool
	done      chan struct{}
	rawOnce   sync.Once
	closeOnce sync.Once
	closeErr  error
}

func newUploadBody(ctx context.Context, raw io.ReadCloser, length int64) *uploadBody {
	b := &uploadBody{raw: raw, remaining: length, checked: length > 0, done: make(chan struct{})}
	b.stop = context.AfterFunc(ctx, func() {
		defer close(b.done)
		b.closeRaw()
	})
	return b
}

func (b *uploadBody) Read(p []byte) (int, error) {
	n, err := b.raw.Read(p)
	if b.checked {
		if int64(n) > b.remaining {
			return n, fmt.Errorf("upload body exceeds Content-Length")
		}
		b.remaining -= int64(n)
		if err == io.EOF && b.remaining > 0 {
			err = io.ErrUnexpectedEOF
		}
	}
	return n, err
}

func (b *uploadBody) closeRaw() {
	b.rawOnce.Do(func() { b.closeErr = b.raw.Close() })
}

func (b *uploadBody) Close() error {
	b.closeOnce.Do(func() {
		if !b.stop() {
			<-b.done
		}
		b.closeRaw()
	})
	return b.closeErr
}
