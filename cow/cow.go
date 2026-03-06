// Package cow 提供泛型 Copy-on-Write 原子值。
//
// 读路径：单次 atomic.Pointer Load（~1ns，零锁）。
// 写路径：构造新值 → atomic Store（单写者）或 CompareAndSwap（多写者）。
//
// 适用于读多写少场景（如订阅快照、集合映射、全局配置）。
package cow

import "sync/atomic"

// Value 泛型 Copy-on-Write 原子值。
//
// 零值的 Load 返回 T 的零值。
type Value[T any] struct {
	p atomic.Pointer[T]
}

// New 创建带初始值的 COW Value。
func New[T any](initial T) *Value[T] {
	v := &Value[T]{}
	v.p.Store(&initial)
	return v
}

// Load 原子读取当前快照。无锁，一次 Load 即完成。
func (v *Value[T]) Load() T {
	p := v.p.Load()
	if p == nil {
		var zero T
		return zero
	}
	return *p
}

// Ptr 返回当前快照的指针（可为 nil）。
func (v *Value[T]) Ptr() *T {
	return v.p.Load()
}

// Store 原子替换值。
func (v *Value[T]) Store(val T) {
	v.p.Store(&val)
}

// Update 读取当前值 → 应用变换函数 → 原子替换。
//
// ⚠️ 仅限单写者：Load 和 Store 之间存在竞态窗口。
// 多写者场景下, 并发调用 Update 会导致更新丢失。
// 必须使用 UpdateCAS（无锁多写者安全）或外部 Mutex 保护。
func (v *Value[T]) Update(fn func(old T) T) {
	old := v.Load()
	nv := fn(old)
	v.p.Store(&nv)
}

// Swap 原子替换值并返回旧值。并发安全。
func (v *Value[T]) Swap(val T) T {
	old := v.p.Swap(&val)
	if old == nil {
		var zero T
		return zero
	}
	return *old
}

// UpdateCAS 基于 CAS 循环的无锁读-改-写。多写者安全。
//
// 若指针在 fn 执行期间被其他写者替换，自动重试。
// fn 可能被多次调用，须为纯函数（无副作用）。
func (v *Value[T]) UpdateCAS(fn func(old T) T) {
	for {
		op := v.p.Load()
		var old T
		if op != nil {
			old = *op
		}
		nv := fn(old)
		if v.p.CompareAndSwap(op, &nv) {
			return
		}
	}
}
