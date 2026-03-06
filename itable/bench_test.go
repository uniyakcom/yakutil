package itable

import (
	"testing"
)

func BenchmarkTable_Swap_Fast(b *testing.B) {
	t := New[int](DefaultThreshold)
	v1, v2 := 1, 2
	t.Set(42, &v1)
	b.ReportAllocs()
	for b.Loop() {
		t.Swap(42, &v2)
	}
}

func BenchmarkTable_Get_vs_MapLookup(b *testing.B) {
	const N = 1000

	b.Run("itable", func(b *testing.B) {
		t := New[int](DefaultThreshold)
		for i := 0; i < N; i++ {
			v := i
			t.Set(i, &v)
		}
		b.ReportAllocs()
		for b.Loop() {
			t.Get(42)
		}
	})

	b.Run("map[int]", func(b *testing.B) {
		m := make(map[int]*int, N)
		for i := 0; i < N; i++ {
			v := i
			m[i] = &v
		}
		b.ReportAllocs()
		for b.Loop() {
			_ = m[42]
		}
	})
}

func BenchmarkTable_Set_Parallel(b *testing.B) {
	t := New[int](DefaultThreshold)
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			v := i
			t.Set(i%10000, &v)
			i++
		}
	})
}
