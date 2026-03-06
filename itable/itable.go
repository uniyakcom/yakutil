// Package itable 提供整型 key 的高性能查找表。
//
// 小 key（< threshold）使用固定数组 + atomic.Pointer 实现 O(1) 无锁查找。
// 大 key 回退到 sync.Map。
//
// 内存开销：快速路径数组占用 threshold × 8B（每个 entry 一个指针）。
// 默认 65536 → 512KB。内存敏感场景请使用较小的 threshold。
//
// 适用于 fd、连接 ID 等密集整数 key 查找场景。
package itable

import (
	"sync"
	"sync/atomic"
)

// DefaultThreshold 快速路径数组默认大小。
// 内存开销 = threshold × 8B，默认 65536 → 512KB。
const DefaultThreshold = 65536

// entry 存储一个值的指针。
type entry[V any] struct {
	p atomic.Pointer[V]
}

// Table 整型 key 查找表。并发安全。
type Table[V any] struct {
	fast []entry[V] // 快速路径：small keys
	slow sync.Map   // 回退路径：large keys
	sz   int
}

// New 创建查找表。threshold 为快速路径数组大小（0=默认 65536，512KB）。
// 内存敏感场景可传入较小的值（如 4096 → 32KB），
// 超出范围的 key 自动回退到 sync.Map。
func New[V any](threshold int) *Table[V] {
	if threshold <= 0 {
		threshold = DefaultThreshold
	}
	return &Table[V]{
		fast: make([]entry[V], threshold),
		sz:   threshold,
	}
}

// Get 查找 key 对应的值。
func (t *Table[V]) Get(key int) (*V, bool) {
	if key >= 0 && key < t.sz {
		p := t.fast[key].p.Load()
		if p == nil {
			return nil, false
		}
		return p, true
	}
	v, ok := t.slow.Load(key)
	if !ok {
		return nil, false
	}
	return v.(*V), true
}

// Set 设置 key 对应的值。val 为 nil 时等效于 Del。
func (t *Table[V]) Set(key int, val *V) {
	if key >= 0 && key < t.sz {
		t.fast[key].p.Store(val)
		return
	}
	if val == nil {
		t.slow.Delete(key)
	} else {
		t.slow.Store(key, val)
	}
}

// Del 删除 key。
func (t *Table[V]) Del(key int) {
	if key >= 0 && key < t.sz {
		t.fast[key].p.Store(nil)
		return
	}
	t.slow.Delete(key)
}

// Swap 原子替换并返回旧值。
func (t *Table[V]) Swap(key int, val *V) *V {
	if key >= 0 && key < t.sz {
		return t.fast[key].p.Swap(val)
	}
	if val == nil {
		old, _ := t.slow.LoadAndDelete(key)
		if old == nil {
			return nil
		}
		return old.(*V)
	}
	old, _ := t.slow.Swap(key, val)
	if old == nil {
		return nil
	}
	return old.(*V)
}
