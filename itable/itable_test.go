package itable

import (
	"sync"
	"testing"
)

// ─── 基础 ────────────────────────────────────────────────────────────────────

func TestTable_SetGet(t *testing.T) {
	tb := New[int](0)
	val := 42
	tb.Set(100, &val)
	got, ok := tb.Get(100)
	if !ok || *got != 42 {
		t.Fatalf("Get(100) = %v, %v", got, ok)
	}
}

func TestTable_GetMissing(t *testing.T) {
	tb := New[int](0)
	_, ok := tb.Get(999)
	if ok {
		t.Fatal("Get missing should return false")
	}
}

func TestTable_Del(t *testing.T) {
	tb := New[string](0)
	val := "hello"
	tb.Set(50, &val)
	tb.Del(50)
	_, ok := tb.Get(50)
	if ok {
		t.Fatal("after Del, Get should return false")
	}
}

func TestTable_SetNil(t *testing.T) {
	tb := New[int](0)
	val := 42
	tb.Set(10, &val)
	tb.Set(10, nil) // 等效 Del
	_, ok := tb.Get(10)
	if ok {
		t.Fatal("Set(nil) should delete")
	}
}

// ─── Fast path vs slow path ─────────────────────────────────────────────────

func TestTable_FastPath(t *testing.T) {
	tb := New[int](1024)
	for i := 0; i < 1024; i++ {
		v := i * 10
		tb.Set(i, &v)
	}
	for i := 0; i < 1024; i++ {
		got, ok := tb.Get(i)
		if !ok || *got != i*10 {
			t.Fatalf("fast Get(%d) = %v, %v", i, got, ok)
		}
	}
}

func TestTable_SlowPath(t *testing.T) {
	tb := New[int](16)
	val := 99
	// key >= threshold → slow path
	tb.Set(100000, &val)
	got, ok := tb.Get(100000)
	if !ok || *got != 99 {
		t.Fatalf("slow Get(100000) = %v, %v", got, ok)
	}
	tb.Del(100000)
	_, ok = tb.Get(100000)
	if ok {
		t.Fatal("slow Del should work")
	}
}

func TestTable_NegativeKey(t *testing.T) {
	tb := New[int](0)
	val := -1
	tb.Set(-5, &val) // negative → slow path
	got, ok := tb.Get(-5)
	if !ok || *got != -1 {
		t.Fatalf("Get(-5) = %v, %v", got, ok)
	}
}

// ─── Swap ────────────────────────────────────────────────────────────────────

func TestTable_Swap_Fast(t *testing.T) {
	tb := New[int](0)
	v1 := 10
	tb.Set(5, &v1)

	v2 := 20
	old := tb.Swap(5, &v2)
	if old == nil || *old != 10 {
		t.Fatalf("Swap old = %v, want 10", old)
	}
	got, _ := tb.Get(5)
	if *got != 20 {
		t.Fatalf("after Swap, Get = %d, want 20", *got)
	}
}

func TestTable_Swap_Slow(t *testing.T) {
	tb := New[int](16)
	v := 42
	tb.Set(999999, &v)
	v2 := 100
	old := tb.Swap(999999, &v2)
	if old == nil || *old != 42 {
		t.Fatalf("Swap slow old = %v", old)
	}
}

func TestTable_Swap_Nil(t *testing.T) {
	tb := New[int](0)
	v := 5
	tb.Set(3, &v)
	old := tb.Swap(3, nil)
	if old == nil || *old != 5 {
		t.Fatalf("Swap(nil) old = %v", old)
	}
	_, ok := tb.Get(3)
	if ok {
		t.Fatal("Swap(nil) should delete")
	}
}

// ─── 并发 ────────────────────────────────────────────────────────────────────

func TestTable_Concurrent(t *testing.T) {
	tb := New[int](0)
	const goroutines = 16
	const ops = 1000

	var wg sync.WaitGroup
	wg.Add(goroutines)
	for g := 0; g < goroutines; g++ {
		go func(base int) {
			defer wg.Done()
			for i := 0; i < ops; i++ {
				key := base + i
				val := key * 2
				tb.Set(key, &val)
				got, ok := tb.Get(key)
				if !ok || *got != val {
					t.Errorf("Get(%d) = %v, %v", key, got, ok)
					return
				}
			}
		}(g * ops)
	}
	wg.Wait()
}

// ─── Benchmarks ─────────────────────────────────────────────────────────────

func BenchmarkTable_Get_Fast(b *testing.B) {
	tb := New[int](0)
	v := 42
	tb.Set(1000, &v)
	for b.Loop() {
		tb.Get(1000)
	}
}

func BenchmarkTable_Get_Slow(b *testing.B) {
	tb := New[int](16)
	v := 42
	tb.Set(100000, &v)
	for b.Loop() {
		tb.Get(100000)
	}
}

func BenchmarkTable_Set_Fast(b *testing.B) {
	tb := New[int](0)
	v := 42
	for b.Loop() {
		tb.Set(1000, &v)
	}
}

func BenchmarkTable_Get_Parallel(b *testing.B) {
	tb := New[int](0)
	v := 42
	tb.Set(500, &v)
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			tb.Get(500)
		}
	})
}
