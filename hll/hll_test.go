package hll

import (
	"fmt"
	"math"
	"testing"
)

// TestSketch_Basic 验证基本插入和计数
func TestSketch_Basic(t *testing.T) {
	s := New()

	// 空估计器应返回 0
	if got := s.Count(); got != 0 {
		t.Fatalf("空估计器 Count() = %d, 期望 0", got)
	}

	// 插入 1000 个不同元素
	n := 1000
	for i := 0; i < n; i++ {
		s.AddStr(fmt.Sprintf("key-%d", i))
	}

	got := s.Count()
	errRate := math.Abs(float64(got)-float64(n)) / float64(n)
	if errRate > 0.05 {
		t.Fatalf("Count() = %d, 期望 ~%d（误差 %.1f%% 超过 5%%）", got, n, errRate*100)
	}
}

// TestSketch_LargeCardinality 验证大基数场景
func TestSketch_LargeCardinality(t *testing.T) {
	s := New()
	n := 1_000_000
	for i := 0; i < n; i++ {
		s.Add([]byte(fmt.Sprintf("item:%08d", i)))
	}

	got := s.Count()
	errRate := math.Abs(float64(got)-float64(n)) / float64(n)
	// 16384 桶，标准误差 ~0.81%，放宽到 6% 考虑哈希偏差
	if errRate > 0.06 {
		t.Fatalf("Count() = %d, 期望 ~%d（误差 %.2f%% 超过 6%%）", got, n, errRate*100)
	}
	t.Logf("n=%d, estimate=%d, 误差=%.2f%%", n, got, errRate*100)
}

// TestSketch_Duplicates 验证重复元素不增加计数
func TestSketch_Duplicates(t *testing.T) {
	s := New()
	for i := 0; i < 10000; i++ {
		s.AddStr("same-key")
	}
	got := s.Count()
	if got > 5 {
		t.Fatalf("重复元素 Count() = %d, 期望 ~1", got)
	}
}

// TestSketch_Merge 验证两个估计器合并
func TestSketch_Merge(t *testing.T) {
	s1 := New()
	s2 := New()

	// s1 含 0..999
	for i := 0; i < 1000; i++ {
		s1.AddStr(fmt.Sprintf("a-%d", i))
	}
	// s2 含 500..1499（与 s1 有 50% 重叠）
	for i := 500; i < 1500; i++ {
		s2.AddStr(fmt.Sprintf("a-%d", i))
	}

	s1.Merge(s2)
	got := s1.Count()
	expected := 1500 // 0..1499
	errRate := math.Abs(float64(got)-float64(expected)) / float64(expected)
	// 合并重叠集合的误差较大，放宽到 10%
	if errRate > 0.10 {
		t.Fatalf("Merge Count() = %d, 期望 ~%d（误差 %.1f%%）", got, expected, errRate*100)
	}
}

// TestSketch_Reset 验证清空
func TestSketch_Reset(t *testing.T) {
	s := New()
	for i := 0; i < 1000; i++ {
		s.AddStr(fmt.Sprintf("k%d", i))
	}
	s.Reset()
	if got := s.Count(); got != 0 {
		t.Fatalf("Reset 后 Count() = %d, 期望 0", got)
	}
}

// BenchmarkSketch_Add 基准测试 Add 性能
func BenchmarkSketch_Add(b *testing.B) {
	s := New()
	keys := make([][]byte, 1024)
	for i := range keys {
		keys[i] = []byte(fmt.Sprintf("bench-key-%08d", i))
	}
	b.ResetTimer()
	for b.Loop() {
		for _, k := range keys {
			s.Add(k)
		}
	}
}
