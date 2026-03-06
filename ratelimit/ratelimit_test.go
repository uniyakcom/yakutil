package ratelimit_test

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/uniyakcom/yakutil/ratelimit"
)

// TestNew_DefaultValues 验证 New 的初始状态：满桶、参数正确。
func TestNew_DefaultValues(t *testing.T) {
	rl := ratelimit.New(100, 10)
	if rl.Rate() != 100 {
		t.Fatalf("Rate = %d, want 100", rl.Rate())
	}
	if rl.Burst() != 10 {
		t.Fatalf("Burst = %d, want 10", rl.Burst())
	}
	if got := rl.Tokens(); got != 10 {
		t.Fatalf("initial Tokens = %d, want 10", got)
	}
}

// TestNew_GuardInvalidArgs 验证非法参数被修正为合理最小值。
func TestNew_GuardInvalidArgs(t *testing.T) {
	rl := ratelimit.New(0, 0)
	if rl.Rate() < 1 {
		t.Error("Rate should be at least 1")
	}
	if rl.Burst() < 1 {
		t.Error("Burst should be at least 1")
	}
}

// TestAllow_ConsumesToken 验证连续 Allow 消耗令牌，超出 burst 后返回 false。
func TestAllow_ConsumesToken(t *testing.T) {
	rl := ratelimit.New(1000, 5)
	for i := 0; i < 5; i++ {
		if !rl.Allow() {
			t.Fatalf("iteration %d: Allow should succeed", i)
		}
	}
	if rl.Allow() {
		t.Fatal("should be rate-limited after burst exhausted")
	}
}

// TestAllowN_Batch 验证批量扣减。
func TestAllowN_Batch(t *testing.T) {
	rl := ratelimit.New(1000, 10)
	if !rl.AllowN(10) {
		t.Fatal("AllowN(10) should succeed with burst=10")
	}
	if rl.AllowN(1) {
		t.Fatal("AllowN should fail after exhausting burst")
	}
}

// TestAllowN_ExceedBurst 验证 n > burst 永远返回 false。
func TestAllowN_ExceedBurst(t *testing.T) {
	rl := ratelimit.New(1000, 5)
	if rl.AllowN(6) {
		t.Fatal("AllowN(n > burst) should always return false")
	}
}

// TestAllowN_ZeroAndNegative 验证 n <= 0 视为 1。
func TestAllowN_ZeroAndNegative(t *testing.T) {
	rl := ratelimit.New(1000, 1)
	if !rl.AllowN(0) {
		t.Fatal("AllowN(0) should be treated as AllowN(1), burst=1 should succeed")
	}
	if rl.AllowN(-1) {
		t.Fatal("AllowN(-1) after exhaustion should fail")
	}
}

// TestTokenRefill 验证令牌随时间补充。
func TestTokenRefill(t *testing.T) {
	rl := ratelimit.New(1000, 5)
	for rl.Allow() {
	}
	time.Sleep(6 * time.Millisecond)
	got := 0
	for rl.Allow() {
		got++
	}
	if got < 3 || got > 6 {
		t.Fatalf("after 5ms at 1000/s: got %d tokens refilled, want ~5", got)
	}
}

// TestReset 验证 Reset 恢复满桶。
func TestReset(t *testing.T) {
	rl := ratelimit.New(1, 5)
	for rl.Allow() {
	}
	if rl.Allow() {
		t.Fatal("should be empty before reset")
	}
	rl.Reset()
	if rl.Tokens() != 5 {
		t.Fatalf("after Reset: Tokens = %d, want 5", rl.Tokens())
	}
	if !rl.Allow() {
		t.Fatal("Allow should succeed after reset")
	}
}

// TestConcurrentAllow 验证并发调用下令牌总消耗不超过初始桶容量。
func TestConcurrentAllow(t *testing.T) {
	const burst = 100
	rl := ratelimit.New(1, burst)
	var (
		wg      sync.WaitGroup
		allowed atomic.Int64
	)
	const goroutines = 500
	ready := make(chan struct{})
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-ready
			if rl.Allow() {
				allowed.Add(1)
			}
		}()
	}
	close(ready)
	wg.Wait()
	got := allowed.Load()
	if got > burst {
		t.Fatalf("concurrent Allow: %d tokens consumed, but burst=%d", got, burst)
	}
	if got < 1 {
		t.Fatal("expected at least 1 Allow to succeed")
	}
}

// TestTokens_Diagnostic 验证 Tokens() 快照不超出 [0, burst] 范围。
func TestTokens_Diagnostic(t *testing.T) {
	rl := ratelimit.New(1000, 20)
	for i := 0; i < 50; i++ {
		rl.Allow()
		tok := rl.Tokens()
		if tok < 0 || tok > 20 {
			t.Fatalf("Tokens() = %d out of [0, 20]", tok)
		}
	}
}

// TestConcurrentAllow_RefillRace 专项验证并发补充 + 扣减时令牌总量不超限。
//
// 测试场景：令牌桶几乎耗尽后等待一小段时间触发补充，
// 在多 goroutine 中同时发起补充和扣减，验证 Allow 的总通过数
// 不超过 burst + 补充量（上界），且不存在"补充 Store 踩踏扣减"的超发问题。
func TestConcurrentAllow_RefillRace(t *testing.T) {
	const rate = 10_000
	const burst = 50
	rl := ratelimit.New(rate, burst)

	// 耗尽桶
	for rl.Allow() {
	}

	// 等待补充约 20 个令牌 (2ms @ 10000/s)
	time.Sleep(2 * time.Millisecond)

	var allowed atomic.Int64
	var wg sync.WaitGroup
	const goroutines = 200
	ready := make(chan struct{})
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-ready
			if rl.Allow() {
				allowed.Add(1)
			}
		}()
	}
	close(ready)
	wg.Wait()

	got := allowed.Load()
	// 2ms 内理论补充令牌数：10000/s * 0.002s = 20，加上可能的时间误差上限 50
	maxExpected := int64(burst)
	if got > maxExpected {
		t.Fatalf("refill+deduct race: %d tokens passed, max expected %d (burst=%d)", got, maxExpected, burst)
	}
}

// BenchmarkAllow 测量单 goroutine 令牌允许路径性能。
func BenchmarkAllow(b *testing.B) {
	rl := ratelimit.New(1<<30, 1<<30)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rl.Allow()
	}
}

// BenchmarkAllowN 测量批量扣减性能。
func BenchmarkAllowN(b *testing.B) {
	rl := ratelimit.New(1<<30, 1<<30)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rl.AllowN(4)
	}
}

// BenchmarkAllow_Exhausted 测量令牌耗尽（快速拒绝）路径性能。
func BenchmarkAllow_Exhausted(b *testing.B) {
	rl := ratelimit.New(1, 1)
	rl.AllowN(1)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rl.Allow()
	}
}

// BenchmarkAllow_Parallel 测量多 goroutine 并发场景性能。
func BenchmarkAllow_Parallel(b *testing.B) {
	rl := ratelimit.New(1<<30, 1<<30)
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			rl.Allow()
		}
	})
}
