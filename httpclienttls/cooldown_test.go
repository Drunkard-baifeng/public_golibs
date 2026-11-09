package httpclienttls

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestRetryAfterParsingBoundsAndDates(t *testing.T) {
	now := time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		raw   string
		want  time.Duration
		valid bool
	}{
		{"120", 2 * time.Minute, true}, {" 15 ", 15 * time.Second, true},
		{"0001", time.Second, true}, {"000", 0, true},
		{"999999999999999999999999999", time.Hour, true},
		{"9223372036854775807", time.Hour, true},
		{"Tue, 27 Oct 2026 12:00:45 GMT", 45 * time.Second, true},
		{"Tue, 27 Oct 2026 10:00:00 GMT", 0, true},
		{"Tue, 27 Oct 2026 23:00:00 GMT", time.Hour, true},
		{"", 0, false}, {"-1", 0, false}, {"+1", 0, false}, {"1.5", 0, false},
		{"1, 2", 0, false}, {"later", 0, false},
	} {
		got, valid := retryAfterDelay(tc.raw, now, time.Hour)
		if got != tc.want || valid != tc.valid {
			t.Errorf("Retry-After %q=%v,%t want=%v,%t", tc.raw, got, valid, tc.want, tc.valid)
		}
	}
	if got, valid := retryAfterDelay("1", now, 50*time.Millisecond); !valid || got != 50*time.Millisecond {
		t.Errorf("subsecond cap overflowed: %v %t", got, valid)
	}
}

func TestCooldownValidationPrecedesRecoverableTransportState(t *testing.T) {
	for _, raw := range []string{
		`{"DefaultDuration":-1}`, `{"MaxDuration":-1}`, `{"MaxOrigins":-1}`,
		`{"DefaultDuration":200,"MaxDuration":100}`, `{"StatusCodes":[]}`,
		`{"StatusCodes":[200]}`, `{"StatusCodes":[302]}`, `{"StatusCodes":[600]}`,
	} {
		c := NewWithConfig(identityConfig(t, `{"Profile":"chrome_146","Cooldown":`+raw+`}`))
		if c.ConfigError() == nil {
			t.Errorf("invalid cooldown accepted: %s", raw)
		}
		c.ClearProxy()
		if c.ConfigError() == nil {
			t.Errorf("ClearProxy erased invalid cooldown: %s", raw)
		}
		c.Close()
	}
}

func TestCooldownSelectedStatusesAndExplicitZero(t *testing.T) {
	for _, tc := range []struct {
		name    string
		status  int
		headers []string
		config  *CooldownConfig
		blocked bool
	}{
		{"disabled", 429, []string{"60"}, nil, false},
		{"429 without retry after", 429, nil, &CooldownConfig{}, true},
		{"503 without retry after", 503, nil, &CooldownConfig{}, true},
		{"403 default", 403, []string{"60"}, &CooldownConfig{}, false},
		{"403 explicit", 403, []string{"60"}, &CooldownConfig{StatusCodes: []int{403}}, true},
		{"success", 200, []string{"60"}, &CooldownConfig{}, false},
		{"zero", 429, []string{"0"}, &CooldownConfig{}, false},
		{"past date", 429, []string{"Tue, 01 Jan 2019 00:00:00 GMT"}, &CooldownConfig{}, false},
		{"invalid", 429, []string{"junk"}, &CooldownConfig{}, true},
		{"duplicate", 429, []string{"0", "5"}, &CooldownConfig{}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var hits atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				hits.Add(1)
				for _, value := range tc.headers {
					w.Header().Add("Retry-After", value)
				}
				w.WriteHeader(tc.status)
			}))
			defer server.Close()
			client, err := NewClient(Config{Profile: "chrome_146", Cooldown: tc.config})
			if err != nil {
				t.Fatal(err)
			}
			defer client.Close()
			if _, err := client.Get(server.URL, nil); err != nil {
				t.Fatal(err)
			}
			_, err = client.Get(server.URL, nil)
			if errors.Is(err, ErrCooldown) != tc.blocked {
				t.Errorf("cooldown=%v want blocked=%t", err, tc.blocked)
			}
			wantHits := int32(2)
			if tc.blocked {
				wantHits = 1
			}
			if hits.Load() != wantHits {
				t.Errorf("requests=%d want=%d", hits.Load(), wantHits)
			}
		})
	}
}

func TestCooldownOriginIsolationRedirectAndBodyCleanup(t *testing.T) {
	var hits atomic.Int32
	limited := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Header().Set("Retry-After", "60")
		w.WriteHeader(429)
	}))
	defer limited.Close()
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/redirect" {
			http.Redirect(w, r, limited.URL, http.StatusFound)
			return
		}
		fmt.Fprint(w, "other origin")
	}))
	defer other.Close()
	client, err := NewClient(Config{Profile: "chrome_146", Cooldown: &CooldownConfig{}})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	if _, err := client.Get(limited.URL, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Get(other.URL, nil); err != nil {
		t.Fatalf("cooldown leaked to another port: %v", err)
	}
	if _, err := client.Get(other.URL+"/redirect", nil); !errors.Is(err, ErrCooldown) {
		t.Fatalf("redirect ignored cooldown: %v", err)
	}
	if hits.Load() != 1 {
		t.Errorf("blocked redirect still reached target %d times", hits.Load())
	}
	body := &observedUpload{ReadCloser: io.NopCloser(strings.NewReader("payload")), closed: make(chan struct{})}
	_, err = client.Post(limited.URL+"/path?secret=value", body, nil)
	var blocked *CooldownError
	if !errors.As(err, &blocked) || blocked.StatusCode != 429 || !blocked.Until.After(time.Now()) {
		t.Fatalf("missing typed cooldown metadata: %v", err)
	}
	if strings.Contains(err.Error(), "secret") || strings.Contains(err.Error(), "path") {
		t.Errorf("URL data in cooldown error: %v", err)
	}
	select {
	case <-body.closed:
	default:
		t.Error("blocked upload body left open")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := client.GetContext(ctx, limited.URL, nil); !errors.Is(err, context.Canceled) {
		t.Errorf("cancelled context replaced by cooldown: %v", err)
	}
	// Clearing cookies or rebuilding the same route should not discard backoff.
	client.ClearCookies().ClearProxy()
	if _, err := client.Get(limited.URL, nil); !errors.Is(err, ErrCooldown) {
		t.Errorf("state reset dropped cooldown: %v", err)
	}
	independent := streamClient(t)
	if _, err := independent.Get(limited.URL, nil); err != nil {
		t.Errorf("cooldown leaked to another client: %v", err)
	}
}

func TestCooldownCacheExpiryBoundsAndConcurrentUpdates(t *testing.T) {
	config := &CooldownConfig{MaxOrigins: 2, StatusCodes: []int{429}}
	state, err := newCooldownState(config)
	if err != nil {
		t.Fatal(err)
	}
	// Configuration mutation after construction must not change live policy.
	config.StatusCodes[0] = 500
	state.observe("https://one.example", 429, []string{"10"})
	state.observe("https://two.example", 429, []string{"60"})
	state.observe("https://three.example", 429, []string{"120"})
	if state.snapshot("https://one.example").Origin != "" || state.snapshot("https://two.example").Origin == "" {
		t.Fatal("bounded cache evicted the wrong entry")
	}
	if state.snapshot("https://TWO.example:443/anything").Origin == "" || state.snapshot("http://two.example").Origin != "" {
		t.Error("origin normalization/isolation failed")
	}
	longest := state.snapshot("https://three.example").Until
	var workers sync.WaitGroup
	for i := 0; i < 12; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			state.observe("https://three.example", 429, []string{"1"})
			state.observe("https://three.example", 200, nil)
			state.snapshot("https://three.example")
		}()
	}
	workers.Wait()
	if state.snapshot("https://three.example").Until != longest {
		t.Error("late response shortened pending cooldown")
	}
	// Move the expiry into the past without slowing the suite down.
	state.mu.Lock()
	entry := state.entries["https://three.example:443"]
	entry.Until = time.Now().Add(-time.Second)
	state.entries[entry.Origin] = entry
	state.mu.Unlock()
	if err := state.check("https://three.example"); err != nil {
		t.Errorf("expired cooldown still blocked request: %v", err)
	}
}

func TestCooldownPreventsExpiredLocaleProbe(t *testing.T) {
	var probes atomic.Int32
	geo := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		probes.Add(1)
		fmt.Fprint(w, `{"ip":"203.0.113.1","country_code":"JP","timezone":"Asia/Tokyo"}`)
	}))
	defer geo.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(429) }))
	defer server.Close()
	client, err := NewClient(Config{
		Profile: "chrome_146", Cooldown: &CooldownConfig{},
		ExitLocale: &ExitLocaleConfig{Endpoints: []string{geo.URL}, CacheTTL: time.Nanosecond},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	if _, err := client.Get(server.URL, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Get(server.URL, nil); !errors.Is(err, ErrCooldown) {
		t.Errorf("missing cooldown: %v", err)
	}
	if probes.Load() != 1 {
		t.Errorf("blocked request sent additional locale probe: %d", probes.Load())
	}
}
