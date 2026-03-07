# yakutil
[![Go Version](https://img.shields.io/github/go-mod/go-version/uniyakcom/yakutil)](https://github.com/uniyakcom/yakutil/blob/main/go.mod)
[![Go Reference](https://pkg.go.dev/badge/github.com/uniyakcom/yakutil.svg)](https://pkg.go.dev/github.com/uniyakcom/yakutil)
[![Go Report Card](https://goreportcard.com/badge/github.com/uniyakcom/yakutil)](https://goreportcard.com/report/github.com/uniyakcom/yakutil)
[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](https://opensource.org/licenses/MIT)
[![Lint](https://github.com/uniyakcom/yakutil/actions/workflows/format.yml/badge.svg)](https://github.com/uniyakcom/yakutil/actions/workflows/format.yml)
[![Test](https://github.com/uniyakcom/yakutil/actions/workflows/test.yml/badge.svg)](https://github.com/uniyakcom/yakutil/actions/workflows/test.yml)
[![Fuzz](https://github.com/uniyakcom/yakutil/actions/workflows/fuzz.yml/badge.svg)](https://github.com/uniyakcom/yakutil/actions/workflows/fuzz.yml)

[English](README.md) | **中文**

**uniyak 生态高性能共享基础原语**

零外部依赖 · 泛型 · 无锁 · 零分配优化

```
go get -u github.com/uniyakcom/yakutil
```

> Go 1.25+

---

## 包一览

| 包 | 用途 | 核心类型/函数 |
|---|------|--------------|
| `yakutil` | 根包：工具 + 常量 + 字节转换 + 哨兵错误 | `B2S` `S2B` `IsPow2` `Pow2Ceil` `Native` `ErrStr` `Pad` `NoCopy` |
| `hash` | FNV-1a 64-bit 哈希 + maphash AES 加速 | `Sum64` `Sum64s` `Sum64Map` `Sum64sMap` |
| `percpu` | Per-CPU 无竞争计数/计量 | `Counter` `Gauge` |
| `mpsc` | MPSC 无锁 Ring（Group Commit） | `Ring[T]` |
| `spsc` | SPSC 无等待 Ring | `Ring[T]` |
| `backoff` | 三级自适应退避 | `Backoff` |
| `arena` | CAS Bump 分配器 | `Arena` |
| `bufpool` | 分级字节切片池 | `Pool` / `Get` / `Put` |
| `cow` | COW 原子值（泛型） | `Value[T]` `Swap` `UpdateCAS` |
| `smap` | 分片并发 Map | `Map[V]` `Map64[V]` |
| `swar` | SWAR 字节并行扫描 | `FindByte` `FindQuote` `FindEscape` `HasZero` `HasByte` `HasLess` `FirstByte` |
| `fold` | 快速 ASCII 大小写无关比较 | `Lower` `Equal` `EqualStr` `EqualBytes` |
| `ring` | 自动增长环形字节缓冲 | `Buffer` |
| `wheel` | 泛型时间轮（可配分辨率） | `Wheel[T]` |
| `wpool` | 工作池 + 自适应伸缩 | `Submitter` `Pool` `Stack` `Adaptive` |
| `itable` | 整型 key 高性能查找表 | `Table[V]` |
| `lru` | 分片 LRU 缓存 | `Cache[V]` |
| `ratelimit` | 令牌桶限速器（无锁 CAS，零分配） | `Limiter` |
| `semaphore` | 计数信号量（有界并发控制） | `Semaphore` |
| `art` | 自适应基数树（有序字典，前缀查询） | `Tree[V]` |
| `hist` | 等频直方图（CBO 选择率估计） | `Hist` `Bucket` |
| `hll` | HyperLogLog 基数估计器 | `Sketch` |
| `sketch` | Count-Min Sketch 频率估计器 | `CMS` |
| `topn` | 泛型 Top-N 选择 | `TopN` `ByKey` |
| `coarsetime` | 精度 500µs 粗粒度时钟（~1ns/op） | `NowNano` `Now` `Stop` |

---

## 根包 `yakutil`

零拷贝转换与位运算工具集。

```go
import "github.com/uniyakcom/yakutil"

s := yakutil.B2S(buf)        // []byte 转 string（零拷贝）
b := yakutil.S2B(s)          // string 转 []byte（零拷贝，只读）
yakutil.IsPow2(1024)         // true
yakutil.Pow2Ceil(1000)       // 1024

// 原生字节序
order := yakutil.Native      // binary.BigEndian 或 LittleEndian

// 零分配哨兵错误
const ErrNotFound = yakutil.ErrStr("not found")
fmt.Println(ErrNotFound.Error()) // "not found"

// 缓存行隔离
type HotStruct struct {
    counter int64
    _       yakutil.Pad // 64B 填充，避免 false sharing
    flag    int64
}
```

**常量与类型**

- `CacheLine = 64` — x86/ARM 缓存行大小
- `Pad` — `[64]byte` 填充类型
- `Native` — `binary.ByteOrder`，运行时检测的 CPU 原生字节序
- `ErrStr` — 零分配 string 错误类型，可声明为 `const`

**NoCopy**

嵌入 `yakutil.NoCopy` 后 `go vet copylocks` 会检测非法拷贝：

```go
type MyMutex struct {
    yakutil.NoCopy
    // ...
}
```

---

## `hash` — FNV-1a + maphash 双引擎哈希

### FNV-1a（跨进程一致）

```go
import "github.com/uniyakcom/yakutil/hash"

h := hash.Sum64(key)    // []byte 输入
h := hash.Sum64s("key") // string 输入（零分配）
```

- 与标准库 `hash/fnv.New64a()` 计算结果一致，可跨进程/持久化比较
- 纯计算，无状态分配，可内联
- `Sum64s` 直接遍历 string 底层字节，避免 `[]byte(s)` 转换
- 实测比 `fnv.New64a()` 快 **~2.8×**，零分配（fnv.New64a 每次 1 alloc/8 B 接口装箱开销）

### maphash（AES-NI 加速，进程内）

```go
h := hash.Sum64Map(key)    // []byte 输入，AES-NI 加速
h := hash.Sum64sMap("key") // string 输入（零分配）
```

- 利用 Go runtime 内置随机 seed + AES-NI 硬件指令
- **注意**：seed 在进程重启后随机化，哈希值不可持久化或跨进程比较
- 零分配，适用于进程内路由、sharding、布隆过滤器等

### 性能对比（Intel Xeon E-2186G @ 3.80GHz，Linux amd64，Go 1.25，`bench_linux_6c12t.txt`）

| 数据长度 | FNV `Sum64s` | maphash `Sum64Map` | maphash `Sum64sMap` | maphash/FNV 倍率 |
|---------|-------------|-------------------|---------------------|------------------|
| 8 B     | 1,344 MB/s  | 1,751 MB/s        | 1,272 MB/s          | ×1.3（仅 8B，差距小）|
| 32 B    | 1,712 MB/s  | **6,882 MB/s**    | **5,545 MB/s**      | ×3.2–4.0         |
| 128 B   | 1,257 MB/s  | **15,381 MB/s**   | **14,200 MB/s**     | ×11.3–12.2       |
| 256 B   | 1,194 MB/s  | **14,544 MB/s**   | **13,480 MB/s**     | ×11.3–12.2       |
| 1 KB    | 1,151 MB/s  | **13,016 MB/s**   | **12,781 MB/s**     | ×11.1–11.3       |
| 4 KB    | 1,141 MB/s  | **12,492 MB/s**   | **12,520 MB/s**     | ×10.9–11.0       |

所有实现均 **0 B/op, 0 allocs/op**。

| 实现 | ns/op | allocs | 说明 |
|------|-------|--------|------|
| `Sum64s`（内部 FNV） | **16.4** | 0 / 0 B | 对照 key，推荐替代标准库 |
| `fnv.New64a()`（标准库） | 50.5 | **2 / 40 B** | 接口装箱，慢 ~3.1×，有分配 |

**并行吞吐（32B key，12 线程，`-race=false`）**

| 实现 | ns/op | MB/s | allocs |
|------|-------|------|--------|
| `FNV Sum64s` | **2.04** | 15,707 | 0 |
| `maphash Sum64sMap` | **0.95** | 33,583 | 0 |
| 并行加速比 | — | **×2.1** | — |

### 选用指南

| 场景 | 推荐 | 原因 |
|------|------|------|
| 跨进程、持久化、确定性哈希 | `Sum64` / `Sum64s` | 与 FNV-1a 规范一致，seed 不变 |
| 短 key（≤16 B）进程内 | `Sum64` / `Sum64s` | 性能差距小（≤1.3×），无 seed 限制 |
| 中长 key（≥32 B）进程内路由 | `Sum64Map` / `Sum64sMap` | 吞吐 4–12×，AES-NI 零代价 |
| smap、布隆过滤器、一致性哈希环 | `Sum64Map` / `Sum64sMap` | 进程内不需要稳定 seed |

### 何时选择 [yakhash](https://github.com/uniyakcom/yakhash)

`yakutil/hash` 设计目标是**零外部依赖的轻量内部工具**，覆盖 yakutil 生态内各包的进程内哈希需求。当业务场景超出以下边界时，推荐使用 uniyak 生态专用哈希库 [yakhash](https://github.com/uniyakcom/yakhash)（xxHash XXH64/XXH3 完整实现，AVX2/NEON 汇编加速）：

| 场景 | yakutil/hash | yakhash |
|---|---|---|
| ≥ 1 KB 大块数据高吞吐 | FNV ~1.15 GB/s（纯字节循环，无向量化） | XXH3-64 **41.6 GB/s @ 1 KB，56.2 GB/s @ 10 MB**（差距 **36–49×**） |
| 可重现哈希（固定 seed） | `maphash` 进程随机，重启后不可重现 | `Sum3_64Seed` / `Sum64Seed` |
| 跨进程 / 分布式一致哈希 | `maphash` seed 不可跨进程共享 | `Sum3_64Secret` + `GenSecret` |
| HashDoS 主动防御（可控密钥） | 无 | 192 字节 secret，`Sum3_64Secret` / `Sum3_64Seed` |
| 128 位输出（内容寻址 / 去重） | 无 | `Sum3_128` / `Sum3_128Seed` / `Sum3_128String` |
| 流式分块处理（`io.Reader`） | 无 | `New3()` + `Write` + `Sum64` / `Sum128` |
| 状态快照 / 断点续传 | 无 | `MarshalBinary` / `UnmarshalBinary` |
| C xxHash 0.8.x 逐位兼容 | 无 | ✓ 所有函数与 C 原版逐位相同 |

```go
go get github.com/uniyakcom/yakhash

// 大块数据：AVX2 向量化，56 GB/s（10 MB 档位，同机实测）
h := yakhash.Sum3_64(data)

// 防 HashDoS（跨进程一致密钥）
secret := yakhash.GenSecret(loadSeedFromConfig())
h, _ := yakhash.Sum3_64Secret(key, secret[:])

// 流式分块
d := yakhash.New3()
io.Copy(d, reader)
h := d.Sum64()
```

> `yakutil/hash` 保持零外部依赖；`yakhash` 为 uniyak 生态哈希专用库，两者无传递依赖关系。

---

## `percpu` — Per-CPU 计数器

多核并发计数近零竞争。

```go
import "github.com/uniyakcom/yakutil/percpu"

c := percpu.New(runtime.GOMAXPROCS(0))

c.Add(1)          // 写入分散到不同 cache line slot
total := c.Load() // 聊合所有 slot（近似平均）
c.Reset()         // 将所有 slot 归零
```

**原理**：利用 goroutine 栈地址乘以 **Fibonacci 乘法哈希常数**（`0x9e3779b97f4a7c15` = 2⁶⁴/φ）后右移 16 位，
将调用方映射到 8–256 个 64B 隔离的 slot。写路径仅原子 Add，无跨核竞争。

自动 slot 数量：`max(8, ceilPow2(procs))`，上限 256；低核环境保留 8 slot。

> **为什么用 Fibonacci 哈希而非 `>>13`？**  
> goroutine pool 中所有栈间距固定为 8 KB（1<<13 字节），纯右移 13 位会把它们映射到连续几个 slot（热点堆积）。  
> Fibonacci 乘法将聚集地址打散到全部 slot，分布更均匀。  
> 32 位系统上常量自动截断为 `0x7f4a7c15`（仍为奇数，保持双射性质）。

**选择指南**

| 场景 | 建议 | 原因 |
|------|---------|------|
| 字节数、消息数等监控计数 | `percpu.Counter` | 高频写，近似读，并行 2× 优势 |
| 连接数、错误数、限流判断 | `atomic.Int64` | 需要精确值控制流程 |
| CAS / Swap / 设置任意初始值 | `atomic.Int64` | percpu 不支持 |
| Load 需要严格一致快照 | `atomic.Int64` | percpu.Load 为 O(slots) 聚合，可能读到中间态 |

**性能参考（Intel Xeon E-2186G @ 3.8GHz，12 核，`bench_linux_6c12t.txt`）**

| 操作 | percpu | atomic.Int64 |
|------|--------|-------------|
| Add 串行 | **5.7 ns** | 5.7 ns |
| Add 并行（12C 竞争） | **2.4 ns** ✓ | 18.3 ns ✗（**×7.6 更慢**）|
| Load | 10.6 ns | ~3 ns ✗ |
| Reset | 145 ns | — |

> 串行场景两者相当；高并发写入时 percpu 约 **8× 优势**；Load 比 atomic.Load 慢约 **4×**。
> 因此 percpu 专用于"写远多于读"的监控计数。

**诊断接口 `Stats()`**

```go
st := c.Stats()
// Stats{Slots:64, Min:..., Max:..., Mean:..., Skew:1.0}
// Skew = Max/Mean；值 ≤2.0 均匀，>2.0 提示热点 slot
if st.Skew > 2.0 {
    log.Printf("percpu hot slot detected: skew=%.1f", st.Skew)
}
```

| 字段 | 含义 |
|------|------|
| `Slots` | 总 slot 数 |
| `Min` / `Max` | 最小 / 最大 slot 值 |
| `Mean` | 平均 slot 值 |
| `Skew` | `Max/Mean`，1.0 = 完全均匀；>2.0 提示热点；>5.0 建议加大 procs |

- `Stats()` 为只读诊断，不影响 Add 路径
- 建议在监控 goroutine 中定期采样，`Skew > 5.0` 时考虑增大 `New(procs)` 参数

### `Gauge` — 双向 per-CPU 计量

`Gauge` 支持 Add + Sub（值可为负），适合追踪活跃连接数、并发请求数等可变指标。

```go
g := percpu.NewGauge(runtime.GOMAXPROCS(0))

g.Add(1)       // 新连接进入
g.Sub(1)       // 连接断开（等价于 Add(-1)）
g.Load()       // 聚合所有 slot（近似快照）
g.Reset()      // 归零所有 slot

// 诊断
st := g.Stats() // GaugeStats{Slots, Min, Max, Sum, Mean, Skew}
```

| Counter vs Gauge | Counter | Gauge |
|---|---|---|
| 单调 | 是（只增） | 否（可增可减） |
| Add/Sub | 只有 Add | Add + Sub |
| Load | 近似聚合 | 近似聚合 |
| 典型场景 | 字节数、消息数 | 活跃连接、内存用量 |

> 性能与 Counter 完全一致（相同 Fibonacci 哈希 + 64B slot 布局）。
> yakio 推荐使用 `Gauge` 替代 `atomic.Int64` 追踪活跃连接数。

**实测性能（Intel Xeon E-2186G @ 3.80GHz，12 核，`bench_linux_6c12t.txt`）**

| 操作 | ns/op | allocs | 说明 |
|------|-------|--------|------|
| `Add`（串行） | **5.64** | 0 | Fibonacci 哈希散帽到随机 slot |
| `Add`（12 并行） | **~2.9** | 0 | 12 线程并行，slot 无争用 |
| `Load`（跨 slot 聚合） | — | 0 | 一次遍历所有 slot |

> 对比：`atomic.Int64.Add` 在 12 并行下约 18.3 ns/op（≥6× 差距）。
> yakdb `server/cmd/Metrics.connGauge` 已使用 `percpu.Gauge` 替换。

---

## `mpsc` — MPSC 无锁 Ring + Group Commit

多生产者单消费者队列，适用于 WAL Group Commit 模式。

```go
import "github.com/uniyakcom/yakutil/mpsc"

r := mpsc.New[Record](4096)

// 生产者（多 goroutine）
seq, ok := r.Enqueue(record)
if ok {
    err := r.Wait(seq) // 阻塞直到消费者 Commit
}

// 消费者（单 goroutine）
start, n := r.Drain(func(rec *Record) error {
    return encodeToBuf(rec) // 批量编码
})
flushErr := fsync(buf)
r.Commit(start, n, flushErr) // 唤醒所有生产者
```

**状态机**：`free → filling → ready → drained → free`

- `Enqueue`: CAS tail，写入 slot
- `Wait`: channel 阻塞等待 done 信号（Go runtime 调度，无 busy-spin），然后释放 slot
- `Drain`: 批量收割连续 ready slot
- `Commit`: 设置 `done=1` 唤醒生产者，可携带 batch error

---

## `spsc` — SPSC 无等待 Ring

单生产者单消费者极低延迟队列。

```go
import "github.com/uniyakcom/yakutil/spsc"

r := spsc.New[Event](1024)

// 生产者（单 goroutine）
r.Push(event)

// 消费者（单 goroutine）
if evt, ok := r.Pop(); ok {
    handle(evt)
}
```

- 无 CAS，仅 `atomic.Store` / `atomic.Load`（x86 上等价普通 MOV）
- `cachedHead` / `cachedTail` 消除常态跨核缓存行读取
- 典型吞吐 **2-5 ns/op**

---

## `backoff` — 三级自适应退避

```go
import "github.com/uniyakcom/yakutil/backoff"

var bo backoff.Backoff
for !condition() {
    bo.Spin()
}
bo.Reset()
```

| 阶段 | 迭代范围 | 行为 |
|------|---------|------|
| Phase 1 | `N < 64` | 紧密 CPU 自旋（零开销） |
| Phase 2 | `N < 128` | `runtime.Gosched()` |
| Phase 3 | `N ≥ 128` | 指数 sleep（1µs 到 1ms） |

可自定义：

```go
bo := backoff.Backoff{
    SpinN:   32,
    YieldN:  16,
    MaxWait: 500 * time.Microsecond,
}
```

零值可直接使用（默认参数）。值类型，无分配。

---

## `arena` — CAS Bump 分配器

高并发 bump alloc，适用于 WAL 编码缓冲等短生命周期场景。

```go
import "github.com/uniyakcom/yakutil/arena"

a := arena.New(0) // 默认 64KB chunk

buf := a.Alloc(128) // 8 字节对齐，CAS 并发安全；n≤0 返回 nil
// ... 使用 buf ...

a.Reset() // 切换新 chunk，旧引用等 GC 回收
```

- 常路径 CAS + 加法 = **< 5ns/op**（对比 Go heap ~25ns）
- `n > chunkSize` 时 fallback 到 `make()`
- chunk 耗尽自动切换，旧 chunk 引用仍有效

---

## `bufpool` — 分级字节切片池

20 级 `sync.Pool`（64B 到 32MB），自动按大小分级。请求大小 > 32MB 时直接分配，不经池。

```go
import "github.com/uniyakcom/yakutil/bufpool"

// 全局函数
buf := bufpool.Get(4096)
defer bufpool.Put(buf)

// 独立实例
var p bufpool.Pool
buf := p.Get(1024)
p.Put(buf)
```

- `Get(size)`: 返回 `len=size, cap=2^n` 的切片
- `Put(b)`: 按 `cap` 归还到对应级别
- `cap < 64B` 或 `cap > 32MB` 的切片自动丢弃（> 32MB 直接 `make` 不经池）
- `cap` 非 2^n 的切片自动丢弃（防止 Get 时 `b[:size]` 越界 panic）

---

## `cow` — Copy-on-Write 原子值

读多写少场景的泛型原子快照。

```go
import "github.com/uniyakcom/yakutil/cow"

v := cow.New[Config](defaultConfig)

// 读（任意 goroutine，~1ns）
cfg := v.Load()

// 写（单写者）
v.Store(newConfig)

// 读-改-写
v.Update(func(old Config) Config {
    old.Timeout = 5 * time.Second
    return old
})
```

- 读路径：单次 `atomic.Pointer.Load()`，真正零锁
- 写路径：构造新值，`atomic.Store`
- `Update` 适用于单写者；多写者须外部加锁
- `Swap` 原子替换并返回旧值，并发安全
- `UpdateCAS` 基于 CAS 循环的无锁读-改-写，多写者安全

```go
// 多写者安全的读-改-写
v.UpdateCAS(func(old Config) Config {
    old.Count++
    return old
})

// 原子替换并获取旧值
old := v.Swap(newConfig)
```

---

## `smap` — 分片并发 Map

高性能并发 Map，N 分片 + RWMutex 隔离竞争。string key 路由使用 **maphash AES 加速**（进程内路由专用，seed 随进程重启变化）。

```go
import "github.com/uniyakcom/yakutil/smap"

// string key（maphash AES 加速分片哈希）
m := smap.New[int](64) // 64 分片
m.Set("foo", 42)
v, ok := m.Get("foo")
m.Range(func(k string, v int) bool { return true })

// 原子 get-or-create（双重检验锁，fn 在写锁内执行 exactly-once）
v, created := m.GetOrSet("session:42", func() int {
    return expensiveInit()
})
// created=true 表示本次调用触发了创建

// uint64 key（Fibonacci 哈希）
m64 := smap.New64[string](32)
m64.Set(12345, "hello")
val, created := m64.GetOrSet(12345, func() string { return "world" })
```

- 读路径 RLock 单分片（~22 ns），无全局锁
- 分片间 cache line 隔离
- `GetOrSet`：先 RLock 快速判断是否存在；存在则直接返回（false），不存在升级 Lock 后二次确认再调用 `fn`，保证并发下 `fn` **只执行一次**

**实测性能（`bench_linux_6c12t.txt`）**

| 操作 | ns/op |
|------|-------|
| Get | 23 ns |
| Set | ~34 ns |
| Parallel Get（12t） | 46 ns |

---

## `swar` — SWAR 字节并行扫描

SIMD-Within-A-Register，一次整数同时处理 8 个字节。

```go
import "github.com/uniyakcom/yakutil/swar"

idx := swar.FindByte(data, '\n')   // 查找换行符
idx := swar.FindQuote(data)        // 查找 '"'
idx := swar.FindEscape(data)       // 查找 <0x20 / '"' / '\\'
```

- 典型加速 4-8x vs 单字节循环
- 适用于 JSON 解析、HTTP header 扫描

---

## `fold` — ASCII 大小写无关比较

基于 256B 查找表，零分配。

```go
import "github.com/uniyakcom/yakutil/fold"

fold.Equal([]byte("Content-Type"), "content-type") // true
fold.EqualStr("HOST", "Host")                      // true
```

- 约 **1.78× 快于** `strings.EqualFold`（**~6.4 vs ~11.4 ns**，`bench_linux_6c12t.txt`）
- 仅 ASCII（A-Z 与 a-z），Unicode 场景请用标准库

---

## `ring` — 环形字节缓冲

2^N 自动增长环形缓冲，适用于网络 I/O。

```go
import "github.com/uniyakcom/yakutil/ring"

buf := ring.New(4096)
buf.Write(data)
buf.WriteByte(0x0A)       // 实现 io.ByteWriter，写入单字节（零分配）
p := buf.Peek(10)         // 查看前 10 字节（不消费）
buf.Discard(10)           // 丢弃
c, err := buf.ReadByte()  // 实现 io.ByteReader，逐字节读取（零分配）
buf.UnreadByte()          // 实现 io.ByteScanner，回放最后 ReadByte（零分配）
buf.WriteTo(conn)         // 零拷贝输出
buf.ReadFrom(conn)        // 从 Reader 读入
```

**逐字节 I/O**（`WriteByte` / `ReadByte` / `UnreadByte` 三角接口）：
- 无需分配 `[]byte`，适合帧头解析（magic、version、length 字段）
- `ReadByte` 返回 `(byte, error)`，空缓冲区返回 `io.EOF`
- `UnreadByte` 回滚上一次 `ReadByte`，实现 `io.ByteScanner`，用于 peek-and-rollback 协议解析（"读一字节判类型，不匹配则回放"）
  - 连续两次调用 `UnreadByte` 返回 `io.ErrNoProgress`
- 与 `Read`/`Write` 可混合使用

**`Peek` 说明**：
- 数据未跨边界时：返回指向内部缓冲区的切片（零拷贝），调用方不得修改
- 数据跨边界时：分配新切片并复制（可直接修改）

- 位掩码取模，零分配 wrap
- 自动 2x 扩容并线性化数据
- 结构体 64 B = 恰好 1 条 cache line

**实测吞吐（`bench_linux_6c12t.txt`）**

| 操作 | ns/op | 吞吐 |
|------|-------|------|
| Write 64 B | 7.6 ns | 8,404 MB/s |
| WriteRead 1 KB | 31.0 ns | 33,064 MB/s |
| PeekDiscard | 9.9 ns | — |
| WriteByte | **2.6 ns** | 0 allocs |
| ReadByte | **1.84 ns** | 0 allocs |
| UnreadByte（Write+Read+Unread+Read 往返） | **4.3 ns** | 0 allocs |

**零拷贝 I/O（`ReadableSegments` / `CommitRead` / `WritableSegments` / `CommitWrite`）**

高性能协议解析器（如 RESP）可利用零拷贝接口直接访问内部缓冲区，消除中层 `Read(tmp)` 拷贝：

```go
// 零拷贝读端
// 返回指向内部缓冲区的一或两段，回环时有两段（s2 非 nil）
s1, s2 := buf.ReadableSegments(n)  // 调用方禁止写入
parse(s1)                          // 绻成零拷贝解析
if s2 != nil { parse(s2) }         // 內容跨边界时处理第二段
buf.CommitRead(n)                  // 推进读指针

// 零拷贝写端
s1, s2 := buf.WritableSegments(need) // 获取可写内存段
copy(s1, data)                       // 直接写入内部缓冲区
buf.CommitWrite(n)                   // 确认写入 n 字节
```

> 返回的 `s1`/`s2` 直接指向内部缓冲区，无需拷贝。消费 k 字节后必须调用 `CommitRead(k)` 推进读指针。

**32 位溢出自动防护**

`r`、`w`、`mask` 字段使用 `uint`（平台原生无符号整型）：

| 平台 | 字段宽度 | 溢出门槛 | 结论 |
|------|---------|---------|------|
| 64 位 | uint64 | ~18 EB | 实际不可达 |
| 32 位 | uint32 | 4 GB | Go slice 上限 2 GB，也不可达 |

无符号溢出自然回绕，`Len() = w - r` 始终正确；持续非空流场景**无需手动调用 `Reset()`**。

---

## `wheel` — 泛型时间轮

可配置分辨率的高性能定时器。

```go
import "github.com/uniyakcom/yakutil/wheel"

w := wheel.New[ConnID](10*time.Millisecond, 1024)
id := w.Add(5*time.Second, connID)  // O(1) 添加
w.Cancel(id)                         // O(1) 取消

// 手动推进
w.Advance(func(id ConnID) { close(id) })

// 或自动 tick
w.Run(ctx, func(id ConnID) { close(id) })
```

- Add/Cancel O(1)，Advance O(expired)
- 支持分层 rounds（超出 slots 数的长延迟）
- sync.Pool 复用 entry 节点

---

## `wpool` — 工作池 + 自适应伸缩

### Submitter 接口

`Pool`、`Stack`、`Adaptive` 均实现 `Submitter`，调用方无需关心底层实现：

```go
type Submitter interface {
    Submit(task func()) bool    // 阻塞直到任务被接受或池已停止（Pool：等队列空间；Stack：等空闲 worker）
    TrySubmit(task func()) bool // 非阻塞；无法立即接受时返回 false
    Running() int               // 当前活跃 worker 数
    Stop()                      // 优雅停止，等待所有 worker 完成
    PanicCount() int64          // 生命周期内任务 panic 总次数（无论是否设置 handler 均递增）
}
```

### TimedSubmitter 接口

`Pool` 与 `Stack` 额外实现 `TimedSubmitter`，提供超时背压控制：

```go
type TimedSubmitter interface {
    Submitter
    SubmitTimeout(task func(), timeout time.Duration) bool
    // 至多等待 timeout：Pool 等队列空位，Stack 等空闲 worker。
    // 超时或池已停止返回 false。
}
```

**适用场景**：
```go
// HTTP handler 最多等 50ms 等待 worker，超时直接返回 503
ok := pool.SubmitTimeout(func() { handle(req) }, 50*time.Millisecond)
if !ok {
    http.Error(w, "503 overloaded", http.StatusServiceUnavailable)
    return
}
```

### Pool — FIFO 基础工作池

```go
import "github.com/uniyakcom/yakutil/wpool"

// 基础池（FIFO channel 调度）
p := wpool.NewPool(8, 5*time.Second) // 8 workers, 5s 空闲超时
p.Submit(func() { handle(req) })
p.TrySubmit(func() { handle(req) }) // 非阻塞
p.Resize(16)                         // 动态扩容
p.Stop()                             // 优雅停止
```

- 队列水位 >75% / idle >50% 由 `Adaptive.adjust()` 自动伸缩（`Pool` 本身不自动伸缩）
- 通过 `Resize(n)` 手动调整 worker 数；动态缩容时多余 worker 在空闲后自动退出
- `Submit` / `TrySubmit` 阻塞/非阻塞两种模式
- `safeRun` 委托 `panicSafeRun`（`wpool/safe.go`），Pool 与 Stack 共享同一实现
- 单任务 panic 不影响 worker 存活；每次 panic 先计数再调 handler
- `PanicCount() int64` 累计 panic 次数（无论是否设置 handler 均计数）

**PanicCount 使用示例**

```go
// handler 签名：func(any, []byte) — 第二参数为 debug.Stack() 输出的堆栈快照
p := wpool.NewPool(8, 5*time.Second, wpool.WithPanicHandler(func(r any, stack []byte) {
    slog.Error("worker panic", "err", r, "stack", string(stack))
}))

// 定期上报到监控（Prometheus、OTEL 等）
go func() {
    for range time.Tick(30 * time.Second) {
        metrics.Set("wpool_panics_total", float64(p.PanicCount()))
    }
}()
```

> `PanicCount` 与 `WithPanicHandler` 相互独立：无 handler 时也递增，有 handler 时先计数再调 handler。

### Stack — FILO 热 Worker 池

```go
// FILO 栈（最近使用的 worker 优先复用，CPU cache 亲和）
s := wpool.NewStack(8, 10*time.Second) // 8 workers, 10s 空闲超时
s.Submit(func() { handle(req) })
s.TrySubmit(func() { handle(req) })
s.PanicCount() // 累计 panic 次数，同 Pool.PanicCount
s.Stop()
```

**FILO vs FIFO 选用原则：**

| 场景 | 推荐 |
|------|------|
| 网络 IO reactor 分发、协议解析（短耗时、高并发） | `Stack`（FILO） |
| 通用后台任务、任务耗时差异大 | `Pool`（FIFO） |

**Stack 设计细节：**
- per-worker `chan(capacity=1)` 避免 Submit 与 workerFunc 互相阻塞
- `sync.Pool` 复用 `stackWorkerChan`，减少 GC 分配
- `sync.Cond` 在 `maxWorkers` 达上限时零 CPU 阻塞等待
- 后台 cleaner 定期回收空闲超时 worker（默认 10s）
- **`Stop()` 保证**：等待所有 worker goroutine 完全退出（包含正在执行任务的 worker），调用返回后 `Running() == 0`，无 goroutine 泄漏

### Adaptive — 自适应伸缩池

```go
// 自适应池（基于队列水位动态伸缩，内部使用 Pool FIFO）
a := wpool.NewAdaptive(4, 64, 500*time.Millisecond, 5*time.Second)
// 参数: min=4, max=64, 采样周期=500ms, 空闲超时=5s
a.Submit(func() { handle(req) })
a.Stop()
```

- 队列水位 >75%：扩容（+25% worker，不超过 max）
- idle worker >50%：缩容（-25% worker，不低于 min）
- `PanicCount() int64` 委托内部 Pool，与 Pool 共用同一计数器

### 接口互换示例

```go
// 依赖接口而非具体类型，便于切换和测试
func NewDispatcher(pool wpool.Submitter) *Dispatcher {
    return &Dispatcher{pool: pool}
}

// reactor IO 场景：使用 FILO Stack
d := NewDispatcher(wpool.NewStack(runtime.NumCPU(), 0))

// 通用后台任务：使用 FIFO Pool
d := NewDispatcher(wpool.NewPool(8, 0))

// 有明确背压截止时间：使用 TimedSubmitter
var ts wpool.TimedSubmitter = wpool.NewStack(8, 0)
ts.SubmitTimeout(task, 50*time.Millisecond)
```

**实测性能（`bench_linux_6c12t.txt`）**

| 实现 | Submit（串行） | TrySubmit | Submit（并行） |
|------|-------------|-----------|-------------|
| `Pool` FIFO | 448 ns | **16 ns** | 382 ns |
| `Stack` FILO | **418 ns** | 41 ns | 409 ns |
| `go spawn` | 236 ns | — | — |
| `Stack vs Pool` direct | 408 ns FILO | — | 713 ns FIFO |

> `TrySubmit` 最快（16 ns），适合非阻塞分发。  
> `Stack.Submit`（FILO）对 CPU cache 亲和，适合低延迟网络 IO 分发场景。  
> `SubmitTimeout` 在已有 worker 空闲时与 Submit 延迟相当；超时路径经内部 timer 复用池，运行时分配低。

**SubmitTimeout 实测性能（`bench_linux_6c12t.txt`）**

| 实现 | ns/op（串行） | allocs | 说明 |
|------|-------------|--------|------|
| `Pool.SubmitTimeout` | 611 ns | **0（0B）** | 经 timer 复用池优化，零分配 |
| `Stack.SubmitTimeout` | **471 ns** | 53 B / 0 alloc | FILO 复用 worker，timer token 复用 |
| `Pool.SubmitTimeout`（并行） | 521 ns | 0 | — |
| `Stack.SubmitTimeout`（并行） | **630 ns** | 72 B / 1 alloc | — |

> `Stack.SubmitTimeout` 比 `Pool.SubmitTimeout` 快约 **23%**。`Pool.SubmitTimeout` 经 timer 复用池优化为 **0 alloc/0 B**（详见 `pool.go` `poolTimerPool`）。`Stack.SubmitTimeout` 串行报告 53 B/0 alloc（sync.Pool 回收前碎片化平均値，实际分配次数远小于 1）；并行路径 72 B/1 alloc（详见 `stack.go` `tokenPool`）。

---

## `itable` — 整型 key 查找表

小 key 数组直查 + 大 key sync.Map 回退。

```go
import "github.com/uniyakcom/yakutil/itable"

tb := itable.New[Conn](0)  // 默认 65536 快速路径
tb.Set(fd, &conn)          // O(1) atomic store
conn, ok := tb.Get(fd)     // O(1) atomic load
tb.Del(fd)
```

- key < 65536：`atomic.Pointer` 数组，零锁无竞争
- key ≥ 65536：`sync.Map` 回退
- 适用于 fd、连接 ID 等密集整数场景

---

## `lru` — 分片 LRU 缓存

多分片减少锁竞争，**maphash AES 加速**分片路由，支持可选惰性 TTL。

```go
import "github.com/uniyakcom/yakutil/lru"

// 基础用法：16 分片，每分片 10000 条
c := lru.New[[]byte](16, 10000)
c.Set("key", data)
val, ok := c.Get("key")
c.Del("key")

// 驱逐回调（LRU 容量淘汰时触发，TTL 过期不触发）
c = lru.New[[]byte](16, 10000, lru.WithEvict[[]byte](func(k string, v []byte) {
    log.Printf("evicted %s", k)
}))

// 惰性 TTL：Get 时检查过期，无后台 goroutine
c = lru.New[[]byte](16, 10000, lru.WithTTL[[]byte](12*time.Hour))

// 组合：TTL + 驱逐回调
c = lru.New[[]byte](16, 10000,
    lru.WithTTL[[]byte](5*time.Minute),
    lru.WithEvict[[]byte](func(k string, v []byte) { ... }),
)

// 遍历所有存活条目（最新→最旧，跳过已过期的 TTL 条目）
c.Range(func(k string, v []byte) bool {
    fmt.Println(k)
    return true // return false 可提前终止
})

// 清空全部条目（不触发 evict 回调）
c.Purge()
```

- `Get` ~27–28 ns，`Set` ~27 ns（`bench_linux_6c12t.txt`）
- Parallel Get（12t）：~86 ns
- 每分片独立 Mutex + 双向链表
- 超出容量自动驱逐最久未使用（LRU）
- `Range`：快照迭代，在锁外回调 fn，跳过 TTL 已过期条目，return false 提前终止
- `Purge`：O(shards) 清空，不触发 evict 回调
- `WithTTL`：覆写同一 key 时 TTL 重置；`Len()` 含惰性未清理的过期条目

---

## `ratelimit` — 令牌桶限速器

无锁 CAS 令牌桶，无后台 goroutine，零分配，适合 yakio Per-IP / 全局限速。

```go
import "github.com/uniyakcom/yakutil/ratelimit"

// 全局限速：1000 次/秒，突发上限 200
rl := ratelimit.New(1000, 200)

// 非阻塞检查（热路径，~15-25 ns，0 allocs）
if !rl.Allow() {
    conn.Close()
    return
}

// 批量消耗（一次写入多条消息）
if !rl.AllowN(5) {
    return ErrRateLimited
}

// 运行时诊断
rl.Tokens()  // 当前可用令牌数快照
rl.Rate()    // 设置的速率（令牌/秒）
rl.Burst()   // 桶容量
rl.Reset()   // 重置为满桶（连接重置、测试初始化）
```

**性能**（Intel Xeon E-2186G @ 3.80GHz，amd64，go1.25）：

| 场景 | 延迟 | 分配 |
|------|------|------|
| `Allow`（单核，令牌充足） | ~15-25 ns | 0 allocs |
| `Allow`（12 线程并行） | ~5-15 ns | 0 allocs |
| `Allow`（令牌耗尽，快速拒绝） | ~10-20 ns | 0 allocs |

**算法要点**：
- 懒惰补充（每次调用时按时间差计算），避免后台 timer goroutine
- 补充计算使用整数纳秒（`1e9 / rate`），无浮点误差
- CAS 循环：先读快照 → 计算新状态 → CAS 更新，竞争时重试（通常 1-2 次收敛）
- `n > burst` 永远返回 `false`（可提前短路，无需等待）

**高性能场景：注入粗粒度时钟（`WithClock`）**

热路径（如 TCP Accept 循环）中，`time.Now()` 的系统调用开销约 40–60 ns；
使用 `coarsetime.NowNano`（精度 500µs，~1 ns/op）可将时钟开销降低 40–60×：

```go
import (
    "github.com/uniyakcom/yakutil/coarsetime"
    "github.com/uniyakcom/yakutil/ratelimit"
)

// 启动粗粒度时钟（整个进程共享一个后台 goroutine）
coarsetime.Start()
defer coarsetime.Stop()

// 注入粗粒度时钟，Allow 热路径时钟开销 ~1ns
rl := ratelimit.New(10_000, 500,
    ratelimit.WithClock(coarsetime.NowNano),
)

// 用法与普通 Limiter 完全相同
if !rl.Allow() {
    return ErrRateLimited
}
```

> **适用场景**：单机 >10 万 QPS 的高并发链路；精度要求宽松（500µs 误差在令牌桶
> 粒度上通常可忽略）。精确控速（如精确流量整形）场景请使用默认 `time.Now()`。

**使用场景**：
- yakio 全局新连接速率（`OnAccept` 前）
- Per-IP 消息速率：使用内置 `IPMap` 管理每 IP 独立限速器
- HTTP 接口 QPS 保护

### `IPMap` — Per-IP 限速管理器

`IPMap` 将 `smap.Map`（maphash AES-NI 分片哈希）与 `*Limiter` 结合，无后台 goroutine，零分配路径。

```go
ipmap := ratelimit.NewIPMap(100, 20, 0) // rate=100/s, burst=20, 默认 64 分片

// 每条新连接入口（OnAccept）
if !ipmap.Allow(conn.RemoteIP()) {
    conn.Close()
    return
}

// 批量操作
if !ipmap.AllowN(ip, 5) { return ErrRateLimited }

// 获取底层 Limiter（诊断）
lim := ipmap.Get(ip)
log.Printf("ip=%s tokens=%d", ip, lim.Tokens())

// 定期驱逐不活跃 IP（防内存泄漏）
go func() {
    for range time.Tick(5 * time.Minute) {
        n := ipmap.Evict(10 * time.Minute)
        log.Printf("evicted %d stale IP limiters", n)
    }
}()

// 清空所有（配置变更/测试初始化）
ipmap.Purge()
```

```go
// 严格模式：消除首次创建 Limiter 的 TOCTOU 竞争（exactly-once 语义）
ipmap := ratelimit.NewIPMap(100, 20, 0, ratelimit.WithStrictNewIP())
```

```go
// 访问热点分析：返回访问次数最多的前 N 个 IP（Allow + AllowN 均计入）
top := ipmap.TopN(5)
for _, e := range top {
    log.Printf("ip=%s hits=%d recent=%d", e.IP, e.Hits, e.RecentHits)
}

// 近期热点分析：按最近 ~1min 滑动窗口活跃度排序（适合实时告警）
recent := ipmap.TopNRecent(5)
for _, e := range recent {
    log.Printf("ip=%s recent_hits=%d", e.IP, e.RecentHits)
}
```

| 方法 | 说明 |
|------|------|
| `Allow(ip)` | 消耗 1 个令牌，false 表示受限 |
| `AllowN(ip, n)` | 批量消耗 n 个令牌 |
| `Get(ip)` | 获取（或创建）底层 `*Limiter` |
| `Evict(ttl)` | 驱逐 ttl 内不活跃的 IP，返回驱逐数 |
| `Len()` | 当前追踪 IP 数 |
| `Purge()` | 清空所有条目 |
| `TopN(n)` | 返回访问次数 Top-N 的 `[]IPEntry`（按 `Hits` 累计降序） |
| `TopNRecent(n)` | 返回近期（~1min 滑动窗口）最活跃的 Top-N `[]IPEntry`（按 `RecentHits` 降序） |

**`WithStrictNewIP()` 说明**：  
默认模式下首次创建 IP Limiter 时存在微小 TOCTOU 窗口（近似限速可接受）；启用严格模式后，内部通过 `smap.GetOrSet` 序列化写分片，保证 Limiter **exactly-once** 初始化，适合对限速精确度有要求的场景。

**`IPEntry` 结构**：
```go
type IPEntry struct {
    IP         string
    Hits       int64  // Allow + AllowN 的累计调用次数
    RecentHits int64  // 近 1min 滑动窗口估算计数（线性插值平滑）
}
```

**滑动窗口算法**：采用 2-bucket 分钟桶轮换（`recentMin`/`recentCount`/`prevCount` 三个原子字段），CAS 保证桶切换的并发安全，无锁。`recentHits()` 返回：`prevCount × (60-elapsed)/60 + curCount`，在分钟边界处平滑过渡而非突跳。

**实测性能（`bench_linux_6c12t.txt`，rate=1M/s, burst=100K）**

| 操作 | ns/op | allocs | 说明 |
|------|-------|--------|------|
| `Allow`（串行，热 IP） | 121 | 0 | maphash 分片 + token-bucket CAS |
| `Allow`（12 并行） | 48 | 1 / 8 B | 分片锁散射并发 |
| `Allow`（含 RecentHits） | 118 | 0 | addRecent 与通常 Allow 等开销 |
| `TopNRecent(5, 100 IPs)` | 11.4 µs | 9 / 12.8 KB | 堆选 top-N，偶发调用 |


> yakdb `server/server.go` 已集成 `IPMap` 为 `ConnRatePerIP` 选项，在 TCP Accept 层过滤连接风暴。

---

## `semaphore` — 计数信号量

channel 底层，Go runtime 原生调度，支持非阻塞 / context 超时，适合 yakio MaxConns 限制。

```go
import "github.com/uniyakcom/yakutil/semaphore"

sem := semaphore.New(opts.MaxConns) // 最大并发连接数

// 新连接入口：非阻塞快速拒绝
if !sem.TryAcquire() {
    conn.Close()
    return ErrTooManyConns
}
defer sem.Release()

// 客户端侧 dial 控制：带 context 的阻塞等待
if err := sem.AcquireContext(ctx); err != nil {
    return err // context.DeadlineExceeded or Canceled
}
defer sem.Release()

// 运行时诊断（Prometheus gauge）
sem.Count()     // 当前持有许可数（活跃连接数）
sem.Cap()       // 最大容量
sem.Available() // 剩余可用许可数
```

**性能**（Intel Xeon E-2186G @ 3.80GHz，amd64，go1.25）：

| 场景 | 延迟 | 分配 |
|------|------|------|
| `Acquire` + `Release`（顺序） | ~29 ns | 0 allocs |
| `TryAcquire`（有余量） | ~28 ns | 0 allocs |
| `TryAcquire`（满载，default 分支） | ~1 ns | 0 allocs |
| `Acquire` + `Release`（12 线程并行） | ~33 ns | 0 allocs |

**API 对比**：

| | `Acquire` | `TryAcquire` | `AcquireContext` | `TryAcquireN(n)` |
|---|---|---|---|---|
| 阻塞 | 是（无限等待） | 否（立即返回） | 是（直到 ctx 取消） | 否（立即返回） |
| 数量 | 1 | 1 | 1 | n 个（全或无） |
| 适合 | 内部后台任务 | 新连接入口 | 客户端拨号 | 批量写槽位预留 |

**批量操作 `TryAcquireN` / `ReleaseN`**：

```go
// 一次预留 n 个槽位（全部成功才占用；任意失败则原子回滚）
if !sem.TryAcquireN(n) {
    return ErrNotEnoughSlots
}
defer sem.ReleaseN(n)
// 安全地使用 n 个槽位...
```

- `TryAcquireN(n) bool`：全或无原子批量获取 n 个许可；n > Cap() 永远返回 false
- `ReleaseN(n)`：批量释放 n 个许可，与 `TryAcquireN` 配对；n 超出已持有量 panic

**实测性能（`bench_linux_6c12t.txt`）**

| 操作 | ns/op | allocs |
|------|-------|--------|
| `Acquire`/`Release`（串行） | 28.8 | 0 |
| `TryAcquire_Success`（串行） | 27.9 | 0 |
| `TryAcquire_Full`（fast fail） | 1.03 | 0 |
| `TryAcquireN(1)` | **15.5** | 0 |
| `TryAcquireN(1)` 并行 | **93** | 0 |
- 适用场景：yakdb 批量写入槽位预留、连接池批量借出等需要原子"全有或全无"的场景

---

## `art` — 自适应基数树

有序字典，支持精确查找、前缀扫描和有序游标迭代。相比 `map`：键有序、前缀查询 O(|prefix|+|result|)、无 rehash 停顿。

```go
import "github.com/uniyakcom/yakutil/art"

var tr art.Tree[int]

// 增
tr.Put("user:1001", 42)
tr.Put("user:1002", 99)
tr.Put("session:abc", 1)

// 查
v, ok := tr.Get("user:1001")  // 42, true

// 删（返回被删除的旧值）
old, ok := tr.Delete("session:abc")  // 1, true

// 有序全遍历（字典序）
tr.ForEach(func(key string, val int) bool {
    fmt.Println(key, val)
    return true // 返回 false 提前终止
})

// 前缀扫描：只遍历 "user:" 开头的 key
tr.ScanPrefix("user:", func(key string, val int) bool {
    fmt.Println(key, val)
    return true
})

// 游标迭代：遍历字典序严格大于 cursor 的所有条目
tr.Seek("user:1001", func(key string, val int) bool {
    fmt.Println(key, val) // "user:1002", 99
    return true
})

// 内存监控 + 压缩（大量删除后回收 arena 死字节）
fmt.Println(tr.PrefixArenaBytes()) // 当前 arena 占用
tr.CompactPrefixArena()            // 业务低峰期调用
```

**特性**

| 特性 | 说明 |
|------|------|
| 键类型 | 二进制安全（`string`，可含 `\x00`） |
| 节点自适应 | 4 / 16 / 48 / 256 子节点自动升降级 |
| 路径压缩 | Pessimistic 前缀压缩，prefix 集中存于 arena |
| 有序性 | `ForEach` / `Seek` / `ScanPrefix` 均按字典序 |
| 内存 | `CompactPrefixArena()` 回收删除遗留死字节 |
| 线程安全 | 非线程安全，调用方加 `sync.RWMutex` |

**时间复杂度**

| 操作 | 复杂度 |
|------|--------|
| `Get` / `Put` / `Delete` | O(k)，k = key 长度 |
| `ForEach` | O(N) |
| `ScanPrefix` | O(k + \|result\|) |
| `Seek` | O(k + \|result\|) |
| `CompactPrefixArena` | O(N × avg\_prefix\_len) |

**性能**（Intel Xeon E-2186G @ 3.80GHz，amd64，go1.25）：

| 操作 | art.Tree | map（对比） |
|------|----------|------------|
| Put（顺序 key） | ~153 ns | ~203 ns |
| Put（随机 key） | ~497 ns | ~203 ns |
| Get（顺序 key） | ~82 ns | ~33 ns |
| Get（随机 key） | ~135 ns | ~33 ns |
| ScanPrefix | ~1.1 µs | ~1.6 µs（全扫） |

> `ScanPrefix` 在前缀命中率低时比 map 全扫快 ~2×；顺序 key 下 art.Put 略快于 map（前缀共享减少内部分配）。
> 随机 key 下精确查找比 map 慢 4–5×，是有序性和自适应节点遍历的代价。除前缀查询频繁外，优先考虑标准 `map`。

---

## 命名设计

| 决策 | 选择 | 理由 |
|------|------|------|
| 根工具 | `B2S` / `S2B` | 短命名，Go 社区惯例 |
| 哈希 | `Sum64` / `Sum64s` | 遵循 `hash/` 标准库命名 |
| Ring | `mpsc.Ring` / `spsc.Ring` | 包名限定语义，类型名无需前缀 |
| 计数器 | `percpu.Counter` | 直达语义 |
| 退避 | `backoff.Backoff` | 标准术语 |
| API 方法 | `Add` `Load` `Push` `Pop` `Spin` `Alloc` `Swap` | 短命名，动词优先 |
| 双类型 Map | `Map[V]` / `Map64[V]` | 按 key 类型分离，避免 interface{} 开销 |
| 时间轮 | `Wheel[T]` | 泛型载荷，无需类型断言 |

---

## 测试

```bash
# 单次测试（25 个包）
go test ./... -count=1

# 含竞态检测
go test ./... -count=1 -race

# 单包详细输出
go test ./ring/... -v -run TestUnreadByte
```

所有 25 个包均通过测试（含 `-race` 竞态检测）。

### 基准测试

使用项目根目录的 `bench.sh` 脚本一键运行全量基准：

```bash
# 基础运行（benchtime=3s，count=1）
./bench.sh

# 自定义 benchtime 和 count
./bench.sh 5s 3

# 参数说明
#   $1 = benchtime（默认 3s）
#   $2 = count（默认 1）
#
# 输出文件：bench_${OS}_${CORES}c${THREADS}t.txt
# 例如：bench_linux_6c12t.txt
```

脚本对每个包执行 `go test -bench=. -benchmem -benchtime=$1 -count=$2`，
生成含完整 CPU 拓扑和 Go runtime 信息的报告文件，可直接与 `benchstat` 对比。

```bash
# 使用 benchstat 统计两次 bench 结果的差异
go install golang.org/x/perf/cmd/benchstat@latest
benchstat before.txt after.txt
```

### Fuzz 测试

六个包含有 Fuzz 测试：`ring`、`hll`、`ratelimit`、`art`、`lru`、`sketch`。本地 Fuzz 使用 `fuzz.sh`：

```bash
# 全部目标，每个 5m
./fuzz.sh

# 单目标，自定义时长
./fuzz.sh art/FuzzPutGet 2m

# 全部目标，自定义时长
./fuzz.sh 10m
```

CI 夜间 Fuzz 任务通过 [`.github/workflows/fuzz.yml`](.github/workflows/fuzz.yml) 自动运行。

---

## License

[MIT](LICENSE) © 2026 uniyak.com
