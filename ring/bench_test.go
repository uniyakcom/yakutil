package ring

import (
	"bytes"
	"testing"
)

func BenchmarkWriteRead_WrapAround(b *testing.B) {
	// 填满一半再持续读写，强制 wrap
	buf := New(1024)
	fill := make([]byte, 512)
	chunk := make([]byte, 64)
	read := make([]byte, 64)
	buf.Write(fill) //nolint:errcheck
	buf.Read(fill)  //nolint:errcheck // 进 head 到 512

	b.ReportAllocs()
	for b.Loop() {
		buf.Write(chunk) //nolint:errcheck
		buf.Read(read)   //nolint:errcheck
	}
}

func BenchmarkPeekDiscard(b *testing.B) {
	buf := New(4096)
	data := make([]byte, 64)
	b.ReportAllocs()
	for b.Loop() {
		buf.Write(data) //nolint:errcheck
		buf.Peek(64)
		buf.Discard(64)
	}
}

func BenchmarkReadFrom_4K(b *testing.B) {
	src := bytes.NewReader(make([]byte, 4096))
	buf := New(8192)
	b.SetBytes(4096)
	b.ReportAllocs()
	for b.Loop() {
		src.Reset(make([]byte, 4096))
		buf.Reset()
		buf.ReadFrom(src) //nolint:errcheck
	}
}
