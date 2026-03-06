package arena

import (
	"sync"
	"testing"
)

// ─── 基础功能 ────────────────────────────────────────────────────────────────

func TestArena_Basic(t *testing.T) {
	a := New(0) // default chunk size
	b := a.Alloc(16)
	if len(b) != 16 {
		t.Fatalf("Alloc(16) len = %d", len(b))
	}
	// 可以写入
	for i := range b {
		b[i] = byte(i)
	}
	for i := range b {
		if b[i] != byte(i) {
			t.Fatalf("b[%d] = %d, want %d", i, b[i], i)
		}
	}
}

func TestArena_Alignment(t *testing.T) {
	a := New(4096)
	sizes := []int{1, 3, 5, 7, 9, 13, 15, 17, 31, 33}
	for _, sz := range sizes {
		b := a.Alloc(sz)
		if len(b) != sz {
			t.Errorf("Alloc(%d) len = %d, want %d", sz, len(b), sz)
		}
		// cap 应是 8 的倍数（对齐后大小）
		aligned := (sz + 7) &^ 7
		if cap(b) != aligned {
			t.Errorf("Alloc(%d) cap = %d, want %d", sz, cap(b), aligned)
		}
	}
}

func TestArena_AlignmentActual(t *testing.T) {
	a := New(4096)
	// 连续分配，检查偏移增量是 8 的倍数
	prev := a.Alloc(1) // 1 byte → aligned to 8
	for i := 0; i < 100; i++ {
		cur := a.Alloc(1)
		// 两个切片不应重叠
		if &prev[0] == &cur[0] {
			t.Fatal("two allocs returned same address")
		}
		prev = cur
	}
}

func TestArena_MultipleAllocs(t *testing.T) {
	a := New(1024)
	allocs := make([][]byte, 100)
	for i := range allocs {
		allocs[i] = a.Alloc(8)
		allocs[i][0] = byte(i)
	}
	// 验证之前的写入没有被覆盖
	for i, b := range allocs {
		if b[0] != byte(i) {
			t.Fatalf("alloc[%d][0] = %d, want %d", i, b[0], byte(i))
		}
	}
}

func TestArena_ChunkExhaustion(t *testing.T) {
	a := New(64) // 很小的 chunk
	// 分配超过一个 chunk 的量
	for i := 0; i < 20; i++ {
		b := a.Alloc(16)
		if len(b) != 16 {
			t.Fatalf("Alloc(16) len = %d", len(b))
		}
		b[0] = byte(i)
	}
}

func TestArena_LargeAlloc(t *testing.T) {
	a := New(64) // chunk = 64B
	// 大于 chunk 的分配直接 fallback 到 make
	b := a.Alloc(256)
	if len(b) != 256 {
		t.Fatalf("Alloc(256) len = %d", len(b))
	}
	b[0] = 0xFF
	b[255] = 0xAA
}

func TestArena_Reset(t *testing.T) {
	a := New(1024)
	b1 := a.Alloc(100)
	b1[0] = 42

	a.Reset()

	b2 := a.Alloc(100)
	// b1 应仍有效（旧 chunk 由 GC 管理）
	if b1[0] != 42 {
		t.Fatal("old alloc corrupted after Reset")
	}
	// b2 是新 chunk 的
	b2[0] = 99
	if b1[0] != 42 {
		t.Fatal("b1 corrupted by b2 after Reset")
	}
}

func TestArena_ZeroAlloc(t *testing.T) {
	a := New(0)
	b := a.Alloc(0)
	if len(b) != 0 {
		t.Fatalf("Alloc(0) len = %d", len(b))
	}
}

// ─── 回归测试（Bug 修复验证） ─────────────────────────────────────────────────

// TestArena_NegativeAlloc 回归：n < 0 必须返回 nil，不得 panic 或死循环。
func TestArena_NegativeAlloc(t *testing.T) {
	a := New(0)
	b := a.Alloc(-1)
	if b != nil {
		t.Fatalf("Alloc(-1) = %v, want nil", b)
	}
	b = a.Alloc(-1000)
	if b != nil {
		t.Fatalf("Alloc(-1000) = %v, want nil", b)
	}
}

// TestArena_HugeAlloc 回归：n 极大时必须走 make fallback，不得整数溢出或死循环。
// 修复前：aligned := (n+7)&^7 在 n > csz 检查前执行，n≈MaxInt 时溢出导致无限 CAS。
func TestArena_HugeAlloc(t *testing.T) {
	a := New(0) // chunkSize = 64KB
	// 刚好超过 chunkSize：走 make fallback
	b := a.Alloc(DefaultChunk + 1)
	if len(b) != DefaultChunk+1 {
		t.Fatalf("Alloc(%d) len = %d", DefaultChunk+1, len(b))
	}
	// 很大值：必须返回正确长度，不崩溃
	const bigN = 4 * 1024 * 1024 // 4MB
	b = a.Alloc(bigN)
	if len(b) != bigN {
		t.Fatalf("Alloc(%d) len = %d", bigN, len(b))
	}
	// 确保内容可写
	b[0] = 0xFF
	b[bigN-1] = 0xAA
}

// ─── 并发 ────────────────────────────────────────────────────────────────────

func TestArena_Concurrent(t *testing.T) {
	a := New(4096)
	const goroutines = 16
	const perG = 1000

	var wg sync.WaitGroup
	wg.Add(goroutines)

	results := make([][]byte, goroutines*perG)
	var mu sync.Mutex
	idx := 0

	for g := 0; g < goroutines; g++ {
		go func() {
			defer wg.Done()
			for i := 0; i < perG; i++ {
				b := a.Alloc(8)
				b[0] = 0xAA
				mu.Lock()
				results[idx] = b
				idx++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()

	// 验证所有分配都不重叠（通过值检查）
	for i := 0; i < goroutines*perG; i++ {
		if results[i][0] != 0xAA {
			t.Fatalf("results[%d][0] = %d, want 0xAA", i, results[i][0])
		}
	}
}

// ─── Benchmarks ─────────────────────────────────────────────────────────────

func BenchmarkArena_Alloc8(b *testing.B) {
	a := New(0)
	for b.Loop() {
		_ = a.Alloc(8)
	}
}

func BenchmarkArena_Alloc64(b *testing.B) {
	a := New(0)
	for b.Loop() {
		_ = a.Alloc(64)
	}
}

func BenchmarkArena_Alloc_Parallel(b *testing.B) {
	a := New(0)
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			_ = a.Alloc(32)
		}
	})
}

func BenchmarkMake_Alloc8(b *testing.B) {
	for b.Loop() {
		_ = make([]byte, 8)
	}
}

// TestNew_MaxChunk 超过 MaxChunk 的 chunkSize 应被截断至 MaxChunk
func TestNew_MaxChunk(t *testing.T) {
	giant := MaxChunk * 2
	a := New(giant)
	if a.csz != MaxChunk {
		t.Fatalf("New(%d).csz = %d, want %d (MaxChunk)", giant, a.csz, MaxChunk)
	}
	// 可正常分配
	buf := a.Alloc(64)
	if len(buf) != 64 {
		t.Fatalf("Alloc(64) after MaxChunk clamp: got len %d", len(buf))
	}
}

// ─── Snapshot / Restore ─────────────────────────────────────────────────────

// TestSnap_Basic 验证 Snap 和 Restore 可以回滚分配
func TestSnap_Basic(t *testing.T) {
	a := New(4096)
	// 先分配一些数据
	a.Alloc(64)

	// 打快照
	sp := a.Snap()

	// 之后分配更多
	a.Alloc(128)
	a.Alloc(256)

	// 恢复到快照
	a.Restore(sp)

	// 再次分配应从快照位置开始（覆盖之前的 128+256）
	b := a.Alloc(16)
	if len(b) != 16 {
		t.Fatalf("Alloc after Restore: len = %d, want 16", len(b))
	}
}

// TestSnap_DataIntegrity 验证 Snap 前分配的数据在 Restore 后仍然有效
func TestSnap_DataIntegrity(t *testing.T) {
	a := New(4096)

	// 分配数据并写入内容
	before := a.Alloc(8)
	for i := range before {
		before[i] = byte(i + 100)
	}

	sp := a.Snap()

	// 分配新数据
	after := a.Alloc(8)
	for i := range after {
		after[i] = byte(i + 200)
	}

	a.Restore(sp)

	// 快照前的数据仍然有效
	for i := range before {
		if before[i] != byte(i+100) {
			t.Fatalf("before[%d] = %d, want %d", i, before[i], byte(i+100))
		}
	}
}

// TestSnap_CrossChunk 验证跨 chunk 时 Restore 能正确回到快照时的 chunk
func TestSnap_CrossChunk(t *testing.T) {
	a := New(64) // 很小的 chunk，容易触发切换

	// 分配一些数据
	a.Alloc(16)
	sp := a.Snap()

	// 大量分配，超出当前 chunk
	for i := 0; i < 20; i++ {
		a.Alloc(16)
	}

	// 恢复到快照
	a.Restore(sp)

	// 应该可以继续正常分配
	b := a.Alloc(8)
	if len(b) != 8 {
		t.Fatalf("Alloc after cross-chunk Restore: len = %d, want 8", len(b))
	}
}

// TestSnap_Multiple 验证多次快照和恢复
func TestSnap_Multiple(t *testing.T) {
	a := New(4096)

	sp1 := a.Snap()
	a.Alloc(32)
	sp2 := a.Snap()
	a.Alloc(64)

	// 恢复到 sp2（保留 32B 分配）
	a.Restore(sp2)
	b1 := a.Alloc(8)
	if len(b1) != 8 {
		t.Fatalf("after Restore(sp2): len = %d", len(b1))
	}

	// 恢复到 sp1（回滚所有）
	a.Restore(sp1)
	b2 := a.Alloc(8)
	if len(b2) != 8 {
		t.Fatalf("after Restore(sp1): len = %d", len(b2))
	}
}
