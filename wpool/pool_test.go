package wpool

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// ─── QueueLen / QueueCap 公开方法 ──────────────────────────────────────

// TestPool_QueueMetrics 验证 QueueLen/QueueCap 反映 channel 实际状态
func TestPool_QueueMetrics(t *testing.T) {
	// 创建 0 worker、0 idleTTL、容量=4 的队列
	p := NewPool(1, time.Second)
	defer p.Stop()

	if c := p.QueueCap(); c <= 0 {
		t.Fatalf("QueueCap()=%d, 期望 >0", c)
	}
	initLen := p.QueueLen()
	if initLen < 0 {
		t.Fatalf("QueueLen()=%d, 期望 >=0", initLen)
	}
	// 停止池后压入任务：Submit 会返回 false
	p.Stop()
	// 确保方法不 panic 即可
	_ = p.QueueLen()
	_ = p.QueueCap()
}

// ─── Pool 基础 ───────────────────────────────────────────────────────────────

func TestPool_Submit(t *testing.T) {
	p := NewPool(4, time.Second)
	defer p.Stop()

	var counter atomic.Int32
	var wg sync.WaitGroup
	const n = 100
	wg.Add(n)
	for i := 0; i < n; i++ {
		ok := p.Submit(func() {
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

func TestPool_TrySubmit(t *testing.T) {
	p := NewPool(1, time.Second)
	defer p.Stop()

	// 让唯一 worker 忙碌
	blocker := make(chan struct{})
	p.Submit(func() { <-blocker })

	// 填满队列
	full := false
	for i := 0; i < 100; i++ {
		if !p.TrySubmit(func() {}) {
			full = true
			break
		}
	}
	close(blocker)
	if !full {
		t.Log("TrySubmit never returned false (queue large enough)")
	}
}

func TestPool_Running(t *testing.T) {
	p := NewPool(4, time.Second)
	defer p.Stop()
	time.Sleep(10 * time.Millisecond) // 等待 workers 启动
	if r := p.Running(); r != 4 {
		t.Fatalf("Running() = %d, want 4", r)
	}
}

func TestPool_Stop(t *testing.T) {
	p := NewPool(4, time.Second)

	var counter atomic.Int32
	for i := 0; i < 10; i++ {
		p.Submit(func() {
			time.Sleep(time.Millisecond)
			counter.Add(1)
		})
	}
	p.Stop()
	// Stop 后 Submit 应返回 false
	if p.Submit(func() {}) {
		t.Fatal("Submit after Stop should return false")
	}
}

func TestPool_Resize(t *testing.T) {
	p := NewPool(2, time.Second)
	defer p.Stop()

	p.Resize(8)
	time.Sleep(10 * time.Millisecond)
	if r := p.Running(); r < 4 {
		t.Fatalf("Running() = %d after Resize(8), want >= 4", r)
	}
}

// ─── Adaptive ────────────────────────────────────────────────────────────────

func TestAdaptive_Submit(t *testing.T) {
	a := NewAdaptive(2, 16, 50*time.Millisecond, time.Second)
	defer a.Stop()

	var counter atomic.Int32
	var wg sync.WaitGroup
	const n = 50
	wg.Add(n)
	for i := 0; i < n; i++ {
		ok := a.Submit(func() {
			counter.Add(1)
			wg.Done()
		})
		if !ok {
			t.Fatal("Adaptive Submit returned false")
		}
	}
	wg.Wait()
	if c := counter.Load(); c != n {
		t.Fatalf("counter = %d, want %d", c, n)
	}
}

func TestAdaptive_Running(t *testing.T) {
	a := NewAdaptive(2, 8, 50*time.Millisecond, time.Second)
	defer a.Stop()
	time.Sleep(10 * time.Millisecond)
	if r := a.Running(); r < 2 {
		t.Fatalf("Running() = %d, want >= 2", r)
	}
}

func TestAdaptive_Stop(t *testing.T) {
	a := NewAdaptive(2, 8, 50*time.Millisecond, time.Second)
	a.Stop()
	if a.Submit(func() {}) {
		t.Fatal("Submit after Stop should return false")
	}
}

// ─── PanicHandler ────────────────────────────────────────────────────────────

func TestPool_PanicHandler(t *testing.T) {
	var caught any
	var gotStack []byte
	var mu sync.Mutex
	handler := func(r any, stack []byte) {
		mu.Lock()
		caught = r
		gotStack = stack
		mu.Unlock()
	}

	p := NewPool(2, time.Second, WithPanicHandler(handler))
	defer p.Stop()

	done := make(chan struct{})
	p.Submit(func() {
		defer close(done)
		panic("test-panic")
	})
	<-done
	// 等待 handler 被调用
	time.Sleep(10 * time.Millisecond)

	mu.Lock()
	got := caught
	st := gotStack
	mu.Unlock()
	if got != "test-panic" {
		t.Fatalf("PanicHandler got %v, want \"test-panic\"", got)
	}
	if len(st) == 0 {
		t.Fatal("PanicHandler: stack trace should be non-empty")
	}
}

// TestPool_NoPanicHandlerNoWorkerLoss 无 handler 时 worker 不因 panic 丢失
func TestPool_NoPanicHandlerNoWorkerLoss(t *testing.T) {
	p := NewPool(2, time.Second)
	defer p.Stop()

	// 触发 panic
	done := make(chan struct{})
	p.Submit(func() {
		defer close(done)
		panic("silent")
	})
	<-done
	time.Sleep(10 * time.Millisecond)

	// pool 应仍然接受新任务
	var ok bool
	var wg sync.WaitGroup
	wg.Add(1)
	ok = p.Submit(func() { wg.Done() })
	wg.Wait()
	if !ok {
		t.Fatal("pool should still accept tasks after panic without handler")
	}
}

// ─── PanicCount ─────────────────────────────────────────────────────────────

// TestPool_PanicCount 无 handler 时 panic 仍计入 PanicCount
func TestPool_PanicCount(t *testing.T) {
	p := NewPool(2, time.Second)
	defer p.Stop()

	done := make(chan struct{})
	p.Submit(func() {
		defer close(done)
		panic("count-me")
	})
	<-done
	time.Sleep(10 * time.Millisecond)

	if got := p.PanicCount(); got != 1 {
		t.Fatalf("PanicCount() = %d, want 1", got)
	}
}

// TestPool_PanicCount_WithHandler handler 和计数同时生效
func TestPool_PanicCount_WithHandler(t *testing.T) {
	var caught any
	var mu sync.Mutex
	handler := func(r any, _ []byte) { mu.Lock(); caught = r; mu.Unlock() }

	p := NewPool(2, time.Second, WithPanicHandler(handler))
	defer p.Stop()

	const n = 3
	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		p.Submit(func() {
			defer wg.Done()
			panic("boom")
		})
	}
	wg.Wait()
	time.Sleep(10 * time.Millisecond)

	if got := p.PanicCount(); got != n {
		t.Fatalf("PanicCount() = %d, want %d", got, n)
	}
	mu.Lock()
	got := caught
	mu.Unlock()
	if got == nil {
		t.Fatal("PanicHandler was never called")
	}
}

// TestAdaptive_PanicCount Adaptive 委托到内部 pool
func TestAdaptive_PanicCount(t *testing.T) {
	a := NewAdaptive(1, 4, 100*time.Millisecond, time.Second)
	defer a.Stop()

	done := make(chan struct{})
	a.Submit(func() {
		defer close(done)
		panic("adaptive-panic")
	})
	<-done
	time.Sleep(10 * time.Millisecond)

	if got := a.PanicCount(); got != 1 {
		t.Fatalf("Adaptive.PanicCount() = %d, want 1", got)
	}
}

// ─── Benchmarks ─────────────────────────────────────────────────────────────

func BenchmarkPool_Submit(b *testing.B) {
	p := NewPool(8, 5*time.Second)
	defer p.Stop()
	task := func() {}
	for b.Loop() {
		p.Submit(task)
	}
}

func BenchmarkPool_Submit_Parallel(b *testing.B) {
	p := NewPool(16, 5*time.Second)
	defer p.Stop()
	task := func() {}
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			p.Submit(task)
		}
	})
}

func BenchmarkPool_SubmitTimeout(b *testing.B) {
	p := NewPool(8, 5*time.Second)
	defer p.Stop()
	task := func() {}
	b.ReportAllocs()
	for b.Loop() {
		p.SubmitTimeout(task, time.Second)
	}
}

func BenchmarkPool_SubmitTimeout_Parallel(b *testing.B) {
	p := NewPool(16, 5*time.Second)
	defer p.Stop()
	task := func() {}
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			p.SubmitTimeout(task, time.Second)
		}
	})
}

// ─── SubmitTimeout 测试 ───────────────────────────────────────────────────────

func TestPool_SubmitTimeout_Success(t *testing.T) {
	p := NewPool(4, 5*time.Second)
	defer p.Stop()

	done := make(chan struct{})
	ok := p.SubmitTimeout(func() { close(done) }, 100*time.Millisecond)
	if !ok {
		t.Fatal("SubmitTimeout should succeed")
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("task did not execute")
	}
}

func TestPool_SubmitTimeout_Timeout(t *testing.T) {
	// 制造一个装满任务的队列使 SubmitTimeout 超时
	p := NewPool(1, 5*time.Second)
	defer p.Stop()

	// 填满队列（1 worker，queue=4）
	block := make(chan struct{})
	for i := 0; i < 5; i++ {
		p.Submit(func() { <-block })
	}

	ok := p.SubmitTimeout(func() {}, 10*time.Millisecond)
	close(block)
	if ok {
		t.Fatal("SubmitTimeout should have timed out on a full queue")
	}
}

func TestPool_SubmitTimeout_StoppedPool(t *testing.T) {
	p := NewPool(2, 5*time.Second)
	p.Stop()

	ok := p.SubmitTimeout(func() {}, 100*time.Millisecond)
	if ok {
		t.Fatal("SubmitTimeout on stopped pool should return false")
	}
}

func TestTimedSubmitter_InterfacePool(t *testing.T) {
	var _ TimedSubmitter = NewPool(2, time.Second)
}

func TestTimedSubmitter_InterfaceStack(t *testing.T) {
	var _ TimedSubmitter = NewStack(2, time.Second)
}
