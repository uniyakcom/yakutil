package sketch

import (
	"fmt"
	"testing"
)

// TestCMS_Basic 验证基本插入和查询
func TestCMS_Basic(t *testing.T) {
	c := New()

	// 空 CMS 查询返回 0
	if got := c.CountStr("no-exist"); got != 0 {
		t.Fatalf("空 CMS CountStr() = %d, 期望 0", got)
	}

	// 插入 key "a" 5 次
	for i := 0; i < 5; i++ {
		c.AddStr("a")
	}

	got := c.CountStr("a")
	// Count-Min Sketch 是单向过估计，所以 got >= 5
	if got < 5 {
		t.Fatalf("CountStr(a) = %d, 期望 >= 5", got)
	}
}

// TestCMS_AddCount 验证 Add 带计数
func TestCMS_AddCount(t *testing.T) {
	c := New()
	c.Add([]byte("key1"), 100)
	c.Add([]byte("key1"), 50)

	got := c.Count([]byte("key1"))
	if got < 150 {
		t.Fatalf("Count(key1) = %d, 期望 >= 150", got)
	}
}

// TestCMS_Accuracy 验证大量不同 key 的精度
func TestCMS_Accuracy(t *testing.T) {
	c := NewSized(4096, 5)

	// 插入 10000 个不同 key，各 1 次
	n := 10000
	for i := 0; i < n; i++ {
		c.AddStr(fmt.Sprintf("k%d", i))
	}

	// 抽查一些 key
	overCount := 0
	tests := 100
	for i := 0; i < tests; i++ {
		got := c.CountStr(fmt.Sprintf("k%d", i))
		if got < 1 {
			t.Fatalf("CountStr(k%d) = %d, 期望 >= 1", i, got)
		}
		if got > 5 {
			overCount++
		}
	}
	// 过估计太多的 key 不应超过 10%
	if overCount > tests/10 {
		t.Fatalf("过估计 key 数 = %d/%d，超过 10%%", overCount, tests)
	}
}

// TestCMS_Total 验证累计计数
func TestCMS_Total(t *testing.T) {
	c := New()
	c.AddStr("a")
	c.AddStr("b")
	c.Add([]byte("c"), 3)
	if got := c.Total(); got != 5 {
		t.Fatalf("Total() = %d, 期望 5", got)
	}
}

// TestCMS_Merge 验证合并
func TestCMS_Merge(t *testing.T) {
	c1 := NewSized(1024, 4)
	c2 := NewSized(1024, 4)

	c1.Add([]byte("x"), 10)
	c2.Add([]byte("x"), 20)

	c1.Merge(c2)
	got := c1.Count([]byte("x"))
	if got < 30 {
		t.Fatalf("Merge 后 Count(x) = %d, 期望 >= 30", got)
	}
	if c1.Total() != 30 {
		t.Fatalf("Merge 后 Total() = %d, 期望 30", c1.Total())
	}
}

// TestCMS_Reset 验证清空
func TestCMS_Reset(t *testing.T) {
	c := New()
	c.Add([]byte("key"), 100)
	c.Reset()
	if got := c.Count([]byte("key")); got != 0 {
		t.Fatalf("Reset 后 Count() = %d, 期望 0", got)
	}
	if c.Total() != 0 {
		t.Fatalf("Reset 后 Total() = %d, 期望 0", c.Total())
	}
}

// BenchmarkCMS_Add 基准测试 Add 性能
func BenchmarkCMS_Add(b *testing.B) {
	c := New()
	keys := make([][]byte, 1024)
	for i := range keys {
		keys[i] = []byte(fmt.Sprintf("bench-%08d", i))
	}
	b.ResetTimer()
	for b.Loop() {
		for _, k := range keys {
			c.Add(k, 1)
		}
	}
}
