package ratelimit

import (
	"sync/atomic"
	"time"

	"github.com/uniyakcom/yakutil/smap"
	"github.com/uniyakcom/yakutil/topn"
)

// ipEntry bundles a Limiter with a last-used timestamp for eviction.
type ipEntry struct {
	lim      *Limiter
	lastUsed atomic.Int64 // Unix nanoseconds

	// 累计计数（永不重置，Evict 时随条目销毁）
	hits atomic.Int64

	// 两桶滑动分钟窗口（近似，免锁）
	// recentMin：当前 "活跃桶" 对应的 unix_minute
	// recentCount：当前分钟的命中计数
	// prevCount：上一分钟的命中计数（当分钟翻转时由 recentCount 快照而来）
	recentMin   atomic.Int64
	recentCount atomic.Int64
	prevCount   atomic.Int64
}

// addRecentAt 向两桶窗口追加 1 次命中，并在分钟翻转时旋转桶。
// nowMin 为 Unix 分钟（调用方传入，避免重复调用 time.Now）。
// 并发安全：CAS 保证只有一个 goroutine 执行旋转；其余直接 Add(1)。
func (e *ipEntry) addRecentAt(nowMin int64) {
	ts := e.recentMin.Load()
	if nowMin != ts {
		if e.recentMin.CompareAndSwap(ts, nowMin) {
			// 本 goroutine 赢得旋转权：将 recentCount 快照到 prevCount 并清零
			prev := e.recentCount.Swap(0)
			e.prevCount.Store(prev)
		}
		// CAS 失败：其他 goroutine 已完成旋转，直接继续
	}
	e.recentCount.Add(1)
}

// recentHits 返回平滑滑动 1 分钟窗口的近似命中数。
//
// 算法：对前一分钟的计数按"本分钟已过去的秒数"进行线性衰减，
// 再加上本分钟的实际计数。整数运算，无浮点。
//
//	RecentHits ≈ prevCount × (60 − elapsed) / 60 + curCount
func (e *ipEntry) recentHits() int64 {
	now := time.Now().Unix()
	nowMin := now / 60
	elapsed := now % 60 // 本分钟已过去的秒数 [0, 59]

	if e.recentMin.Load() != nowMin {
		// 当前分钟尚未被 addRecent 激活：上一个活跃桶已超过 1min，只看 recentCount
		return e.recentCount.Load()
	}
	prev := e.prevCount.Load()
	cur := e.recentCount.Load()
	return prev*(60-elapsed)/60 + cur
}

// IPMap 管理每个 IP 独立的令牌桶限速器。
//
// 底层使用 smap.Map（maphash AES 分片哈希），适合 yakio OnAccept
// 阶段的 Per-IP 限速，并发安全，零后台 goroutine。
//
// # 典型用法（yakio OnAccept）
//
//	ipmap := ratelimit.NewIPMap(100, 20, 0) // 100 次/秒，突发 20，默认 64 分片
//
//	// 每条新连接
//	if !ipmap.Allow(conn.RemoteIP()) {
//	    conn.Close()
//	    return
//	}
//
//	// 定期清理不活跃条目（防内存泄漏）
//	go func() {
//	    for range time.Tick(5 * time.Minute) {
//	        evicted := ipmap.Evict(10 * time.Minute)
//	        log.Printf("evicted %d stale IP limiters", evicted)
//	    }
//	}()
//
// # TOCTOU 说明
//
// 默认模式：首次访问新 IP 时，两个并发 goroutine 可能同时创建限速器，
// 后者覆盖前者。被覆盖的限速器将 GC 回收。
// 实践中这仅导致新 IP 在极短窗口内多通过 burst*2 请求，无安全风险。
// 若需严格 exactly-once 创建，使用 WithStrictNewIP() 选项。
type IPMap struct {
	m      *smap.Map[*ipEntry]
	rate   int
	burst  int
	strict bool // true = 使用 smap.GetOrSet 保证首次 IP exactly-once 创建
}

// IPMapOption IPMap 构造选项函数。
type IPMapOption func(*IPMap)

// WithStrictNewIP 开启严格新 IP 创建模式。
//
// 开启后，首次访问新 IP 时将使用 smap.GetOrSet（双重检验锁）保证
// 限速器 exactly-once 创建，彻底消除 TOCTOU 少量多通的问题。
// 开启后首次新 IP 的慢路径队得稍高（写锁 vs. 读锁），
// 建议在对首次限流轻微不加喜欢的场景下开启。
func WithStrictNewIP() IPMapOption {
	return func(m *IPMap) { m.strict = true }
}

// IPEntry IP 访问频次统计条目（用于 TopN 返回）。
type IPEntry struct {
	IP         string
	Hits       int64 // 累计 Allow/AllowN/Get 调用次数（永不重置）
	RecentHits int64 // 平滑滑动 1 分钟窗口近似命中数（见 ipEntry.recentHits）
}

// NewIPMap 创建 per-IP 限速管理器。
//   - rate：令牌/秒
//   - burst：桶容量（最大突发量）
//   - shards：smap 分片数（0 = 默认 64）
//   - opts：可选 WithStrictNewIP 等选项
func NewIPMap(rate, burst, shards int, opts ...IPMapOption) *IPMap {
	if shards <= 0 {
		shards = 64
	}
	m := &IPMap{
		m:     smap.New[*ipEntry](shards),
		rate:  rate,
		burst: burst,
	}
	for _, o := range opts {
		o(m)
	}
	return m
}

// getOrCreate 获取或创建指定 IP 的 ipEntry。
//
// 默认模式：利用 unsafe.Pointer + atomic 实现 load-or-store 语义：
//
//  1. 快速路径：smap.Get → 缓存命中，更新 lastUsed，返回
//  2. 慢路径：创建新 entry → smap.Set → 再次 Get（取竞争胜者）
//
// 慢路径极罕见（仅首次连接），两次 Get 间隙的双创会被后续 Get 消除。
//
// strict 模式（WithStrictNewIP）：使用 smap.GetOrSet 双重检验锁，保证 fn 只调一次。
//
// 入口一次获取 nowNano，向下传递，避免多次调用 time.Now()。
func (m *IPMap) getOrCreate(ip string) *ipEntry {
	nowNano := time.Now().UnixNano()       // 入口单次获取时间
	nowMin := nowNano / int64(time.Minute) // 派生分钟（對 recent bucket 用）

	if m.strict {
		// 严格模式： smap.GetOrSet 双重检验锁保证 exactly-once 创建
		e, _ := m.m.GetOrSet(ip, func() *ipEntry {
			ne := &ipEntry{lim: New(m.rate, m.burst)}
			ne.lastUsed.Store(nowNano)
			return ne
		})
		e.lastUsed.Store(nowNano)
		e.hits.Add(1)
		e.addRecentAt(nowMin)
		return e
	}
	// 默认模式：快路径读锁加一次写锁
	if e, ok := m.m.Get(ip); ok {
		e.lastUsed.Store(nowNano)
		e.hits.Add(1)
		e.addRecentAt(nowMin)
		return e
	}
	// 慢路径：首次见到此 IP
	ne := &ipEntry{lim: New(m.rate, m.burst)}
	ne.lastUsed.Store(nowNano)
	m.m.Set(ip, ne)
	// 竞争写后取最终胜出的 entry（若被其他 goroutine 覆盖也 OK）
	if e, ok := m.m.Get(ip); ok {
		e.hits.Add(1)
		e.addRecentAt(nowMin)
		return e
	}
	ne.hits.Add(1)
	ne.addRecentAt(nowMin)
	return ne
}

// maxIPKeyLen IP key 最大允许长度（防超长 key OOM 攻击）。
// IPv6 标准最大文本长度 39B（含 :），加括号和端口最多 ~53B，超过此限视为异常。
const maxIPKeyLen = 128

// Allow 非阻塞检查：ip 是否允许通过（消耗 1 个令牌）。
// 令牌不足时返回 false；~15–25 ns 路径，0 allocs。
// 若 ip 长度超过 128 字节，直接返回 false（拒绝超长 key 的异常请求，防 OOM）。
func (m *IPMap) Allow(ip string) bool {
	if len(ip) > maxIPKeyLen {
		return false
	}
	return m.getOrCreate(ip).lim.Allow()
}

// AllowN 批量消耗 n 个令牌。用于一次写入多条记录等场景。
// 超长 key 直接返回 false。
func (m *IPMap) AllowN(ip string, n int) bool {
	if len(ip) > maxIPKeyLen {
		return false
	}
	return m.getOrCreate(ip).lim.AllowN(n)
}

// Get 返回指定 ip 的 Limiter（未找到则自动创建）。
// 适合需要调用 Reset/Tokens/Rate 诊断的场景。
// 超长 key 返回 nil（调用方应检查返回值）。
func (m *IPMap) Get(ip string) *Limiter {
	if len(ip) > maxIPKeyLen {
		return nil
	}
	return m.getOrCreate(ip).lim
}

// Evict 删除超过 ttl 未活跃的 IP 条目，返回驱逐条目数。
//
// 建议在独立 goroutine 中定期调用（如 5min 一次，ttl=10min），
// 防止短暂高峰引入大量新 IP 导致内存泄漏。
func (m *IPMap) Evict(ttl time.Duration) int {
	cutoff := time.Now().Add(-ttl).UnixNano()
	var evicted int
	var toDelete []string

	m.m.Range(func(ip string, e *ipEntry) bool {
		if e.lastUsed.Load() < cutoff {
			toDelete = append(toDelete, ip)
		}
		return true
	})
	for _, ip := range toDelete {
		m.m.Del(ip)
		evicted++
	}
	return evicted
}

// TopN 返回累计访问频次最高的前 n 个 IP 条目（按 Hits 降序）。
//
// 每个条目同时携带 RecentHits（平滑滑动 1 分钟近似值），方便同时观测长期和近期趋势。
// 被 Evict 驱逐的 IP 其历史不保留。n <= 0 返回 nil。
func (m *IPMap) TopN(n int) []IPEntry {
	if n <= 0 {
		return nil
	}
	var all []IPEntry
	m.m.Range(func(ip string, e *ipEntry) bool {
		all = append(all, IPEntry{
			IP:         ip,
			Hits:       e.hits.Load(),
			RecentHits: e.recentHits(),
		})
		return true
	})
	return topn.ByKey(all, n, func(e IPEntry) int64 { return e.Hits })
}

// TopNRecent 返回近期（滑动 1 分钟窗口）访问频次最高的前 n 个 IP 条目（按 RecentHits 降序）。
//
// 适合实时流量监控：与 TopN 的区别在于排序键为近期频次而非累计频次，
// 能快速发现"刚开始发力"的新攻击 IP。n <= 0 返回 nil。
func (m *IPMap) TopNRecent(n int) []IPEntry {
	if n <= 0 {
		return nil
	}
	var all []IPEntry
	m.m.Range(func(ip string, e *ipEntry) bool {
		rh := e.recentHits()
		if rh > 0 {
			all = append(all, IPEntry{
				IP:         ip,
				Hits:       e.hits.Load(),
				RecentHits: rh,
			})
		}
		return true
	})
	return topn.ByKey(all, n, func(e IPEntry) int64 { return e.RecentHits })
}

// Len 返回当前追踪的 IP 数量（含尚未驱逐的过期条目）。
func (m *IPMap) Len() int { return m.m.Len() }

// Purge 清空所有限速器（测试初始化、滚动配置变更等场景）。
func (m *IPMap) Purge() {
	var keys []string
	m.m.Range(func(k string, _ *ipEntry) bool {
		keys = append(keys, k)
		return true
	})
	for _, k := range keys {
		m.m.Del(k)
	}
}
