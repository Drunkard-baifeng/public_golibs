package proxypool

import (
	"errors"
	"regexp"
	"sync"
)

var (
	ErrNoAvailableProxy = errors.New("没有可用的代理")
	ErrPoolNotInit      = errors.New("代理池未初始化")
	ErrAPIURLEmpty      = errors.New("API地址为空")
)

// ProxyPool 代理池
type ProxyPool struct {
	proxies       []*ProxyItem // 代理列表
	poolMu        sync.RWMutex // 代理池读写锁
	refreshMu     sync.Mutex   // 刷新锁（防止并发刷新）
	lastErrMu     sync.RWMutex // 最近刷新错误读写锁
	apiURL        string       // 代理API地址
	maxUseCount   int          // 默认最大使用次数
	expireSeconds int          // 默认过期时间（秒）
	minPoolSize   int          // 最小池大小（低于此值触发刷新）
	fetchFunc     FetchFunc    // 自定义获取代理函数
	onProxyGet    OnProxyGetFn // 获取代理回调
	onRefresh     OnRefreshFn  // 刷新代理回调
	roundRobinIdx int          // 轮询索引
	lastErr       error        // 最近一次刷新错误
}

// FetchFunc 自定义获取代理函数类型
type FetchFunc func(apiURL string) ([]ProxyAddr, error)

// OnProxyGetFn 获取代理时的回调
type OnProxyGetFn func(proxy *ProxyItem)

// OnRefreshFn 刷新代理时的回调
type OnRefreshFn func(count int, err error)

// ProxyAddr 代理地址
type ProxyAddr struct {
	IP   string
	Port string
}

// Config 代理池配置
type Config struct {
	APIURL        string       // 代理API地址
	MaxUseCount   int          // 最大使用次数（默认5）
	ExpireSeconds int          // 过期时间秒数（默认180）
	MinPoolSize   int          // 最小池大小（默认3）
	FetchFunc     FetchFunc    // 自定义获取函数
	OnProxyGet    OnProxyGetFn // 获取代理回调
	OnRefresh     OnRefreshFn  // 刷新回调
}

// New 创建代理池
func New(cfg Config) *ProxyPool {
	if cfg.MaxUseCount <= 0 {
		cfg.MaxUseCount = 5
	}
	if cfg.ExpireSeconds <= 0 {
		cfg.ExpireSeconds = 180
	}
	if cfg.MinPoolSize <= 0 {
		cfg.MinPoolSize = 3
	}

	return &ProxyPool{
		proxies:       make([]*ProxyItem, 0),
		apiURL:        cfg.APIURL,
		maxUseCount:   cfg.MaxUseCount,
		expireSeconds: cfg.ExpireSeconds,
		minPoolSize:   cfg.MinPoolSize,
		fetchFunc:     cfg.FetchFunc,
		onProxyGet:    cfg.OnProxyGet,
		onRefresh:     cfg.OnRefresh,
	}
}

// 默认的IP:Port正则
var ipPortRegex = regexp.MustCompile(`(\d{1,3}\.\d{1,3}\.\d{1,3}\.\d{1,3}):(\d{1,5})`)

// defaultFetchFunc 默认获取代理函数（简单HTTP GET）
func defaultFetchFunc(apiURL string) ([]ProxyAddr, error) {
	// 这里需要导入 httpclient，或者使用标准库
	// 为了解耦，我们让用户自己传入 FetchFunc
	return nil, errors.New("请设置 FetchFunc 或使用 SetFetchFunc")
}

// SetAPIURL 设置API地址
func (p *ProxyPool) SetAPIURL(url string) *ProxyPool {
	p.poolMu.Lock()
	defer p.poolMu.Unlock()
	p.apiURL = url
	return p
}

// SetMaxUseCount 设置最大使用次数
func (p *ProxyPool) SetMaxUseCount(count int) *ProxyPool {
	p.poolMu.Lock()
	defer p.poolMu.Unlock()
	p.maxUseCount = count
	return p
}

// SetExpireSeconds 设置过期时间
func (p *ProxyPool) SetExpireSeconds(seconds int) *ProxyPool {
	p.poolMu.Lock()
	defer p.poolMu.Unlock()
	p.expireSeconds = seconds
	return p
}

// SetMinPoolSize 设置最小池大小
func (p *ProxyPool) SetMinPoolSize(size int) *ProxyPool {
	p.poolMu.Lock()
	defer p.poolMu.Unlock()
	p.minPoolSize = size
	return p
}

// SetFetchFunc 设置自定义获取代理函数
func (p *ProxyPool) SetFetchFunc(fn FetchFunc) *ProxyPool {
	p.poolMu.Lock()
	defer p.poolMu.Unlock()
	p.fetchFunc = fn
	return p
}

// SetOnProxyGet 设置获取代理回调
func (p *ProxyPool) SetOnProxyGet(fn OnProxyGetFn) *ProxyPool {
	p.poolMu.Lock()
	defer p.poolMu.Unlock()
	p.onProxyGet = fn
	return p
}

// SetOnRefresh 设置刷新回调
func (p *ProxyPool) SetOnRefresh(fn OnRefreshFn) *ProxyPool {
	p.poolMu.Lock()
	defer p.poolMu.Unlock()
	p.onRefresh = fn
	return p
}

// LastRefreshError 返回最近一次刷新错误（无错误时返回 nil）。
func (p *ProxyPool) LastRefreshError() error {
	p.lastErrMu.RLock()
	defer p.lastErrMu.RUnlock()
	return p.lastErr
}

func (p *ProxyPool) setLastRefreshError(err error) {
	p.lastErrMu.Lock()
	defer p.lastErrMu.Unlock()
	p.lastErr = err
}

// Refresh 刷新代理池
func (p *ProxyPool) Refresh() error {
	p.poolMu.RLock()
	apiURL := p.apiURL
	fetchFunc := p.fetchFunc
	onRefresh := p.onRefresh
	p.poolMu.RUnlock()

	if apiURL == "" && fetchFunc == nil {
		p.setLastRefreshError(ErrAPIURLEmpty)
		return ErrAPIURLEmpty
	}

	// 尝试获取刷新锁，避免并发刷新
	if !p.refreshMu.TryLock() {
		return nil // 已有刷新在进行
	}
	defer p.refreshMu.Unlock()

	var proxies []ProxyAddr
	var err error

	if fetchFunc != nil {
		proxies, err = fetchFunc(apiURL)
	} else {
		err = errors.New("请设置 FetchFunc")
	}

	if err != nil {
		p.setLastRefreshError(err)
		if onRefresh != nil {
			onRefresh(0, err)
		}
		return err
	}

	p.setLastRefreshError(nil)

	// 添加新代理
	count := 0
	for _, addr := range proxies {
		if p.AddProxy(addr.IP, addr.Port) {
			count++
		}
	}

	if onRefresh != nil {
		onRefresh(count, nil)
	}

	return nil
}

// AddProxy 添加代理
func (p *ProxyPool) AddProxy(ip, port string) bool {
	p.poolMu.Lock()
	defer p.poolMu.Unlock()

	// 检查是否已存在
	key := ip + ":" + port
	for _, proxy := range p.proxies {
		if proxy.String() == key {
			return false
		}
	}

	proxy := NewProxyItemWithConfig(ip, port, p.maxUseCount, p.expireSeconds)
	p.proxies = append(p.proxies, proxy)
	return true
}

// AddProxyItem 添加代理项
func (p *ProxyPool) AddProxyItem(proxy *ProxyItem) bool {
	return p.AddProxy(proxy.IP, proxy.Port)
}

// Get 获取一个可用代理
func (p *ProxyPool) Get() (*ProxyItem, error) {
	p.poolMu.Lock()

	// 清理无效代理
	p.cleanupUnsafe()

	// 如果池子为空，同步刷新一次
	if len(p.proxies) == 0 {
		p.poolMu.Unlock()
		p.Refresh() // 同步刷新
		p.poolMu.Lock()
	} else if len(p.proxies) < p.minPoolSize {
		// 池子不为空但低于最小值，异步刷新
		go p.Refresh()
	}

	defer p.poolMu.Unlock()

	// 获取可用代理
	available := make([]*ProxyItem, 0)
	for _, proxy := range p.proxies {
		if proxy.IsAvailable() {
			available = append(available, proxy)
		}
	}

	if len(available) == 0 {
		return nil, ErrNoAvailableProxy
	}

	// 轮询选择（均匀分配）
	idx := p.roundRobinIdx % len(available)
	p.roundRobinIdx++
	proxy := available[idx]

	if proxy.IncrementUseCount() {
		if p.onProxyGet != nil {
			p.onProxyGet(proxy)
		}
		return proxy, nil
	}

	return nil, ErrNoAvailableProxy
}

// GetString 获取代理字符串 ip:port
func (p *ProxyPool) GetString() (string, error) {
	proxy, err := p.Get()
	if err != nil {
		return "", err
	}
	return proxy.String(), nil
}

// GetURL 获取代理URL http://ip:port
func (p *ProxyPool) GetURL() (string, error) {
	proxy, err := p.Get()
	if err != nil {
		return "", err
	}
	return proxy.URL(), nil
}

// cleanupUnsafe 清理无效代理（非线程安全，需要在持有锁时调用）
func (p *ProxyPool) cleanupUnsafe() {
	valid := make([]*ProxyItem, 0, len(p.proxies))
	for _, proxy := range p.proxies {
		if proxy.IsAvailable() {
			valid = append(valid, proxy)
		}
	}
	p.proxies = valid
}

// Cleanup 清理无效代理
func (p *ProxyPool) Cleanup() int {
	p.poolMu.Lock()
	defer p.poolMu.Unlock()

	before := len(p.proxies)
	p.cleanupUnsafe()
	return before - len(p.proxies)
}

// Clear 清空代理池
func (p *ProxyPool) Clear() {
	p.poolMu.Lock()
	defer p.poolMu.Unlock()
	p.proxies = make([]*ProxyItem, 0)
}

// Remove 移除指定代理
func (p *ProxyPool) Remove(ip, port string) bool {
	p.poolMu.Lock()
	defer p.poolMu.Unlock()

	key := ip + ":" + port
	for i, proxy := range p.proxies {
		if proxy.String() == key {
			p.proxies = append(p.proxies[:i], p.proxies[i+1:]...)
			return true
		}
	}
	return false
}

// RemoveByString 通过字符串移除代理
func (p *ProxyPool) RemoveByString(proxyStr string) bool {
	p.poolMu.Lock()
	defer p.poolMu.Unlock()

	for i, proxy := range p.proxies {
		if proxy.String() == proxyStr {
			p.proxies = append(p.proxies[:i], p.proxies[i+1:]...)
			return true
		}
	}
	return false
}

// Size 获取代理池大小
func (p *ProxyPool) Size() int {
	p.poolMu.RLock()
	defer p.poolMu.RUnlock()
	return len(p.proxies)
}

// AvailableCount 获取可用代理数量
func (p *ProxyPool) AvailableCount() int {
	p.poolMu.RLock()
	defer p.poolMu.RUnlock()

	count := 0
	for _, proxy := range p.proxies {
		if proxy.IsAvailable() {
			count++
		}
	}
	return count
}

// Stats 代理池统计信息
type Stats struct {
	Total     int `json:"total"`     // 总数
	Available int `json:"available"` // 可用数
	Expired   int `json:"expired"`   // 已过期
	MaxUsed   int `json:"max_used"`  // 达到最大使用次数
}

// GetStats 获取统计信息
func (p *ProxyPool) GetStats() Stats {
	p.poolMu.RLock()
	defer p.poolMu.RUnlock()

	stats := Stats{Total: len(p.proxies)}
	for _, proxy := range p.proxies {
		if proxy.IsAvailable() {
			stats.Available++
		}
		if proxy.IsExpired() {
			stats.Expired++
		}
		if proxy.IsMaxUsed() {
			stats.MaxUsed++
		}
	}
	return stats
}

// GetAll 获取所有代理（返回副本）
func (p *ProxyPool) GetAll() []*ProxyItem {
	p.poolMu.RLock()
	defer p.poolMu.RUnlock()

	result := make([]*ProxyItem, len(p.proxies))
	copy(result, p.proxies)
	return result
}

// GetAvailable 获取所有可用代理
func (p *ProxyPool) GetAvailable() []*ProxyItem {
	p.poolMu.RLock()
	defer p.poolMu.RUnlock()

	result := make([]*ProxyItem, 0)
	for _, proxy := range p.proxies {
		if proxy.IsAvailable() {
			result = append(result, proxy)
		}
	}
	return result
}
