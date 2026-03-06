// Package wpool 提供高性能 goroutine 工作池。
//
// # 类型说明
//
// Pool（FIFO）：channel 驱动的 goroutine 工作池，任务调度均衡，
// 适用 CPU 密集或任务耗时较长的场景。
//
// Stack（FILO）：互斥量+切片栈驱动，最近使用的 worker 优先复用，
// 充分利用 CPU cache 亲和性，适用网络 IO reactor、短耗时任务分发等
// 对延迟敏感的场景。
//
// Adaptive：在 Pool 之上提供基于负载的自适应 worker 数伸缩。
//
// # 通用接口
//
// Pool、Stack、Adaptive 均实现 Submitter 接口，可互换使用。
//
// # 注意
//
// 所有类型持有 goroutine 资源，使用完毕后必须调用 Stop()，
// 否则 goroutine 将永久泄漏。建议配合 defer p.Stop() 使用。
package wpool

import (
	"runtime"
	"sync"
	"sync/atomic"
	"time"
)

// poolTimerPool 为 Pool.SubmitTimeout 热路径提供 *time.Timer 复用。
// 将每次调用从 3 alloc/248 B 降至 0 alloc/0 B。
var poolTimerPool = sync.Pool{
	New: func() any {
		t := time.NewTimer(0)
		// 进池前确保 timer 停止且 channel 已排空
		if !t.Stop() {
			select {
			case <-t.C:
			default:
			}
		}
		return t
	},
}

// Submitter goroutine 工作池通用接口。
// Pool、Stack、Adaptive 均实现此接口，调用方无需关心底层实现。
type Submitter interface {
	// Submit 提交任务，阻塞直到任务被接受或池已停止。
	// Pool：队列满时阻塞直到队列有空间；Stack：worker 达上限时阻塞直到有空闲 worker。
	// 返回 false 表示池已停止。
	Submit(task func()) bool

	// TrySubmit 非阻塞提交。worker 达到上限或池已停止时返回 false。
	TrySubmit(task func()) bool

	// Running 返回当前活跃 worker 数。
	Running() int

	// Stop 优雅停止池。等待所有 worker 完成当前任务后返回。
	Stop()

	// PanicCount 返回工作池生命周期内任务发生 panic 的总次数。
	// 无论是否设置 WithPanicHandler，每次 panic 均递增。
	// 可用于 Prometheus 等监控系统上报 worker panic 速率。
	PanicCount() int64
}

// DefaultIdleTimeout worker 空闲超时默认值。
const DefaultIdleTimeout = 5 * time.Second

// TimedSubmitter 扩展 Submitter，增加限时提交能力。
// Pool 与 Stack 均实现此接口，可在需要背压超时控制的调用方使用。
type TimedSubmitter interface {
	Submitter
	// SubmitTimeout 提交任务，至多等待 timeout 时间。
	// 在 timeout 内任务被接受返回 true；超时或池已停止返回 false。
	// Pool：等待队列空位；Stack：等待空闲 worker。
	SubmitTimeout(task func(), timeout time.Duration) bool
}

// PoolOption Pool 配置选项。
type PoolOption func(*Pool)

// WithPanicHandler 设置任务 panic 时的回调。
// 默认行为：panic 被静默恢复（worker 不退出）。
// fn 第一个参数为 recover() 的返回值，第二个为 goroutine 堆栈快照（runtime/debug.Stack()）。
//
// fn 在 worker goroutine 内调用，禁止在 fn 内再次 panic。
func WithPanicHandler(fn func(any, []byte)) PoolOption {
	return func(p *Pool) { p.panicHandler = fn }
}

// Pool FIFO goroutine 工作池。并发安全。
//
// 底层使用 channel 队列分发任务，调度均衡，
// 适用于 CPU 密集型或任务耗时较长的场景。
// 对 CPU cache 亲和性有严格要求时，改用 Stack（FILO）。
type Pool struct {
	maxSize      atomic.Int32
	running      atomic.Int32
	idle         atomic.Int32
	panicCount   atomic.Int64 // 生命周期内任务 panic 总次数
	tasks        chan func()
	stop         chan struct{}
	stopped      atomic.Bool
	idleTTL      time.Duration
	panicHandler func(any, []byte) // 不可变（NewPool 后只读），无需加锁
	once         sync.Once
	wg           sync.WaitGroup
}

// NewPool 创建工作池。size 为初始 worker 数，idleTTL 为空闲超时（0=默认 5s）。
func NewPool(size int, idleTTL time.Duration, opts ...PoolOption) *Pool {
	if size <= 0 {
		size = runtime.GOMAXPROCS(0)
	}
	if idleTTL <= 0 {
		idleTTL = DefaultIdleTimeout
	}
	p := &Pool{
		tasks:   make(chan func(), size*4),
		stop:    make(chan struct{}),
		idleTTL: idleTTL,
	}
	for _, opt := range opts {
		opt(p)
	}
	p.maxSize.Store(int32(size))
	p.spawn(size)
	return p
}

// Submit 提交任务到池。池已停止时返回 false。
// 如果所有 worker 忙碌且队列未满，任务入队等待。
func (p *Pool) Submit(task func()) bool {
	if p.stopped.Load() {
		return false
	}
	select {
	case p.tasks <- task:
		return true
	case <-p.stop:
		return false
	}
}

// TrySubmit 非阻塞提交。队列满或池停止时返回 false。
func (p *Pool) TrySubmit(task func()) bool {
	if p.stopped.Load() {
		return false
	}
	select {
	case p.tasks <- task:
		return true
	default:
		return false
	}
}

// SubmitTimeout 提交任务，至多等待 timeout 后仍无队列空间则返回 false。
// 实现 TimedSubmitter 接口，适合有明确背压截止时间的调用方（如 HTTP 请求处理）。
//
// 热路径通过 poolTimerPool 复用 *time.Timer，相比每次 time.NewTimer
// 将 alloc 从 3 次/248 B 降至 0 次/0 B。
func (p *Pool) SubmitTimeout(task func(), timeout time.Duration) bool {
	if p.stopped.Load() {
		return false
	}
	t := poolTimerPool.Get().(*time.Timer)
	t.Reset(timeout)
	defer func() {
		// 归还前确保 timer 干净（若已触发则排空 channel）
		if !t.Stop() {
			select {
			case <-t.C:
			default:
			}
		}
		poolTimerPool.Put(t)
	}()
	select {
	case p.tasks <- task:
		return true
	case <-t.C:
		return false
	case <-p.stop:
		return false
	}
}

// Resize 动态调整 worker 数。n < 当前数时多余 worker 在空闲后自动退出。
func (p *Pool) Resize(n int) {
	if n <= 0 {
		n = 1
	}
	old := int(p.maxSize.Swap(int32(n)))
	if n > old {
		p.spawn(n - old)
	}
	// n < old 时 worker 在下次空闲检查时自动退出
}

// Running 返回当前活跃 worker 数。
func (p *Pool) Running() int { return int(p.running.Load()) }

// Idle 返回当前空闲 worker 数。
func (p *Pool) Idle() int { return int(p.idle.Load()) }

// QueueLen 返回任务队列当前排队数。
// 共同 Channel len/cap 语义，并发安全无锁。
func (p *Pool) QueueLen() int { return len(p.tasks) }

// QueueCap 返回任务队列的容量。
func (p *Pool) QueueCap() int { return cap(p.tasks) }

// PanicCount 返回工作池生命周期内任务发生 panic 的总次数。
// 此计数可用于 Prometheus 监控指标接入。
func (p *Pool) PanicCount() int64 { return p.panicCount.Load() }

// Stop 优雅停止池。等待所有 worker 完成当前任务。
func (p *Pool) Stop() {
	p.once.Do(func() {
		p.stopped.Store(true)
		close(p.stop)
		p.wg.Wait()
	})
}

func (p *Pool) spawn(n int) {
	for i := 0; i < n; i++ {
		p.running.Add(1)
		p.wg.Add(1)
		go p.worker()
	}
}

func (p *Pool) worker() {
	// retired 标志：CAS 预扣 running 成功时置 true，跳过 defer 中的二次 Add(-1)。
	// 这确保精确缩容：只有恰好抢到退出名额的 worker 才退出，
	// 避免多个 worker 同时看到 running > maxSize 而全部退出（降为 0）。
	retired := false
	defer func() {
		if !retired {
			p.running.Add(-1)
		}
		p.wg.Done()
	}()
	timer := time.NewTimer(p.idleTTL)
	defer timer.Stop()

	for {
		// 精确缩容：CAS 竞争退出名额，每次只减少一个 worker，不多不少。
		for {
			cur := p.running.Load()
			if cur <= p.maxSize.Load() {
				break
			}
			if p.running.CompareAndSwap(cur, cur-1) {
				retired = true
				return
			}
			// CAS 失败说明其他 worker 已抢先退出，重新检查
		}
		// ↑ CAS 循环结束后 running ≤ maxSize，本 worker 继续服务

		// 修复：Reset 前先 drain 已触发的 channel，防止立即误触发。
		// 参见 https://pkg.go.dev/time#Timer.Reset
		if !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
		}
		timer.Reset(p.idleTTL)
		p.idle.Add(1)

		select {
		case task, ok := <-p.tasks:
			p.idle.Add(-1)
			if !ok {
				return
			}
			p.safeRun(task)
		case <-timer.C:
			p.idle.Add(-1)
			// 空闲超时时同样用 CAS 精确缩容，与循环顶部逻辑保持一致。
			for {
				cur := p.running.Load()
				if cur <= p.maxSize.Load() {
					break // 未超出上限，继续等待
				}
				if p.running.CompareAndSwap(cur, cur-1) {
					retired = true
					return
				}
			}
			// 未抢到退出名额，继续空闲等待（保持最低 worker 数）
		case <-p.stop:
			p.idle.Add(-1)
			// drain remaining tasks
			for {
				select {
				case task := <-p.tasks:
					p.safeRun(task)
				default:
					return
				}
			}
		}
	}
}

// safeRun 执行任务并捕获 panic，防止单个任务 panic 导致 worker 永久丢失。
// 委托给 panicSafeRun 共享实现，消除与 Stack.safeRun 的重复代码。
func (p *Pool) safeRun(task func()) {
	panicSafeRun(task, &p.panicCount, p.panicHandler)
}

// ─── Adaptive 自适应包装器 ──────────────────────────────────────────────────

// Adaptive 在 Pool 之上提供基于负载的自适应 worker 伸缩。
//
// 周期性采样任务队列水位和 idle worker 比例：
//   - 队列水位 > 75%：扩容（+25% worker，不超过 Max）
//   - idle > 50% running：缩容（-25% worker，不低于 Min）
type Adaptive struct {
	pool   *Pool
	min    int32
	max    int32
	tick   time.Duration
	stopCh chan struct{}
	once   sync.Once
}

// NewAdaptive 创建自适应工作池。
// min/max 为 worker 数范围，tick 为采样周期（0=默认 500ms）。
//
// Adaptive 内部启动 monitor goroutine，使用完毕后必须调用 Stop() 释放资源，
// 否则 goroutine 永远不会退出。
func NewAdaptive(min, max int, tick time.Duration, idleTTL time.Duration) *Adaptive {
	if min <= 0 {
		min = 1
	}
	if max < min {
		max = min
	}
	if tick <= 0 {
		tick = 500 * time.Millisecond
	}
	a := &Adaptive{
		pool:   NewPool(min, idleTTL),
		min:    int32(min),
		max:    int32(max),
		tick:   tick,
		stopCh: make(chan struct{}),
	}
	go a.monitor()
	return a
}

// Submit 提交任务。
func (a *Adaptive) Submit(task func()) bool { return a.pool.Submit(task) }

// TrySubmit 非阻塞提交。
func (a *Adaptive) TrySubmit(task func()) bool { return a.pool.TrySubmit(task) }

// Running 当前活跃 worker 数。
func (a *Adaptive) Running() int { return a.pool.Running() }

// PanicCount 返回底层 Pool 的任务 panic 总次数。
func (a *Adaptive) PanicCount() int64 { return a.pool.PanicCount() }

// Stop 停止自适应监控和底层池。
func (a *Adaptive) Stop() {
	a.once.Do(func() {
		close(a.stopCh)
		a.pool.Stop()
	})
}

func (a *Adaptive) monitor() {
	ticker := time.NewTicker(a.tick)
	defer ticker.Stop()
	for {
		select {
		case <-a.stopCh:
			return
		case <-ticker.C:
			a.adjust()
		}
	}
}

func (a *Adaptive) adjust() {
	running := a.pool.Running()
	idle := a.pool.Idle()
	qLen := a.pool.QueueLen() // 改用公开方法，不再直接访问私有字段
	qCap := a.pool.QueueCap()

	var target int32

	switch {
	case qCap > 0 && qLen*4 > qCap*3: // 队列水位 > 75%
		delta := int32(running) / 4
		if delta < 1 {
			delta = 1
		}
		target = int32(running) + delta
	case running > 0 && idle*2 > running: // idle > 50%
		delta := int32(running) / 4
		if delta < 1 {
			delta = 1
		}
		target = int32(running) - delta
	default:
		return
	}

	if target < a.min {
		target = a.min
	}
	if target > a.max {
		target = a.max
	}
	if target != int32(running) {
		a.pool.Resize(int(target))
	}
}
