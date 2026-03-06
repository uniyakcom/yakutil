package wheel

import (
	"context"
	"sync"
	"testing"
	"time"
)

// ─── 基础 ────────────────────────────────────────────────────────────────────

func TestWheel_AddAdvance(t *testing.T) {
	w := New[string](time.Millisecond, 16)

	var got []string
	w.Add(1*time.Millisecond, "a")
	w.Add(2*time.Millisecond, "b")

	w.Advance(func(v string) { got = append(got, v) })
	if len(got) != 1 || got[0] != "a" {
		t.Fatalf("tick 1: got %v, want [a]", got)
	}

	got = got[:0]
	w.Advance(func(v string) { got = append(got, v) })
	if len(got) != 1 || got[0] != "b" {
		t.Fatalf("tick 2: got %v, want [b]", got)
	}
}

func TestWheel_Cancel(t *testing.T) {
	w := New[int](time.Millisecond, 16)
	id := w.Add(3*time.Millisecond, 42)

	ok := w.Cancel(id)
	if !ok {
		t.Fatal("Cancel should return true")
	}

	// 再次取消应返回 false
	if w.Cancel(id) {
		t.Fatal("double Cancel should return false")
	}

	// Advance 3 次不应触发
	fired := false
	for i := 0; i < 5; i++ {
		w.Advance(func(int) { fired = true })
	}
	if fired {
		t.Fatal("cancelled entry should not fire")
	}
}

func TestWheel_Len(t *testing.T) {
	w := New[int](time.Millisecond, 16)
	w.Add(5*time.Millisecond, 1)
	w.Add(10*time.Millisecond, 2)
	if n := w.Len(); n != 2 {
		t.Fatalf("Len() = %d, want 2", n)
	}
	w.Advance(func(int) {})
	w.Advance(func(int) {})
	w.Advance(func(int) {})
	w.Advance(func(int) {})
	w.Advance(func(int) {}) // tick=5, fires entry 1
	if n := w.Len(); n != 1 {
		t.Fatalf("Len() = %d after 5 ticks, want 1", n)
	}
}

func TestWheel_Tick(t *testing.T) {
	w := New[int](10*time.Millisecond, 16)
	if w.Tick() != 10*time.Millisecond {
		t.Fatalf("Tick() = %v", w.Tick())
	}
}

func TestWheel_Rounds(t *testing.T) {
	// numSlots=4, tick=1ms, 添加 delay=6ms → pos=(0+6)%4=2, rounds=6/4=1
	// 需要转 4+2=6 次才到期
	w := New[string](time.Millisecond, 4) // 实际 slots=4（最小 16... 但 4<16, 会变 16）
	// 最小 16 slots
	// delay=20ms → pos=(0+20)%16=4, rounds=20/16=1
	w.Add(20*time.Millisecond, "late")
	fired := false
	for i := 0; i < 19; i++ {
		w.Advance(func(string) { fired = true })
	}
	if fired {
		t.Fatal("should not fire before round completes")
	}
	w.Advance(func(v string) {
		if v != "late" {
			t.Fatalf("got %q, want late", v)
		}
		fired = true
	})
	if !fired {
		t.Fatal("should fire at tick 20")
	}
}

// ─── Run ─────────────────────────────────────────────────────────────────────

func TestWheel_Run(t *testing.T) {
	w := New[int](5*time.Millisecond, 16)
	w.Add(10*time.Millisecond, 77)

	var mu sync.Mutex
	var got []int
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	go w.Run(ctx, func(v int) {
		mu.Lock()
		got = append(got, v)
		mu.Unlock()
	})

	time.Sleep(50 * time.Millisecond)
	cancel()

	mu.Lock()
	if len(got) != 1 || got[0] != 77 {
		t.Fatalf("Run got %v, want [77]", got)
	}
	mu.Unlock()
}

// ─── 并发 ────────────────────────────────────────────────────────────────────

func TestWheel_Concurrent(t *testing.T) {
	w := New[int](time.Millisecond, 64)
	const goroutines = 8
	const ops = 100

	var wg sync.WaitGroup
	wg.Add(goroutines + 1)

	// 添加者
	for g := 0; g < goroutines; g++ {
		go func(id int) {
			defer wg.Done()
			for i := 0; i < ops; i++ {
				d := time.Duration(i+1) * time.Millisecond
				w.Add(d, id*ops+i)
			}
		}(g)
	}

	// 推进者
	go func() {
		defer wg.Done()
		for i := 0; i < ops+10; i++ {
			w.Advance(func(int) {})
			time.Sleep(100 * time.Microsecond)
		}
	}()

	wg.Wait()
}

// ─── Benchmarks ─────────────────────────────────────────────────────────────

func BenchmarkWheel_Add(b *testing.B) {
	w := New[int](time.Millisecond, 1024)
	for b.Loop() {
		w.Add(100*time.Millisecond, 42)
	}
}

func BenchmarkWheel_AddCancel(b *testing.B) {
	w := New[int](time.Millisecond, 1024)
	for b.Loop() {
		id := w.Add(100*time.Millisecond, 42)
		w.Cancel(id)
	}
}

func BenchmarkWheel_Advance(b *testing.B) {
	w := New[int](time.Millisecond, 1024)
	for i := 0; i < 1000; i++ {
		w.Add(time.Duration(i+1)*time.Millisecond, i)
	}
	b.ResetTimer()
	for b.Loop() {
		w.Advance(func(int) {})
	}
}
