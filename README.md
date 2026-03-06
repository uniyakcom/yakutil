# yakutil
[![Go Version](https://img.shields.io/github/go-mod/go-version/uniyakcom/yakutil)](https://github.com/uniyakcom/yakutil/blob/main/go.mod)
[![Go Reference](https://pkg.go.dev/badge/github.com/uniyakcom/yakutil.svg)](https://pkg.go.dev/github.com/uniyakcom/yakutil)
[![Go Report Card](https://goreportcard.com/badge/github.com/uniyakcom/yakutil)](https://goreportcard.com/report/github.com/uniyakcom/yakutil)
[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](https://opensource.org/licenses/MIT)
[![Lint](https://github.com/uniyakcom/yakutil/actions/workflows/format.yml/badge.svg)](https://github.com/uniyakcom/yakutil/actions/workflows/format.yml)
[![Test](https://github.com/uniyakcom/yakutil/actions/workflows/test.yml/badge.svg)](https://github.com/uniyakcom/yakutil/actions/workflows/test.yml)
[![Fuzz](https://github.com/uniyakcom/yakutil/actions/workflows/fuzz.yml/badge.svg)](https://github.com/uniyakcom/yakutil/actions/workflows/fuzz.yml)

**English** | [中文](README.zh.md)

**High-performance shared primitives for the uniyak ecosystem**

Zero external dependencies · Generics · Lock-free · Zero-allocation optimized

```
go get -u github.com/uniyakcom/yakutil
```

> Go 1.25+

---

## Package Overview

| Package | Purpose | Core Types / Functions |
|---|------|--------------|
| `yakutil` | Root: utilities + constants + byte conversion + sentinel errors | `B2S` `S2B` `IsPow2` `Pow2Ceil` `Native` `ErrStr` `Pad` `NoCopy` |
| `hash` | FNV-1a 64-bit hash + maphash AES acceleration | `Sum64` `Sum64s` `Sum64Map` `Sum64sMap` |
| `percpu` | Per-CPU contention-free counter/gauge | `Counter` `Gauge` |
| `mpsc` | MPSC lock-free Ring (Group Commit) | `Ring[T]` |
| `spsc` | SPSC wait-free Ring | `Ring[T]` |
| `backoff` | Three-phase adaptive backoff | `Backoff` |
| `arena` | CAS bump allocator | `Arena` |
| `bufpool` | Tiered byte-slice pool | `Pool` / `Get` / `Put` |
| `cow` | COW atomic value (generic) | `Value[T]` `Swap` `UpdateCAS` |
| `smap` | Sharded concurrent Map | `Map[V]` `Map64[V]` |
| `swar` | SWAR byte-parallel scan | `FindByte` `FindQuote` `FindEscape` `HasZero` `HasByte` `HasLess` `FirstByte` |
| `fold` | Fast ASCII case-insensitive comparison | `Lower` `Equal` `EqualStr` `EqualBytes` |
| `ring` | Auto-growing ring byte buffer | `Buffer` |
| `wheel` | Generic timing wheel (configurable resolution) | `Wheel[T]` |
| `wpool` | Worker pool + adaptive scaling | `Submitter` `Pool` `Stack` `Adaptive` |
| `itable` | Integer-key high-performance lookup table | `Table[V]` |
| `lru` | Sharded LRU cache | `Cache[V]` |
| `ratelimit` | Token-bucket rate limiter (CAS lock-free, zero-alloc) | `Limiter` |
| `semaphore` | Counting semaphore (bounded concurrency control) | `Semaphore` |
| `art` | Adaptive Radix Tree (ordered dictionary, prefix queries) | `Tree[V]` |
| `hist` | Equi-height histogram (CBO selectivity estimation) | `Hist` `Bucket` |
| `hll` | HyperLogLog cardinality estimator | `Sketch` |
| `sketch` | Count-Min Sketch frequency estimator | `CMS` |
| `topn` | Generic Top-N selection | `TopN` `ByKey` |
| `coarsetime` | Coarse-grained clock at 500µs precision (~1ns/op) | `NowNano` `Now` `Stop` |

---

## Root Package `yakutil`

Zero-copy conversions and bitwise utility toolkit.

```go
import "github.com/uniyakcom/yakutil"

s := yakutil.B2S(buf)        // []byte → string (zero-copy)
b := yakutil.S2B(s)          // string → []byte (zero-copy, read-only)
yakutil.IsPow2(1024)         // true
yakutil.Pow2Ceil(1000)       // 1024

// Native byte order
order := yakutil.Native      // binary.BigEndian or LittleEndian

// Zero-allocation sentinel error
const ErrNotFound = yakutil.ErrStr("not found")
fmt.Println(ErrNotFound.Error()) // "not found"

// Cache-line padding
type HotStruct struct {
    counter int64
    _       yakutil.Pad // 64-byte padding, avoids false sharing
    flag    int64
}
```

**Constants and Types**

- `CacheLine = 64` — x86/ARM cache line size
- `Pad` — `[64]byte` padding type
- `Native` — `binary.ByteOrder`, CPU native byte order detected at runtime
- `ErrStr` — zero-allocation string error type, declarable as `const`

**NoCopy**

Embedding `yakutil.NoCopy` makes `go vet copylocks` detect illegal copies:

```go
type MyMutex struct {
    yakutil.NoCopy
    // ...
}
```

---

## `hash` — FNV-1a + maphash Dual-Engine Hash

### FNV-1a (Cross-Process Deterministic)

```go
import "github.com/uniyakcom/yakutil/hash"

h := hash.Sum64(key)    // []byte input
h := hash.Sum64s("key") // string input (zero-alloc)
```

- Produces the same result as `hash/fnv.New64a()`; results can be compared cross-process or persisted
- Pure computation, no state allocation, inlinable
- `Sum64s` iterates directly over the string's underlying bytes, avoiding `[]byte(s)` conversion
- Measured **~2.8× faster** than `fnv.New64a()`, zero allocations (fnv.New64a costs 1 alloc/8 B per call)

### maphash (AES-NI Accelerated, Process-Local)

```go
h := hash.Sum64Map(key)    // []byte input, AES-NI accelerated
h := hash.Sum64sMap("key") // string input (zero-alloc)
```

- Leverages Go runtime's built-in random seed + AES-NI hardware instructions
- **Note**: seed is randomized on process restart; hash values cannot be persisted or compared cross-process
- Zero allocations; suitable for in-process routing, sharding, Bloom filters, etc.

### Performance Comparison (Intel Xeon E-2186G @ 3.80GHz, Linux amd64, Go 1.25, `bench_linux_6c12t.txt`)

| Data Size | FNV `Sum64s` | maphash `Sum64Map` | maphash `Sum64sMap` | maphash/FNV ratio |
|---------|-------------|-------------------|---------------------|------------------|
| 8 B     | 1,344 MB/s  | 1,751 MB/s        | 1,272 MB/s          | ×1.3 (small gap at 8B) |
| 32 B    | 1,712 MB/s  | **6,882 MB/s**    | **5,545 MB/s**      | ×3.2–4.0         |
| 128 B   | 1,257 MB/s  | **15,381 MB/s**   | **14,200 MB/s**     | ×11.3–12.2       |
| 256 B   | 1,194 MB/s  | **14,544 MB/s**   | **13,480 MB/s**     | ×11.3–12.2       |
| 1 KB    | 1,151 MB/s  | **13,016 MB/s**   | **12,781 MB/s**     | ×11.1–11.3       |
| 4 KB    | 1,141 MB/s  | **12,492 MB/s**   | **12,520 MB/s**     | ×10.9–11.0       |

All implementations: **0 B/op, 0 allocs/op**.

| Implementation | ns/op | allocs | Notes |
|------|-------|--------|------|
| `Sum64s` (internal FNV) | **16.4** | 0 / 0 B | Reference key; recommended stdlib replacement |
| `fnv.New64a()` (stdlib) | 50.5 | **2 / 40 B** | Interface boxing; ~3.1× slower, allocations |

**Parallel throughput (32B key, 12 threads, `-race=false`)**

| Implementation | ns/op | MB/s | allocs |
|------|-------|------|--------|
| `FNV Sum64s` | **2.04** | 15,707 | 0 |
| `maphash Sum64sMap` | **0.95** | 33,583 | 0 |
| Parallel speedup | — | **×2.1** | — |

### Selection Guide

| Scenario | Recommended | Reason |
|------|------|------|
| Cross-process, persisted, deterministic hash | `Sum64` / `Sum64s` | FNV-1a spec compliant, stable seed |
| Short key (≤16 B), in-process | `Sum64` / `Sum64s` | Small perf gap (≤1.3×), no seed restriction |
| Medium-long key (≥32 B) in-process routing | `Sum64Map` / `Sum64sMap` | 4–12× throughput, zero-cost AES-NI |
| smap, Bloom filter, consistent hash ring | `Sum64Map` / `Sum64sMap` | In-process; stable seed unnecessary |

### When to Choose [yakhash](https://github.com/uniyakcom/yakhash)

`yakutil/hash` is designed as a **zero-external-dependency lightweight internal tool**, covering in-process hashing needs across the yakutil ecosystem. When your use case exceeds the following boundaries, consider the dedicated uniyak hash library [yakhash](https://github.com/uniyakcom/yakhash) (full xxHash XXH64/XXH3 implementation with AVX2/NEON assembly acceleration):

| Scenario | yakutil/hash | yakhash |
|---|---|---|
| ≥ 1 KB large-block throughput | FNV ~1.15 GB/s (byte-loop ceiling, no vectorization) | XXH3-64 **41.6 GB/s @ 1 KB, 56.2 GB/s @ 10 MB** (**36–49× faster**) |
| Reproducible hash (fixed seed) | `maphash` is process-random; not reproducible | `Sum3_64Seed` / `Sum64Seed` |
| Cross-process / distributed consistent hash | `maphash` seed cannot be shared cross-process | `Sum3_64Secret` + `GenSecret` |
| HashDoS active defense (controlled key) | Not available | 192-byte secret space, `Sum3_64Secret` / `Sum3_64Seed` |
| 128-bit output (content addressing / dedup) | Not available | `Sum3_128` / `Sum3_128Seed` / `Sum3_128String` |
| Streaming chunked processing (`io.Reader`) | Not available | `New3()` + `Write` + `Sum64` / `Sum128` |
| State snapshot / checkpoint-resume | Not available | `MarshalBinary` / `UnmarshalBinary` |
| C xxHash 0.8.x bit-exact compatibility | Not available | ✓ All functions bit-identical to C original |

```go
go get github.com/uniyakcom/yakhash

// Large data: AVX2 vectorized, 56 GB/s (10 MB block, same machine)
h := yakhash.Sum3_64(data)

// HashDoS defense (cross-process consistent key)
secret := yakhash.GenSecret(loadSeedFromConfig())
h, _ := yakhash.Sum3_64Secret(key, secret[:])

// Streaming chunked
d := yakhash.New3()
io.Copy(d, reader)
h := d.Sum64()
```

> `yakutil/hash` maintains zero external dependencies; `yakhash` is the dedicated uniyak ecosystem hash library. The two have no transitive dependency relationship.

---

## `percpu` — Per-CPU Counters

Multi-core concurrent counting with near-zero contention.

```go
import "github.com/uniyakcom/yakutil/percpu"

c := percpu.New(runtime.GOMAXPROCS(0))

c.Add(1)          // writes distributed across different cache line slots
total := c.Load() // aggregate all slots (approximate)
c.Reset()         // zero all slots
```

**Design**: Maps callers to 8–256 isolated 64B slots using goroutine stack address × **Fibonacci multiplicative hash constant** (`0x9e3779b97f4a7c15` = 2⁶⁴/φ) right-shifted 16 bits. Write path uses only atomic Add, no cross-core contention.

Auto slot count: `max(8, ceilPow2(procs))`, capped at 256; low-core environments retain 8 slots.

> **Why Fibonacci hash instead of `>>13`?**  
> goroutine pool stack spacing is fixed at 8 KB (1<<13 bytes); plain `>>13` maps them to consecutive slots (hotspot clustering).  
> Fibonacci multiplication disperses clustered addresses uniformly across all slots.  
> On 32-bit systems the constant auto-truncates to `0x7f4a7c15` (still odd, preserving bijection).

**Selection Guide**

| Scenario | Recommendation | Reason |
|------|---------|------|
| Monitoring counters (bytes, messages) | `percpu.Counter` | High-frequency writes, approximate reads, 2× parallel advantage |
| Connection count, error count, rate limiting logic | `atomic.Int64` | Requires exact value for control flow |
| CAS / Swap / arbitrary initial value | `atomic.Int64` | percpu does not support these |
| Load requires strict consistent snapshot | `atomic.Int64` | percpu.Load is O(slots) aggregation, may see intermediate state |

**Performance (Intel Xeon E-2186G @ 3.8GHz, 12 cores, `bench_linux_6c12t.txt`)**

| Operation | percpu | atomic.Int64 |
|------|--------|-------------|
| Add serial | **5.7 ns** | 5.7 ns |
| Add parallel (12C contention) | **2.4 ns** ✓ | 18.3 ns ✗ (**×7.6 slower**) |
| Load | 10.6 ns | ~3 ns ✗ |
| Reset | 145 ns | — |

> Serial: comparable; high-concurrency writes: percpu ~**8× advantage**; Load is ~**4×** slower than atomic.Load.  
> Use percpu exclusively for "write-heavy, read-light" monitoring counters.

**Diagnostic interface `Stats()`**

```go
st := c.Stats()
// Stats{Slots:64, Min:..., Max:..., Mean:..., Skew:1.0}
// Skew = Max/Mean; ≤2.0 is uniform, >2.0 suggests hot slot
if st.Skew > 2.0 {
    log.Printf("percpu hot slot detected: skew=%.1f", st.Skew)
}
```

| Field | Meaning |
|------|------|
| `Slots` | Total slot count |
| `Min` / `Max` | Minimum / maximum slot value |
| `Mean` | Average slot value |
| `Skew` | `Max/Mean`; 1.0 = perfectly uniform; >2.0 suggests hotspot; >5.0 suggests increasing procs |

### `Gauge` — Bidirectional Per-CPU Gauge

`Gauge` supports Add + Sub (value can be negative), suitable for tracking active connections, concurrent request counts, etc.

```go
g := percpu.NewGauge(runtime.GOMAXPROCS(0))

g.Add(1)       // new connection enters
g.Sub(1)       // connection closes (equivalent to Add(-1))
g.Load()       // aggregate all slots (approximate snapshot)
g.Reset()      // zero all slots

// Diagnostics
st := g.Stats() // GaugeStats{Slots, Min, Max, Sum, Mean, Skew}
```

| Counter vs Gauge | Counter | Gauge |
|---|---|---|
| Monotonic | Yes (increment only) | No (increment and decrement) |
| Add/Sub | Add only | Add + Sub |
| Load | Approximate aggregate | Approximate aggregate |
| Typical use | Byte counts, message counts | Active connections, memory usage |

> Performance identical to Counter (same Fibonacci hash + 64B slot layout).

**Measured Performance (Intel Xeon E-2186G @ 3.80GHz, 12 cores, `bench_linux_6c12t.txt`)**

| Operation | ns/op | allocs | Notes |
|------|-------|--------|------|
| `Add` (serial) | **5.64** | 0 | Fibonacci hash distributes to random slot |
| `Add` (12 parallel) | **~2.9** | 0 | 12 threads, no slot contention |
| `Load` (cross-slot aggregate) | — | 0 | Single pass over all slots |

> Comparison: `atomic.Int64.Add` at 12 parallel ≈ 18.3 ns/op (≥6× gap).

---

## `mpsc` — MPSC Lock-Free Ring + Group Commit

Multi-producer single-consumer queue, ideal for WAL Group Commit pattern.

```go
import "github.com/uniyakcom/yakutil/mpsc"

r := mpsc.New[Record](4096)

// Producer (multiple goroutines)
seq, ok := r.Enqueue(record)
if ok {
    err := r.Wait(seq) // blocks until consumer Commits
}

// Consumer (single goroutine)
start, n := r.Drain(func(rec *Record) error {
    return encodeToBuf(rec) // batch encode
})
flushErr := fsync(buf)
r.Commit(start, n, flushErr) // wake all producers
```

**State machine**: `free → filling → ready → drained → free`

- `Enqueue`: CAS tail, write slot
- `Wait`: channel-block waiting for done signal (Go runtime scheduling, no busy-spin), then release slot
- `Drain`: batch harvest consecutive ready slots
- `Commit`: set `done=1` to wake producers, can carry batch error

---

## `spsc` — SPSC Wait-Free Ring

Single-producer single-consumer ultra-low-latency queue.

```go
import "github.com/uniyakcom/yakutil/spsc"

r := spsc.New[Event](1024)

// Producer (single goroutine)
r.Push(event)

// Consumer (single goroutine)
if evt, ok := r.Pop(); ok {
    handle(evt)
}
```

- No CAS; only `atomic.Store` / `atomic.Load` (equivalent to plain MOV on x86)
- `cachedHead` / `cachedTail` eliminate normal cross-core cache line reads
- Typical throughput **2-5 ns/op**

---

## `backoff` — Three-Phase Adaptive Backoff

```go
import "github.com/uniyakcom/yakutil/backoff"

var bo backoff.Backoff
for !condition() {
    bo.Spin()
}
bo.Reset()
```

| Phase | Iteration Range | Behavior |
|------|---------|------|
| Phase 1 | `N < 64` | Tight CPU spin (zero overhead) |
| Phase 2 | `N < 128` | `runtime.Gosched()` |
| Phase 3 | `N ≥ 128` | Exponential sleep (1µs to 1ms) |

Customizable:

```go
bo := backoff.Backoff{
    SpinN:   32,
    YieldN:  16,
    MaxWait: 500 * time.Microsecond,
}
```

Zero value usable directly (default parameters). Value type, no allocation.

---

## `arena` — CAS Bump Allocator

High-concurrency bump allocator, suitable for short-lived scenarios like WAL encode buffers.

```go
import "github.com/uniyakcom/yakutil/arena"

a := arena.New(0) // default 64KB chunk

buf := a.Alloc(128) // 8-byte aligned, CAS concurrency-safe; n≤0 returns nil
// ... use buf ...

a.Reset() // switch to new chunk; old references still valid until GC
```

- Fast path CAS + add = **< 5 ns/op** (vs Go heap ~25 ns)
- `n > chunkSize` falls back to `make()`
- Chunk exhaustion auto-switches; old chunk references remain valid

---

## `bufpool` — Tiered Byte-Slice Pool

20 tiers of `sync.Pool` (64B to 32MB), automatically tiered by size. Requests >32MB are directly allocated, bypassing the pool.

```go
import "github.com/uniyakcom/yakutil/bufpool"

// Global functions
buf := bufpool.Get(4096)
defer bufpool.Put(buf)

// Independent instance
var p bufpool.Pool
buf := p.Get(1024)
p.Put(buf)
```

- `Get(size)`: returns a slice with `len=size, cap=2^n`
- `Put(b)`: returns to the appropriate tier by `cap`
- Slices with `cap < 64B` or `cap > 32MB` are automatically discarded
- Slices with non-power-of-2 `cap` are automatically discarded (prevents `b[:size]` out-of-bounds panic)

---

## `cow` — Copy-on-Write Atomic Value

Generic atomic snapshot for read-heavy, write-light scenarios.

```go
import "github.com/uniyakcom/yakutil/cow"

v := cow.New[Config](defaultConfig)

// Read (any goroutine, ~1ns)
cfg := v.Load()

// Write (single writer)
v.Store(newConfig)

// Read-modify-write
v.Update(func(old Config) Config {
    old.Timeout = 5 * time.Second
    return old
})
```

- Read path: single `atomic.Pointer.Load()`, truly lock-free
- Write path: construct new value, `atomic.Store`
- `Update`: suitable for single writer; use external lock for multiple writers
- `Swap`: atomically replaces and returns old value, concurrency-safe
- `UpdateCAS`: CAS-loop lock-free read-modify-write, safe for multiple writers

```go
// Multiple-writer-safe read-modify-write
v.UpdateCAS(func(old Config) Config {
    old.Count++
    return old
})

// Atomic replace and get old value
old := v.Swap(newConfig)
```

---

## `smap` — Sharded Concurrent Map

High-performance concurrent Map, N shards + RWMutex to isolate contention. String key routing uses **maphash AES acceleration** (for in-process routing; seed changes on process restart).

```go
import "github.com/uniyakcom/yakutil/smap"

// string key (maphash AES-accelerated shard hash)
m := smap.New[int](64) // 64 shards
m.Set("foo", 42)
v, ok := m.Get("foo")
m.Range(func(k string, v int) bool { return true })

// Atomic get-or-create (double-checked locking; fn executes exactly-once under write lock)
v, created := m.GetOrSet("session:42", func() int {
    return expensiveInit()
})
// created=true means this call triggered creation

// uint64 key (Fibonacci hash)
m64 := smap.New64[string](32)
m64.Set(12345, "hello")
val, created := m64.GetOrSet(12345, func() string { return "world" })
```

- Read path RLock on single shard (~22 ns), no global lock
- Cache-line isolation between shards
- `GetOrSet`: fast RLock check first; if present returns (false); if absent upgrades to Lock, double-checks, then calls `fn` — guarantees `fn` executes **only once** concurrently

**Measured Performance (`bench_linux_6c12t.txt`)**

| Operation | ns/op |
|------|-------|
| Get | 23 ns |
| Set | ~34 ns |
| Parallel Get (12t) | 46 ns |

---

## `swar` — SWAR Byte-Parallel Scan

SIMD-Within-A-Register: process 8 bytes simultaneously in a single integer operation.

```go
import "github.com/uniyakcom/yakutil/swar"

idx := swar.FindByte(data, '\n')   // find newline
idx := swar.FindQuote(data)        // find '"'
idx := swar.FindEscape(data)       // find <0x20 / '"' / '\\'
```

- Typical 4–8× speedup vs single-byte loop
- Suitable for JSON parsing, HTTP header scanning

---

## `fold` — ASCII Case-Insensitive Comparison

Based on 256-byte lookup table, zero allocation.

```go
import "github.com/uniyakcom/yakutil/fold"

fold.Equal([]byte("Content-Type"), "content-type") // true
fold.EqualStr("HOST", "Host")                      // true
```

- Approximately **1.78× faster** than `strings.EqualFold` (**~6.4 vs ~11.4 ns**, `bench_linux_6c12t.txt`)
- ASCII only (A-Z and a-z); use stdlib for Unicode

---

## `ring` — Ring Byte Buffer

2^N auto-growing ring buffer for network I/O.

```go
import "github.com/uniyakcom/yakutil/ring"

buf := ring.New(4096)
buf.Write(data)
buf.WriteByte(0x0A)       // implements io.ByteWriter, writes single byte (zero-alloc)
p := buf.Peek(10)         // inspect first 10 bytes (non-consuming)
buf.Discard(10)           // discard
c, err := buf.ReadByte()  // implements io.ByteReader, read byte-by-byte (zero-alloc)
buf.UnreadByte()          // implements io.ByteScanner, replay last ReadByte (zero-alloc)
buf.WriteTo(conn)         // zero-copy output
buf.ReadFrom(conn)        // read from Reader
```

**Byte-by-byte I/O** (`WriteByte` / `ReadByte` / `UnreadByte` trio):
- No `[]byte` allocation needed; suitable for frame header parsing (magic, version, length fields)
- `ReadByte` returns `(byte, error)`; empty buffer returns `io.EOF`
- `UnreadByte` rolls back the last `ReadByte`, implements `io.ByteScanner` for peek-and-rollback protocol parsing
  - Two consecutive `UnreadByte` calls return `io.ErrNoProgress`
- Can be mixed with `Read`/`Write`

**`Peek` semantics**:
- No wraparound: returns slice pointing to internal buffer (zero-copy); caller must not modify
- Crossing boundary: allocates new slice and copies (can modify directly)

- Bitmask modulo, zero-alloc wrap
- Auto 2× expansion with data linearization
- Struct is exactly 64B = 1 cache line

**Measured Throughput (`bench_linux_6c12t.txt`)**

| Operation | ns/op | Throughput |
|------|-------|------|
| Write 64 B | 7.6 ns | 8,404 MB/s |
| WriteRead 1 KB | 31.0 ns | 33,064 MB/s |
| PeekDiscard | 9.9 ns | — |
| WriteByte | **2.6 ns** | 0 allocs |
| ReadByte | **1.84 ns** | 0 allocs |
| UnreadByte (Write+Read+Unread+Read round-trip) | **4.3 ns** | 0 allocs |

**Zero-copy I/O (`ReadableSegments` / `CommitRead` / `WritableSegments` / `CommitWrite`)**

High-performance protocol parsers (e.g., RESP) can use the zero-copy interface to directly access internal buffers, eliminating intermediate `Read(tmp)` copies:

```go
// Zero-copy read side
// Returns one or two segments pointing to internal buffer; two segments when wrapping
s1, s2 := buf.ReadableSegments(n)  // caller must not write to these
parse(s1)                          // zero-copy parse
if s2 != nil { parse(s2) }         // handle second segment when wrapping
buf.CommitRead(n)                  // advance read pointer

// Zero-copy write side
s1, s2 := buf.WritableSegments(need) // get writable memory segments
copy(s1, data)                       // write directly into internal buffer
buf.CommitWrite(n)                   // confirm n bytes written
```

> Returned `s1`/`s2` point directly to internal buffer, no copy needed. After consuming k bytes, must call `CommitRead(k)` to advance read pointer.

**32-bit overflow auto-protection**

`r`, `w`, `mask` fields use `uint` (platform-native unsigned integer):

| Platform | Field width | Overflow threshold | Conclusion |
|------|---------|---------|------|
| 64-bit | uint64 | ~18 EB | Practically unreachable |
| 32-bit | uint32 | 4 GB | Go slice limit 2 GB; also unreachable |

Unsigned overflow wraps naturally; `Len() = w - r` always correct; no need to manually call `Reset()` in continuous non-empty stream scenarios.

---

## `wheel` — Generic Timing Wheel

High-performance timer with configurable resolution.

```go
import "github.com/uniyakcom/yakutil/wheel"

w := wheel.New[ConnID](10*time.Millisecond, 1024)
id := w.Add(5*time.Second, connID)  // O(1) add
w.Cancel(id)                         // O(1) cancel

// Manual advance
w.Advance(func(id ConnID) { close(id) })

// Or automatic tick
w.Run(ctx, func(id ConnID) { close(id) })
```

- Add/Cancel O(1), Advance O(expired)
- Supports layered rounds (long delays exceeding slots count)
- `sync.Pool` reuses entry nodes

---

## `wpool` — Worker Pool + Adaptive Scaling

### Submitter Interface

`Pool`, `Stack`, and `Adaptive` all implement `Submitter`; callers need not care about the underlying implementation:

```go
type Submitter interface {
    Submit(task func()) bool    // blocks until task accepted or pool stopped
    TrySubmit(task func()) bool // non-blocking; returns false if cannot accept immediately
    Running() int               // current active worker count
    Stop()                      // graceful shutdown, waits for all workers to finish
    PanicCount() int64          // lifetime task panic count (increments regardless of handler)
}
```

### TimedSubmitter Interface

`Pool` and `Stack` additionally implement `TimedSubmitter` for timeout backpressure control:

```go
type TimedSubmitter interface {
    Submitter
    SubmitTimeout(task func(), timeout time.Duration) bool
    // Waits at most timeout: Pool waits for queue slot, Stack waits for idle worker.
    // Returns false on timeout or pool stopped.
}
```

**Use case**:
```go
// HTTP handler: wait at most 50ms for worker, return 503 on timeout
ok := pool.SubmitTimeout(func() { handle(req) }, 50*time.Millisecond)
if !ok {
    http.Error(w, "503 overloaded", http.StatusServiceUnavailable)
    return
}
```

### Pool — FIFO Basic Worker Pool

```go
import "github.com/uniyakcom/yakutil/wpool"

p := wpool.NewPool(8, 5*time.Second) // 8 workers, 5s idle timeout
p.Submit(func() { handle(req) })
p.TrySubmit(func() { handle(req) }) // non-blocking
p.Resize(16)                         // dynamic scaling
p.Stop()                             // graceful shutdown
```

- Queue watermark >75% / idle >50% auto-scaled by `Adaptive.adjust()` (`Pool` itself does not auto-scale)
- Adjust worker count via `Resize(n)`; excess workers auto-exit on idle after downsizing
- `Submit` / `TrySubmit` blocking/non-blocking modes
- `safeRun` delegates to `panicSafeRun` (`wpool/safe.go`), shared by Pool and Stack
- Single task panic does not affect worker survival; each panic is counted then handler called
- `PanicCount() int64` cumulative panic count (increments regardless of whether handler is set)

**PanicCount usage example**

```go
p := wpool.NewPool(8, 5*time.Second, wpool.WithPanicHandler(func(r any, stack []byte) {
    slog.Error("worker panic", "err", r, "stack", string(stack))
}))

// Periodically report to monitoring (Prometheus, OTEL, etc.)
go func() {
    for range time.Tick(30 * time.Second) {
        metrics.Set("wpool_panics_total", float64(p.PanicCount()))
    }
}()
```

> `PanicCount` and `WithPanicHandler` are independent: increments without handler, counts first then calls handler when present.

### Stack — FILO Hot Worker Pool

```go
s := wpool.NewStack(8, 10*time.Second) // 8 workers, 10s idle timeout
s.Submit(func() { handle(req) })
s.TrySubmit(func() { handle(req) })
s.PanicCount()
s.Stop()
```

**FILO vs FIFO Selection**:

| Scenario | Recommended |
|------|------|
| Network IO reactor dispatch, protocol parsing (short-lived, high-concurrency) | `Stack` (FILO) |
| General background tasks, variable task duration | `Pool` (FIFO) |

**Stack design details**:
- per-worker `chan(capacity=1)` avoids Submit/workerFunc mutual blocking
- `sync.Pool` reuses `stackWorkerChan`, reduces GC allocation
- `sync.Cond` zero-CPU-block waits when `maxWorkers` is reached
- Background cleaner periodically reclaims idle-timeout workers (default 10s)
- **`Stop()` guarantee**: waits for all worker goroutines to fully exit (including currently executing tasks); `Running() == 0` after return, no goroutine leak

### Adaptive — Auto-Scaling Pool

```go
a := wpool.NewAdaptive(4, 64, 500*time.Millisecond, 5*time.Second)
// params: min=4, max=64, sample period=500ms, idle timeout=5s
a.Submit(func() { handle(req) })
a.Stop()
```

- Queue watermark >75%: scale up (+25% workers, up to max)
- Idle workers >50%: scale down (-25% workers, down to min)
- `PanicCount() int64` delegates to internal Pool, sharing the same counter

### Interface Substitution Example

```go
func NewDispatcher(pool wpool.Submitter) *Dispatcher {
    return &Dispatcher{pool: pool}
}

// Reactor IO: use FILO Stack
d := NewDispatcher(wpool.NewStack(runtime.NumCPU(), 0))

// General background tasks: use FIFO Pool
d := NewDispatcher(wpool.NewPool(8, 0))

// With explicit backpressure deadline: use TimedSubmitter
var ts wpool.TimedSubmitter = wpool.NewStack(8, 0)
ts.SubmitTimeout(task, 50*time.Millisecond)
```

**Measured Performance (`bench_linux_6c12t.txt`)**

| Implementation | Submit (serial) | TrySubmit | Submit (parallel) |
|------|-------------|-----------|-------------|
| `Pool` FIFO | 448 ns | **16 ns** | 382 ns |
| `Stack` FILO | **418 ns** | 41 ns | 409 ns |
| `go spawn` | 236 ns | — | — |

> `TrySubmit` is fastest (16 ns), suitable for non-blocking dispatch.  
> `Stack.Submit` (FILO) is CPU-cache affine, suitable for low-latency network IO dispatch.

**SubmitTimeout Measured Performance (`bench_linux_6c12t.txt`)**

| Implementation | ns/op (serial) | allocs | Notes |
|------|-------------|--------|------|
| `Pool.SubmitTimeout` | 611 ns | **0 (0B)** | Timer pool optimization, zero-alloc |
| `Stack.SubmitTimeout` | **471 ns** | 53 B / 0 alloc | FILO worker reuse, timer token pooled |
| `Pool.SubmitTimeout` (parallel) | 521 ns | 0 | — |
| `Stack.SubmitTimeout` (parallel) | **630 ns** | 72 B / 1 alloc | — |

> `Stack.SubmitTimeout` is ~**23% faster** than `Pool.SubmitTimeout`.

---

## `itable` — Integer-Key Lookup Table

Small key array direct lookup + large key `sync.Map` fallback.

```go
import "github.com/uniyakcom/yakutil/itable"

tb := itable.New[Conn](0)  // default 65536 fast path
tb.Set(fd, &conn)          // O(1) atomic store
conn, ok := tb.Get(fd)     // O(1) atomic load
tb.Del(fd)
```

- key < 65536: `atomic.Pointer` array, zero-lock, no contention
- key ≥ 65536: `sync.Map` fallback
- Suitable for dense integer scenarios like fd, connection IDs

---

## `lru` — Sharded LRU Cache

Multiple shards reduce lock contention, **maphash AES-accelerated** shard routing, optional lazy TTL.

```go
import "github.com/uniyakcom/yakutil/lru"

// Basic: 16 shards, 10000 entries per shard
c := lru.New[[]byte](16, 10000)
c.Set("key", data)
val, ok := c.Get("key")
c.Del("key")

// Eviction callback (triggered on LRU capacity eviction, not TTL expiry)
c = lru.New[[]byte](16, 10000, lru.WithEvict[[]byte](func(k string, v []byte) {
    log.Printf("evicted %s", k)
}))

// Lazy TTL: checked on Get, no background goroutine
c = lru.New[[]byte](16, 10000, lru.WithTTL[[]byte](12*time.Hour))

// Combined: TTL + eviction callback
c = lru.New[[]byte](16, 10000,
    lru.WithTTL[[]byte](5*time.Minute),
    lru.WithEvict[[]byte](func(k string, v []byte) { ... }),
)

// Iterate all live entries (newest→oldest, skip TTL-expired)
c.Range(func(k string, v []byte) bool {
    fmt.Println(k)
    return true // return false to terminate early
})

// Clear all entries (does not trigger evict callback)
c.Purge()
```

- `Get` ~27–28 ns, `Set` ~27 ns (`bench_linux_6c12t.txt`)
- Parallel Get (12t): ~86 ns
- Per-shard independent Mutex + doubly-linked list
- Auto-evicts least recently used on capacity overflow
- `Range`: snapshot iteration, callback invoked outside lock, skips TTL-expired entries
- `Purge`: O(shards) clear, does not trigger evict callback
- `WithTTL`: TTL resets when same key is overwritten; `Len()` includes lazily uncleaned expired entries

---

## `ratelimit` — Token-Bucket Rate Limiter

CAS lock-free token bucket, no background goroutines, zero allocation, suitable for per-IP / global rate limiting.

```go
import "github.com/uniyakcom/yakutil/ratelimit"

// Global: 1000/sec, burst cap 200
rl := ratelimit.New(1000, 200)

// Non-blocking check (hot path, ~15-25 ns, 0 allocs)
if !rl.Allow() {
    conn.Close()
    return
}

// Batch consume (writing multiple messages at once)
if !rl.AllowN(5) {
    return ErrRateLimited
}

// Runtime diagnostics
rl.Tokens()  // current available token count snapshot
rl.Rate()    // configured rate (tokens/sec)
rl.Burst()   // bucket capacity
rl.Reset()   // reset to full bucket (connection reset, test init)
```

**Performance** (Intel Xeon E-2186G @ 3.80GHz, amd64, go1.25):

| Scenario | Latency | Allocation |
|------|------|------|
| `Allow` (single core, tokens available) | ~15-25 ns | 0 allocs |
| `Allow` (12-thread parallel) | ~5-15 ns | 0 allocs |
| `Allow` (tokens exhausted, fast reject) | ~10-20 ns | 0 allocs |

**Algorithm highlights**:
- Lazy refill (compute diff per call by time), avoids background timer goroutine
- Refill calculation uses integer nanoseconds (`1e9 / rate`), no floating-point error
- CAS loop: read snapshot → compute new state → CAS update, retries on contention (typically 1-2 rounds)
- `n > burst` always returns `false` (can short-circuit early, no waiting)

**High-performance scenario: inject coarse clock (`WithClock`)**

In hot paths (e.g., TCP Accept loop), `time.Now()` syscall overhead is ~40–60 ns;  
using `coarsetime.NowNano` (500µs precision, ~1 ns/op) reduces clock overhead by 40–60×:

```go
import (
    "github.com/uniyakcom/yakutil/coarsetime"
    "github.com/uniyakcom/yakutil/ratelimit"
)

coarsetime.Start()
defer coarsetime.Stop()

rl := ratelimit.New(10_000, 500,
    ratelimit.WithClock(coarsetime.NowNano),
)
if !rl.Allow() {
    return ErrRateLimited
}
```

> **Use case**: high-concurrency paths with >100K QPS single-machine; relaxed precision requirements (500µs jitter is typically negligible at token-bucket granularity). For precise rate shaping, use default `time.Now()`.

### `IPMap` — Per-IP Rate Limit Manager

`IPMap` combines `smap.Map` (maphash AES-NI sharded hash) with `*Limiter`, no background goroutines, zero-alloc hot path.

```go
ipmap := ratelimit.NewIPMap(100, 20, 0) // rate=100/s, burst=20, default 64 shards

// Each new connection (OnAccept)
if !ipmap.Allow(conn.RemoteIP()) {
    conn.Close()
    return
}

// Batch operation
if !ipmap.AllowN(ip, 5) { return ErrRateLimited }

// Get underlying Limiter (diagnostics)
lim := ipmap.Get(ip)
log.Printf("ip=%s tokens=%d", ip, lim.Tokens())

// Periodically evict inactive IPs (prevent memory leak)
go func() {
    for range time.Tick(5 * time.Minute) {
        n := ipmap.Evict(10 * time.Minute)
        log.Printf("evicted %d stale IP limiters", n)
    }
}()

// Clear all (config change / test init)
ipmap.Purge()
```

```go
// Strict mode: eliminates TOCTOU race on first Limiter creation (exactly-once semantics)
ipmap := ratelimit.NewIPMap(100, 20, 0, ratelimit.WithStrictNewIP())
```

```go
// Access hotspot analysis: top N IPs by hit count
top := ipmap.TopN(5)
for _, e := range top {
    log.Printf("ip=%s hits=%d recent=%d", e.IP, e.Hits, e.RecentHits)
}

// Recent hotspot analysis: sorted by ~1min sliding window activity
recent := ipmap.TopNRecent(5)
for _, e := range recent {
    log.Printf("ip=%s recent_hits=%d", e.IP, e.RecentHits)
}
```

| Method | Description |
|------|------|
| `Allow(ip)` | Consume 1 token; false = rate limited |
| `AllowN(ip, n)` | Batch consume n tokens |
| `Get(ip)` | Get (or create) underlying `*Limiter` |
| `Evict(ttl)` | Evict IPs inactive within ttl; returns eviction count |
| `Len()` | Current tracked IP count |
| `Purge()` | Clear all entries |
| `TopN(n)` | Top-N by cumulative `Hits` (descending) |
| `TopNRecent(n)` | Top-N by recent (~1min window) `RecentHits` (descending) |

**Measured Performance (`bench_linux_6c12t.txt`, rate=1M/s, burst=100K)**

| Operation | ns/op | allocs | Notes |
|------|-------|--------|------|
| `Allow` (serial, hot IP) | 121 | 0 | maphash shard + token-bucket CAS |
| `Allow` (12 parallel) | 48 | 1 / 8 B | Shard lock scatter |

---

## `semaphore` — Counting Semaphore

Channel-backed, Go runtime native scheduling, supports non-blocking / context timeout, suitable for MaxConns limiting.

```go
import "github.com/uniyakcom/yakutil/semaphore"

sem := semaphore.New(opts.MaxConns) // max concurrent connections

// New connection entry: non-blocking fast reject
if !sem.TryAcquire() {
    conn.Close()
    return ErrTooManyConns
}
defer sem.Release()

// Client-side dial control: context-blocked wait
if err := sem.AcquireContext(ctx); err != nil {
    return err // context.DeadlineExceeded or Canceled
}
defer sem.Release()

// Runtime diagnostics (Prometheus gauge)
sem.Count()     // current held permits (active connection count)
sem.Cap()       // maximum capacity
sem.Available() // remaining available permits
```

| | `Acquire` | `TryAcquire` | `AcquireContext` | `TryAcquireN(n)` |
|---|---|---|---|---|
| Blocking | Yes (wait indefinitely) | No (return immediately) | Yes (until ctx cancelled) | No (return immediately) |
| Count | 1 | 1 | 1 | n (all-or-nothing) |
| Suitable for | Internal background tasks | New connection entry | Client dial | Batch slot pre-reservation |

**Batch operations `TryAcquireN` / `ReleaseN`**:

```go
if !sem.TryAcquireN(n) {
    return ErrNotEnoughSlots
}
defer sem.ReleaseN(n)
```

- `TryAcquireN(n) bool`: atomically acquire n permits all-or-nothing; `n > Cap()` always false
- `ReleaseN(n)`: batch release n permits; panics if n exceeds held count

**Measured Performance (`bench_linux_6c12t.txt`)**

| Operation | ns/op | allocs |
|------|-------|--------|
| `Acquire`/`Release` (serial) | 28.8 | 0 |
| `TryAcquire_Success` (serial) | 27.9 | 0 |
| `TryAcquire_Full` (fast fail) | 1.03 | 0 |
| `TryAcquireN(1)` | **15.5** | 0 |
| `TryAcquireN(1)` parallel | **93** | 0 |

---

## `art` — Adaptive Radix Tree

Ordered dictionary supporting exact lookup, prefix scan, and ordered cursor iteration. vs `map`: keys are ordered, prefix queries in O(|prefix|+|result|), no rehash pauses.

```go
import "github.com/uniyakcom/yakutil/art"

var tr art.Tree[int]

tr.Put("user:1001", 42)
tr.Put("user:1002", 99)
tr.Put("session:abc", 1)

v, ok := tr.Get("user:1001")  // 42, true

old, ok := tr.Delete("session:abc")  // 1, true

// Ordered full traversal (lexicographic)
tr.ForEach(func(key string, val int) bool {
    fmt.Println(key, val)
    return true // return false to terminate early
})

// Prefix scan: only traverse keys starting with "user:"
tr.ScanPrefix("user:", func(key string, val int) bool {
    fmt.Println(key, val)
    return true
})

// Cursor iteration: traverse all entries strictly greater than cursor
tr.Seek("user:1001", func(key string, val int) bool {
    fmt.Println(key, val) // "user:1002", 99
    return true
})

// Memory monitoring + compaction (reclaim arena dead bytes after bulk deletions)
fmt.Println(tr.PrefixArenaBytes())
tr.CompactPrefixArena() // call during off-peak
```

**Characteristics**

| Feature | Description |
|------|------|
| Key type | Binary-safe (`string`, may contain `\x00`) |
| Node adaptation | 4 / 16 / 48 / 256 children auto upgrade/downgrade |
| Path compression | Pessimistic prefix compression; prefix stored in arena |
| Ordering | `ForEach` / `Seek` / `ScanPrefix` all lexicographic |
| Memory | `CompactPrefixArena()` reclaims dead bytes after deletions |
| Thread safety | Not thread-safe; wrap with `sync.RWMutex` |

**Time Complexity**

| Operation | Complexity |
|------|--------|
| `Get` / `Put` / `Delete` | O(k), k = key length |
| `ForEach` | O(N) |
| `ScanPrefix` | O(k + \|result\|) |
| `Seek` | O(k + \|result\|) |
| `CompactPrefixArena` | O(N × avg\_prefix\_len) |

**Performance** (Intel Xeon E-2186G @ 3.80GHz, amd64, go1.25):

| Operation | art.Tree | map (comparison) |
|------|----------|------------|
| Put (sequential key) | ~153 ns | ~203 ns |
| Put (random key) | ~497 ns | ~203 ns |
| Get (sequential key) | ~82 ns | ~33 ns |
| Get (random key) | ~135 ns | ~33 ns |
| ScanPrefix | ~1.1 µs | ~1.6 µs (full scan) |

> `ScanPrefix` is ~2× faster than map full scan when prefix hit rate is low; sequential key `art.Put` is slightly faster than map (prefix sharing reduces internal allocations).  
> Random key exact lookup is 4–5× slower than map — the cost of ordering and adaptive node traversal. Prefer standard `map` unless prefix queries are frequent.

---

## `hist` — Equi-Height Histogram

Used for CBO selectivity estimation.

---

## `hll` — HyperLogLog Cardinality Estimator

Estimates the number of distinct elements in a stream.

---

## `sketch` — Count-Min Sketch Frequency Estimator

Estimates the frequency of elements in a data stream.

---

## `topn` — Generic Top-N Selection

```go
import "github.com/uniyakcom/yakutil/topn"

top := topn.TopN[Item](items, 10, func(a, b Item) bool {
    return a.Score > b.Score // descending
})
```

---

## `coarsetime` — Coarse-Grained Clock

500µs precision, ~1 ns/op, single background goroutine per process.

```go
import "github.com/uniyakcom/yakutil/coarsetime"

coarsetime.Start()
defer coarsetime.Stop()

ns := coarsetime.NowNano() // ~1 ns/op vs time.Now() ~40-60 ns/op
t  := coarsetime.Now()     // time.Time
```

Suitable for hot paths where `time.Now()` syscall overhead is unacceptable (e.g., token bucket refill, connection timestamp).

---

## Naming Design

| Decision | Choice | Reason |
|------|------|------|
| Root utilities | `B2S` / `S2B` | Short naming, Go community convention |
| Hash | `Sum64` / `Sum64s` | Following `hash/` stdlib naming |
| Ring | `mpsc.Ring` / `spsc.Ring` | Package-qualified semantics; type name needs no prefix |
| Counter | `percpu.Counter` | Direct semantics |
| Backoff | `backoff.Backoff` | Standard terminology |
| API methods | `Add` `Load` `Push` `Pop` `Spin` `Alloc` `Swap` | Short naming, verb-first |
| Dual-type Map | `Map[V]` / `Map64[V]` | Separated by key type, avoids `interface{}` overhead |
| Timing wheel | `Wheel[T]` | Generic payload, no type assertion needed |

---

## Testing

```bash
# Single run (25 packages)
go test ./... -count=1

# With race detection
go test ./... -count=1 -race

# Single package verbose
go test ./ring/... -v -run TestUnreadByte
```

All 25 packages pass tests (including `-race` race detection).

### Benchmarks

Use the `bench.sh` script in the project root to run all benchmarks at once:

```bash
# Basic run (benchtime=3s, count=1)
./bench.sh

# Custom benchtime and count
./bench.sh 5s 3

# Output file: bench_${OS}_${CORES}c${THREADS}t.txt
# Example: bench_linux_6c12t.txt
```

```bash
# Use benchstat to compare two bench results
go install golang.org/x/perf/cmd/benchstat@latest
benchstat before.txt after.txt
```

### Fuzz Testing

Six packages have Fuzz tests: `ring`, `hll`, `ratelimit`, `art`, `lru`, `sketch`. Use `fuzz_local.sh` for local fuzzing:

```bash
# All targets, 5m each
./fuzz_local.sh

# Single target, custom duration
./fuzz_local.sh art/FuzzPutGet 2m

# Custom duration for all
./fuzz_local.sh 10m
```

CI nightly fuzzing runs automatically via [`.github/workflows/fuzz.yml`](.github/workflows/fuzz.yml).

---

## License

[MIT](LICENSE) © 2026 uniyakcom
