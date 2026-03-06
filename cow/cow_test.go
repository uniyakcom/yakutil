package cow

import (
	"sync"
	"testing"
)

// ─── 基础功能 ────────────────────────────────────────────────────────────────

func TestValue_New(t *testing.T) {
	v := New(42)
	if got := v.Load(); got != 42 {
		t.Fatalf("Load() = %d, want 42", got)
	}
}

func TestValue_ZeroValue(t *testing.T) {
	var v Value[int]
	if got := v.Load(); got != 0 {
		t.Fatalf("zero Value Load() = %d, want 0", got)
	}
}

func TestValue_Store(t *testing.T) {
	v := New("hello")
	v.Store("world")
	if got := v.Load(); got != "world" {
		t.Fatalf("after Store, Load() = %q, want world", got)
	}
}

func TestValue_Update(t *testing.T) {
	v := New(10)
	v.Update(func(old int) int { return old + 5 })
	if got := v.Load(); got != 15 {
		t.Fatalf("after Update, Load() = %d, want 15", got)
	}
}

func TestValue_Ptr(t *testing.T) {
	v := New(99)
	p := v.Ptr()
	if p == nil {
		t.Fatal("Ptr() = nil")
	}
	if *p != 99 {
		t.Fatalf("*Ptr() = %d, want 99", *p)
	}
}

func TestValue_Ptr_ZeroValue(t *testing.T) {
	var v Value[int]
	if p := v.Ptr(); p != nil {
		t.Fatalf("zero Value Ptr() = %p, want nil", p)
	}
}

// ─── 泛型类型 ────────────────────────────────────────────────────────────────

type config struct {
	Host string
	Port int
}

func TestValue_Struct(t *testing.T) {
	v := New(config{Host: "localhost", Port: 8080})

	got := v.Load()
	if got.Host != "localhost" || got.Port != 8080 {
		t.Fatalf("Load() = %+v", got)
	}

	v.Store(config{Host: "0.0.0.0", Port: 9090})
	got = v.Load()
	if got.Host != "0.0.0.0" || got.Port != 9090 {
		t.Fatalf("after Store, Load() = %+v", got)
	}
}

func TestValue_Slice(t *testing.T) {
	v := New([]int{1, 2, 3})
	got := v.Load()
	if len(got) != 3 || got[0] != 1 || got[1] != 2 || got[2] != 3 {
		t.Fatalf("Load() = %v", got)
	}

	// Copy-on-write: 新值不影响旧快照
	old := v.Load()
	newSlice := make([]int, len(old)+1)
	copy(newSlice, old)
	newSlice[3] = 4
	v.Store(newSlice)

	// old 不受影响
	if len(old) != 3 {
		t.Fatalf("old snapshot changed: %v", old)
	}
	// 新值正确
	cur := v.Load()
	if len(cur) != 4 || cur[3] != 4 {
		t.Fatalf("new value = %v", cur)
	}
}

func TestValue_Map(t *testing.T) {
	m := map[string]int{"a": 1}
	v := New(m)

	got := v.Load()
	if got["a"] != 1 {
		t.Fatal("Load()[a] != 1")
	}
}

// ─── 多次 Update ─────────────────────────────────────────────────────────────

func TestValue_MultipleUpdates(t *testing.T) {
	v := New(0)
	for i := 0; i < 100; i++ {
		v.Update(func(old int) int { return old + 1 })
	}
	if got := v.Load(); got != 100 {
		t.Fatalf("after 100 Updates, Load() = %d, want 100", got)
	}
}

// ─── 并发读 ──────────────────────────────────────────────────────────────────

func TestValue_ConcurrentReads(t *testing.T) {
	v := New(42)
	const readers = 64

	var wg sync.WaitGroup
	wg.Add(readers)
	for i := 0; i < readers; i++ {
		go func() {
			defer wg.Done()
			for j := 0; j < 10000; j++ {
				got := v.Load()
				if got != 42 {
					t.Errorf("Load() = %d, want 42", got)
					return
				}
			}
		}()
	}
	wg.Wait()
}

func TestValue_ConcurrentReadWrite(t *testing.T) {
	v := New(0)
	const writers = 1 // COW 单写者
	const readers = 16
	const iters = 10000

	var wg sync.WaitGroup
	wg.Add(writers + readers)

	// Writer
	go func() {
		defer wg.Done()
		for i := 1; i <= iters; i++ {
			v.Store(i)
		}
	}()

	// Readers：读到的值应 ≥ 0 且 ≤ iters
	for r := 0; r < readers; r++ {
		go func() {
			defer wg.Done()
			for j := 0; j < iters; j++ {
				got := v.Load()
				if got < 0 || got > iters {
					t.Errorf("Load() = %d, out of range [0, %d]", got, iters)
					return
				}
			}
		}()
	}
	wg.Wait()
}

// ─── Benchmarks ─────────────────────────────────────────────────────────────

func BenchmarkValue_Load(b *testing.B) {
	v := New(42)
	for b.Loop() {
		_ = v.Load()
	}
}

func BenchmarkValue_Store(b *testing.B) {
	v := New(0)
	for b.Loop() {
		v.Store(42)
	}
}

func BenchmarkValue_Load_Parallel(b *testing.B) {
	v := New(42)
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			_ = v.Load()
		}
	})
}

// ─── Swap ────────────────────────────────────────────────────────────────────

func TestValue_Swap(t *testing.T) {
	v := New(10)
	old := v.Swap(20)
	if old != 10 {
		t.Fatalf("Swap old = %d, want 10", old)
	}
	if got := v.Load(); got != 20 {
		t.Fatalf("after Swap, Load() = %d, want 20", got)
	}
}

func TestValue_Swap_ZeroValue(t *testing.T) {
	var v Value[int]
	old := v.Swap(42)
	if old != 0 {
		t.Fatalf("Swap on zero Value old = %d, want 0", old)
	}
	if got := v.Load(); got != 42 {
		t.Fatalf("Load() = %d, want 42", got)
	}
}

// ─── UpdateCAS ───────────────────────────────────────────────────────────────

func TestValue_UpdateCAS(t *testing.T) {
	v := New(10)
	v.UpdateCAS(func(old int) int { return old + 5 })
	if got := v.Load(); got != 15 {
		t.Fatalf("after UpdateCAS, Load() = %d, want 15", got)
	}
}

func TestValue_UpdateCAS_ZeroValue(t *testing.T) {
	var v Value[int]
	v.UpdateCAS(func(old int) int { return old + 1 })
	if got := v.Load(); got != 1 {
		t.Fatalf("UpdateCAS on zero Value, Load() = %d, want 1", got)
	}
}

func TestValue_UpdateCAS_Concurrent(t *testing.T) {
	v := New(0)
	const goroutines = 16
	const iters = 1000

	var wg sync.WaitGroup
	wg.Add(goroutines)
	for g := 0; g < goroutines; g++ {
		go func() {
			defer wg.Done()
			for i := 0; i < iters; i++ {
				v.UpdateCAS(func(old int) int { return old + 1 })
			}
		}()
	}
	wg.Wait()

	want := goroutines * iters
	if got := v.Load(); got != want {
		t.Fatalf("concurrent UpdateCAS: Load() = %d, want %d", got, want)
	}
}

func BenchmarkValue_Swap(b *testing.B) {
	v := New(0)
	for b.Loop() {
		v.Swap(42)
	}
}

func BenchmarkValue_UpdateCAS(b *testing.B) {
	v := New(0)
	for b.Loop() {
		v.UpdateCAS(func(old int) int { return old + 1 })
	}
}

func BenchmarkValue_UpdateCAS_Parallel(b *testing.B) {
	v := New(0)
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			v.UpdateCAS(func(old int) int { return old + 1 })
		}
	})
}
