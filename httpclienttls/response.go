package httpclienttls

import (
	"encoding/json"
	"net/http"
)

// Response is an in-memory HTTP response.
type Response struct {
	StatusCode       int
	Status           string
	Proto            string
	Headers          http.Header
	Cookies          []*http.Cookie
	Body             []byte
	Request          *http.Request
	URL              string
	TransferEncoding []string
	Uncompressed     bool
}

func (r *Response) Text() string { return string(r.Body) }

func (r *Response) Bytes() []byte { return r.Body }

func (r *Response) JSON(value any) error { return json.Unmarshal(r.Body, value) }

func (r *Response) JSONMap() (map[string]any, error) {
	var result map[string]any
	err := json.Unmarshal(r.Body, &result)
	return result, err
}

func (r *Response) IsSuccess() bool { return r.StatusCode >= 200 && r.StatusCode < 300 }

func (r *Response) IsRedirect() bool { return r.StatusCode >= 300 && r.StatusCode < 400 }

func (r *Response) IsClientError() bool { return r.StatusCode >= 400 && r.StatusCode < 500 }

func (r *Response) IsServerError() bool { return r.StatusCode >= 500 && r.StatusCode < 600 }

func (r *Response) GetHeader(key string) string { return r.Headers.Get(key) }

func (r *Response) GetHeaders(key string) []string { return r.Headers.Values(key) }

func (r *Response) GetAllHeaders() map[string]string {
	result := make(map[string]string, len(r.Headers))
	for key, values := range r.Headers {
		if len(values) > 0 {
			result[key] = values[0]
		}
	}
	return result
}

func (r *Response) GetCookie(name string) string {
	for _, cookie := range r.Cookies {
		if cookie.Name == name {
			return cookie.Value
		}
	}
	return ""
}

func (r *Response) GetAllCookies() map[string]string {
	result := make(map[string]string, len(r.Cookies))
	for _, cookie := range r.Cookies {
		result[cookie.Name] = cookie.Value
	}
	return result
}

func (r *Response) ContentType() string { return r.Headers.Get("Content-Type") }

// ContentLength returns the number of bytes held in Body.
func (r *Response) ContentLength() int { return len(r.Body) }

func (r *Response) Location() string { return r.Headers.Get("Location") }
