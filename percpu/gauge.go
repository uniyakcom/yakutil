package percpu

import (
	"unsafe"
)

// Gauge 是 per-CPU 双向（可增可减）计量，适合追踪活跃连接数、
// 并发请求数、内存使用量等可变指标。
//
// 与 Counter 相比，Gauge 额外提供 Sub（等价于 Add(-delta)），
// 其余特性（slot 布局、Fibonacci 哈希、Load 近似性）相同。
//
// # 典型使用
//
//	g := percpu.NewGauge(runtime.GOMAXPROCS(0))
//	g.Add(1)  // 新连接
//	g.Sub(1)  // 连接断开
//	metrics.ActiveConns.Set(float64(g.Load()))
//
// # 与 atomic.Int64 的对比
//
// 场景                          | 建议         | 原因
// ------------------------------|-------------|----------------------------------------
// 活跃连接数、请求深度等高频±   | Gauge        | 并行写无竞争，读近似即可
// 严格一致快照（限流判断）       | atomic.Int64 | percpu.Load 为 O(slots) 聚合
// CAS / Swap 语义               | atomic.Int64 | Gauge 不支持
//
// 若 Skew（见 Stats/诊断）> 2.0，建议加大 NewGauge 参数。
type Gauge struct {
	slots []slot // 复用 counter.go 中的 slot 类型（64 B cache line）
	mask  int
	// shift 字段已移除，使用 fibShift 常量（同 Counter 优化）。
}

// NewGauge 创建 per-CPU gauge。procs 通常为 runtime.GOMAXPROCS(0)。
// slot 数量自动向上取 2 的幂，最小 8，最大 256（与 New 规则一致）。
func NewGauge(procs int) *Gauge {
	sz := 1
	for sz < procs {
		sz <<= 1
	}
	if sz < 8 {
		sz = 8
	}
	if sz > maxSlots {
		sz = maxSlots
	}
	return &Gauge{
		slots: make([]slot, sz),
		mask:  sz - 1,
	}
}

// Add 原子加 delta（可正可负）。slot 由 Fibonacci 哈希选取，无跨核竞争。
//
//go:nosplit
func (g *Gauge) Add(delta int64) {
	var x uintptr
	id := int((uintptr(unsafe.Pointer(&x)) * fibMul) >> fibShift) // 使用编译期常量 fibShift
	g.slots[id&g.mask].val.Add(delta)
}

// Sub 原子减 delta（等价于 Add(-delta)）。
//
//go:nosplit
func (g *Gauge) Sub(delta int64) {
	g.Add(-delta)
}

// Load 聚合所有 slot 的值（近似快照）。
//
// 与 percpu.Counter.Load 相同：并发 Add/Sub 期间可能观测到中间态。
// 若需严格一致快照（如限流断路），请使用 atomic.Int64。
func (g *Gauge) Load() int64 {
	var sum int64
	n := g.mask + 1
	for i := 0; i < n; i++ {
		sum += g.slots[i].val.Load()
	}
	return sum
}

// Reset 将所有 slot 归零。
//
// 非原子：并发 Add/Sub 期间调用 Reset 可能丢失增量。
func (g *Gauge) Reset() {
	n := g.mask + 1
	for i := 0; i < n; i++ {
		g.slots[i].val.Store(0)
	}
}

// ─── 诊断 ─────────────────────────────────────────────────────────────────────

// GaugeStats 汇总 per-slot 分布，含负值支持。
type GaugeStats struct {
	Slots int     // slot 总数
	Min   int64   // 最低 slot 值（可为负）
	Max   int64   // 最高 slot 值
	Sum   int64   // 所有 slot 之和（= Load() 的并发快照）
	Mean  float64 // 平均 slot 值
	// Skew = Max/Mean（仅在 Mean > 0 时有意义）；
	// > 2.0 提示热点 slot，建议加大 NewGauge 参数。
	Skew float64
}

// Stats 返回 per-slot 分布的诊断快照。
func (g *Gauge) Stats() GaugeStats {
	n := g.mask + 1
	var minV, maxV, sum int64
	minV = g.slots[0].val.Load()
	maxV = minV
	for i := 0; i < n; i++ {
		v := g.slots[i].val.Load()
		sum += v
		if v < minV {
			minV = v
		}
		if v > maxV {
			maxV = v
		}
	}
	mean := float64(sum) / float64(n)
	var skew float64
	if mean > 0 {
		skew = float64(maxV) / mean
	} else {
		skew = 1.0
	}
	return GaugeStats{
		Slots: n, Min: minV, Max: maxV,
		Sum: sum, Mean: mean, Skew: skew,
	}
}

// ─── 确保接口一致性（编译期检查）────────────────────────────────────────────-

var _ interface {
	Add(int64)
	Sub(int64)
	Load() int64
	Reset()
} = (*Gauge)(nil)
