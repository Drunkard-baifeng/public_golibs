package proxypool

import "testing"

func TestExtractIPPortFiltersInvalidValues(t *testing.T) {
	text := `
		999.999.999.999:80
		1.2.3.4:65536
		1.2.3.4:8080
		10.0.0.1:1
	`

	got := ExtractIPPort(text)
	if len(got) != 2 {
		t.Fatalf("expected 2 valid proxies, got=%d %+v", len(got), got)
	}
	if got[0].IP != "1.2.3.4" || got[0].Port != "8080" {
		t.Fatalf("unexpected first proxy: %+v", got[0])
	}
	if got[1].IP != "10.0.0.1" || got[1].Port != "1" {
		t.Fatalf("unexpected second proxy: %+v", got[1])
	}
}
