package hash

import (
	"hash/fnv"
	"testing"
)

// ─── 基础正确性 ──────────────────────────────────────────────────────────────

func TestSum64_Empty(t *testing.T) {
	got := Sum64(nil)
	if got != offset64 {
		t.Errorf("Sum64(nil) = %d, want %d (offset basis)", got, uint64(offset64))
	}
	got = Sum64([]byte{})
	if got != offset64 {
		t.Errorf("Sum64([]byte{}) = %d, want %d", got, uint64(offset64))
	}
}

func TestSum64s_Empty(t *testing.T) {
	got := Sum64s("")
	if got != offset64 {
		t.Errorf("Sum64s(\"\") = %d, want %d", got, uint64(offset64))
	}
}

// TestSum64_KnownVectors 对比标准库 hash/fnv
func TestSum64_KnownVectors(t *testing.T) {
	vectors := []string{
		"",
		"a",
		"ab",
		"abc",
		"hello",
		"hello, world!",
		"The quick brown fox jumps over the lazy dog",
		"\x00\x01\x02\x03",
	}

	for _, s := range vectors {
		want := stdFNV1a([]byte(s))
		got := Sum64([]byte(s))
		if got != want {
			t.Errorf("Sum64(%q) = %x, want %x (stdlib)", s, got, want)
		}
	}
}

func TestSum64s_KnownVectors(t *testing.T) {
	vectors := []string{
		"",
		"a",
		"foobar",
		"yakutil hash test",
		"日本語テスト",
	}
	for _, s := range vectors {
		want := stdFNV1a([]byte(s))
		got := Sum64s(s)
		if got != want {
			t.Errorf("Sum64s(%q) = %x, want %x", s, got, want)
		}
	}
}

// ─── Sum64 / Sum64s 一致性 ─────────────────────────────────────────────────

func TestSum64_Sum64s_Consistency(t *testing.T) {
	strs := []string{
		"", "x", "ab", "test", "consistency",
		"a longer string with spaces and 日本語",
	}
	for _, s := range strs {
		h1 := Sum64([]byte(s))
		h2 := Sum64s(s)
		if h1 != h2 {
			t.Errorf("Sum64(%q) = %x != Sum64s = %x", s, h1, h2)
		}
	}
}

// ─── 分布检验 ────────────────────────────────────────────────────────────────

func TestSum64_Distribution(t *testing.T) {
	const n = 10000
	const buckets = 64
	var dist [buckets]int

	for i := 0; i < n; i++ {
		key := []byte{byte(i), byte(i >> 8), byte(i >> 16)}
		h := Sum64(key)
		dist[h%buckets]++
	}

	avg := float64(n) / buckets
	for i, c := range dist {
		ratio := float64(c) / avg
		if ratio < 0.5 || ratio > 1.5 {
			t.Errorf("bucket %d: count=%d, ratio=%.2f (expected ~1.0)", i, c, ratio)
		}
	}
}

// ─── 特殊输入 ────────────────────────────────────────────────────────────────

func TestSum64_SingleByte(t *testing.T) {
	// 所有单字节值映射到不同哈希
	seen := make(map[uint64]byte, 256)
	for i := 0; i < 256; i++ {
		h := Sum64([]byte{byte(i)})
		if prev, ok := seen[h]; ok {
			t.Fatalf("collision: byte(%d) and byte(%d) both hash to %x", i, prev, h)
		}
		seen[h] = byte(i)
	}
}

// ─── Benchmarks ─────────────────────────────────────────────────────────────

func BenchmarkSum64_16B(b *testing.B) {
	data := []byte("0123456789abcdef")
	for b.Loop() {
		_ = Sum64(data)
	}
}

func BenchmarkSum64s_16B(b *testing.B) {
	s := "0123456789abcdef"
	for b.Loop() {
		_ = Sum64s(s)
	}
}

func BenchmarkSum64_64B(b *testing.B) {
	data := make([]byte, 64)
	for i := range data {
		data[i] = byte(i)
	}
	b.ResetTimer()
	for b.Loop() {
		_ = Sum64(data)
	}
}

func BenchmarkSum64s_64B(b *testing.B) {
	s := string(make([]byte, 64))
	b.ResetTimer()
	for b.Loop() {
		_ = Sum64s(s)
	}
}

// ─── helpers ─────────────────────────────────────────────────────────────────

func stdFNV1a(b []byte) uint64 {
	h := fnv.New64a()
	h.Write(b)
	return h.Sum64()
}
