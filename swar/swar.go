// Package swar 提供 SWAR（SIMD-Within-A-Register）字节级并行扫描原语。
//
// 利用 64 位整数运算同时处理 8 个字节，无需 SIMD 指令集。
// 适用于 JSON 解析、HTTP header 扫描等逐字节搜索场景。
//
// 典型加速：4-8x vs 单字节循环。
package swar

import (
	"encoding/binary"
	"math/bits"
)

// 基础常量
const (
	Lo = 0x0101010101010101 // 每字节 0x01
	Hi = 0x8080808080808080 // 每字节 0x80
)

// HasZero 判断 64 位字中是否存在零字节。
func HasZero(x uint64) bool {
	return (x-Lo)&^x&Hi != 0
}

// HasByte 判断 64 位字中是否存在值为 b 的字节。
func HasByte(x uint64, b byte) bool {
	return HasZero(x ^ (Lo * uint64(b)))
}

// HasLess 判断 64 位字中是否存在值 < n 的字节。
// 仅对 n ∈ [1, 128] 结果精确；n=0 恒返回 false。
//
// 可靠性保证（重要）：
//   - 无假阴性（no false negatives）：若字中确实存在 < n 的字节，此函数必返回 true。
//   - 极少假阳性（rare false positives）： SWAR 借位传播可能返回 true 而实际无匹配字节。
//
// FindEscape 等高层函数靠评此保证实现正确 fall-through。
func HasLess(x uint64, n byte) bool {
	return (x-Lo*uint64(n)) & ^x & Hi != 0
}

// FirstByte 返回 64 位字中第一个值为 b 的字节索引（0-7），
// 不存在时返回 8。假设小端序。
func FirstByte(x uint64, b byte) int {
	mask := (x ^ (Lo * uint64(b)))
	mask = (mask - Lo) & ^mask & Hi
	if mask == 0 {
		return 8
	}
	return bits.TrailingZeros64(mask) >> 3
}

// ─── 高级扫描函数 ────────────────────────────────────────────────────────────

// FindByte 在 data 中搜索第一个值为 b 的字节索引。
// 未找到返回 -1。
func FindByte(data []byte, b byte) int {
	i := 0
	n := len(data)

	// SWAR 8-byte 批量
	for i+8 <= n {
		x := binary.LittleEndian.Uint64(data[i:])
		idx := FirstByte(x, b)
		if idx < 8 {
			return i + idx
		}
		i += 8
	}

	// 尾部逐字节
	for ; i < n; i++ {
		if data[i] == b {
			return i
		}
	}
	return -1
}

// FindQuote 在 data 中搜索第一个双引号 '"' 的索引。
// 未找到返回 -1。
func FindQuote(data []byte) int {
	return FindByte(data, '"')
}

// FindEscape 在 data 中搜索第一个需要 JSON 转义的字节。
// 即：< 0x20（控制字符）、'"'(0x22)、'\'(0x5C)。
// 未找到返回 -1。
func FindEscape(data []byte) int {
	i := 0
	n := len(data)

	for i+8 <= n {
		x := binary.LittleEndian.Uint64(data[i:])
		// 检查 < 0x20
		if HasLess(x, 0x20) {
			goto slow
		}
		// 检查 " 和 backslash
		if HasByte(x, '"') || HasByte(x, '\\') {
			goto slow
		}
		i += 8
		continue
	slow:
		for j := 0; j < 8 && i+j < n; j++ {
			c := data[i+j]
			if c < 0x20 || c == '"' || c == '\\' {
				return i + j
			}
		}
		i += 8
	}

	for ; i < n; i++ {
		c := data[i]
		if c < 0x20 || c == '"' || c == '\\' {
			return i
		}
	}
	return -1
}
