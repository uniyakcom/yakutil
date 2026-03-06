// Package topn 提供泛型 Top-N 选择工具。
//
// 思路：原地部分选择排序（仅排前 n 位），时间复杂度 O(n × len(src))，
// 适合 n << len(src) 的小规模诊断场景，例如：
//
//   - 热点 KV 分片报告（topN=5，分片数=64）
//   - 慢命令统计（topN=10，命令数=50）
//   - 最高延迟桶（topN=3，桶数=16）
//
// 零依赖：仅使用 Go 标准类型参数约束。
package topn

import (
	"cmp"
	"slices"
)

// TopN 返回 src 中按 less(a, b) 排序（less 返回 true 代表 a "优先于" b）的前 n 项。
//
// 行为说明：
//   - 返回新 slice，不修改 src。
//   - n <= 0 返回 nil。
//   - n >= len(src) 返回全部元素（已排序）。
//   - less 应实现"降序"语义：大值元素传入 a 时返回 true。
//
// 自适应策略：
//   - n ≤ len(src)/4：部分选择排序 O(n × |src|)，适合 n 远小于 src 的场景。
//   - n > len(src)/4：全量全排序 O(|src| log |src|)，避免大 n 场景退化为 O(n²)。
func TopN[T any](src []T, n int, less func(a, b T) bool) []T {
	if n <= 0 || len(src) == 0 {
		return nil
	}
	if n > len(src) {
		n = len(src)
	}
	// 浅拷贝，保护原 slice 不被修改
	dst := make([]T, len(src))
	copy(dst, src)

	// n > |src|/4 时切换全量排序，避免选择排序退化为 O(n²)
	if n > len(dst)/4 {
		slices.SortFunc(dst, func(a, b T) int {
			if less(a, b) {
				return -1
			}
			if less(b, a) {
				return 1
			}
			return 0
		})
		return dst[:n]
	}

	// 部分选择排序：只找前 n 位的最优元素
	for i := 0; i < n; i++ {
		best := i
		for j := i + 1; j < len(dst); j++ {
			if less(dst[j], dst[best]) {
				best = j
			}
		}
		dst[i], dst[best] = dst[best], dst[i]
	}
	return dst[:n]
}

// ByKey 是 TopN 的便捷版本，按 key(elem) 降序取前 n 项（大值优先）。
//
// K 支持所有可比较有序类型（int、int64、float64、string 等），
// 等价于 TopN(src, n, func(a, b T) bool { return key(a) > key(b) })。
//
// 示例：
//
//	type ShardStat struct { Idx int; Ops int64 }
//	top5 := topn.ByKey(stats, 5, func(s ShardStat) int64 { return s.Ops })
func ByKey[T any, K cmp.Ordered](src []T, n int, key func(T) K) []T {
	return TopN(src, n, func(a, b T) bool {
		return key(a) > key(b)
	})
}
