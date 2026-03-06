package wheel

import (
	"testing"
	"time"
)

func BenchmarkWheel_Advance_10KTimers(b *testing.B) {
	w := New[int](time.Millisecond, 256)
	for i := 0; i < 10000; i++ {
		w.Add(time.Millisecond, i) // 全部在下一个 tick 到期
	}
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		// Re-add after consume
		w.Advance(func(v int) {})
		// 补回
		for i := 0; i < 10000; i++ {
			w.Add(time.Millisecond, i)
		}
	}
}

func BenchmarkWheel_AddCancel_Churn(b *testing.B) {
	w := New[int](time.Millisecond, 1024)
	b.ReportAllocs()
	for b.Loop() {
		id := w.Add(10*time.Second, 42)
		w.Cancel(id)
	}
}

func BenchmarkWheel_Add_Parallel(b *testing.B) {
	w := New[int](time.Millisecond, 1024)
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			w.Add(time.Duration(i%100)*time.Millisecond, i)
			i++
		}
	})
}
