package httpclienttls

import (
	"fmt"
	"net"
	"net/url"
	"strings"

	fhttp "github.com/bogdanfinn/fhttp"
	"golang.org/x/net/publicsuffix"
)

// BrowserMode controls request-context headers layered on top of the selected
// TLS/browser identity. The zero value adds identity headers only.
type BrowserMode string

const (
	BrowserModeIdentity   BrowserMode = "identity"
	BrowserModeNavigation BrowserMode = "navigation"
	BrowserModeFetch      BrowserMode = "fetch"
	BrowserModeJSON       BrowserMode = "json"
	BrowserModeScript     BrowserMode = "script"
	BrowserModeOff        BrowserMode = "off"
)

func applyBrowserHeaders(headers fhttp.Header, profile Profile, mode BrowserMode, target string) error {
	// cloneOptions normalizes the mode before request construction.
	if mode == "" {
		mode = BrowserModeIdentity
	}
	if mode == BrowserModeOff {
		if !headerExists(headers, "User-Agent") {
			headers["User-Agent"] = []string{""}
		}
		return nil
	}
	switch mode {
	case BrowserModeIdentity, BrowserModeNavigation, BrowserModeFetch, BrowserModeJSON, BrowserModeScript:
	default:
		return fmt.Errorf("unsupported browser mode %q", mode)
	}

	setHeaderDefault(headers, "User-Agent", profile.UserAgent)
	setHeaderDefault(headers, "Accept-Language", profile.AcceptLanguage)
	setHeaderDefault(headers, "Accept-Encoding", profile.AcceptEncoding)
	trustworthy := clientHintsAllowed(target)
	if profile.SecCHUA != "" && trustworthy {
		setHeaderDefault(headers, "Sec-Ch-Ua", profile.SecCHUA)
		setHeaderDefault(headers, "Sec-Ch-Ua-Mobile", boolClientHint(profile.Mobile))
		setHeaderDefault(headers, "Sec-Ch-Ua-Platform", `"`+profile.Platform+`"`)
	}

	switch mode {
	case BrowserModeNavigation:
		setHeaderDefault(headers, "Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,image/apng,*/*;q=0.8,application/signed-exchange;v=b3;q=0.7")
		setHeaderDefault(headers, "Upgrade-Insecure-Requests", "1")
		if trustworthy {
			setHeaderDefault(headers, "Sec-Fetch-Mode", "navigate")
			setHeaderDefault(headers, "Sec-Fetch-User", "?1")
			setHeaderDefault(headers, "Sec-Fetch-Dest", "document")
			setHeaderDefault(headers, "Sec-Fetch-Site", fetchSite(headers.Get("Referer"), target, true))
		}
		setHeaderDefault(headers, "Priority", "u=0, i")
	case BrowserModeFetch, BrowserModeJSON:
		if mode == BrowserModeJSON {
			setHeaderDefault(headers, "Accept", "application/json")
		} else {
			setHeaderDefault(headers, "Accept", "*/*")
		}
		if trustworthy {
			setHeaderDefault(headers, "Sec-Fetch-Mode", "cors")
			setHeaderDefault(headers, "Sec-Fetch-Dest", "empty")
			setHeaderDefault(headers, "Sec-Fetch-Site", fetchSite(requestSource(headers), target, false))
		}
		setHeaderDefault(headers, "Priority", "u=1, i")
	case BrowserModeScript:
		setHeaderDefault(headers, "Accept", "*/*")
		if trustworthy {
			setHeaderDefault(headers, "Sec-Fetch-Mode", "no-cors")
			setHeaderDefault(headers, "Sec-Fetch-Dest", "script")
			setHeaderDefault(headers, "Sec-Fetch-Site", fetchSite(headers.Get("Referer"), target, false))
		}
	}
	return nil
}

func headerExists(headers fhttp.Header, key string) bool {
	// Both public inputs and internal header names use canonical MIME casing.
	_, exists := headers[key]
	return exists
}

func setHeaderDefault(headers fhttp.Header, key, value string) {
	if value != "" && !headerExists(headers, key) {
		headers.Set(key, value)
	}
}

func requestSource(headers fhttp.Header) string {
	if value := strings.TrimSpace(headers.Get("Origin")); value != "" {
		return value
	}
	return strings.TrimSpace(headers.Get("Referer"))
}

func fetchSite(source, target string, navigation bool) string {
	if strings.TrimSpace(source) == "" {
		if navigation {
			return "none"
		}
		return ""
	}
	left, leftErr := url.Parse(source)
	right, rightErr := url.Parse(target)
	if leftErr != nil || rightErr != nil || left.Hostname() == "" || right.Hostname() == "" {
		return "cross-site"
	}
	if strings.EqualFold(left.Scheme, right.Scheme) && strings.EqualFold(left.Hostname(), right.Hostname()) && effectivePort(left) == effectivePort(right) {
		return "same-origin"
	}
	if strings.EqualFold(left.Scheme, right.Scheme) && registrableDomain(left.Hostname()) == registrableDomain(right.Hostname()) {
		return "same-site"
	}
	return "cross-site"
}

func registrableDomain(host string) string {
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	if net.ParseIP(host) != nil {
		return host
	}
	domain, err := publicsuffix.EffectiveTLDPlusOne(host)
	if err != nil {
		return host
	}
	return domain
}

func clientHintsAllowed(target string) bool {
	parsed, err := url.Parse(target)
	if err != nil {
		return false
	}
	if strings.EqualFold(parsed.Scheme, "https") {
		return true
	}
	if !strings.EqualFold(parsed.Scheme, "http") {
		return false
	}
	host := strings.ToLower(strings.TrimSuffix(parsed.Hostname(), "."))
	return host == "localhost" || strings.HasSuffix(host, ".localhost") || net.ParseIP(host) != nil && net.ParseIP(host).IsLoopback()
}

func boolClientHint(value bool) string {
	if value {
		return "?1"
	}
	return "?0"
}

func normalizeHeaderOrder(order []string) []string {
	result := make([]string, 0, len(order))
	seen := make(map[string]struct{}, len(order))
	for _, key := range order {
		key = strings.ToLower(strings.TrimSpace(key))
		if key == "" {
			continue
		}
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, key)
	}
	return result
}

func profileHeaderOrder(profile Profile, mode BrowserMode, redirected bool) []string {
	if profile.Browser == "chrome" || profile.Browser == "brave" {
		switch mode {
		case BrowserModeNavigation:
			if redirected {
				return append([]string(nil), chromiumRedirectHeaderOrder...)
			}
		case BrowserModeFetch, BrowserModeJSON:
			return append([]string(nil), chromiumFetchHeaderOrder...)
		case BrowserModeScript:
			return append([]string(nil), chromiumScriptHeaderOrder...)
		}
	}
	return append([]string(nil), profile.headerOrder...)
}

var browserContextHeaders = []string{
	"Sec-Ch-Ua", "Sec-Ch-Ua-Mobile", "Sec-Ch-Ua-Platform",
	"Sec-Fetch-Site", "Sec-Fetch-Mode", "Sec-Fetch-Dest", "Sec-Fetch-User",
}

// Keep the original initiator and the least-trusted hop across redirects.
// Generated headers may change with the destination; explicit overrides stay.
type browserHeaderState struct {
	source   string
	site     string
	explicit map[string]bool
}

func newBrowserHeaderState(headers fhttp.Header, mode BrowserMode, target string) *browserHeaderState {
	state := &browserHeaderState{}
	for _, key := range browserContextHeaders {
		if headerExists(headers, key) {
			if state.explicit == nil {
				state.explicit = make(map[string]bool)
			}
			state.explicit[key] = true
		}
	}
	switch mode {
	case BrowserModeNavigation, BrowserModeScript:
		state.source = headers.Get("Referer")
		state.site = fetchSite(state.source, target, mode == BrowserModeNavigation)
	case BrowserModeFetch, BrowserModeJSON:
		state.source = requestSource(headers)
		state.site = fetchSite(state.source, target, false)
	}
	return state
}

func (state *browserHeaderState) redirect(headers fhttp.Header, profile Profile, mode BrowserMode, target string) {
	switch mode {
	case BrowserModeNavigation, BrowserModeScript, BrowserModeFetch, BrowserModeJSON:
		next := fetchSite(state.source, target, mode == BrowserModeNavigation)
		if state.site != "none" && state.site != "cross-site" {
			if next == "cross-site" || state.site != "same-site" {
				state.site = next
			}
		}
	}
	for _, key := range browserContextHeaders {
		if !state.explicit[key] {
			headers.Del(key)
		}
	}
	// The mode was validated before the first request.
	_ = applyBrowserHeaders(headers, profile, mode, target)
	if !state.explicit["Sec-Fetch-Site"] {
		headers.Del("Sec-Fetch-Site")
		if state.site != "" && clientHintsAllowed(target) {
			headers.Set("Sec-Fetch-Site", state.site)
		}
	}
}
