// Package sketch 提供 Count-Min Sketch 频率估计器。
//
// Count-Min Sketch 用于估计流数据中元素的出现频率，
// 使用 O(w×d) 固定内存，支持增量更新和频率查询。
// 估计值 ≥ 真实值（单向误差），误差概率由参数 epsilon/delta 控制。
//
// 用途：yakdb 热点 key 检测、CBO 频率估计。
//
// # 哈希策略
//
// 使用 maphash（AES-NI 加速，进程内随机种子）替代固定种子 FNV-1a：
//   - 防止哈希碰撞攻击
//   - Kirsch-Mitzenmacher 双哈希：单次哈希计算派生 d 个独立列索引，
//     从原来的 d 次 FNV-1a 降至 0 次额外哈希
//
// 注意：maphash 种子进程间不同，状态不可序列化跨进程复现。
package sketch

import (
	"math"

	"github.com/uniyakcom/yakutil/hash"
)

const (
	// defaultWidth 默认宽度（w = ceil(e/epsilon)），epsilon=0.001 → w≈2718
	defaultWidth = 2048

	// defaultDepth 默认深度（d = ceil(ln(1/delta))），delta=0.01 → d≈5
	defaultDepth = 4
)

// CMS Count-Min Sketch 频率估计器。非线程安全。
type CMS struct {
	w     int       // 宽度
	d     int       // 深度（哈希函数个数）
	table [][]int64 // d 行 × w 列
	seeds []uint64  // 每行不同的哈希种子
	total int64     // 累计添加次数
}

// New 创建默认参数的 Count-Min Sketch（w=2048, d=4）。
func New() *CMS {
	return NewSized(defaultWidth, defaultDepth)
}

// NewSized 创建指定宽度和深度的 Count-Min Sketch。
// w 控制精度（越大越准），d 控制置信度（越大误判越低）。
func NewSized(w, d int) *CMS {
	if w < 1 {
		w = defaultWidth
	}
	if d < 1 {
		d = defaultDepth
	}
	c := &CMS{
		w:     w,
		d:     d,
		table: make([][]int64, d),
		seeds: make([]uint64, d),
	}
	for i := 0; i < d; i++ {
		c.table[i] = make([]int64, w)
		// 使用不同种子区分各行哈希
		c.seeds[i] = uint64(i)*0x9E3779B97F4A7C15 + 0xBF58476D1CE4E5B9
	}
	return c
}

// NewFromError 根据误差参数创建 CMS。
// epsilon: 频率估计误差上界（0 < epsilon < 1）
// delta:   超出误差的概率上界（0 < delta < 1）
func NewFromError(epsilon, delta float64) *CMS {
	w := int(math.Ceil(math.E / epsilon))
	d := int(math.Ceil(math.Log(1.0 / delta)))
	return NewSized(w, d)
}

// Add 增加 key 的计数 count 次。count 可以为负数（减少）。
func (c *CMS) Add(key []byte, count int64) {
	h := hash.Sum64Map(key) // maphash：随机种子，防碰撞攻击
	c.total += count
	for i := 0; i < c.d; i++ {
		idx := c.hashIdx(h, i)
		c.table[i][idx] += count
	}
}

// AddStr 增加字符串 key 的计数 1 次（零分配）。
func (c *CMS) AddStr(key string) {
	h := hash.Sum64sMap(key) // maphash 字符串版本
	c.total++
	for i := 0; i < c.d; i++ {
		idx := c.hashIdx(h, i)
		c.table[i][idx]++
	}
}

// Count 查询 key 的估计频率（≥ 真实值）。
func (c *CMS) Count(key []byte) int64 {
	h := hash.Sum64Map(key) // maphash 一致性
	minVal := int64(math.MaxInt64)
	for i := 0; i < c.d; i++ {
		idx := c.hashIdx(h, i)
		if c.table[i][idx] < minVal {
			minVal = c.table[i][idx]
		}
	}
	if minVal < 0 {
		return 0
	}
	return minVal
}

// CountStr 查询字符串 key 的估计频率。
func (c *CMS) CountStr(key string) int64 {
	h := hash.Sum64sMap(key) // maphash 字符串版本
	minVal := int64(math.MaxInt64)
	for i := 0; i < c.d; i++ {
		idx := c.hashIdx(h, i)
		if c.table[i][idx] < minVal {
			minVal = c.table[i][idx]
		}
	}
	if minVal < 0 {
		return 0
	}
	return minVal
}

// Total 返回累计添加次数。
func (c *CMS) Total() int64 { return c.total }

// Reset 清空所有计数器。
func (c *CMS) Reset() {
	for i := range c.table {
		clear(c.table[i])
	}
	c.total = 0
}

// Merge 合并另一个同规格的 CMS（计数器逐项相加）。
// 要求两个 CMS 的 w 和 d 相同，否则 panic。
func (c *CMS) Merge(other *CMS) {
	if c.w != other.w || c.d != other.d {
		panic("sketch: Merge requires same w and d")
	}
	for i := 0; i < c.d; i++ {
		for j := 0; j < c.w; j++ {
			c.table[i][j] += other.table[i][j]
		}
	}
	c.total += other.total
}

// hashIdx 计算 key 在第 i 行的列索引。
//
// 使用 Kirsch-Mitzenmacher 双哈希方案：
//
//	idx_i = (h1 + i × (h2 ^ seed_i)) mod w
//
// h1 = 哈希低 32 位，h2 = 哈希高 32 位，直接由外层传入的单次 maphash 値派生。
// 每行拵有独立种子 seed_i，增强行间独立性。
// 与原实现相比（对每行进行独立 FNV-1a），这种方案将额外哈希调用从 d 次降至 0 次。
func (c *CMS) hashIdx(h uint64, i int) int {
	h1 := h & 0xFFFF_FFFF        // 低 32 位
	h2 := (h >> 32) ^ c.seeds[i] // 高 32 位异或行种子
	return int((h1 + uint64(i+1)*h2) % uint64(c.w))
}
