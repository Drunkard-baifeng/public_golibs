package httpclienttls

import (
	"os"
	"testing"
)

type liveFingerprintReport struct {
	UserAgent  string `json:"user_agent"`
	JA3Hash    string `json:"ja3_hash"`
	JA4        string `json:"ja4"`
	AkamaiHash string `json:"akamai_hash"`
	AkamaiText string `json:"akamai_text"`
}

// TestLiveFingerprint is opt-in because it calls a public diagnostics service.
// Run with HTTPCLIENTTLS_LIVE_TEST=1 go test -run TestLiveFingerprint -v.
func TestLiveFingerprint(t *testing.T) {
	if os.Getenv("HTTPCLIENTTLS_LIVE_TEST") != "1" {
		t.Skip("set HTTPCLIENTTLS_LIVE_TEST=1 to run the wire fingerprint probe")
	}
	for _, profileName := range []string{"chrome_146", "firefox_148", "safari_ios_18_5"} {
		t.Run(profileName, func(t *testing.T) {
			client, err := NewClient(Config{Profile: profileName, Timeout: defaultTimeout})
			if err != nil {
				t.Fatal(err)
			}
			defer client.Close()
			response, err := client.Get("https://tls.browserleaks.com/json", &Options{BrowserMode: BrowserModeNavigation})
			if err != nil {
				t.Fatal(err)
			}
			var report liveFingerprintReport
			if err := response.JSON(&report); err != nil {
				t.Fatalf("decode fingerprint response: %v", err)
			}
			profile := client.Profile()
			if report.UserAgent != profile.UserAgent {
				t.Fatalf("User-Agent=%q want=%q", report.UserAgent, profile.UserAgent)
			}
			if report.JA3Hash == "" || report.JA4 == "" {
				t.Fatalf("incomplete TLS fingerprint: %+v", report)
			}
			if report.AkamaiHash == "" || report.AkamaiText == "" {
				t.Fatalf("incomplete HTTP/2 fingerprint: hash=%q fingerprint=%q", report.AkamaiHash, report.AkamaiText)
			}
			t.Logf("profile=%s ja3=%s ja4=%s akamai=%s", profileName,
				report.JA3Hash, report.JA4, report.AkamaiHash)
		})
	}
}
