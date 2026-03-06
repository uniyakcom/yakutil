package fold

import (
	"strings"
	"testing"
)

func BenchmarkEqual_vs_EqualFold(b *testing.B) {
	s1 := []byte("Content-Type")
	s2 := "content-type"

	b.Run("fold.Equal", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			Equal(s1, s2)
		}
	})

	b.Run("strings.EqualFold", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			strings.EqualFold(string(s1), s2)
		}
	})
}

func BenchmarkEqualBytes_HTTPHeader(b *testing.B) {
	h1 := []byte("X-Forwarded-For")
	h2 := []byte("x-forwarded-for")
	b.ReportAllocs()
	for b.Loop() {
		EqualBytes(h1, h2)
	}
}
