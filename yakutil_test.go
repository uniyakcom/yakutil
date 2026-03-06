package yakutil

import (
	"math/bits"
	"testing"
)

// ─── B2S / S2B ──────────────────────────────────────────────────────────────

func TestB2S(t *testing.T) {
	cases := []string{"", "hello", "こんにちは", "a\x00b"}
	for _, s := range cases {
		got := B2S([]byte(s))
		if got != s {
			t.Errorf("B2S(%q) = %q, want %q", s, got, s)
		}
	}
}

func TestS2B(t *testing.T) {
	cases := []string{"", "world", "日本語", "\xff\xfe"}
	for _, s := range cases {
		got := S2B(s)
		if string(got) != s {
			t.Errorf("S2B(%q) = %q, want %q", s, got, s)
		}
		if len(got) != len(s) {
			t.Errorf("S2B(%q) len = %d, want %d", s, len(got), len(s))
		}
	}
}

func TestB2S_S2B_Roundtrip(t *testing.T) {
	orig := []byte("roundtrip test 🚀")
	s := B2S(orig)
	b := S2B(s)
	if string(b) != string(orig) {
		t.Errorf("roundtrip failed: got %q, want %q", b, orig)
	}
}

func TestB2S_Empty(t *testing.T) {
	s := B2S(nil)
	if s != "" {
		t.Errorf("B2S(nil) = %q, want empty", s)
	}
	s = B2S([]byte{})
	if s != "" {
		t.Errorf("B2S([]byte{}) = %q, want empty", s)
	}
}

func TestS2B_Empty(t *testing.T) {
	b := S2B("")
	if len(b) != 0 {
		t.Errorf("S2B(\"\") len = %d, want 0", len(b))
	}
}

// ─── IsPow2 ─────────────────────────────────────────────────────────────────

func TestIsPow2(t *testing.T) {
	tests := []struct {
		n    int
		want bool
	}{
		{0, false},
		{1, true},
		{2, true},
		{3, false},
		{4, true},
		{7, false},
		{8, true},
		{16, true},
		{17, false},
		{1024, true},
		{1023, false},
		{-1, false},
		{-4, false},
		{1 << 20, true},
		{(1 << 20) + 1, false},
	}
	for _, tt := range tests {
		got := IsPow2(tt.n)
		if got != tt.want {
			t.Errorf("IsPow2(%d) = %v, want %v", tt.n, got, tt.want)
		}
	}
}

// ─── Pow2Ceil ───────────────────────────────────────────────────────────────

func TestPow2Ceil(t *testing.T) {
	tests := []struct {
		n    int
		want int
	}{
		{0, 1},
		{1, 1},
		{2, 2},
		{3, 4},
		{4, 4},
		{5, 8},
		{7, 8},
		{8, 8},
		{9, 16},
		{15, 16},
		{16, 16},
		{17, 32},
		{100, 128},
		{1000, 1024},
		{1024, 1024},
		{1025, 2048},
		{-1, 1},
		{-100, 1},
	}
	for _, tt := range tests {
		got := Pow2Ceil(tt.n)
		if got != tt.want {
			t.Errorf("Pow2Ceil(%d) = %d, want %d", tt.n, got, tt.want)
		}
	}
}

func TestPow2Ceil_IsPow2_Consistency(t *testing.T) {
	// Pow2Ceil 的结果必须是 2 的幂
	for n := 0; n <= 4096; n++ {
		c := Pow2Ceil(n)
		if !IsPow2(c) {
			t.Fatalf("Pow2Ceil(%d) = %d, not a power of 2", n, c)
		}
		if n > 0 && c < n {
			t.Fatalf("Pow2Ceil(%d) = %d < n", n, c)
		}
	}
}

// ─── CacheLine / Pad ────────────────────────────────────────────────────────

func TestCacheLine(t *testing.T) {
	if CacheLine != 64 {
		t.Fatalf("CacheLine = %d, want 64", CacheLine)
	}
}

func TestPadSize(t *testing.T) {
	var p Pad
	if len(p) != CacheLine {
		t.Fatalf("len(Pad) = %d, want %d", len(p), CacheLine)
	}
}

// ─── Benchmarks ─────────────────────────────────────────────────────────────

func BenchmarkB2S(b *testing.B) {
	data := []byte("benchmark test string for B2S conversion")
	b.ResetTimer()
	for b.Loop() {
		_ = B2S(data)
	}
}

func BenchmarkS2B(b *testing.B) {
	s := "benchmark test string for S2B conversion"
	b.ResetTimer()
	for b.Loop() {
		_ = S2B(s)
	}
}

func BenchmarkIsPow2(b *testing.B) {
	for b.Loop() {
		_ = IsPow2(1024)
	}
}

func BenchmarkPow2Ceil(b *testing.B) {
	for b.Loop() {
		_ = Pow2Ceil(1000)
	}
}

// ─── Pow2Ceil 溢出 ──────────────────────────────────────────────────────────

func TestPow2Ceil_Overflow(t *testing.T) {
	// 使用平台自适应的溢出阈值：1<<30 on 32-bit，1<<62 on 64-bit
	max := 1 << (bits.UintSize - 2)
	defer func() {
		if r := recover(); r == nil {
			t.Fatalf("Pow2Ceil(%d+1) should panic", max)
		}
	}()
	Pow2Ceil(max + 1)
}

func TestPow2Ceil_MaxValid(t *testing.T) {
	// 平台最大合法输入本身是 2 的幂，不应 panic
	max := 1 << (bits.UintSize - 2)
	got := Pow2Ceil(max)
	if got != max {
		t.Fatalf("Pow2Ceil(%d) = %d", max, got)
	}
}

// ─── Native ─────────────────────────────────────────────────────────────────

func TestNative(t *testing.T) {
	if Native == nil {
		t.Fatal("Native is nil")
	}
	// 应为 BigEndian 或 LittleEndian 之一
	name := Native.String()
	if name != "BigEndian" && name != "LittleEndian" {
		t.Fatalf("Native = %q, unknown byte order", name)
	}
}

// ─── ErrStr ─────────────────────────────────────────────────────────────────

func TestErrStr(t *testing.T) {
	const e = ErrStr("something went wrong")
	if e.Error() != "something went wrong" {
		t.Fatalf("Error() = %q", e.Error())
	}

	// 满足 error 接口
	var err error = e
	if err.Error() != "something went wrong" {
		t.Fatal("ErrStr does not satisfy error interface")
	}
}

func TestErrStr_Empty(t *testing.T) {
	const e = ErrStr("")
	if e.Error() != "" {
		t.Fatalf("empty ErrStr Error() = %q", e.Error())
	}
}

func TestErrStr_Const(t *testing.T) {
	// 验证可作为编译时常量
	const ErrFoo = ErrStr("foo")
	const ErrBar = ErrStr("bar")
	if ErrFoo == ErrBar {
		t.Fatal("different ErrStr should not be equal")
	}
	if ErrFoo != ErrStr("foo") {
		t.Fatal("same content ErrStr should be equal")
	}
}
