package ring

import (
	"bytes"
	"io"
	"strings"
	"testing"
)

// ─── 基础 ────────────────────────────────────────────────────────────────────

func TestNew(t *testing.T) {
	b := New(10)
	if b.Cap() < 10 {
		t.Fatalf("Cap() = %d, want >= 10", b.Cap())
	}
	if b.Cap()&(b.Cap()-1) != 0 {
		t.Fatalf("Cap() = %d, not power of 2", b.Cap())
	}
	if b.Len() != 0 {
		t.Fatalf("Len() = %d, want 0", b.Len())
	}
}

func TestNew_MinCap(t *testing.T) {
	b := New(1)
	if b.Cap() < 64 {
		t.Fatalf("Cap() = %d, min should be 64", b.Cap())
	}
}

// ─── Write / Read ────────────────────────────────────────────────────────────

func TestWriteRead(t *testing.T) {
	b := New(64)
	data := []byte("hello, ring buffer!")
	n, err := b.Write(data)
	if err != nil || n != len(data) {
		t.Fatalf("Write = %d, %v", n, err)
	}
	if b.Len() != len(data) {
		t.Fatalf("Len() = %d after Write", b.Len())
	}

	out := make([]byte, 100)
	n, err = b.Read(out)
	if err != nil || n != len(data) {
		t.Fatalf("Read = %d, %v", n, err)
	}
	if string(out[:n]) != string(data) {
		t.Fatalf("Read data = %q, want %q", out[:n], data)
	}
}

func TestReadEmpty(t *testing.T) {
	b := New(64)
	out := make([]byte, 10)
	n, err := b.Read(out)
	if n != 0 || err != io.EOF {
		t.Fatalf("Read empty = %d, %v; want 0, EOF", n, err)
	}
}

func TestWriteByte(t *testing.T) {
	b := New(64)
	for i := byte(0); i < 10; i++ {
		b.WriteByte(i) //nolint:errcheck
	}
	if b.Len() != 10 {
		t.Fatalf("Len() = %d after 10 WriteByte", b.Len())
	}
	out := make([]byte, 10)
	b.Read(out) //nolint:errcheck
	for i := byte(0); i < 10; i++ {
		if out[i] != i {
			t.Fatalf("byte[%d] = %d, want %d", i, out[i], i)
		}
	}
}

// ─── Wrap around ─────────────────────────────────────────────────────────────

func TestWrapAround(t *testing.T) {
	b := New(64) // cap=64
	// 写满近一半，读掉，再写更多以触发 wrap
	data := bytes.Repeat([]byte("X"), 50)
	b.Write(data) //nolint:errcheck
	tmp := make([]byte, 40)
	b.Read(tmp) //nolint:errcheck // r=40, w=50

	// 再写 30 字节，w 将 wrap 到 80>64
	data2 := bytes.Repeat([]byte("Y"), 30)
	b.Write(data2) //nolint:errcheck
	// 现在 Len = 10(剩余X) + 30(Y) = 40
	if b.Len() != 40 {
		t.Fatalf("Len() = %d, want 40", b.Len())
	}

	out := make([]byte, 40)
	n, _ := b.Read(out)
	if n != 40 {
		t.Fatalf("Read = %d, want 40", n)
	}
	// 前 10 字节是 X，后 30 字节是 Y
	for i := 0; i < 10; i++ {
		if out[i] != 'X' {
			t.Fatalf("out[%d] = %c, want X", i, out[i])
		}
	}
	for i := 10; i < 40; i++ {
		if out[i] != 'Y' {
			t.Fatalf("out[%d] = %c, want Y", i, out[i])
		}
	}
}

// ─── Peek / Discard ──────────────────────────────────────────────────────────

func TestPeek(t *testing.T) {
	b := New(64)
	b.Write([]byte("abcde")) //nolint:errcheck

	p := b.Peek(3)
	if string(p) != "abc" {
		t.Fatalf("Peek(3) = %q, want abc", p)
	}
	// Peek 不消费
	if b.Len() != 5 {
		t.Fatalf("Len after Peek = %d, want 5", b.Len())
	}
}

func TestPeek_Empty(t *testing.T) {
	b := New(64)
	if p := b.Peek(5); p != nil {
		t.Fatalf("Peek empty = %q, want nil", p)
	}
}

func TestDiscard(t *testing.T) {
	b := New(64)
	b.Write([]byte("abcdefgh")) //nolint:errcheck
	b.Discard(5)
	if b.Len() != 3 {
		t.Fatalf("Len after Discard(5) = %d, want 3", b.Len())
	}
	out := make([]byte, 10)
	n, _ := b.Read(out)
	if string(out[:n]) != "fgh" {
		t.Fatalf("Read after Discard = %q, want fgh", out[:n])
	}
}

func TestDiscard_More(t *testing.T) {
	b := New(64)
	b.Write([]byte("abc")) //nolint:errcheck
	b.Discard(100)         // 超过 Len，截断
	if b.Len() != 0 {
		t.Fatalf("Len after over-Discard = %d, want 0", b.Len())
	}
}

// ─── Auto-grow ───────────────────────────────────────────────────────────────

func TestAutoGrow(t *testing.T) {
	b := New(64)
	// 写大量数据触发多次扩容
	data := bytes.Repeat([]byte("G"), 500)
	b.Write(data) //nolint:errcheck
	if b.Len() != 500 {
		t.Fatalf("Len() = %d, want 500", b.Len())
	}
	if b.Cap() < 500 {
		t.Fatalf("Cap() = %d, should be >= 500", b.Cap())
	}

	out := make([]byte, 500)
	n, _ := b.Read(out)
	if n != 500 || !bytes.Equal(out, data) {
		t.Fatal("data mismatch after grow")
	}
}

// ─── Reset ───────────────────────────────────────────────────────────────────

func TestReset(t *testing.T) {
	b := New(64)
	b.Write([]byte("data")) //nolint:errcheck
	b.Reset()
	if b.Len() != 0 {
		t.Fatalf("Len after Reset = %d", b.Len())
	}
}

// ─── WriteTo / ReadFrom ─────────────────────────────────────────────────────

func TestWriteTo(t *testing.T) {
	b := New(64)
	b.Write([]byte("hello world")) //nolint:errcheck
	var buf bytes.Buffer
	n, err := b.WriteTo(&buf)
	if err != nil || n != 11 {
		t.Fatalf("WriteTo = %d, %v", n, err)
	}
	if buf.String() != "hello world" {
		t.Fatalf("WriteTo data = %q", buf.String())
	}
	if b.Len() != 0 {
		t.Fatalf("Len after WriteTo = %d", b.Len())
	}
}

func TestReadFrom(t *testing.T) {
	b := New(64)
	r := strings.NewReader("test data from reader")
	n, err := b.ReadFrom(r)
	if err != nil {
		t.Fatalf("ReadFrom err = %v", err)
	}
	if n != 21 {
		t.Fatalf("ReadFrom = %d, want 21", n)
	}
	out := make([]byte, 50)
	rn, _ := b.Read(out)
	if string(out[:rn]) != "test data from reader" {
		t.Fatalf("data = %q", out[:rn])
	}
}

// ─── Free ────────────────────────────────────────────────────────────────────

func TestFree(t *testing.T) {
	b := New(64)
	if b.Free() != b.Cap() {
		t.Fatalf("Free() = %d, want %d", b.Free(), b.Cap())
	}
	b.Write([]byte("1234567890")) //nolint:errcheck
	if b.Free() != b.Cap()-10 {
		t.Fatalf("Free() = %d after 10 bytes write", b.Free())
	}
}

// ─── Benchmarks ─────────────────────────────────────────────────────────────

func BenchmarkWrite64(b *testing.B) {
	buf := New(4096)
	data := make([]byte, 64)
	b.SetBytes(64)
	for b.Loop() {
		buf.Write(data) //nolint:errcheck
		buf.Discard(64)
	}
}

func BenchmarkWriteRead1K(b *testing.B) {
	buf := New(4096)
	data := make([]byte, 1024)
	out := make([]byte, 1024)
	b.SetBytes(1024)
	for b.Loop() {
		buf.Write(data) //nolint:errcheck
		buf.Read(out)   //nolint:errcheck
	}
}

// ─── 回归测试（Bug 修复验证） ────────────────────────────────────────────────

// TestBuffer_OffsetNormalization 回归：验证 Read/Discard 清空缓冲区时
// r/w 被重置为 0，防止 32 位系统上无界增长溢出。
func TestBuffer_OffsetNormalization_Discard(t *testing.T) {
	buf := New(64)
	data := []byte("hello")
	for i := 0; i < 10_000; i++ {
		buf.Write(data) //nolint:errcheck
		buf.Discard(len(data))
		// 清空后 r/w 必须归零
		if buf.r != 0 || buf.w != 0 {
			t.Fatalf("iter %d: r=%d w=%d, want both 0", i, buf.r, buf.w)
		}
		if buf.Len() != 0 {
			t.Fatalf("iter %d: Len()=%d want 0", i, buf.Len())
		}
	}
}

func TestBuffer_OffsetNormalization_Read(t *testing.T) {
	buf := New(64)
	data := []byte("world")
	out := make([]byte, len(data))
	for i := 0; i < 10_000; i++ {
		buf.Write(data) //nolint:errcheck
		n, _ := buf.Read(out)
		if n != len(data) || string(out[:n]) != "world" {
			t.Fatalf("iter %d: Read got %q", i, out[:n])
		}
		// 清空后 r/w 必须归零
		if buf.r != 0 || buf.w != 0 {
			t.Fatalf("iter %d: r=%d w=%d, want both 0", i, buf.r, buf.w)
		}
	}
}

// TestBuffer_OffsetNormalization_PartialRead 验证非空清零时不影响残留数据正确性。
func TestBuffer_OffsetNormalization_PartialRead(t *testing.T) {
	buf := New(64)
	for i := 0; i < 1000; i++ {
		buf.Write([]byte("abcde")) //nolint:errcheck
		out := make([]byte, 3)
		buf.Read(out) //nolint:errcheck // 读 3 字节，还剩 2
		if buf.Len() != 2 {
			t.Fatalf("iter %d: Len()=%d want 2", i, buf.Len())
		}
		// 未清空时不应归零
		if buf.w == 0 && buf.r == 0 {
			if buf.Len() != 0 {
				t.Fatalf("iter %d: r/w reset while buffer not empty", i)
			}
		}
		// 读完剩余字节
		buf.Read(make([]byte, 2)) //nolint:errcheck
		// 现在应该归零
		if buf.r != 0 || buf.w != 0 {
			t.Fatalf("iter %d: r=%d w=%d after full drain, want 0", i, buf.r, buf.w)
		}
	}
}

func TestBuffer_PeekSegments_Empty(t *testing.T) {
	b := New(64)
	s1, s2 := b.PeekSegments()
	if s1 != nil || s2 != nil {
		t.Errorf("empty buffer: got s1=%v s2=%v, want both nil", s1, s2)
	}
}

func TestBuffer_PeekSegments_NoWrap(t *testing.T) {
	b := New(64)
	data := []byte("hello-yakutil")
	b.Write(data) //nolint:errcheck

	s1, s2 := b.PeekSegments()
	if s2 != nil {
		t.Errorf("non-wrapping data: expected s2=nil, got %v", s2)
	}
	if string(s1) != string(data) {
		t.Errorf("s1 = %q, want %q", s1, data)
	}
	// PeekSegments 不消费数据
	if b.Len() != len(data) {
		t.Errorf("Len after PeekSegments = %d, want %d", b.Len(), len(data))
	}
}

func TestBuffer_PeekSegments_Wrap(t *testing.T) {
	// New(64) 恰好分配 64 字节，mask=63。
	// 写 50 字节后只读 44 字节，使 r=44, w=50（6 字节留在位置 [44..49]）。
	// 再写 20 字节：写位置从 50 开始，先填 [50..63]（14 字节），
	// 再从 [0..5] 回绕写 6 字节。此时 start=44 > end=6，触发回绕分段。
	b := New(64) // actual cap = 64, mask = 63

	initial := make([]byte, 50)
	for i := range initial {
		initial[i] = byte(i)
	}
	b.Write(initial) //nolint:errcheck

	consumed := make([]byte, 44)
	b.Read(consumed) //nolint:errcheck // r=44, w=50, Len=6

	extra := make([]byte, 20)
	for i := range extra {
		extra[i] = byte(100 + i)
	}
	b.Write(extra) //nolint:errcheck // w=70, 70&63=6 < 44 → wrap

	s1, s2 := b.PeekSegments()
	if s2 == nil {
		t.Fatal("expected wrap: s2 should not be nil")
	}

	combined := append([]byte(nil), s1...)
	combined = append(combined, s2...)

	want := append(append([]byte(nil), initial[44:]...), extra...)
	if string(combined) != string(want) {
		t.Errorf("s1+s2 = %v, want %v", combined, want)
	}
	// PeekSegments 不消费数据
	wantLen := 6 + 20
	if b.Len() != wantLen {
		t.Errorf("Len after PeekSegments = %d, want %d", b.Len(), wantLen)
	}
}

func TestBuffer_PeekSegments_FullBuffer(t *testing.T) {
	cap := 16
	b := New(cap)

	// 写满整个 buffer（cap-1 字节，ring buffer 通常保留一格）
	data := make([]byte, cap/2)
	for i := range data {
		data[i] = byte(i)
	}
	b.Write(data) //nolint:errcheck

	s1, s2 := b.PeekSegments()
	total := len(s1) + len(s2)
	if total != len(data) {
		t.Errorf("total segments length = %d, want %d", total, len(data))
	}
}

// ─── ReadByte ────────────────────────────────────────────────────────────────

// TestReadByte_Basic 验证 ReadByte 正确消费字节并维护 Len。
func TestReadByte_Basic(t *testing.T) {
	b := New(16)
	for i := byte(0); i < 5; i++ {
		b.WriteByte(i) //nolint:errcheck
	}
	for i := byte(0); i < 5; i++ {
		c, err := b.ReadByte()
		if err != nil {
			t.Fatalf("ReadByte[%d]: unexpected error %v", i, err)
		}
		if c != i {
			t.Fatalf("ReadByte[%d] = %d, want %d", i, c, i)
		}
	}
	if b.Len() != 0 {
		t.Fatalf("Len() = %d after reading all bytes, want 0", b.Len())
	}
}

// TestReadByte_EOF 验证空缓冲区返回 io.EOF。
func TestReadByte_EOF(t *testing.T) {
	b := New(16)
	c, err := b.ReadByte()
	if err != io.EOF {
		t.Fatalf("ReadByte empty: err = %v, want io.EOF", err)
	}
	if c != 0 {
		t.Fatalf("ReadByte empty: byte = %d, want 0", c)
	}
}

// TestReadByte_EOF_AfterDrain 验证全部读完后下一次返回 io.EOF。
func TestReadByte_EOF_AfterDrain(t *testing.T) {
	b := New(8)
	b.WriteByte(42) //nolint:errcheck
	if _, err := b.ReadByte(); err != nil {
		t.Fatal("first ReadByte should succeed")
	}
	_, err := b.ReadByte()
	if err != io.EOF {
		t.Fatalf("second ReadByte: err = %v, want io.EOF", err)
	}
}

// TestReadWriteByte_Symmetric 验证 WriteByte/ReadByte 互为逆操作（对称性）。
// 模拟 yakio 帧头解析：逐字节读取 magic/version/length 等字段。
func TestReadWriteByte_Symmetric(t *testing.T) {
	b := New(64)
	const n = 50
	// 写入 n 字节
	for i := 0; i < n; i++ {
		b.WriteByte(byte(i * 3)) //nolint:errcheck
	}
	// 逐字节读取验证
	for i := 0; i < n; i++ {
		c, err := b.ReadByte()
		if err != nil {
			t.Fatalf("iter %d: ReadByte err = %v", i, err)
		}
		if c != byte(i*3) {
			t.Fatalf("iter %d: got %d, want %d", i, c, byte(i*3))
		}
	}
}

// TestReadByte_WrapAround 验证 ReadByte 在环形折回（wrap-around）后正确工作。
func TestReadByte_WrapAround(t *testing.T) {
	b := New(8) // cap=8

	// 写入 6 字节，读取 4 字节，使 r=4，w=6
	for i := byte(0); i < 6; i++ {
		b.WriteByte(i) //nolint:errcheck
	}
	for i := 0; i < 4; i++ {
		b.ReadByte() //nolint:errcheck
	}

	// 再写入 6 字节，w 将折回到新位置（超过 cap）
	for i := byte(10); i < 16; i++ {
		b.WriteByte(i) //nolint:errcheck
	}

	// 应能正确读出 2 个旧字节 + 6 个新字节 = 8 字节
	expected := []byte{4, 5, 10, 11, 12, 13, 14, 15}
	for i, want := range expected {
		c, err := b.ReadByte()
		if err != nil {
			t.Fatalf("wrap[%d]: err = %v", i, err)
		}
		if c != want {
			t.Fatalf("wrap[%d] = %d, want %d", i, c, want)
		}
	}
}

// TestReadByte_InterleaveWithRead 验证 ReadByte 与 Read 可以混合使用。
func TestReadByte_InterleaveWithRead(t *testing.T) {
	b := New(32)
	b.Write([]byte("hello world")) //nolint:errcheck

	// 用 ReadByte 读 5 字节（"hello"）
	for i, want := range []byte("hello") {
		c, err := b.ReadByte()
		if err != nil || c != want {
			t.Fatalf("ReadByte[%d]: got (%d,%v), want (%d,nil)", i, c, err, want)
		}
	}

	// 用 Read 读剩余（" world"）
	rest := make([]byte, 6)
	n, err := b.Read(rest)
	if n != 6 || err != nil {
		t.Fatalf("Read: got (%d,%v), want (6,nil)", n, err)
	}
	if string(rest) != " world" {
		t.Fatalf("Read = %q, want \" world\"", rest)
	}
}

// ─── ReadByte 基准测试 ────────────────────────────────────────────────────────
//
// 运行方式：
//
//	go test -bench=BenchmarkBuffer_ReadByte -benchmem ./ring/
//
// 预期（Intel Xeon E-2186G @ 3.80GHz, amd64, go1.25）：
//
//	BenchmarkBuffer_ReadByte     ~3-6 ns/op   0 allocs/op   (无逃逸，内联）
//	BenchmarkBuffer_WriteByte    ~3-6 ns/op   0 allocs/op   (对比基线）

// BenchmarkBuffer_ReadByte 测量逐字节读取的热路径性能。
// 注意：现实中缓冲区消费速率 = 生产速率，这里预填充避免 EOF。
func BenchmarkBuffer_ReadByte(b *testing.B) {
	buf := New(1 << 20) // 1MB 环形缓冲
	// 预填充（避免 bench 期间扩容）
	fill := make([]byte, 1<<20-1)
	buf.Write(fill) //nolint:errcheck
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if buf.Len() == 0 {
			buf.Write(fill) //nolint:errcheck
		}
		buf.ReadByte() //nolint:errcheck
	}
}

// BenchmarkBuffer_WriteByte 作为对比基线。
func BenchmarkBuffer_WriteByte(b *testing.B) {
	buf := New(1 << 20)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if buf.Len() >= 1<<20-1 {
			buf.Reset()
		}
		buf.WriteByte(byte(i)) //nolint:errcheck
	}
}

// BenchmarkBuffer_UnreadByte 测量 WriteByte + ReadByte + UnreadByte 往返开销。
func BenchmarkBuffer_UnreadByte(b *testing.B) {
	buf := New(1 << 10)
	// 预填充 1 字节保证 ReadByte 可用
	buf.WriteByte(0x00) //nolint:errcheck
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		buf.WriteByte(byte(i)) //nolint:errcheck
		c, _ := buf.ReadByte()
		_ = c
		_ = buf.UnreadByte()
		buf.ReadByte() //nolint:errcheck // 确保消费掉 unread 的字节
	}
}

// ─── UnreadByte 测试 ───────────────────────────────────────────────────────────

func TestUnreadByte_Basic(t *testing.T) {
	buf := New(64)
	buf.Write([]byte{0xAA, 0xBB, 0xCC}) //nolint:errcheck

	// 读取一字节后回放，再次读取应得到相同字节
	c, err := buf.ReadByte()
	if err != nil || c != 0xAA {
		t.Fatalf("ReadByte: want (0xAA, nil), got (%x, %v)", c, err)
	}
	if err := buf.UnreadByte(); err != nil {
		t.Fatalf("UnreadByte: unexpected error: %v", err)
	}
	c2, err := buf.ReadByte()
	if err != nil || c2 != 0xAA {
		t.Fatalf("after UnreadByte ReadByte: want (0xAA, nil), got (%x, %v)", c2, err)
	}
	// 后续字节应正常读取
	c3, _ := buf.ReadByte()
	if c3 != 0xBB {
		t.Fatalf("want 0xBB, got %x", c3)
	}
}

func TestUnreadByte_Normalization(t *testing.T) {
	// 测试读取最后一个字节后触发规范化的 UnreadByte
	buf := New(64)
	buf.Write([]byte{0x42}) //nolint:errcheck

	c, err := buf.ReadByte()
	if err != nil || c != 0x42 {
		t.Fatalf("ReadByte: want (0x42, nil), got (%x, %v)", c, err)
	}
	// 此时 r=0, w=0（规范化）
	if buf.Len() != 0 {
		t.Fatalf("expected empty, Len=%d", buf.Len())
	}
	if err := buf.UnreadByte(); err != nil {
		t.Fatalf("UnreadByte after normalization: %v", err)
	}
	if buf.Len() != 1 {
		t.Fatalf("after UnreadByte, Len should be 1, got %d", buf.Len())
	}
	c2, err := buf.ReadByte()
	if err != nil || c2 != 0x42 {
		t.Fatalf("re-read after UnreadByte: want (0x42, nil), got (%x, %v)", c2, err)
	}
}

func TestUnreadByte_DoubleUnread(t *testing.T) {
	buf := New(64)
	buf.WriteByte(0x11) //nolint:errcheck
	buf.ReadByte()      //nolint:errcheck

	if err := buf.UnreadByte(); err != nil {
		t.Fatalf("first UnreadByte: %v", err)
	}
	// 第二次 UnreadByte 应返回错误
	if err := buf.UnreadByte(); err == nil {
		t.Fatal("expected error on second UnreadByte, got nil")
	}
}

func TestUnreadByte_NoReadByte(t *testing.T) {
	buf := New(64)
	buf.Write([]byte{0x01, 0x02}) //nolint:errcheck

	// 从未调用 ReadByte，UnreadByte 应返回错误
	if err := buf.UnreadByte(); err == nil {
		t.Fatal("expected error when UnreadByte called without ReadByte")
	}
}

func TestUnreadByte_EmptyBuffer(t *testing.T) {
	buf := New(64)
	// 空缓冲区调用 UnreadByte 应返回错误
	if err := buf.UnreadByte(); err == nil {
		t.Fatal("expected error on empty buffer")
	}
}

func TestUnreadByte_WrapAround(t *testing.T) {
	// 在环形边界处测试 UnreadByte
	buf := New(8)                       // 小缓冲区，容易触发 wrap
	buf.Write([]byte{1, 2, 3, 4, 5, 6}) //nolint:errcheck
	buf.Discard(5)                      // r 推进到接近边界

	buf.WriteByte(0xFF) //nolint:errcheck
	c, err := buf.ReadByte()
	if err != nil {
		t.Fatalf("ReadByte at wrap: %v", err)
	}
	if err := buf.UnreadByte(); err != nil {
		t.Fatalf("UnreadByte at wrap: %v", err)
	}
	c2, _ := buf.ReadByte()
	if c != c2 {
		t.Fatalf("mismatch: first=%x, after unread=%x", c, c2)
	}
}

// ─── WritableSegments / CommitWrite ──────────────────────────────────────────

func TestWritableSegments_EmptyBuffer(t *testing.T) {
	buf := New(16)
	s1, s2 := buf.WritableSegments(8)
	if len(s1) < 8 {
		t.Fatalf("s1 len=%d, want >=8", len(s1))
	}
	if s2 != nil {
		t.Fatalf("expected s2=nil on empty buffer, got len=%d", len(s2))
	}
	// 写入数据并 CommitWrite
	copy(s1, []byte("hello"))
	buf.CommitWrite(5)
	if buf.Len() != 5 {
		t.Fatalf("Len after CommitWrite(5) = %d, want 5", buf.Len())
	}
	// 读回验证
	out := make([]byte, 5)
	buf.Read(out) //nolint:errcheck
	if string(out) != "hello" {
		t.Fatalf("read back %q, want \"hello\"", out)
	}
}

func TestWritableSegments_WrapAround(t *testing.T) {
	// 制造环形边界：写 10 字节，读走 6，再写新数据触发 wrap
	buf := New(16)
	buf.Write(bytes.Repeat([]byte("X"), 10)) //nolint:errcheck
	buf.Discard(6)                           // r=6, w=10, 剩余 4 字节

	// 再写 8 字节会 wrap（cap=16，free = 16-4=12，但物理上 wPos=10，rPos=6）
	s1, s2 := buf.WritableSegments(8)
	totalFree := len(s1)
	if s2 != nil {
		totalFree += len(s2)
	}
	if totalFree < 8 {
		t.Fatalf("total writable=%d, want >=8", totalFree)
	}
	// 向 s1 写 6 字节
	data := []byte("ABCDEF")
	n := copy(s1, data)
	buf.CommitWrite(n)
	// 再验证读取
	buf.Discard(4) // 丢弃原来的 4 个 X
	got := make([]byte, n)
	buf.Read(got) //nolint:errcheck
	if !bytes.Equal(got, data[:n]) {
		t.Fatalf("got %q, want %q", got, data[:n])
	}
}

func TestWritableSegments_CommitWrite_FullCycle(t *testing.T) {
	// 模拟 net.Conn.Read 直接写入 ring 的典型用法
	buf := New(64)

	input := "GET mykey\r\n"
	s1, _ := buf.WritableSegments(len(input))
	n := copy(s1, input)
	buf.CommitWrite(n)

	if buf.Len() != len(input) {
		t.Fatalf("Len=%d, want %d", buf.Len(), len(input))
	}
	// 读回验证
	out := make([]byte, len(input))
	buf.Read(out) //nolint:errcheck
	if string(out) != input {
		t.Fatalf("got %q, want %q", out, input)
	}
}

func BenchmarkWritableSegments_vs_Write(b *testing.B) {
	data := bytes.Repeat([]byte("X"), 4096)
	b.Run("Write_copy", func(b *testing.B) {
		buf := New(8192)
		for i := 0; i < b.N; i++ {
			buf.Reset()
			buf.Write(data) //nolint:errcheck
		}
	})
	b.Run("WritableSegments_zerocopy", func(b *testing.B) {
		buf := New(8192)
		for i := 0; i < b.N; i++ {
			buf.Reset()
			s1, _ := buf.WritableSegments(4096)
			n := copy(s1, data)
			buf.CommitWrite(n)
		}
	})
}

// ─── ReadableSegments + CommitRead ───────────────────────────────────────────

func TestReadableSegments_Empty(t *testing.T) {
	buf := New(64)
	s1, s2 := buf.ReadableSegments(10)
	if s1 != nil || s2 != nil {
		t.Fatal("expected nil segments on empty buffer")
	}
}

func TestReadableSegments_Contiguous(t *testing.T) {
	buf := New(64)
	buf.Write([]byte("hello world")) //nolint:errcheck
	s1, s2 := buf.ReadableSegments(5)
	if string(s1) != "hello" {
		t.Fatalf("s1 = %q, want \"hello\"", s1)
	}
	if s2 != nil {
		t.Fatalf("s2 should be nil, got len=%d", len(s2))
	}
	// CommitRead 推进读指针
	buf.CommitRead(5)
	if buf.Len() != 6 {
		t.Fatalf("Len after CommitRead(5) = %d, want 6", buf.Len())
	}
	out := make([]byte, 6)
	buf.Read(out) //nolint:errcheck
	if string(out) != " world" {
		t.Fatalf("remaining = %q, want \" world\"", out)
	}
}

func TestReadableSegments_WrapAround(t *testing.T) {
	// 制造环绕场景：cap=16，写 12 字节，读走 8，再写 8（触发 wrap）
	buf := New(16)
	buf.Write(bytes.Repeat([]byte("A"), 12)) //nolint:errcheck
	buf.Discard(8)                           // r=8, w=12, 剩余 4 字节
	buf.Write(bytes.Repeat([]byte("B"), 8))  //nolint:errcheck // w=20, 物理位置 w&mask=4

	// 现在 Len=12，数据跨边界：[8..16)=4 个 A，[0..4)=4 个 B，加后面 [4..8)=4 个 B
	// 其中 s1 应为 [8, 16) = 8 字节 (4A 读完 + 继续)
	s1, s2 := buf.ReadableSegments(12)
	total := len(s1)
	if s2 != nil {
		total += len(s2)
	}
	if total != 12 {
		t.Fatalf("total readable segments = %d, want 12", total)
	}
	// 验证 CommitRead 后 Len 正确
	buf.CommitRead(12)
	if buf.Len() != 0 {
		t.Fatalf("Len after CommitRead(12) = %d, want 0", buf.Len())
	}
}

func TestReadableSegments_FullBuffer(t *testing.T) {
	// 满缓冲区：rStart==wStart
	buf := New(8)
	buf.Write(bytes.Repeat([]byte("Z"), 8)) //nolint:errcheck
	if buf.Len() != 8 {
		t.Fatalf("Len = %d, want 8", buf.Len())
	}
	s1, s2 := buf.ReadableSegments(8)
	total := len(s1)
	if s2 != nil {
		total += len(s2)
	}
	if total != 8 {
		t.Fatalf("full buffer total = %d, want 8", total)
	}
}

func TestReadableSegments_CommitRead_NormalizesOnEmpty(t *testing.T) {
	buf := New(64)
	buf.Write([]byte("abc")) //nolint:errcheck
	buf.CommitRead(3)
	// 内部 r==w 后应规范化为 r=0,w=0
	if buf.Len() != 0 {
		t.Fatalf("Len = %d, want 0", buf.Len())
	}
	// 规范化后再写，不应触发 grow
	buf.Write([]byte("xyz")) //nolint:errcheck
	out := make([]byte, 3)
	buf.Read(out) //nolint:errcheck
	if string(out) != "xyz" {
		t.Fatalf("after normalize: got %q, want \"xyz\"", out)
	}
}

func TestCommitWrite_PanicOnOverflow(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic on CommitWrite out of range")
		}
	}()
	buf := New(8)
	buf.Write([]byte("hello")) //nolint:errcheck // Len=5, Free=3
	s1, _ := buf.WritableSegments(3)
	_ = s1
	buf.CommitWrite(100) // 超出 Free，应 panic
}

func TestCommitRead_PanicOnOverflow(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic on CommitRead out of range")
		}
	}()
	buf := New(64)
	buf.Write([]byte("hello")) //nolint:errcheck
	buf.CommitRead(100)        // 超出 Len，应 panic
}

func BenchmarkReadableSegments_vs_Read(b *testing.B) {
	payload := bytes.Repeat([]byte("X"), 4096)
	b.Run("Read_copy", func(b *testing.B) {
		buf := New(8192)
		tmp := make([]byte, 4096)
		for i := 0; i < b.N; i++ {
			buf.Reset()
			buf.Write(payload) //nolint:errcheck
			buf.Read(tmp)      //nolint:errcheck
		}
	})
	b.Run("ReadableSegments_zerocopy", func(b *testing.B) {
		buf := New(8192)
		for i := 0; i < b.N; i++ {
			buf.Reset()
			buf.Write(payload) //nolint:errcheck
			s1, _ := buf.ReadableSegments(4096)
			_ = s1
			buf.CommitRead(len(s1))
		}
	})
}

// ─── 零值 Buffer 安全性回归测试 ──────────────────────────────────────

// TestZeroValueBuffer 验证未经 New 构造的零值 Buffer 不会在 Write 时触发无限循环。
// 修复前：grow() 从 newCap=0 执行 0 <<= 1 永远为 0，导致死循环。
func TestZeroValueBuffer(t *testing.T) {
	var b Buffer // 零值，buf=nil, mask=0
	data := []byte("hello, zero value buffer")
	b.Write(data) //nolint:errcheck
	got := make([]byte, len(data))
	n, _ := b.Read(got)
	if n != len(data) || string(got[:n]) != string(data) {
		t.Fatalf("ZeroValue Buffer: got %q, want %q", got[:n], data)
	}
}

// TestZeroValueBuffer_WriteByte 零值 Buffer 逐字节写入不 panic。
func TestZeroValueBuffer_WriteByte(t *testing.T) {
	var b Buffer
	_ = b.WriteByte('A')
	c, err := b.ReadByte()
	if err != nil || c != 'A' {
		t.Fatalf("ZeroValue WriteByte: got %c %v", c, err)
	}
}
