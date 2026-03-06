// Package lru 提供分片 LRU 缓存。
//
// 多分片减少锁竞争，每分片独立 LRU 链表 + map。
// string key + maphash 分片路由（AES-NI 加速，进程内使用）。
//
// # 哈希策略
//
// shardFor 基于 hash.Sum64sMap（maphash），较 FNV-1a 在中长 key（≥32B）
// 吞吐提升 4–12×。seed 随进程重启变化，仅用于本地分片路由。
//
// 性能参考（Intel Xeon E-2186G @ 3.80GHz，Go 1.25）：
// Get ~30-50ns（含分片 Mutex Lock），Set ~50-80ns。
// 注：Get 需 moveToFront 修改链表，故使用 Mutex 而非 RWMutex。
package lru

import (
	"sync"
	"time"

	"github.com/uniyakcom/yakutil/hash"
)

// EvictFn 条目被 LRU 容量驱逐时的回调（TTL 过期不触发）。
type EvictFn[V any] func(key string, val V)

// node 双向链表节点。
type node[V any] struct {
	key     string
	val     V
	created int64 // UnixNano；TTL 禁用时为 0
	prev    *node[V]
	next    *node[V]
}

// shard 单个分片。
type shard[V any] struct {
	mu      sync.Mutex
	items   map[string]*node[V]
	head    node[V]      // sentinel（head.next = 最新，head.prev = 最旧）
	nowFn   func() int64 // 时钟源，TTL 禁用时为 nil
	cap     int
	ttl     int64 // 过期阈值（纳秒）；0 = 禁用
	onEvict EvictFn[V]
}

func (s *shard[V]) init(cap int, ttl int64, onEvict EvictFn[V], nowFn func() int64) {
	s.items = make(map[string]*node[V])
	s.cap = cap
	s.ttl = ttl
	s.onEvict = onEvict
	if ttl > 0 {
		if nowFn != nil {
			s.nowFn = nowFn
		} else {
			s.nowFn = func() int64 { return time.Now().UnixNano() }
		}
	}
	s.head.next = &s.head
	s.head.prev = &s.head
}

func (s *shard[V]) pushFront(n *node[V]) {
	n.prev = &s.head
	n.next = s.head.next
	s.head.next.prev = n
	s.head.next = n
}

func (s *shard[V]) remove(n *node[V]) {
	n.prev.next = n.next
	n.next.prev = n.prev
}

func (s *shard[V]) moveToFront(n *node[V]) {
	s.remove(n)
	s.pushFront(n)
}

func (s *shard[V]) get(key string) (V, bool) {
	s.mu.Lock()
	n, ok := s.items[key]
	if !ok {
		s.mu.Unlock()
		var zero V
		return zero, false
	}
	// 惰性 TTL 检查：过期则就地删除，按未命中处理（不触发 onEvict）
	if s.ttl > 0 && s.nowFn()-n.created > s.ttl {
		s.remove(n)
		delete(s.items, key)
		s.mu.Unlock()
		var zero V
		return zero, false
	}
	s.moveToFront(n)
	val := n.val
	s.mu.Unlock()
	return val, true
}

func (s *shard[V]) set(key string, val V) {
	s.mu.Lock()
	if n, ok := s.items[key]; ok {
		n.val = val
		if s.ttl > 0 {
			n.created = s.nowFn() // 覆写时重置 TTL（使用注入时钟而非 time.Now）
		}
		s.moveToFront(n)
		s.mu.Unlock()
		return
	}
	var ts int64
	if s.ttl > 0 {
		ts = s.nowFn()
	}
	n := &node[V]{key: key, val: val, created: ts}
	s.items[key] = n
	s.pushFront(n)

	var evKey string
	var evVal V
	var evicted bool
	if len(s.items) > s.cap {
		evKey, evVal, evicted = s.unlinkOldest()
	}
	s.mu.Unlock()

	// 在锁外调用回调，避免用户回调中访问同一 Cache 导致 ABBA 死锁
	if evicted && s.onEvict != nil {
		s.onEvict(evKey, evVal)
	}
}

func (s *shard[V]) del(key string) {
	s.mu.Lock()
	n, ok := s.items[key]
	if ok {
		s.remove(n)
		delete(s.items, key)
	}
	s.mu.Unlock()
}

// unlinkOldest 从链表和 map 中移除最久未使用的条目。
// 返回被移除的 key/val 和是否有移除。不调用 onEvict（由调用者在锁外调用）。
func (s *shard[V]) unlinkOldest() (string, V, bool) {
	tail := s.head.prev
	if tail == &s.head {
		var zero V
		return "", zero, false
	}
	s.remove(tail)
	delete(s.items, tail.key)
	return tail.key, tail.val, true
}

func (s *shard[V]) len() int {
	s.mu.Lock()
	n := len(s.items)
	s.mu.Unlock()
	return n
}

// rangeAll 按 LRU 从最新到最旧遍历非过期条目，fn 返回 false 时停止。
// 调用方持有外部调用无锁要求，此方法内部加锁。
//
// 内存说明：持锁期间对当前分片做全量快照，快照大小 O(cap_per_shard * sizeof(kv))。
// 超大分片（capPerShard > 10万）建议使用 RangeLimit 分批遍历以控制峰值内存。
func (s *shard[V]) rangeAll(now int64, fn func(key string, val V) bool) bool {
	s.mu.Lock()
	// 收集快照（持锁期间复制指针切片，避免回调中修改链表）
	type kv struct {
		key string
		val V
	}
	// 预分配：len(s.items) 即为实际条目数，一次分配避免 append 多次扩容
	snap := make([]kv, 0, len(s.items))
	for n := s.head.next; n != &s.head; n = n.next {
		if s.ttl > 0 && now-n.created > s.ttl {
			continue // 跳过已过期但尚未惰性清理的条目
		}
		snap = append(snap, kv{n.key, n.val})
	}
	s.mu.Unlock()
	for _, kv := range snap {
		if !fn(kv.key, kv.val) {
			return false
		}
	}
	return true
}

// rangeBatch 分批（每批 batchSize 条目）遍历分片，控制峰值内存。
// 适合超大分片（capPerShard > 10万）的低内存场景。
// 每批重新加锁；两批之间可能看到 Set/Del 的中间状态。
func (s *shard[V]) rangeBatch(now int64, batchSize int, fn func(key string, val V) bool) bool {
	type kv struct {
		key string
		val V
	}
	batch := make([]kv, 0, batchSize)
	lastKey := "" // 断点续扫用
	first := true

	for {
		batch = batch[:0]
		s.mu.Lock()
		skip := first
		for n := s.head.next; n != &s.head; n = n.next {
			if skip {
				if n.key == lastKey {
					skip = false
				}
				continue
			}
			if s.ttl > 0 && now-n.created > s.ttl {
				continue
			}
			batch = append(batch, kv{n.key, n.val})
			if len(batch) == batchSize {
				lastKey = n.key
				break
			}
		}
		s.mu.Unlock()

		if len(batch) == 0 {
			break
		}
		for _, kv := range batch {
			if !fn(kv.key, kv.val) {
				return false
			}
		}
		if len(batch) < batchSize {
			break
		}
		first = false
	}
	return true
}

// purge 清空分片所有条目，不触发 onEvict。
func (s *shard[V]) purge() {
	s.mu.Lock()
	s.items = make(map[string]*node[V], len(s.items))
	s.head.next = &s.head
	s.head.prev = &s.head
	s.mu.Unlock()
}

// ─── Cache ──────────────────────────────────────────────────────────────────

// Cache 分片 LRU 缓存。并发安全。
type Cache[V any] struct {
	shards []shard[V]
	mask   uint64
}

// Option 配置选项。
type Option[V any] func(*cacheOpts[V])

type cacheOpts[V any] struct {
	onEvict EvictFn[V]
	ttl     int64        // 纳秒；0 = 禁用
	nowFn   func() int64 // 注入时钟；nil = time.Now().UnixNano()
}

// WithEvict 设置 LRU 容量驱逐回调。
func WithEvict[V any](fn EvictFn[V]) Option[V] {
	return func(o *cacheOpts[V]) { o.onEvict = fn }
}

// WithTTL 设置全局惰性过期时间。
//
// TTL 在 Get 时检查（惰性），过期条目返回未命中并就地删除，
// 无后台 goroutine，无额外锁。覆写同一 key 时 TTL 从头计算。
func WithTTL[V any](d time.Duration) Option[V] {
	return func(o *cacheOpts[V]) { o.ttl = int64(d) }
}

// WithClock 注入自定义时钟函数，返回当前时间的 Unix 纳秒数。
//
// 默认使用 time.Now().UnixNano()（~5-15ns/op vDSO）。
// 高频 TTL 场景可注入粗粒度时钟（如每 1ms 刚新一次的 atomic.Int64），
// 将得到 ~1ns/op，代价是 TTL 精度降至 1ms 左右。
//
// fn 在 TTL 禁用（无 WithTTL）时不会被调用。
func WithClock[V any](fn func() int64) Option[V] {
	return func(o *cacheOpts[V]) { o.nowFn = fn }
}

// New 创建分片 LRU 缓存。
// numShards: 分片数（自动向上取 2 的幂，最小 4）。
// capPerShard: 每分片容量上限。
func New[V any](numShards, capPerShard int, opts ...Option[V]) *Cache[V] {
	var o cacheOpts[V]
	for _, opt := range opts {
		opt(&o)
	}

	sz := 4
	for sz < numShards {
		sz <<= 1
	}
	c := &Cache[V]{
		shards: make([]shard[V], sz),
		mask:   uint64(sz - 1),
	}
	for i := range c.shards {
		c.shards[i].init(capPerShard, o.ttl, o.onEvict, o.nowFn)
	}
	return c
}

// shardFor 通过 maphash 选取分片（AES-NI 加速，进程内路由专用）。
func (c *Cache[V]) shardFor(key string) *shard[V] {
	return &c.shards[hash.Sum64sMap(key)&c.mask]
}

// Get 查找 key。命中时将条目移到 LRU 头部。TTL 过期时按未命中处理。
func (c *Cache[V]) Get(key string) (V, bool) {
	return c.shardFor(key).get(key)
}

// Set 设置键值对。超出容量时驱逐最久未使用的条目。覆写时重置 TTL。
func (c *Cache[V]) Set(key string, val V) {
	c.shardFor(key).set(key, val)
}

// Del 删除 key。
func (c *Cache[V]) Del(key string) {
	c.shardFor(key).del(key)
}

// Len 返回所有分片的条目总数（含已过期但尚未惰性清理的条目）。
func (c *Cache[V]) Len() int {
	n := 0
	for i := range c.shards {
		n += c.shards[i].len()
	}
	return n
}

// Range 遍历所有有效（未过期）条目，调用 fn(key, val)。
// fn 返回 false 时停止遍历。顺序为 LRU 从最新到最旧，分片间顺序不保证。
//
// fn 在分片锁之外调用，可安全访问同一 Cache 的其他分片（Set/Del 到不同 key 是安全的）；
// 对同一分片的 key 进行 Set/Del 不会死锁，但结果不包含在本次快照中。
//
// 内存说明：每个分片做全量快照，峰值内存 ≈ capPerShard × sizeof(V)。
// 对超大缓存（capPerShard > 10 万），建议使用 RangeLimit 分批遍历。
func (c *Cache[V]) Range(fn func(key string, val V) bool) {
	now := int64(0)
	if c.shards[0].ttl > 0 {
		now = c.shards[0].nowFn()
	}
	for i := range c.shards {
		if !c.shards[i].rangeAll(now, fn) {
			return
		}
	}
}

// RangeLimit 分批遍历所有有效（未过期）条目，每批最多 batchSize 条。
//
// 适用于超大缓存场景：每批重新加锁，两批之间可能看到 Set/Del 的中间状态。
// batchSize <= 0 时退化为 Range（全量一次性快照）。
// fn 返回 false 时停止遍历。
func (c *Cache[V]) RangeLimit(batchSize int, fn func(key string, val V) bool) {
	if batchSize <= 0 {
		c.Range(fn)
		return
	}
	now := int64(0)
	if c.shards[0].ttl > 0 {
		now = c.shards[0].nowFn()
	}
	for i := range c.shards {
		if !c.shards[i].rangeBatch(now, batchSize, fn) {
			return
		}
	}
}

// Purge 清空缓存所有条目，不触发 onEvict 回调。
func (c *Cache[V]) Purge() {
	for i := range c.shards {
		c.shards[i].purge()
	}
}
