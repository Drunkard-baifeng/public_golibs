package http2

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	stdhttp "net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	http "github.com/bogdanfinn/fhttp"
	"github.com/bogdanfinn/fhttp/http2/hpack"
	tls "github.com/bogdanfinn/utls"
)

type blockedBody struct {
	*io.PipeReader
	started chan struct{}
	once    sync.Once
	closes  atomic.Int32
}

func (b *blockedBody) Read(p []byte) (int, error) {
	b.once.Do(func() { close(b.started) })
	return b.PipeReader.Read(p)
}

func (b *blockedBody) Close() error {
	b.closes.Add(1)
	return b.PipeReader.Close()
}

func TestEarlyResponseWithExpectContinue(t *testing.T) {
	for _, sendContinue := range []bool{false, true} {
		t.Run(fmt.Sprint(sendContinue), func(t *testing.T) {
			reader, writer := io.Pipe()
			defer reader.Close()
			defer writer.Close()
			body := &blockedBody{PipeReader: reader, started: make(chan struct{})}
			s := httptest.NewUnstartedServer(stdhttp.HandlerFunc(func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
				if sendContinue {
					w.WriteHeader(stdhttp.StatusContinue)
					select {
					case <-body.started:
					case <-r.Context().Done():
						return
					}
				}
				w.WriteHeader(stdhttp.StatusRequestEntityTooLarge)
				w.(stdhttp.Flusher).Flush()
				if _, err := io.Copy(io.Discard, r.Body); err != nil {
					t.Error(err)
					return
				}
				fmt.Fprint(w, "response tail")
			}))
			s.EnableHTTP2 = true
			s.StartTLS()
			defer s.Close()
			tr := &Transport{
				TLSClientConfig:   &tls.Config{InsecureSkipVerify: true},
				PseudoHeaderOrder: []string{":method", ":scheme", ":authority", ":path"},
				t1:                &http.Transport{ExpectContinueTimeout: time.Hour},
			}
			defer tr.CloseIdleConnections()
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			req, err := http.NewRequestWithContext(ctx, "POST", s.URL, body)
			if err != nil {
				t.Fatal(err)
			}
			req.Header.Set("Expect", "100-continue")
			res, err := tr.RoundTrip(req)
			if err != nil {
				t.Fatal(err)
			}
			defer res.Body.Close()
			data, err := io.ReadAll(res.Body)
			if err != nil || string(data) != "response tail" {
				t.Fatalf("body=%q err=%v", data, err)
			}
			if body.closes.Load() != 1 {
				t.Fatalf("body closed %d times", body.closes.Load())
			}
			if !sendContinue {
				select {
				case <-body.started:
					t.Fatal("canceled continue timer started the upload")
				default:
				}
			}
		})
	}
}

// A strict peer rejects a premature END_STREAM with fewer DATA bytes than the
// declared Content-Length. Use actual frames so permissive server handlers
// cannot hide a protocol violation. A second stream marks response delivery.
func TestEarlyKnownLengthResponseDoesNotSendTruncatedEndStream(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	reader, writer := io.Pipe()
	defer reader.Close()
	defer writer.Close()
	body := &blockedBody{PipeReader: reader, started: make(chan struct{})}
	peerDone := make(chan error, 1)
	go func() {
		peerDone <- func() error {
			conn, err := listener.Accept()
			if err != nil {
				return err
			}
			defer conn.Close()
			conn.SetDeadline(time.Now().Add(5 * time.Second))
			preface := make([]byte, len(ClientPreface))
			if _, err := io.ReadFull(conn, preface); err != nil {
				return err
			}
			if string(preface) != ClientPreface {
				return fmt.Errorf("invalid HTTP/2 preface")
			}
			fr := NewFramer(conn, conn)
			if err := fr.WriteSettings(); err != nil {
				return err
			}
			writeStatus := func(id uint32, status string, end bool) error {
				var block bytes.Buffer
				enc := hpack.NewEncoder(&block)
				if err := enc.WriteField(hpack.HeaderField{Name: ":status", Value: status}); err != nil {
					return err
				}
				return fr.WriteHeaders(HeadersFrameParam{StreamID: id, BlockFragment: block.Bytes(), EndHeaders: true, EndStream: end})
			}
			var uploadStream uint32
			responseEnded := false
			for {
				frame, err := fr.ReadFrame()
				if err != nil {
					return err
				}
				switch f := frame.(type) {
				case *SettingsFrame:
					if !f.IsAck() {
						if err := fr.WriteSettingsAck(); err != nil {
							return err
						}
					}
				case *HeadersFrame:
					if uploadStream == 0 {
						uploadStream = f.StreamID
						select {
						case <-body.started:
						case <-time.After(3 * time.Second):
							return fmt.Errorf("upload never started")
						}
						if err := writeStatus(uploadStream, "403", false); err != nil {
							return err
						}
					} else if f.StreamID != uploadStream {
						if err := writeStatus(f.StreamID, "200", true); err != nil {
							return err
						}
						if err := fr.WriteData(uploadStream, true, []byte("response tail")); err != nil {
							return err
						}
						responseEnded = true
					}
				case *DataFrame:
					if f.StreamID == uploadStream && f.StreamEnded() {
						return fmt.Errorf("truncated request END_STREAM: declared 10 bytes, sent 0")
					}
				case *RSTStreamFrame:
					if f.StreamID == uploadStream {
						if !responseEnded || f.ErrCode != ErrCodeNo {
							return fmt.Errorf("unexpected upload reset: responseEnded=%t code=%v", responseEnded, f.ErrCode)
						}
						return nil
					}
				}
			}
		}()
	}()
	tr := &Transport{AllowHTTP: true, PseudoHeaderOrder: []string{":method", ":scheme", ":authority", ":path"},
		DialTLS: func(network, addr string, _ *tls.Config) (net.Conn, error) {
			return (&net.Dialer{Timeout: time.Second}).Dial(network, addr)
		},
	}
	defer tr.CloseIdleConnections()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	url := "http://" + listener.Addr().String()
	req, _ := http.NewRequestWithContext(ctx, "POST", url, body)
	req.ContentLength = 10
	res, err := tr.RoundTrip(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	marker, _ := http.NewRequestWithContext(ctx, "GET", url, nil)
	markerRes, err := tr.RoundTrip(marker)
	if err != nil {
		t.Fatal(err)
	}
	markerRes.Body.Close()
	data, err := io.ReadAll(res.Body)
	if err != nil || string(data) != "response tail" {
		t.Fatalf("body=%q err=%v", data, err)
	}
	select {
	case err := <-peerDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("aborted upload not cleaned up after response")
	}
	if body.closes.Load() != 1 {
		t.Fatalf("upload closed %d times", body.closes.Load())
	}
}

func TestSuccessfulResponseReleasesBlockedUpload(t *testing.T) {
	for _, mode := range []string{"eof", "close", "connection-loss", "cancel", "head"} {
		t.Run(mode, func(t *testing.T) {
			reader, writer := io.Pipe()
			defer reader.Close()
			defer writer.Close()
			body := &blockedBody{PipeReader: reader, started: make(chan struct{})}
			s := httptest.NewUnstartedServer(stdhttp.HandlerFunc(func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
				select {
				case <-body.started:
				case <-r.Context().Done():
					return
				}
				w.WriteHeader(200)
				w.(stdhttp.Flusher).Flush()
				if mode == "eof" {
					fmt.Fprint(w, "done")
					return
				}
				<-r.Context().Done()
			}))
			s.EnableHTTP2 = true
			s.StartTLS()
			defer s.Close()
			tr := &Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, PseudoHeaderOrder: []string{":method", ":scheme", ":authority", ":path"}}
			defer tr.CloseIdleConnections()
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			method := "POST"
			if mode == "head" {
				method = "HEAD"
			}
			req, _ := http.NewRequestWithContext(ctx, method, s.URL, body)
			res, err := tr.RoundTrip(req)
			if err != nil {
				t.Fatal(err)
			}
			defer res.Body.Close()
			switch mode {
			case "eof":
				data, err := io.ReadAll(res.Body)
				if err != nil || string(data) != "done" {
					t.Fatalf("body=%q error=%v", data, err)
				}
			case "close", "head":
				res.Body.Close()
			case "connection-loss":
				s.CloseClientConnections()
			case "cancel":
				cancel()
			}
			deadline := time.Now().Add(500 * time.Millisecond)
			for body.closes.Load() == 0 && time.Now().Before(deadline) {
				time.Sleep(time.Millisecond)
			}
			if body.closes.Load() != 1 {
				t.Fatalf("terminal response left upload open: closes=%d", body.closes.Load())
			}
		})
	}
}

func TestCompletedWriterStillReleasesUnfinishedUpload(t *testing.T) {
	for i := 0; i < 100; i++ {
		var wire bytes.Buffer
		cc := &ClientConn{streams: make(map[uint32]*clientStream), maxFrameSize: 16384}
		cc.cond = sync.NewCond(&cc.mu)
		cc.bw = bufio.NewWriter(&wire)
		cc.fr = NewFramer(cc.bw, nil)
		cs := cc.newStreamWithID(1, true)
		req, _ := http.NewRequest("POST", "http://fixture.test", bytes.NewBufferString("x"))
		req.ContentLength = 2
		cs.req = req
		cs.bufPipe = pipe{b: &dataBuffer{}}
		// Final response DATA removes the stream before closing its pipe;
		// a flow-control waiter may finish first without sending END_STREAM.
		cc.forgetStreamID(cs.ID)
		err := cs.writeRequestBody(req.Body)
		if err != errStreamClosed {
			t.Fatalf("writer error=%v", err)
		}
		resc := make(chan error, 1)
		resc <- err
		cs.bufPipe.CloseWithError(io.EOF)
		cs.watchSuccessfulUpload(bodyWriterState{resc: resc}, true)
		frame, err := NewFramer(nil, &wire).ReadFrame()
		if err != nil {
			t.Fatalf("iteration %d: unfinished upload was not reset: %v", i, err)
		}
		reset, ok := frame.(*RSTStreamFrame)
		if !ok || reset.ErrCode != ErrCodeNo {
			t.Fatalf("unexpected cleanup frame: %v", frame)
		}
	}
}

func TestSuccessfulResponseAfterWriterCompletion(t *testing.T) {
	for attempt := 0; attempt < 20; attempt++ {
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		peer := make(chan error, 1)
		go func() {
			peer <- func() error {
				conn, err := listener.Accept()
				if err != nil {
					return err
				}
				defer conn.Close()
				conn.SetDeadline(time.Now().Add(time.Second))
				preface := make([]byte, len(ClientPreface))
				if _, err = io.ReadFull(conn, preface); err != nil {
					return err
				}
				fr := NewFramer(conn, conn)
				if err = fr.WriteSettings(Setting{ID: SettingInitialWindowSize, Val: 0}); err != nil {
					return err
				}
				var id uint32
				for {
					f, err := fr.ReadFrame()
					if err != nil {
						return err
					}
					switch f := f.(type) {
					case *SettingsFrame:
						if !f.IsAck() {
							if err = fr.WriteSettingsAck(); err != nil {
								return err
							}
						}
					case *HeadersFrame:
						id = f.StreamID
						var block, frames bytes.Buffer
						enc := hpack.NewEncoder(&block)
						enc.WriteField(hpack.HeaderField{Name: ":status", Value: "200"})
						fw := NewFramer(&frames, nil)
						fw.WriteHeaders(HeadersFrameParam{StreamID: id, EndHeaders: true, BlockFragment: block.Bytes()})
						fw.WriteData(id, true, []byte("done"))
						if _, err = conn.Write(frames.Bytes()); err != nil {
							return err
						}
						conn.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
					case *RSTStreamFrame:
						if f.StreamID == id && f.ErrCode == ErrCodeNo {
							return nil
						}
					case *DataFrame:
						if f.StreamEnded() {
							return fmt.Errorf("unexpected complete upload")
						}
					}
				}
			}()
		}()
		tr := &Transport{AllowHTTP: true, PseudoHeaderOrder: []string{":method", ":scheme", ":authority", ":path"}, DialTLS: func(network, addr string, _ *tls.Config) (net.Conn, error) { return net.Dial(network, addr) }}
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		req, _ := http.NewRequestWithContext(selectDelayContext{ctx}, "POST", "http://"+listener.Addr().String(), bytes.NewReader(make([]byte, 1<<20)))
		res, err := tr.RoundTrip(req)
		if err == nil {
			data, e := io.ReadAll(res.Body)
			res.Body.Close()
			if e != nil || string(data) != "done" {
				err = fmt.Errorf("body=%q err=%v", data, e)
			}
		}
		peerErr := <-peer
		cancel()
		tr.CloseIdleConnections()
		listener.Close()
		if err != nil {
			t.Fatalf("attempt %d request error: %v", attempt, err)
		}
		if peerErr != nil {
			t.Fatalf("attempt %d: successful response but no upload reset: %v", attempt, peerErr)
		}
	}
}

type selectDelayContext struct{ context.Context }

func (c selectDelayContext) Done() <-chan struct{} {
	// Force both response and writer-result channels ready before the main select.
	time.Sleep(5 * time.Millisecond)
	return c.Context.Done()
}
