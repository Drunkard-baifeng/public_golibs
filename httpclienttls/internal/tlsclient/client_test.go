package tls_client

import (
	"strings"
	"testing"

	http "github.com/bogdanfinn/fhttp"
)

func TestCaughtTransportPanicReturnsError(t *testing.T) {
	client, err := NewHttpClient(NewNoopLogger(), WithCatchPanics())
	if err != nil {
		t.Fatal(err)
	}
	c := client.(*httpClient)
	c.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) { panic("fixture transport failure") })
	body := &observedRaceBody{Reader: strings.NewReader("upload")}
	req, _ := http.NewRequest("POST", "https://fixture.test", body)
	res, err := c.Do(req)
	if err == nil || res != nil {
		t.Fatalf("caught panic returned response=%v error=%v", res, err)
	}
	if body.closed.Load() != 1 {
		t.Fatalf("panic left request body open: closes=%d", body.closed.Load())
	}
}
