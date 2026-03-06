package mpsc

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// ─── 基础功能 ────────────────────────────────────────────────────────────────

func TestRing_BasicEnqueueDrainCommit(t *testing.T) {
	r := New[int](8)

	// Enqueue 3 items
	for i := 1; i <= 3; i++ {
		seq, ok := r.Enqueue(i)
		if !ok {
			t.Fatalf("Enqueue(%d) failed", i)
		}
		_ = seq
	}

	if r.Len() != 3 {
		t.Fatalf("Len() = %d, want 3", r.Len())
	}

	// Drain
	var vals []int
	start, n := r.Drain(func(v *int) error {
		vals = append(vals, *v)
		return nil
	})

	if n != 3 {
		t.Fatalf("Drain n = %d, want 3", n)
	}
	if len(vals) != 3 || vals[0] != 1 || vals[1] != 2 || vals[2] != 3 {
		t.Fatalf("drained vals = %v, want [1 2 3]", vals)
	}

	// Commit
	r.Commit(start, n, nil)

	// All producers' Wait should return nil
	// (Already committed, slots freed)
}

func TestRing_WaitReturnsNil(t *testing.T) {
	r := New[string](4)

	seq, ok := r.Enqueue("hello")
	if !ok {
		t.Fatal("Enqueue failed")
	}

	done := make(chan error, 1)
	go func() {
		done <- r.Wait(seq)
	}()

	// Drain and Commit
	start, n := r.Drain(func(v *string) error { return nil })
	r.Commit(start, n, nil)

	err := <-done
	if err != nil {
		t.Fatalf("Wait returned error: %v", err)
	}
}

func TestRing_WaitReturnsBatchError(t *testing.T) {
	r := New[int](4)

	seq, _ := r.Enqueue(42)

	done := make(chan error, 1)
	go func() {
		done <- r.Wait(seq)
	}()

	batchErr := errors.New("flush failed")
	start, n := r.Drain(func(v *int) error { return nil })
	r.Commit(start, n, batchErr)

	err := <-done
	if err != batchErr {
		t.Fatalf("Wait error = %v, want %v", err, batchErr)
	}
}

func TestRing_DrainFnError(t *testing.T) {
	r := New[int](8)

	r.Enqueue(1)
	r.Enqueue(2)
	r.Enqueue(3)

	itemErr := errors.New("encode failed")
	start, n := r.Drain(func(v *int) error {
		if *v == 2 {
			return itemErr
		}
		return nil
	})

	// Commit without batch error — per-slot errors preserved
	r.Commit(start, n, nil)
}

// ─── 容量边界 ────────────────────────────────────────────────────────────────

func TestRing_Cap(t *testing.T) {
	r := New[int](16)
	if r.Cap() != 16 {
		t.Fatalf("Cap() = %d, want 16", r.Cap())
	}
}

func TestRing_CapRoundsUp(t *testing.T) {
	r := New[int](5)
	if !isPow2(r.Cap()) {
		t.Fatalf("Cap() = %d not power of 2", r.Cap())
	}
	if r.Cap() < 5 {
		t.Fatalf("Cap() = %d < 5", r.Cap())
	}
}

func TestRing_MinCap(t *testing.T) {
	r := New[int](1)
	if r.Cap() < 4 {
		t.Fatalf("Cap() = %d, want >= 4", r.Cap())
	}
}

func TestRing_DrainEmpty(t *testing.T) {
	r := New[int](8)
	start, n := r.Drain(func(v *int) error {
		t.Fatal("should not be called")
		return nil
	})
	if n != 0 {
		t.Fatalf("Drain empty: n = %d, want 0", n)
	}
	_ = start
}

// ─── 多生产者并发 ────────────────────────────────────────────────────────────

func TestRing_ConcurrentProducers(t *testing.T) {
	const ringSize = 256
	const producers = 8
	const perProducer = 1000

	r := New[int](ringSize)

	var produced atomic.Int64
	var consumed atomic.Int64

	// 消费者 goroutine
	var consumerWg sync.WaitGroup
	consumerWg.Add(1)
	stopConsumer := make(chan struct{})
	go func() {
		defer consumerWg.Done()
		for {
			start, n := r.Drain(func(v *int) error {
				consumed.Add(int64(*v))
				return nil
			})
			if n > 0 {
				r.Commit(start, n, nil)
			}
			select {
			case <-stopConsumer:
				// 最终收割
				start, n := r.Drain(func(v *int) error {
					consumed.Add(int64(*v))
					return nil
				})
				if n > 0 {
					r.Commit(start, n, nil)
				}
				return
			default:
			}
		}
	}()

	// 生产者 goroutines
	var producerWg sync.WaitGroup
	producerWg.Add(producers)
	for p := 0; p < producers; p++ {
		go func() {
			defer producerWg.Done()
			for i := 1; i <= perProducer; i++ {
				for {
					seq, ok := r.Enqueue(i)
					if ok {
						produced.Add(int64(i))
						err := r.Wait(seq)
						if err != nil {
							t.Errorf("Wait returned error: %v", err)
						}
						break
					}
					// Ring full, retry
				}
			}
		}()
	}

	producerWg.Wait()
	close(stopConsumer)
	consumerWg.Wait()

	if produced.Load() != consumed.Load() {
		t.Fatalf("produced=%d consumed=%d", produced.Load(), consumed.Load())
	}
}

// ─── Benchmarks ─────────────────────────────────────────────────────────────

func BenchmarkRing_EnqueueDrainCommit(b *testing.B) {
	r := New[int](1024)
	for b.Loop() {
		seq, ok := r.Enqueue(42)
		if !ok {
			b.Fatal("enqueue failed")
		}
		start, n := r.Drain(func(v *int) error { return nil })
		r.Commit(start, n, nil)
		_ = r.Wait(seq) // should return immediately since committed
	}
}

func BenchmarkRing_Parallel(b *testing.B) {
	const ringSize = 4096
	r := New[int](ringSize)

	// 单消费者持续 drain
	stop := make(chan struct{})
	go func() {
		for {
			select {
			case <-stop:
				return
			default:
				start, n := r.Drain(func(v *int) error { return nil })
				if n > 0 {
					r.Commit(start, n, nil)
				}
			}
		}
	}()

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			for {
				seq, ok := r.Enqueue(1)
				if ok {
					r.Wait(seq) //nolint:errcheck
					break
				}
			}
		}
	})

	b.StopTimer()
	close(stop)
}

// ─── helpers ─────────────────────────────────────────────────────────────────

func isPow2(n int) bool { return n > 0 && n&(n-1) == 0 }

// ─── WaitContext ────────────────────────────────────────────────

// TestWaitContext_NormalCommit 验证 WaitContext 在 Commit 正常到来时返回 nil。
func TestWaitContext_NormalCommit(t *testing.T) {
	r := New[int](4)
	seq, ok := r.Enqueue(42)
	if !ok {
		t.Fatal("Enqueue failed")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	done := make(chan error, 1)
	go func() { done <- r.WaitContext(ctx, seq) }()

	// 消费者侧
	time.Sleep(5 * time.Millisecond)
	start, n := r.Drain(func(v *int) error { return nil })
	r.Commit(start, n, nil)

	if err := <-done; err != nil {
		t.Fatalf("WaitContext returned %v, want nil", err)
	}
}

// TestWaitContext_ContextCancelled 验证 context 取消后 WaitContext 立即返回 ctx.Err()，
// 且 Ring 不会卡死（slot 最终被 goroutine 清理后可重用）。
func TestWaitContext_ContextCancelled(t *testing.T) {
	r := New[int](4)
	seq, ok := r.Enqueue(99)
	if !ok {
		t.Fatal("Enqueue failed")
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- r.WaitContext(ctx, seq) }()

	// 在 Commit 前取消 context
	time.Sleep(5 * time.Millisecond)
	cancel()

	err := <-done
	if err != context.Canceled {
		t.Fatalf("WaitContext returned %v, want context.Canceled", err)
	}

	// 消费者延迟提交——确保后台 goroutine 能清理 slot
	start, n := r.Drain(func(v *int) error { return nil })
	r.Commit(start, n, nil)

	// 等待 slot 被后台 goroutine 清理（slot.state → sFree）
	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		if r.slots[seq&r.mask].state.Load() == sFree {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if r.slots[seq&r.mask].state.Load() != sFree {
		t.Error("slot not freed after context-cancel + eventual Commit")
	}

	// Ring 应可继续使用
	seq2, ok2 := r.Enqueue(1)
	if !ok2 {
		t.Fatal("Ring stuck after WaitContext cancel")
	}
	s2, n2 := r.Drain(func(v *int) error { return nil })
	r.Commit(s2, n2, nil)
	if err := r.Wait(seq2); err != nil {
		t.Fatalf("second Wait returned %v", err)
	}
}

// TestWaitContext_ContextAlreadyCancelled 验证 ctx 提前取消时立即返回。
func TestWaitContext_ContextAlreadyCancelled(t *testing.T) {
	r := New[int](4)
	seq, ok := r.Enqueue(1)
	if !ok {
		t.Fatal("Enqueue failed")
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // 提前取消

	done := make(chan error, 1)
	go func() { done <- r.WaitContext(ctx, seq) }()

	select {
	case err := <-done:
		if err != context.Canceled {
			t.Fatalf("early cancel: got %v, want context.Canceled", err)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("WaitContext did not return quickly for pre-cancelled ctx")
	}

	// 清理 in-flight slot
	start, n := r.Drain(func(v *int) error { return nil })
	r.Commit(start, n, nil)
}
