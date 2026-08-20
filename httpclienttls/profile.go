package httpclienttls

import (
	"crypto/rand"
	"fmt"
	"math/big"
	"sort"
	"strings"

	"github.com/bogdanfinn/tls-client/profiles"
)

// Profile describes a TLS/browser fingerprint and its matching User-Agent.
type Profile struct {
	Name                    string
	Browser                 string
	UserAgent               string
	Platform                string
	Mobile                  bool
	SecCHUA                 string
	AcceptLanguage          string
	AcceptEncoding          string
	RandomTLSExtensionOrder bool
	SessionResumption       bool

	client                    profiles.ClientProfile
	resumptionClient          profiles.ClientProfile
	supportsSessionResumption bool
	headerOrder               []string
}

var chromiumHeaderOrder = []string{
	"sec-ch-ua", "sec-ch-ua-mobile", "sec-ch-ua-platform", "upgrade-insecure-requests",
	"user-agent", "accept", "sec-fetch-site", "sec-fetch-mode", "sec-fetch-user",
	"sec-fetch-dest", "origin", "referer", "accept-encoding", "accept-language",
	"cookie", "priority", "content-type", "content-length",
}

var chromiumRedirectHeaderOrder = []string{
	"upgrade-insecure-requests", "user-agent", "accept", "sec-fetch-site", "sec-fetch-mode",
	"sec-fetch-user", "sec-fetch-dest", "sec-ch-ua", "sec-ch-ua-mobile", "sec-ch-ua-platform",
	"referer", "accept-encoding", "accept-language", "cookie", "priority",
}

var chromiumFetchHeaderOrder = []string{
	"sec-ch-ua-platform", "user-agent", "sec-ch-ua", "content-type", "sec-ch-ua-mobile",
	"accept", "origin", "sec-fetch-site", "sec-fetch-mode", "sec-fetch-dest", "referer",
	"accept-encoding", "accept-language", "cookie", "priority", "content-length",
}

var chromiumScriptHeaderOrder = []string{
	"sec-ch-ua-platform", "user-agent", "sec-ch-ua", "sec-ch-ua-mobile", "accept",
	"sec-fetch-site", "sec-fetch-mode", "sec-fetch-dest", "referer", "accept-encoding",
	"accept-language", "cookie", "priority",
}

var firefoxHeaderOrder = []string{
	"user-agent", "accept", "accept-language", "accept-encoding", "content-type",
	"origin", "referer", "cookie", "sec-fetch-dest", "sec-fetch-mode",
	"sec-fetch-site", "sec-fetch-user", "priority", "content-length",
}

var safariHeaderOrder = []string{
	"accept", "sec-fetch-site", "cookie", "sec-fetch-dest", "user-agent",
	"accept-language", "sec-fetch-mode", "referer", "accept-encoding", "priority",
	"content-type", "origin", "content-length",
}

var profileRegistry = map[string]Profile{
	"brave_146": {
		Name: "brave_146", Browser: "brave", Platform: "Windows",
		UserAgent:      "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/146.0.0.0 Safari/537.36",
		SecCHUA:        `"Chromium";v="146", "Not=A?Brand";v="24"`,
		AcceptLanguage: "en-US,en;q=0.9", AcceptEncoding: "gzip, deflate, br, zstd",
		RandomTLSExtensionOrder: true,
		client:                  profiles.Brave_146, resumptionClient: profiles.Brave_146_PSK,
		supportsSessionResumption: true, headerOrder: chromiumHeaderOrder,
	},
	"chrome_144": {
		Name: "chrome_144", Browser: "chrome", Platform: "Windows",
		UserAgent:      "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/144.0.0.0 Safari/537.36",
		SecCHUA:        `"Google Chrome";v="144", "Chromium";v="144", "Not=A?Brand";v="24"`,
		AcceptLanguage: "en-US,en;q=0.9", AcceptEncoding: "gzip, deflate, br, zstd",
		RandomTLSExtensionOrder: true,
		client:                  profiles.Chrome_144, resumptionClient: profiles.Chrome_144_PSK,
		supportsSessionResumption: true, headerOrder: chromiumHeaderOrder,
	},
	"chrome_146": {
		Name: "chrome_146", Browser: "chrome", Platform: "Windows",
		UserAgent:      "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/146.0.0.0 Safari/537.36",
		SecCHUA:        `"Google Chrome";v="146", "Chromium";v="146", "Not=A?Brand";v="24"`,
		AcceptLanguage: "en-US,en;q=0.9", AcceptEncoding: "gzip, deflate, br, zstd",
		RandomTLSExtensionOrder: true,
		client:                  profiles.Chrome_146, resumptionClient: profiles.Chrome_146_PSK,
		supportsSessionResumption: true, headerOrder: chromiumHeaderOrder,
	},
	"firefox_147": {
		Name: "firefox_147", Browser: "firefox", Platform: "Windows",
		UserAgent:      "Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:147.0) Gecko/20100101 Firefox/147.0",
		AcceptLanguage: "en-US,en;q=0.5", AcceptEncoding: "gzip, deflate, br, zstd",
		client: profiles.Firefox_147, resumptionClient: profiles.Firefox_147_PSK,
		supportsSessionResumption: true, headerOrder: firefoxHeaderOrder,
	},
	"firefox_148": {
		Name: "firefox_148", Browser: "firefox", Platform: "Windows",
		UserAgent:      "Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:148.0) Gecko/20100101 Firefox/148.0",
		AcceptLanguage: "en-US,en;q=0.5", AcceptEncoding: "gzip, deflate, br, zstd",
		client: profiles.Firefox_148, headerOrder: firefoxHeaderOrder,
	},
	"safari_ios_18_5": {
		Name: "safari_ios_18_5", Browser: "safari", Platform: "iOS", Mobile: true,
		UserAgent:      "Mozilla/5.0 (iPhone; CPU iPhone OS 18_5 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.5 Mobile/15E148 Safari/604.1",
		AcceptLanguage: "en-US,en;q=0.9", AcceptEncoding: "gzip, deflate, br",
		client: profiles.Safari_IOS_18_5, headerOrder: safariHeaderOrder,
	},
	"safari_ios_26_0": {
		Name: "safari_ios_26_0", Browser: "safari", Platform: "iOS", Mobile: true,
		UserAgent:      "Mozilla/5.0 (iPhone; CPU iPhone OS 26_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/26.0 Mobile/15E148 Safari/604.1",
		AcceptLanguage: "en-US,en;q=0.9", AcceptEncoding: "gzip, deflate, br",
		client: profiles.Safari_IOS_26_0, headerOrder: safariHeaderOrder,
	},
}

var defaultRandomProfiles = []string{
	"brave_146",
	"chrome_144",
	"chrome_146",
	"firefox_147",
	"firefox_148",
	"safari_ios_18_5",
	"safari_ios_26_0",
}

// AvailableProfiles returns the built-in profile names in stable order.
func AvailableProfiles() []string {
	names := make([]string, 0, len(profileRegistry))
	for name := range profileRegistry {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func publicProfile(profile Profile) Profile {
	return Profile{
		Name: profile.Name, Browser: profile.Browser, UserAgent: profile.UserAgent,
		Platform: profile.Platform, Mobile: profile.Mobile, SecCHUA: profile.SecCHUA,
		AcceptLanguage: profile.AcceptLanguage, AcceptEncoding: profile.AcceptEncoding,
		RandomTLSExtensionOrder: profile.RandomTLSExtensionOrder,
		SessionResumption:       profile.SessionResumption,
	}
}

func selectProfile(name string, pool []string) (Profile, error) {
	name = normalizeProfileName(name)
	if name != "" {
		profile, ok := profileRegistry[name]
		if !ok {
			return Profile{}, fmt.Errorf("unsupported TLS profile %q", name)
		}
		return profile, nil
	}

	if len(pool) == 0 {
		pool = defaultRandomProfiles
	}
	candidates := make([]Profile, 0, len(pool))
	seen := make(map[string]struct{}, len(pool))
	for _, candidate := range pool {
		candidate = normalizeProfileName(candidate)
		profile, ok := profileRegistry[candidate]
		if !ok {
			return Profile{}, fmt.Errorf("unsupported TLS profile %q", candidate)
		}
		if _, exists := seen[candidate]; exists {
			continue
		}
		seen[candidate] = struct{}{}
		candidates = append(candidates, profile)
	}
	if len(candidates) == 0 {
		return Profile{}, fmt.Errorf("TLS profile pool is empty")
	}

	index, err := rand.Int(rand.Reader, big.NewInt(int64(len(candidates))))
	if err != nil {
		return Profile{}, fmt.Errorf("select TLS profile: %w", err)
	}
	return candidates[index.Int64()], nil
}

func normalizeProfileName(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	switch name {
	case "brave146", "brave-146":
		return "brave_146"
	case "chrome144", "chrome-144":
		return "chrome_144"
	case "chrome146", "chrome-146":
		return "chrome_146"
	case "firefox147", "firefox-147":
		return "firefox_147"
	case "firefox148", "firefox-148":
		return "firefox_148"
	case "safari-ios-18.5", "safari_ios_18.5", "safari_ios_18_5":
		return "safari_ios_18_5"
	case "safari-ios-26.0", "safari_ios_26.0", "safari_ios_26_0":
		return "safari_ios_26_0"
	default:
		return name
	}
}
