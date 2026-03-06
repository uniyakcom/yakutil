// Package backoff 提供三级自适应退避策略。
//
// 适用于 spin-wait 场景（如无锁队列消费者等待生产者就绪）：
//
//	Phase 1 (n < SpinN):   紧密 CPU 自旋（零开销，最低延迟）
//	Phase 2 (n < SpinN+YieldN): runtime.Gosched()（让出 P 但不阻塞）
//	Phase 3 (n ≥ SpinN+YieldN): 指数退避 sleep（1µs → MaxWait）
//
// 用法：
//
//	var bo backoff.Backoff
//	for !condition {
//	    bo.Spin()
//	}
//	bo.Reset()
package backoff

import (
	"runtime"
	"sync/atomic"
	"time"
)

// spinHint 为 Phase 1 自旋提供 CPU 调度提示。
// 对其进行原子读可以在 SMT（超线程）场景下降低流水线失速和功耗。
var spinHint atomic.Int32

// 默认阶段边界
const (
	DefaultSpinN  = 64
	DefaultYieldN = 64
	DefaultMax    = time.Millisecond
)

// Backoff 三级自适应退避器（值类型，无需构造函数）。
//
// 零值可直接使用（默认参数）。可按需设置 SpinN / YieldN / MaxWait。
// SpinN=0 和 YieldN=0 视为使用默认值（各 64 次）。
// 若需跳过 Phase 1，设 SpinN=1（1 次空自旋可忽略）。
//
// N 计数器到达 uint32 上限后自动饱和，不会回绕。
type Backoff struct {
	N       uint32        // 当前迭代次数（自动递增）
	SpinN   uint32        // Phase 1 上限（0 = 默认 64）
	YieldN  uint32        // Phase 2 额外迭代数（0 = 默认 64）
	MaxWait time.Duration // Phase 3 最大 sleep（0 = 默认 1ms）
}

// Spin 执行一次退避迭代。
func (b *Backoff) Spin() {
	spinN := b.SpinN
	if spinN == 0 {
		spinN = DefaultSpinN
	}
	yieldN := b.YieldN
	if yieldN == 0 {
		yieldN = DefaultYieldN
	}
	maxWait := b.MaxWait
	if maxWait == 0 {
		maxWait = DefaultMax
	}

	n := b.N
	if n < ^uint32(0) {
		b.N = n + 1
	}

	switch {
	case n < spinN:
		// Phase 1: 紧密 CPU 自旋。
		// 读取共享原子变量产生内存屏障提示，等效于向 CPU 信号当前处于自旋等待。
		// x86 SMT 场景可减少流水线 mis-speculation 和过度占用共享资源。
		// 若需真正的 PAUSE 指令，可改用汇编层的 runtime.procyield。
		_ = spinHint.Load()
	case n < spinN+yieldN:
		// Phase 2: 让出处理器
		runtime.Gosched()
	default:
		// Phase 3: 指数退避 sleep
		shift := n - spinN - yieldN
		if shift > 10 {
			shift = 10
		}
		d := time.Microsecond << shift
		if d > maxWait {
			d = maxWait
		}
		time.Sleep(d)
	}
}

// Reset 重置退避状态（条件满足后调用）。
func (b *Backoff) Reset() { b.N = 0 }
