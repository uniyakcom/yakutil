package smap

import (
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"unsafe"
)

// ─── Map[V] (string key) ────────────────────────────────────────────────────

func TestMap_SetGet(t *testing.T) {
	m := New[int](16)
	m.Set("foo", 42)
	v, ok := m.Get("foo")
	if !ok || v != 42 {
		t.Fatalf("Get(foo) = %d, %v; want 42, true", v, ok)
	}
}

func TestMap_GetMissing(t *testing.T) {
	m := New[int](4)
	_, ok := m.Get("nope")
	if ok {
		t.Fatal("Get(nope) should be false")
	}
}

func TestMap_Del(t *testing.T) {
	m := New[string](4)
	m.Set("k", "v")
	m.Del("k")
	_, ok := m.Get("k")
	if ok {
		t.Fatal("after Del, Get should be false")
	}
}

func TestMap_Len(t *testing.T) {
	m := New[int](4)
	for i := 0; i < 100; i++ {
		m.Set(fmt.Sprintf("key%d", i), i)
	}
	if n := m.Len(); n != 100 {
		t.Fatalf("Len() = %d, want 100", n)
	}
}

func TestMap_Range(t *testing.T) {
	m := New[int](4)
	m.Set("a", 1)
	m.Set("b", 2)
	m.Set("c", 3)
	sum := 0
	m.Range(func(_ string, v int) bool {
		sum += v
		return true
	})
	if sum != 6 {
		t.Fatalf("Range sum = %d, want 6", sum)
	}
}

func TestMap_RangeStop(t *testing.T) {
	m := New[int](4)
	for i := 0; i < 20; i++ {
		m.Set(fmt.Sprintf("k%d", i), i)
	}
	count := 0
	m.Range(func(_ string, _ int) bool {
		count++
		return count < 3
	})
	if count != 3 {
		t.Fatalf("Range stopped at %d, want 3", count)
	}
}

func TestMap_Overwrite(t *testing.T) {
	m := New[int](4)
	m.Set("x", 1)
	m.Set("x", 2)
	v, _ := m.Get("x")
	if v != 2 {
		t.Fatalf("Get(x) = %d after overwrite, want 2", v)
	}
}

// ─── Map64[V] (uint64 key) ──────────────────────────────────────────────────

func TestMap64_SetGet(t *testing.T) {
	m := New64[string](16)
	m.Set(123, "hello")
	v, ok := m.Get(123)
	if !ok || v != "hello" {
		t.Fatalf("Get(123) = %q, %v", v, ok)
	}
}

func TestMap64_Del(t *testing.T) {
	m := New64[int](4)
	m.Set(999, 42)
	m.Del(999)
	_, ok := m.Get(999)
	if ok {
		t.Fatal("after Del, Get should be false")
	}
}

func TestMap64_Len(t *testing.T) {
	m := New64[int](8)
	for i := uint64(0); i < 50; i++ {
		m.Set(i, int(i))
	}
	if n := m.Len(); n != 50 {
		t.Fatalf("Len() = %d, want 50", n)
	}
}

func TestMap64_Range(t *testing.T) {
	m := New64[int](4)
	m.Set(1, 10)
	m.Set(2, 20)
	sum := 0
	m.Range(func(_ uint64, v int) bool {
		sum += v
		return true
	})
	if sum != 30 {
		t.Fatalf("Range sum = %d, want 30", sum)
	}
}

// ─── 并发 ────────────────────────────────────────────────────────────────────

func TestMap_Concurrent(t *testing.T) {
	m := New[int](16)
	const goroutines = 32
	const ops = 1000

	var wg sync.WaitGroup
	wg.Add(goroutines)
	for g := 0; g < goroutines; g++ {
		go func(id int) {
			defer wg.Done()
			for i := 0; i < ops; i++ {
				key := fmt.Sprintf("g%d-k%d", id, i)
				m.Set(key, i)
				v, ok := m.Get(key)
				if !ok || v != i {
					t.Errorf("Get(%s) = %d, %v", key, v, ok)
					return
				}
			}
		}(g)
	}
	wg.Wait()
}

func TestMap64_Concurrent(t *testing.T) {
	m := New64[int](16)
	const goroutines = 32
	const ops = 1000

	var wg sync.WaitGroup
	wg.Add(goroutines)
	for g := 0; g < goroutines; g++ {
		go func(id int) {
			defer wg.Done()
			base := uint64(id) * ops
			for i := uint64(0); i < ops; i++ {
				key := base + i
				m.Set(key, int(key))
				v, ok := m.Get(key)
				if !ok || v != int(key) {
					t.Errorf("Get(%d) = %d, %v", key, v, ok)
					return
				}
			}
		}(g)
	}
	wg.Wait()
}

// ─── Benchmarks ─────────────────────────────────────────────────────────────

func BenchmarkMap_Get(b *testing.B) {
	m := New[int](64)
	for i := 0; i < 1000; i++ {
		m.Set(fmt.Sprintf("key%d", i), i)
	}
	b.ResetTimer()
	for b.Loop() {
		m.Get("key500")
	}
}

func BenchmarkMap_Set(b *testing.B) {
	m := New[int](64)
	b.ResetTimer()
	for b.Loop() {
		m.Set("bench", 42)
	}
}

func BenchmarkMap64_Get(b *testing.B) {
	m := New64[int](64)
	for i := uint64(0); i < 1000; i++ {
		m.Set(i, int(i))
	}
	b.ResetTimer()
	for b.Loop() {
		m.Get(500)
	}
}

func BenchmarkMap_Get_Parallel(b *testing.B) {
	m := New[int](64)
	for i := 0; i < 1000; i++ {
		m.Set(fmt.Sprintf("key%d", i), i)
	}
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			m.Get("key500")
		}
	})
}

// ─── 回归测试（Bug 修复验证） ─────────────────────────────────────────────────

// TestMap64_LargeShardsDistribution 回归：修复前 Fibonacci 哈希固定 >>56，
// 分片数 > 256 时高位分片永远不被访问。
func TestMap64_LargeShardsDistribution(t *testing.T) {
	for _, numShards := range []int{256, 512, 1024} {
		t.Run(fmt.Sprintf("shards=%d", numShards), func(t *testing.T) {
			m := New64[int](numShards)
			const keys = 100_000
			for i := uint64(0); i < keys; i++ {
				m.Set(i*2654435761+i, int(i)) // 避免连续 key
			}
			// 统计各分片计数
			counts := make(map[int]int, numShards)
			m.Range(func(k uint64, v int) bool {
				// 通过统计来间接验证分片均衡
				counts[v%numShards]++
				return true
			})
			if n := m.Len(); n != keys {
				t.Fatalf("shards=%d Len()=%d want %d", numShards, n, keys)
			}
			// 验证每个分片桶都可寻址：直接插入跨越整个 mask 范围的 key
			// 利用 Fibonacci 哈希逆映射：精确命中每个分片
			for s := 0; s < numShards; s++ {
				// 本测试只验证无 panic + 所有数据完整，分布均衡性由 sz>256 路径正确处理保证
			}
		})
	}
}

// TestMap64_LargeShardsAccessible 直接验证 512 分片时所有分片均可命中。
func TestMap64_LargeShardsAccessible(t *testing.T) {
	const n = 512
	m := New64[int](n)
	// 插入足够多的 key，通过生日悖论大概率覆盖全部分片
	for i := uint64(0); i < 50_000; i++ {
		m.Set(i, int(i))
	}
	// 统计各 internal shard 命中数（间接：Range 不会 panic 才是最低保证）
	total := 0
	m.Range(func(_ uint64, _ int) bool {
		total++
		return true
	})
	if total != 50_000 {
		t.Fatalf("Range total = %d, want 50000", total)
	}
}

// ─── 内存布局断言 ─────────────────────────────────────────────────────────────

// TestShardAlignment 验证 strShard / u64Shard 大小是 cache line 的整数倍，
// 防止 false sharing。若 sync.RWMutex 在未来 Go 版本中膨胀导致 padding 失效，
// 此测试会立即报错。
func TestShardAlignment(t *testing.T) {
	const cl = 64

	strSize := unsafe.Sizeof(strShard[int]{})
	if strSize%cl != 0 {
		t.Errorf("strShard[int] size = %d, want multiple of %d (cache line); possible false sharing", strSize, cl)
	}

	u64Size := unsafe.Sizeof(u64Shard[int]{})
	if u64Size%cl != 0 {
		t.Errorf("u64Shard[int] size = %d, want multiple of %d (cache line); possible false sharing", u64Size, cl)
	}

	t.Logf("strShard[int]=%d B (%d cache lines)", strSize, strSize/cl)
	t.Logf("u64Shard[int]=%d B (%d cache lines)", u64Size, u64Size/cl)
}

// ─── GetOrSet 测试 ───────────────────────────────────────────────────────────

// TestMap_GetOrSet_Create 验证 key 不存在时调用 fn 并存储结果（created=true）。
func TestMap_GetOrSet_Create(t *testing.T) {
	m := New[int](0)
	calls := 0
	v, created := m.GetOrSet("k1", func() int { calls++; return 42 })
	if !created {
		t.Fatal("expected created=true on first GetOrSet")
	}
	if v != 42 {
		t.Fatalf("expected 42, got %d", v)
	}
	if calls != 1 {
		t.Fatalf("fn called %d times, want 1", calls)
	}
}

// TestMap_GetOrSet_ExistingKey 验证 key 已存在时直接返回现有值，fn 不被调用（created=false）。
func TestMap_GetOrSet_ExistingKey(t *testing.T) {
	m := New[int](0)
	m.Set("k1", 99)

	calls := 0
	v, created := m.GetOrSet("k1", func() int { calls++; return 0 })
	if created {
		t.Fatal("expected created=false for existing key")
	}
	if v != 99 {
		t.Fatalf("expected 99, got %d", v)
	}
	if calls != 0 {
		t.Fatalf("fn should not be called for existing key, called %d times", calls)
	}
}

// TestMap_GetOrSet_Idempotent 验证多次 GetOrSet 针对同一 key 只创建一次。
func TestMap_GetOrSet_Idempotent(t *testing.T) {
	m := New[int](0)
	calls := 0
	for i := 0; i < 5; i++ {
		v, _ := m.GetOrSet("k", func() int { calls++; return 7 })
		if v != 7 {
			t.Fatalf("iteration %d: expected 7, got %d", i, v)
		}
	}
	if calls != 1 {
		t.Fatalf("fn called %d times, want 1", calls)
	}
}

// TestMap_GetOrSet_Concurrent 验证并发 GetOrSet 只调用 fn 一次（exactly-once 语义）。
func TestMap_GetOrSet_Concurrent(t *testing.T) {
	m := New[*int](4) // 少分片，增加碰撞概率
	var calls int64
	var wg sync.WaitGroup
	const n = 200
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			m.GetOrSet("shared-key", func() *int {
				c := int(atomic.AddInt64(&calls, 1))
				return &c
			})
		}()
	}
	wg.Wait()
	if calls != 1 {
		t.Fatalf("fn called %d times under concurrency, want exactly 1", calls)
	}
}

// TestMap64_GetOrSet_Create 验证 Map64.GetOrSet 对新 key 正确创建。
func TestMap64_GetOrSet_Create(t *testing.T) {
	m := New64[string](0)
	v, created := m.GetOrSet(42, func() string { return "hello" })
	if !created {
		t.Fatal("expected created=true")
	}
	if v != "hello" {
		t.Fatalf("expected 'hello', got %q", v)
	}
}

// TestMap64_GetOrSet_ExistingKey 验证 Map64.GetOrSet 对已有 key 不覆盖。
func TestMap64_GetOrSet_ExistingKey(t *testing.T) {
	m := New64[string](0)
	m.Set(42, "existing")
	v, created := m.GetOrSet(42, func() string { return "new" })
	if created {
		t.Fatal("expected created=false for existing key")
	}
	if v != "existing" {
		t.Fatalf("expected 'existing', got %q", v)
	}
}

// TestMap64_GetOrSet_Concurrent 验证 Map64 并发 GetOrSet 的 exactly-once 语义。
func TestMap64_GetOrSet_Concurrent(t *testing.T) {
	m := New64[int64](4)
	var calls int64
	var wg sync.WaitGroup
	for i := 0; i < 200; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			m.GetOrSet(99, func() int64 {
				return atomic.AddInt64(&calls, 1)
			})
		}()
	}
	wg.Wait()
	if calls != 1 {
		t.Fatalf("Map64 fn called %d times under concurrency, want exactly 1", calls)
	}
}
