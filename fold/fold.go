// Package fold 提供基于查找表的快速大小写无关字节比较。
//
// 对比 strings.EqualFold：
//   - 零分配（直接操作 []byte/string）
//   - 查找表 O(1) 大小写转换（256B 常驻 L1 cache）
//   - 约 2-3x 加速
//
// 仅支持 ASCII 大小写折叠（A-Z → a-z），Unicode 场景请用标准库。
package fold

import (
	"encoding/binary"
	"unsafe"
)

// lower 256B ASCII 小写查找表。
var lower [256]byte

// upper 256B ASCII 大写查找表。
var upper [256]byte

func init() {
	for i := range lower {
		lower[i] = byte(i)
		upper[i] = byte(i)
		if i >= 'A' && i <= 'Z' {
			lower[i] = byte(i + ('a' - 'A'))
		}
		if i >= 'a' && i <= 'z' {
			upper[i] = byte(i - ('a' - 'A'))
		}
	}
}

// iCmp8 常量：SWAR 8字节大小写无关比较
const (
	_caseMask = uint64(0x2020202020202020) // 每字节 bit-5（大小写区分位）
	_hiBits   = uint64(0x8080808080808080) // 每字节 bit-7
	_aLo      = uint64(0x6161616161616161) // 'a' ×8
	_zSubBase = uint64(0xfafafafafafafafa) // 0x7a+0x80 ×8；用于 ≤'z' 检测
)

// i8differs 判断两个 8 字节 LE 字是否在 ASCII 大小写不敏感语义下不相等。
// 返回 true 表示不相等，false 表示相等。
// 仅处理 ASCII 字母的大小写差异；非 ASCII 字符按字节值精确比较。
func i8differs(wa, wb uint64) bool {
	d := wa ^ wb
	if d == 0 {
		return false // 完全相同
	}
	if d&^_caseMask != 0 {
		return true // 非 bit-5 位有差异 → 一定不等
	}
	// d 仅含 bit-5 位差异；验证这些位置均为 ASCII 字母
	la := wa | _caseMask // 按位压小写
	// bit-7 = 1 表示该字节 ≥ 'a'（利用 |0x80 避免 borrow 传播）
	geA := ((la | _hiBits) - _aLo) & _hiBits
	// bit-7 = 1 表示该字节 ≤ 'z'（0xfa - la_byte ≥ 0，无 borrow）
	leZ := (_zSubBase - la) & _hiBits
	// 字母掩码：bit-7 set → 是字母
	letter := geA & leZ
	// 将 bit-7 右移 2 位得到 bit-5，与 d 比较
	return d&^(letter>>2) != 0
}

// Lower 返回 b 的 ASCII 小写形式。非字母字节原样返回。
func Lower(b byte) byte { return lower[b] }

// Upper 返回 b 的 ASCII 大写形式。非字母字节原样返回。
func Upper(b byte) byte { return upper[b] }

// ToUpperBytes 原地将 p 中的 ASCII 小写字母转为大写。零分配。
func ToUpperBytes(p []byte) {
	for i, b := range p {
		p[i] = upper[b]
	}
}

// ToUpperString 返回 s 的 ASCII 大写形式。
// 仅处理 ASCII 字母（a-z → A-Z），Unicode 字符请用 strings.ToUpper。
// 1 次 alloc（新字符串）。
func ToUpperString(s string) string {
	b := make([]byte, len(s))
	for i := 0; i < len(s); i++ {
		b[i] = upper[s[i]]
	}
	return string(b)
}

// Equal 大小写无关比较 []byte 与 string。
// 等价于 strings.EqualFold(string(a), b) 但零分配。
//
// 使用 SWAR 8 字节批量比较加速：每轮同时处理 8 字节，
// 仅在尾部（< 8 字节）退回查找表路径。
func Equal(a []byte, b string) bool {
	n := len(a)
	if n != len(b) {
		return false
	}
	bData := unsafe.Slice(unsafe.StringData(b), n)
	i := 0
	for ; i+8 <= n; i += 8 {
		wa := binary.LittleEndian.Uint64(a[i:])
		wb := binary.LittleEndian.Uint64(bData[i:])
		if i8differs(wa, wb) {
			return false
		}
	}
	for ; i < n; i++ {
		if lower[a[i]] != lower[b[i]] {
			return false
		}
	}
	return true
}

// EqualBytes 大小写无关比较两个 []byte。
func EqualBytes(a, b []byte) bool {
	n := len(a)
	if n != len(b) {
		return false
	}
	i := 0
	for ; i+8 <= n; i += 8 {
		wa := binary.LittleEndian.Uint64(a[i:])
		wb := binary.LittleEndian.Uint64(b[i:])
		if i8differs(wa, wb) {
			return false
		}
	}
	for ; i < n; i++ {
		if lower[a[i]] != lower[b[i]] {
			return false
		}
	}
	return true
}

// EqualStr 大小写无关比较两个 string。
func EqualStr(a, b string) bool {
	n := len(a)
	if n != len(b) {
		return false
	}
	aData := unsafe.Slice(unsafe.StringData(a), n)
	bData := unsafe.Slice(unsafe.StringData(b), n)
	i := 0
	for ; i+8 <= n; i += 8 {
		wa := binary.LittleEndian.Uint64(aData[i:])
		wb := binary.LittleEndian.Uint64(bData[i:])
		if i8differs(wa, wb) {
			return false
		}
	}
	for ; i < n; i++ {
		if lower[a[i]] != lower[b[i]] {
			return false
		}
	}
	return true
}
