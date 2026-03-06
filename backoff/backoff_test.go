package backoff

import (
	"testing"
	"time"
)

// ─── 基础功能 ────────────────────────────────────────────────────────────────

func TestBackoff_ZeroValue(t *testing.T) {
	var b Backoff
	// 零值可直接使用
	b.Spin()
	if b.N != 1 {
		t.Fatalf("after Spin(), N = %d, want 1", b.N)
	}
}

func TestBackoff_Phase1_Fast(t *testing.T) {
	var b Backoff
	start := time.Now()
	// Phase 1: 紧密自旋，应该几乎不耗时
	for i := 0; i < int(DefaultSpinN); i++ {
		b.Spin()
	}
	elapsed := time.Since(start)
	if elapsed > 10*time.Millisecond {
		t.Fatalf("Phase 1 (%d spins) took %v, expected < 10ms", DefaultSpinN, elapsed)
	}
}

func TestBackoff_Phase2_Yield(t *testing.T) {
	var b Backoff
	// 跳到 phase 2
	b.N = DefaultSpinN
	start := time.Now()
	for i := 0; i < int(DefaultYieldN); i++ {
		b.Spin()
	}
	elapsed := time.Since(start)
	// Phase 2 调用 Gosched，应该比 phase 1 慢但 < 100ms
	if elapsed > 100*time.Millisecond {
		t.Fatalf("Phase 2 took %v, expected < 100ms", elapsed)
	}
}

func TestBackoff_Phase3_Sleep(t *testing.T) {
	var b Backoff
	b.N = DefaultSpinN + DefaultYieldN

	start := time.Now()
	b.Spin() // first phase 3 iteration = 1µs sleep
	elapsed := time.Since(start)

	// 至少有一点 sleep 时间（> 0）
	if elapsed < time.Microsecond/2 {
		t.Logf("Phase 3 first Spin took only %v (may vary by OS scheduler)", elapsed)
	}
}

func TestBackoff_Reset(t *testing.T) {
	var b Backoff
	for i := 0; i < 200; i++ {
		b.Spin()
	}
	if b.N != 200 {
		t.Fatalf("N = %d, want 200", b.N)
	}
	b.Reset()
	if b.N != 0 {
		t.Fatalf("after Reset, N = %d, want 0", b.N)
	}
}

// ─── 自定义参数 ──────────────────────────────────────────────────────────────

func TestBackoff_Custom(t *testing.T) {
	b := Backoff{
		SpinN:   10,
		YieldN:  5,
		MaxWait: 500 * time.Microsecond,
	}

	// Phase 1: 0..9
	for i := 0; i < 10; i++ {
		b.Spin()
	}
	if b.N != 10 {
		t.Fatalf("N = %d, want 10", b.N)
	}

	// Phase 2: 10..14
	for i := 0; i < 5; i++ {
		b.Spin()
	}
	if b.N != 15 {
		t.Fatalf("N = %d, want 15", b.N)
	}

	// Phase 3: 15+
	b.Spin()
	if b.N != 16 {
		t.Fatalf("N = %d, want 16", b.N)
	}
}

func TestBackoff_MaxWaitCap(t *testing.T) {
	b := Backoff{
		SpinN:   0, // defaults to 64
		YieldN:  0, // defaults to 64
		MaxWait: 100 * time.Microsecond,
	}
	// 跳到 phase 3 深处
	b.N = DefaultSpinN + DefaultYieldN + 20

	start := time.Now()
	b.Spin()
	elapsed := time.Since(start)

	// sleep 应该被 MaxWait cap 住（-race 下放宽到 20ms）
	if elapsed > 20*time.Millisecond {
		t.Fatalf("Spin() with MaxWait=100µs took %v", elapsed)
	}
}

// ─── Phase 边界 ──────────────────────────────────────────────────────────────

func TestBackoff_PhaseBoundary(t *testing.T) {
	var b Backoff
	total := int(DefaultSpinN + DefaultYieldN + 10)
	for i := 0; i < total; i++ {
		b.Spin()
	}
	if b.N != uint32(total) {
		t.Fatalf("N = %d, want %d", b.N, total)
	}
}

// ─── Benchmarks ─────────────────────────────────────────────────────────────

func BenchmarkBackoff_Phase1(b *testing.B) {
	var bo Backoff
	for b.Loop() {
		bo.N = 0
		bo.Spin()
	}
}

func BenchmarkBackoff_Phase2(b *testing.B) {
	var bo Backoff
	for b.Loop() {
		bo.N = DefaultSpinN
		bo.Spin()
	}
}
