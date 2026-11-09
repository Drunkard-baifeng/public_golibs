package tls_client

import (
	"context"
	"errors"
	"io"
	stdhttp "net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	http "github.com/bogdanfinn/fhttp"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

func testRacer(transports map[string]http.RoundTripper) *protocolRacer {
	return &protocolRacer{protocolCache: make(map[string]string), cachedTransports: transports, cachedTransportsLck: new(sync.Mutex)}
}

func TestRacingDoesNotReplayUnsafeRequests(t *testing.T) {
	for _, method := range []string{"POST", "PUT", "DELETE", "GET"} {
		t.Run(method, func(t *testing.T) {
			var calls atomic.Int32
			failure := errors.New("request may already have reached peer")
			transport := roundTripFunc(func(req *http.Request) (*http.Response, error) {
				if req.Body != nil {
					req.Body.Close()
				}
				if calls.Add(1) == 1 {
					return nil, failure
				}
				return &http.Response{StatusCode: 200, Body: http.NoBody, Header: make(http.Header)}, nil
			})
			addr := "127.0.0.1:1"
			transports := map[string]http.RoundTripper{addr: transport}
			pr := testRacer(transports)
			pr.protocolCache[addr] = "h2"
			rt := &roundTripper{cachedTransports: transports, racer: pr}
			pr.cachedTransportsLck = &rt.cachedTransportsLck
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			req, _ := http.NewRequestWithContext(ctx, method, "https://"+addr, strings.NewReader("body"))
			res, err := rt.RoundTrip(req)
			if res != nil {
				res.Body.Close()
			}
			if !errors.Is(err, failure) || calls.Load() != 1 {
				t.Fatalf("error=%v calls=%d; request must be sent exactly once", err, calls.Load())
			}
		})
	}
}

type observedRaceBody struct {
	io.Reader
	closed atomic.Int32
}

func (b *observedRaceBody) Close() error { b.closed.Add(1); return nil }

func TestRaceClosesLosingResponse(t *testing.T) {
	addr := "127.0.0.1:1"
	loserBody := &observedRaceBody{Reader: strings.NewReader("loser")}
	winnerBody := &observedRaceBody{Reader: strings.NewReader("winner")}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	pr := testRacer(make(map[string]http.RoundTripper))
	results := make(chan racingResult, 2)
	results <- racingResult{protocol: "h2", response: &http.Response{Body: winnerBody}}
	results <- racingResult{protocol: "h3", response: &http.Response{Body: loserBody}}
	res, err := pr.waitForRaceWinner(ctx, addr, results, func(string) {})
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	deadline := time.Now().Add(time.Second)
	for loserBody.closed.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if loserBody.closed.Load() != 1 {
		t.Fatal("losing response body left open")
	}
	if winnerBody.closed.Load() != 0 {
		t.Fatal("winning response body closed before caller reads it")
	}
}

func TestRaceWinnerStaysLiveAndCancelsLoser(t *testing.T) {
	for _, winner := range []string{"h2", "h3"} {
		t.Run(winner, func(t *testing.T) {
			addr := "fixture:443"
			started := make(chan struct{}, 2)
			loserCanceled := make(chan struct{})
			var winnerContext context.Context
			var calls atomic.Int32
			var requestsMu sync.Mutex
			requests := make(map[*http.Request]bool)
			winnerBody := &observedRaceBody{Reader: strings.NewReader("stream remains live")}
			loserBody := &observedRaceBody{Reader: strings.NewReader("discard")}
			factory := func(protocol string) http.RoundTripper {
				return roundTripFunc(func(req *http.Request) (*http.Response, error) {
					calls.Add(1)
					requestsMu.Lock()
					requests[req] = true
					requestsMu.Unlock()
					req.Header.Set("Candidate", protocol)
					if protocol != winner {
						started <- struct{}{}
						<-req.Context().Done()
						close(loserCanceled)
						// Simulate headers racing cancellation: cleanup still owns it.
						return &http.Response{StatusCode: 200, Body: loserBody}, nil
					}
					select {
					case <-started:
					case <-req.Context().Done():
						return nil, req.Context().Err()
					}
					winnerContext = req.Context()
					return &http.Response{StatusCode: 200, Body: winnerBody}, nil
				})
			}
			transports := map[string]http.RoundTripper{addr: factory("h2"), addr + ":h3": factory("h3")}
			pr := testRacer(transports)
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			req, _ := http.NewRequestWithContext(ctx, "GET", "https://"+addr, nil)
			req.Header.Set("Original", "unchanged")
			res, err := pr.race(req, addr, func(*http.Request, string) error {
				t.Error("cached transports should be reused")
				return errors.New("unexpected dial")
			})
			if err != nil {
				t.Fatal(err)
			}
			defer res.Body.Close()
			if winnerContext.Err() != nil {
				t.Fatal("winning response canceled at header return")
			}
			select {
			case <-loserCanceled:
			case <-ctx.Done():
				t.Fatal("losing request was not canceled")
			}
			deadline := time.Now().Add(time.Second)
			for loserBody.closed.Load() == 0 && time.Now().Before(deadline) {
				time.Sleep(time.Millisecond)
			}
			if loserBody.closed.Load() != 1 {
				t.Fatal("late losing response left open")
			}
			requestsMu.Lock()
			count := len(requests)
			requestsMu.Unlock()
			if count != 2 || req.Header.Get("Candidate") != "" || calls.Load() != 2 {
				t.Fatal("candidates shared or mutated caller request")
			}
			if _, ok := transports[addr+":h3"].(roundTripFunc); !ok {
				t.Fatal("race replaced the existing H3 transport")
			}
			if pr.protocolCache[addr] != winner {
				t.Fatal("winner not cached")
			}
			data, err := io.ReadAll(res.Body)
			if err != nil || string(data) != "stream remains live" {
				t.Fatalf("body=%q err=%v", data, err)
			}
			res.Body.Close()
			if winnerContext.Err() == nil || winnerBody.closed.Load() != 1 {
				t.Fatal("winner EOF/Close did not release context/body exactly once")
			}
		})
	}
}

func TestRaceCancellationStopsDelayedCandidate(t *testing.T) {
	addr := "fixture:443"
	started := make(chan struct{})
	stopped := make(chan struct{})
	var h2Calls atomic.Int32
	pr := testRacer(map[string]http.RoundTripper{
		addr: roundTripFunc(func(*http.Request) (*http.Response, error) {
			h2Calls.Add(1)
			return nil, errors.New("unexpected H2 request")
		}),
		addr + ":h3": roundTripFunc(func(req *http.Request) (*http.Response, error) {
			close(started)
			<-req.Context().Done()
			close(stopped)
			return nil, req.Context().Err()
		}),
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", "https://"+addr, nil)
	done := make(chan error, 1)
	go func() { _, err := pr.race(req, addr, nil); done <- err }()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("H3 did not start")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("racing ignored caller cancellation")
	}
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("H3 attempt remained running")
	}
	time.Sleep(350 * time.Millisecond)
	if h2Calls.Load() != 0 {
		t.Fatal("delayed H2 request ran after cancellation")
	}
}

func TestCachedRacingFailureIsNotRetried(t *testing.T) {
	addr := "fixture:443"
	failure := errors.New("network failure")
	var calls atomic.Int32
	pr := testRacer(map[string]http.RoundTripper{addr: roundTripFunc(func(*http.Request) (*http.Response, error) { calls.Add(1); return nil, failure })})
	pr.protocolCache[addr] = "h2"
	req, _ := http.NewRequest("GET", "https://"+addr, nil)
	_, err := pr.race(req, addr, nil)
	if !errors.Is(err, failure) || calls.Load() != 1 {
		t.Fatalf("err=%v calls=%d", err, calls.Load())
	}
	if _, ok := pr.protocolCache[addr]; ok {
		t.Fatal("failed cached protocol not cleared")
	}
}

func TestCanceledRaceReleasesNegotiatedConnection(t *testing.T) {
	s := httptest.NewTLSServer(stdhttp.HandlerFunc(func(w stdhttp.ResponseWriter, r *stdhttp.Request) {}))
	defer s.Close()
	client, err := NewHttpClient(NewNoopLogger(), WithInsecureSkipVerify(), WithDisableHttp3())
	if err != nil {
		t.Fatal(err)
	}
	rt := client.(*httpClient).Transport.(*roundTripper)
	defer func() {
		rt.CloseIdleConnections()
		rt.Lock()
		defer rt.Unlock()
		for _, conn := range rt.cachedConnections {
			conn.Close()
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", s.URL, nil)
	addr := rt.getDialTLSAddr(req)
	pr := testRacer(rt.cachedTransports)
	pr.cachedTransportsLck = &rt.cachedTransportsLck
	negotiated := make(chan struct{})
	rt.cachedTransports[addr+":h3"] = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		select {
		case <-negotiated:
		case <-r.Context().Done():
			return nil, r.Context().Err()
		}
		return &http.Response{StatusCode: 200, Body: http.NoBody}, nil
	})
	response, err := pr.startRace(req, addr, func(r *http.Request, a string) error {
		err := rt.getTransport(r, a)
		close(negotiated)
		<-r.Context().Done()
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	deadline := time.Now().Add(500 * time.Millisecond)
	for {
		rt.Lock()
		remaining := len(rt.cachedConnections)
		rt.Unlock()
		if remaining == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("canceled loser left negotiated connection open")
		}
		time.Sleep(time.Millisecond)
	}
}

func TestCloseIdleConnectionsReleasesNegotiatedConnection(t *testing.T) {
	s := httptest.NewTLSServer(nil)
	defer s.Close()
	client, err := NewHttpClient(NewNoopLogger(), WithInsecureSkipVerify(), WithDisableHttp3())
	if err != nil {
		t.Fatal(err)
	}
	rt := client.(*httpClient).Transport.(*roundTripper)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", s.URL, nil)
	addr := rt.getDialTLSAddr(req)
	rt.cachedTransportsLck.Lock()
	err = rt.getTransport(req, addr)
	rt.cachedTransportsLck.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		rt.Lock()
		defer rt.Unlock()
		for _, c := range rt.cachedConnections {
			c.Close()
		}
	}()
	rt.CloseIdleConnections()
	rt.Lock()
	defer rt.Unlock()
	if len(rt.cachedConnections) != 0 {
		t.Fatal("CloseIdleConnections left unused negotiated connection open")
	}
}

func TestCanceledCandidateDoesNotDispatchAfterNegotiation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	addr := "fixture:443"
	req, _ := http.NewRequestWithContext(ctx, "GET", "https://"+addr, nil)
	var calls atomic.Int32
	pr := testRacer(make(map[string]http.RoundTripper))
	results := make(chan racingResult, 1)
	pr.attemptProtocol(req, addr, "h2", 0, func(*http.Request, string) error {
		pr.cachedTransports[addr] = roundTripFunc(func(*http.Request) (*http.Response, error) {
			calls.Add(1)
			return &http.Response{StatusCode: 200, Body: http.NoBody}, nil
		})
		cancel() // A competing protocol won while TCP/TLS was negotiating.
		return nil
	}, results, cancel)
	result := <-results
	defer closeRacingResult(result)
	if calls.Load() != 0 || !errors.Is(result.err, context.Canceled) {
		t.Fatalf("canceled candidate dispatched: calls=%d error=%v", calls.Load(), result.err)
	}
}
