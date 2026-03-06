package bufpool

import (
	"testing"
)

// ─── 基础功能 ────────────────────────────────────────────────────────────────

func TestPool_GetPut(t *testing.T) {
	var p Pool
	buf := p.Get(100)
	if len(buf) != 100 {
		t.Fatalf("Get(100) len = %d", len(buf))
	}
	p.Put(buf)

	// 再次获取，可能命中池
	buf2 := p.Get(50)
	if len(buf2) != 50 {
		t.Fatalf("Get(50) len = %d", len(buf2))
	}
	p.Put(buf2)
}

func TestPool_CapPowerOf2(t *testing.T) {
	var p Pool
	sizes := []int{1, 10, 63, 64, 65, 100, 1000, 4096, 10000}
	for _, sz := range sizes {
		buf := p.Get(sz)
		c := cap(buf)
		if c < sz {
			t.Errorf("Get(%d) cap = %d < size", sz, c)
		}
		// cap 应该是 2^n
		if c&(c-1) != 0 {
			t.Errorf("Get(%d) cap = %d, not power of 2", sz, c)
		}
		p.Put(buf)
	}
}

func TestPool_MinSize(t *testing.T) {
	var p Pool
	buf := p.Get(1)
	if cap(buf) < (1 << minBits) {
		t.Fatalf("Get(1) cap = %d, want >= %d", cap(buf), 1<<minBits)
	}
	p.Put(buf)
}

func TestPool_ZeroSize(t *testing.T) {
	var p Pool
	buf := p.Get(0)
	if len(buf) != 1 { // 0 → 1
		t.Fatalf("Get(0) len = %d, want 1", len(buf))
	}
	p.Put(buf)
}

func TestPool_LargeSize(t *testing.T) {
	var p Pool
	sz := 1 << 24 // 16MB
	buf := p.Get(sz)
	if len(buf) != sz {
		t.Fatalf("Get(%d) len = %d", sz, len(buf))
	}
	p.Put(buf)
}

func TestPool_MaxSize(t *testing.T) {
	var p Pool
	sz := maxSize // 恰好 32MB
	buf := p.Get(sz)
	if len(buf) != sz {
		t.Fatalf("Get(%d) len = %d", sz, len(buf))
	}
	if cap(buf) < sz {
		t.Fatalf("Get(%d) cap = %d < size", sz, cap(buf))
	}
	p.Put(buf)
}

// TestPool_OversizeNoPanic 验证 Get(size > 32MB) 不再 panic。
func TestPool_OversizeNoPanic(t *testing.T) {
	var p Pool
	sizes := []int{
		maxSize + 1,    // 32MB + 1B
		maxSize + 4096, // 32MB + 4KB
		maxSize * 2,    // 64MB
	}
	for _, sz := range sizes {
		buf := p.Get(sz) // 不应 panic
		if len(buf) != sz {
			t.Errorf("Get(%d) len = %d, want %d", sz, len(buf), sz)
		}
		if cap(buf) < sz {
			t.Errorf("Get(%d) cap = %d < len %d", sz, cap(buf), sz)
		}
		// Put 应静默丢弃超大切片（不 panic）
		p.Put(buf)
	}
}

// ─── Global 函数 ─────────────────────────────────────────────────────────────

func TestGlobal_GetPut(t *testing.T) {
	buf := Get(256)
	if len(buf) != 256 {
		t.Fatalf("Get(256) len = %d", len(buf))
	}
	Put(buf)
}

// ─── Put 边界 ────────────────────────────────────────────────────────────────

func TestPool_PutSmall(t *testing.T) {
	var p Pool
	// cap < 64 的切片应被丢弃（不 panic）
	small := make([]byte, 10, 32)
	p.Put(small) // should not panic
}

func TestPool_PutOversized(t *testing.T) {
	var p Pool
	// cap > 32MB 的切片应被丢弃
	huge := make([]byte, 0, 1<<26) // 64MB
	p.Put(huge)                    // should not panic
}

func TestPool_PutNil(t *testing.T) {
	var p Pool
	p.Put(nil) // should not panic
}

// ─── Put 非 2^n cap 切片安全性 ─────────────────────────────────────────────

func TestPool_PutNonPow2Cap(t *testing.T) {
	var p Pool
	// cap=100 不是 2^n，应被丢弃而非归入池
	buf := make([]byte, 50, 100)
	p.Put(buf)

	// Get(80)从 tier 1(128B)取，不应拿到 cap=100 的切片
	// 如果 bug 存在，这里会 panic
	for i := 0; i < 100; i++ {
		b := p.Get(120)
		if cap(b) < 120 {
			t.Fatalf("Get(120) cap = %d < 120", cap(b))
		}
		p.Put(b)
	}
}

// ─── tier 计算 ───────────────────────────────────────────────────────────────

func TestTier(t *testing.T) {
	tests := []struct {
		n    int
		want int
	}{
		{1, 0},
		{64, 0},
		{65, 1},
		{128, 1},
		{129, 2},
		{256, 2},
		{1024, 4},
		{4096, 6},
	}
	for _, tt := range tests {
		got := tier(tt.n)
		if got != tt.want {
			t.Errorf("tier(%d) = %d, want %d", tt.n, got, tt.want)
		}
	}
}

func TestTier_Monotonic(t *testing.T) {
	prev := tier(1)
	for n := 2; n <= (1 << maxBits); n++ {
		cur := tier(n)
		if cur < prev {
			t.Fatalf("tier(%d) = %d < tier(%d) = %d", n, cur, n-1, prev)
		}
		prev = cur
	}
}

// ─── Benchmarks ─────────────────────────────────────────────────────────────

func BenchmarkPool_Get128(b *testing.B) {
	var p Pool
	for b.Loop() {
		buf := p.Get(128)
		p.Put(buf)
	}
}

func BenchmarkPool_Get4K(b *testing.B) {
	var p Pool
	for b.Loop() {
		buf := p.Get(4096)
		p.Put(buf)
	}
}

func BenchmarkPool_Get128_Parallel(b *testing.B) {
	var p Pool
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			buf := p.Get(128)
			p.Put(buf)
		}
	})
}

func BenchmarkMake_128(b *testing.B) {
	for b.Loop() {
		_ = make([]byte, 128)
	}
}
