package percpu

import (
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
)

// BenchmarkCounter_AddLoad_Mixed 模拟实际监控场景：多核并发写为主，偷穿读取。
func BenchmarkCounter_AddLoad_Mixed(b *testing.B) {
	c := New(runtime.GOMAXPROCS(0))
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			if i&0xF == 0 { // ~6% 读
				c.Load()
			} else {
				c.Add(1)
			}
			i++
		}
	})
}

// BenchmarkCounter_vs_AtomicInt64 对比串行和并行场景下的写入开销。
//
// 串行场景：atomic.Int64 更快（单 cas 指令对比天然务差）。
// 并行场景：核数越多，percpu 优势越明显（消除 cache-line 乱吓）。
func BenchmarkCounter_vs_AtomicInt64(b *testing.B) {
	b.Run("percpu/serial", func(b *testing.B) {
		c := New(runtime.GOMAXPROCS(0))
		b.ReportAllocs()
		for b.Loop() {
			c.Add(1)
		}
	})
	b.Run("atomic/serial", func(b *testing.B) {
		var v atomic.Int64
		b.ReportAllocs()
		for b.Loop() {
			v.Add(1)
		}
	})
	b.Run("percpu/parallel", func(b *testing.B) {
		c := New(runtime.GOMAXPROCS(0))
		b.ReportAllocs()
		b.RunParallel(func(pb *testing.PB) {
			for pb.Next() {
				c.Add(1)
			}
		})
	})
	b.Run("atomic/parallel", func(b *testing.B) {
		var v atomic.Int64
		b.ReportAllocs()
		b.RunParallel(func(pb *testing.PB) {
			for pb.Next() {
				v.Add(1)
			}
		})
	})
}

// BenchmarkCounter_Load_vs_AtomicLoad 对比读取开销。
//
// percpu.Load = O(slots)，12 核时约 8ns；atomic.Load < 1ns。
// 这是 percpu 在高频读场景居下风的根本原因。
func BenchmarkCounter_Load_vs_AtomicLoad(b *testing.B) {
	b.Run("percpu/load", func(b *testing.B) {
		c := New(runtime.GOMAXPROCS(0))
		c.Add(12345)
		b.ReportAllocs()
		for b.Loop() {
			_ = c.Load()
		}
	})
	b.Run("atomic/load", func(b *testing.B) {
		var v atomic.Int64
		v.Store(12345)
		b.ReportAllocs()
		for b.Loop() {
			_ = v.Load()
		}
	})
}

// BenchmarkCounter_HighContention 模拟 8x GOMAXPROCS goroutine 的极高第争场景。
func BenchmarkCounter_HighContention(b *testing.B) {
	procs := runtime.GOMAXPROCS(0)
	c := New(procs)
	b.ReportAllocs()
	b.SetParallelism(8) // 8x GOMAXPROCS goroutines
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			c.Add(1)
		}
	})
}

// BenchmarkCounter_Reset 并发写入期间的 Reset 开销。
func BenchmarkCounter_Reset(b *testing.B) {
	c := New(runtime.GOMAXPROCS(0))
	var wg sync.WaitGroup
	wg.Add(1)
	done := make(chan struct{})
	go func() {
		wg.Done()
		for {
			select {
			case <-done:
				return
			default:
				c.Add(1)
			}
		}
	}()
	wg.Wait()
	b.ReportAllocs()
	for b.Loop() {
		c.Reset()
	}
	close(done)
}
