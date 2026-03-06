// Package arena 提供 CAS 无锁 Bump 分配器。
//
// Arena 从 64KB chunk 中 bump 分配字节切片，CAS 保证并发安全。
// chunk 耗尽时自动切换新 chunk，旧 chunk 由 GC 回收。
//
// 适用于：WAL 编码缓冲、Document JSON 缓冲等短生命周期批量分配场景。
// 相比 Go heap malloc（~25ns），bump alloc 常态 <5ns（仅 CAS + 加法）。
//
// # Snap / Restore 并发安全说明
//
// Alloc 是并发安全的（CAS 无锁）。
// Snap 也是并发安全的（只读原子操作）。
// Restore **不是** 并发安全的：需要修改 chunk 内部偏移量和当前 chunk 指针两个独立原子字段，
// 两步之间存在窗口，与并发 Alloc 交叉会导致 slice 内存重叠。
// 调用方须保证：Restore 调用期间不存在并发 Alloc（使用外部互斥或单协程使用）。
package arena

import (
	"sync/atomic"
)

// DefaultChunk 默认 chunk 大小
const DefaultChunk = 64 * 1024 // 64KB

// MaxChunk chunk 大小上限（防止误传超大值，如 1<<30 导致单次 make 1GB）
const MaxChunk = 64 * 1024 * 1024 // 64MB

// ArenaStats Arena 运行时统计快照。
type ArenaStats struct {
	Chunks     int64 // 累计分配的 chunk 数（含初始 chunk）
	AlignWaste int64 // 累计 8 字节对齐产生的浪费字节数
	TailWaste  int64 // 当前 chunk 末尾未分配的剩余字节数
	ChunkSize  int   // 每个 chunk 的字节数
}

// chunk 一块连续内存
type chunk struct {
	buf []byte
	off atomic.Uint64
}

// Arena CAS 无锁 Bump 分配器。并发安全。
type Arena struct {
	cur        atomic.Pointer[chunk]
	csz        int
	chunks     atomic.Int64 // 累计分配的 chunk 数
	alignWaste atomic.Int64 // 累计 8 字节对齐浪费的字节数
}

// New 创建 Arena。chunkSize ≤ 0 使用默认 64KB；超过 MaxChunk 截断至 MaxChunk。
func New(chunkSize int) *Arena {
	if chunkSize <= 0 {
		chunkSize = DefaultChunk
	}
	if chunkSize > MaxChunk {
		chunkSize = MaxChunk
	}
	a := &Arena{csz: chunkSize}
	c := &chunk{buf: make([]byte, chunkSize)}
	a.cur.Store(c)
	a.chunks.Store(1) // 初始 chunk
	return a
}

// Alloc 分配 n 字节（8 字节对齐）。并发安全。
// 返回 len=n 的切片，底层按 8 字节对齐分配。
// n <= 0 返回 nil；n > chunkSize 时直接 make（不走 arena 路径）。
func (a *Arena) Alloc(n int) []byte {
	if n <= 0 {
		return nil
	}
	if n > a.csz {
		return make([]byte, n)
	}
	// n ≤ a.csz，最大 64KB，(n+7) 不会溢出
	aligned := (n + 7) &^ 7 // 8-byte align
	waste := aligned - n
	for {
		c := a.cur.Load()
		off := c.off.Load()
		end := off + uint64(aligned)
		if end <= uint64(len(c.buf)) {
			if c.off.CompareAndSwap(off, end) {
				if waste > 0 {
					a.alignWaste.Add(int64(waste))
				}
				return c.buf[off : off+uint64(n) : end]
			}
			continue
		}
		// chunk 耗尽——先检查是否已被其他 goroutine 切换，避免无用 make
		if a.cur.Load() != c {
			continue
		}
		nc := &chunk{buf: make([]byte, a.csz)}
		if a.cur.CompareAndSwap(c, nc) {
			a.chunks.Add(1)
			// 旧 chunk 由 GC 在所有引用释放后回收
			continue
		}
		// CAS 失败，其他 goroutine 已切换，重试
	}
}

// Reset 重置 Arena（切换到新空 chunk）。
// 旧数据引用仍有效，由 GC 回收。
func (a *Arena) Reset() {
	nc := &chunk{buf: make([]byte, a.csz)}
	a.cur.Store(nc)
	a.chunks.Add(1)
}

// Snap 返回当前 Arena 分配位置的快照标记。
// 可与 Restore 配合实现 SavePoint 语义：
//
//	sp := a.Snap()
//	a.Alloc(...)  // 分配一些数据
//	a.Restore(sp) // 回滚到 sp 处，后续分配的数据被逻辑丢弃
//
// 注意：Snap 仅记录当前 chunk 的偏移量。如果在 Snap 到 Restore
// 之间发生了 chunk 切换（分配量超过当前 chunk 剩余），Restore
// 将切换回 snap 时的 chunk（中间 chunk 交由 GC 回收）。
// 并发安全（只读原子操作）。
func (a *Arena) Snap() Snapshot {
	c := a.cur.Load()
	off := c.off.Load()
	return Snapshot{chk: c, off: off}
}

// Restore 还原 Arena 到 Snap 返回的快照位置。
// 快照之后分配的内存在逻辑上失效（底层 chunk 由 GC 回收或被覆盖）。
//
// ⚠️  非并发安全：Restore 修改两个独立原子字段（chunk 偏移量 + 当前 chunk 指针），
// 两步之间存在窗口，与并发 Alloc 交叉会导致 slice 内存重叠。
// 调用方必须保证：Restore 期间不存在任何并发 Alloc 调用。
// 典型安全用法：单 goroutine 使用，或调用前加互斥锁。
func (a *Arena) Restore(sp Snapshot) {
	// 恢复到快照时的 chunk 和偏移量
	sp.chk.off.Store(sp.off)
	a.cur.Store(sp.chk)
}

// Snapshot Arena 分配位置快照，与 Snap/Restore 配合使用。
type Snapshot struct {
	chk *chunk
	off uint64
}

// Stats 返回 Arena 的运行时统计快照。并发安全（近似快照）。
//
// ArenaStats.TailWaste 为当前 chunk 末尾未分配字节数（取决于 Alloc 调用时机）。
func (a *Arena) Stats() ArenaStats {
	c := a.cur.Load()
	used := int64(c.off.Load())
	tailWaste := int64(a.csz) - used
	if tailWaste < 0 {
		tailWaste = 0
	}
	return ArenaStats{
		Chunks:     a.chunks.Load(),
		AlignWaste: a.alignWaste.Load(),
		TailWaste:  tailWaste,
		ChunkSize:  a.csz,
	}
}
