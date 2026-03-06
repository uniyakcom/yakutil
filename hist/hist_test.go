package hist

import (
	"math"
	"testing"
)

// TestBuild_Empty 验证空样本构建
func TestBuild_Empty(t *testing.T) {
	h := Build(nil, 0)
	if h.Len() != 0 {
		t.Fatalf("空样本 Len() = %d, 期望 0", h.Len())
	}
	if h.Total() != 0 {
		t.Fatalf("空样本 Total() = %d, 期望 0", h.Total())
	}
}

// TestBuild_Uniform 验证均匀分布样本
func TestBuild_Uniform(t *testing.T) {
	samples := make([]float64, 1000)
	for i := range samples {
		samples[i] = float64(i)
	}
	h := Build(samples, 10)

	if h.Total() != 1000 {
		t.Fatalf("Total() = %d, 期望 1000", h.Total())
	}
	if h.Min() != 0 {
		t.Fatalf("Min() = %f, 期望 0", h.Min())
	}
	if h.Max() != 999 {
		t.Fatalf("Max() = %f, 期望 999", h.Max())
	}
	if h.Len() > 10 {
		t.Fatalf("Len() = %d, 期望 <= 10", h.Len())
	}

	var sum int64
	for _, b := range h.Buckets() {
		sum += b.Count
	}
	if sum != 1000 {
		t.Fatalf("桶元素总和 = %d, 期望 1000", sum)
	}
}

// TestBuild_Unsorted 验证未排序样本自动排序
func TestBuild_Unsorted(t *testing.T) {
	samples := []float64{5, 3, 1, 4, 2}
	h := Build(samples, 2)
	if h.Min() != 1 {
		t.Fatalf("未排序样本 Min() = %f, 期望 1", h.Min())
	}
	if h.Max() != 5 {
		t.Fatalf("未排序样本 Max() = %f, 期望 5", h.Max())
	}
}

// TestEstEq_Uniform 验证等值选择率
func TestEstEq_Uniform(t *testing.T) {
	samples := make([]float64, 1000)
	for i := range samples {
		samples[i] = float64(i)
	}
	h := Build(samples, 10)

	sel := h.EstEq(500)
	if sel <= 0 {
		t.Fatalf("EstEq(500) = %f, 期望 > 0", sel)
	}
	sel = h.EstEq(2000)
	if sel != 0 {
		t.Fatalf("EstEq(2000) = %f, 期望 0", sel)
	}
}

// TestEstRange_Full 验证全范围选择率
func TestEstRange_Full(t *testing.T) {
	samples := make([]float64, 1000)
	for i := range samples {
		samples[i] = float64(i)
	}
	h := Build(samples, 10)

	sel := h.EstRange(0, 999)
	if math.Abs(sel-1.0) > 0.01 {
		t.Fatalf("EstRange(0,999) = %f, 期望 ~1.0", sel)
	}
	sel = h.EstRange(1000, 2000)
	if sel != 0 {
		t.Fatalf("EstRange(1000,2000) = %f, 期望 0", sel)
	}
	sel = h.EstRange(999, 0)
	if sel != 0 {
		t.Fatalf("EstRange(999,0) = %f, 期望 0", sel)
	}
}

// TestEstRange_Partial 验证部分范围
func TestEstRange_Partial(t *testing.T) {
	samples := make([]float64, 1000)
	for i := range samples {
		samples[i] = float64(i)
	}
	h := Build(samples, 100)

	sel := h.EstRange(0, 499)
	if math.Abs(sel-0.5) > 0.1 {
		t.Fatalf("EstRange(0,499) = %f, 期望 ~0.5", sel)
	}
}

// TestEstEq_Empty 验证空直方图的选择率
func TestEstEq_Empty(t *testing.T) {
	h := Build(nil, 0)
	if sel := h.EstEq(42); sel != 0 {
		t.Fatalf("空直方图 EstEq(42) = %f, 期望 0", sel)
	}
}

// BenchmarkBuild 基准测试构建性能
func BenchmarkBuild(b *testing.B) {
	samples := make([]float64, 100000)
	for i := range samples {
		samples[i] = float64(i)
	}
	b.ResetTimer()
	for b.Loop() {
		Build(samples, 256)
	}
}

// ─── NaN/Inf 防御 ────────────────────────────────────────────────────

// TestBuild_NaN 验证 Build 过滤 NaN 样本
func TestBuild_NaN(t *testing.T) {
	samples := []float64{1, 2, math.NaN(), 3, 4}
	h := Build(samples, 10)
	// NaN 被过滤，剩下 4 个样本
	if h.Total() != 4 {
		t.Fatalf("Build 应过滤 NaN，剧 Total()=%d", h.Total())
	}
	if math.IsNaN(h.Min()) || math.IsNaN(h.Max()) {
		t.Fatal("Build NaN 应被过滤，但 Min/Max 为 NaN")
	}
}

// TestBuild_Inf 验证 Build 过滤 ±Inf
func TestBuild_Inf(t *testing.T) {
	samples := []float64{1, 2, math.Inf(1), math.Inf(-1), 3}
	h := Build(samples, 10)
	if h.Total() != 3 {
		t.Fatalf("过滤 ±Inf 后 Total()=%d，期望 3", h.Total())
	}
}

// TestBuild_AllNaN 验证全 NaN 样本返回空直方图
func TestBuild_AllNaN(t *testing.T) {
	samples := []float64{math.NaN(), math.NaN()}
	h := Build(samples, 10)
	if h.Len() != 0 || h.Total() != 0 {
		t.Fatal("全 NaN 应返回空直方图")
	}
}

// TestEstEq_NaN 验证 EstEq 遇 NaN 返回 0
func TestEstEq_NaN(t *testing.T) {
	h := Build([]float64{1, 2, 3}, 3)
	if sel := h.EstEq(math.NaN()); sel != 0 {
		t.Fatalf("EstEq(NaN) = %f, 期望 0", sel)
	}
	if sel := h.EstEq(math.Inf(1)); sel != 0 {
		t.Fatalf("EstEq(+Inf) = %f, 期望 0", sel)
	}
}

// TestEstRange_NaN 验证 EstRange 遇 NaN/Inf 返回 0
func TestEstRange_NaN(t *testing.T) {
	h := Build([]float64{1, 2, 3}, 3)
	if sel := h.EstRange(math.NaN(), 2); sel != 0 {
		t.Fatalf("EstRange(NaN,2) = %f, 期望 0", sel)
	}
	if sel := h.EstRange(1, math.Inf(1)); sel != 0 {
		t.Fatalf("EstRange(1,+Inf) = %f, 期望 0", sel)
	}
	if sel := h.EstRange(math.Inf(-1), math.Inf(1)); sel != 0 {
		t.Fatalf("EstRange(-Inf,+Inf) = %f, 期望 0", sel)
	}
}
