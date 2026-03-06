package spsc

import (
	"sync"
	"testing"
)

// ─── 基础功能 ────────────────────────────────────────────────────────────────

func TestRing_PushPop(t *testing.T) {
	r := New[int](8)

	if !r.Push(1) {
		t.Fatal("Push(1) failed")
	}
	if !r.Push(2) {
		t.Fatal("Push(2) failed")
	}
	if !r.Push(3) {
		t.Fatal("Push(3) failed")
	}

	for _, want := range []int{1, 2, 3} {
		got, ok := r.Pop()
		if !ok {
			t.Fatalf("Pop() failed, want %d", want)
		}
		if got != want {
			t.Fatalf("Pop() = %d, want %d", got, want)
		}
	}
}

func TestRing_Empty(t *testing.T) {
	r := New[int](4)
	val, ok := r.Pop()
	if ok {
		t.Fatalf("Pop on empty ring returned val=%d", val)
	}
}

func TestRing_Full(t *testing.T) {
	r := New[int](4)
	cap := r.Cap()
	for i := 0; i < cap; i++ {
		if !r.Push(i) {
			t.Fatalf("Push(%d) failed, cap=%d", i, cap)
		}
	}
	if r.Push(99) {
		t.Fatal("Push on full ring should fail")
	}
}

func TestRing_FillAndDrain(t *testing.T) {
	r := New[int](8)
	cap := r.Cap()

	// 填满
	for i := 0; i < cap; i++ {
		r.Push(i)
	}
	if r.Len() != cap {
		t.Fatalf("Len() = %d, want %d", r.Len(), cap)
	}

	// 全部弹出
	for i := 0; i < cap; i++ {
		val, ok := r.Pop()
		if !ok || val != i {
			t.Fatalf("Pop() = (%d, %v), want (%d, true)", val, ok, i)
		}
	}
	if r.Len() != 0 {
		t.Fatalf("after drain Len() = %d, want 0", r.Len())
	}
}

func TestRing_Wraparound(t *testing.T) {
	r := New[int](4) // cap = 4
	cap := r.Cap()

	// 多轮 push/pop 测试 wrap
	for round := 0; round < 10; round++ {
		for i := 0; i < cap; i++ {
			if !r.Push(round*cap + i) {
				t.Fatalf("round %d: Push(%d) failed", round, i)
			}
		}
		for i := 0; i < cap; i++ {
			val, ok := r.Pop()
			if !ok {
				t.Fatalf("round %d: Pop() failed", round)
			}
			want := round*cap + i
			if val != want {
				t.Fatalf("round %d: Pop() = %d, want %d", round, val, want)
			}
		}
	}
}

// ─── Cap/Len ─────────────────────────────────────────────────────────────────

func TestRing_Cap(t *testing.T) {
	r := New[int](16)
	if r.Cap() != 16 {
		t.Fatalf("Cap() = %d, want 16", r.Cap())
	}
}

func TestRing_CapRoundsUp(t *testing.T) {
	r := New[int](5)
	if r.Cap() < 5 {
		t.Fatalf("Cap() = %d < 5", r.Cap())
	}
	c := r.Cap()
	if c&(c-1) != 0 {
		t.Fatalf("Cap() = %d, not power of 2", c)
	}
}

func TestRing_Len(t *testing.T) {
	r := New[int](8)
	if r.Len() != 0 {
		t.Fatalf("Len() = %d, want 0", r.Len())
	}
	r.Push(1)
	r.Push(2)
	if r.Len() != 2 {
		t.Fatalf("Len() = %d, want 2", r.Len())
	}
	r.Pop()
	if r.Len() != 1 {
		t.Fatalf("Len() = %d, want 1", r.Len())
	}
}

// ─── 泛型类型测试 ────────────────────────────────────────────────────────────

func TestRing_String(t *testing.T) {
	r := New[string](4)
	r.Push("hello")
	r.Push("world")
	val, ok := r.Pop()
	if !ok || val != "hello" {
		t.Fatalf("Pop() = (%q, %v), want (hello, true)", val, ok)
	}
}

type testStruct struct {
	ID   int
	Name string
}

func TestRing_Struct(t *testing.T) {
	r := New[testStruct](4)
	r.Push(testStruct{1, "alice"})
	r.Push(testStruct{2, "bob"})

	got, ok := r.Pop()
	if !ok || got.ID != 1 || got.Name != "alice" {
		t.Fatalf("Pop() = %+v, want {1, alice}", got)
	}
}

// 验证 Pop 后 slot 被清零（防止 GC 泄漏）
func TestRing_PopClearsSlot(t *testing.T) {
	type big struct {
		data *[1024]byte
	}
	r := New[big](4)
	data := &[1024]byte{}
	data[0] = 0xAA
	r.Push(big{data: data})

	got, ok := r.Pop()
	if !ok || got.data[0] != 0xAA {
		t.Fatal("Pop failed")
	}

	// 填满并清空，确保旧 slot 不再持有指针
	for i := 0; i < r.Cap(); i++ {
		r.Push(big{})
		r.Pop()
	}
	// 此时 slot 中的 data 应该是 nil（已被清零）
	// GC 可回收原始 data
}

// ─── 并发测试（单生产者-单消费者） ──────────────────────────────────────────

func TestRing_ConcurrentSPSC(t *testing.T) {
	r := New[int](1024)
	const total = 100000

	var wg sync.WaitGroup
	wg.Add(2)

	// Producer
	go func() {
		defer wg.Done()
		for i := 0; i < total; i++ {
			for !r.Push(i) {
				// spin
			}
		}
	}()

	// Consumer
	errs := make(chan string, 1)
	go func() {
		defer wg.Done()
		for i := 0; i < total; i++ {
			var val int
			var ok bool
			for {
				val, ok = r.Pop()
				if ok {
					break
				}
			}
			if val != i {
				errs <- "mismatch"
				return
			}
		}
	}()

	wg.Wait()
	select {
	case e := <-errs:
		t.Fatal(e)
	default:
	}
}

// ─── Benchmarks ─────────────────────────────────────────────────────────────

func BenchmarkRing_PushPop(b *testing.B) {
	r := New[int](1024)
	for b.Loop() {
		r.Push(42)
		r.Pop()
	}
}

func BenchmarkRing_SPSC_Throughput(b *testing.B) {
	r := New[int](4096)
	done := make(chan struct{})

	go func() {
		for {
			select {
			case <-done:
				return
			default:
				r.Pop()
			}
		}
	}()

	b.ResetTimer()
	for b.Loop() {
		for !r.Push(1) {
		}
	}
	b.StopTimer()
	close(done)
}
