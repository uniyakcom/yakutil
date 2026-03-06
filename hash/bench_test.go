package hash

import (
	"hash/fnv"
	"testing"
)

func BenchmarkSum64_1K(b *testing.B) {
	data := make([]byte, 1024)
	for i := range data {
		data[i] = byte(i)
	}
	b.SetBytes(1024)
	b.ReportAllocs()
	var h uint64
	for b.Loop() {
		h = Sum64(data)
	}
	_ = h
}

func BenchmarkSum64s_vs_StdlibFnv64a(b *testing.B) {
	key := "some/route/path/to/benchmark"
	b.Run("Sum64s", func(b *testing.B) {
		b.ReportAllocs()
		var h uint64
		for b.Loop() {
			h = Sum64s(key)
		}
		_ = h
	})
	b.Run("fnv.New64a", func(b *testing.B) {
		b.ReportAllocs()
		var h uint64
		for b.Loop() {
			f := fnv.New64a()
			f.Write([]byte(key))
			h = f.Sum64()
		}
		_ = h
	})
}
