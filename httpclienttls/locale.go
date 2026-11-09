package httpclienttls

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/url"
	"strings"
	"sync"
	"time"
	_ "time/tzdata" // Make IANA timezone validation portable, including on Windows.

	tlsclient "github.com/Drunkard-baifeng/public_golibs/httpclienttls/internal/tlsclient"
	fhttp "github.com/bogdanfinn/fhttp"
)

// ExitLocaleConfig enables lazy exit-IP locale lookup. A nil Config.ExitLocale
// disables lookup. Probes use the same transport/proxy as the business request,
// with only the configured UA and JSON/identity Accept headers; business headers
// and cookies are not inherited. Constructors perform no network I/O.
type ExitLocaleConfig struct {
	// Nil uses https://ipwho.is/ and https://ipapi.co/json/ in that order.
	// Custom endpoints must use HTTPS or trustworthy loopback HTTP.
	Endpoints []string
	// Timeout bounds the entire lookup, including all provider attempts (default 6s).
	Timeout time.Duration
	// CacheTTL caches successful results per client/transport (default 15m).
	CacheTTL time.Duration
	// FailureTTL backs off failed lookups (default 30s). Parent cancellation is not cached.
	FailureTTL time.Duration
}

// LocaleInfo is a value snapshot, not a browser/JS environment. Country-based
// language is a default preference, not proof of the user's language. Timezone
// is an IANA name; compute offsets for the relevant date, not a fixed DST offset.
type LocaleInfo struct {
	IP             string
	Country        string
	City           string
	Timezone       string
	Language       string
	AcceptLanguage string
	DetectedAt     time.Time
	ExpiresAt      time.Time
	// LastError is a sanitized lookup error; fallback does not poison ConfigError.
	LastError string
}

type exitLocaleState struct {
	mu      sync.Mutex
	config  ExitLocaleConfig
	pending chan struct{}
	info    LocaleInfo
	err     error
}

func normalizeExitLocale(config *ExitLocaleConfig) (*ExitLocaleConfig, error) {
	if config == nil {
		return nil, nil
	}
	value := *config
	if value.Timeout < 0 || value.CacheTTL < 0 || value.FailureTTL < 0 {
		return nil, fmt.Errorf("exit locale durations must be non-negative")
	}
	if value.Timeout == 0 {
		value.Timeout = 6 * time.Second
	}
	if value.CacheTTL == 0 {
		value.CacheTTL = 15 * time.Minute
	}
	if value.FailureTTL == 0 {
		value.FailureTTL = 30 * time.Second
	}
	if value.Endpoints == nil {
		value.Endpoints = []string{"https://ipwho.is/", "https://ipapi.co/json/"}
	} else {
		value.Endpoints = append([]string(nil), value.Endpoints...)
	}
	if len(value.Endpoints) == 0 {
		return nil, fmt.Errorf("exit locale endpoints must not be empty")
	}
	for i, endpoint := range value.Endpoints {
		endpoint = strings.TrimSpace(endpoint)
		u, err := url.Parse(endpoint)
		if err != nil || u.Hostname() == "" || u.User != nil || u.Fragment != "" ||
			(u.Scheme != "https" && (u.Scheme != "http" || !clientHintsAllowed(endpoint))) {
			return nil, fmt.Errorf("invalid exit locale endpoint %d: use HTTPS or loopback HTTP without URL credentials or fragments", i+1)
		}
		value.Endpoints[i] = endpoint
	}
	return &value, nil
}

func (s *exitLocaleState) snapshot() LocaleInfo {
	if s == nil {
		return LocaleInfo{}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.info
}

func localeWithDefault(info LocaleInfo, fallback string, explicit bool) LocaleInfo {
	if explicit || info.AcceptLanguage == "" {
		info.AcceptLanguage = fallback
	}
	info.Language = strings.TrimSpace(strings.Split(strings.Split(info.AcceptLanguage, ",")[0], ";")[0])
	return info
}

// Locale returns the latest observation without network I/O. Before lookup or
// after a failed lookup it reports the configured/default language. Explicit
// per-request headers are not reflected in this client-level snapshot.
func (c *Client) Locale() LocaleInfo {
	c.mu.RLock()
	state, fallback, explicit := c.exitLocale, c.profile.AcceptLanguage, c.explicitLanguage
	c.mu.RUnlock()
	return localeWithDefault(state.snapshot(), fallback, explicit)
}

// RefreshLocale bypasses cache expiry and joins an existing lookup if present.
// On failure, ordinary requests keep the configured/default language and route.
// A concurrent proxy change isolates this lookup to the old transport snapshot.
func (c *Client) RefreshLocale(ctx context.Context) (LocaleInfo, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	c.mu.RLock()
	state, inner, ua := c.exitLocale, c.inner, c.userAgent
	fallback, explicit, configErr, closed := c.profile.AcceptLanguage, c.explicitLanguage, c.configErr, c.closed
	c.mu.RUnlock()
	if configErr != nil {
		return LocaleInfo{}, fmt.Errorf("httpclienttls configuration: %w", configErr)
	}
	if closed {
		return LocaleInfo{}, fmt.Errorf("httpclienttls: client is closed")
	}
	if inner == nil {
		return LocaleInfo{}, fmt.Errorf("httpclienttls: client is not initialized")
	}
	if state == nil {
		return localeWithDefault(LocaleInfo{}, fallback, explicit), fmt.Errorf("exit locale lookup is disabled")
	}
	info, err := state.resolve(ctx, inner, ua, true)
	return localeWithDefault(info, fallback, explicit), err
}

func (s *exitLocaleState) resolve(ctx context.Context, inner tlsclient.HttpClient, ua string, force bool) (LocaleInfo, error) {
	for {
		if err := ctx.Err(); err != nil {
			return LocaleInfo{}, err
		}
		s.mu.Lock()
		if pending := s.pending; pending != nil {
			s.mu.Unlock()
			select {
			case <-ctx.Done():
				return LocaleInfo{}, ctx.Err()
			case <-pending:
				force = false
				continue
			}
		}
		if !force && time.Now().Before(s.info.ExpiresAt) {
			info, err := s.info, s.err
			s.mu.Unlock()
			return info, err
		}
		pending := make(chan struct{})
		s.pending = pending
		s.mu.Unlock()
		info, err := lookupExitLocale(ctx, inner, ua, s.config)
		s.mu.Lock()
		if ctx.Err() == nil {
			now, ttl := time.Now(), s.config.CacheTTL
			if err != nil {
				info.LastError = err.Error()
				ttl = s.config.FailureTTL
			} else {
				info.DetectedAt = now
			}
			info.ExpiresAt = now.Add(ttl)
			s.info, s.err = info, err
		} else {
			err = ctx.Err()
		}
		s.pending = nil
		close(pending)
		s.mu.Unlock()
		return info, err
	}
}

func lookupExitLocale(ctx context.Context, inner tlsclient.HttpClient, ua string, config ExitLocaleConfig) (LocaleInfo, error) {
	ctx, cancel := context.WithTimeout(ctx, config.Timeout)
	defer cancel()
	var lastErr error
	for i, endpoint := range config.Endpoints {
		if err := ctx.Err(); err != nil {
			return LocaleInfo{}, err
		}
		deadline, _ := ctx.Deadline()
		// Share the remaining budget so one stalled provider does not starve failover.
		attempt, stop := context.WithTimeout(ctx, time.Until(deadline)/time.Duration(len(config.Endpoints)-i))
		info, err := probeExitLocale(attempt, inner, ua, endpoint)
		stop()
		if err == nil {
			return info, nil
		}
		lastErr = fmt.Errorf("exit locale endpoint %d: %w", i+1, err)
	}
	return LocaleInfo{}, lastErr
}

func probeExitLocale(ctx context.Context, inner tlsclient.HttpClient, ua, endpoint string) (LocaleInfo, error) {
	req, err := fhttp.NewRequestWithContext(ctx, fhttp.MethodGet, endpoint, nil)
	if err != nil {
		return LocaleInfo{}, fmt.Errorf("invalid lookup request")
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Accept-Encoding", "identity")
	req.Header.Set("User-Agent", ua)
	response, err := inner.Do(req)
	if response != nil && response.Body != nil {
		defer response.Body.Close()
	}
	if err != nil {
		if ctx.Err() != nil {
			return LocaleInfo{}, ctx.Err()
		}
		return LocaleInfo{}, fmt.Errorf("lookup transport failed")
	}
	if response == nil || response.Body == nil {
		return LocaleInfo{}, fmt.Errorf("empty lookup response")
	}
	if response.StatusCode != fhttp.StatusOK {
		return LocaleInfo{}, fmt.Errorf("lookup returned HTTP %d", response.StatusCode)
	}
	const maxBody = 64 << 10
	body, err := io.ReadAll(io.LimitReader(response.Body, maxBody+1))
	if err != nil {
		if ctx.Err() != nil {
			return LocaleInfo{}, ctx.Err()
		}
		return LocaleInfo{}, fmt.Errorf("read lookup response failed")
	}
	if len(body) > maxBody {
		return LocaleInfo{}, fmt.Errorf("lookup response exceeds 64 KiB")
	}
	return parseExitLocale(body)
}

func parseExitLocale(body []byte) (LocaleInfo, error) {
	var data struct {
		IP          string          `json:"ip"`
		Country     string          `json:"country"`
		CountryCode string          `json:"country_code"`
		City        string          `json:"city"`
		Timezone    json.RawMessage `json:"timezone"`
		Success     *bool           `json:"success"`
		Error       json.RawMessage `json:"error"`
	}
	if err := json.Unmarshal(body, &data); err != nil {
		return LocaleInfo{}, fmt.Errorf("invalid lookup JSON")
	}
	if data.Success != nil && !*data.Success || len(data.Error) > 0 && string(data.Error) != "false" && string(data.Error) != "null" {
		return LocaleInfo{}, fmt.Errorf("lookup provider reported failure")
	}
	ip := net.ParseIP(strings.TrimSpace(data.IP))
	country := strings.ToUpper(strings.TrimSpace(data.CountryCode))
	if country == "" {
		country = strings.ToUpper(strings.TrimSpace(data.Country))
	}
	if ip == nil || len(country) != 2 || country[0] < 'A' || country[0] > 'Z' || country[1] < 'A' || country[1] > 'Z' {
		return LocaleInfo{}, fmt.Errorf("lookup requires an IP address and two-letter country code")
	}
	var timezone string
	if len(data.Timezone) > 0 && string(data.Timezone) != "null" {
		if err := json.Unmarshal(data.Timezone, &timezone); err != nil {
			var nested struct {
				ID string `json:"id"`
			}
			if err := json.Unmarshal(data.Timezone, &nested); err != nil {
				return LocaleInfo{}, fmt.Errorf("invalid lookup timezone")
			}
			timezone = nested.ID
		}
		timezone = strings.TrimSpace(timezone)
		if timezone != "" {
			if _, err := time.LoadLocation(timezone); err != nil || timezone == "Local" {
				return LocaleInfo{}, fmt.Errorf("lookup timezone must be a valid IANA name")
			}
		}
	}
	return LocaleInfo{IP: ip.String(), Country: country, City: strings.TrimSpace(data.City), Timezone: timezone, AcceptLanguage: countryAcceptLanguage(country)}, nil
}

// Defaults are preferences only; explicit Config.AcceptLanguage and request
// headers always take precedence. Unknown country codes use neutral en-US.
func countryAcceptLanguage(country string) string {
	if country == "HK" {
		return "zh-HK,zh-TW;q=0.9,zh;q=0.8,en-US;q=0.7,en;q=0.6"
	}
	if country == "SG" {
		return "en-SG,en-US;q=0.9,en;q=0.8"
	}
	language := map[string]string{
		"JP": "ja-JP", "CN": "zh-CN", "TW": "zh-TW", "US": "en-US", "GB": "en-GB",
		"DE": "de-DE", "FR": "fr-FR", "NL": "nl-NL", "VN": "vi-VN", "TH": "th-TH",
		"KR": "ko-KR", "AU": "en-AU", "CA": "en-CA", "IE": "en-IE", "NZ": "en-NZ",
		"IN": "en-IN", "ES": "es-ES", "IT": "it-IT", "BR": "pt-BR", "PT": "pt-PT",
		"MX": "es-MX", "ID": "id-ID", "MY": "ms-MY", "PH": "en-PH", "PL": "pl-PL",
	}[country]
	if language == "" {
		language = "en-US"
	}
	base := strings.SplitN(language, "-", 2)[0]
	if base == "en" {
		return language + ",en;q=0.9"
	}
	return language + "," + base + ";q=0.9,en-US;q=0.8,en;q=0.7"
}
