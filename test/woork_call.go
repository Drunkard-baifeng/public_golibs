package main

import (
	"fmt"

	"github.com/Drunkard-baifeng/public_golibs/workerpool"
)

// 启动多线程函数
func WorkerPoolCall() {
	taskPool(1, 100)
}

// 停止任务函数
func stopTaskPool() {
	workerpool.Stop()
}

// 修改线程池大小
func resizeTaskPool(workers int) {
	workerpool.SetDefaultMaxWorkers(workers)
}

// 获取线程池状态
func getTaskPoolStats() workerpool.Stats {
	return workerpool.DefaultStats()
}

// 多线程函数
func taskPool(workers int, tasks int) {
	if tasks <= 0 {
		tasks = 1
	}
	if workers > tasks {
		workers = tasks
	}
	// 创建线程池
	workerpool.SetDefaultMaxWorkers(workers)
	workerpool.Default()

	// 投递任务
	for i := 0; i < tasks; i++ {
		taskID := i
		workerpool.Submit(func() {
			executeTask(taskID)
		})
	}

	// 等待完成
	workerpool.WaitIdle()

	// 停止线程池
	workerpool.Stop()
}

// 单个任务函数
func executeTask(i int) {
	fmt.Println("hello world", i)
}
