package topn

import (
	"fmt"
	"math/rand/v2"
	"slices"
	"testing"
)

// ─── TopN ─────────────────────────────────────────────────────────────────────

func TestTopN_Basic(t *testing.T) {
	src := []int{3, 1, 4, 1, 5, 9, 2, 6}
	got := TopN(src, 3, func(a, b int) bool { return a > b })
	if len(got) != 3 {
		t.Fatalf("len = %d, want 3", len(got))
	}
	want := []int{9, 6, 5}
	for i, v := range want {
		if got[i] != v {
			t.Errorf("got[%d] = %d, want %d", i, got[i], v)
		}
	}
}

func TestTopN_NGreaterThanLen(t *testing.T) {
	src := []int{3, 1, 4}
	got := TopN(src, 10, func(a, b int) bool { return a > b })
	if len(got) != 3 {
		t.Fatalf("len = %d, want 3", len(got))
	}
	if got[0] != 4 || got[1] != 3 || got[2] != 1 {
		t.Errorf("got = %v, want [4 3 1]", got)
	}
}

func TestTopN_NZero(t *testing.T) {
	src := []int{1, 2, 3}
	got := TopN(src, 0, func(a, b int) bool { return a > b })
	if got != nil {
		t.Fatalf("TopN(n=0) = %v, want nil", got)
	}
}

func TestTopN_NNegative(t *testing.T) {
	src := []int{1, 2, 3}
	got := TopN(src, -1, func(a, b int) bool { return a > b })
	if got != nil {
		t.Fatalf("TopN(n=-1) = %v, want nil", got)
	}
}

func TestTopN_EmptyInput(t *testing.T) {
	got := TopN([]int{}, 3, func(a, b int) bool { return a > b })
	if got != nil {
		t.Fatalf("TopN(empty) = %v, want nil", got)
	}
}

func TestTopN_NilInput(t *testing.T) {
	got := TopN(nil, 3, func(a, b int) bool { return a > b })
	if got != nil {
		t.Fatalf("TopN(nil) = %v, want nil", got)
	}
}

func TestTopN_N1(t *testing.T) {
	src := []int{5, 3, 9, 1}
	got := TopN(src, 1, func(a, b int) bool { return a > b })
	if len(got) != 1 || got[0] != 9 {
		t.Fatalf("TopN(n=1) = %v, want [9]", got)
	}
}

func TestTopN_AllEqual(t *testing.T) {
	src := []int{5, 5, 5, 5}
	got := TopN(src, 2, func(a, b int) bool { return a > b })
	if len(got) != 2 || got[0] != 5 || got[1] != 5 {
		t.Fatalf("got = %v, want [5 5]", got)
	}
}

// 确保 src 不被修改
func TestTopN_DoesNotModifySrc(t *testing.T) {
	src := []int{3, 1, 4, 1, 5}
	orig := make([]int, len(src))
	copy(orig, src)
	_ = TopN(src, 3, func(a, b int) bool { return a > b })
	for i, v := range orig {
		if src[i] != v {
			t.Fatalf("src[%d] changed: got %d, want %d", i, src[i], v)
		}
	}
}

func TestTopN_TopItemsSubset(t *testing.T) {
	src := []int{9, 3, 7, 1, 5, 6, 2, 8, 4}
	got := TopN(src, 4, func(a, b int) bool { return a > b })
	want := []int{9, 8, 7, 6}
	for i, w := range want {
		if got[i] != w {
			t.Errorf("got[%d] = %d, want %d", i, got[i], w)
		}
	}
}

// 与标准库排序结果对比验证
func TestTopN_AgainstSort(t *testing.T) {
	const size = 200
	const n = 20
	src := make([]int, size)
	for i := range src {
		src[i] = rand.IntN(10000)
	}
	got := TopN(src, n, func(a, b int) bool { return a > b })

	sorted := make([]int, len(src))
	copy(sorted, src)
	slices.SortFunc(sorted, func(a, b int) int { return b - a })
	want := sorted[:n]

	for i := range want {
		if got[i] != want[i] {
			t.Errorf("pos %d: got %d, want %d", i, got[i], want[i])
		}
	}
}

// 结构体测试（模拟 HotShardInfo）
func TestTopN_Struct(t *testing.T) {
	type ShardStat struct {
		Idx int
		Ops int64
	}
	src := []ShardStat{
		{0, 100}, {1, 500}, {2, 50}, {3, 800}, {4, 300},
	}
	got := TopN(src, 3, func(a, b ShardStat) bool { return a.Ops > b.Ops })
	if len(got) != 3 {
		t.Fatalf("len = %d, want 3", len(got))
	}
	want := []int64{800, 500, 300}
	for i, w := range want {
		if got[i].Ops != w {
			t.Errorf("got[%d].Ops = %d, want %d", i, got[i].Ops, w)
		}
	}
}

// ─── ByKey ────────────────────────────────────────────────────────────────────

func TestByKey_Int64(t *testing.T) {
	type item struct{ val int64 }
	src := []item{{3}, {1}, {9}, {5}, {7}}
	got := ByKey(src, 2, func(x item) int64 { return x.val })
	if len(got) != 2 || got[0].val != 9 || got[1].val != 7 {
		t.Fatalf("ByKey got %v, want [{9} {7}]", got)
	}
}

func TestByKey_String(t *testing.T) {
	src := []string{"banana", "apple", "cherry", "date"}
	got := ByKey(src, 2, func(s string) string { return s })
	if got[0] != "date" || got[1] != "cherry" {
		t.Fatalf("ByKey got %v, want [date cherry]", got)
	}
}

func TestByKey_Float64(t *testing.T) {
	src := []float64{1.5, 3.14, 2.72, 0.5}
	got := ByKey(src, 1, func(f float64) float64 { return f })
	if got[0] != 3.14 {
		t.Fatalf("ByKey float got %v, want [3.14]", got)
	}
}

func TestByKey_NegativeKey(t *testing.T) {
	// 按负值降序 = 按正值升序
	src := []int{5, 3, 8, 1}
	got := ByKey(src, 2, func(v int) int { return -v })
	// 最"大"的 -v 是最小的 v: 1, 3
	if got[0] != 1 || got[1] != 3 {
		t.Fatalf("ByKey negative got %v, want [1 3]", got)
	}
}

// ─── Benchmark ───────────────────────────────────────────────────────────────

func BenchmarkTopN_64_5(b *testing.B) {
	src := make([]int64, 64)
	for i := range src {
		src[i] = rand.Int64N(1_000_000)
	}
	b.ResetTimer()
	for range b.N {
		_ = TopN(src, 5, func(a, b int64) bool { return a > b })
	}
}

func BenchmarkByKey_64_5(b *testing.B) {
	type ShardStat struct{ Ops int64 }
	src := make([]ShardStat, 64)
	for i := range src {
		src[i].Ops = rand.Int64N(1_000_000)
	}
	b.ResetTimer()
	for range b.N {
		_ = ByKey(src, 5, func(s ShardStat) int64 { return s.Ops })
	}
}

func BenchmarkTopN_1024_10(b *testing.B) {
	src := make([]int64, 1024)
	for i := range src {
		src[i] = rand.Int64N(1_000_000)
	}
	b.ResetTimer()
	for range b.N {
		_ = TopN(src, 10, func(a, b int64) bool { return a > b })
	}
}

func ExampleTopN() {
	scores := []int{42, 17, 95, 8, 63, 31}
	top3 := TopN(scores, 3, func(a, b int) bool { return a > b })
	fmt.Println(top3)
	// Output: [95 63 42]
}

func ExampleByKey() {
	type Player struct {
		Name  string
		Score int
	}
	players := []Player{
		{"Alice", 85},
		{"Bob", 92},
		{"Carol", 78},
		{"Dave", 95},
	}
	top2 := ByKey(players, 2, func(p Player) int { return p.Score })
	fmt.Printf("%s(%d) %s(%d)\n", top2[0].Name, top2[0].Score, top2[1].Name, top2[1].Score)
	// Output: Dave(95) Bob(92)
}

// ─── 自适应排序策略 ────────────────────────────────────────────────────────

// TestTopN_AdaptiveLargeN 验证 n > len(src)/4 时触发全量排序路径，结果正确
func TestTopN_AdaptiveLargeN(t *testing.T) {
	// 100 个元素，n=40 > 100/4=25，触发 slices.SortFunc
	src := rand.Perm(100)
	n := 40
	got := TopN(src, n, func(a, b int) bool { return a > b })
	if len(got) != n {
		t.Fatalf("len = %d, want %d", len(got), n)
	}
	// 验证结果是降序（前 n 个最大元素，有序）
	if !slices.IsSortedFunc(got, func(a, b int) int { return b - a }) {
		t.Fatalf("result should be descending: %v", got[:10])
	}
	// 最大值应为 99
	if got[0] != 99 {
		t.Fatalf("got[0] = %d, want 99", got[0])
	}
}

// TestTopN_AdaptiveSmallN 验证 n <= len(src)/4 时仍走部分选择排序，结果正确
func TestTopN_AdaptiveSmallN(t *testing.T) {
	// 100 个元素，n=5 <= 25，走部分选择排序
	src := rand.Perm(100)
	n := 5
	got := TopN(src, n, func(a, b int) bool { return a > b })
	if len(got) != n {
		t.Fatalf("len = %d, want %d", len(got), n)
	}
	// 前 5 大的元素应为 99,98,97,96,95
	want := []int{99, 98, 97, 96, 95}
	for i, v := range want {
		if got[i] != v {
			t.Fatalf("got[%d] = %d, want %d", i, got[i], v)
		}
	}
}

// TestTopN_AdaptiveBoundary 验证 n == len(src)/4 时（恰好边界）两种路径均可接受
func TestTopN_AdaptiveBoundary(t *testing.T) {
	N := 100
	n := N / 4 // 恰好等于边界，走部分选择排序路径
	src := rand.Perm(N)
	got := TopN(src, n, func(a, b int) bool { return a > b })
	if len(got) != n {
		t.Fatalf("len = %d, want %d", len(got), n)
	}
	if got[0] != N-1 {
		t.Fatalf("got[0] = %d, want %d", got[0], N-1)
	}
}
