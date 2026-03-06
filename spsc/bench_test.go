package spsc

import (
	"testing"
)

func BenchmarkRing_PushPop_Burst(b *testing.B) {
	const batch = 64
	r := New[int64](256)
	b.ReportAllocs()
	for b.Loop() {
		for j := 0; j < batch; j++ {
			r.Push(int64(j))
		}
		for j := 0; j < batch; j++ {
			r.Pop()
		}
	}
}

func BenchmarkRing_PingPong_Latency(b *testing.B) {
	r1 := New[int64](64)
	r2 := New[int64](64)
	done := make(chan struct{})
	go func() {
		for {
			select {
			case <-done:
				return
			default:
				if v, ok := r1.Pop(); ok {
					for !r2.Push(v + 1) {
					}
				}
			}
		}
	}()
	b.ReportAllocs()
	for b.Loop() {
		for !r1.Push(1) {
		}
		for {
			if _, ok := r2.Pop(); ok {
				break
			}
		}
	}
	close(done)
}
