package httpclienttls_test

import (
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/Drunkard-baifeng/public_golibs/httpclienttls"
)

func Example() {
	client, err := httpclienttls.NewClient(httpclienttls.Config{
		Timeout:     20 * time.Second,
		ProfilePool: []string{"chrome_146", "firefox_148", "safari_ios_18_5"},
	})
	if err != nil {
		panic(err)
	}
	defer client.Close()

	fingerprint := client.Profile()
	fmt.Printf("%s %s mobile=%t\n", fingerprint.Name, fingerprint.Platform, fingerprint.Mobile)

	response, err := client.Get("https://example.com", &httpclienttls.Options{
		BrowserMode: httpclienttls.BrowserModeNavigation,
	})
	if err != nil {
		panic(err)
	}
	fmt.Println(response.StatusCode)
}

func ExampleBrowserIdentity() {
	client, err := httpclienttls.NewClient(httpclienttls.Config{
		Profile:           "chrome_146",
		BrowserIdentity:   &httpclienttls.BrowserIdentity{Platform: "macOS"},
		AcceptLanguage:    "ja-JP,ja;q=0.9,en-US;q=0.8,en;q=0.7",
		StrictFingerprint: true,
	})
	if err != nil {
		panic(err)
	}
	defer client.Close()
	profile := client.Profile()
	fmt.Println(profile.Name, profile.Platform, profile.HighEntropyClientHints)
	// Output: chrome_146 macOS false
}

func ExampleExitLocaleConfig() {
	client, err := httpclienttls.NewClient(httpclienttls.Config{
		Profile: "chrome_146",
		ExitLocale: &httpclienttls.ExitLocaleConfig{
			Timeout:  6 * time.Second,
			CacheTTL: 15 * time.Minute,
		},
	})
	if err != nil {
		panic(err)
	}
	defer client.Close()
	// Construction and Locale are offline. The first request or an explicit
	// RefreshLocale(ctx) performs lookup; until then the profile default applies.
	info := client.Locale()
	fmt.Println(info.Language, info.DetectedAt.IsZero())
	// Output: en-US true
}

func ExampleClientHintsConfig() {
	client, err := httpclienttls.NewClient(httpclienttls.Config{
		Profile: "chrome_146",
		BrowserIdentity: &httpclienttls.BrowserIdentity{
			Platform: "macOS", PlatformVersion: "15.7.0", Architecture: "arm", Bitness: "64",
			FullVersion: "146.0.1.2", // Example fixture; supply the actual browser build.
		},
		ClientHints:       &httpclienttls.ClientHintsConfig{MaxOrigins: 128},
		StrictFingerprint: true,
	})
	if err != nil {
		panic(err)
	}
	defer client.Close()
	// This constructor-only example performs no network requests.
	profile := client.Profile()
	fmt.Println(profile.NegotiatedClientHints, profile.HighEntropyClientHints)
	fmt.Println(len(client.AcceptedClientHints("https://example.com")))
	// Output:
	// true false
	// 0
}

func ExampleNewClientHintContext() {
	// In real use, pass the final document response's URL and Headers.
	headers := http.Header{
		"Accept-Ch":          {"Sec-CH-UA-Arch"},
		"Permissions-Policy": {`ch-ua-arch=(self "https://cdn.example.com")`},
	}
	hintContext, err := httpclienttls.NewClientHintContext("https://example.com/page", headers)
	if err != nil {
		panic(err)
	}
	options := &httpclienttls.Options{
		BrowserMode:       httpclienttls.BrowserModeFetch,
		ClientHintContext: hintContext,
		Headers:           map[string]string{"Referer": "https://example.com/page"},
	}
	// Pass options to the negotiated client's subresource Get/Do call.
	fmt.Println(options.ClientHintContext != nil)
	// Output: true
}

func ExampleClient_CookieHeader() {
	client, err := httpclienttls.NewClient(httpclienttls.Config{Profile: "chrome_146"})
	if err != nil {
		panic(err)
	}
	defer client.Close()
	if err := client.SetCookie("https://example.com", "session", "example", "", "/account", true); err != nil {
		panic(err)
	}
	header, err := client.CookieHeader("https://example.com/account/settings")
	if err != nil {
		panic(err)
	}
	fmt.Println(header)
	header, err = client.CookieHeader("http://example.com/account/settings")
	if err != nil {
		panic(err)
	}
	fmt.Println(header == "")
	// Output:
	// session=example
	// true
}

func ExampleCooldownConfig() {
	client, err := httpclienttls.NewClient(httpclienttls.Config{
		Profile:  "chrome_146",
		Cooldown: &httpclienttls.CooldownConfig{DefaultDuration: time.Minute, MaxDuration: time.Hour},
	})
	if err != nil {
		panic(err)
	}
	defer client.Close()
	// No request is sent by this constructor-only example. In request handling:
	// if errors.Is(err, httpclienttls.ErrCooldown) { ... }
	fmt.Println(client.Cooldown("https://example.com").Until.IsZero())
	// Output: true
}

func ExampleCooldownError() {
	// In real use the client returns this type for a locally blocked request.
	var err error = &httpclienttls.CooldownError{CooldownInfo: httpclienttls.CooldownInfo{
		Origin: "https://example.com:443", StatusCode: 429,
	}}
	fmt.Println(errors.Is(err, httpclienttls.ErrCooldown))
	var blocked *httpclienttls.CooldownError
	if errors.As(err, &blocked) {
		fmt.Println(blocked.Origin, blocked.StatusCode)
	}
	// Output:
	// true
	// https://example.com:443 429
}
