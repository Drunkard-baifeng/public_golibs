package proxypool

import (
	"fmt"
	"math/rand"
	"strings"
	"sync"
	"time"
)

func init() {
	rand.Seed(time.Now().UnixNano())
}

// ProxyMode 代理模式
type ProxyMode string

const (
	ModeNone    ProxyMode = "不换IP"  // 不使用代理
	ModeVirtual ProxyMode = "虚拟IP"  // 虚拟IP协议头
	ModePool    ProxyMode = "代理池IP" // 代理池
	ModeAuth    ProxyMode = "账密IP"  // 账密代理
)

// ProxyType 代理协议类型
type ProxyType string

const (
	TypeHTTP   ProxyType = "http"
	TypeSocks5 ProxyType = "socks5"
)

// ProxyResult 获取代理的结果
type ProxyResult struct {
	Type    ProxyType         // 代理协议类型 http/socks5
	Proxy   string            // 代理地址 ip:port 或 ip:port:user:pass
	Headers map[string]string // 额外的HTTP头（虚拟IP模式用）
	IP      string            // 显示用的IP
}

// Proxy 代理管理器
type Proxy struct {
	mu        sync.RWMutex
	mode      ProxyMode // 代理模式
	proxyType ProxyType // 代理协议类型 http/socks5

	// 代理池（内置）
	pool *ProxyPool

	// 代理池配置
	poolAPIURL        string
	poolMaxUseCount   int
	poolExpireSeconds int
	poolMinSize       int

	// 账密代理配置 ip:port:user:pass
	authHost     string
	authPort     string
	authUsername string
	authPassword string
}

// 单例
var (
	defaultProxyMu sync.Mutex
	defaultProxy   *Proxy
)

// NewProxy 创建代理管理器
func NewProxy() *Proxy {
	return &Proxy{
		mode:              ModeNone,
		proxyType:         TypeHTTP,
		poolMaxUseCount:   5,
		poolExpireSeconds: 180,
		poolMinSize:       3,
	}
}

// DefaultProxy 获取默认代理管理器（单例）
func DefaultProxy() *Proxy {
	defaultProxyMu.Lock()
	defer defaultProxyMu.Unlock()

	if defaultProxy == nil {
		defaultProxy = NewProxy()
	}
	return defaultProxy
}

// InitDefaultProxy 初始化/重建默认代理管理器。
func InitDefaultProxy(proxy *Proxy) *Proxy {
	defaultProxyMu.Lock()
	defer defaultProxyMu.Unlock()

	if proxy == nil {
		proxy = NewProxy()
	}
	defaultProxy = proxy
	return defaultProxy
}

// SetMode 设置代理模式
func (p *Proxy) SetMode(mode ProxyMode) *Proxy {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.mode = mode
	return p
}

// SetType 设置代理协议类型
func (p *Proxy) SetType(proxyType ProxyType) *Proxy {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.proxyType = proxyType
	return p
}

// ==================== 代理池配置 ====================

// SetPoolAPI 设置代理池API地址
func (p *Proxy) SetPoolAPI(apiURL string) *Proxy {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.poolAPIURL = apiURL
	if p.pool != nil {
		p.pool.SetAPIURL(apiURL)
	}
	return p
}

// SetPoolMaxUseCount 设置代理池最大使用次数
func (p *Proxy) SetPoolMaxUseCount(count int) *Proxy {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.poolMaxUseCount = count
	if p.pool != nil {
		p.pool.SetMaxUseCount(count)
	}
	return p
}

// SetPoolExpireSeconds 设置代理池过期时间
func (p *Proxy) SetPoolExpireSeconds(seconds int) *Proxy {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.poolExpireSeconds = seconds
	if p.pool != nil {
		p.pool.SetExpireSeconds(seconds)
	}
	return p
}

// SetPoolMinSize 设置代理池最小数量
func (p *Proxy) SetPoolMinSize(size int) *Proxy {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.poolMinSize = size
	if p.pool != nil {
		p.pool.SetMinPoolSize(size)
	}
	return p
}

// initPool 初始化代理池（内部调用）
func (p *Proxy) initPool() {
	if p.pool == nil {
		p.pool = New(Config{
			APIURL:        p.poolAPIURL,
			MaxUseCount:   p.poolMaxUseCount,
			ExpireSeconds: p.poolExpireSeconds,
			MinPoolSize:   p.poolMinSize,
			FetchFunc:     SimpleFetchFunc, // 使用默认获取函数
		})
	}
}

// GetPool 获取内部代理池（如果需要直接操作）
func (p *Proxy) GetPool() *ProxyPool {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.initPool()
	return p.pool
}

// ==================== 账密代理配置 ====================

// SetAuthProxy 设置账密代理
// 格式: ip:port:user:pass 或 ip:port（无认证）
func (p *Proxy) SetAuthProxy(authStr string) *Proxy {
	p.mu.Lock()
	defer p.mu.Unlock()

	parts := splitAuthProxy(authStr)
	p.authHost = ""
	p.authPort = ""
	p.authUsername = ""
	p.authPassword = ""

	if len(parts) >= 2 {
		p.authHost = parts[0]
		p.authPort = parts[1]
	}
	if len(parts) >= 3 {
		p.authUsername = parts[2]
	}
	if len(parts) >= 4 {
		p.authPassword = parts[3]
	}
	return p
}

// splitAuthProxy 解析 ip:port:user:pass 格式
func splitAuthProxy(s string) []string {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	// 限制最多分4段，最后一段保留剩余全部内容，支持密码内包含 ':'
	return strings.SplitN(s, ":", 4)
}

// ==================== 获取代理 ====================

// GetProxy 获取代理（统一入口，带重试）
func (p *Proxy) GetProxy() (*ProxyResult, error) {
	p.mu.RLock()
	mode := p.mode
	proxyType := p.proxyType
	p.mu.RUnlock()

	// 不换IP 和 虚拟IP 模式不需要重试
	if mode == ModeNone {
		return p.getNoProxy()
	}
	if mode == ModeVirtual {
		return p.getVirtualProxy()
	}
	if mode == ModeAuth {
		return p.getAuthProxy(proxyType)
	}

	// 代理池模式：循环重试直到获取成功
	maxRetry := 10
	for i := 0; i < maxRetry; i++ {
		result, err := p.getPoolProxy(proxyType)
		if err == nil {
			return result, nil
		}
		// 获取失败，等待1秒后重试
		time.Sleep(time.Second)
	}

	return nil, fmt.Errorf("获取代理失败，已重试 %d 次", maxRetry)
}

// getNoProxy 不换IP模式
func (p *Proxy) getNoProxy() (*ProxyResult, error) {
	return &ProxyResult{
		Type:    "",
		Proxy:   "",
		Headers: nil,
		IP:      "直连",
	}, nil
}

// getVirtualProxy 虚拟IP模式
func (p *Proxy) getVirtualProxy() (*ProxyResult, error) {
	ip := fmt.Sprintf("%d.%d.%d.%d",
		rand.Intn(223)+1,
		rand.Intn(256),
		rand.Intn(256),
		rand.Intn(254)+1,
	)

	headers := map[string]string{
		"X-Forwarded-For":     ip,
		"X-Real-IP":           ip,
		"X-Client-IP":         ip,
		"X-Originating-IP":    ip,
		"CF-Connecting-IP":    ip,
		"True-Client-IP":      ip,
		"X-Remote-IP":         ip,
		"X-Remote-Addr":       ip,
		"X-Cluster-Client-IP": ip,
	}

	return &ProxyResult{
		Type:    "",
		Proxy:   "",
		Headers: headers,
		IP:      ip + " (虚拟)",
	}, nil
}

// getPoolProxy 代理池模式
func (p *Proxy) getPoolProxy(proxyType ProxyType) (*ProxyResult, error) {
	p.mu.Lock()
	p.initPool()
	pool := p.pool
	p.mu.Unlock()

	proxy, err := pool.Get()
	if err != nil {
		return nil, err
	}

	// 返回 ip:port 格式
	return &ProxyResult{
		Type:    proxyType,
		Proxy:   proxy.String(), // ip:port
		Headers: nil,
		IP:      proxy.String(),
	}, nil
}

// getAuthProxy 账密代理模式
func (p *Proxy) getAuthProxy(proxyType ProxyType) (*ProxyResult, error) {
	p.mu.RLock()
	host := p.authHost
	port := p.authPort
	username := p.authUsername
	password := p.authPassword
	p.mu.RUnlock()

	if host == "" || port == "" {
		return nil, fmt.Errorf("账密代理未配置")
	}

	var proxy string
	if username != "" && password != "" {
		// ip:port:user:pass
		proxy = fmt.Sprintf("%s:%s:%s:%s", host, port, username, password)
	} else {
		// ip:port
		proxy = fmt.Sprintf("%s:%s", host, port)
	}

	return &ProxyResult{
		Type:    proxyType,
		Proxy:   proxy,
		Headers: nil,
		IP:      fmt.Sprintf("%s:%s", host, port),
	}, nil
}

// ==================== 状态获取 ====================

// GetMode 获取当前模式
func (p *Proxy) GetMode() ProxyMode {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.mode
}

// GetType 获取当前代理类型
func (p *Proxy) GetType() ProxyType {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.proxyType
}

// GetPoolStats 获取代理池统计
func (p *Proxy) GetPoolStats() Stats {
	p.mu.Lock()
	p.initPool()
	pool := p.pool
	p.mu.Unlock()
	return pool.GetStats()
}
