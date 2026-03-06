// Package percpu provides per-CPU contention-free counters.
//
// It hashes write operations to different cache-line-isolated slots using
// goroutine stack address entropy, eliminating cross-core atomic contention.
//
// # Slot selection strategy
//
// Add uses Fibonacci multiplicative hashing of the goroutine stack address
// to select a slot. Compared to a plain right-shift:
//
//   - Goroutine pool with fixed-size stacks (addresses spaced 8 KB apart)
//     would repeatedly map to the same few slots with >>13 alone;
//     the Fibonacci hash spreads them across all available slots.
//
// The hash is still approximate — it is best-effort, not guaranteed
// collision-free (two goroutines sharing a cache line is a performance
// degradation, not a correctness issue).
//
// # When to use percpu.Counter
//
//   - High-frequency monotonic metrics: bytes read/written, message counts
//   - Scenarios where an approximate snapshot on Load is acceptable
//
// # When NOT to use percpu.Counter — use atomic.Int64 instead
//
//   - Precise value required for control flow (e.g. rate-limit decisions)
//   - CAS (CompareAndSwap) or Swap semantics needed
//   - Store(arbitrary value) needed
//   - Strict consistent snapshot required: concurrent Add during Load may
//     produce an intermediate sum, not a point-in-time value
package percpu

import (
	"math"
	"math/bits"
	"sync/atomic"
	"unsafe"

	yakutil "github.com/uniyakcom/yakutil"
)

const (
	maxSlots = 256
	// cacheLine 缓存行大小，引用根包常量确保全库一致性。
	cacheLine = yakutil.CacheLine

	// fibShift 是 Fibonacci 乘积的固定右移量，取高 8 位索引 slot（最多 256 个）。
	// 自适应平台宽度：64 位取 bits[63:56]，32 位取 bits[31:24]。
	fibShift = bits.UintSize - 8
)

// fibMul64 是 Fibonacci 乘法常数的 uint64 形式（2^64 / φ，取整）。
// 必须为 var 而非 const，以确保 fibMul 的截断在运行时发生（避免 32 位编译期溢出检查）。
var fibMul64 uint64 = 0x9e3779b97f4a7c15

// fibMul 在运行时截断为 uintptr 宽度：
//   - 64 位：0x9e3779b97f4a7c15
//   - 32 位：0x7f4a7c15（下 32 位，仍为奇数，保持双射性质）
var fibMul = uintptr(fibMul64)

// slot is a single cache-line-padded counter bucket.
type slot struct {
	val atomic.Int64
	_   [cacheLine - 8]byte // pad to 64 B
}

// Counter is a per-CPU contention-free counter.
//
// Multiple goroutines may call Add concurrently with near-zero contention.
// Load returns an approximate aggregate snapshot across all slots; it does
// not guarantee a globally consistent point-in-time value.
//
// Memory: each Counter allocates exactly (slots × 64B) on the heap; slot
// count is auto-sized by New, so there is no fixed 16 KB overhead.
type Counter struct {
	slots []slot
	mask  int
	// shift 字段已移除：Add 使用固定常量 fibShift=56，
	// 从 64 位 Fibonacci 乘积中提取高位 8 bits（bits[63:56]），
	// 然后 AND mask 取有效位。对所有合法 slot 数量（8-256）等效，
	// 消除堆读取和 SHRQ 边界检查开销。
}

// New creates a Counter. The slot count is auto-sized to the CPU count:
// max(8, ceilPow2(procs)), capped at 256.
//
// Low-core environments get a minimum of 8 slots (collision rate drops from
// ~100% to ~25% on 2 vCPU); high-core environments (>=256 vCPU) are capped
// at 256 slots to avoid memory waste.
//
// Memory: allocates sz×64 bytes on the heap (e.g. 8 slots = 512 B,
// 64 slots = 4 KB, 256 slots = 16 KB).
func New(procs int) *Counter {
	sz := 1
	for sz < procs {
		sz <<= 1
	}
	if sz < 8 {
		sz = 8
	}
	if sz > maxSlots {
		sz = maxSlots
	}
	return &Counter{
		slots: make([]slot, sz),
		mask:  sz - 1,
	}
}

// Add atomically adds delta. The target slot is selected by Fibonacci
// multiplicative hashing of the caller goroutine's stack address.
//
// Fibonacci hashing outperforms a plain right-shift in goroutine pool
// scenarios where all stacks are spaced exactly 8 KB (1<<13) apart:
// plain >>13 maps them all to consecutive slots; Fibonacci hashing
// distributes them quasi-uniformly across all available slots.
//
// The shift amount is computed once in New as (wordBits - log2(slots)),
// extracting the highest-entropy leading bits of the 64-bit product
// replaces fixed >>16 with optimal per-size shift.
//
//go:nosplit
func (c *Counter) Add(delta int64) {
	var x uintptr
	// Fibonacci 乘法哈希：取 goroutine 栈地址 × fibMul，右移 fibShift（编译期常量），
	// 消除 c.shift 内存读取和运行时 SHRQ 边界检查。
	id := int((uintptr(unsafe.Pointer(&x)) * fibMul) >> fibShift)
	c.slots[id&c.mask].val.Add(delta)
}

// Load returns the sum across all slots (approximate snapshot).
//
// Concurrent Add calls during Load may cause intermediate slot values to be
// observed. For rate-limiting or audit counting that requires an exact value,
// use atomic.Int64 instead.
func (c *Counter) Load() int64 {
	var sum int64
	n := c.mask + 1
	for i := 0; i < n; i++ {
		sum += c.slots[i].val.Load()
	}
	return sum
}

// Reset zeroes all slots.
//
// Not atomic: concurrent Add calls during Reset may lose some increments.
func (c *Counter) Reset() {
	n := c.mask + 1
	for i := 0; i < n; i++ {
		c.slots[i].val.Store(0)
	}
}

// ─── Diagnostics ─────────────────────────────────────────────────────────────

// Stats summarises the per-slot value distribution of a Counter.
// It is intended for monitoring and debugging only — do not use it for
// control flow, as the snapshot is not atomic across slots.
type Stats struct {
	Slots int     // total number of slots
	Min   int64   // minimum per-slot value
	Max   int64   // maximum per-slot value
	Mean  float64 // mean per-slot value
	// Skew is the Max/Mean ratio (1.0 = perfectly uniform).
	// A value > 2.0 suggests that a hot slot is carrying a
	// disproportionate share of increments — consider increasing
	// the slot count (i.e. pass a larger procs to New).
	Skew float64
}

// Stats returns a snapshot of the per-slot value distribution.
//
// This is a read-only diagnostic: it does not affect Add performance.
// The snapshot is not a consistent point-in-time view if Add is
// called concurrently.
func (c *Counter) Stats() Stats {
	n := c.mask + 1
	var minV, maxV, total int64
	minV = math.MaxInt64
	for i := 0; i < n; i++ {
		v := c.slots[i].val.Load()
		total += v
		if v < minV {
			minV = v
		}
		if v > maxV {
			maxV = v
		}
	}
	mean := float64(total) / float64(n)
	var skew float64
	if mean > 0 {
		skew = float64(maxV) / mean
	} else {
		skew = 1.0
	}
	return Stats{
		Slots: n,
		Min:   minV,
		Max:   maxV,
		Mean:  mean,
		Skew:  skew,
	}
}
