// Package hist 提供等频直方图（equi-height histogram）。
//
// 等频直方图用于估计数据分布，每个桶包含大致相同数量的元素。
// CBO 优化器使用直方图进行选择率估计和 JOIN 代价计算。
//
// 构建方式：先收集样本，再调用 Build 生成直方图。
// 查询方式：EstEq（等值选择率）、EstRange（范围选择率）。
package hist

import (
	"math"
	"slices"
)

const (
	// DefaultBuckets 默认桶数
	DefaultBuckets = 256
)

// Bucket 直方图桶，表示 [Lo, Hi] 区间及该区间内的值分布。
type Bucket struct {
	Lo    float64 // 下界（含）
	Hi    float64 // 上界（含）
	Count int64   // 该桶内元素数量
	NDV   int64   // 该桶内不同值数量（近似）
}

// Hist 等频直方图。非线程安全。
type Hist struct {
	buckets []Bucket
	total   int64   // 总元素数量
	min     float64 // 全局最小值
	max     float64 // 全局最大值
}

// Build 从有序样本数据构建等频直方图。
// samples 必须已排序。nbuckets 为目标桶数（≤0 使用默认 256）。
// 沿途中的 NaN 和 Inf 将被过滤，不影响其他样本的统计结果。
func Build(samples []float64, nbuckets int) *Hist {
	if nbuckets <= 0 {
		nbuckets = DefaultBuckets
	}
	n := len(samples)
	if n == 0 {
		return &Hist{}
	}

	// 过滤 NaN 和 ±Inf：这些元素会导致后续浮点运算产生传染性 NaN
	clean := samples[:0:0]
	for _, v := range samples {
		if !math.IsNaN(v) && !math.IsInf(v, 0) {
			clean = append(clean, v)
		}
	}
	if len(clean) == 0 {
		return &Hist{}
	}
	samples = clean
	n = len(samples)

	// 确保已排序
	if !slices.IsSorted(samples) {
		s := make([]float64, n)
		copy(s, samples)
		slices.Sort(s)
		samples = s
	}

	h := &Hist{
		total: int64(n),
		min:   samples[0],
		max:   samples[n-1],
	}

	// 每桶目标元素数
	perBucket := n / nbuckets
	if perBucket < 1 {
		perBucket = 1
	}

	h.buckets = make([]Bucket, 0, nbuckets)
	i := 0
	for i < n {
		end := i + perBucket
		if end > n {
			end = n
		}
		// 将相同值归入同一桶，避免跨桶切割
		for end < n && samples[end] == samples[end-1] {
			end++
		}
		b := Bucket{
			Lo:    samples[i],
			Hi:    samples[end-1],
			Count: int64(end - i),
		}
		// 计算桶内 NDV
		b.NDV = countDistinct(samples[i:end])
		h.buckets = append(h.buckets, b)
		i = end
	}
	return h
}

// EstEq 估计等值选择率：在总样本中命中值 v 的比例。
// 返回値 [0, 1]。若 v 为 NaN 或 Inf，返回 0。
func (h *Hist) EstEq(v float64) float64 {
	if h.total == 0 || math.IsNaN(v) || math.IsInf(v, 0) {
		return 0
	}
	for i := range h.buckets {
		b := &h.buckets[i]
		if v >= b.Lo && v <= b.Hi {
			if b.NDV <= 0 {
				return 0
			}
			// 假设桶内均匀分布
			sel := float64(b.Count) / float64(b.NDV) / float64(h.total)
			return sel
		}
	}
	return 0 // v 不在直方图范围内
}

// EstRange 估计范围选择率：在总样本中 [lo, hi] 命中的比例。
// 返回値 [0, 1]。若 lo/hi 为 NaN 或 Inf，返回 0。
func (h *Hist) EstRange(lo, hi float64) float64 {
	if h.total == 0 || lo > hi {
		return 0
	}
	if math.IsNaN(lo) || math.IsNaN(hi) || math.IsInf(lo, 0) || math.IsInf(hi, 0) {
		return 0
	}

	var hitCount float64
	for i := range h.buckets {
		b := &h.buckets[i]
		// 桶完全在范围之外
		if b.Hi < lo || b.Lo > hi {
			continue
		}
		// 桶完全在范围之内
		if b.Lo >= lo && b.Hi <= hi {
			hitCount += float64(b.Count)
			continue
		}
		// 部分重叠：按宽度比例估计
		bw := b.Hi - b.Lo
		if bw <= 0 {
			// 桶内只有一个值
			hitCount += float64(b.Count)
			continue
		}
		overlapLo := lo
		if b.Lo > overlapLo {
			overlapLo = b.Lo
		}
		overlapHi := hi
		if b.Hi < overlapHi {
			overlapHi = b.Hi
		}
		ratio := (overlapHi - overlapLo) / bw
		hitCount += float64(b.Count) * ratio
	}
	sel := hitCount / float64(h.total)
	if sel > 1 {
		sel = 1
	}
	return sel
}

// Buckets 返回直方图的所有桶（只读副本）。
func (h *Hist) Buckets() []Bucket {
	out := make([]Bucket, len(h.buckets))
	copy(out, h.buckets)
	return out
}

// Total 返回构建时的总样本数。
func (h *Hist) Total() int64 { return h.total }

// Min 返回全局最小值。
func (h *Hist) Min() float64 { return h.min }

// Max 返回全局最大值。
func (h *Hist) Max() float64 { return h.max }

// Len 返回桶数。
func (h *Hist) Len() int { return len(h.buckets) }

// countDistinct 计算已排序切片中的不同值数量。
func countDistinct(sorted []float64) int64 {
	if len(sorted) == 0 {
		return 0
	}
	ndv := int64(1)
	for i := 1; i < len(sorted); i++ {
		if sorted[i] != sorted[i-1] {
			ndv++
		}
	}
	return ndv
}
