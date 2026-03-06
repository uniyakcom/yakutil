// Package ratelimit 提供高性能令牌桶限速器。
//
// # 设计目标
//
// 专为 yakio 等高并发网络服务设计：
//   - 写路径单次 atomic CAS，无锁，无 goroutine，无 timer
//   - 零分配（允许 Allow/AllowN 使用 locking-free ring buffer）
//   - 支持 Per-IP、全局、每连接等多粒度限速
//
// # 令牌桶算法
//
// 每次调用时懒惰补充令牌（基于时间差 + 速率），
// 避免后台 goroutine，节省调度开销。
// 补充计算使用整数纳秒，避免浮点误差。
//
// # 精度说明
//
// 补充粒度为 1/rate 秒（令牌间隔纳秒数）。
// 极高速率（>1e9/s）时精度受纳秒整除限制，建议每秒不超过 1e8。
//
// # 并发安全
//
// Limiter 可被多 goroutine 安全并发调用，基于 CAS 无锁实现。
// 高竞争场景（如 yakio 每连接限速）建议每连接独立实例，避免热点。
//
// # 典型用法
//
//	// 全局限速：1000 次/秒，突发上限 200
//	rl := ratelimit.New(1000, 200)
//
//	// 每次请求前检查
//	if !rl.Allow() {
//	    conn.Close()
//	    return
//	}
//
//	// 批量消耗（如一次写入多条消息）
//	if !rl.AllowN(5) {
//	    return ErrRateLimited
//	}
//
//	// 与 yakio Per-IP 限速集成
//	rl := ratelimit.New(opts.PerIPMsgRate, opts.PerIPMsgBurst)
//	// 在 OnData 前检查
package ratelimit

import (
	"sync/atomic"
	"time"
)

// state 将 tokens（32bit）和 lastRefill（64bit 时间戳）打包进两个 atomic 字段，
// 避免 atomic.Value 装箱开销。
//
// 精确的复合原子操作通过 CAS 循环实现——先读快照，计算新状态，
// CAS 更新；失败（有竞争）时重试，最多几次即可收敛。
type Limiter struct {
	// tokens 当前可用令牌数（int64 方便带符号计算）
	tokens atomic.Int64
	// lastNano 上次补充令牌的时间（UnixNano，atomic）
	lastNano atomic.Int64

	rate  int64 // 每秒令牌数
	burst int64 // 桶容量（最大令牌数）
	// interval 每个令牌的纳秒间隔（= 1e9 / rate）
	// 预计算避免热路径整除
	interval int64 // clockFn 可注入时钟（nil = time.Now().UnixNano）。
	// 使用 WithClock(coarsetime.NowNano) 可将时钟 Allow 开销降至 ~1ns。
	clockFn func() int64
}

// LimiterOption Limiter 配置选项。
type LimiterOption func(*Limiter)

// WithClock 注入自定义时钟函数（返回 UnixNano）。
//
// 最常用途：将 coarsetime.NowNano 注入以替换 time.Now()，
// 在 Allow/AllowN 热路径将时钟开销从 ~60 ns 降至 ~1 ns：
//
//	rl := ratelimit.New(1000, 200, ratelimit.WithClock(coarsetime.NowNano))
func WithClock(fn func() int64) LimiterOption {
	return func(l *Limiter) { l.clockFn = fn }
}

// New 创建令牌桶限速器。
//
//   - rate：每秒补充令牌数（必须 > 0）
//   - burst：桶容量（突发上限，必须 >= 1）
//
// 常见配置：
//   - 严格限速（无突发）：burst = 1 或 burst = rate
//   - 允许短突发：burst = rate * 2
//   - 每连接消息数限速：ratelimit.New(1000, 50)
//
// 初始令牌数 = burst（满桶），允许开机即突发。
func New(rate, burst int, opts ...LimiterOption) *Limiter {
	if rate <= 0 {
		rate = 1
	}
	if burst <= 0 {
		burst = 1
	}
	l := &Limiter{
		rate:     int64(rate),
		burst:    int64(burst),
		interval: int64(time.Second) / int64(rate),
	}
	for _, opt := range opts {
		opt(l)
	}
	l.tokens.Store(int64(burst)) // 满桶初始化
	l.lastNano.Store(l.now())
	return l
}

// now 返回当前 UnixNano。注入了 clockFn 时使用它，否则回退到 time.Now()。
func (l *Limiter) now() int64 {
	if l.clockFn != nil {
		return l.clockFn()
	}
	return time.Now().UnixNano()
}

// Allow 尝试消耗 1 个令牌。
//
// 返回 true 表示允许通过；false 表示令牌不足，请求应被拒绝或延迟。
// 调用方不应在 false 时自旋，大量拒绝属于预期行为。
//
// 零分配，CAS 无锁，适合高并发热路径。
func (l *Limiter) Allow() bool {
	return l.AllowN(1)
}

// AllowN 尝试一次消耗 n 个令牌。
//
// n <= 0 视为 1。n > burst 永远返回 false（不可能积累到 n 个令牌）。
//
// 零分配，CAS 无锁。
//
// # 并发安全说明
//
// 令牌补充（refill）与令牌扣减（deduct）均通过 CAS 循环完成：
//   - CAS lastNano 抢到"补充权"后，用 CAS 循环原子累加 tokens，
//     避免 Store 覆盖并发完成的扣减操作。
//   - deduct 同样用 CAS 扣减，失败时重试，保证令牌数单调正确。
func (l *Limiter) AllowN(n int) bool {
	if n <= 0 {
		n = 1
	}
	need := int64(n)
	if need > l.burst {
		return false // 永远不可能满足
	}

	now := l.now()

	for {
		last := l.lastNano.Load()
		cur := l.tokens.Load()

		// 计算自上次补充以来应补充的令牌数
		elapsed := now - last
		if elapsed < 0 {
			elapsed = 0 // 时钟回拨保护
		}
		add := elapsed / l.interval
		if add > 0 {
			// 抢补充权：只有一个 goroutine 的 CAS 会成功
			if l.lastNano.CompareAndSwap(last, last+add*l.interval) {
				// 用 CAS 循环安全累加，避免 tokens.Store 覆盖并发的 deduct
				for {
					tok := l.tokens.Load()
					newTok := tok + add
					if newTok > l.burst {
						newTok = l.burst
					}
					if l.tokens.CompareAndSwap(tok, newTok) {
						cur = newTok
						break
					}
					// 另一 goroutine 修改了 tokens（扣减），重试以累加到最新值
				}
			} else {
				// 另一 goroutine 已负责补充，重读最新 tokens
				cur = l.tokens.Load()
			}
		}

		if cur < need {
			return false
		}
		// CAS 扣减：若 tokens 未被其他 goroutine 修改则成功
		if l.tokens.CompareAndSwap(cur, cur-need) {
			return true
		}
		// 竞争失败，重试（通常 1-2 次内收敛）
	}
}

// Tokens 返回当前可用令牌数的快照（不触发补充）。
//
// 仅供诊断/监控使用，不保证调用后 Allow() 一定成功。
func (l *Limiter) Tokens() int64 {
	return l.tokens.Load()
}

// Rate 返回设置的速率（令牌/秒）。
func (l *Limiter) Rate() int64 { return l.rate }

// Burst 返回设置的桶容量。
func (l *Limiter) Burst() int64 { return l.burst }

// Reset 重置令牌数为满桶，并更新时间基准为当前时间。
//
// 适用于连接重置、测试初始化等场景。并发安全。
func (l *Limiter) Reset() {
	l.lastNano.Store(l.now())
	l.tokens.Store(l.burst)
}
