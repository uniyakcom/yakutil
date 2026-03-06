package wpool

import (
	"sync"
	"testing"
	"time"
)

func BenchmarkPool_TrySubmit(b *testing.B) {
	p := NewPool(64, DefaultIdleTimeout)
	defer p.Stop()
	b.ReportAllocs()
	for b.Loop() {
		p.TrySubmit(func() {})
	}
}

func BenchmarkPool_vs_GoSpawn(b *testing.B) {
	b.Run("wpool", func(b *testing.B) {
		p := NewPool(256, DefaultIdleTimeout)
		defer p.Stop()
		var wg sync.WaitGroup
		b.ReportAllocs()
		for b.Loop() {
			wg.Add(1)
			p.Submit(func() { wg.Done() })
		}
		wg.Wait()
	})

	b.Run("go_spawn", func(b *testing.B) {
		var wg sync.WaitGroup
		b.ReportAllocs()
		for b.Loop() {
			wg.Add(1)
			go func() { wg.Done() }()
		}
		wg.Wait()
	})
}

func BenchmarkAdaptive_Submit_Parallel(b *testing.B) {
	p := NewAdaptive(4, 256, 100*time.Millisecond, DefaultIdleTimeout)
	defer p.Stop()
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			p.Submit(func() {})
		}
	})
}
