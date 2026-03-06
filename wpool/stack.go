package wpool

import (
	"sync"
	"sync/atomic"
	"time"
)

// timeoutToken 为 Stack.SubmitTimeout 热路径提供零 alloc 超时状态。
//
// 通过 tokenPool 复用对象，嵌入 generation 计数器防止"延迟触发"的 AfterFunc
// 在 token 归还 pool 后写入 expired 字段（ABA 竞态）：
//
//  1. 调用取 token，捕获 curGen = tok.gen.Load()
//  2. AfterFunc 回调先校验 tok.gen.Load() == curGen 再写 expired
//  3. 函数返回前 tok.gen.Add(1) 使飞行中的回调失效，再归还 pool
//
// 性能收益：var expired atomic.Bool 逃逸堆（1 alloc/49 B）→ 0 alloc/0 B。
type timeoutToken struct {
	expired atomic.Bool
	gen     atomic.Uint32
}

var tokenPool = sync.Pool{New: func() any { return new(timeoutToken) }}

// DefaultStackIdleTimeout Stack worker 空闲超时默认值。
const DefaultStackIdleTimeout = 10 * time.Second

// stackWorkerChan 单个 FILO worker 的通信通道。
//
// 用 capacity=1 的 chan 传递任务，减少 goroutine 唤醒延迟；
// 通过 sync.Pool 复用对象，降低 GC 压力。
type stackWorkerChan struct {
	ch      chan func()
	lastUse time.Time
}

// Stack FILO（First-In-Last-Out）goroutine 工作池。
//
// 与 Pool（FIFO channel）的核心区别：
//   - Stack 使用互斥量 + 切片栈，最近使用的 worker 优先复用。
//   - FILO 语义使热 goroutine 的栈帧、TLB 条目仍驻留 CPU cache，
//     对短耗时任务（网络 IO reactor 分发、协议解析等）有 10~30% 延迟收益。
//   - 适合：reactor IO worker 池、RESP 命令处理器等延迟敏感场景。
//   - 不适合：任务耗时差异极大的场景（长任务占满所有 worker，短任务排队）。
//
// 设计细节：
//   - per-worker chan(capacity=1) 避免阻塞 Submit 和 workerFunc。
//   - sync.Pool 复用 stackWorkerChan，减少 GC 分配。
//   - sync.Cond 在 worker 达到上限时阻塞 Submit（零 CPU 忙等）。
//   - 后台 cleaner goroutine 周期回收空闲超时的 worker。
//
// 实现 Submitter 接口，可与 Pool / Adaptive 互换。
type Stack struct {
	// 字段按对齐大小降序排列，消除内部填充

	chanPool sync.Pool // stackWorkerChan 对象池
	mu       sync.Mutex
	cond     *sync.Cond     // 等待空闲 worker（maxWorkers 限制时使用）
	wg       sync.WaitGroup // 跟踪所有 worker goroutine，用于 Stop() 悪尽等待
	stop     chan struct{}
	done     chan struct{}

	ready        []*stackWorkerChan // 空闲 worker 栈（FILO，mu 保护）
	maxIdle      time.Duration
	panicHandler func(any, []byte) // 不可变（NewStack 后只读），无需加锁

	panicCount atomic.Int64 // 生命周期内任务 panic 总次数
	maxWorkers int32
	running    atomic.Int32
	stopped    atomic.Bool
}

// StackOption Stack 配置选项。
type StackOption func(*Stack)

// WithStackPanicHandler 设置任务 panic 时的回调。
// fn 第一个参数为 recover() 的返回值，第二个为 goroutine 堆栈快照（runtime/debug.Stack()）。
// 默认行为：panic 被静默恢复。
//
// fn 在 worker goroutine 内调用，禁止在 fn 内再次 panic。
func WithStackPanicHandler(fn func(any, []byte)) StackOption {
	return func(s *Stack) { s.panicHandler = fn }
}

// NewStack 创建 FILO 工作池。
//
//	maxWorkers: 最大并发 worker 数（0 = 不限制，按需启动）
//	maxIdle:    worker 空闲超时，超时后自动回收（0 = 默认 10s）
//
// 返回的 Stack 实现 Submitter 接口。
func NewStack(maxWorkers int, maxIdle time.Duration, opts ...StackOption) *Stack {
	if maxIdle <= 0 {
		maxIdle = DefaultStackIdleTimeout
	}
	s := &Stack{
		maxWorkers: int32(maxWorkers),
		maxIdle:    maxIdle,
		stop:       make(chan struct{}),
		done:       make(chan struct{}),
	}
	for _, opt := range opts {
		opt(s)
	}
	s.cond = sync.NewCond(&s.mu)
	s.chanPool.New = func() any {
		return &stackWorkerChan{ch: make(chan func(), 1)}
	}
	go s.cleaner()
	return s
}

// Submit 提交任务到池。阻塞直到有可用 worker 或池已停止。
// 返回 false 表示池已停止。
func (s *Stack) Submit(task func()) bool {
	if s.stopped.Load() {
		return false
	}
	wc := s.getWorker()
	if wc == nil {
		return false
	}
	wc.ch <- task
	return true
}

// TrySubmit 非阻塞提交。
// 有空闲 worker 或可新建 worker（未达上限）时返回 true；
// 全部忙碌且达到 maxWorkers 上限时返回 false。
func (s *Stack) TrySubmit(task func()) bool {
	if s.stopped.Load() {
		return false
	}
	wc := s.tryGetWorker()
	if wc == nil {
		return false
	}
	wc.ch <- task
	return true
}

// SubmitTimeout 提交任务，至多等待 timeout 后仍无空闲 worker 则返回 false。
// 实现 TimedSubmitter 接口。
//
// 实现：复用 tokenPool 中的 timeoutToken，generation counter 防止
// 延迟触发的 AfterFunc 在 token 归还后写入 expired（ABA 竞态）。
// 热路径 0 alloc/0 B（原 atomic.Bool 逃逸方案：1 alloc/49 B）。
func (s *Stack) SubmitTimeout(task func(), timeout time.Duration) bool {
	if s.stopped.Load() {
		return false
	}
	// 快速路径：立即可获取 worker
	if wc := s.tryGetWorker(); wc != nil {
		wc.ch <- task
		return true
	}
	if timeout <= 0 {
		return false
	}

	tok := tokenPool.Get().(*timeoutToken)
	tok.expired.Store(false)
	curGen := tok.gen.Load() // 捕获当前 generation

	t := time.AfterFunc(timeout, func() {
		// 只有 generation 未变（token 尚未归还 pool）才写 expired
		if tok.gen.Load() == curGen {
			tok.expired.Store(true)
			s.cond.Broadcast()
		}
	})
	defer func() {
		t.Stop()
		tok.gen.Add(1) // 使飞行中的 AfterFunc 回调失效
		tokenPool.Put(tok)
	}()

	s.mu.Lock()
	for {
		// 优先检查 ready：时间已到但恰好有 worker 被归还时不应丢失
		n := len(s.ready) - 1
		if n >= 0 {
			wc := s.ready[n]
			s.ready[n] = nil
			s.ready = s.ready[:n]
			s.mu.Unlock()
			wc.ch <- task
			return true
		}
		if s.stopped.Load() || tok.expired.Load() {
			s.mu.Unlock()
			return false
		}
		s.cond.Wait()
	}
}

// Running 返回当前活跃 worker 数。
func (s *Stack) Running() int { return int(s.running.Load()) }

// PanicCount 返回工作池生命周期内任务发生 panic 的总次数。
// 此计数可用于 Prometheus 监控指标接入。
func (s *Stack) PanicCount() int64 { return s.panicCount.Load() }

// Stop 优雅停止池。等待所有 worker 完成当前任务后返回。
// 多次调用安全（幂等）。
func (s *Stack) Stop() {
	if !s.stopped.CompareAndSwap(false, true) {
		return
	}
	close(s.stop)

	// 唤醒所有待 cond.Wait 的 Submit 调用
	s.cond.Broadcast()

	// 向所有空闲 worker 发送退出信号
	s.mu.Lock()
	for _, wc := range s.ready {
		wc.ch <- nil
	}
	s.ready = s.ready[:0]
	s.mu.Unlock()

	<-s.done    // 等待 cleaner goroutine 退出（cleaner 可能也在发退出信号）
	s.wg.Wait() // 等待所有 worker goroutine 完全退出
}

// ─── 内部方法 ────────────────────────────────────────────────────────────────

// getWorker 获取可用 worker（FILO），无空闲时新建，达上限时阻塞等待。
func (s *Stack) getWorker() *stackWorkerChan {
	// 优先从空闲栈取（FILO）
	s.mu.Lock()
	n := len(s.ready) - 1
	if n >= 0 {
		wc := s.ready[n]
		s.ready[n] = nil // 防止内存泄漏
		s.ready = s.ready[:n]
		s.mu.Unlock()
		return wc
	}
	s.mu.Unlock()

	// 无空闲 worker：尝试新建
	if s.maxWorkers > 0 {
		for {
			cur := s.running.Load()
			if cur >= s.maxWorkers {
				// 达到上限：阻塞等待 worker 归还
				return s.waitForWorker()
			}
			if s.running.CompareAndSwap(cur, cur+1) {
				break
			}
		}
	} else {
		s.running.Add(1)
	}

	wc := s.chanPool.Get().(*stackWorkerChan)
	s.wg.Add(1)
	go s.workerFunc(wc)
	return wc
}

// tryGetWorker 非阻塞版 getWorker：无可用 worker 时直接返回 nil。
func (s *Stack) tryGetWorker() *stackWorkerChan {
	// 从空闲栈取
	s.mu.Lock()
	n := len(s.ready) - 1
	if n >= 0 {
		wc := s.ready[n]
		s.ready[n] = nil
		s.ready = s.ready[:n]
		s.mu.Unlock()
		return wc
	}
	// 判断能否新建
	if s.maxWorkers > 0 && s.running.Load() >= s.maxWorkers {
		s.mu.Unlock()
		return nil
	}
	s.mu.Unlock()

	// CAS 抢占新建名额
	if s.maxWorkers > 0 {
		for {
			cur := s.running.Load()
			if cur >= s.maxWorkers {
				return nil
			}
			if s.running.CompareAndSwap(cur, cur+1) {
				break
			}
		}
	} else {
		s.running.Add(1)
	}

	wc := s.chanPool.Get().(*stackWorkerChan)
	s.wg.Add(1)
	go s.workerFunc(wc)
	return wc
}

// waitForWorker 阻塞等待直到有空闲 worker 归还到栈（使用 sync.Cond 零 CPU 忙等）。
func (s *Stack) waitForWorker() *stackWorkerChan {
	s.mu.Lock()
	defer s.mu.Unlock()
	for {
		if s.stopped.Load() {
			return nil
		}
		n := len(s.ready) - 1
		if n >= 0 {
			wc := s.ready[n]
			s.ready[n] = nil
			s.ready = s.ready[:n]
			return wc
		}
		s.cond.Wait()
	}
}

// workerFunc worker goroutine 主循环。
//
// 任务完成后将自身归还到 FILO 栈，并通过 cond.Signal 唤醒等待的 Submit。
func (s *Stack) workerFunc(wc *stackWorkerChan) {
	defer s.wg.Done()
	defer s.running.Add(-1)

	for task := range wc.ch {
		if task == nil {
			// 收到退出信号（cleaner 或 Stop 发出）
			break
		}
		s.safeRun(task)

		// 快速路径：stopped 已置位则直接退出，无需加锁。
		if s.stopped.Load() {
			break
		}

		// 慢路径：在锁内再次检查 stopped，消除以下竞态：
		//   1. worker 读 stopped = false（锁外）
		//   2. Stop() 设置 stopped + 扫描 s.ready（已错过此 worker）
		//   3. worker 推入 s.ready → 永久阻塞（goroutine 泄漏）
		// 因为 Stop() 也需持 s.mu 才能扫描 s.ready，二者互斥。
		wc.lastUse = time.Now()
		s.mu.Lock()
		if s.stopped.Load() {
			s.mu.Unlock()
			break
		}
		s.ready = append(s.ready, wc)
		s.cond.Signal() // 唤醒一个等待 worker 的 Submit 调用
		s.mu.Unlock()
	}

	// 归还 stackWorkerChan 到 sync.Pool，减少 GC 压力
	wc.ch = drainChan(wc.ch) // 清空残余任务（防止下次复用时意外触发）
	s.chanPool.Put(wc)
}

// drainChan 清空 channel 中残余的任务，保证复用安全。
// 仅在 worker 退出时调用，此时 channel 中最多有 1 个值（capacity=1）。
func drainChan(ch chan func()) chan func() {
	for {
		select {
		case <-ch:
		default:
			return ch
		}
	}
}

// cleaner 后台清理空闲超时的 worker goroutine。
//
// 每 maxIdle/2 扫描一次，向超时 worker 发送退出信号。
func (s *Stack) cleaner() {
	defer close(s.done)

	ticker := time.NewTicker(s.maxIdle / 2)
	defer ticker.Stop()

	for {
		select {
		case <-s.stop:
			return
		case <-ticker.C:
			s.cleanup()
		}
	}
}

// cleanup 找出空闲超时的 worker 并逐个发送退出信号。
//
// s.ready 中靠近栈底（小索引）的 worker 最老（lastUse 最早），
// 因此线性扫描从头开始找超时条目是正确的。
func (s *Stack) cleanup() {
	deadline := time.Now().Add(-s.maxIdle)

	s.mu.Lock()
	if len(s.ready) == 0 {
		s.mu.Unlock()
		return
	}

	// 找出所有超时 worker（栈底优先，lastUse 最旧）
	var expired []*stackWorkerChan
	i := 0
	for i < len(s.ready) && s.ready[i].lastUse.Before(deadline) {
		expired = append(expired, s.ready[i])
		s.ready[i] = nil
		i++
	}
	if i > 0 {
		copy(s.ready, s.ready[i:])
		s.ready = s.ready[:len(s.ready)-i]
	}
	s.mu.Unlock()

	// 锁外非阻塞发送退出信号，避免 channel 已满时阻塞 cleaner goroutine。
	// ch 容量=1；若 channel 已满说明 worker 收到任务即将处理自己的事，直接跳过即可
	// （worker 执行完任务后会重新检查 stopped 标志并决定是否继续）。
	for _, wc := range expired {
		select {
		case wc.ch <- nil:
		default:
		}
	}
}

// safeRun 执行任务并捕获 panic，防止单任务崩溃导致 worker goroutine 永久丢失。
// 委托给 panicSafeRun 共享实现，消除与 Pool.safeRun 的重复代码。
func (s *Stack) safeRun(task func()) {
	panicSafeRun(task, &s.panicCount, s.panicHandler)
}

// ─── 编译期接口检查 ─────────────────────────────────────────────────────────

var (
	_ Submitter = (*Pool)(nil)
	_ Submitter = (*Stack)(nil)
	_ Submitter = (*Adaptive)(nil)
)
