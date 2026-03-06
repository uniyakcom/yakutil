package coarsetime

import (
	"sync"
	"testing"
	"time"
)

func TestNowNano_Positive(t *testing.T) {
	n := NowNano()
	if n <= 0 {
		t.Fatalf("NowNano() returned non-positive: %d", n)
	}
}

func TestNowNano_CloseToReal(t *testing.T) {
	const maxDrift = 2 * int64(time.Millisecond)
	coarse := NowNano()
	real := time.Now().UnixNano()
	drift := coarse - real
	if drift < 0 {
		drift = -drift
	}
	if drift > maxDrift {
		t.Fatalf("drift too large: %dns (max %dns)", drift, maxDrift)
	}
}

func TestNowNano_MonotonicProgress(t *testing.T) {
	n1 := NowNano()
	time.Sleep(2 * time.Millisecond)
	n2 := NowNano()
	if n2 < n1 {
		t.Fatalf("time went backwards: %d -> %d", n1, n2)
	}
}

func TestNowNano_Concurrent(t *testing.T) {
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 10000; j++ {
				n := NowNano()
				if n <= 0 {
					t.Errorf("non-positive: %d", n)
					return
				}
			}
		}()
	}
	wg.Wait()
}

func TestNow_Type(t *testing.T) {
	now := Now()
	real := time.Now()
	drift := now.Sub(real)
	if drift < 0 {
		drift = -drift
	}
	if drift > 2*time.Millisecond {
		t.Fatalf("Now() drift too large: %v", drift)
	}
}

func BenchmarkNowNano(b *testing.B) {
	for b.Loop() {
		NowNano()
	}
}

func BenchmarkTimeNow(b *testing.B) {
	for b.Loop() {
		time.Now().UnixNano()
	}
}

func BenchmarkNowNano_Parallel(b *testing.B) {
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			NowNano()
		}
	})
}

func BenchmarkTimeNow_Parallel(b *testing.B) {
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			time.Now().UnixNano() //nolint:staticcheck
		}
	})
}
