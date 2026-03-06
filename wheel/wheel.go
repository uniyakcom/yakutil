// Package wheel 提供可配置分辨率的泛型时间轮。
//
// 适用于 TTL 过期、连接超时、心跳检测等大规模定时场景。
// 时间复杂度：Add O(1)、Cancel O(1)、Advance O(expired)。
//
// 用法：
//
//	w := wheel.New[ConnID](10*time.Millisecond, 1024)
//	id := w.Add(5*time.Second, connID)
//	w.Cancel(id)
//	// 或启动自动 tick：
//	w.Run(ctx, func(connID ConnID) { close(connID) })
package wheel

import (
	"context"
	"sync"
	"sync/atomic"
	"time"
)

// entry 定时条目，嵌入双向链表。
type entry[T any] struct {
	id     uint64
	val    T
	rounds int
	next   *entry[T]
	prev   *entry[T]
}

// slot 时间轮槽位，双向链表头。
type slot[T any] struct {
	head entry[T] // sentinel
}

func (s *slot[T]) init() {
	s.head.next = &s.head
	s.head.prev = &s.head
}

func (s *slot[T]) pushBack(e *entry[T]) {
	e.prev = s.head.prev
	e.next = &s.head
	s.head.prev.next = e
	s.head.prev = e
}

func removeEntry[T any](e *entry[T]) {
	e.prev.next = e.next
	e.next.prev = e.prev
	e.prev = nil
	e.next = nil
}

// Wheel 泛型分层时间轮。并发安全。
type Wheel[T any] struct {
	tick   time.Duration
	slots  []slot[T]
	mask   int
	cur    int
	nextID atomic.Uint64
	index  map[uint64]*entry[T]
	mu     sync.Mutex
	pool   sync.Pool
}

// New 创建时间轮。tick 为刻度分辨率，numSlots 自动向上取 2 的幂（最小 16）。
func New[T any](tick time.Duration, numSlots int) *Wheel[T] {
	sz := 16
	for sz < numSlots {
		sz <<= 1
	}
	w := &Wheel[T]{
		tick:  tick,
		slots: make([]slot[T], sz),
		mask:  sz - 1,
		index: make(map[uint64]*entry[T]),
	}
	for i := range w.slots {
		w.slots[i].init()
	}
	w.pool.New = func() any { return &entry[T]{} }
	return w
}

// Add 添加一个定时条目，d 后到期。返回唯一 ID 用于取消。
func (w *Wheel[T]) Add(d time.Duration, val T) uint64 {
	ticks := int(d / w.tick)
	if ticks <= 0 {
		ticks = 1
	}

	id := w.nextID.Add(1)
	e := w.pool.Get().(*entry[T])
	e.id = id
	e.val = val

	w.mu.Lock()
	pos := (w.cur + ticks) & w.mask
	e.rounds = ticks / (w.mask + 1)
	w.slots[pos].pushBack(e)
	w.index[id] = e
	w.mu.Unlock()
	return id
}

// Cancel 取消一个定时条目。条目已到期或不存在时返回 false。
func (w *Wheel[T]) Cancel(id uint64) bool {
	w.mu.Lock()
	e, ok := w.index[id]
	if !ok {
		w.mu.Unlock()
		return false
	}
	delete(w.index, id)
	removeEntry(e)
	w.mu.Unlock()

	var zero T
	e.val = zero
	w.pool.Put(e)
	return true
}

// Advance 推进时间轮一个 tick，对所有到期条目调用 fn。
// 由调用者驱动（手动 tick 模式）。
//
// fn 在锁外串行调用——可安全执行慢操作（如关闭连接）或访问 Wheel 其他方法。
// 若单次 tick 触发大量到期条目且 fn 较慢，建议在 fn 内用 goroutine 异步处理：
//
//	w.Advance(func(id ConnID) {
//	    go closeConn(id) // 异步处理，不阻塞后续到期回调
//	})
func (w *Wheel[T]) Advance(fn func(T)) {
	w.mu.Lock()
	w.cur = (w.cur + 1) & w.mask
	s := &w.slots[w.cur]

	// 收集到期条目的值，defer 到锁外回调
	var expired []T
	cur := s.head.next
	for cur != &s.head {
		next := cur.next
		if cur.rounds > 0 {
			cur.rounds--
			cur = next
			continue
		}
		// 到期
		removeEntry(cur)
		delete(w.index, cur.id)
		expired = append(expired, cur.val)
		var zero T
		cur.val = zero
		w.pool.Put(cur)
		cur = next
	}
	w.mu.Unlock()

	// 在锁外调用 fn，避免阻塞 Add/Cancel
	for _, val := range expired {
		fn(val)
	}
}

// Len 返回当前待触发的条目数。
func (w *Wheel[T]) Len() int {
	w.mu.Lock()
	n := len(w.index)
	w.mu.Unlock()
	return n
}

// Tick 返回时间轮的刻度分辨率。
func (w *Wheel[T]) Tick() time.Duration { return w.tick }

// Run 启动自动 tick 循环。阻塞直到 ctx 取消。
// 每个 tick 调用 fn 处理到期条目。
func (w *Wheel[T]) Run(ctx context.Context, fn func(T)) {
	ticker := time.NewTicker(w.tick)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			w.Advance(fn)
		}
	}
}
