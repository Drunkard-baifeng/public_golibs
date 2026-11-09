package tls_client

import (
	"context"
	"errors"
	"io"
	"sync"
	"time"

	http "github.com/bogdanfinn/fhttp"
	"github.com/bogdanfinn/fhttp/http2"
	"github.com/bogdanfinn/tls-client/bandwidth"
	tls "github.com/bogdanfinn/utls"
)

type protocolRacer struct {
	protocolCache   map[string]string
	protocolCacheMu sync.RWMutex

	clientSessionCache  tls.ClientSessionCache
	insecureSkipVerify  bool
	serverNameOverwrite string
	transportOptions    *TransportOptions
	settings            map[http2.SettingID]uint32
	cachedTransports    map[string]http.RoundTripper
	cachedTransportsLck *sync.Mutex
	certificatePinner   CertificatePinner
	badPinHandlerFunc   BadPinHandlerFunc
	bandwidthTracker    bandwidth.BandwidthTracker

	// HTTP/3 specific settings
	http3Settings          map[uint64]uint64
	http3SettingsOrder     []uint64
	http3PriorityParam     uint32
	http3PseudoHeaderOrder []string
	http3SendGreaseFrames  bool
}

func newProtocolRacer(
	clientSessionCache tls.ClientSessionCache,
	insecureSkipVerify bool,
	serverNameOverwrite string,
	transportOptions *TransportOptions,
	settings map[http2.SettingID]uint32,
	cachedTransports map[string]http.RoundTripper,
	cachedTransportsLck *sync.Mutex,
	certificatePinner CertificatePinner,
	badPinHandlerFunc BadPinHandlerFunc,
	bandwidthTracker bandwidth.BandwidthTracker,
	http3Settings map[uint64]uint64,
	http3SettingsOrder []uint64,
	http3PriorityParam uint32,
	http3PseudoHeaderOrder []string,
	http3SendGreaseFrames bool,
) *protocolRacer {
	return &protocolRacer{
		protocolCache:          make(map[string]string),
		clientSessionCache:     clientSessionCache,
		insecureSkipVerify:     insecureSkipVerify,
		serverNameOverwrite:    serverNameOverwrite,
		transportOptions:       transportOptions,
		settings:               settings,
		cachedTransports:       cachedTransports,
		cachedTransportsLck:    cachedTransportsLck,
		certificatePinner:      certificatePinner,
		badPinHandlerFunc:      badPinHandlerFunc,
		bandwidthTracker:       bandwidthTracker,
		http3Settings:          http3Settings,
		http3SettingsOrder:     http3SettingsOrder,
		http3PriorityParam:     http3PriorityParam,
		http3PseudoHeaderOrder: http3PseudoHeaderOrder,
		http3SendGreaseFrames:  http3SendGreaseFrames,
	}
}

// Only bodyless, safe requests may be sent on two transports. In particular,
// replayability alone does not make it safe to duplicate a POST or PUT.
func canRaceRequest(req *http.Request) bool {
	return (req.Method == http.MethodGet || req.Method == http.MethodHead) &&
		(req.Body == nil || req.Body == http.NoBody) && req.ContentLength <= 0 && len(req.Trailer) == 0
}

// race selects the first successful response. Each candidate owns its request
// and cancellation; the winning response remains live until EOF or Close.
func (pr *protocolRacer) race(req *http.Request, addr string, getTransportFunc func(*http.Request, string) error) (*http.Response, error) {
	if !canRaceRequest(req) {
		tr, err := pr.getOrCreateTransport("h2", addr, req, getTransportFunc)
		if err != nil {
			if req.Body != nil {
				req.Body.Close()
			}
			return nil, err
		}
		return tr.RoundTrip(req)
	}
	pr.protocolCacheMu.RLock()
	cachedProtocol, found := pr.protocolCache[addr]
	pr.protocolCacheMu.RUnlock()
	if !found {
		return pr.startRace(req, addr, getTransportFunc)
	}
	transport, err := pr.getOrCreateTransport(cachedProtocol, addr, req, getTransportFunc)
	if err != nil {
		pr.handleCachedProtocolError(err, addr, req)
		return nil, err
	}
	resp, err := transport.RoundTrip(req)
	if err != nil {
		// Do not replay a request that may already have reached the peer.
		// Clear only the hint so the next caller can choose again.
		pr.clearProtocolCache(addr)
	}
	return resp, err
}

func (pr *protocolRacer) getOrCreateTransport(protocol, addr string, req *http.Request, getTransportFunc func(*http.Request, string) error) (http.RoundTripper, error) {
	if err := req.Context().Err(); err != nil {
		return nil, err
	}
	transportKey := pr.getTransportKey(protocol, addr)

	pr.cachedTransportsLck.Lock()
	defer pr.cachedTransportsLck.Unlock()
	if err := req.Context().Err(); err != nil {
		return nil, err
	}

	if transport, exists := pr.cachedTransports[transportKey]; exists {
		return transport, nil
	}

	transport, err := pr.createTransportForProtocol(protocol, addr, req, getTransportFunc)
	if err != nil {
		return nil, err
	}

	pr.cachedTransports[transportKey] = transport
	if err := req.Context().Err(); err != nil {
		return nil, err
	}
	return transport, nil
}

func (pr *protocolRacer) createTransportForProtocol(protocol, addr string, req *http.Request, getTransportFunc func(*http.Request, string) error) (http.RoundTripper, error) {
	if protocol == "h3" {
		return buildHTTP3Transport(pr.getHTTP3Config())
	}

	// For HTTP/2, use the standard transport creation
	transportKey := pr.getTransportKey(protocol, addr)
	if err := getTransportFunc(req, transportKey); err != nil {
		return nil, err
	}

	return pr.cachedTransports[transportKey], nil
}

func (pr *protocolRacer) startRace(req *http.Request, addr string, getTransportFunc func(*http.Request, string) error) (*http.Response, error) {
	resultCh := make(chan racingResult, 2)
	// The selection budget must not become a deadline on the winning stream.
	ctx, stopWait := context.WithTimeout(req.Context(), 10*time.Second)
	defer stopWait()
	h3ctx, cancelH3 := context.WithCancel(req.Context())
	h2ctx, cancelH2 := context.WithCancel(req.Context())
	go pr.attemptProtocol(req.Clone(h3ctx), addr, "h3", 0, getTransportFunc, resultCh, cancelH3)
	go pr.attemptProtocol(req.Clone(h2ctx), addr, "h2", 300*time.Millisecond, getTransportFunc, resultCh, cancelH2)
	return pr.waitForRaceWinner(ctx, addr, resultCh, func(winner string) {
		if winner != "h3" {
			cancelH3()
		}
		if winner != "h2" {
			cancelH2()
		}
	})
}

func (pr *protocolRacer) attemptProtocol(req *http.Request, addr, protocol string, delay time.Duration, getTransportFunc func(*http.Request, string) error, results chan<- racingResult, cancel context.CancelFunc) {
	result := racingResult{protocol: protocol, cancel: cancel}
	defer func() { results <- result }()
	if delay > 0 {
		timer := time.NewTimer(delay)
		defer timer.Stop()
		select {
		case <-timer.C:
		case <-req.Context().Done():
			result.err = req.Context().Err()
			return
		}
	}
	transport, err := pr.getOrCreateTransport(protocol, addr, req, getTransportFunc)
	if err != nil {
		result.err = err
		return
	}
	if err := req.Context().Err(); err != nil {
		result.err = err
		return
	}
	result.response, result.err = transport.RoundTrip(req)
}

func (pr *protocolRacer) waitForRaceWinner(ctx context.Context, addr string, resultCh <-chan racingResult, stopOthers func(string)) (*http.Response, error) {
	var lastErr error
	remaining := 2
	winner := ""
	defer func() {
		stopOthers(winner)
		// A result can race cancellation. Always collect and close losers,
		// including responses already queued when selection finishes.
		if remaining > 0 {
			go func(count int) {
				for ; count > 0; count-- {
					closeRacingResult(<-resultCh)
				}
			}(remaining)
		}
	}()
	for remaining > 0 {
		select {
		case result := <-resultCh:
			remaining--
			if err := ctx.Err(); err != nil {
				closeRacingResult(result)
				return nil, err
			}
			if result.err == nil && result.response != nil {
				winner = result.protocol
				pr.cacheWinningProtocol(addr, result.protocol)
				if result.cancel != nil {
					body := result.response.Body
					if body == nil {
						body = http.NoBody
					}
					result.response.Body = &racingBody{ReadCloser: body, cancel: result.cancel}
				}
				return result.response, nil
			}
			lastErr = result.err
			closeRacingResult(result)
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}

	if lastErr != nil {
		return nil, lastErr
	}
	return nil, errors.New("http3 racing: both protocols failed to connect")
}

func (pr *protocolRacer) getTransportKey(protocol, addr string) string {
	if protocol == "h3" {
		return addr + ":h3"
	}
	return addr
}

func (pr *protocolRacer) clearProtocolCache(addr string) {
	pr.protocolCacheMu.Lock()
	delete(pr.protocolCache, addr)
	pr.protocolCacheMu.Unlock()
}

func (pr *protocolRacer) cacheWinningProtocol(addr, protocol string) {
	pr.protocolCacheMu.Lock()
	pr.protocolCache[addr] = protocol
	pr.protocolCacheMu.Unlock()
}

func (pr *protocolRacer) handleCachedProtocolError(err error, addr string, req *http.Request) {
	if errors.Is(err, ErrBadPinDetected) && pr.badPinHandlerFunc != nil {
		pr.badPinHandlerFunc(req)
	}
	pr.clearProtocolCache(addr)
}

func (pr *protocolRacer) getHTTP3Config() *http3Config {
	return &http3Config{
		clientSessionCache:     pr.clientSessionCache,
		insecureSkipVerify:     pr.insecureSkipVerify,
		serverNameOverwrite:    pr.serverNameOverwrite,
		transportOptions:       pr.transportOptions,
		http3Settings:          pr.http3Settings,
		http3SettingsOrder:     pr.http3SettingsOrder,
		http3PriorityParam:     pr.http3PriorityParam,
		http3PseudoHeaderOrder: pr.http3PseudoHeaderOrder,
		http3SendGreaseFrames:  pr.http3SendGreaseFrames,
	}
}

type racingResult struct {
	protocol string
	response *http.Response
	err      error
	cancel   context.CancelFunc
}

func closeRacingResult(result racingResult) {
	if result.cancel != nil {
		result.cancel()
	}
	if result.response != nil && result.response.Body != nil {
		result.response.Body.Close()
	}
}

type racingBody struct {
	io.ReadCloser
	cancel context.CancelFunc
	once   sync.Once
	err    error
}

func (b *racingBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	if err != nil {
		b.Close()
	}
	return n, err
}

func (b *racingBody) Close() error {
	b.once.Do(func() { b.cancel(); b.err = b.ReadCloser.Close() })
	return b.err
}
