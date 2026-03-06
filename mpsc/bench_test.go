package mpsc

import (
	"fmt"
	"testing"
)

func BenchmarkRing_GroupCommit_Batch(b *testing.B) {
	for _, batchSize := range []int{1, 8, 64} {
		b.Run(fmt.Sprintf("batch=%d", batchSize), func(b *testing.B) {
			r := New[int64](1024)
			b.ReportAllocs()
			for b.Loop() {
				var seqs [64]uint64
				for j := 0; j < batchSize; j++ {
					seqs[j], _ = r.Enqueue(int64(j))
				}
				start, n := r.Drain(func(v *int64) error { return nil })
				r.Commit(start, n, nil)
				for j := 0; j < batchSize; j++ {
					_ = r.Wait(seqs[j]) //nolint:errcheck
				}
			}
		})
	}
}

// BenchmarkRing_Concurrent 模拟真实多生产者 + 独立消费者 goroutine 场景。
// 这是 spin-then-park 优化的实际受益场景（生产者需等待消费者 goroutine）。
func BenchmarkRing_Concurrent(b *testing.B) {
	for _, producers := range []int{1, 4} {
		b.Run(fmt.Sprintf("p=%d", producers), func(b *testing.B) {
			r := New[int64](1024)
			stop := make(chan struct{})
			// 独立消费者 goroutine（模拟真实 flusher）
			go func() {
				for {
					select {
					case <-stop:
						return
					default:
					}
					start, n := r.Drain(func(v *int64) error { return nil })
					if n > 0 {
						r.Commit(start, n, nil)
					}
				}
			}()
			b.ReportAllocs()
			b.SetParallelism(producers)
			b.RunParallel(func(pb *testing.PB) {
				for pb.Next() {
					if seq, ok := r.Enqueue(42); ok {
						_ = r.Wait(seq) //nolint:errcheck
					}
				}
			})
			close(stop)
		})
	}
}

func BenchmarkRing_Enqueue_Varied_Size(b *testing.B) {
	// 不同 ring 大小下的 Enqueue+Drain 吞吐
	for _, sz := range []int{64, 256, 4096} {
		b.Run(fmt.Sprintf("size=%d", sz), func(b *testing.B) {
			r := New[int64](sz)
			b.ReportAllocs()
			for b.Loop() {
				seq, _ := r.Enqueue(42)
				start, n := r.Drain(func(v *int64) error { return nil })
				r.Commit(start, n, nil)
				_ = r.Wait(seq) //nolint:errcheck
			}
		})
	}
}

func BenchmarkRing_FullRoundtrip(b *testing.B) {
	r := New[int64](1024)
	b.ReportAllocs()
	for b.Loop() {
		seq, _ := r.Enqueue(42)
		start, n := r.Drain(func(v *int64) error { return nil })
		r.Commit(start, n, nil)
		_ = r.Wait(seq) //nolint:errcheck
	}
}
