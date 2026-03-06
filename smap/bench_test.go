package smap

import (
	"fmt"
	"sync"
	"testing"
)

func BenchmarkMap_vs_SyncMap_ReadHeavy(b *testing.B) {
	const n = 1000

	b.Run("smap.Map", func(b *testing.B) {
		m := New[int](32)
		for i := 0; i < n; i++ {
			m.Set(fmt.Sprintf("key-%d", i), i)
		}
		b.ReportAllocs()
		b.RunParallel(func(pb *testing.PB) {
			i := 0
			for pb.Next() {
				m.Get(fmt.Sprintf("key-%d", i%n))
				i++
			}
		})
	})

	b.Run("sync.Map", func(b *testing.B) {
		var m sync.Map
		for i := 0; i < n; i++ {
			m.Store(fmt.Sprintf("key-%d", i), i)
		}
		b.ReportAllocs()
		b.RunParallel(func(pb *testing.PB) {
			i := 0
			for pb.Next() {
				m.Load(fmt.Sprintf("key-%d", i%n))
				i++
			}
		})
	})
}

func BenchmarkMap64_Set_Parallel(b *testing.B) {
	m := New64[int](32)
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		i := uint64(0)
		for pb.Next() {
			m.Set(i, int(i))
			i++
		}
	})
}

func BenchmarkMap_Range_1000(b *testing.B) {
	m := New[int](32)
	for i := 0; i < 1000; i++ {
		m.Set(fmt.Sprintf("key-%d", i), i)
	}
	b.ReportAllocs()
	for b.Loop() {
		m.Range(func(k string, v int) bool { return true })
	}
}
