package swar

import (
	"bytes"
	"testing"
)

// ─── HasZero ─────────────────────────────────────────────────────────────────

func TestHasZero(t *testing.T) {
	tests := []struct {
		x    uint64
		want bool
	}{
		{0x0101010101010101, false},
		{0x0101010101010100, true},
		{0x0100010101010101, true},
		{0x0000000000000000, true},
		{0xFFFFFFFFFFFFFFFF, false},
	}
	for _, tt := range tests {
		if got := HasZero(tt.x); got != tt.want {
			t.Errorf("HasZero(%016x) = %v, want %v", tt.x, got, tt.want)
		}
	}
}

// ─── HasByte ─────────────────────────────────────────────────────────────────

func TestHasByte(t *testing.T) {
	x := uint64(0x4142434445464748) // "ABCDEFGH" in LE
	if !HasByte(x, 0x41) {
		t.Error("should find 0x41")
	}
	if HasByte(x, 0x00) {
		t.Error("should not find 0x00")
	}
}

// ─── FirstByte ───────────────────────────────────────────────────────────────

func TestFirstByte(t *testing.T) {
	// LE: byte[0]=0x48, byte[1]=0x47, ...
	x := uint64(0x4142434445464748)
	if idx := FirstByte(x, 0x48); idx != 0 {
		t.Errorf("FirstByte(0x48) = %d, want 0", idx)
	}
	if idx := FirstByte(x, 0x41); idx != 7 {
		t.Errorf("FirstByte(0x41) = %d, want 7", idx)
	}
	if idx := FirstByte(x, 0xFF); idx != 8 {
		t.Errorf("FirstByte(0xFF) = %d, want 8", idx)
	}
}

// ─── FindByte ────────────────────────────────────────────────────────────────

func TestFindByte(t *testing.T) {
	data := []byte("hello world, this is a test!")
	// 验证与 bytes.IndexByte 一致
	for b := byte(0); b < 128; b++ {
		got := FindByte(data, b)
		want := bytes.IndexByte(data, b)
		if got != want {
			t.Fatalf("FindByte(%q, %q) = %d, want %d", data, b, got, want)
		}
	}
}

func TestFindByte_Empty(t *testing.T) {
	if got := FindByte(nil, 'a'); got != -1 {
		t.Fatalf("FindByte(nil) = %d, want -1", got)
	}
}

func TestFindByte_Short(t *testing.T) {
	data := []byte("abc")
	if got := FindByte(data, 'c'); got != 2 {
		t.Fatalf("FindByte(abc, c) = %d, want 2", got)
	}
	if got := FindByte(data, 'z'); got != -1 {
		t.Fatalf("FindByte(abc, z) = %d, want -1", got)
	}
}

func TestFindByte_Long(t *testing.T) {
	data := make([]byte, 1024)
	for i := range data {
		data[i] = 'A'
	}
	data[1023] = 'X'
	if got := FindByte(data, 'X'); got != 1023 {
		t.Fatalf("FindByte = %d, want 1023", got)
	}
}

// ─── FindQuote ───────────────────────────────────────────────────────────────

func TestFindQuote(t *testing.T) {
	data := []byte(`hello "world"`)
	if got := FindQuote(data); got != 6 {
		t.Fatalf("FindQuote = %d, want 6", got)
	}
	if got := FindQuote([]byte("no quotes")); got != -1 {
		t.Fatalf("FindQuote(no quotes) = %d, want -1", got)
	}
}

// ─── FindEscape ──────────────────────────────────────────────────────────────

func TestFindEscape(t *testing.T) {
	tests := []struct {
		data []byte
		want int
	}{
		{[]byte("hello"), -1},
		{[]byte("he\"llo"), 2},             // "
		{[]byte("he\\llo"), 2},             // backslash
		{[]byte("he\nllo"), 2},             // control char
		{[]byte("\x00abc"), 0},             // NUL
		{[]byte("abcdefghijklmnop\t"), 16}, // control after SWAR batch
		{nil, -1},
	}
	for _, tt := range tests {
		if got := FindEscape(tt.data); got != tt.want {
			t.Errorf("FindEscape(%q) = %d, want %d", tt.data, got, tt.want)
		}
	}
}

func TestFindEscape_CleanLong(t *testing.T) {
	data := make([]byte, 256)
	for i := range data {
		data[i] = 'a'
	}
	if got := FindEscape(data); got != -1 {
		t.Fatalf("FindEscape(clean) = %d, want -1", got)
	}
}

// ─── Benchmarks ─────────────────────────────────────────────────────────────

func BenchmarkFindByte_64(b *testing.B) {
	data := make([]byte, 64)
	for i := range data {
		data[i] = 'a'
	}
	data[63] = 'X'
	b.SetBytes(64)
	for b.Loop() {
		FindByte(data, 'X')
	}
}

func BenchmarkFindEscape_64(b *testing.B) {
	data := make([]byte, 64)
	for i := range data {
		data[i] = 'a'
	}
	b.SetBytes(64)
	for b.Loop() {
		FindEscape(data)
	}
}

func BenchmarkFindByte_1K(b *testing.B) {
	data := make([]byte, 1024)
	for i := range data {
		data[i] = 'a'
	}
	data[1023] = 'X'
	b.SetBytes(1024)
	for b.Loop() {
		FindByte(data, 'X')
	}
}
