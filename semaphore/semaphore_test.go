package semaphore_test

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/uniyakcom/yakutil/semaphore"
)

func TestNew_Panic(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("New(0) should panic")
		}
	}()
	semaphore.New(0)
}

func TestCapAndCount_Initial(t *testing.T) {
	sem := semaphore.New(5)
	if cap := sem.Cap(); cap != 5 {
		t.Fatalf("Cap = %d, want 5", cap)
	}
	if cnt := sem.Count(); cnt != 0 {
		t.Fatalf("Count = %d, want 0", cnt)
	}
	if avail := sem.Available(); avail != 5 {
		t.Fatalf("Available = %d, want 5", avail)
	}
}

func TestAcquireRelease_Sequential(t *testing.T) {
	sem := semaphore.New(3)
	sem.Acquire()
	if sem.Count() != 1 {
		t.Fatalf("after 1 Acquire: Count = %d", sem.Count())
	}
	sem.Acquire()
	sem.Acquire()
	if sem.Count() != 3 || sem.Available() != 0 {
		t.Fatalf("after 3 Acquire: Count=%d Available=%d", sem.Count(), sem.Available())
	}
	sem.Release()
	if sem.Count() != 2 {
		t.Fatalf("after Release: Count = %d", sem.Count())
	}
	sem.Release()
	sem.Release()
	if sem.Count() != 0 {
		t.Fatalf("after all Release: Count = %d", sem.Count())
	}
}

func TestTryAcquire_Success(t *testing.T) {
	sem := semaphore.New(2)
	if !sem.TryAcquire() {
		t.Fatal("TryAcquire should succeed when not full")
	}
	if !sem.TryAcquire() {
		t.Fatal("TryAcquire should succeed (count=1, cap=2)")
	}
}

func TestTryAcquire_Full(t *testing.T) {
	sem := semaphore.New(2)
	sem.Acquire()
	sem.Acquire()
	if sem.TryAcquire() {
		t.Fatal("TryAcquire should fail when full")
	}
	sem.Release()
	if !sem.TryAcquire() {
		t.Fatal("TryAcquire should succeed after Release")
	}
}

func TestAcquireContext_Success(t *testing.T) {
	sem := semaphore.New(1)
	ctx := context.Background()
	if err := sem.AcquireContext(ctx); err != nil {
		t.Fatalf("AcquireContext failed: %v", err)
	}
	if sem.Count() != 1 {
		t.Fatalf("Count = %d after AcquireContext", sem.Count())
	}
	sem.Release()
}

func TestAcquireContext_Timeout(t *testing.T) {
	sem := semaphore.New(1)
	sem.Acquire()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	start := time.Now()
	err := sem.AcquireContext(ctx)
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("AcquireContext should fail when semaphore is full and ctx times out")
	}
	if elapsed < 15*time.Millisecond {
		t.Fatalf("returned too early: %v", elapsed)
	}
	sem.Release()
}

func TestAcquireContext_Cancel(t *testing.T) {
	sem := semaphore.New(1)
	sem.Acquire()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- sem.AcquireContext(ctx)
	}()
	time.Sleep(5 * time.Millisecond)
	cancel()
	err := <-done
	if err == nil {
		t.Fatal("expected error after cancel")
	}
	if err != context.Canceled {
		t.Fatalf("expected Canceled, got %v", err)
	}
	sem.Release()
}

func TestRelease_PanicOnExcess(t *testing.T) {
	sem := semaphore.New(2)
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("Release without Acquire should panic")
		}
	}()
	sem.Release()
}

func TestConcurrentBound(t *testing.T) {
	const cap = 10
	sem := semaphore.New(cap)
	var (
		wg      sync.WaitGroup
		maxSeen atomic.Int64
	)
	for i := 0; i < 200; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem.Acquire()
			defer sem.Release()
			cur := int64(sem.Count())
			for {
				old := maxSeen.Load()
				if cur <= old || maxSeen.CompareAndSwap(old, cur) {
					break
				}
			}
			time.Sleep(time.Millisecond)
		}()
	}
	wg.Wait()
	if got := maxSeen.Load(); got > int64(cap) {
		t.Fatalf("peak Count = %d exceeded Cap = %d", got, cap)
	}
}

func TestAcquireContext_Unblock(t *testing.T) {
	sem := semaphore.New(1)
	sem.Acquire()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- sem.AcquireContext(ctx)
	}()
	time.Sleep(10 * time.Millisecond)
	sem.Release()
	if err := <-done; err != nil {
		t.Fatalf("expected successful acquire after release, got: %v", err)
	}
	sem.Release()
}

func BenchmarkAcquireRelease(b *testing.B) {
	sem := semaphore.New(b.N + 1)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		sem.Acquire()
		sem.Release()
	}
}

func BenchmarkTryAcquire_Success(b *testing.B) {
	sem := semaphore.New(b.N + 1)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if sem.TryAcquire() {
			sem.Release()
		}
	}
}

func BenchmarkTryAcquire_Full(b *testing.B) {
	sem := semaphore.New(1)
	sem.Acquire()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		sem.TryAcquire()
	}
}

func BenchmarkAcquireRelease_Parallel(b *testing.B) {
	sem := semaphore.New(1024)
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			sem.Acquire()
			sem.Release()
		}
	})
}

// ─── TryAcquireN / ReleaseN 测试 ─────────────────────────────────────────────

func TestTryAcquireN_Success(t *testing.T) {
	sem := semaphore.New(10)
	if !sem.TryAcquireN(3) {
		t.Fatal("TryAcquireN(3) on empty sem: expected true")
	}
	if sem.Count() != 3 {
		t.Fatalf("Count: want 3, got %d", sem.Count())
	}
	if sem.Available() != 7 {
		t.Fatalf("Available: want 7, got %d", sem.Available())
	}
}

func TestTryAcquireN_Insufficient(t *testing.T) {
	sem := semaphore.New(5)
	sem.TryAcquireN(4) // 占用 4

	if sem.TryAcquireN(2) {
		t.Fatal("TryAcquireN(2) with only 1 available: expected false")
	}
	if sem.Count() != 4 {
		t.Fatalf("Count after failed TryAcquireN: want 4, got %d", sem.Count())
	}
}

func TestTryAcquireN_ExceedsCap(t *testing.T) {
	sem := semaphore.New(5)
	if sem.TryAcquireN(6) {
		t.Fatal("TryAcquireN(6) on cap=5: expected false")
	}
	if sem.Count() != 0 {
		t.Fatal("Count should remain 0 after failed TryAcquireN")
	}
}

func TestTryAcquireN_ZeroAndNegative(t *testing.T) {
	sem := semaphore.New(5)
	if sem.TryAcquireN(0) {
		t.Fatal("TryAcquireN(0): expected false")
	}
	if sem.TryAcquireN(-1) {
		t.Fatal("TryAcquireN(-1): expected false")
	}
}

func TestReleaseN_Success(t *testing.T) {
	sem := semaphore.New(10)
	sem.TryAcquireN(5)
	sem.ReleaseN(5)
	if sem.Count() != 0 {
		t.Fatalf("after ReleaseN(5): want Count=0, got %d", sem.Count())
	}
}

func TestReleaseN_PanicOnExcess(t *testing.T) {
	sem := semaphore.New(5)
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("ReleaseN without matching acquire should panic")
		}
	}()
	sem.ReleaseN(1)
}

func TestTryAcquireN_Concurrent(t *testing.T) {
	sem := semaphore.New(10)
	const goroutines = 20
	var wg sync.WaitGroup
	var success atomic.Int64

	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			if sem.TryAcquireN(1) {
				success.Add(1)
			}
		}()
	}
	wg.Wait()

	if got := success.Load(); got > 10 {
		t.Fatalf("too many TryAcquireN succeeded: %d (cap=10)", got)
	}
	if int(success.Load()) != sem.Count() {
		t.Fatalf("success count %d != sem.Count() %d", success.Load(), sem.Count())
	}
}

func BenchmarkTryAcquireN(b *testing.B) {
	sem := semaphore.New(1024)
	b.ReportAllocs()
	for b.Loop() {
		if sem.TryAcquireN(1) {
			sem.ReleaseN(1)
		}
	}
}

func BenchmarkTryAcquireN_Parallel(b *testing.B) {
	sem := semaphore.New(1 << 20)
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			if sem.TryAcquireN(1) {
				sem.ReleaseN(1)
			}
		}
	})
}
