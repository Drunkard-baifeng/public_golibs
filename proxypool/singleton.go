package proxypool

import "sync"

var (
	defaultMu   sync.Mutex
	defaultPool *ProxyPool
	defaultCfg  Config
)

// Default 获取默认代理池（单例）
func Default() *ProxyPool {
	defaultMu.Lock()
	defer defaultMu.Unlock()

	if defaultPool == nil {
		defaultPool = New(defaultCfg)
	}
	return defaultPool
}

// InitDefault 初始化/重建默认代理池。
// 后续 Default() 会返回这个新实例。
func InitDefault(cfg Config) *ProxyPool {
	defaultMu.Lock()
	defer defaultMu.Unlock()

	defaultCfg = cfg
	defaultPool = New(cfg)
	return defaultPool
}

// Get 从默认代理池获取一个可用代理。
func Get() (*ProxyItem, error) {
	return Default().Get()
}

// GetStats 获取默认代理池统计信息。
func GetStats() Stats {
	return Default().GetStats()
}
