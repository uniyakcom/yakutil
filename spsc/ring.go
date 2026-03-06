// Package spsc 提供单生产者单消费者（SPSC）无等待环形缓冲区。
//
// 仅使用 atomic Load/Store（x86 上等价于普通 MOV），零 CAS。
// cachedHead / cachedTail 优化消除常态下的跨核缓存行读取。
//
// 吞吐量：单对生产者-消费者 ~2-5ns/op。
package spsc

import (
	"sync/atomic"

	yakutil "github.com/uniyakcom/yakutil"
)

// cacheLine 缓存行大小，引用根包常量确保全库一致性。
const cacheLine = yakutil.CacheLine

// Ring SPSC 无等待环形缓冲区（泛型）。
// 仅允许单生产者调用 Push，单消费者调用 Pop。
type Ring[T any] struct {
	buf  []T
	mask uint64

	// 生产者侧 cache line
	_          [cacheLine]byte
	tail       atomic.Uint64 // 生产者写，消费者读
	cachedHead uint64        // 生产者本地缓存的 head（减少跨核读）
	_          [cacheLine - 16]byte

	// 消费者侧 cache line
	head       atomic.Uint64 // 消费者写，生产者读
	cachedTail uint64        // 消费者本地缓存的 tail（减少跨核读）
	_          [cacheLine - 16]byte
}

// New 创建容量为 cap 的 SPSC Ring。cap 自动向上取 2 的幂。
func New[T any](cap int) *Ring[T] {
	sz := 1
	for sz < cap {
		sz <<= 1
	}
	if sz < 2 {
		sz = 2
	}
	return &Ring[T]{
		buf:  make([]T, sz),
		mask: uint64(sz - 1),
	}
}

// Push 生产者写入一个值。满时返回 false。
// 仅单 goroutine 调用安全。
func (r *Ring[T]) Push(val T) bool {
	t := r.tail.Load()
	// 快速路径：用缓存的 head 判断是否满
	if t-r.cachedHead >= uint64(len(r.buf)) {
		// 刷新 head 缓存
		r.cachedHead = r.head.Load()
		if t-r.cachedHead >= uint64(len(r.buf)) {
			return false // 确实满了
		}
	}
	r.buf[t&r.mask] = val
	r.tail.Store(t + 1) // release store
	return true
}

// Pop 消费者读取一个值。空时返回零值 + false。
// 仅单 goroutine 调用安全。
func (r *Ring[T]) Pop() (T, bool) {
	h := r.head.Load()
	// 快速路径：用缓存的 tail 判断是否空
	if h == r.cachedTail {
		r.cachedTail = r.tail.Load()
		if h == r.cachedTail {
			var zero T
			return zero, false
		}
	}
	idx := h & r.mask
	val := r.buf[idx]
	// 清零 slot，防止含指针的 T 被 buf 持有导致 GC 泄漏
	var zero T
	r.buf[idx] = zero
	r.head.Store(h + 1) // release store
	return val, true
}

// Len 当前队列中的元素数（近似值）。
func (r *Ring[T]) Len() int {
	t := r.tail.Load()
	h := r.head.Load()
	if t >= h {
		return int(t - h)
	}
	return 0
}

// Cap 返回 Ring 容量。
func (r *Ring[T]) Cap() int { return len(r.buf) }
