package yakutil

import (
	"fmt"
	"testing"
)

// ─── ErrStr ─────────────────────────────────────────────────────────────────

func BenchmarkErrStr_Error(b *testing.B) {
	e := ErrStr("connection refused")
	b.ReportAllocs()
	var s string
	for b.Loop() {
		s = e.Error()
	}
	_ = s
}

func BenchmarkFmtErrorf(b *testing.B) {
	b.ReportAllocs()
	var err error
	for b.Loop() {
		err = fmt.Errorf("connection refused")
	}
	_ = err
}

// ─── B2S vs string() 对比 ──────────────────────────────────────────────────

func BenchmarkB2S_vs_StringCast(b *testing.B) {
	data := []byte("Content-Type: application/json")
	b.Run("B2S", func(b *testing.B) {
		b.ReportAllocs()
		var s string
		for b.Loop() {
			s = B2S(data)
		}
		_ = s
	})
	b.Run("string()", func(b *testing.B) {
		b.ReportAllocs()
		var s string
		for b.Loop() {
			s = string(data)
		}
		_ = s
	})
}

// ─── Pow2Ceil 大数 ──────────────────────────────────────────────────────────

func BenchmarkPow2Ceil_Large(b *testing.B) {
	n := (1 << 28) + 1
	b.ReportAllocs()
	var r int
	for b.Loop() {
		r = Pow2Ceil(n)
	}
	_ = r
}
