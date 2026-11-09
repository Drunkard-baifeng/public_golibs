package httpclienttls

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// CooldownConfig opts into per-origin backoff after selected HTTP errors.
// The original response is returned normally. Later requests fail locally
// until expiry; no request is automatically retried or put to sleep.
type CooldownConfig struct {
	// Nil uses 429 and 503. A non-nil list replaces those defaults and must
	// contain only 4xx/5xx statuses; for example, add 403 explicitly if wanted.
	StatusCodes []int
	// DefaultDuration is used for missing/invalid Retry-After (default 1m).
	DefaultDuration time.Duration
	// MaxDuration caps server-provided Retry-After values (default 1h).
	MaxDuration time.Duration
	// MaxOrigins bounds memory (default 256). At capacity the entry with the
	// earliest expiry is evicted. Cooldowns never cross Client instances.
	MaxOrigins int
}

// CooldownInfo is a value snapshot of an active origin cooldown. A zero value
// means no cooldown. It includes neither URL userinfo, path, nor query data.
type CooldownInfo struct {
	Origin     string
	StatusCode int
	Until      time.Time
}

var ErrCooldown = errors.New("httpclienttls: origin is cooling down")

// CooldownError reports a request blocked before network dispatch.
// Use errors.Is(err, ErrCooldown) or errors.As(err, &cooldownError).
type CooldownError struct {
	CooldownInfo
}

func (e *CooldownError) Error() string {
	return fmt.Sprintf("%s: %s after HTTP %d until %s", ErrCooldown, e.Origin, e.StatusCode, e.Until.UTC().Format(time.RFC3339Nano))
}

func (e *CooldownError) Unwrap() error { return ErrCooldown }

type cooldownState struct {
	mu      sync.Mutex
	config  CooldownConfig
	entries map[string]CooldownInfo
}

func newCooldownState(config *CooldownConfig) (*cooldownState, error) {
	if config == nil {
		return nil, nil
	}
	value := *config
	if value.DefaultDuration < 0 || value.MaxDuration < 0 || value.MaxOrigins < 0 {
		return nil, fmt.Errorf("cooldown durations and capacity must be non-negative")
	}
	if value.DefaultDuration == 0 {
		value.DefaultDuration = time.Minute
	}
	if value.MaxDuration == 0 {
		value.MaxDuration = time.Hour
	}
	if value.DefaultDuration > value.MaxDuration {
		return nil, fmt.Errorf("cooldown default duration exceeds maximum duration")
	}
	if value.MaxOrigins == 0 {
		value.MaxOrigins = 256
	}
	if value.StatusCodes == nil {
		value.StatusCodes = []int{http.StatusTooManyRequests, http.StatusServiceUnavailable}
	} else {
		value.StatusCodes = append([]int(nil), value.StatusCodes...)
	}
	if len(value.StatusCodes) == 0 {
		return nil, fmt.Errorf("cooldown status list must not be empty")
	}
	for _, status := range value.StatusCodes {
		if status < 400 || status > 599 {
			return nil, fmt.Errorf("cooldown status must be between 400 and 599")
		}
	}
	return &cooldownState{config: value, entries: make(map[string]CooldownInfo)}, nil
}

// Cooldown returns the current, unexpired backoff for target's exact HTTP(S)
// origin. Invalid URLs and disabled/expired cooldowns yield a zero value.
func (c *Client) Cooldown(target string) CooldownInfo {
	c.mu.RLock()
	state := c.cooldown
	c.mu.RUnlock()
	return state.snapshot(target)
}

func (s *cooldownState) snapshot(target string) CooldownInfo {
	if s == nil {
		return CooldownInfo{}
	}
	origin := canonicalOrigin(target)
	s.mu.Lock()
	defer s.mu.Unlock()
	entry := s.entries[origin]
	if !time.Now().Before(entry.Until) {
		delete(s.entries, origin)
		return CooldownInfo{}
	}
	return entry
}

func (s *cooldownState) check(target string) error {
	if entry := s.snapshot(target); entry.Origin != "" {
		return &CooldownError{CooldownInfo: entry}
	}
	return nil
}

func (s *cooldownState) observe(target string, status int, retryAfter []string) {
	if s == nil {
		return
	}
	tracked := false
	for _, code := range s.config.StatusCodes {
		if code == status {
			tracked = true
			break
		}
	}
	if !tracked {
		return
	}
	origin := canonicalOrigin(target)
	if origin == "" {
		return
	}
	now := time.Now()
	duration := s.config.DefaultDuration
	// Retry-After is not a list. Multiple occurrences are ambiguous.
	if len(retryAfter) == 1 {
		if delay, valid := retryAfterDelay(retryAfter[0], now, s.config.MaxDuration); valid {
			duration = delay
		}
	}
	if duration <= 0 {
		return
	}
	entry := CooldownInfo{Origin: origin, StatusCode: status, Until: now.Add(duration)}
	s.mu.Lock()
	defer s.mu.Unlock()
	for key, existing := range s.entries {
		if !now.Before(existing.Until) {
			delete(s.entries, key)
		}
	}
	if existing, ok := s.entries[origin]; ok {
		// A late shorter response must not shorten another in-flight response's
		// backoff; successful responses also leave existing cooldowns intact.
		if existing.Until.After(entry.Until) {
			return
		}
	} else if len(s.entries) >= s.config.MaxOrigins {
		earliestKey := ""
		var earliest time.Time
		for key, existing := range s.entries {
			if earliestKey == "" || existing.Until.Before(earliest) {
				earliestKey, earliest = key, existing.Until
			}
		}
		delete(s.entries, earliestKey)
	}
	s.entries[origin] = entry
}

// retryAfterDelay accepts RFC 9110 delay-seconds or HTTP-date. Digit overflow
// saturates at the configured bound instead of wrapping to a negative delay.
func retryAfterDelay(raw string, now time.Time, limit time.Duration) (time.Duration, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, false
	}
	digits := true
	for _, ch := range raw {
		if ch < '0' || ch > '9' {
			digits = false
			break
		}
	}
	if digits {
		raw = strings.TrimLeft(raw, "0")
		if raw == "" {
			return 0, true
		}
		seconds, err := strconv.ParseUint(raw, 10, 64)
		if err != nil || seconds > uint64(limit/time.Second) {
			return limit, true
		}
		return time.Duration(seconds) * time.Second, true
	}
	date, err := http.ParseTime(raw)
	if err != nil {
		return 0, false
	}
	delay := date.Sub(now)
	if delay < 0 {
		delay = 0
	}
	if delay > limit {
		delay = limit
	}
	return delay, true
}
