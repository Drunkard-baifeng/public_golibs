package workerpool

import (
	"runtime"
	"sync"
)

var (
	defaultMu         sync.Mutex
	defaultPool       *Pool
	defaultMaxWorkers = runtime.NumCPU()
)

// Default 返回默认单例池，首次调用会自动初始化。
func Default() *Pool {
	defaultMu.Lock()
	defer defaultMu.Unlock()

	if defaultPool == nil || defaultPool.IsStopped() {
		defaultPool = New(defaultMaxWorkers)
	}
	return defaultPool
}

// SetDefaultMaxWorkers 设置默认单例的最大 worker 数。
// 如果单例已创建，会立即应用到当前单例。
func SetDefaultMaxWorkers(maxWorkers int) {
	if maxWorkers <= 0 {
		maxWorkers = 1
	}

	defaultMu.Lock()
	defaultMaxWorkers = maxWorkers
	pool := defaultPool
	defaultMu.Unlock()

	if pool != nil && !pool.IsStopped() {
		pool.Resize(maxWorkers)
	}
}

// Submit 向默认单例池提交任务。
func Submit(task Task) bool {
	return Default().Submit(task)
}

// WaitIdle 等待默认单例池空闲。
func WaitIdle() {
	Default().WaitIdle()
}

// Stop 停止并释放默认单例池。
// 再次调用 Submit/Default 会自动创建新单例。
func Stop() {
	defaultMu.Lock()
	pool := defaultPool
	defaultPool = nil
	defaultMu.Unlock()

	if pool != nil {
		pool.Stop()
	}
}

// DefaultStats 返回默认单例池统计信息。
func DefaultStats() Stats {
	return Default().Stats()
}
