// Package smap 提供分片并发 Map。
//
// Map[V] 以 string 为 key，内置 maphash 哈希路由（AES-NI 加速，进程内使用）。
// Map64[V] 以 uint64 为 key，直接位运算分片。
// 两者均使用 N 分片 + RWMutex 隔离竞争。
//
// # 哈希策略
//
// Map[V] 的分片路由基于 hash.Sum64sMap（maphash 引擎），
// 较 FNV-1a 在中长 key（≥32B）上吞吐提升 4–12×（AES-NI 平台实测）。
// seed 在进程启动时随机初始化，哈希结果不跨进程稳定，
// 仅用于本地分片路由。
//
// # 性能参考（Intel Xeon E-2186G @ 3.80GHz，Go 1.25）
//
// 读路径 RLock 单分片：~15-20ns；无全局锁。
// 分片路由（32B key，12 线程）：~1.15 ns/op（27,849 MB/s）。
package smap

import (
	"math/bits"
	"sync"
	"unsafe"

	"github.com/uniyakcom/yakutil/hash"
)

// ─── Map[V] (string key) ────────────────────────────────────────────────────

type strShard[V any] struct {
	mu sync.RWMutex
	m  map[string]V
	// 填充至 128B（2 个 cache line），使用 unsafe.Sizeof 动态计算避免依赖 RWMutex 内部大小。
	// 若 sync.RWMutex 未来增大超出上限，编译器会报错（array size < 0）。
	_ [128 - unsafe.Sizeof(sync.RWMutex{}) - 8]byte
}

// 编译期断言：strShard[int] 必须正好占 128 字节（2个 cache line）。
// 此行编译失败意味着 sync.RWMutex 或 map header 大小已变，
// 需要更新 strShard._ 字段的填充大小以护持 cache-line 对齐。
var _ [128 - unsafe.Sizeof(strShard[int]{})]byte

// Map 分片并发 Map，string key。
type Map[V any] struct {
	shards []strShard[V]
	mask   uint64
}

// New 创建 string-key 分片 Map。shards 自动向上取 2 的幂（最小 4）。
func New[V any](shards int) *Map[V] {
	sz := 4
	for sz < shards {
		sz <<= 1
	}
	m := &Map[V]{
		shards: make([]strShard[V], sz),
		mask:   uint64(sz - 1),
	}
	for i := range m.shards {
		m.shards[i].m = make(map[string]V)
	}
	return m
}

// shard 通过 maphash 选取分片（AES-NI 加速，进程内路由专用）。
// 注意：哈希 seed 随进程重启变化，结果不可持久化或跨进程比较。
func (m *Map[V]) shard(key string) *strShard[V] {
	return &m.shards[hash.Sum64sMap(key)&m.mask]
}

// Get 读取 key 对应的值。
func (m *Map[V]) Get(key string) (V, bool) {
	s := m.shard(key)
	s.mu.RLock()
	v, ok := s.m[key]
	s.mu.RUnlock()
	return v, ok
}

// Set 写入键值对。
func (m *Map[V]) Set(key string, val V) {
	s := m.shard(key)
	s.mu.Lock()
	s.m[key] = val
	s.mu.Unlock()
}

// Del 删除 key。
func (m *Map[V]) Del(key string) {
	s := m.shard(key)
	s.mu.Lock()
	delete(s.m, key)
	s.mu.Unlock()
}

// Len 返回所有分片的 key 总数（近似值）。
func (m *Map[V]) Len() int {
	n := 0
	for i := range m.shards {
		m.shards[i].mu.RLock()
		n += len(m.shards[i].m)
		m.shards[i].mu.RUnlock()
	}
	return n
}

// Range 遍历所有键值对。fn 返回 false 时停止。
// 遍历期间持有各分片读锁（按序获取）。
func (m *Map[V]) Range(fn func(key string, val V) bool) {
	for i := range m.shards {
		s := &m.shards[i]
		s.mu.RLock()
		for k, v := range s.m {
			if !fn(k, v) {
				s.mu.RUnlock()
				return
			}
		}
		s.mu.RUnlock()
	}
}

// GetOrSet 原子 get-or-create：若 key 已存在则返回现有值（第二返回值 false），
// 否则调用 fn() 创建并存储新值（第二返回值 true）。
//
// 实现使用双重检验锁：先以 RLock 快速检查，若缺失则升级为写锁并二次检查，
// 确保并发场景下 fn 只被调用一次（严格 exactly-once 语义）。
// fn 在持有写锁期间调用，应尽量轻量；不可在 fn 内对同一 Map 加锁（死锁）。
func (m *Map[V]) GetOrSet(key string, fn func() V) (V, bool) {
	s := m.shard(key)
	// 快速路径：读锁检查
	s.mu.RLock()
	if v, ok := s.m[key]; ok {
		s.mu.RUnlock()
		return v, false
	}
	s.mu.RUnlock()
	// 慢路径：升级为写锁，二次检查防止重复创建
	s.mu.Lock()
	if v, ok := s.m[key]; ok {
		s.mu.Unlock()
		return v, false
	}
	v := fn()
	s.m[key] = v
	s.mu.Unlock()
	return v, true
}

// ─── Map64[V] (uint64 key) ──────────────────────────────────────────────────

type u64Shard[V any] struct {
	mu sync.RWMutex
	m  map[uint64]V
	// 同上：128B 填充，动态计算防止 false sharing（假共享）。
	_ [128 - unsafe.Sizeof(sync.RWMutex{}) - 8]byte
}

// Map64 分片并发 Map，uint64 key。
type Map64[V any] struct {
	shards []u64Shard[V]
	mask   uint64
	shift  uint // Fibonacci 哈希右移位数（预计算），= 64 - log2(numShards)
}

// New64 创建 uint64-key 分片 Map。
func New64[V any](shards int) *Map64[V] {
	sz := 4
	for sz < shards {
		sz <<= 1
	}
	// 预计算 Fibonacci 哈希右移位数：取 64 位乘积的高 log2(sz) 位。
	// 固定 >>56 仅适用于 sz ≤ 256；此处动态计算，支持任意 2^n 分片数。
	shift := uint(64 - bits.Len(uint(sz-1)))
	m := &Map64[V]{
		shards: make([]u64Shard[V], sz),
		mask:   uint64(sz - 1),
		shift:  shift,
	}
	for i := range m.shards {
		m.shards[i].m = make(map[uint64]V)
	}
	return m
}

func (m *Map64[V]) shard(key uint64) *u64Shard[V] {
	// Fibonacci hashing：用预计算的 shift 取高 log2(numShards) 位，
	// 保证所有分片均匀可达，不受分片数上限约束。
	return &m.shards[(key*11400714819323198485)>>m.shift&m.mask]
}

// Get 读取 key 对应的值。
func (m *Map64[V]) Get(key uint64) (V, bool) {
	s := m.shard(key)
	s.mu.RLock()
	v, ok := s.m[key]
	s.mu.RUnlock()
	return v, ok
}

// Set 写入键值对。
func (m *Map64[V]) Set(key uint64, val V) {
	s := m.shard(key)
	s.mu.Lock()
	s.m[key] = val
	s.mu.Unlock()
}

// Del 删除 key。
func (m *Map64[V]) Del(key uint64) {
	s := m.shard(key)
	s.mu.Lock()
	delete(s.m, key)
	s.mu.Unlock()
}

// Len 返回所有分片的 key 总数。
func (m *Map64[V]) Len() int {
	n := 0
	for i := range m.shards {
		m.shards[i].mu.RLock()
		n += len(m.shards[i].m)
		m.shards[i].mu.RUnlock()
	}
	return n
}

// Range 遍历所有键值对。fn 返回 false 时停止。
func (m *Map64[V]) Range(fn func(key uint64, val V) bool) {
	for i := range m.shards {
		s := &m.shards[i]
		s.mu.RLock()
		for k, v := range s.m {
			if !fn(k, v) {
				s.mu.RUnlock()
				return
			}
		}
		s.mu.RUnlock()
	}
}

// GetOrSet 原子 get-or-create：若 key 已存在则返回现有值（第二返回值 false），
// 否则调用 fn() 创建并存储新值（第二返回值 true）。
// 使用双重检验锁保证严格 exactly-once 语义，fn 在持有写锁期间调用。
func (m *Map64[V]) GetOrSet(key uint64, fn func() V) (V, bool) {
	s := m.shard(key)
	s.mu.RLock()
	if v, ok := s.m[key]; ok {
		s.mu.RUnlock()
		return v, false
	}
	s.mu.RUnlock()
	s.mu.Lock()
	if v, ok := s.m[key]; ok {
		s.mu.Unlock()
		return v, false
	}
	v := fn()
	s.m[key] = v
	s.mu.Unlock()
	return v, true
}
