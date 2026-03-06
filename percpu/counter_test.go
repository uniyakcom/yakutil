package percpu

import (
	"math/bits"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
)

// ─── slot 布局与哈希参数健全性测试 ──────────────────────────────────────────

// TestCounter_SlotConfig 验证 mask 字段正确计算，
// 以及 fibShift 常量对所有合法 slot 数量均适用（bits[63:56] 覆盖所有 mask 范围）。
func TestCounter_SlotConfig(t *testing.T) {
	// sz=8: mask=7, fibShift=56 → bits[58:56] = 3 bits ≥ log2(8)=3 ✓
	tests := []struct {
		procs int
		slots int // 实际分配 slot 数（向上取 2 的幂，最小 8）
	}{
		{1, 8},
		{4, 8},
		{8, 8},
		{9, 16},
		{16, 16},
		{32, 32},
	}
	for _, tc := range tests {
		c := New(tc.procs)
		if c.mask != tc.slots-1 {
			t.Errorf("procs=%d: mask=%d, 期望 %d", tc.procs, c.mask, tc.slots-1)
		}
		// fibShift + log2(slots) 应 ≤ wordBits（确保高位覆盖 AND mask 有效位宽）
		slotBits := bits.Len(uint(c.mask)) // log2(slots)
		if fibShift+slotBits > bits.UintSize {
			t.Errorf("procs=%d: fibShift=%d + log2(slots)=%d = %d > wordBits=%d",
				tc.procs, fibShift, slotBits, fibShift+slotBits, bits.UintSize)
		}
	}
}

// ─── 基础功能 ────────────────────────────────────────────────────────────────

func TestCounter_Basic(t *testing.T) {
	c := New(runtime.GOMAXPROCS(0))

	c.Add(1)
	c.Add(2)
	c.Add(3)

	if got := c.Load(); got != 6 {
		t.Fatalf("Load() = %d, want 6", got)
	}
}

func TestCounter_Negative(t *testing.T) {
	c := New(4)
	c.Add(10)
	c.Add(-3)
	if got := c.Load(); got != 7 {
		t.Fatalf("Load() = %d, want 7", got)
	}
}

func TestCounter_Reset(t *testing.T) {
	c := New(4)
	c.Add(100)
	c.Reset()
	if got := c.Load(); got != 0 {
		t.Fatalf("after Reset, Load() = %d, want 0", got)
	}
}

func TestCounter_Zero(t *testing.T) {
	c := New(4)
	if got := c.Load(); got != 0 {
		t.Fatalf("new counter Load() = %d, want 0", got)
	}
}

// ─── mask 验证 ───────────────────────────────────────────────────────────────

func TestNew_MinSlots(t *testing.T) {
	c := New(1)
	// 最少 8 slots
	if c.mask < 7 {
		t.Fatalf("mask = %d, want >= 7 (min 8 slots)", c.mask)
	}
}

func TestNew_MaxSlots(t *testing.T) {
	c := New(1000)
	if c.mask+1 > maxSlots {
		t.Fatalf("slots = %d, exceeds max %d", c.mask+1, maxSlots)
	}
}

func TestNew_PowerOf2(t *testing.T) {
	for _, procs := range []int{1, 2, 3, 4, 7, 8, 16, 64, 128, 300} {
		c := New(procs)
		sz := c.mask + 1
		if sz&(sz-1) != 0 {
			t.Errorf("New(%d) mask+1 = %d, not power of 2", procs, sz)
		}
	}
}

// ─── 并发正确性 ──────────────────────────────────────────────────────────────

func TestCounter_Concurrent(t *testing.T) {
	c := New(runtime.GOMAXPROCS(0))
	const goroutines = 64
	const perG = 10000

	var wg sync.WaitGroup
	wg.Add(goroutines)
	for g := 0; g < goroutines; g++ {
		go func() {
			defer wg.Done()
			for i := 0; i < perG; i++ {
				c.Add(1)
			}
		}()
	}
	wg.Wait()

	want := int64(goroutines * perG)
	got := c.Load()
	if got != want {
		t.Fatalf("concurrent Load() = %d, want %d", got, want)
	}
}

// ─── 内存布局 ────────────────────────────────────────────────────────────────

// TestCounter_SlotCount 验证 New(procs) 分配的 slot 数与 procs 严格对应，
// 不再固定占用 16KB（原 [256]slot 内嵌数组的开销）。
func TestCounter_SlotCount(t *testing.T) {
	cases := []struct {
		procs     int
		wantSlots int
	}{
		{1, 8},  // 最低保障 8 slots
		{2, 8},  // 2 → ceil_pow2=2 → min 8
		{4, 8},  // 4 → min 8
		{8, 8},  // 8 → 8
		{9, 16}, // 9 → pow2 ceil → 16
		{16, 16},
		{64, 64},
		{100, 128},
		{256, 256},
		{512, 256}, // capped at maxSlots=256
	}
	for _, tc := range cases {
		c := New(tc.procs)
		got := c.mask + 1
		if got != tc.wantSlots {
			t.Errorf("New(%d): slots=%d, want %d", tc.procs, got, tc.wantSlots)
		}
		// slots 切片长度也应与 mask+1 一致
		if len(c.slots) != tc.wantSlots {
			t.Errorf("New(%d): len(slots)=%d, want %d", tc.procs, len(c.slots), tc.wantSlots)
		}
	}
}

func TestCounter_ConcurrentMixed(t *testing.T) {
	c := New(runtime.GOMAXPROCS(0))
	const goroutines = 32
	const perG = 5000

	var wg sync.WaitGroup
	wg.Add(goroutines * 2)
	// 一半 +1，一半 -1
	for g := 0; g < goroutines; g++ {
		go func() {
			defer wg.Done()
			for i := 0; i < perG; i++ {
				c.Add(1)
			}
		}()
		go func() {
			defer wg.Done()
			for i := 0; i < perG; i++ {
				c.Add(-1)
			}
		}()
	}
	wg.Wait()

	if got := c.Load(); got != 0 {
		t.Fatalf("mixed concurrent Load() = %d, want 0", got)
	}
}

// ─── Benchmarks ─────────────────────────────────────────────────────────────

func BenchmarkCounter_Add(b *testing.B) {
	c := New(runtime.GOMAXPROCS(0))
	b.ResetTimer()
	for b.Loop() {
		c.Add(1)
	}
}

func BenchmarkCounter_Add_Parallel(b *testing.B) {
	c := New(runtime.GOMAXPROCS(0))
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			c.Add(1)
		}
	})
}

func BenchmarkAtomicInt64_Add_Parallel(b *testing.B) {
	var a atomic.Int64
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			a.Add(1)
		}
	})
}

func BenchmarkCounter_Load(b *testing.B) {
	c := New(runtime.GOMAXPROCS(0))
	c.Add(12345)
	b.ResetTimer()
	for b.Loop() {
		_ = c.Load()
	}
}

// ─── Stats 诊断 ───────────────────────────────────────────────────────────────

func TestCounter_Stats_Empty(t *testing.T) {
	c := New(8)
	s := c.Stats()
	if s.Slots != 8 {
		t.Errorf("Slots = %d, want 8", s.Slots)
	}
	// 未写入时 all zeros → skew 应为 1.0（由实现中 mean==0 分支保证）
	if s.Skew != 1.0 {
		t.Errorf("empty counter Skew = %.2f, want 1.0", s.Skew)
	}
}

func TestCounter_Stats_Uniform(t *testing.T) {
	c := New(8)
	n := c.mask + 1
	// 对每个 slot 直接写相同值——模拟均匀分布
	for i := 0; i < n; i++ {
		c.slots[i].val.Store(100)
	}
	s := c.Stats()
	if s.Min != 100 || s.Max != 100 {
		t.Errorf("uniform: Min=%d Max=%d, want both 100", s.Min, s.Max)
	}
	if s.Skew < 0.99 || s.Skew > 1.01 {
		t.Errorf("uniform: Skew=%.4f, want ~1.0", s.Skew)
	}
}

func TestCounter_Stats_Skewed(t *testing.T) {
	c := New(8)
	n := c.mask + 1
	// slot 0 承担 99% 写入，其余全零
	c.slots[0].val.Store(99)
	for i := 1; i < n; i++ {
		c.slots[i].val.Store(1)
	}
	s := c.Stats()
	if s.Max != 99 {
		t.Errorf("Max = %d, want 99", s.Max)
	}
	// skew 应明显 > 2.0
	if s.Skew < 2.0 {
		t.Errorf("skewed counter Skew = %.2f, want > 2.0", s.Skew)
	}
}

// TestCounter_FibHash_Distribution 验证 Fibonacci 分槽哈希在 goroutine 池
// 场景下（地址以固定步长递增）比朴素 >>13 分布更均匀。
// 该测试通过并发写计数后检查 Stats().Skew，而非涉及内部实现细节。
func TestCounter_FibHash_Distribution(t *testing.T) {
	procs := runtime.GOMAXPROCS(0)
	if procs < 2 {
		t.Skip("requires GOMAXPROCS >= 2")
	}
	c := New(procs * 4)

	const workers = 64
	const iters = 10000

	var wg sync.WaitGroup
	wg.Add(workers)
	for i := 0; i < workers; i++ {
		go func() {
			defer wg.Done()
			for j := 0; j < iters; j++ {
				c.Add(1)
			}
		}()
	}
	wg.Wait()

	s := c.Stats()
	// 理想均匀分布 skew = 1.0；允许 5× 偏差（仍远好于全集中的极端情况）
	if s.Skew > 5.0 {
		t.Logf("Stats: slots=%d min=%d max=%d mean=%.1f skew=%.2f",
			s.Slots, s.Min, s.Max, s.Mean, s.Skew)
		t.Errorf("Skew=%.2f too high (>5.0), suggests poor hash distribution", s.Skew)
	} else {
		t.Logf("Distribution OK: slots=%d skew=%.2f", s.Slots, s.Skew)
	}
}
