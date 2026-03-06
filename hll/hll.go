// Package hll 提供 HyperLogLog 基数估计器。
//
// HyperLogLog 用于估计集合的不同元素数量（NDV, Number of Distinct Values），
// 使用固定 O(m) 内存实现 O(N) 时间复杂度的去重计数。
// 标准误差约 1.04 / sqrt(m)。
//
// 用途：yakdb CBO 优化器统计列 NDV。
//
// # 哈希策略
//
// 使用 maphash（AES-NI 加速，进程内随机种子）替代固定种子 FNV-1a：
//   - 防止哈希碰撞攻击
//   - 中长 key（≥32B）均有3–8× 加速
//
// 注意：maphash 种子进程间不同，状态不可序列化跨进程复现。
package hll

import (
	"math"
	"math/bits"

	"github.com/uniyakcom/yakutil/hash"
)

const (
	// precision 精度位数（p），决定桶数 m = 2^p
	precision = 14 // m = 16384，标准误差 ≈ 0.81%

	// m 桶数
	m = 1 << precision // 16384

	// alpha_m 修正系数
	alphaM = 0.7213 / (1.0 + 1.079/float64(m))
)

// Sketch HyperLogLog 估计器。非线程安全，调用方需自行同步。
type Sketch struct {
	regs [m]uint8 // 每个桶存储最大前导零 +1
}

// New 创建新的 HyperLogLog 估计器。
func New() *Sketch {
	return &Sketch{}
}

// Add 向估计器中添加一个字节序列。
func (s *Sketch) Add(data []byte) {
	h := hash.Sum64Map(data) // maphash：随机种子，防碰撞攻击
	s.addHash(h)
}

// AddStr 向估计器中添加一个字符串（零分配）。
func (s *Sketch) AddStr(str string) {
	h := hash.Sum64sMap(str) // maphash：随机种子，字符串版本
	s.addHash(h)
}

// addHash 内部方法：使用哈希值更新桶。
func (s *Sketch) addHash(h uint64) {
	// 低 p 位作为桶索引（FNV-1a 低位散布更均匀）
	idx := h & (m - 1)
	// 剩余高位右移后，计算最低置位位的位置（几何分布）
	w := h >> precision
	var rho uint8
	if w == 0 {
		// 全零时取最大可能值
		rho = uint8(64-precision) + 1
	} else {
		rho = uint8(bits.TrailingZeros64(w)) + 1
	}
	if rho > s.regs[idx] {
		s.regs[idx] = rho
	}
}

// Count 返回估计的不同元素数量。
func (s *Sketch) Count() uint64 {
	// 调和平均数
	var sum float64
	zeros := 0
	for i := range s.regs {
		if s.regs[i] == 0 {
			zeros++
		}
		sum += 1.0 / float64(uint64(1)<<s.regs[i])
	}
	est := alphaM * float64(m) * float64(m) / sum

	// 小范围修正（线性计数）
	if est <= 2.5*float64(m) && zeros > 0 {
		est = float64(m) * math.Log(float64(m)/float64(zeros))
	}

	return uint64(est + 0.5)
}

// Merge 合并另一个 HyperLogLog 估计器（取每个桶的最大值）。
// 并集操作：合并后 Count() 返回两个集合并集的基数估计。
func (s *Sketch) Merge(other *Sketch) {
	for i := range s.regs {
		if other.regs[i] > s.regs[i] {
			s.regs[i] = other.regs[i]
		}
	}
}

// Reset 清空估计器。
func (s *Sketch) Reset() {
	clear(s.regs[:])
}
