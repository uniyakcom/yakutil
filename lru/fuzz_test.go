package lru

import (
	"testing"
	"time"
)

// FuzzSetGet 验证 Set 后 Get 必须能取回相同值（不含 TTL）。
func FuzzSetGet(f *testing.F) {
	f.Add("hello", 42)
	f.Add("", 0)
	f.Add("a", -1)
	f.Add("long/key/path/component", 9999)
	f.Fuzz(func(t *testing.T, key string, val int) {
		c := New[int](4, 8)
		c.Set(key, val)
		got, ok := c.Get(key)
		if !ok {
			t.Errorf("Get(%q) not found after Set", key)
		}
		if got != val {
			t.Errorf("Get(%q) = %d, want %d", key, got, val)
		}
	})
}

// FuzzSetDelGet 验证 Set 后 Del 再 Get 应返回 not-found。
func FuzzSetDelGet(f *testing.F) {
	f.Add("foo")
	f.Add("")
	f.Add("\x00")
	f.Fuzz(func(t *testing.T, key string) {
		c := New[int](4, 8)
		c.Set(key, 1)
		c.Del(key)
		if _, ok := c.Get(key); ok {
			t.Errorf("Get(%q) found after Del", key)
		}
	})
}

// FuzzRangeLimit 验证 RangeLimit 在任意 batchSize 下不 panic，
// 且回调次数不超过实际条目数。
func FuzzRangeLimit(f *testing.F) {
	f.Add(1)
	f.Add(0)
	f.Add(100)
	f.Add(7)
	f.Fuzz(func(t *testing.T, batchSize int) {
		if batchSize < 0 {
			batchSize = -batchSize
		}
		c := New[int](4, 32)
		const total = 20
		for i := 0; i < total; i++ {
			c.Set(string(rune('a'+i%26))+string(rune('0'+i/26)), i)
		}
		count := 0
		c.RangeLimit(batchSize, func(_ string, _ int) bool {
			count++
			return true
		})
		if count > total {
			t.Errorf("RangeLimit(batchSize=%d) visited %d > total %d", batchSize, count, total)
		}
	})
}

// FuzzTTL_FakeClock 验证 WithClock 注入后过期逻辑正确：
// 时钟未到 TTL 时命中，超过 TTL 时未命中。
func FuzzTTL_FakeClock(f *testing.F) {
	f.Add(int64(0), int64(1000))
	f.Add(int64(100), int64(200))
	f.Add(int64(0), int64(1))
	f.Fuzz(func(t *testing.T, insertTime, ttlNano int64) {
		// 限制值域，防止 int64 溢出干扰 TTL 逻辑验证
		const maxTTL = int64(1_000_000_000) // 1s
		if ttlNano <= 0 || ttlNano > maxTTL {
			ttlNano = 1000
		}
		if insertTime < 0 {
			insertTime = -insertTime
		}
		const bound = int64(1) << 60
		if insertTime > bound-maxTTL-2 {
			insertTime = 0
		}

		ttl := time.Duration(ttlNano)
		fakeNow := insertTime
		clock := func() int64 { return fakeNow }

		c := New(4, 8, WithTTL[int](ttl), WithClock[int](clock))

		fakeNow = insertTime
		c.Set("k", 1)

		// 在 TTL 内查询（time delta = ttlNano-1）：应命中
		// TTL 检查：now - created > ttl（严格大于），ttlNano-1 < ttlNano → 未过期
		fakeNow = insertTime + ttlNano - 1
		v, ok := c.Get("k")
		if !ok {
			t.Errorf("within TTL: Get() miss; insertTime=%d ttl=%d query=%d",
				insertTime, ttlNano, fakeNow)
		}
		if ok && v != 1 {
			t.Errorf("within TTL: Get() = %d, want 1", v)
		}

		// 超过 TTL：time delta = ttlNano+1 > ttlNano → 已过期
		fakeNow = insertTime + ttlNano + 1
		if _, ok := c.Get("k"); ok {
			t.Errorf("after TTL: Get() hit; insertTime=%d ttl=%d query=%d",
				insertTime, ttlNano, fakeNow)
		}
	})
}
