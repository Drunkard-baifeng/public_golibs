package httpclienttls

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	fhttp "github.com/bogdanfinn/fhttp"
)

// BrowserIdentity describes HTTP identity metadata, not a JavaScript runtime.
// Empty Platform keeps the selected profile's platform. Chrome/Brave/Firefox
// desktop profiles accept Windows, macOS and Linux; Safari retains its platform.
// Values are copied.
// High-entropy values are caller-supplied, never inferred from the host machine.
type BrowserIdentity struct {
	Platform        string
	PlatformVersion string
	Architecture    string // "x86" or "arm"; these desktop identities are 64-bit.
	Bitness         string // "64" for the supported desktop identities.
	FullVersion     string // Chromium's four-component version, matching the profile major.
}

var (
	identityVersionPattern = regexp.MustCompile(`^[0-9]+(?:\.[0-9]+){0,3}$`)
	clientHintHeaderNames  = []string{
		"Sec-Ch-Ua", "Sec-Ch-Ua-Mobile", "Sec-Ch-Ua-Platform",
		"Sec-Ch-Ua-Full-Version-List", "Sec-Ch-Ua-Platform-Version",
		"Sec-Ch-Ua-Arch", "Sec-Ch-Ua-Bitness", "Sec-Ch-Ua-Model",
		"Sec-Ch-Ua-Full-Version", "Sec-Ch-Ua-Wow64", "Sec-Ch-Ua-Form-Factors",
	}
)

func configureProfileIdentity(profile Profile, config Config) (Profile, error) {
	if config.ClientHints != nil && config.HighEntropyClientHints {
		return Profile{}, fmt.Errorf("choose negotiated ClientHints or eager HighEntropyClientHints, not both")
	}
	configured, err := configureIdentity(profile, config.BrowserIdentity, config.HighEntropyClientHints || config.ClientHints != nil)
	if err != nil {
		return Profile{}, err
	}
	configured.HighEntropyClientHints = config.HighEntropyClientHints
	configured.NegotiatedClientHints = config.ClientHints != nil
	return configured, nil
}

func configureIdentity(profile Profile, identity *BrowserIdentity, highEntropy bool) (Profile, error) {
	values := BrowserIdentity{Platform: profile.Platform}
	if identity != nil {
		values = *identity
		if strings.TrimSpace(values.Platform) == "" {
			values.Platform = profile.Platform
		}
	}
	switch strings.ToLower(strings.TrimSpace(values.Platform)) {
	case "windows":
		values.Platform = "Windows"
	case "macos":
		values.Platform = "macOS"
	case "linux":
		values.Platform = "Linux"
	case "ios":
		values.Platform = "iOS"
	default:
		return Profile{}, fmt.Errorf("unsupported browser identity platform")
	}
	if profile.Mobile != (values.Platform == "iOS") {
		return Profile{}, fmt.Errorf("browser identity platform conflicts with the selected desktop/mobile TLS profile")
	}
	if profile.Browser == "safari" && values.Platform != profile.Platform {
		return Profile{}, fmt.Errorf("safari identity must retain its profile platform")
	}
	if values.Architecture != "" && values.Architecture != "x86" && values.Architecture != "arm" {
		return Profile{}, fmt.Errorf("browser identity architecture must be x86 or arm")
	}
	if values.Bitness != "" && values.Bitness != "64" {
		return Profile{}, fmt.Errorf("supported desktop browser identities require 64-bit bitness")
	}
	if values.PlatformVersion != "" && !identityVersionPattern.MatchString(values.PlatformVersion) {
		return Profile{}, fmt.Errorf("browser identity platform version must be a dotted numeric version")
	}
	chromium := profile.Browser == "chrome" || profile.Browser == "brave"
	if !chromium && (highEntropy || values.PlatformVersion != "" || values.Architecture != "" || values.Bitness != "" || values.FullVersion != "") {
		return Profile{}, fmt.Errorf("Client Hints metadata requires a Chromium profile")
	}
	major := strings.TrimPrefix(profile.Name, profile.Browser+"_")
	if values.FullVersion != "" {
		parts := strings.Split(values.FullVersion, ".")
		if len(parts) != 4 || !identityVersionPattern.MatchString(values.FullVersion) || parts[0] != major {
			return Profile{}, fmt.Errorf("browser full version must have four numeric components matching the TLS profile major")
		}
	}
	if highEntropy && (values.PlatformVersion == "" || values.Architecture == "" || values.Bitness == "" || values.FullVersion == "") {
		return Profile{}, fmt.Errorf("high-entropy Client Hints require PlatformVersion, Architecture, Bitness and FullVersion")
	}
	// Chromium desktop UA platform tokens are reduced; do not put the actual OS
	// version or Apple Silicon architecture in these frozen tokens.
	platformToken := "Windows NT 10.0; Win64; x64"
	switch values.Platform {
	case "macOS":
		platformToken = "Macintosh; Intel Mac OS X 10_15_7"
		if profile.Browser == "firefox" {
			platformToken = "Macintosh; Intel Mac OS X 10.15"
		}
	case "Linux":
		platformToken = "X11; Linux x86_64"
	}
	profile.UserAgent = strings.Replace(profile.UserAgent, "Windows NT 10.0; Win64; x64", platformToken, 1)
	profile.Platform = values.Platform
	profile.Identity = values
	profile.HighEntropyClientHints = highEntropy
	if highEntropy {
		// Keep the profile's brand order and GREASE brand. Only full-version
		// values change; the ordinary UA and low-entropy brands stay reduced.
		profile.SecCHUAFullVersionList = strings.ReplaceAll(profile.SecCHUA, `;v="`+major+`"`, `;v="`+values.FullVersion+`"`)
		profile.SecCHUAFullVersionList = strings.ReplaceAll(profile.SecCHUAFullVersionList, `;v="24"`, `;v="24.0.0.0"`)
	}
	return profile, nil
}

func applyHighEntropyHeaders(headers fhttp.Header, profile Profile) {
	setHeaderDefault(headers, "Sec-Ch-Ua-Full-Version-List", profile.SecCHUAFullVersionList)
	setHeaderDefault(headers, "Sec-Ch-Ua-Platform-Version", strconv.Quote(profile.Identity.PlatformVersion))
	setHeaderDefault(headers, "Sec-Ch-Ua-Arch", strconv.Quote(profile.Identity.Architecture))
	setHeaderDefault(headers, "Sec-Ch-Ua-Bitness", strconv.Quote(profile.Identity.Bitness))
	setHeaderDefault(headers, "Sec-Ch-Ua-Model", `""`)
	setHeaderDefault(headers, "Sec-Ch-Ua-Full-Version", strconv.Quote(profile.Identity.FullVersion))
	// Supported Chromium identities are desktop 64-bit, never WoW64 processes.
	setHeaderDefault(headers, "Sec-Ch-Ua-Wow64", "?0")
	setHeaderDefault(headers, "Sec-Ch-Ua-Form-Factors", `"Desktop"`)
}

// Strict mode pins non-empty identity headers to the configured profile and
// rejects unmodeled UA hints. Empty or absent headers still express suppression,
// not a claim of another identity.
func validateIdentityHeaders(headers fhttp.Header, profile Profile) error {
	expected := make(fhttp.Header, len(clientHintHeaderNames)+1)
	expected.Set("User-Agent", profile.UserAgent)
	if profile.SecCHUA != "" {
		expected.Set("Sec-Ch-Ua", profile.SecCHUA)
		expected.Set("Sec-Ch-Ua-Platform", strconv.Quote(profile.Platform))
		expected.Set("Sec-Ch-Ua-Mobile", boolClientHint(profile.Mobile))
		if profile.HighEntropyClientHints || profile.NegotiatedClientHints {
			applyHighEntropyHeaders(expected, profile)
		}
	}
	for key, values := range headers {
		name := strings.ToLower(key)
		if name != "user-agent" && name != "sec-ch-ua" && !strings.HasPrefix(name, "sec-ch-ua-") {
			continue
		}
		if len(values) == 0 || len(values) == 1 && values[0] == "" {
			continue
		}
		if len(values) != 1 || values[0] != expected.Get(key) {
			return fmt.Errorf("fingerprint header %s conflicts with the configured browser identity", key)
		}
	}
	return nil
}
