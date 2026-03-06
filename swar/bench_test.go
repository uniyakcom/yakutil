package swar

import (
	"bytes"
	"testing"
)

func BenchmarkFindEscape_1K(b *testing.B) {
	// 大 buffer，最后一个字节才有转义字符
	data := make([]byte, 1024)
	for i := range data {
		data[i] = 'a'
	}
	data[1023] = '\\'
	b.SetBytes(1024)
	b.ReportAllocs()
	for b.Loop() {
		FindEscape(data)
	}
}

func BenchmarkFindByte_vs_BytesIndexByte(b *testing.B) {
	data := make([]byte, 256)
	for i := range data {
		data[i] = 'x'
	}
	data[200] = '"'

	b.Run("swar.FindByte", func(b *testing.B) {
		b.SetBytes(256)
		b.ReportAllocs()
		for b.Loop() {
			FindByte(data, '"')
		}
	})

	b.Run("bytes.IndexByte", func(b *testing.B) {
		b.SetBytes(256)
		b.ReportAllocs()
		for b.Loop() {
			bytes.IndexByte(data, '"')
		}
	})
}

func BenchmarkFindQuote_JSON(b *testing.B) {
	// 模拟 JSON payload: {"key":"value","name":"test"}
	data := []byte(`{"key":"value","name":"test","id":12345,"nested":{"a":"b"}}`)
	b.SetBytes(int64(len(data)))
	b.ReportAllocs()
	for b.Loop() {
		FindQuote(data)
	}
}
