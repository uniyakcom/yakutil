// Package ring 提供自动增长的环形字节缓冲区。
//
// 基于 2^N 容量 + 位掩码实现，支持连续写入、分段读取、
// Peek/Discard、WriteTo 零拷贝输出。
//
// 适用于网络 I/O 读缓冲、流式协议解析等场景。
//
// # 整数溢出安全
//
// r、w、mask 字段使用 uint（平台原生无符号整型），
// 无符号溢出回绕后 w-r 仍能得到正确的字节数，
// 32 位和 64 位系统下持续流场景均无需定期 Reset()。
package ring

import "io"

// Buffer 环形字节缓冲区。非并发安全。
//
// r、w、mask 使用 uint（平台原生无符号类型）——
// 无符号溢出自然回绕，64 位系统需写入 ~18EB 才溢出，
// 32 位系统需写入 4GB 才溢出（而 Go slice 最大 2GB），
// 从而消除持续非空流场景下 Len() 返回负值的问题。
//
// 内存布局：8 个字段合计恰好 64 B（一条 cache line），
// 避免两次 cache-line 读取启动读热路径。
type Buffer struct {
	buf      []byte // 24 B（ptr + len + cap）
	r        uint   // 读位置（逻辑偏移，不 wrap；无符号）
	w        uint   // 写位置（逻辑偏移，不 wrap；无符号）
	mask     uint
	readPrev uint // UnreadByte 回滚点：ReadByte 前的 r 值
	// hasUnread 为 true 时 readPrev 有效；任意 ReadByte 成功时置位。
	// 结构体尾 1B，其余 7B 编译器补齐 → 整体 64 B（1 cache line）。
	hasUnread bool
}

// New 创建容量为 cap 的 Buffer。cap 自动向上取 2 的幂（最小 64）。
func New(cap int) *Buffer {
	sz := 64
	for sz < cap {
		sz <<= 1
	}
	return &Buffer{
		buf:  make([]byte, sz),
		mask: uint(sz) - 1,
	}
}

// Len 返回可读字节数。
// uint 减法在 w >= r 时始终正确（包括回绕场景）。
func (b *Buffer) Len() int { return int(b.w - b.r) }

// Cap 返回底层缓冲区容量。
func (b *Buffer) Cap() int { return len(b.buf) }

// Free 返回可写空间大小。
func (b *Buffer) Free() int { return len(b.buf) - b.Len() }

// Reset 清空缓冲区（不释放内存）。
func (b *Buffer) Reset() { b.r = 0; b.w = 0 } // uint 零值赋值，无需类型转换

// ─── 写操作 ──────────────────────────────────────────────────────────────────

// Write 将 p 写入缓冲区。空间不足时自动扩容。始终返回 len(p), nil。
func (b *Buffer) Write(p []byte) (int, error) {
	b.hasUnread = false // 写操作使 readPrev 失效
	b.grow(len(p))
	n := len(p)
	start := b.w & b.mask
	// 可能需要两段拷贝（wrap around）
	first := copy(b.buf[start:], p)
	if first < n {
		copy(b.buf, p[first:])
	}
	b.w += uint(n)
	return n, nil
}

// WriteByte 写入单个字节。实现 io.ByteWriter 接口。
func (b *Buffer) WriteByte(c byte) error {
	b.hasUnread = false // 写操作使 readPrev 失效
	b.grow(1)
	b.buf[b.w&b.mask] = c
	b.w++ // uint++，回绕安全
	return nil
}

// ─── 读操作 ──────────────────────────────────────────────────────────────────

// ReadByte 读取并消费一个字节。实现 io.ByteReader 接口。
//
// 返回值：(byte, nil) 成功读取；(0, io.EOF) 缓冲区为空。
// 与 Read 相比无需分配 []byte，适合逐字头解析协议头的场景（如 yakio 帧解析）。
//
// 内联安全（无逃逸路径），与 WriteByte 构成对称字节级 I/O。
// 成功调用后可用 UnreadByte 回放一次。
func (b *Buffer) ReadByte() (byte, error) {
	if b.r == b.w {
		b.hasUnread = false
		return 0, io.EOF
	}
	b.readPrev = b.r // 保存回滚点（在规范化之前）
	c := b.buf[b.r&b.mask]
	b.r++
	b.hasUnread = true
	// 缓冲区变空时规范化偏移，与 Read/Discard 保持一致。
	if b.r == b.w {
		b.r = 0
		b.w = 0
	}
	return c, nil
}

// UnreadByte 将上一次 ReadByte 读取的字节放回缓冲区（回滚一个字节）。
//
// 实现 io.ByteScanner，与 ReadByte 配合用于 peek-and-rollback 协议解析，
// 例如"读一字节判断类型，若不符合预期则回放"，无需额外分配缓冲区。
//
// 约束：
//   - 每次成功 ReadByte 后最多调用一次；连续两次调用返回 io.ErrNoProgress。
//   - Write/WriteByte/Read/Discard 之后的状态仍可回滚，调用方负责保证调用顺序一致。
//   - 空缓冲或从未 ReadByte 时返回 io.ErrNoProgress。
func (b *Buffer) UnreadByte() error {
	if !b.hasUnread {
		return io.ErrNoProgress
	}
	b.hasUnread = false
	// 若 ReadByte 触发了规范化（缓冲区恰好读空 → r=0, w=0），
	// 则需同时还原 w；否则只需还原 r。
	if b.r == 0 && b.w == 0 {
		b.r = b.readPrev
		b.w = b.readPrev + 1
	} else {
		b.r = b.readPrev
	}
	return nil
}

// Read 从缓冲区读取数据到 p。返回实际读取的字节数。
func (b *Buffer) Read(p []byte) (int, error) {
	if b.Len() == 0 {
		return 0, io.EOF
	}
	b.hasUnread = false // 批量 Read 使 readPrev 失效
	n := len(p)
	if n > b.Len() {
		n = b.Len()
	}
	start := b.r & b.mask
	first := copy(p, b.buf[start:min(int(start)+n, len(b.buf))])
	if first < n {
		copy(p[first:], b.buf[:n-first])
	}
	b.r += uint(n)
	// 缓冲区为空时规范化偏移，避免 uint 在极端长流场景下逼近溢出边界。
	if b.r == b.w {
		b.r = 0
		b.w = 0
	}
	return n, nil
}

// Peek 查看前 n 个可读字节（不消费）。
//
// 零拷贝情况（数据无 wrap）：返回的切片直接指向内部缓冲区，
// 与 Buffer 共享底层内存。调用方禁止修改返回的切片；
// 任何后续 Write 操作都可能使此切片失效（wrap 后覆盖）。
//
// 有 wrap 情况：返回新分配的连续切片（拷贝）。
//
// n 超过可读量时截断为实际可读数；空缓冲区返回 nil。
func (b *Buffer) Peek(n int) []byte {
	if n > b.Len() {
		n = b.Len()
	}
	if n == 0 {
		return nil
	}
	start := b.r & b.mask
	end := start + uint(n)
	if int(end) <= len(b.buf) {
		return b.buf[start:end]
	}
	// wrap——需要拼接，返回连续拷贝
	tmp := make([]byte, n)
	first := copy(tmp, b.buf[start:])
	copy(tmp[first:], b.buf[:n-first])
	return tmp
}

// PeekSegments 返回可读数据的原始内存段（不消费）。
//
// 若数据不跨边界，s2 为 nil；跨环形边界时返回两段 s1, s2。
// 适用于 writev(2) 零拷贝聚集写。
//
// 调用方禁止修改返回的切片；任何后续 Write 都可能使切片失效。
func (b *Buffer) PeekSegments() (s1, s2 []byte) {
	if b.r == b.w {
		return nil, nil
	}
	start := b.r & b.mask
	end := b.w & b.mask
	if start < end {
		// 数据不跨边界
		return b.buf[start:end], nil
	}
	// 数据跨边界: 两段
	return b.buf[start:len(b.buf)], b.buf[0:end]
}

// Discard 丢弃前 n 个可读字节。
func (b *Buffer) Discard(n int) {
	if n > b.Len() {
		n = b.Len()
	}
	if n > 0 {
		b.hasUnread = false // Discard 使 readPrev 失效
	}
	b.r += uint(n)
	if b.r == b.w {
		b.r = 0
		b.w = 0
	}
}

// ─── I/O 集成 ───────────────────────────────────────────────────────────────

// WriteTo 将缓冲区全部内容写入 w（零拷贝）。
func (b *Buffer) WriteTo(w io.Writer) (int64, error) {
	if b.Len() == 0 {
		return 0, nil
	}
	total := int64(0)
	start := b.r & b.mask
	end := start + uint(b.Len())

	if int(end) <= len(b.buf) {
		n, err := w.Write(b.buf[start:end])
		b.r += uint(n)
		return int64(n), err
	}

	// 两段写
	n, err := w.Write(b.buf[start:])
	total += int64(n)
	b.r += uint(n)
	if err != nil {
		return total, err
	}
	remain := b.Len()
	n, err = w.Write(b.buf[:remain])
	total += int64(n)
	b.r += uint(n)
	return total, err
}

// ReadFrom 从 r 读取数据到缓冲区。
// 每次迭代尽可能填充两个连续段（wrap 场景），减少 Read 系统调用次数。
func (b *Buffer) ReadFrom(r io.Reader) (int64, error) {
	total := int64(0)
	for {
		if b.Free() == 0 {
			b.grow(4096)
		}
		start := b.w & b.mask
		// 第一段：[start, min(start+Free, cap))
		seg1End := start + uint(b.Free())
		if int(seg1End) > len(b.buf) {
			seg1End = uint(len(b.buf))
		}
		n, err := r.Read(b.buf[start:seg1End])
		b.w += uint(n)
		total += int64(n)
		if err != nil {
			if err == io.EOF {
				return total, nil
			}
			return total, err
		}
		// 移除二次读优化：原实现在第一段填满且还有 wrap 空间时立即发起第二次 Read，
		// 对 blocking IO（net.Conn）会阶塞调用方待得延迟升高。
		// 下一课循环会自然填充 wrap 段，语义不变。
	}
}

// ─── 零拷贝读端 ─────────────────────────────────────────────────────────────

// ReadableSegments 暴露读端零拷贝段，供 RESP 解析器等直接消费 ring 内存，
// 消除 "ring.Read(tmp) -> 解析 tmp" 的中间拷贝。
//
// 返回最多 n 个可读字节的一或两段连续内存：
//   - s1：读指针到 buf 末尾（或到写指针）的连续段
//   - s2：环绕时 buf 头部到写指针的段（无环绕时为 nil）
//
// 返回的切片直接指向内部缓冲区，调用方禁止修改；
// 消费 k 字节后必须调用 CommitRead(k) 推进读指针。
func (b *Buffer) ReadableSegments(n int) (s1, s2 []byte) {
	avail := b.Len()
	if n <= 0 || avail == 0 {
		return nil, nil
	}
	if n > avail {
		n = avail
	}
	rStart := int(b.r & b.mask)
	wStart := int(b.w & b.mask)
	firstLen := len(b.buf) - rStart // [rStart, cap) 段长度
	if rStart < wStart {
		// 数据不跨边界：[rStart, wStart)，截取前 n 字节
		return b.buf[rStart : rStart+n], nil
	}
	// 数据跨边界（含满缓冲区 rStart==wStart 场景）
	if n <= firstLen {
		return b.buf[rStart : rStart+n], nil
	}
	s1 = b.buf[rStart:] // 全部第一段
	remain := n - firstLen
	if wStart > 0 {
		if remain > wStart {
			remain = wStart
		}
		s2 = b.buf[:remain]
	}
	return
}

// CommitRead 将读指针前进 n 字节，确认已消费 ReadableSegments 返回的数据。
//
// n 必须 ≥ 0 且 ≤ Len()；违反时 panic，防止读指针超过写指针破坏缓冲区不变量。
// 效果与 Discard(n) 完全等价，名称上与 CommitWrite 对称，
// 明确表达"与 ReadableSegments 配对使用"的语义。
func (b *Buffer) CommitRead(n int) {
	if n < 0 || n > b.Len() {
		panic("ring: CommitRead out of range")
	}
	b.r += uint(n)
	if b.r == b.w {
		b.r = 0
		b.w = 0
	}
}

// ─── 零拷贝写端 ─────────────────────────────────────────────────────────────

// WritableSegments 暴露写端零拷贝段，供 net.Conn.Read 等直接写入 ring，
// 消除 "var tmp [N]byte → ring.Write(tmp[:n])" 的中间拷贝。
//
// 保证 ring 有至少 need 字节可写空间（不足时自动扩容）。
// 返回一或两段连续可写内存：
//   - s1：写指针到 buf 末尾（或到读指针）的连续段
//   - s2：环绕时 buf 头部到读指针的段（无环绕时为 nil）
//
// 调用方向 s1（通常即可覆盖大多数场景）写入 n 字节后，
// 必须调用 CommitWrite(n) 推进写指针，否则数据对读端不可见。
func (b *Buffer) WritableSegments(need int) (s1, s2 []byte) {
	b.grow(need)
	free := b.Free()
	if free == 0 {
		return nil, nil
	}
	wStart := int(b.w & b.mask)
	rStart := int(b.r & b.mask)
	if b.r == b.w {
		// 缓冲区已空（规范化为 r=0,w=0）：整块连续，s2 为 nil
		return b.buf[:free], nil
	}
	if wStart < rStart {
		// 写指针未绕环：[wStart, rStart) 连续可写
		return b.buf[wStart:rStart], nil
	}
	// 写指针已绕至 buf 末尾之后：两段
	s1 = b.buf[wStart:]
	if rStart > 0 {
		s2 = b.buf[:rStart]
	}
	return
}

// CommitWrite 将写指针前进 n 字节，即"确认" WritableSegments 已写入的数据。
//
// n 必须 ≥ 0 且 ≤ Free()（即 WritableSegments 返回的总可写字节数）；
// 违反时 panic，防止写指针越过 buf 末尾或回环到未读数据区域。
func (b *Buffer) CommitWrite(n int) {
	if n < 0 || n > b.Free() {
		panic("ring: CommitWrite out of range")
	}
	b.w += uint(n)
}

// ─── 内部 ────────────────────────────────────────────────────────────────────

func (b *Buffer) grow(need int) {
	if b.Free() >= need {
		return
	}
	newCap := len(b.buf)
	if newCap == 0 {
		// 零值 Buffer（未经 New 构造）首次扩容：初始化为最小容量 64，
		// 避免 newCap <<= 1 在 0 值时永远为 0 的无限循环。
		newCap = 64
	}
	for newCap-b.Len() < need {
		newCap <<= 1
	}
	newBuf := make([]byte, newCap)
	// 拷贝现有数据
	n := b.Len()
	start := b.r & b.mask
	first := copy(newBuf, b.buf[start:min(int(start)+n, len(b.buf))])
	if first < n {
		copy(newBuf[first:], b.buf[:n-first])
	}
	b.buf = newBuf
	b.mask = uint(newCap) - 1
	b.r = 0
	b.w = uint(n)
}
