package proxypool

import (
	"fmt"
	"sync"
	"sync/atomic"
	"time"
)

// ProxyItem 代理项
type ProxyItem struct {
	mu          sync.RWMutex
	IP          string    // IP地址
	Port        string    // 端口
	usedCount   int64     // 使用次数（原子操作）
	maxUseCount int64     // 最大使用次数
	expireTime  time.Time // 过期时间
	createTime  time.Time // 创建时间
	lastUseTime time.Time // 最后使用时间
}

// NewProxyItem 创建代理项
func NewProxyItem(ip, port string) *ProxyItem {
	return &ProxyItem{
		IP:          ip,
		Port:        port,
		usedCount:   0,
		maxUseCount: 5,
		expireTime:  time.Now().Add(180 * time.Second),
		createTime:  time.Now(),
	}
}

// NewProxyItemWithConfig 创建代理项（带配置）
func NewProxyItemWithConfig(ip, port string, maxUseCount int, expireSeconds int) *ProxyItem {
	return &ProxyItem{
		IP:          ip,
		Port:        port,
		usedCount:   0,
		maxUseCount: int64(maxUseCount),
		expireTime:  time.Now().Add(time.Duration(expireSeconds) * time.Second),
		createTime:  time.Now(),
	}
}

// String 返回代理字符串格式 ip:port
func (p *ProxyItem) String() string {
	return fmt.Sprintf("%s:%s", p.IP, p.Port)
}

// URL 返回代理URL格式 http://ip:port
func (p *ProxyItem) URL() string {
	return fmt.Sprintf("http://%s:%s", p.IP, p.Port)
}

// Socks5URL 返回SOCKS5代理URL格式
func (p *ProxyItem) Socks5URL() string {
	return fmt.Sprintf("socks5://%s:%s", p.IP, p.Port)
}

// IsAvailable 检查代理是否可用
func (p *ProxyItem) IsAvailable() bool {
	p.mu.RLock()
	expireTime := p.expireTime
	maxUseCount := atomic.LoadInt64(&p.maxUseCount)
	p.mu.RUnlock()

	return time.Now().Before(expireTime) && atomic.LoadInt64(&p.usedCount) < maxUseCount
}

// IsExpired 检查代理是否过期
func (p *ProxyItem) IsExpired() bool {
	p.mu.RLock()
	expireTime := p.expireTime
	p.mu.RUnlock()
	return time.Now().After(expireTime)
}

// IsMaxUsed 检查是否达到最大使用次数
func (p *ProxyItem) IsMaxUsed() bool {
	return atomic.LoadInt64(&p.usedCount) >= atomic.LoadInt64(&p.maxUseCount)
}

// IncrementUseCount 增加使用次数（线程安全）
// 返回是否成功增加（未达到最大次数）
func (p *ProxyItem) IncrementUseCount() bool {
	for {
		current := atomic.LoadInt64(&p.usedCount)
		if current >= atomic.LoadInt64(&p.maxUseCount) {
			return false
		}
		if atomic.CompareAndSwapInt64(&p.usedCount, current, current+1) {
			p.mu.Lock()
			p.lastUseTime = time.Now()
			p.mu.Unlock()
			return true
		}
	}
}

// GetUsedCount 获取使用次数
func (p *ProxyItem) GetUsedCount() int {
	return int(atomic.LoadInt64(&p.usedCount))
}

// GetMaxUseCount 获取最大使用次数
func (p *ProxyItem) GetMaxUseCount() int {
	return int(atomic.LoadInt64(&p.maxUseCount))
}

// GetRemainingCount 获取剩余可用次数
func (p *ProxyItem) GetRemainingCount() int {
	remaining := int(atomic.LoadInt64(&p.maxUseCount) - atomic.LoadInt64(&p.usedCount))
	if remaining < 0 {
		return 0
	}
	return remaining
}

// GetRemainingTime 获取剩余有效时间
func (p *ProxyItem) GetRemainingTime() time.Duration {
	p.mu.RLock()
	expireTime := p.expireTime
	p.mu.RUnlock()

	remaining := time.Until(expireTime)
	if remaining < 0 {
		return 0
	}
	return remaining
}

// SetMaxUseCount 设置最大使用次数
func (p *ProxyItem) SetMaxUseCount(count int) {
	atomic.StoreInt64(&p.maxUseCount, int64(count))
}

// SetExpireTime 设置过期时间
func (p *ProxyItem) SetExpireTime(expireTime time.Time) {
	p.mu.Lock()
	p.expireTime = expireTime
	p.mu.Unlock()
}

// ExtendExpireTime 延长过期时间
func (p *ProxyItem) ExtendExpireTime(duration time.Duration) {
	p.mu.Lock()
	p.expireTime = p.expireTime.Add(duration)
	p.mu.Unlock()
}

// Reset 重置代理（重新使用）
func (p *ProxyItem) Reset(expireSeconds int) {
	atomic.StoreInt64(&p.usedCount, 0)
	p.mu.Lock()
	p.expireTime = time.Now().Add(time.Duration(expireSeconds) * time.Second)
	p.lastUseTime = time.Time{}
	p.mu.Unlock()
}
