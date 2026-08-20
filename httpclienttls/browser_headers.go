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
	mode = BrowserMode(strings.ToLower(strings.TrimSpace(string(mode))))
	if mode == "" {
		mode = BrowserModeIdentity
	}
	if mode == BrowserModeOff {
		if !headerExists(headers, "user-agent") {
			headers["User-Agent"] = []string{""}
		}
		return nil
	}
	switch mode {
	case BrowserModeIdentity, BrowserModeNavigation, BrowserModeFetch, BrowserModeJSON, BrowserModeScript:
	default:
		return fmt.Errorf("unsupported browser mode %q", mode)
	}

	setHeaderDefault(headers, "user-agent", profile.UserAgent)
	setHeaderDefault(headers, "accept-language", profile.AcceptLanguage)
	setHeaderDefault(headers, "accept-encoding", profile.AcceptEncoding)
	if profile.SecCHUA != "" && clientHintsAllowed(target) {
		setHeaderDefault(headers, "sec-ch-ua", profile.SecCHUA)
		setHeaderDefault(headers, "sec-ch-ua-mobile", boolClientHint(profile.Mobile))
		setHeaderDefault(headers, "sec-ch-ua-platform", `"`+profile.Platform+`"`)
	}

	switch mode {
	case BrowserModeNavigation:
		setHeaderDefault(headers, "accept", "text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,image/apng,*/*;q=0.8,application/signed-exchange;v=b3;q=0.7")
		setHeaderDefault(headers, "upgrade-insecure-requests", "1")
		setHeaderDefault(headers, "sec-fetch-mode", "navigate")
		setHeaderDefault(headers, "sec-fetch-user", "?1")
		setHeaderDefault(headers, "sec-fetch-dest", "document")
		setHeaderDefault(headers, "sec-fetch-site", fetchSite(headers.Get("referer"), target, true))
		setHeaderDefault(headers, "priority", "u=0, i")
	case BrowserModeFetch, BrowserModeJSON:
		if mode == BrowserModeJSON {
			setHeaderDefault(headers, "accept", "application/json")
		} else {
			setHeaderDefault(headers, "accept", "*/*")
		}
		setHeaderDefault(headers, "sec-fetch-mode", "cors")
		setHeaderDefault(headers, "sec-fetch-dest", "empty")
		setHeaderDefault(headers, "sec-fetch-site", fetchSite(requestSource(headers), target, false))
		setHeaderDefault(headers, "priority", "u=1, i")
	case BrowserModeScript:
		setHeaderDefault(headers, "accept", "*/*")
		setHeaderDefault(headers, "sec-fetch-mode", "no-cors")
		setHeaderDefault(headers, "sec-fetch-dest", "script")
		setHeaderDefault(headers, "sec-fetch-site", fetchSite(headers.Get("referer"), target, false))
	}
	return nil
}

func headerExists(headers fhttp.Header, key string) bool {
	for existing := range headers {
		if strings.EqualFold(existing, key) {
			return true
		}
	}
	return false
}

func setHeaderDefault(headers fhttp.Header, key, value string) {
	if value != "" && headers.Get(key) == "" {
		headers.Set(key, value)
	}
}

func requestSource(headers fhttp.Header) string {
	if value := strings.TrimSpace(headers.Get("origin")); value != "" {
		return value
	}
	return strings.TrimSpace(headers.Get("referer"))
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
	if strings.EqualFold(left.Scheme, right.Scheme) && strings.EqualFold(left.Host, right.Host) {
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
	host := strings.ToLower(parsed.Hostname())
	return host == "localhost" || net.ParseIP(host) != nil && net.ParseIP(host).IsLoopback()
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
	mode = BrowserMode(strings.ToLower(strings.TrimSpace(string(mode))))
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

func updateRedirectBrowserHeaders(headers fhttp.Header, mode BrowserMode, nextURL string) {
	mode = BrowserMode(strings.ToLower(strings.TrimSpace(string(mode))))
	if mode == "" {
		mode = BrowserModeIdentity
	}
	switch mode {
	case BrowserModeNavigation:
		headers.Set("sec-fetch-site", fetchSite(headers.Get("referer"), nextURL, true))
	case BrowserModeFetch, BrowserModeJSON:
		if value := fetchSite(requestSource(headers), nextURL, false); value != "" {
			headers.Set("sec-fetch-site", value)
		}
	case BrowserModeScript:
		if value := fetchSite(headers.Get("referer"), nextURL, false); value != "" {
			headers.Set("sec-fetch-site", value)
		}
	}
}
