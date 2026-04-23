package proxypool

import (
	"io"
	"net/http"
	"regexp"
	"time"

	"github.com/Drunkard-baifeng/public_golibs/logger"
)

// IP:Port 正则表达式
var defaultIPPortRegex = regexp.MustCompile(`(\d{1,3}\.\d{1,3}\.\d{1,3}\.\d{1,3})[:\s]+(\d{1,5})`)

// ExtractIPPort 从文本中提取IP和端口
func ExtractIPPort(text string) []ProxyAddr {
	matches := defaultIPPortRegex.FindAllStringSubmatch(text, -1)
	result := make([]ProxyAddr, 0, len(matches))
	for _, match := range matches {
		if len(match) >= 3 {
			result = append(result, ProxyAddr{
				IP:   match[1],
				Port: match[2],
			})
		}
	}
	return result
}

// SimpleFetchFunc 简单的HTTP获取函数（使用标准库）
// 用于从API获取代理列表
func SimpleFetchFunc(apiURL string) ([]ProxyAddr, error) {
	client := &http.Client{
		Timeout: 10 * time.Second,
	}

	resp, err := client.Get(apiURL)
	if err != nil {
		logger.Errorf("加载代理失败: %v", err)
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	logger.Debugf("加载代理: %s", string(body))
	result := ExtractIPPort(string(body))
	logger.Successf("成功加载 %d 个代理", len(result))
	return result, nil
}

// CreateFetchFunc 创建自定义HTTP客户端的获取函数
// 可以传入自定义的 HTTP 客户端
func CreateFetchFunc(httpClient *http.Client) FetchFunc {
	return func(apiURL string) ([]ProxyAddr, error) {
		resp, err := httpClient.Get(apiURL)
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()

		body, err := io.ReadAll(resp.Body)
		if err != nil {
			return nil, err
		}

		return ExtractIPPort(string(body)), nil
	}
}
