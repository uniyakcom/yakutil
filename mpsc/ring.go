// Package mpsc 提供多生产者单消费者（MPSC）无锁环形缓冲区。
//
// 设计用于 Group Commit 模式：
//   - 多写者并发 Enqueue → 单消费者 Drain 批量收割 → Commit 唤醒
//   - 生产者阻塞等待消费者确认（可携带 error 反馈）
//
// 状态机：
//
//	free(0) → filling(1) → ready(2) → drained(3) → free(0)
//
// 内存布局：head/tail 分处不同 cache line，消除 false sharing。
package mpsc

import (
	"context"
	"runtime"
	"sync/atomic"

	yakutil "github.com/uniyakcom/yakutil"
	"github.com/uniyakcom/yakutil/backoff"
)

// cacheLine 缓存行大小，引用根包常量确保全库一致性。
const cacheLine = yakutil.CacheLine

// ─── slot ────────────────────────────────────────────────────────────────────

type slot[T any] struct {
	state atomic.Uint32 // 0=free 1=filling 2=ready 3=drained
	val   T
	err   error
	done  chan struct{} // 已完成信号（buffer=1，Commit 发送，Wait 接收）
}

const (
	sFree    = 0
	sFill    = 1
	sReady   = 2
	sDrained = 3

	// spinWaitN：Wait 轮询次数上限，每次 Gosched() 约 1-5 µs；
	// 8 次覆盖典型消费者处理延迟，超出则 park goroutine。
	spinWaitN = 8
)

// ─── Ring ────────────────────────────────────────────────────────────────────

// Ring MPSC 无锁环形缓冲区（泛型）。
type Ring[T any] struct {
	slots []slot[T]
	mask  uint64

	_    [cacheLine]byte
	tail atomic.Uint64
	_    [cacheLine - 8]byte
	head atomic.Uint64
	_    [cacheLine - 8]byte
}

// New 创建容量为 size 的 Ring。size 自动向上取 2 的幂。
func New[T any](size int) *Ring[T] {
	sz := 1
	for sz < size {
		sz <<= 1
	}
	if sz < 4 {
		sz = 4
	}
	r := &Ring[T]{
		slots: make([]slot[T], sz),
		mask:  uint64(sz - 1),
	}
	for i := range r.slots {
		r.slots[i].done = make(chan struct{}, 1)
	}
	return r
}

// Enqueue 生产者提交一个值到 Ring。
// 返回 (seq, true)；Ring 满时返回 (0, false)。
// 成功后需调用 Wait(seq) 等待消费者处理完毕。
func (r *Ring[T]) Enqueue(val T) (uint64, bool) {
	for spin := 0; ; spin++ {
		t := r.tail.Load()
		h := r.head.Load()
		if t-h >= uint64(len(r.slots)) {
			if spin > 64 {
				return 0, false
			}
			runtime.Gosched()
			continue
		}
		if !r.tail.CompareAndSwap(t, t+1) {
			continue
		}
		idx := t & r.mask
		s := &r.slots[idx]
		var slotBo backoff.Backoff
		for s.state.Load() != sFree {
			slotBo.Spin()
		}
		s.state.Store(sFill)
		s.val = val
		s.err = nil
		// 排空上一轮可能残留的信号（防御性：正常用法下 Wait 已消费）
		select {
		case <-s.done:
		default:
		}
		s.state.Store(sReady)
		return t, true
	}
}

// Wait 生产者阻塞等待 seq 对应的 slot 被消费者处理完毕。
// 返回消费者回写的 error。调用后 slot 被释放（可重用）。
//
// 实现说明：采用 "spin-then-park" 策略：
//   - 先以 runtime.Gosched() 快速轮询 done channel（最多 spinWaitN 次），
//     覆盖消费者已在另一核心运行的常见情况（避免 goroutine park/unpark）。
//   - 若轮询期间未收到信号，再 park 到 channel 上（不影响单 goroutine 路径）。
//
// 若消费者可能永久挂起，请使用 WaitContext。
func (r *Ring[T]) Wait(seq uint64) error {
	idx := seq & r.mask
	s := &r.slots[idx]
	// 快速路径：已完成（单 goroutine 场景 Commit 先于 Wait）
	select {
	case <-s.done:
		goto done
	default:
	}
	// spin-then-park：多 goroutine 生产者场景；消费者若在其他核运行，
	// 几次 Gosched 内即可完成 Drain+Commit，避免 park 开销。
	for i := 0; i < spinWaitN; i++ {
		runtime.Gosched()
		select {
		case <-s.done:
			goto done
		default:
		}
	}
	<-s.done // 确实需要 park
done:
	err := s.err
	// 清零 val 和 err，防止含指针的 T 被 slot 持有导致 GC 泄漏
	var zero T
	s.val = zero
	s.err = nil
	s.state.Store(sFree)
	return err
}

// WaitContext 与 Wait 功能相同，但支持 context 取消/超时。
//
// 返回值：
//   - nil：消费者正常提交
//   - ctx.Err()：context 取消或超时
//   - 其他：消费者通过 Commit(batchErr) 回写的业务错误
//
// 并发安全说明：ctx 取消时，若消费者尚未调用 Commit，WaitContext 会
// 启动一个轻量后台 goroutine 在 Commit 信号到来后完成 slot 清理，
// 确保 slot 最终被释放、Ring 可持续使用。
// 调用方收到 ctx.Err() 后，Ring 仍可继续向同一实例写入。
func (r *Ring[T]) WaitContext(ctx context.Context, seq uint64) error {
	idx := seq & r.mask
	s := &r.slots[idx]
	select {
	case <-s.done:
		// 正常路径：消费者已 Commit
		err := s.err
		var zero T
		s.val = zero
		s.err = nil
		s.state.Store(sFree)
		return err
	case <-ctx.Done():
		// Context 取消：尝试立即接收（若 Commit 恰好已发送）
		select {
		case <-s.done:
			// Commit 已完成：同步清理 slot
			var zero T
			s.val = zero
			s.err = nil
			s.state.Store(sFree)
		default:
			// Commit 尚未运行：启动轻量 goroutine 在信号到来时清理，
			// 避免 slot 永久泄漏（Ring 可继续使用）
			go func() {
				<-s.done
				var zero T
				s.val = zero
				s.err = nil
				s.state.Store(sFree)
			}()
		}
		return ctx.Err()
	}
}

// Drain 消费者批量收割所有连续 ready 的 slot。
// fn 用于处理每个值（如编码到缓冲区）；fn 返回值暂存到 slot.err。
// 不唤醒生产者——刷盘完成后须调用 Commit。
// 返回 (起始序号, 收割数量)。
func (r *Ring[T]) Drain(fn func(*T) error) (uint64, int) {
	h := r.head.Load()
	t := r.tail.Load()
	start := h
	n := 0
	for h < t {
		idx := h & r.mask
		s := &r.slots[idx]
		if s.state.Load() != sReady {
			break
		}
		s.err = fn(&s.val)
		s.state.Store(sDrained)
		h++
		n++
	}
	r.head.Store(h)
	return start, n
}

// Commit 唤醒 [start, start+n) 的所有生产者。
// batchErr 非 nil 时覆盖各 slot 的 err（用于 flush 失败场景）。
func (r *Ring[T]) Commit(start uint64, n int, batchErr error) {
	for i := 0; i < n; i++ {
		idx := (start + uint64(i)) & r.mask
		s := &r.slots[idx]
		if batchErr != nil {
			s.err = batchErr
		}
		s.done <- struct{}{} // 唤醒对应的 Wait 调用
	}
}

// Len 当前未消费的记录数（近似值）。
func (r *Ring[T]) Len() int {
	t := r.tail.Load()
	h := r.head.Load()
	if t >= h {
		return int(t - h)
	}
	return 0
}

// Cap 返回 Ring 容量。
func (r *Ring[T]) Cap() int { return len(r.slots) }
