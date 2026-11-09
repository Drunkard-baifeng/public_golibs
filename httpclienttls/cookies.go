package httpclienttls

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

// CookiesForURL returns a detached snapshot of jar cookies eligible for target.
// The jar applies host/domain, path, Secure and expiry rules, preserving multiple
// cookies with the same name. As with net/http.CookieJar, only request cookie
// fields are returned, not original Set-Cookie attributes. Unscoped SetCookies/
// AddCookie overrides, raw Cookie headers and request Options are not included.
// This is HTTP request access, so HttpOnly cookies are included.
func (c *Client) CookiesForURL(target string) ([]*http.Cookie, error) {
	u, err := parseCookieURL(target)
	if err != nil {
		return nil, err
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.jar == nil {
		return nil, fmt.Errorf("httpclienttls: cookie jar is not initialized")
	}
	return toStandardCookies(c.jar.Cookies(u)), nil
}

// CookieHeader serializes the URL-scoped jar snapshot as a request Cookie header.
// Callers must query again for a different URL; do not forward it across origins.
func (c *Client) CookieHeader(target string) (string, error) {
	cookies, err := c.CookiesForURL(target)
	if err != nil {
		return "", err
	}
	request := &http.Request{Header: make(http.Header)}
	for _, cookie := range cookies {
		request.AddCookie(cookie)
	}
	return request.Header.Get("Cookie"), nil
}

func parseCookieURL(target string) (*url.URL, error) {
	target = strings.TrimSpace(target)
	u, err := url.Parse(target)
	if err != nil || u.User != nil || canonicalOrigin(target) == "" {
		// Parse errors may include URL credentials or query secrets.
		return nil, fmt.Errorf("invalid cookie URL: use an absolute HTTP(S) URL without credentials")
	}
	return u, nil
}
