package proxypool

import (
	"errors"
	"fmt"
	"math/rand"
	"strings"
	"sync"
	"sync/atomic"
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

// GetProxyMode 获取代理策略
type GetProxyMode int

const (
	// GetProxyModeOnce 只获取一次（成功或失败立即返回）
	GetProxyModeOnce GetProxyMode = iota
	// GetProxyModeMustSuccess 必须成功（循环获取，直到成功或被停止）
	GetProxyModeMustSuccess
)

var (
	// ErrGetProxyStopped 必成功模式下被停止标识中断
	ErrGetProxyStopped = errors.New("获取代理已被停止标识中断")
	// ErrInvalidGetProxyMode 非法获取策略
	ErrInvalidGetProxyMode = errors.New("非法获取代理策略")
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
	getStop   atomic.Bool

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

// SetGetProxyStop 设置获取代理停止标识。
// 当设置为 true 时，GetProxy( GetProxyModeMustSuccess ) 会中断并返回错误。
func (p *Proxy) SetGetProxyStop(stop bool) *Proxy {
	p.getStop.Store(stop)
	return p
}

// StopGetProxy 停止必成功循环获取。
func (p *Proxy) StopGetProxy() *Proxy {
	return p.SetGetProxyStop(true)
}

// ResumeGetProxy 取消停止标识，恢复必成功循环获取能力。
func (p *Proxy) ResumeGetProxy() *Proxy {
	return p.SetGetProxyStop(false)
}

// GetProxy 获取代理（统一入口）。
// 可选参数 mode：
// - 不传：默认 GetProxyModeOnce
// - GetProxyModeOnce：只尝试一次
// - GetProxyModeMustSuccess：持续重试直到成功，或收到停止标识
func (p *Proxy) GetProxy(modes ...GetProxyMode) (*ProxyResult, error) {
	p.mu.RLock()
	mode := p.mode
	proxyType := p.proxyType
	p.mu.RUnlock()

	getMode := GetProxyModeOnce
	if len(modes) > 0 {
		getMode = modes[0]
	}

	// 不换IP/虚拟IP/账密IP：单次即返回
	if mode == ModeNone {
		return p.getNoProxy()
	}
	if mode == ModeVirtual {
		return p.getVirtualProxy()
	}
	if mode == ModeAuth {
		return p.getAuthProxy(proxyType)
	}

	// 代理池模式
	switch getMode {
	case GetProxyModeOnce:
		return p.getPoolProxy(proxyType)
	case GetProxyModeMustSuccess:
		var lastErr error
		for {
			if p.getStop.Load() {
				if lastErr != nil {
					return nil, errors.Join(ErrGetProxyStopped, lastErr)
				}
				return nil, ErrGetProxyStopped
			}

			result, err := p.getPoolProxy(proxyType)
			if err == nil {
				return result, nil
			}
			lastErr = err
			time.Sleep(time.Second)
		}
	default:
		return nil, ErrInvalidGetProxyMode
	}
}

func (p *Proxy) getPoolProxy(proxyType ProxyType) (*ProxyResult, error) {
	p.mu.Lock()
	p.initPool()
	pool := p.pool
	p.mu.Unlock()

	proxy, err := pool.Get()
	if err != nil {
		// 优先返回最近一次加载代理失败错误
		if lastErr := pool.LastRefreshError(); lastErr != nil {
			return nil, lastErr
		}
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
