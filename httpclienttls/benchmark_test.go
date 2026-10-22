package httpclienttls

import (
	"testing"

	fhttp "github.com/bogdanfinn/fhttp"
)

func BenchmarkBrowserHeaders(b *testing.B) {
	profile := profileRegistry["chrome_146"]
	for _, mode := range []BrowserMode{BrowserModeIdentity, BrowserModeNavigation, BrowserModeFetch} {
		b.Run(string(mode), func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				headers := fhttp.Header{"Referer": {"https://example.com/page"}}
				state := newBrowserHeaderState(headers, mode, "https://example.com/start")
				if err := applyBrowserHeaders(headers, profile, mode, "https://example.com/start"); err != nil {
					b.Fatal(err)
				}
				state.redirect(headers, profile, mode, "https://example.com/end")
			}
		})
	}
}
