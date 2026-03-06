package fold

import (
	"strings"
	"testing"
)

// ─── Lower ───────────────────────────────────────────────────────────────────

func TestLower(t *testing.T) {
	for c := byte('A'); c <= 'Z'; c++ {
		got := Lower(c)
		want := c + ('a' - 'A')
		if got != want {
			t.Errorf("Lower(%c) = %c, want %c", c, got, want)
		}
	}
	// 非字母不变
	for _, c := range []byte{'0', '9', '.', '-', '\x00', '\xff'} {
		if got := Lower(c); got != c {
			t.Errorf("Lower(0x%02x) = 0x%02x, want unchanged", c, got)
		}
	}
}

// ─── Equal ───────────────────────────────────────────────────────────────────

func TestEqual(t *testing.T) {
	tests := []struct {
		a    []byte
		b    string
		want bool
	}{
		{[]byte("Content-Type"), "content-type", true},
		{[]byte("CONTENT-TYPE"), "content-type", true},
		{[]byte("Content-Type"), "Content-Type", true},
		{[]byte("foo"), "bar", false},
		{[]byte("foo"), "fooo", false},
		{[]byte(""), "", true},
		{nil, "", true},
		{[]byte("ABC123"), "abc123", true},
	}
	for _, tt := range tests {
		got := Equal(tt.a, tt.b)
		if got != tt.want {
			t.Errorf("Equal(%q, %q) = %v, want %v", tt.a, tt.b, got, tt.want)
		}
	}
}

// ─── EqualBytes ──────────────────────────────────────────────────────────────

func TestEqualBytes(t *testing.T) {
	if !EqualBytes([]byte("Hello"), []byte("hello")) {
		t.Error("EqualBytes(Hello, hello) should be true")
	}
	if EqualBytes([]byte("Hello"), []byte("world")) {
		t.Error("EqualBytes(Hello, world) should be false")
	}
}

// ─── EqualStr ────────────────────────────────────────────────────────────────

func TestEqualStr(t *testing.T) {
	if !EqualStr("Transfer-Encoding", "transfer-encoding") {
		t.Error("EqualStr should match")
	}
	if EqualStr("abc", "abd") {
		t.Error("EqualStr should not match")
	}
	if EqualStr("ab", "abc") {
		t.Error("EqualStr different length should not match")
	}
}

// ─── 与标准库一致性 ──────────────────────────────────────────────────────────

func TestEqual_ConsistentWithStdlib(t *testing.T) {
	cases := []struct{ a, b string }{
		{"Content-Type", "content-type"},
		{"HOST", "Host"},
		{"keep-alive", "Keep-Alive"},
		{"X-Forwarded-For", "x-forwarded-for"},
		{"abc", "ABC"},
		{"abc", "abd"},
		{"", ""},
	}
	for _, tt := range cases {
		got := EqualStr(tt.a, tt.b)
		want := strings.EqualFold(tt.a, tt.b)
		if got != want {
			t.Errorf("EqualStr(%q, %q) = %v, strings.EqualFold = %v", tt.a, tt.b, got, want)
		}
	}
}

// ─── Benchmarks ─────────────────────────────────────────────────────────────

func BenchmarkEqual_Short(b *testing.B) {
	a := []byte("Content-Type")
	s := "content-type"
	for b.Loop() {
		Equal(a, s)
	}
}

func BenchmarkEqual_Long(b *testing.B) {
	a := []byte("X-Forwarded-For-Some-Very-Long-Header-Name")
	s := "x-forwarded-for-some-very-long-header-name"
	for b.Loop() {
		Equal(a, s)
	}
}

func BenchmarkEqualStr(b *testing.B) {
	for b.Loop() {
		EqualStr("Transfer-Encoding", "transfer-encoding")
	}
}
