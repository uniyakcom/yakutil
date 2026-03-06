package percpu

import (
	"runtime"
	"sync"
	"testing"
)

func TestGauge_AddSub(t *testing.T) {
	g := NewGauge(runtime.GOMAXPROCS(0))
	g.Add(10)
	g.Add(5)
	g.Sub(3)
	if got := g.Load(); got != 12 {
		t.Fatalf("Load: want 12, got %d", got)
	}
}

func TestGauge_Reset(t *testing.T) {
	g := NewGauge(4)
	g.Add(100)
	g.Reset()
	if got := g.Load(); got != 0 {
		t.Fatalf("after Reset: want 0, got %d", got)
	}
}

func TestGauge_Negative(t *testing.T) {
	g := NewGauge(4)
	g.Sub(5) // gauge 允许负值
	if got := g.Load(); got != -5 {
		t.Fatalf("negative gauge: want -5, got %d", got)
	}
}

func TestGauge_Concurrent(t *testing.T) {
	g := NewGauge(runtime.GOMAXPROCS(0))
	const N = 1000
	var wg sync.WaitGroup
	wg.Add(N * 2)
	for i := 0; i < N; i++ {
		go func() { defer wg.Done(); g.Add(1) }()
		go func() { defer wg.Done(); g.Sub(1) }()
	}
	wg.Wait()
	if got := g.Load(); got != 0 {
		t.Fatalf("after N pairs Add/Sub: want 0, got %d", got)
	}
}

func TestGauge_Stats(t *testing.T) {
	g := NewGauge(4)
	g.Add(100)
	st := g.Stats()
	if st.Sum != 100 {
		t.Fatalf("Stats.Sum: want 100, got %d", st.Sum)
	}
	if st.Slots != g.mask+1 {
		t.Fatalf("Stats.Slots: want %d, got %d", g.mask+1, st.Slots)
	}
}

func TestGauge_MinSlots(t *testing.T) {
	g := NewGauge(1) // 强制最小 slot 数
	if g.mask+1 < 8 {
		t.Fatalf("minimum slots should be 8, got %d", g.mask+1)
	}
}

func BenchmarkGauge_Add(b *testing.B) {
	g := NewGauge(runtime.GOMAXPROCS(0))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		g.Add(1)
	}
}

func BenchmarkGauge_Add_Parallel(b *testing.B) {
	g := NewGauge(runtime.GOMAXPROCS(0))
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			g.Add(1)
		}
	})
}
