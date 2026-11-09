package httpclienttls

import (
	"bytes"
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http/httptest"
	"testing"
	"time"

	patched "github.com/Drunkard-baifeng/public_golibs/httpclienttls/internal/tlsclient"
	http "github.com/bogdanfinn/fhttp"
	"github.com/bogdanfinn/fhttp/http2"
	"github.com/bogdanfinn/fhttp/http2/hpack"
	upstream "github.com/bogdanfinn/tls-client"
)

// Compare actual initial HTTP/2 frames, including SETTINGS order, window update,
// priorities, pseudo-headers and HPACK bytes, with the pinned upstream client.
func TestPatchedTransportPreservesHTTP2Wire(t *testing.T) {
	certServer := httptest.NewTLSServer(nil)
	tlsConfig := certServer.TLS.Clone()
	certServer.Close()
	tlsConfig.NextProtos = []string{"h2"}
	for _, name := range []string{"chrome_146", "firefox_148", "safari_16_0"} {
		t.Run(name, func(t *testing.T) {
			profile, err := selectProfile(Config{Profile: name})
			if err != nil {
				t.Fatal(err)
			}
			listener, err := tls.Listen("tcp", "127.0.0.1:0", tlsConfig)
			if err != nil {
				t.Fatal(err)
			}
			defer listener.Close()
			type captured struct {
				data []byte
				err  error
			}
			captures := make(chan captured, 2)
			go func() {
				for i := 0; i < 2; i++ {
					conn, err := listener.Accept()
					if err != nil {
						captures <- captured{err: err}
						return
					}
					data, err := captureInitialHTTP2(conn)
					captures <- captured{data, err}
				}
			}()
			original, err := upstream.NewHttpClient(upstream.NewNoopLogger(),
				upstream.WithClientProfile(profile.client), upstream.WithInsecureSkipVerify(), upstream.WithDisableHttp3())
			if err != nil {
				t.Fatal(err)
			}
			defer original.CloseIdleConnections()
			fixed, err := patched.NewHttpClient(patched.NewNoopLogger(),
				patched.WithClientProfile(profile.client), patched.WithInsecureSkipVerify(), patched.WithDisableHttp3())
			if err != nil {
				t.Fatal(err)
			}
			defer fixed.CloseIdleConnections()
			var snapshots [][]byte
			for _, client := range []interface {
				Do(*http.Request) (*http.Response, error)
			}{original, fixed} {
				ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
				defer cancel()
				req, err := http.NewRequestWithContext(ctx, "GET", "https://"+listener.Addr().String()+"/wire", nil)
				if err != nil {
					t.Fatal(err)
				}
				req.Header.Set("User-Agent", profile.UserAgent)
				res, err := client.Do(req)
				if err != nil {
					t.Fatal(err)
				}
				res.Body.Close()
				select {
				case capture := <-captures:
					if capture.err != nil {
						t.Fatal(capture.err)
					}
					snapshots = append(snapshots, capture.data)
				case <-ctx.Done():
					t.Fatal(ctx.Err())
				}
			}
			if !bytes.Equal(snapshots[0], snapshots[1]) {
				t.Fatalf("HTTP/2 wire changed:\nupstream=%x\npatched =%x", snapshots[0], snapshots[1])
			}
		})
	}
}

func captureInitialHTTP2(conn net.Conn) ([]byte, error) {
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(3 * time.Second))
	var wire bytes.Buffer
	reader := io.TeeReader(conn, &wire)
	preface := make([]byte, len(http2.ClientPreface))
	if _, err := io.ReadFull(reader, preface); err != nil {
		return nil, err
	}
	if string(preface) != http2.ClientPreface {
		return nil, fmt.Errorf("invalid HTTP/2 preface")
	}
	fr := http2.NewFramer(conn, reader)
	fr.ReadMetaHeaders = hpack.NewDecoder(4096, nil)
	for {
		frame, err := fr.ReadFrame()
		if err != nil {
			return nil, err
		}
		if headers, ok := frame.(*http2.MetaHeadersFrame); ok {
			snapshot := append([]byte(nil), wire.Bytes()...)
			if err := fr.WriteSettings(); err != nil {
				return nil, err
			}
			// Drain the settings acknowledgement before closing the socket;
			// Windows otherwise resets a connection with unread incoming data.
			for {
				f, err := fr.ReadFrame()
				if err != nil {
					return nil, err
				}
				if ack, ok := f.(*http2.SettingsFrame); ok && ack.IsAck() {
					break
				}
			}
			var block bytes.Buffer
			encoder := hpack.NewEncoder(&block)
			if err := encoder.WriteField(hpack.HeaderField{Name: ":status", Value: "200"}); err != nil {
				return nil, err
			}
			err := fr.WriteHeaders(http2.HeadersFrameParam{StreamID: headers.StreamID, BlockFragment: block.Bytes(), EndHeaders: true, EndStream: true})
			return snapshot, err
		}
	}
}
