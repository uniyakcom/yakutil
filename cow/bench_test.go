package cow

import (
	"sync"
	"testing"
)

type benchCfg struct {
	Max  int
	Name string
}

func BenchmarkValue_ReadHeavy(b *testing.B) {
	v := New(benchCfg{Max: 100, Name: "test"})
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			if i%20 == 0 { // 5% write
				v.Store(benchCfg{Max: i, Name: "test"})
			} else {
				_ = v.Load()
			}
			i++
		}
	})
}

func BenchmarkValue_vs_RWMutex(b *testing.B) {
	cfg := benchCfg{Max: 100, Name: "bench"}

	b.Run("cow.Value", func(b *testing.B) {
		v := New(cfg)
		b.ReportAllocs()
		b.RunParallel(func(pb *testing.PB) {
			for pb.Next() {
				_ = v.Load()
			}
		})
	})

	b.Run("RWMutex", func(b *testing.B) {
		var mu sync.RWMutex
		val := cfg
		b.ReportAllocs()
		b.RunParallel(func(pb *testing.PB) {
			for pb.Next() {
				mu.RLock()
				_ = val
				mu.RUnlock()
			}
		})
	})
}
