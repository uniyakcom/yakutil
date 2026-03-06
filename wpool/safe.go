package wpool

import (
	"runtime/debug"
	"sync/atomic"
)

// panicSafeRun 执行 task 并捕获 panic，防止单个任务 panic 导致 worker goroutine 永久丢失。
//
// 每次 panic 均递增 count；若 handler 非 nil，每次 panic 时调用 handler（含堆栈快照）。
// handler 在 worker goroutine 内运行，禁止在其内部再次 panic。
//
// 本函数被 Pool.safeRun 和 Stack.safeRun 共同调用，消除重复实现。
func panicSafeRun(task func(), count *atomic.Int64, handler func(any, []byte)) {
	if handler == nil {
		defer func() {
			if r := recover(); r != nil {
				count.Add(1)
			}
		}()
		task()
		return
	}
	defer func() {
		if r := recover(); r != nil {
			count.Add(1)
			handler(r, debug.Stack())
		}
	}()
	task()
}
