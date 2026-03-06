// Package semaphore 提供计数信号量（有界并发控制）。
//
// # 设计目标
//
// 面向 yakio MaxConns / yakdb 连接池等需要精确并发上限的场景：
//   - 支持非阻塞尝试（TryAcquire/TryAcquireN）与带超时上下文（AcquireContext）
//   - TryAcquireN 单次 CAS 循环，O(1)，无锁、无 Mutex
//   - Count/Cap 提供运行时诊断，用于 Prometheus 指标暴露
//   - 零外部依赖
//
// # 实现说明
//
// 采用 "atomic.Int64 + 通知 channel" 双轨设计：
//   - avail（atomic.Int64）是可用许可数，唯一可信源，初始值 = cap。
//   - notify（cap-缓冲 channel）仅用于唤醒阻塞 goroutine，不承载许可语义。
//
// TryAcquire / TryAcquireN 走 CAS 快路径，无锁。
// Acquire / AcquireContext 先尝试 CAS，失败时阻塞在 notify channel，
// 被 Release 唤醒后重试——无自旋、无忙等。
//
// # 对比 sync.WaitGroup / Mutex
//
// sync.WaitGroup  ──  等待完成，无并发上限
// sync.Mutex      ──  互斥锁，最大并发=1
// semaphore.New(n)──  有界并发，最大并发=n
//
// # 典型用法（yakio MaxConns）
//
// sem := semaphore.New(opts.MaxConns)
//
// // 每条新连接进入时
//
//	if !sem.TryAcquire() {
//	   conn.Close()
//	   return ErrTooManyConns
//	}
//
// defer sem.Release()
//
// // 带 context 的阻塞等待（客户端侧 dial 控制）
//
//	if err := sem.AcquireContext(ctx); err != nil {
//	   return err // ctx cancelled or deadline exceeded
//	}
//
// defer sem.Release()
//
// // 监控暴露
// metrics.ActiveConns.Set(float64(sem.Count()))
//
// # 并发安全
//
// 所有方法均可被多 goroutine 安全并发调用。
package semaphore

import (
	"context"
	"sync/atomic"
)

// Semaphore 是计数信号量。
//
// 零值不可用，请通过 New 构造。
//
// 实现采用 "atomic.Int64 + 唤醒 channel" 双轨设计：
//   - avail（atomic.Int64）是可用许可数，唯一可信源，初始值 = cap。
//   - wake（cap 缓冲 channel）仅用于唤醒阻塞 goroutine，不承载许可语义。
//
// TryAcquire / TryAcquireN 走 CAS 快路径，O(1) 无锁。
// Acquire / AcquireContext 先尝试 CAS，失败时阻塞在 wake channel，
// 被 Release 唤醒后重试——无自旋、无忙等。
type Semaphore struct {
	avail atomic.Int64  // 可用许可数，区间 [0, cap]
	wake  chan struct{} // 唤醒通道；Release 写入，阻塞中的 Acquire 读取
	cap   int           // 容量（不变）
}

// New 创建最大并发数为 n 的信号量。
//
// n 必须 >= 1，否则 panic（防止误用）。
// 初始状态：0 个持有者（全部空闲）。
func New(n int) *Semaphore {
	if n < 1 {
		panic("semaphore: capacity must be >= 1")
	}
	s := &Semaphore{
		wake: make(chan struct{}, n),
		cap:  n,
	}
	s.avail.Store(int64(n))
	return s
}

// Acquire 阻塞直到成功获取一个许可。
//
// 若当前持有数已达上限，调用方 goroutine 被挂起（Go runtime 调度，无自旋）。
// 无法取消；需要超时控制请使用 AcquireContext。
func (s *Semaphore) Acquire() {
	for {
		if old := s.avail.Load(); old > 0 && s.avail.CompareAndSwap(old, old-1) {
			return
		}
		<-s.wake // 等待 Release 唤醒后重试 CAS
	}
}

// TryAcquire 尝试非阻塞获取一个许可。
//
// 立即返回：true 表示成功获取；false 表示当前已满，调用方应拒绝或排队。
// 适合 yakio 新连接入口：超过上限时快速拒绝，避免 goroutine 堆积。
func (s *Semaphore) TryAcquire() bool {
	for {
		old := s.avail.Load()
		if old <= 0 {
			return false
		}
		if s.avail.CompareAndSwap(old, old-1) {
			return true
		}
	}
}

// TryAcquireN 原子批量获取 n 个许可（非阻塞）。
//
// 若当前剩余可用许可 >= n，则原子占用 n 个许可并返回 true；
// 否则不消耗任何许可，立即返回 false。
//
// Mutex 保护批量检查和批量消费在同一临界区内完成，防止两个并发调用
// 都通过“剩余 >= n”校验后竞争同一批令牌。
//
// n 必须 >= 1；n > Cap() 时永远返回 false。
func (s *Semaphore) TryAcquireN(n int) bool {
	if n < 1 || n > s.cap {
		return false
	}
	nn := int64(n)
	for {
		old := s.avail.Load()
		if old < nn {
			return false
		}
		if s.avail.CompareAndSwap(old, old-nn) {
			return true
		}
	}
}

// AcquireContext 阻塞直到成功获取许可，或 ctx 被取消/超时。
//
// 返回 nil 表示成功获取；返回 ctx.Err() 表示等待被取消。
// 成功获取后 ctx 再被取消，许可仍有效，调用方需正常 Release。
func (s *Semaphore) AcquireContext(ctx context.Context) error {
	for {
		if old := s.avail.Load(); old > 0 && s.avail.CompareAndSwap(old, old-1) {
			return nil
		}
		select {
		case <-s.wake:
			// 被唤醒，重试 CAS
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// Release 释放一个许可。
//
// 每次成功的 Acquire/TryAcquire/AcquireContext 后都必须对应一次 Release。
// 多余的 Release（超过已 Acquire 次数）会 panic。
func (s *Semaphore) Release() {
	if nw := s.avail.Add(1); nw > int64(s.cap) {
		s.avail.Add(-1)
		panic("semaphore: Release called without a matching Acquire")
	}
	// 尝试唤醒一个阻塞在 wake 的 goroutine；若无阻塞方则非阻塞放弃
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

// ReleaseN 批量释放 n 个许可，与 TryAcquireN 配对使用。
//
// 每次成功的 TryAcquireN(n) 后应调用 ReleaseN(n)。
// n 必须 >= 1 且 <= 当前持有量，否则 panic。
func (s *Semaphore) ReleaseN(n int) {
	if n < 1 {
		panic("semaphore: ReleaseN: n must be >= 1")
	}
	if nw := s.avail.Add(int64(n)); nw > int64(s.cap) {
		s.avail.Add(-int64(n))
		panic("semaphore: ReleaseN called without matching Acquire")
	}
	// 唤醒最多 n 个阻塞 goroutine
	for i := 0; i < n; i++ {
		select {
		case s.wake <- struct{}{}:
		default:
			return
		}
	}
}

// Count 返回当前已持有的许可数（活跃并发数）。
//
// 仅供诊断使用，不保证调用返回后状态不变。
// 适合 Prometheus gauge 采集：
//
// activeConns.Set(float64(sem.Count()))
func (s *Semaphore) Count() int {
	return s.cap - int(s.avail.Load())
}

// Cap 返回信号量的最大容量（构造时指定的 n）。
//
// 不可变，可安全并发读取。
func (s *Semaphore) Cap() int {
	return s.cap
}

// Available 返回当前剩余可用许可数（= Cap - Count）。
//
// 适合决策逻辑：if sem.Available() < threshold { reject }
func (s *Semaphore) Available() int {
	return int(s.avail.Load())
}
