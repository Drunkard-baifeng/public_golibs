package httpclienttls

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestLegacyDesktopSafariProfilesUseRealTransport(t *testing.T) {
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Sec-CH-UA") != "" {
			t.Error("Safari emitted Chromium hints")
		}
		fmt.Fprint(w, r.Header.Get("User-Agent"))
	}))
	server.EnableHTTP2 = true
	server.StartTLS()
	defer server.Close()
	for _, tc := range []struct{ name, canonical, version string }{
		{"safari-15.6.1", "safari_15_6_1", "Version/15.6.1"},
		{"safari16.0", "safari_16_0", "Version/16.0"},
	} {
		client, err := NewClient(Config{Profile: tc.name, Verify: BoolPtr(false)})
		if err != nil {
			t.Error(err)
			continue
		}
		r, err := client.Get(server.URL, nil)
		if err != nil || r.Proto != "HTTP/2.0" || !strings.Contains(r.Text(), tc.version) || !strings.Contains(r.Text(), "Macintosh") {
			t.Errorf("legacy Safari wire: %v %v", r, err)
		}
		p := client.Profile()
		if p.Name != tc.canonical || p.Platform != "macOS" || p.Mobile {
			t.Errorf("wrong Safari identity: %+v", p)
		}
		client.Close()
	}
	for _, name := range defaultRandomProfiles {
		if name == "safari_15_6_1" || name == "safari_16_0" {
			t.Error("legacy profiles entered default pool")
		}
	}
}

func TestDesktopSafariRejectsOtherOperatingSystems(t *testing.T) {
	for _, platform := range []string{"Windows", "Linux", "iOS"} {
		client, err := NewClient(Config{Profile: "safari_16_0", BrowserIdentity: &BrowserIdentity{Platform: platform}})
		if client != nil {
			client.Close()
		}
		if err == nil {
			t.Errorf("desktop Safari accepted platform %s", platform)
		}
	}
}
