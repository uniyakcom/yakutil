package backoff

import (
	"runtime"
	"testing"
)

func BenchmarkBackoff_FullCycle(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		var bo Backoff
		// Phase1(64) + Phase2(64) + Phase3(3) = 131 iterations
		for range 131 {
			bo.Spin()
		}
		bo.Reset()
	}
}

func BenchmarkBackoff_SpinOnly_vs_Gosched(b *testing.B) {
	b.Run("Backoff.Spin", func(b *testing.B) {
		b.ReportAllocs()
		var bo Backoff
		for b.Loop() {
			bo.Spin()
			if bo.N > DefaultSpinN-1 {
				bo.Reset()
			}
		}
	})
	b.Run("Gosched", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			runtime.Gosched()
		}
	})
}
