package workerpool

import (
	"context"
	"sync"
	"sync/atomic"
	"time"
)

// Task 任务函数类型
type Task func()

// Pool worker 池
type Pool struct {
	maxWorkers  int32 // 最大 worker 数
	activeCount int32 // 活跃 worker 数
	taskQueue   chan Task
	ctx         context.Context
	cancel      context.CancelFunc
	stopped     int32

	// 并发控制
	workerMu   sync.Mutex
	workerCond *sync.Cond
}

// New 创建 worker 池
func New(maxWorkers int) *Pool {
	if maxWorkers <= 0 {
		maxWorkers = 1
	}

	ctx, cancel := context.WithCancel(context.Background())

	p := &Pool{
		maxWorkers: int32(maxWorkers),
		taskQueue:  make(chan Task, 10000),
		ctx:        ctx,
		cancel:     cancel,
	}
	p.workerCond = sync.NewCond(&p.workerMu)

	// 启动调度器
	go p.dispatcher()

	return p
}

// dispatcher 任务调度器
func (p *Pool) dispatcher() {
	for {
		select {
		case <-p.ctx.Done():
			return
		case task, ok := <-p.taskQueue:
			if !ok {
				return
			}
			if atomic.LoadInt32(&p.stopped) == 1 {
				return
			}
			// 等待有空闲 worker
			p.workerMu.Lock()
			for atomic.LoadInt32(&p.activeCount) >= atomic.LoadInt32(&p.maxWorkers) {
				p.workerCond.Wait()
				if atomic.LoadInt32(&p.stopped) == 1 {
					p.workerMu.Unlock()
					return
				}
			}
			if atomic.LoadInt32(&p.stopped) == 1 {
				p.workerMu.Unlock()
				return
			}
			atomic.AddInt32(&p.activeCount, 1)
			p.workerMu.Unlock()

			// 启动 worker
			go p.runWorker(task)
		}
	}
}

// runWorker 执行任务的 worker
func (p *Pool) runWorker(task Task) {
	defer func() {
		p.workerMu.Lock()
		atomic.AddInt32(&p.activeCount, -1)
		p.workerCond.Signal()
		p.workerMu.Unlock()
	}()

	// 执行任务（带 panic 恢复）
	func() {
		defer func() { recover() }()
		task()
	}()
}

// Submit 提交任务
func (p *Pool) Submit(task Task) bool {
	if atomic.LoadInt32(&p.stopped) == 1 {
		return false
	}

	select {
	case p.taskQueue <- task:
		return true
	case <-p.ctx.Done():
		return false
	}
}

// Resize 动态调整最大 worker 数
func (p *Pool) Resize(newSize int) {
	if newSize <= 0 {
		newSize = 1
	}
	atomic.StoreInt32(&p.maxWorkers, int32(newSize))
	p.workerCond.Broadcast()
}

// MaxWorkers 获取最大 worker 数
func (p *Pool) MaxWorkers() int {
	return int(atomic.LoadInt32(&p.maxWorkers))
}

// ActiveWorkers 获取活跃 worker 数
func (p *Pool) ActiveWorkers() int {
	return int(atomic.LoadInt32(&p.activeCount))
}

// IdleWorkers 获取空闲 worker 数
func (p *Pool) IdleWorkers() int {
	idle := p.MaxWorkers() - p.ActiveWorkers()
	if idle < 0 {
		idle = 0
	}
	return idle
}

// QueueSize 获取队列中等待的任务数
func (p *Pool) QueueSize() int {
	return len(p.taskQueue)
}

// IsIdle 判断是否完全空闲
func (p *Pool) IsIdle() bool {
	return p.ActiveWorkers() == 0 && p.QueueSize() == 0
}

// WaitIdle 阻塞等待空闲
func (p *Pool) WaitIdle() {
	for !p.IsIdle() {
		time.Sleep(50 * time.Millisecond)
	}
}

// Stop 停止池子
func (p *Pool) Stop() {
	if !atomic.CompareAndSwapInt32(&p.stopped, 0, 1) {
		return
	}

	p.cancel()
	p.workerCond.Broadcast()

	// 清空队列
	for {
		select {
		case <-p.taskQueue:
		default:
			return
		}
	}
}

// IsStopped 是否已停止
func (p *Pool) IsStopped() bool {
	return atomic.LoadInt32(&p.stopped) == 1
}

// Stats 统计信息
type Stats struct {
	MaxWorkers    int
	ActiveWorkers int
	IdleWorkers   int
	QueueSize     int
	Stopped       bool
}

func (p *Pool) Stats() Stats {
	return Stats{
		MaxWorkers:    p.MaxWorkers(),
		ActiveWorkers: p.ActiveWorkers(),
		IdleWorkers:   p.IdleWorkers(),
		QueueSize:     p.QueueSize(),
		Stopped:       p.IsStopped(),
	}
}
