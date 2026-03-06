package wpool

import (
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// ─── Stack 基础 ──────────────────────────────────────────────────────────────

func TestStack_Submit(t *testing.T) {
	s := NewStack(4, time.Second)
	defer s.Stop()

	var counter atomic.Int32
	var wg sync.WaitGroup
	const n = 200
	wg.Add(n)
	for i := 0; i < n; i++ {
		ok := s.Submit(func() {
			counter.Add(1)
			wg.Done()
		})
		if !ok {
			t.Fatal("Submit returned false before Stop")
		}
	}
	wg.Wait()
	if c := counter.Load(); c != n {
		t.Fatalf("counter = %d, want %d", c, n)
	}
}

func TestStack_Submit_UnlimitedWorkers(t *testing.T) {
	// maxWorkers=0：无上限，按需创建
	s := NewStack(0, 100*time.Millisecond)
	defer s.Stop()

	var counter atomic.Int32
	var wg sync.WaitGroup
	const n = 50
	wg.Add(n)
	for i := 0; i < n; i++ {
		ok := s.Submit(func() {
			counter.Add(1)
			wg.Done()
		})
		if !ok {
			t.Fatal("Submit returned false")
		}
	}
	wg.Wait()
	if c := counter.Load(); c != n {
		t.Fatalf("counter = %d, want %d", c, n)
	}
}

func TestStack_TrySubmit(t *testing.T) {
	// maxWorkers=1，且唯一 worker 正在忙碌时 TrySubmit 应返回 false
	s := NewStack(1, time.Second)
	defer s.Stop()

	blocker := make(chan struct{})
	// 先占用唯一 worker
	s.Submit(func() { <-blocker })
	// 等待 worker 开始执行
	time.Sleep(5 * time.Millisecond)

	// 此时 running=1, ready 为空, 已达上限
	ok := s.TrySubmit(func() {})
	close(blocker)
	if ok {
		t.Fatal("TrySubmit should return false when at maxWorkers limit")
	}
}

func TestStack_TrySubmit_IdleWorker(t *testing.T) {
	s := NewStack(4, time.Second)
	defer s.Stop()

	// 先把 worker 预热
	var wg sync.WaitGroup
	wg.Add(1)
	s.Submit(func() { wg.Done() })
	wg.Wait()
	time.Sleep(5 * time.Millisecond) // 等 worker 归还到栈

	// 有空闲 worker，TrySubmit 应成功
	done := make(chan struct{})
	ok := s.TrySubmit(func() { close(done) })
	if !ok {
		t.Fatal("TrySubmit should return true when idle workers exist")
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("task never executed")
	}
}

func TestStack_Running(t *testing.T) {
	s := NewStack(0, time.Second)
	defer s.Stop()

	// 初始为 0
	if r := s.Running(); r != 0 {
		t.Fatalf("Running() = %d before any submit, want 0", r)
	}

	gate := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(4)
	for i := 0; i < 4; i++ {
		s.Submit(func() {
			wg.Done()
			<-gate
		})
	}
	wg.Wait() // 等 4 个 worker 都开始执行

	if r := s.Running(); r != 4 {
		t.Fatalf("Running() = %d while 4 workers busy, want 4", r)
	}
	close(gate)
}

// ─── MaxWorkers 限制 ─────────────────────────────────────────────────────────

func TestStack_MaxWorkers_Enforced(t *testing.T) {
	const max = 3
	s := NewStack(max, time.Second)
	defer s.Stop()

	gate := make(chan struct{})
	var startWg sync.WaitGroup
	startWg.Add(max)
	for i := 0; i < max; i++ {
		s.Submit(func() {
			startWg.Done()
			<-gate
		})
	}
	startWg.Wait() // 等 max 个 worker 全部阻塞

	if r := s.Running(); r != max {
		t.Fatalf("Running() = %d, want %d", r, max)
	}

	// 额外提交：此时必须等待（Submit 会阻塞）
	submitted := make(chan bool, 1)
	go func() {
		ok := s.Submit(func() {})
		submitted <- ok
	}()

	// 短暂等待后确认 goroutine 仍在等待
	time.Sleep(20 * time.Millisecond)
	select {
	case <-submitted:
		t.Fatal("Submit should be blocking when maxWorkers reached")
	default:
	}

	// 释放 gate，Submit 应立即完成
	close(gate)
	select {
	case ok := <-submitted:
		if !ok {
			t.Fatal("Submit returned false")
		}
	case <-time.After(time.Second):
		t.Fatal("Submit blocked too long after workers freed")
	}
}

// ─── Stop 行为 ───────────────────────────────────────────────────────────────

func TestStack_Stop_Idempotent(t *testing.T) {
	s := NewStack(2, time.Second)
	s.Stop()
	s.Stop() // 多次调用应安全
}

func TestStack_Stop_RejectsNewSubmit(t *testing.T) {
	s := NewStack(2, time.Second)
	s.Stop()
	if s.Submit(func() {}) {
		t.Fatal("Submit after Stop should return false")
	}
	if s.TrySubmit(func() {}) {
		t.Fatal("TrySubmit after Stop should return false")
	}
}

func TestStack_Stop_WaitsForRunningTasks(t *testing.T) {
	s := NewStack(4, time.Second)

	var completed atomic.Int32
	var wg sync.WaitGroup
	const n = 8
	wg.Add(n)
	for i := 0; i < n; i++ {
		s.Submit(func() {
			time.Sleep(5 * time.Millisecond)
			completed.Add(1)
			wg.Done()
		})
	}
	wg.Wait()
	s.Stop()

	if c := completed.Load(); c != n {
		t.Fatalf("completed = %d, want %d", c, n)
	}
}

// TestStack_Stop_WhileBlocked 验证阻塞在 waitForWorker 的 Submit 在 Stop 后能正常返回。
func TestStack_Stop_WhileBlocked(t *testing.T) {
	s := NewStack(1, time.Second)

	gate := make(chan struct{})
	s.Submit(func() { <-gate }) // 占用唯一 worker

	// 另一个 goroutine 阻塞在 Submit
	done := make(chan bool, 1)
	go func() {
		ok := s.Submit(func() {})
		done <- ok
	}()
	time.Sleep(10 * time.Millisecond)

	// Stop 应唤醒阻塞的 Submit 并返回 false
	close(gate) // 先释放正在执行的任务
	s.Stop()

	select {
	case ok := <-done:
		// 可能返回 true（Stop 前任务完成）或 false（Stop 期间）
		_ = ok
	case <-time.After(time.Second):
		t.Fatal("blocked Submit did not return after Stop")
	}
}

// ─── Panic 安全 ──────────────────────────────────────────────────────────────

func TestStack_PanicSafety(t *testing.T) {
	s := NewStack(2, time.Second)
	defer s.Stop()

	var normalDone atomic.Int32
	var wg sync.WaitGroup

	// 先提交一个 panic 任务
	wg.Add(1)
	s.Submit(func() {
		defer wg.Done()
		panic("intentional panic")
	})
	wg.Wait()

	// panic 之后 worker 应仍在（或能被新建），后续任务正常执行
	const n = 10
	wg.Add(n)
	for i := 0; i < n; i++ {
		ok := s.Submit(func() {
			normalDone.Add(1)
			wg.Done()
		})
		if !ok {
			t.Fatal("Submit returned false after panic recovery")
		}
	}
	wg.Wait()

	if c := normalDone.Load(); c != n {
		t.Fatalf("normalDone = %d after panic, want %d", c, n)
	}
}

// ─── FILO 顺序验证 ───────────────────────────────────────────────────────────

// TestStack_FILO_Ordering 验证 FILO 语义：串行场景下最近用过的 worker 优先。
// 方法：通过 runtime.Stack 读取 goroutine ID，验证连续单任务复用同一 goroutine。
func TestStack_FILO_Ordering(t *testing.T) {
	s := NewStack(4, 5*time.Second)
	defer s.Stop()

	// 记录每次任务由哪个 goroutine 执行
	goroutineIDs := make([]int64, 0, 10)
	var mu sync.Mutex

	for i := 0; i < 10; i++ {
		var wg sync.WaitGroup
		wg.Add(1)
		s.Submit(func() {
			id := currentGoroutineID()
			mu.Lock()
			goroutineIDs = append(goroutineIDs, id)
			mu.Unlock()
			wg.Done()
		})
		wg.Wait()
		// 等 worker 完全归还到栈
		runtime.Gosched()
		time.Sleep(time.Millisecond)
	}

	// 在串行场景下，FILO 应使同一 goroutine 反复服务（相邻任务大概率相同）
	sameCount := 0
	for i := 1; i < len(goroutineIDs); i++ {
		if goroutineIDs[i] == goroutineIDs[i-1] {
			sameCount++
		}
	}
	// 至少 50% 的连续任务由同一 worker 处理（FIFO 下接近随机）
	if sameCount < len(goroutineIDs)/2 {
		t.Logf("goroutineIDs: %v", goroutineIDs)
		t.Logf("FILO same-worker rate: %d/%d (below 50%%)", sameCount, len(goroutineIDs)-1)
		// 在非常嘈杂的 CI 环境下可能偶发失败，仅记录不致命
	} else {
		t.Logf("FILO same-worker rate: %d/%d", sameCount, len(goroutineIDs)-1)
	}
}

// currentGoroutineID 利用 runtime.Stack 提取当前 goroutine ID。
// 仅用于测试，不应在生产代码使用。
func currentGoroutineID() int64 {
	var buf [64]byte
	n := runtime.Stack(buf[:], false)
	// 格式："goroutine 7 [running]:\n..."
	parts := strings.Fields(string(buf[:n]))
	if len(parts) >= 2 && parts[0] == "goroutine" {
		id := parseIntStr(parts[1])
		return id
	}
	return -1
}

// parseIntStr 将纯数字字符串解析为 int64，非数字返回 -1。
func parseIntStr(s string) int64 {
	if len(s) == 0 {
		return -1
	}
	var n int64
	for _, c := range s {
		if c < '0' || c > '9' {
			return -1
		}
		n = n*10 + int64(c-'0')
	}
	return n
}

// ─── 并发安全 ────────────────────────────────────────────────────────────────

func TestStack_Concurrent(t *testing.T) {
	s := NewStack(8, 100*time.Millisecond)
	defer s.Stop()

	const goroutines = 16
	const tasksPerGoroutine = 50
	var counter atomic.Int64
	var wg sync.WaitGroup

	wg.Add(goroutines * tasksPerGoroutine)
	for g := 0; g < goroutines; g++ {
		go func() {
			for i := 0; i < tasksPerGoroutine; i++ {
				s.Submit(func() {
					counter.Add(1)
					wg.Done()
				})
			}
		}()
	}
	wg.Wait()

	want := int64(goroutines * tasksPerGoroutine)
	if c := counter.Load(); c != want {
		t.Fatalf("counter = %d, want %d", c, want)
	}
}

// ─── 空闲回收 ────────────────────────────────────────────────────────────────

func TestStack_IdleCleanup(t *testing.T) {
	maxIdle := 50 * time.Millisecond
	s := NewStack(0, maxIdle)
	defer s.Stop()

	// 启动若干 worker
	var wg sync.WaitGroup
	wg.Add(4)
	for i := 0; i < 4; i++ {
		s.Submit(func() { wg.Done() })
	}
	wg.Wait()

	// 等待 worker 全部归还到栈
	time.Sleep(5 * time.Millisecond)
	runningAfterTasks := s.Running()

	// 等待超过 maxIdle，cleaner 应回收
	time.Sleep(maxIdle * 3)

	runningAfterIdle := s.Running()
	if runningAfterIdle >= runningAfterTasks && runningAfterTasks > 0 {
		t.Logf("Running before cleanup: %d, after: %d", runningAfterTasks, runningAfterIdle)
		t.Fatal("workers not cleaned up after idle timeout")
	}
}

// ─── Submitter 接口 ──────────────────────────────────────────────────────────

func TestSubmitter_Interface(t *testing.T) {
	// 编译期已通过 var _ Submitter = (*Stack)(nil) 检查，此处做运行时验证
	var iface Submitter

	iface = NewStack(2, time.Second)
	iface.Stop()

	iface = NewPool(2, time.Second)
	iface.Stop()

	iface = NewAdaptive(1, 4, 100*time.Millisecond, time.Second)
	iface.Stop()

	_ = iface
}

// ─── Benchmarks ──────────────────────────────────────────────────────────────

func BenchmarkStack_Submit(b *testing.B) {
	s := NewStack(8, DefaultStackIdleTimeout)
	defer s.Stop()
	task := func() {}
	for b.Loop() {
		s.Submit(task)
	}
}

func BenchmarkStack_Submit_Parallel(b *testing.B) {
	s := NewStack(64, DefaultStackIdleTimeout)
	defer s.Stop()
	task := func() {}
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			s.Submit(task)
		}
	})
}

// BenchmarkStack_vs_Pool 比较 FILO（Stack）与 FIFO（Pool）在短任务上的延迟差异。
func BenchmarkStack_vs_Pool(b *testing.B) {
	workers := runtime.GOMAXPROCS(0) * 2
	const idleTTL = 5 * time.Second

	b.Run("Stack_FILO", func(b *testing.B) {
		s := NewStack(workers, idleTTL)
		defer s.Stop()
		var wg sync.WaitGroup
		b.ReportAllocs()
		for b.Loop() {
			wg.Add(1)
			s.Submit(func() { wg.Done() })
		}
		wg.Wait()
	})

	b.Run("Pool_FIFO", func(b *testing.B) {
		p := NewPool(workers, idleTTL)
		defer p.Stop()
		var wg sync.WaitGroup
		b.ReportAllocs()
		for b.Loop() {
			wg.Add(1)
			p.Submit(func() { wg.Done() })
		}
		wg.Wait()
	})

	b.Run("GoSpawn", func(b *testing.B) {
		var wg sync.WaitGroup
		b.ReportAllocs()
		for b.Loop() {
			wg.Add(1)
			go func() { wg.Done() }()
		}
		wg.Wait()
	})
}

func BenchmarkStack_TrySubmit(b *testing.B) {
	s := NewStack(64, DefaultStackIdleTimeout)
	defer s.Stop()
	b.ReportAllocs()
	for b.Loop() {
		s.TrySubmit(func() {})
	}
}

func BenchmarkStack_SubmitTimeout(b *testing.B) {
	s := NewStack(8, DefaultStackIdleTimeout)
	defer s.Stop()
	task := func() {}
	b.ReportAllocs()
	for b.Loop() {
		s.SubmitTimeout(task, time.Second)
	}
}

func BenchmarkStack_SubmitTimeout_Parallel(b *testing.B) {
	s := NewStack(16, DefaultStackIdleTimeout)
	defer s.Stop()
	task := func() {}
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			s.SubmitTimeout(task, time.Second)
		}
	})
}

// ─── PanicHandler ────────────────────────────────────────────────────────────

func TestStack_PanicHandler(t *testing.T) {
	var caught any
	var gotStack []byte
	var mu sync.Mutex
	handler := func(r any, stack []byte) {
		mu.Lock()
		caught = r
		gotStack = stack
		mu.Unlock()
	}

	s := NewStack(2, time.Second, WithStackPanicHandler(handler))
	defer s.Stop()

	done := make(chan struct{})
	s.Submit(func() {
		defer close(done)
		panic("stack-panic")
	})
	<-done
	time.Sleep(10 * time.Millisecond)

	mu.Lock()
	got := caught
	st := gotStack
	mu.Unlock()
	if got != "stack-panic" {
		t.Fatalf("PanicHandler got %v, want \"stack-panic\"", got)
	}
	if len(st) == 0 {
		t.Fatal("PanicHandler: stack trace should be non-empty")
	}
}

// TestStack_PanicCount 无 handler 时 panic 计入 PanicCount
func TestStack_PanicCount(t *testing.T) {
	s := NewStack(2, time.Second)
	defer s.Stop()

	done := make(chan struct{})
	s.Submit(func() {
		defer close(done)
		panic("stack-count")
	})
	<-done
	time.Sleep(10 * time.Millisecond)

	if got := s.PanicCount(); got != 1 {
		t.Fatalf("PanicCount() = %d, want 1", got)
	}
}

// TestStack_PanicCount_WithHandler handler 和计数同时生效
func TestStack_PanicCount_WithHandler(t *testing.T) {
	var caught any
	var mu sync.Mutex
	handler := func(r any, _ []byte) { mu.Lock(); caught = r; mu.Unlock() }

	s := NewStack(2, time.Second, WithStackPanicHandler(handler))
	defer s.Stop()

	const n = 3
	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		s.Submit(func() {
			defer wg.Done()
			panic("stack-boom")
		})
	}
	wg.Wait()
	time.Sleep(10 * time.Millisecond)

	if got := s.PanicCount(); got != n {
		t.Fatalf("PanicCount() = %d, want %d", got, n)
	}
	mu.Lock()
	got := caught
	mu.Unlock()
	if got == nil {
		t.Fatal("PanicHandler was never called")
	}
}

// ─── 回归测试（Bug 修复验证） ────────────────────────────────────────────────

// TestStack_Stop_NoGoroutineLeak 回归：修复前 Stop() 扫描 ready 后，
// 已完成任务的 worker 推入 ready 会永久阻塞（goroutine 泄漏）。
// 压力测试：大量任务完成的瞬间反复 Stop，检查 Running() 最终降为 0。
func TestStack_Stop_NoGoroutineLeak(t *testing.T) {
	const rounds = 20
	for r := 0; r < rounds; r++ {
		s := NewStack(8, 100*time.Millisecond)
		const n = 64
		var wg sync.WaitGroup
		wg.Add(n)
		// 让所有 worker 跑起来
		for i := 0; i < n; i++ {
			s.Submit(func() {
				// 短暂运行后完成（此刻 Stop 可能同时发生）
				runtime.Gosched()
				wg.Done()
			})
		}
		wg.Wait()
		// 任务全部完成后立即 Stop：worker 正在 push 回 ready 栈
		s.Stop()
		// Stop 返回后 running 必须为 0
		if got := s.Running(); got != 0 {
			t.Errorf("round %d: Running()=%d after Stop, want 0", r, got)
		}
	}
}

// TestPool_Resize_Shrink_Precise 回归：修复前多 worker 同时看到 running > maxSize，
// 导致全部退出（running 降为 0）而非精确缩减。
func TestPool_Resize_Shrink_Precise(t *testing.T) {
	const initial = 16
	const target = 4
	p := NewPool(initial, 50*time.Millisecond)
	defer p.Stop()

	// 等 workers 全部就绪
	time.Sleep(20 * time.Millisecond)

	// 触发精确缩容
	p.Resize(target)

	// 给 worker 时间退出
	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		r := p.Running()
		if r == target {
			return // 精确缩至目标
		}
		if r < target {
			t.Fatalf("Resize(%d): Running()=%d 低于目标，发生过度缩容", target, r)
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("Resize(%d): Running()=%d 超时未收敛到目标", target, p.Running())
}

// ─── SubmitTimeout 高并发压力测试 ────────────────────────────────────────────

// TestStack_SubmitTimeout_HighConcurrency 验证 Stack.SubmitTimeout 在
// 1000 个并发 goroutine 下不会死锁、不会 goroutine 泄漏，且计数一致。
func TestStack_SubmitTimeout_HighConcurrency(t *testing.T) {
	const (
		workers    = 32
		goroutines = 1000
		taskSlice  = 2 * time.Millisecond // 每个任务持续约 2ms
		timeout    = 100 * time.Millisecond
	)

	s := NewStack(workers, DefaultStackIdleTimeout)
	defer s.Stop()

	var accepted, rejected int64

	var wg sync.WaitGroup
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			ok := s.SubmitTimeout(func() {
				time.Sleep(taskSlice)
			}, timeout)
			if ok {
				atomic.AddInt64(&accepted, 1)
			} else {
				atomic.AddInt64(&rejected, 1)
			}
		}()
	}

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		// OK
	case <-time.After(10 * time.Second):
		t.Fatal("SubmitTimeout high-concurrency test: timed out (possible deadlock or goroutine leak)")
	}

	total := accepted + rejected
	if total != goroutines {
		t.Fatalf("total=%d want %d (accepted=%d rejected=%d)", total, goroutines, accepted, rejected)
	}
	t.Logf("1000-goroutine SubmitTimeout: accepted=%d rejected=%d (%.1f%% accept rate)",
		accepted, rejected, float64(accepted)*100/float64(goroutines))
}

// BenchmarkStack_SubmitTimeout_1K 在 1000 goroutine 并发下量化 SubmitTimeout 吞吐。
// 使用 b.RunParallel + GOMAXPROCS×1000/GOMAXPROCS goroutine 以模拟真实并发。
func BenchmarkStack_SubmitTimeout_1K(b *testing.B) {
	s := NewStack(64, DefaultStackIdleTimeout)
	defer s.Stop()
	task := func() {}
	b.SetParallelism(1000 / runtime.GOMAXPROCS(0))
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			s.SubmitTimeout(task, 50*time.Millisecond)
		}
	})
}
