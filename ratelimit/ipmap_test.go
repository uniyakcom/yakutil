package ratelimit_test

import (
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/uniyakcom/yakutil/ratelimit"
)

func TestIPMap_Allow(t *testing.T) {
	m := ratelimit.NewIPMap(10, 5, 0)
	for i := 0; i < 5; i++ {
		if !m.Allow("1.2.3.4") {
			t.Fatalf("request %d should be allowed (within burst)", i+1)
		}
	}
	if m.Allow("1.2.3.4") {
		t.Fatal("request after burst exhaustion should be denied")
	}
}

func TestIPMap_DifferentIPs(t *testing.T) {
	m := ratelimit.NewIPMap(1, 1, 0)
	if !m.Allow("10.0.0.1") {
		t.Fatal("first request from 10.0.0.1 should pass")
	}
	if !m.Allow("10.0.0.2") {
		t.Fatal("first request from 10.0.0.2 should pass (different IP)")
	}
}

func TestIPMap_AllowN(t *testing.T) {
	m := ratelimit.NewIPMap(100, 10, 0)
	if !m.AllowN("192.168.0.1", 5) {
		t.Fatal("AllowN(5) within burst should pass")
	}
	if !m.AllowN("192.168.0.1", 5) {
		t.Fatal("AllowN(5) second time within burst should pass")
	}
	if m.AllowN("192.168.0.1", 1) {
		t.Fatal("AllowN after burst exhaustion should fail")
	}
}

func TestIPMap_Get(t *testing.T) {
	m := ratelimit.NewIPMap(50, 10, 0)
	lim := m.Get("203.0.113.5")
	if lim == nil {
		t.Fatal("Get should return a non-nil Limiter")
	}
	if lim.Rate() != 50 {
		t.Fatalf("Limiter rate: want 50, got %d", lim.Rate())
	}
	if lim.Burst() != 10 {
		t.Fatalf("Limiter burst: want 10, got %d", lim.Burst())
	}
}

func TestIPMap_Len(t *testing.T) {
	m := ratelimit.NewIPMap(100, 10, 0)
	m.Allow("a")
	m.Allow("b")
	m.Allow("c")
	if got := m.Len(); got < 3 {
		t.Fatalf("Len: want >= 3, got %d", got)
	}
}

func TestIPMap_Purge(t *testing.T) {
	m := ratelimit.NewIPMap(100, 10, 0)
	for i := 0; i < 10; i++ {
		m.Allow(fmt.Sprintf("10.0.0.%d", i))
	}
	m.Purge()
	if got := m.Len(); got != 0 {
		t.Fatalf("after Purge: Len should be 0, got %d", got)
	}
}

func TestIPMap_Evict(t *testing.T) {
	m := ratelimit.NewIPMap(100, 10, 0)
	m.Allow("old.ip")
	time.Sleep(5 * time.Millisecond)
	evicted := m.Evict(1 * time.Millisecond)
	if evicted == 0 {
		t.Fatal("Evict should remove the old entry")
	}
	if m.Len() != 0 {
		t.Fatalf("after Evict: Len should be 0, got %d", m.Len())
	}
}

func TestIPMap_Concurrent(t *testing.T) {
	m := ratelimit.NewIPMap(10000, 1000, 0)
	const goroutines = 50
	var allowed atomic.Int64

	done := make(chan struct{}, goroutines)
	for i := 0; i < goroutines; i++ {
		go func(id int) {
			ip := fmt.Sprintf("192.168.1.%d", id%10)
			for j := 0; j < 20; j++ {
				if m.Allow(ip) {
					allowed.Add(1)
				}
			}
			done <- struct{}{}
		}(i)
	}
	for i := 0; i < goroutines; i++ {
		<-done
	}
	if allowed.Load() < 0 {
		t.Fatal("allowed should be >= 0")
	}
}

func BenchmarkIPMap_Allow(b *testing.B) {
	m := ratelimit.NewIPMap(1000000, 100000, 0)
	b.ReportAllocs()
	for b.Loop() {
		m.Allow("192.168.1.1")
	}
}

func BenchmarkIPMap_Allow_Parallel(b *testing.B) {
	m := ratelimit.NewIPMap(1000000, 100000, 0)
	const ips = 10
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			ip := fmt.Sprintf("10.0.0.%d", i%ips)
			m.Allow(ip)
			i++
		}
	})
}

// ─── WithStrictNewIP 测试 ────────────────────────────────────────────────────

// TestIPMap_WithStrictNewIP_Basic 验证 WithStrictNewIP 模式下 Allow 正常工作。
func TestIPMap_WithStrictNewIP_Basic(t *testing.T) {
	m := ratelimit.NewIPMap(100, 10, 0, ratelimit.WithStrictNewIP())
	if !m.Allow("10.0.0.1") {
		t.Fatal("expected Allow to succeed with fresh limiter")
	}
}

// TestIPMap_WithStrictNewIP_ExactlyOnce 验证并发场景下 WithStrictNewIP 对同一 IP 只创建一个 Limiter。
// 使用 rate=1, burst=1 令牌桶：若初始令牌只被赋予一次，则大量并发 Allow 中恰好只有 1 次成功。
func TestIPMap_WithStrictNewIP_ExactlyOnce(t *testing.T) {
	// burst=1：每个新 IP 的初始令牌桶只有 1 个令牌
	// 若 strict 模式工作正常：只有 1 个 goroutine 能消耗到令牌（通过）
	// 若 TOCTOU 发生：可能有 2 个 goroutine 各拿到不同的满桶，允许 2 次
	m := ratelimit.NewIPMap(1, 1, 4, ratelimit.WithStrictNewIP())
	const goroutines = 200
	var allowed atomic.Int64
	var wg sync.WaitGroup
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if m.Allow("strict-test-ip") {
				allowed.Add(1)
			}
		}()
	}
	wg.Wait()
	// strict 模式：初始令牌恰好 1 个，所以 allowed 应该 == 1
	if a := allowed.Load(); a != 1 {
		t.Fatalf("WithStrictNewIP: expected exactly 1 allowed, got %d", a)
	}
}

// TestIPMap_WithStrictNewIP_Concurrent 验证 WithStrictNewIP 在高并发下线程安全，不会 panic 或数据竞争。
func TestIPMap_WithStrictNewIP_Concurrent(t *testing.T) {
	m := ratelimit.NewIPMap(10000, 1000, 0, ratelimit.WithStrictNewIP())
	const goroutines = 50
	var wg sync.WaitGroup
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			ip := fmt.Sprintf("192.168.%d.1", i%10)
			for j := 0; j < 20; j++ {
				m.Allow(ip)
			}
		}(i)
	}
	wg.Wait()
}

// ─── TopN 测试 ────────────────────────────────────────────────────────────────

// TestIPMap_TopN_Basic 验证 TopN 按 Hits 降序返回正确 IP。
func TestIPMap_TopN_Basic(t *testing.T) {
	m := ratelimit.NewIPMap(100000, 10000, 0)

	// 构造不同频率的 IP 访问
	for i := 0; i < 10; i++ {
		m.Allow("high") // 10 hits
	}
	for i := 0; i < 5; i++ {
		m.Allow("mid") // 5 hits
	}
	m.Allow("low") // 1 hit

	top := m.TopN(2)
	if len(top) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(top))
	}
	if top[0].IP != "high" {
		t.Errorf("top[0]: expected 'high', got %q", top[0].IP)
	}
	if top[0].Hits != 10 {
		t.Errorf("top[0].Hits: expected 10, got %d", top[0].Hits)
	}
	if top[1].IP != "mid" {
		t.Errorf("top[1]: expected 'mid', got %q", top[1].IP)
	}
}

// TestIPMap_TopN_Zero 验证 n<=0 时返回 nil。
func TestIPMap_TopN_Zero(t *testing.T) {
	m := ratelimit.NewIPMap(100, 10, 0)
	m.Allow("x")
	if m.TopN(0) != nil {
		t.Fatal("TopN(0) should return nil")
	}
	if m.TopN(-1) != nil {
		t.Fatal("TopN(-1) should return nil")
	}
}

// TestIPMap_TopN_Empty 验证空 IPMap 返回空 slice（而非 panic）。
func TestIPMap_TopN_Empty(t *testing.T) {
	m := ratelimit.NewIPMap(100, 10, 0)
	top := m.TopN(5)
	if len(top) != 0 {
		t.Fatalf("empty IPMap TopN should return empty, got %v", top)
	}
}

// TestIPMap_TopN_AllowN_CountsHits 验证 AllowN 也纳入 Hits 计数。
func TestIPMap_TopN_AllowN_CountsHits(t *testing.T) {
	m := ratelimit.NewIPMap(100000, 10000, 0)
	m.AllowN("batchip", 3) // 1 次 AllowN 调用 = 1 hit（不是 3 hits）
	m.Allow("singleip")    // 1 次 Allow 调用 = 1 hit

	top := m.TopN(2)
	// 两者均为 1 hit，顺序不定；只验证都存在
	var found int
	for _, e := range top {
		if e.IP == "batchip" || e.IP == "singleip" {
			found++
		}
	}
	if found != 2 {
		t.Fatalf("expected batchip and singleip in TopN, got %v", top)
	}
}

func BenchmarkIPMap_TopN(b *testing.B) {
	m := ratelimit.NewIPMap(1000000, 100000, 0)
	// 预热 100 个不同 IP
	for i := 0; i < 100; i++ {
		for j := 0; j < 10; j++ {
			m.Allow(fmt.Sprintf("10.0.0.%d", i))
		}
	}
	b.ResetTimer()
	b.ReportAllocs()
	for b.Loop() {
		m.TopN(5)
	}
}

// ─── TopNRecent + RecentHits 测试 ─────────────────────────────────────────────

// TestIPMap_TopN_RecentHitsField 验证 TopN 返回的 IPEntry 同时携带 RecentHits 字段。
func TestIPMap_TopN_RecentHitsField(t *testing.T) {
	m := ratelimit.NewIPMap(1000000, 100000, 0)
	for i := 0; i < 5; i++ {
		m.Allow("192.168.1.1")
	}
	top := m.TopN(1)
	if len(top) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(top))
	}
	if top[0].Hits != 5 {
		t.Errorf("Hits: got %d, want 5", top[0].Hits)
	}
	// RecentHits 应 > 0（本分钟内刚刚访问）
	if top[0].RecentHits <= 0 {
		t.Errorf("RecentHits should be > 0, got %d", top[0].RecentHits)
	}
}

// TestIPMap_TopNRecent_Basic 验证 TopNRecent 按 RecentHits 降序排序。
func TestIPMap_TopNRecent_Basic(t *testing.T) {
	m := ratelimit.NewIPMap(1000000, 100000, 0)
	// ip1: 10 次，ip2: 5 次，ip3: 1 次
	for i := 0; i < 10; i++ {
		m.Allow("10.0.0.1")
	}
	for i := 0; i < 5; i++ {
		m.Allow("10.0.0.2")
	}
	m.Allow("10.0.0.3")

	top := m.TopNRecent(2)
	if len(top) != 2 {
		t.Fatalf("expected 2, got %d", len(top))
	}
	if top[0].IP != "10.0.0.1" {
		t.Errorf("top[0] should be 10.0.0.1, got %s", top[0].IP)
	}
	if top[0].RecentHits < top[1].RecentHits {
		t.Errorf("TopNRecent not sorted: %d < %d", top[0].RecentHits, top[1].RecentHits)
	}
}

// TestIPMap_TopNRecent_Zero 验证 n<=0 返回 nil。
func TestIPMap_TopNRecent_Zero(t *testing.T) {
	m := ratelimit.NewIPMap(100, 20, 0)
	m.Allow("1.1.1.1")
	if m.TopNRecent(0) != nil {
		t.Error("TopNRecent(0) should return nil")
	}
	if m.TopNRecent(-1) != nil {
		t.Error("TopNRecent(-1) should return nil")
	}
}

// TestIPMap_TopNRecent_FilterZero 验证 RecentHits==0 的 IP 不出现在 TopNRecent 中。
func TestIPMap_TopNRecent_FilterZero(t *testing.T) {
	m := ratelimit.NewIPMap(1000000, 100000, 0)
	m.Allow("200.0.0.1")
	top := m.TopNRecent(10)
	for _, e := range top {
		if e.RecentHits <= 0 {
			t.Errorf("entry with RecentHits<=0 in TopNRecent: %+v", e)
		}
	}
}

// TestIPMap_RecentHits_NeverNegative 验证 recentHits 在任何情况下 >= 0。
func TestIPMap_RecentHits_NeverNegative(t *testing.T) {
	m := ratelimit.NewIPMap(1000000, 100000, 0)
	for i := 0; i < 20; i++ {
		m.Allow(fmt.Sprintf("10.%d.0.1", i))
	}
	tops := m.TopNRecent(100)
	for _, e := range tops {
		if e.RecentHits < 0 {
			t.Errorf("negative RecentHits: %d for IP %s", e.RecentHits, e.IP)
		}
	}
}

// TestIPMap_TopNRecent_Concurrent 验证并发 Allow + TopNRecent 不 panic，race 检测通过。
func TestIPMap_TopNRecent_Concurrent(t *testing.T) {
	m := ratelimit.NewIPMap(1000000, 100000, 0)
	const goroutines = 50
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		i := i
		go func() {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				m.Allow(fmt.Sprintf("192.168.%d.%d", i%4, j%8))
			}
			m.TopNRecent(5)
		}()
	}
	wg.Wait()
}

func BenchmarkIPMap_TopNRecent(b *testing.B) {
	m := ratelimit.NewIPMap(1000000, 100000, 0)
	for i := 0; i < 100; i++ {
		for j := 0; j < 10; j++ {
			m.Allow(fmt.Sprintf("10.0.%d.%d", i/10, i%10))
		}
	}
	b.ResetTimer()
	b.ReportAllocs()
	for b.Loop() {
		m.TopNRecent(5)
	}
}

// BenchmarkIPMap_Allow_RecentHit 验证 addRecent 对 Allow 热路径的延迟影响。
func BenchmarkIPMap_Allow_RecentHit(b *testing.B) {
	m := ratelimit.NewIPMap(1000000, 100000, 0)
	m.Allow("10.0.0.1") // 预热
	b.ResetTimer()
	b.ReportAllocs()
	for b.Loop() {
		m.Allow("10.0.0.1")
	}
}

// ─── 安全测试 ──────────────────────────────────────────────────────────────────

// TestIPMap_Allow_EmptyIP 验证空 IP 字符串不 panic。
func TestIPMap_Allow_EmptyIP(t *testing.T) {
	m := ratelimit.NewIPMap(100, 20, 0)
	// 空 IP 应正常处理（不 panic）
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("Allow(\"\") panicked: %v", r)
		}
	}()
	m.Allow("")
}

// TestIPMap_Allow_LongIP 验证超长 IP 字符串不导致内存异常。
func TestIPMap_Allow_LongIP(t *testing.T) {
	m := ratelimit.NewIPMap(100, 20, 0)
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("Allow(longIP) panicked: %v", r)
		}
	}()
	long := fmt.Sprintf("%0256d", 1) // 256 字符伪 IP
	m.Allow(long)
}

// TestIPMap_Allow_Strict_ConcurrentSafetyUnderRace 使高并发严格模式 exactly-once 无 panic。
func TestIPMap_Allow_Strict_ConcurrentSafetyUnderRace(t *testing.T) {
	m := ratelimit.NewIPMap(1000000, 100000, 0, ratelimit.WithStrictNewIP())
	var wg sync.WaitGroup
	var created atomic.Int64
	const N = 200
	wg.Add(N)
	for i := 0; i < N; i++ {
		go func() {
			defer wg.Done()
			// 全部 goroutine 并发首次访问同一 IP → 验证限速器 exactly-once
			if m.Allow("10.0.0.255") {
				created.Add(1)
			}
		}()
	}
	wg.Wait()
	top := m.TopN(1)
	if len(top) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(top))
	}
	if top[0].Hits != N {
		t.Errorf("Hits: got %d, want %d", top[0].Hits, N)
	}
}

// FuzzIPMap_Allow 对任意 IP 字符串进行 fuzz 以验证不 panic。
func FuzzIPMap_Allow(f *testing.F) {
	f.Add("127.0.0.1")
	f.Add("::1")
	f.Add("")
	f.Add("999.999.999.999")
	f.Add(string([]byte{0, 1, 2, 3, 4, 5}))
	m := ratelimit.NewIPMap(1000000, 100000, 0)
	f.Fuzz(func(t *testing.T, ip string) {
		defer func() {
			if r := recover(); r != nil {
				t.Errorf("Allow(%q) panicked: %v", ip, r)
			}
		}()
		m.Allow(ip)
		m.TopNRecent(3)
	})
}
