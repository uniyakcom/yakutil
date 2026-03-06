package ring

import (
	"bytes"
	"io"
	"testing"
)

// FuzzWriteRead 验证任意写入序列后读取得到的数据与写入数据完全一致，
// 不发生截断、错位或数据损坏。
func FuzzWriteRead(f *testing.F) {
	f.Add([]byte("hello, ring buffer!"))
	f.Add([]byte(""))
	f.Add([]byte("\x00\xff\x01\xfe"))
	f.Add(bytes.Repeat([]byte("ab"), 64))
	f.Fuzz(func(t *testing.T, data []byte) {
		b := New(16)
		n, err := b.Write(data)
		if err != nil {
			t.Fatalf("Write error: %v", err)
		}
		if n != len(data) {
			t.Fatalf("Write returned %d, want %d", n, len(data))
		}
		if b.Len() != len(data) {
			t.Fatalf("Len() = %d after Write(%d)", b.Len(), len(data))
		}

		out := make([]byte, len(data)+1)
		rn, rerr := b.Read(out)
		if len(data) == 0 {
			if rerr != io.EOF && rn != 0 {
				t.Fatalf("Read empty buffer: n=%d err=%v", rn, rerr)
			}
			return
		}
		if rerr != nil {
			t.Fatalf("Read error: %v", rerr)
		}
		if rn != len(data) {
			t.Fatalf("Read returned %d, want %d", rn, len(data))
		}
		if !bytes.Equal(out[:rn], data) {
			t.Errorf("Read data mismatch: got %v, want %v", out[:rn], data)
		}
	})
}

// FuzzMultiWriteRead 分段写入多块数据后，验证整体读取不丢字节不越界。
func FuzzMultiWriteRead(f *testing.F) {
	f.Add([]byte("chunk1"), []byte("chunk2"))
	f.Add([]byte(""), []byte("nonempty"))
	f.Add([]byte("wrap"), bytes.Repeat([]byte("x"), 100))
	f.Fuzz(func(t *testing.T, d1, d2 []byte) {
		b := New(32)
		b.Write(d1) //nolint:errcheck
		b.Write(d2) //nolint:errcheck

		want := append(d1, d2...)
		if b.Len() != len(want) {
			t.Fatalf("Len()=%d after two writes, want %d", b.Len(), len(want))
		}
		got := make([]byte, len(want))
		var total int
		for total < len(want) {
			n, err := b.Read(got[total:])
			total += n
			if err != nil && err != io.EOF {
				t.Fatalf("Read error: %v", err)
			}
			if err == io.EOF {
				break
			}
		}
		if total != len(want) {
			t.Fatalf("Read total=%d, want %d", total, len(want))
		}
		if !bytes.Equal(got[:total], want) {
			t.Errorf("data mismatch after multi-write")
		}
	})
}

// FuzzUnreadByte 验证 ReadByte 后立即 UnreadByte 能还原字节，
// 再次 ReadByte 得到相同字节。
func FuzzUnreadByte(f *testing.F) {
	f.Add([]byte("abc"))
	f.Add([]byte("\x00"))
	f.Add([]byte("\xff\xfe"))
	f.Add([]byte("X"))
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) == 0 {
			return
		}
		b := New(64)
		b.Write(data) //nolint:errcheck

		c1, err := b.ReadByte()
		if err != nil {
			t.Fatalf("ReadByte error: %v", err)
		}

		if err := b.UnreadByte(); err != nil {
			t.Fatalf("UnreadByte error: %v", err)
		}

		c2, err := b.ReadByte()
		if err != nil {
			t.Fatalf("ReadByte after UnreadByte error: %v", err)
		}
		if c1 != c2 {
			t.Errorf("ReadByte after UnreadByte = 0x%02x, want 0x%02x", c2, c1)
		}
		if c1 != data[0] {
			t.Errorf("ReadByte = 0x%02x, want data[0]=0x%02x", c1, data[0])
		}
	})
}

// FuzzPeekDiscard 验证 Peek(n) 不消费数据，Discard(n) 后 Len 正确减少。
func FuzzPeekDiscard(f *testing.F) {
	f.Add([]byte("hello"), 3)
	f.Add([]byte("x"), 0)
	f.Add([]byte("abcdefgh"), 8)
	f.Fuzz(func(t *testing.T, data []byte, n int) {
		if n < 0 {
			n = -n
		}
		b := New(64)
		b.Write(data) //nolint:errcheck

		peeked := b.Peek(n)
		expectedPeek := n
		if expectedPeek > len(data) {
			expectedPeek = len(data)
		}
		if len(peeked) != expectedPeek {
			t.Fatalf("Peek(%d) on %d bytes = %d bytes, want %d", n, len(data), len(peeked), expectedPeek)
		}
		// Peek 不应改变 Len
		if b.Len() != len(data) {
			t.Fatalf("Len() changed after Peek: %d != %d", b.Len(), len(data))
		}

		discard := n
		if discard > len(data) {
			discard = len(data)
		}
		b.Discard(discard)
		if b.Len() != len(data)-discard {
			t.Fatalf("Len() after Discard(%d) = %d, want %d", discard, b.Len(), len(data)-discard)
		}
	})
}
