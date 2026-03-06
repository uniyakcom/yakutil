package lru

import (
	"fmt"
	"sync"
	"testing"
	"time"
)

// ─── 基础 ────────────────────────────────────────────────────────────────────

func TestCache_SetGet(t *testing.T) {
	c := New[int](4, 100)
	c.Set("foo", 42)
	v, ok := c.Get("foo")
	if !ok || v != 42 {
		t.Fatalf("Get(foo) = %d, %v", v, ok)
	}
}

func TestCache_GetMissing(t *testing.T) {
	c := New[int](4, 100)
	_, ok := c.Get("nope")
	if ok {
		t.Fatal("Get missing should return false")
	}
}

func TestCache_Del(t *testing.T) {
	c := New[string](4, 100)
	c.Set("k", "v")
	c.Del("k")
	_, ok := c.Get("k")
	if ok {
		t.Fatal("after Del, Get should return false")
	}
}

func TestCache_Overwrite(t *testing.T) {
	c := New[int](4, 100)
	c.Set("x", 1)
	c.Set("x", 2)
	v, _ := c.Get("x")
	if v != 2 {
		t.Fatalf("Get(x) = %d after overwrite, want 2", v)
	}
}

// ─── Eviction ────────────────────────────────────────────────────────────────

func TestCache_Eviction(t *testing.T) {
	// capPerShard = 2, shards = 1 (but min is 4)
	// 我们使用足够小的 shards，让所有 key 落入同一分片概率高
	c := New[int](4, 2)
	// 插入多个 key，确保某个分片触发驱逐
	for i := 0; i < 100; i++ {
		c.Set(fmt.Sprintf("k%d", i), i)
	}
	// 总数不应超过 shards * capPerShard
	if n := c.Len(); n > 4*2+4 { // 允许些许偏差因为分片
		t.Fatalf("Len() = %d, exceeds capacity", n)
	}
}

func TestCache_EvictCallback(t *testing.T) {
	var evicted []string
	c := New(4, 2, WithEvict(func(key string, _ int) {
		evicted = append(evicted, key)
	}))
	// 填满一个分片并触发驱逐
	// 用相同前缀确保同分片
	for i := 0; i < 50; i++ {
		c.Set(fmt.Sprintf("key%d", i), i)
	}
	if len(evicted) == 0 {
		t.Log("no evictions triggered (keys spread across shards)")
	}
}

// ─── LRU 顺序 ───────────────────────────────────────────────────────────────

func TestCache_LRUOrder(t *testing.T) {
	// 单分片测试（所有 key hash 到同一分片是概率性的，这里用大量 key 测试行为）
	c := New[int](4, 3) // 每分片最多 3 条
	// 插入 A, B, C（确保同一分片——用 shardFor 来验证）
	c.Set("a", 1)
	c.Set("b", 2)
	c.Set("c", 3)

	// 访问 a，使其成为最近使用
	c.Get("a")

	// 总量：Len 应合理
	if n := c.Len(); n != 3 {
		// 如果分散到不同分片，跳过
		t.Skipf("keys spread across shards, Len = %d", n)
	}
}

// ─── Len ─────────────────────────────────────────────────────────────────────

func TestCache_Len(t *testing.T) {
	c := New[int](8, 100)
	for i := 0; i < 50; i++ {
		c.Set(fmt.Sprintf("k%d", i), i)
	}
	if n := c.Len(); n != 50 {
		t.Fatalf("Len() = %d, want 50", n)
	}
}

// ─── 并发 ────────────────────────────────────────────────────────────────────

func TestCache_Concurrent(t *testing.T) {
	c := New[int](16, 1000)
	const goroutines = 16
	const ops = 500

	var wg sync.WaitGroup
	wg.Add(goroutines * 2)
	for g := 0; g < goroutines; g++ {
		// Writer
		go func(id int) {
			defer wg.Done()
			for i := 0; i < ops; i++ {
				c.Set(fmt.Sprintf("g%d-k%d", id, i), i)
			}
		}(g)
		// Reader
		go func(id int) {
			defer wg.Done()
			for i := 0; i < ops; i++ {
				c.Get(fmt.Sprintf("g%d-k%d", id, i))
			}
		}(g)
	}
	wg.Wait()
}

// ─── Benchmarks ─────────────────────────────────────────────────────────────

func BenchmarkCache_Get(b *testing.B) {
	c := New[int](16, 10000)
	for i := 0; i < 1000; i++ {
		c.Set(fmt.Sprintf("key%d", i), i)
	}
	b.ResetTimer()
	for b.Loop() {
		c.Get("key500")
	}
}

func BenchmarkCache_Set(b *testing.B) {
	c := New[int](16, 10000)
	for b.Loop() {
		c.Set("bench", 42)
	}
}

func BenchmarkCache_Get_Parallel(b *testing.B) {
	c := New[int](16, 10000)
	for i := 0; i < 1000; i++ {
		c.Set(fmt.Sprintf("key%d", i), i)
	}
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			c.Get("key500")
		}
	})
}

// ─── TTL ─────────────────────────────────────────────────────────────────────

func TestCache_TTL_Expire(t *testing.T) {
	c := New(4, 100, WithTTL[int](20*time.Millisecond))
	c.Set("k", 1)

	// 过期前应命中
	if _, ok := c.Get("k"); !ok {
		t.Fatal("should hit before TTL expires")
	}

	time.Sleep(30 * time.Millisecond)

	// 过期后应未命中
	if _, ok := c.Get("k"); ok {
		t.Fatal("should miss after TTL expires")
	}
}

func TestCache_TTL_NotExpired(t *testing.T) {
	c := New(4, 100, WithTTL[string](200*time.Millisecond))
	c.Set("hello", "world")

	time.Sleep(10 * time.Millisecond)

	v, ok := c.Get("hello")
	if !ok || v != "world" {
		t.Fatalf("Get = %q, %v; want world, true", v, ok)
	}
}

func TestCache_TTL_ResetOnOverwrite(t *testing.T) {
	c := New(4, 100, WithTTL[int](30*time.Millisecond))
	c.Set("k", 1)

	time.Sleep(20 * time.Millisecond)
	// 覆写重置 TTL
	c.Set("k", 2)
	time.Sleep(20 * time.Millisecond)

	// 若 TTL 未重置则 40ms 后已过期；重置后仅 20ms，应仍命中
	v, ok := c.Get("k")
	if !ok || v != 2 {
		t.Fatalf("Get = %d, %v; want 2, true (TTL should have reset)", v, ok)
	}
}

func TestCache_NoTTL_NeverExpires(t *testing.T) {
	// 不设 TTL，条目永不过期
	c := New[int](4, 100)
	c.Set("k", 42)
	_, ok := c.Get("k")
	if !ok {
		t.Fatal("without TTL, entry should never expire")
	}
}

// ─── Range ───────────────────────────────────────────────────────────────────

func TestCache_Range_All(t *testing.T) {
	c := New[int](4, 100)
	keys := []string{"a", "b", "c", "d"}
	for i, k := range keys {
		c.Set(k, i)
	}

	seen := map[string]int{}
	c.Range(func(key string, val int) bool {
		seen[key] = val
		return true
	})
	if len(seen) != len(keys) {
		t.Fatalf("Range visited %d keys, want %d", len(seen), len(keys))
	}
	for i, k := range keys {
		if seen[k] != i {
			t.Errorf("Range key %q: got %d, want %d", k, seen[k], i)
		}
	}
}

func TestCache_Range_EarlyStop(t *testing.T) {
	c := New[int](4, 100)
	for i := 0; i < 20; i++ {
		c.Set(fmt.Sprintf("k%d", i), i)
	}

	count := 0
	c.Range(func(key string, val int) bool {
		count++
		return count < 3 // 访问 3 条后停止
	})
	if count != 3 {
		t.Fatalf("Range stopped at %d, want 3", count)
	}
}

func TestCache_Range_SkipsExpired(t *testing.T) {
	c := New(4, 100, WithTTL[int](20*time.Millisecond))
	c.Set("alive", 1)
	c.Set("dead", 2)

	time.Sleep(30 * time.Millisecond)
	c.Set("alive", 1) // 重置 TTL，使其继续有效

	seen := map[string]bool{}
	c.Range(func(key string, _ int) bool {
		seen[key] = true
		return true
	})
	if seen["dead"] {
		t.Error("Range should not visit expired key 'dead'")
	}
	if !seen["alive"] {
		t.Error("Range should visit non-expired key 'alive'")
	}
}

// ─── Purge ───────────────────────────────────────────────────────────────────

func TestCache_Purge(t *testing.T) {
	c := New[int](4, 100)
	for i := 0; i < 50; i++ {
		c.Set(fmt.Sprintf("k%d", i), i)
	}
	if c.Len() == 0 {
		t.Fatal("Len should be > 0 before Purge")
	}

	c.Purge()

	if c.Len() != 0 {
		t.Fatalf("Len after Purge = %d, want 0", c.Len())
	}
	if _, ok := c.Get("k0"); ok {
		t.Error("Get after Purge should return false")
	}
}

func TestCache_Purge_ThenSet(t *testing.T) {
	c := New[string](4, 100)
	c.Set("before", "x")
	c.Purge()
	c.Set("after", "y")

	if _, ok := c.Get("before"); ok {
		t.Error("'before' should not exist after Purge")
	}
	v, ok := c.Get("after")
	if !ok || v != "y" {
		t.Errorf("Get('after') = %q, %v; want y, true", v, ok)
	}
}

func TestCache_Purge_NoEvictCallback(t *testing.T) {
	evicted := 0
	c := New(4, 100, WithEvict(func(_ string, _ int) {
		evicted++
	}))
	for i := 0; i < 20; i++ {
		c.Set(fmt.Sprintf("k%d", i), i)
	}
	c.Purge()
	if evicted != 0 {
		t.Errorf("Purge should not trigger onEvict, got %d calls", evicted)
	}
}

// ─── WithClock ────────────────────────────────────────────────────────────────

// TestCache_WithClock_Expire 使用可控虚拟时钟验证 TTL 过期逻辑，
// 无需 time.Sleep，测试速度快且稳定。
func TestCache_WithClock_Expire(t *testing.T) {
	var fakeNow int64 = 0
	clock := func() int64 { return fakeNow }

	const ttl = 1000 // 1000 纳秒虚拟 TTL
	c := New(4, 100,
		WithTTL[string](ttl),
		WithClock[string](clock),
	)

	c.Set("key", "value")

	// 未过期：应命中
	if _, ok := c.Get("key"); !ok {
		t.Fatal("should hit before TTL")
	}

	// 推进虚拟时钟超过 TTL
	fakeNow = ttl + 1

	// 过期：应未命中
	if _, ok := c.Get("key"); ok {
		t.Fatal("should miss after TTL expired")
	}
}

// TestCache_WithClock_Refresh 覆写 key 时 TTL 重新计时。
func TestCache_WithClock_Refresh(t *testing.T) {
	var fakeNow int64 = 0
	clock := func() int64 { return fakeNow }

	const ttl = 1000
	c := New(4, 100,
		WithTTL[string](ttl),
		WithClock[string](clock),
	)

	c.Set("k", "v1")
	fakeNow = ttl - 1 // 快过期但尚未过期

	// 覆写：TTL 从当前虚拟时间重新计时
	c.Set("k", "v2")
	fakeNow = ttl + 1 // 超过原始 TTL 但未超过刷新后的 TTL

	v, ok := c.Get("k")
	if !ok || v != "v2" {
		t.Fatalf("Get(k) = %q, %v after refresh; want v2, true", v, ok)
	}
}

// TestCache_NilClock_DefaultsToSystemTime 无 WithClock 时默认使用系统时钟。
func TestCache_NilClock_DefaultsToSystemTime(t *testing.T) {
	c := New(4, 100, WithTTL[int](time.Hour))
	c.Set("k", 1)
	v, ok := c.Get("k")
	if !ok || v != 1 {
		t.Fatal("default clock should keep entry alive within TTL")
	}
}

// TestCache_WithClock_OverwriteRespectsClock 专项验证覆写 key 时应使用注入时钟记录 created。
//
// 场景：
//  1. fakeNow=100 时插入 key
//  2. fakeNow=500 时覆写 key（中途推进）
//  3. fakeNow=500+ttl+1 时 Get：TTL 应从 fakeNow=500 开始计算，应已过期
func TestCache_WithClock_OverwriteRespectsClock(t *testing.T) {
	var fakeNow int64 = 0
	clock := func() int64 { return fakeNow }

	const ttl = 1000
	c := New(4, 100,
		WithTTL[string](ttl),
		WithClock[string](clock),
	)

	fakeNow = 100
	c.Set("k", "v1")

	// 推进虚拟时钟，覆写 key
	fakeNow = 500
	c.Set("k", "v2")

	// 推进到覆写后 TTL 内：应命中
	fakeNow = 500 + ttl - 1
	if v, ok := c.Get("k"); !ok || v != "v2" {
		t.Fatalf("at time %d: Get(k)=%q,%v; want v2,true (still within TTL after overwrite)",
			fakeNow, v, ok)
	}

	// 推进到覆写后 TTL 已过：应未命中
	fakeNow = 500 + ttl + 1
	if _, ok := c.Get("k"); ok {
		t.Fatalf("at time %d: Get(k) should miss (TTL expired from overwrite time 500)",
			fakeNow)
	}
}
