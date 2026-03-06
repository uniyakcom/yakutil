// Package bufpool 提供分级自适应字节切片池。
//
// 20 级 2^n sync.Pool（64B → 32MB），Get 时向上取整到最近一级，
// Put 时按 cap 归还对应级。避免频繁 make/GC 回收。
//
// # 大小限制
//
//   - Get(size) 当 size > 32MB（池最大级别）时，直接 make([]byte, size) 返回，
//     不使用池路径，避免 len > cap 导致的 panic，但也不享有池复用收益。
//   - Put(b) 对 cap > 32MB 的切片静默丢弃，不归入池。
//
// 用法：
//
//	buf := bufpool.Get(1024)   // 获取 ≥1024B 的 []byte
//	defer bufpool.Put(buf)     // 用完归还
package bufpool

import (
	"math/bits"
	"sync"
)

const (
	minBits = 6  // 2^6  = 64B 最小
	maxBits = 25 // 2^25 = 32MB 最大
	levels  = maxBits - minBits + 1
	maxSize = 1 << maxBits // 32MB，池内最大尺寸
)

// Pool 分级字节切片池。零值可用。
type Pool struct {
	tiers [levels]sync.Pool
}

// 全局默认 Pool
var global Pool

// Get 从全局池获取 ≥ size 字节的切片（len=size）。
func Get(size int) []byte { return global.Get(size) }

// Put 将切片归还全局池。
func Put(b []byte) { global.Put(b) }

// Get 获取 ≥ size 字节的切片（len=size, cap 向上取 2^n）。
//
// 当 size > 32MB 时，绕过池直接分配，不保证 cap 是 2^n 倍数，
// 也不应调用 Put 归还（Put 会静默丢弃超大切片）。
func (p *Pool) Get(size int) []byte {
	if size <= 0 {
		size = 1
	}
	if size > maxSize {
		// 超过池最大尺寸：直接分配，避免 make([]byte,size,32MB) 时 len>cap panic
		return make([]byte, size)
	}
	idx := tier(size)
	if v := p.tiers[idx].Get(); v != nil {
		b := v.([]byte)
		return b[:size]
	}
	return make([]byte, size, 1<<(idx+minBits))
}

// Put 归还切片到对应级池。cap 不是 2^n、过小或过大的切片被丢弃。
func (p *Pool) Put(b []byte) {
	c := cap(b)
	if c < (1 << minBits) {
		return
	}
	// 拒绝非 2^n cap 的切片，防止 Get 时 b[:size] panic
	if c&(c-1) != 0 {
		return
	}
	idx := tier(c)
	if idx >= levels {
		return
	}
	p.tiers[idx].Put(b[:0:c]) //nolint:staticcheck // SA6002: 池化 slice header 是预期行为
}

// tier 计算 size 对应的池级别索引。
func tier(n int) int {
	if n <= (1 << minBits) {
		return 0
	}
	b := bits.Len(uint(n - 1))
	i := b - minBits
	if i < 0 {
		return 0
	}
	if i >= levels {
		return levels - 1
	}
	return i
}
