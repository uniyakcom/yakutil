package lru

import (
	"fmt"
	"sync"
	"testing"
)

func BenchmarkCache_Set_Eviction(b *testing.B) {
	// 容量 100，持续写入 → 每次 Set 触发驱逐
	c := New[int](4, 100)
	for i := 0; i < 400; i++ {
		c.Set(fmt.Sprintf("pre-%d", i), i)
	}
	b.ReportAllocs()
	for b.Loop() {
		c.Set(fmt.Sprintf("new-%d", b.N), b.N)
	}
}

func BenchmarkCache_Mixed_90R10W(b *testing.B) {
	c := New[int](16, 1024)
	for i := 0; i < 10000; i++ {
		c.Set(fmt.Sprintf("k-%d", i), i)
	}
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			if i%10 == 0 { // 10% write
				c.Set(fmt.Sprintf("k-%d", i%10000), i)
			} else {
				c.Get(fmt.Sprintf("k-%d", i%10000))
			}
			i++
		}
	})
}

func BenchmarkCache_vs_SyncMap(b *testing.B) {
	const N = 10000

	b.Run("lru.Cache", func(b *testing.B) {
		c := New[int](16, N)
		for i := 0; i < N; i++ {
			c.Set(fmt.Sprintf("k-%d", i), i)
		}
		b.ReportAllocs()
		b.RunParallel(func(pb *testing.PB) {
			i := 0
			for pb.Next() {
				c.Get(fmt.Sprintf("k-%d", i%N))
				i++
			}
		})
	})

	b.Run("sync.Map", func(b *testing.B) {
		var m sync.Map
		for i := 0; i < N; i++ {
			m.Store(fmt.Sprintf("k-%d", i), i)
		}
		b.ReportAllocs()
		b.RunParallel(func(pb *testing.PB) {
			i := 0
			for pb.Next() {
				m.Load(fmt.Sprintf("k-%d", i%N))
				i++
			}
		})
	})
}
