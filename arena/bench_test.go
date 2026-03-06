package arena

import (
	"testing"
)

func BenchmarkArena_Alloc256(b *testing.B) {
	a := New(DefaultChunk)
	b.ReportAllocs()
	for b.Loop() {
		_ = a.Alloc(256)
	}
}

func BenchmarkArena_ChunkExhaust(b *testing.B) {
	// chunk 大小 4K，分配 512B → 每 8 次切换 chunk
	a := New(4096)
	b.ReportAllocs()
	for b.Loop() {
		a.Alloc(512)
	}
}

func BenchmarkArena_Alloc_Parallel_8P(b *testing.B) {
	a := New(DefaultChunk)
	b.SetParallelism(8)
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			a.Alloc(64)
		}
	})
}
