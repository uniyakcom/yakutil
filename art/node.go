package art

import (
	"math/bits"
	"unsafe"

	"github.com/uniyakcom/yakutil/swar"
)

// ─── 节点类型 ─────────────────────────────────────────────────────────────────
//
// ART 的 4 种内部节点 + 叶节点：
//
//   leaf    — 终端节点，存储完整 key + value
//   node4   — 1-4 子节点，排序数组，线性搜索
//   node16  — 5-16 子节点，排序数组，SWAR 并行匹配
//   node48  — 17-48 子节点，256 字节索引 + 有序 keys + 空闲 bitmap
//   node256 — 49-256 子节点，直接索引 + 存在位图
//
// 路径压缩（pessimistic）：每个内部节点存储完整压缩前缀。

// ─── leaf ─────────────────────────────────────────────────────────────────────

type leaf[V any] struct {
	key string
	val V
}

// ─── inner 公共头 ─────────────────────────────────────────────────────────────

// inner 是所有内部节点的公共嵌入部分。
type inner[V any] struct {
	prefix []byte   // 路径压缩前缀
	leaf   *leaf[V] // 有 key 在此节点终止时非 nil
}

// checkPrefix 检查 key[depth:] 是否与节点前缀匹配。
// 利用 Go 编译器内置 memequal 替代逐字节循环。
func (hdr *inner[V]) checkPrefix(key string, depth int) bool {
	pfx := hdr.prefix
	pfxLen := len(pfx)
	if pfxLen == 0 {
		return true
	}
	end := depth + pfxLen
	if end > len(key) {
		return false
	}
	// 利用编译器将 string == 优化为 memequal 批量比较
	return key[depth:end] == unsafe.String(&pfx[0], pfxLen)
}

// prefixMismatch 返回节点前缀与 key[depth:] 的首个不匹配位置。
// 若完全匹配则返回 len(prefix)。
func (hdr *inner[V]) prefixMismatch(key string, depth int) int {
	pfx := hdr.prefix
	for i := 0; i < len(pfx); i++ {
		if depth+i >= len(key) || pfx[i] != key[depth+i] {
			return i
		}
	}
	return len(pfx)
}

// ─── node4 ────────────────────────────────────────────────────────────────────

type node4[V any] struct {
	inner[V]
	num      uint8
	keys     [4]byte
	children [4]any // each: nil | *leaf[V] | *node4[V] | ...
}

func (nd *node4[V]) findChild(c byte) any { //nolint:unused
	for i := 0; i < int(nd.num); i++ {
		if nd.keys[i] == c {
			return nd.children[i]
		}
	}
	return nil
}

// addChild 在保持 keys 有序的前提下插入子节点。
// 调用方需确保 num < 4。
func (nd *node4[V]) addChild(c byte, child any) {
	// 找到插入位置（保持排序）
	pos := int(nd.num)
	for i := 0; i < int(nd.num); i++ {
		if c < nd.keys[i] {
			pos = i
			break
		}
	}
	// 移动后续元素
	for i := int(nd.num); i > pos; i-- {
		nd.keys[i] = nd.keys[i-1]
		nd.children[i] = nd.children[i-1]
	}
	nd.keys[pos] = c
	nd.children[pos] = child
	nd.num++
}

func (nd *node4[V]) removeChildAt(i int) {
	nd.num--
	for j := i; j < int(nd.num); j++ {
		nd.keys[j] = nd.keys[j+1]
		nd.children[j] = nd.children[j+1]
	}
	nd.keys[nd.num] = 0
	nd.children[nd.num] = nil
}

// grow 将 node4 升级为 node16。
func (nd *node4[V]) grow() *node16[V] {
	n16 := &node16[V]{inner: nd.inner}
	n16.num = nd.num
	copy(n16.keys[:], nd.keys[:nd.num])
	copy(n16.children[:], nd.children[:nd.num])
	return n16
}

// ─── node16 ───────────────────────────────────────────────────────────────────

type node16[V any] struct {
	inner[V]
	num      uint8
	keys     [16]byte
	children [16]any
}

// findChild 使用 SWAR 并行匹配在 16 字节 keys 中查找目标字节。
// 将 keys 视为 2 个 uint64，利用异或+零字节检测一次比较 8 个字节。
func (nd *node16[V]) findChild(c byte) any { //nolint:unused
	broadcast := swar.Lo * uint64(c)
	k0 := *(*uint64)(unsafe.Pointer(&nd.keys[0]))
	xor0 := k0 ^ broadcast
	mask0 := (xor0 - swar.Lo) & ^xor0 & swar.Hi
	if mask0 != 0 {
		idx := bits.TrailingZeros64(mask0) >> 3
		if idx < int(nd.num) {
			return nd.children[idx]
		}
	}
	if nd.num > 8 {
		k1 := *(*uint64)(unsafe.Pointer(&nd.keys[8]))
		xor1 := k1 ^ broadcast
		mask1 := (xor1 - swar.Lo) & ^xor1 & swar.Hi
		if mask1 != 0 {
			idx := 8 + bits.TrailingZeros64(mask1)>>3
			if idx < int(nd.num) {
				return nd.children[idx]
			}
		}
	}
	return nil
}

func (nd *node16[V]) addChild(c byte, child any) {
	pos := int(nd.num)
	for i := 0; i < int(nd.num); i++ {
		if c < nd.keys[i] {
			pos = i
			break
		}
	}
	for i := int(nd.num); i > pos; i-- {
		nd.keys[i] = nd.keys[i-1]
		nd.children[i] = nd.children[i-1]
	}
	nd.keys[pos] = c
	nd.children[pos] = child
	nd.num++
}

func (nd *node16[V]) removeChildAt(i int) {
	nd.num--
	for j := i; j < int(nd.num); j++ {
		nd.keys[j] = nd.keys[j+1]
		nd.children[j] = nd.children[j+1]
	}
	nd.keys[nd.num] = 0
	nd.children[nd.num] = nil
}

// grow 将 node16 升级为 node48。
func (nd *node16[V]) grow() *node48[V] {
	n48 := &node48[V]{inner: nd.inner}
	n48.num = nd.num
	// 槽 0..num-1 已用，num..47 空闲
	n48.free = ((1 << 48) - 1) &^ ((1 << nd.num) - 1)
	for i := 0; i < int(nd.num); i++ {
		n48.index[nd.keys[i]] = uint8(i + 1)
		n48.children[i] = nd.children[i]
		n48.sorted[i] = nd.keys[i] // node16 keys 已有序
	}
	return n48
}

// shrink 将 node16 降级为 node4。
func (nd *node16[V]) shrink() *node4[V] {
	n4 := &node4[V]{inner: nd.inner}
	n4.num = nd.num
	copy(n4.keys[:], nd.keys[:nd.num])
	copy(n4.children[:], nd.children[:nd.num])
	return n4
}

// ─── node48 ───────────────────────────────────────────────────────────────────

// node48 使用 256 字节索引数组将 byte 映射到 0-47 号子节点槽。
// index[byte] = 0 表示无子节点，index[byte] = i+1 表示 children[i]。
// sorted 维护有序字节列表供遍历使用，free 位图记录空闲槽位。
type node48[V any] struct {
	inner[V]
	num      uint8
	free     uint64     // 低 48 位：1=空闲 0=已用
	sorted   [48]byte   // 按升序排列的实际子节点字节
	index    [256]uint8 // byte → slot+1 (0 = absent)
	children [48]any
}

func (nd *node48[V]) findChild(c byte) any { //nolint:unused
	idx := nd.index[c]
	if idx == 0 {
		return nil
	}
	return nd.children[idx-1]
}

func (nd *node48[V]) addChild(c byte, child any) {
	// 空闲 bitmap 找空槽: O(1)
	slot := uint8(bits.TrailingZeros64(nd.free))
	nd.free &^= 1 << slot
	nd.index[c] = slot + 1
	nd.children[slot] = child
	// 维护 sorted 数组有序性
	pos := int(nd.num)
	for i := 0; i < int(nd.num); i++ {
		if c < nd.sorted[i] {
			pos = i
			break
		}
	}
	for i := int(nd.num); i > pos; i-- {
		nd.sorted[i] = nd.sorted[i-1]
	}
	nd.sorted[pos] = c
	nd.num++
}

func (nd *node48[V]) grow() *node256[V] {
	n256 := &node256[V]{inner: nd.inner}
	n256.num = uint16(nd.num)
	for i := 0; i < int(nd.num); i++ {
		b := nd.sorted[i]
		idx := nd.index[b]
		n256.children[b] = nd.children[idx-1]
		n256.present[b/64] |= 1 << (b % 64)
	}
	return n256
}

// shrink 将 node48 降级为 node16。
func (nd *node48[V]) shrink() *node16[V] {
	n16 := &node16[V]{inner: nd.inner}
	n16.num = nd.num
	for i := 0; i < int(nd.num); i++ {
		b := nd.sorted[i]
		n16.keys[i] = b
		n16.children[i] = nd.children[nd.index[b]-1]
	}
	return n16
}

// ─── node256 ──────────────────────────────────────────────────────────────────

type node256[V any] struct {
	inner[V]
	num      uint16
	present  [4]uint64 // 256 位 bitmap：bit i 表示 children[i] 非 nil
	children [256]any
}

func (nd *node256[V]) findChild(c byte) any { //nolint:unused
	return nd.children[c]
}

func (nd *node256[V]) addChild(c byte, child any) {
	nd.children[c] = child
	nd.present[c/64] |= 1 << (c % 64)
	nd.num++
}

// shrink 将 node256 降级为 node48。
func (nd *node256[V]) shrink() *node48[V] {
	n48 := &node48[V]{inner: nd.inner}
	n48.free = (1 << 48) - 1
	n := uint8(0)
	for w := 0; w < 4; w++ {
		word := nd.present[w]
		for word != 0 {
			bit := bits.TrailingZeros64(word)
			b := byte(w*64 + bit)
			slot := n
			n48.index[b] = slot + 1
			n48.children[slot] = nd.children[b]
			n48.sorted[n] = b
			n48.free &^= 1 << slot
			n++
			word &= word - 1
		}
	}
	n48.num = n
	return n48
}

// ─── artNode 接口 ─────────────────────────────────────────────────────────────

// artNode 四种内部节点类型共同实现的接口。
// 通过接口分发将 get/forEach/compactNode 等大型 type-switch 合并为两分支
// （*leaf[V] 与 artNode[V]），降低圈复杂度。
type artNode[V any] interface {
	getInner() *inner[V]
	findChild(c byte) any
	// rangeChildren 按字节升序遍历每个子节点，fn 返回 false 时立即停止。
	rangeChildren(fn func(c byte, ch any) bool) bool
}

// 编译期断言：四种内部节点类型均满足 artNode 接口。
var (
	_ artNode[struct{}] = (*node4[struct{}])(nil)
	_ artNode[struct{}] = (*node16[struct{}])(nil)
	_ artNode[struct{}] = (*node48[struct{}])(nil)
	_ artNode[struct{}] = (*node256[struct{}])(nil)
)

func (nd *node4[V]) getInner() *inner[V]   { return &nd.inner } //nolint:unused
func (nd *node16[V]) getInner() *inner[V]  { return &nd.inner } //nolint:unused
func (nd *node48[V]) getInner() *inner[V]  { return &nd.inner } //nolint:unused
func (nd *node256[V]) getInner() *inner[V] { return &nd.inner } //nolint:unused

func (nd *node4[V]) rangeChildren(fn func(byte, any) bool) bool { //nolint:unused
	for i := 0; i < int(nd.num); i++ {
		if !fn(nd.keys[i], nd.children[i]) {
			return false
		}
	}
	return true
}

func (nd *node16[V]) rangeChildren(fn func(byte, any) bool) bool { //nolint:unused
	for i := 0; i < int(nd.num); i++ {
		if !fn(nd.keys[i], nd.children[i]) {
			return false
		}
	}
	return true
}

func (nd *node48[V]) rangeChildren(fn func(byte, any) bool) bool { //nolint:unused
	for i := 0; i < int(nd.num); i++ {
		b := nd.sorted[i]
		if !fn(b, nd.children[nd.index[b]-1]) {
			return false
		}
	}
	return true
}

func (nd *node256[V]) rangeChildren(fn func(byte, any) bool) bool { //nolint:unused
	for w := 0; w < 4; w++ {
		word := nd.present[w]
		for word != 0 {
			bit := bits.TrailingZeros64(word)
			b := byte(w*64 + bit)
			if !fn(b, nd.children[b]) {
				return false
			}
			word &= word - 1
		}
	}
	return true
}
