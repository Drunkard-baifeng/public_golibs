package httpclienttls

import (
	"fmt"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	fhttp "github.com/bogdanfinn/fhttp"
	"github.com/dunglas/httpsfv"
	"golang.org/x/net/idna"
)

// ClientHintsConfig opts into HTTPS response-driven UA Client Hints. It requires
// a fully configured Chromium BrowserIdentity and excludes eager high-entropy
// mode. Origin preferences stay in memory and are never shared across clients.
type ClientHintsConfig struct {
	CacheTTL   time.Duration // Defaults to 30 minutes; refreshed by valid Accept-CH.
	MaxOrigins int           // Defaults to 256. Oldest observations are evicted at capacity.
	// Critical retries are limited to one bodyless GET per Do/DoStream call.
	DisableCriticalRetry bool
}

type clientHintEntry struct {
	hints    map[string]bool
	expires  time.Time
	sequence uint64
}

type clientHintCache struct {
	mu       sync.Mutex
	config   ClientHintsConfig
	entries  map[string]clientHintEntry
	sequence uint64
}

func newClientHintCache(config *ClientHintsConfig) (*clientHintCache, error) {
	if config == nil {
		return nil, nil
	}
	value := *config
	if value.CacheTTL < 0 || value.MaxOrigins < 0 {
		return nil, fmt.Errorf("Client Hints cache limits must be non-negative")
	}
	if value.CacheTTL == 0 {
		value.CacheTTL = 30 * time.Minute
	}
	if value.MaxOrigins == 0 {
		value.MaxOrigins = 256
	}
	return &clientHintCache{config: value, entries: make(map[string]clientHintEntry)}, nil
}

// ClearClientHints drops all origin preferences. In-flight responses only update
// their old cache snapshot and do not repopulate the cleared client state.
func (c *Client) ClearClientHints() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.clientHints != nil {
		c.clientHints, _ = newClientHintCache(&c.clientHints.config)
	}
}

// AcceptedClientHints returns a copy of the unexpired HTTPS origin preference.
func (c *Client) AcceptedClientHints(target string) []string {
	c.mu.RLock()
	cache := c.clientHints
	c.mu.RUnlock()
	if cache == nil {
		return nil
	}
	var names []string
	for name := range cache.requested(target) {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func canonicalOrigin(target string) string {
	u, err := url.Parse(target)
	if err != nil || u.Hostname() == "" || (u.Scheme != "https" && u.Scheme != "http") {
		return ""
	}
	host := strings.ToLower(u.Hostname())
	if ip := net.ParseIP(host); ip != nil {
		host = ip.String()
	} else {
		host, err = idna.Lookup.ToASCII(host)
		if err != nil {
			return ""
		}
	}
	port := u.Port()
	if port == "" {
		if u.Scheme == "https" {
			port = "443"
		} else {
			port = "80"
		}
	}
	number, err := strconv.Atoi(port)
	if err != nil || number < 1 || number > 65535 {
		return ""
	}
	return u.Scheme + "://" + net.JoinHostPort(host, strconv.Itoa(number))
}

func hintTokenList(values []string) (map[string]bool, bool) {
	size := 0
	for _, value := range values {
		size += len(value) + 2
		if size > 8192 {
			return nil, false
		}
	}
	list, err := httpsfv.UnmarshalList(values)
	if err != nil {
		return nil, false
	}
	names := make(map[string]bool)
	for _, member := range list {
		item, ok := member.(httpsfv.Item)
		if !ok {
			return nil, false
		}
		token, ok := item.Value.(httpsfv.Token)
		if !ok {
			return nil, false
		}
		name := http.CanonicalHeaderKey(string(token))
		for _, known := range clientHintHeaderNames {
			if known == name {
				names[name] = true
				break
			}
		}
	}
	return names, true
}

func (s *clientHintCache) requested(target string) map[string]bool {
	origin := canonicalOrigin(target)
	if !strings.HasPrefix(origin, "https://") {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	entry, ok := s.entries[origin]
	if !ok {
		return nil
	}
	if !time.Now().Before(entry.expires) {
		delete(s.entries, origin)
		return nil
	}
	return entry.hints // Private, immutable after publication.
}

func (s *clientHintCache) observe(target string, headers fhttp.Header) (map[string]bool, bool) {
	origin := canonicalOrigin(target)
	values, present := headers["Accept-Ch"]
	if !present || !strings.HasPrefix(origin, "https://") {
		return nil, false
	}
	hints, valid := hintTokenList(values)
	if !valid {
		return nil, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(hints) == 0 {
		delete(s.entries, origin)
		return hints, true
	}
	now := time.Now()
	for name, entry := range s.entries {
		if !now.Before(entry.expires) {
			delete(s.entries, name)
		}
	}
	if _, exists := s.entries[origin]; !exists && len(s.entries) >= s.config.MaxOrigins {
		oldest := ""
		sequence := ^uint64(0)
		for name, entry := range s.entries {
			if entry.sequence < sequence {
				oldest, sequence = name, entry.sequence
			}
		}
		delete(s.entries, oldest)
	}
	s.sequence++
	s.entries[origin] = clientHintEntry{hints: hints, expires: now.Add(s.config.CacheTTL), sequence: s.sequence}
	return hints, true
}

func (s *clientHintCache) apply(headers fhttp.Header, profile Profile, explicit map[string]bool, target string, pinned map[string]bool, policy, extraPolicy *ClientHintContext, initiator string) {
	hints := pinned
	if policy != nil {
		hints = policy.hints
	} else if hints == nil {
		hints = s.requested(target)
	}
	values := make(fhttp.Header)
	applyHighEntropyHeaders(values, profile)
	values.Set("Sec-Ch-Ua", profile.SecCHUA)
	values.Set("Sec-Ch-Ua-Mobile", boolClientHint(profile.Mobile))
	values.Set("Sec-Ch-Ua-Platform", strconv.Quote(profile.Platform))
	origin := canonicalOrigin(target)
	for index, key := range clientHintHeaderNames {
		if explicit[key] {
			continue
		}
		headers.Del(key)
		if !policy.allows(key, target) || !extraPolicy.allows(key, target) {
			continue
		}
		if index < 3 {
			if clientHintsAllowed(target) {
				headers.Set(key, values.Get(key))
			}
			continue
		}
		if policy == nil && initiator != "" && canonicalOrigin(initiator) != origin {
			continue
		}
		if hints[key] && strings.HasPrefix(origin, "https://") {
			headers.Set(key, values.Get(key))
		}
	}
}

func (s *clientHintCache) criticalRetry(request *fhttp.Request, response *fhttp.Response, profile Profile, explicit map[string]bool, hints map[string]bool, policy, extraPolicy *ClientHintContext, initiator string) bool {
	if s.config.DisableCriticalRetry || request.Method != "GET" || (request.Body != nil && request.Body != fhttp.NoBody) || response.StatusCode < 200 || response.StatusCode >= 400 {
		return false
	}
	critical, valid := hintTokenList(response.Header.Values("Critical-CH"))
	if !valid {
		return false
	}
	values := request.Header.Clone()
	s.apply(values, profile, explicit, request.URL.String(), hints, policy, extraPolicy, initiator)
	for name := range critical {
		if hints[name] && !explicit[name] && request.Header.Get(name) == "" && values.Get(name) != "" {
			return true
		}
	}
	return false
}
