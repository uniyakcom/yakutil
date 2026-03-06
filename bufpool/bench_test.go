package bufpool

import (
	"testing"
)

func BenchmarkPool_GetPut_Cycle(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		buf := Get(128)
		Put(buf)
	}
}

func BenchmarkPool_CrossSize(b *testing.B) {
	sizes := [3]int{64, 1024, 16384}
	b.ReportAllocs()
	for b.Loop() {
		for _, sz := range sizes {
			buf := Get(sz)
			Put(buf)
		}
	}
}

func BenchmarkPool_GetPut_Parallel(b *testing.B) {
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			buf := Get(256)
			Put(buf)
		}
	})
}
