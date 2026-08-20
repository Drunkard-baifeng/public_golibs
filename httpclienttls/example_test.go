package httpclienttls_test

import (
	"fmt"
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
