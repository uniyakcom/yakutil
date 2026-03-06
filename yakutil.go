// Package yakutil 提供 uniyak 生态共享的高性能基础原语。
//
// 根包包含轻量级工具函数和常量，无外部依赖。
// 子包提供独立的数据结构：
//   - 并发原语：mpsc、spsc、percpu、cow、smap
//   - 内存管理：arena、bufpool、ring、lru
//   - 调度与定时：backoff、wpool、wheel
//   - 编解码辅助：hash、swar、fold、itable
//
// 命名约定：
//   - 根包：单词级缩写（B2S、S2B、Pow2Ceil）
//   - 子包：包名已限定语义，类型名尽短（Ring、Counter、Pool）
//   - 方法名：动词优先，≤6 字符（Add、Load、Push、Pop、Spin）
package yakutil

import (
	"encoding/binary"
	"math/bits"
	"unsafe"
)

// ─── 缓存行 ─────────────────────────────────────────────────────────────────

// CacheLine 现代 x86/ARM 处理器缓存行大小（字节）。
// 用于 padding 避免 false sharing。
const CacheLine = 64

// Pad 缓存行填充类型，嵌入结构体中隔离热字段。
type Pad [CacheLine]byte

// ─── 零拷贝转换 ──────────────────────────────────────────────────────────────

// B2S 零拷贝 []byte → string（不分配内存）。
// 警告：返回的 string 与原始 []byte 共享底层内存，
// 修改 []byte 会导致 string 内容变化。
func B2S(b []byte) string {
	return unsafe.String(unsafe.SliceData(b), len(b))
}

// S2B 零拷贝 string → []byte（不分配内存）。
// 安全嵌入：返回的 []byte 与原始 string 共享底层内存。
//
// ❗ 禁止对返回值进行任何写入操作（包括 append 导致的拷贝明显时除外）。
// string 常量存放于只读数据段（.rodata），写入将触发 SIGSEGV 或静默数据损坏。
// 仅适用于只读场景（如传入库函数、哈希计算）。
func S2B(s string) []byte {
	return unsafe.Slice(unsafe.StringData(s), len(s))
}

// ─── 位运算 ──────────────────────────────────────────────────────────────────

// IsPow2 报告 n 是否为正的 2 的幂。
func IsPow2(n int) bool {
	return n > 0 && n&(n-1) == 0
}

// Pow2Ceil 返回 ≥ n 的最小 2 的幂。n ≤ 0 返回 1。
// n 超过 1<<62 时返回 panic（结果溢出 int）。
func Pow2Ceil(n int) int {
	if n <= 1 {
		return 1
	}
	// 防溢出：阈值根据平台位宽自适应（1<<30 on 32-bit，1<<62 on 64-bit）
	if n > 1<<(bits.UintSize-2) {
		panic("yakutil.Pow2Ceil: overflow")
	}
	n--
	n |= n >> 1
	n |= n >> 2
	n |= n >> 4
	n |= n >> 8
	n |= n >> 16
	n |= n >> (bits.UintSize / 2) // 64 位时 = >>32 填充高位；32 位时 = >>16（幂等，无副作用）
	return n + 1
}

// ─── NoCopy ──────────────────────────────────────────────────────────────────

// NoCopy 嵌入结构体后触发 go vet copylocks 检查。
// 零大小，不占空间。
//
// 用法：
//
//	type MyStruct struct {
//	    yakutil.NoCopy
//	    // ...
//	}
type NoCopy struct{}

func (*NoCopy) Lock()   {}
func (*NoCopy) Unlock() {}

// ─── 字节序 ──────────────────────────────────────────────────────────────────

// Native 运行时 CPU 的原生字节序（BigEndian 或 LittleEndian）。
// 通过 binary.NativeEndian.PutUint16 探测，无需 unsafe.Pointer 类型转换。
var Native binary.ByteOrder

func init() {
	// 写入已知 uint16 值，读取第一个字节判断大小端：
	// 大端：高位字节在低地址 → buf[0] == 0x01
	// 小端：低位字节在低地址 → buf[0] == 0x02
	var buf [2]byte
	binary.NativeEndian.PutUint16(buf[:], 0x0102)
	if buf[0] == 0x01 {
		Native = binary.BigEndian
	} else {
		Native = binary.LittleEndian
	}
}

// ─── 哨兵 Error ──────────────────────────────────────────────────────────────

// ErrStr 零分配字符串错误类型。
// 可声明为编译时常量：const ErrFoo = yakutil.ErrStr("foo")
//
// 注意：将 ErrStr 赋値给 error 接口变量时发生接口装笱（堆逗出 ~16 B）。
// 在热路径频繁返回同一错误时，建议改用 errors.New（指针小、接口装笱不加分配）。
// 常量直接比较（== / errors.Is）不涉及堆展
// 开：if err == yakutil.ErrFoo { ... }（零分配）。
type ErrStr string

func (e ErrStr) Error() string { return string(e) }
