package httpclient

import (
	"encoding/json"
	"net/http"
)

// Response HTTP响应
type Response struct {
	StatusCode int            // 状态码
	Status     string         // 状态描述
	Headers    http.Header    // 响应头
	Cookies    []*http.Cookie // 响应Cookie
	Body       []byte         // 响应体
	Request    *http.Request  // 原始请求
}

// Text 获取响应文本
func (r *Response) Text() string {
	return string(r.Body)
}

// Bytes 获取响应字节
func (r *Response) Bytes() []byte {
	return r.Body
}

// JSON 解析JSON响应到目标结构
func (r *Response) JSON(v interface{}) error {
	return json.Unmarshal(r.Body, v)
}

// JSONMap 解析JSON响应为map
func (r *Response) JSONMap() (map[string]interface{}, error) {
	var result map[string]interface{}
	err := json.Unmarshal(r.Body, &result)
	return result, err
}

// IsSuccess 是否成功响应 (2xx)
func (r *Response) IsSuccess() bool {
	return r.StatusCode >= 200 && r.StatusCode < 300
}

// IsRedirect 是否重定向响应 (3xx)
func (r *Response) IsRedirect() bool {
	return r.StatusCode >= 300 && r.StatusCode < 400
}

// IsClientError 是否客户端错误 (4xx)
func (r *Response) IsClientError() bool {
	return r.StatusCode >= 400 && r.StatusCode < 500
}

// IsServerError 是否服务端错误 (5xx)
func (r *Response) IsServerError() bool {
	return r.StatusCode >= 500 && r.StatusCode < 600
}

// GetHeader 获取响应头（返回第一个值）
func (r *Response) GetHeader(key string) string {
	return r.Headers.Get(key)
}

// GetHeaders 获取响应头（返回所有值）
func (r *Response) GetHeaders(key string) []string {
	return r.Headers.Values(key)
}

// GetAllHeaders 获取所有响应头（每个key只返回第一个值）
func (r *Response) GetAllHeaders() map[string]string {
	result := make(map[string]string)
	for k, v := range r.Headers {
		if len(v) > 0 {
			result[k] = v[0]
		}
	}
	return result
}

// GetCookie 获取指定名称的Cookie值
func (r *Response) GetCookie(name string) string {
	for _, cookie := range r.Cookies {
		if cookie.Name == name {
			return cookie.Value
		}
	}
	return ""
}

// GetAllCookies 获取所有Cookie
func (r *Response) GetAllCookies() map[string]string {
	result := make(map[string]string)
	for _, cookie := range r.Cookies {
		result[cookie.Name] = cookie.Value
	}
	return result
}

// ContentType 获取Content-Type
func (r *Response) ContentType() string {
	return r.Headers.Get("Content-Type")
}

// ContentLength 获取Content-Length
func (r *Response) ContentLength() int {
	return len(r.Body)
}

// Location 获取重定向地址
func (r *Response) Location() string {
	return r.Headers.Get("Location")
}
