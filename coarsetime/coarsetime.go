package coarsetime

import (
	"sync"
	"sync/atomic"
	"time"
)

// nanotime stores Unix nanosecond timestamp.
// Updated every 500us by background goroutine via time.NewTicker.
var nanotime atomic.Int64

var (
	stopOnce sync.Once
	stopCh   = make(chan struct{})
)

func init() {
	nanotime.Store(time.Now().UnixNano())
	go tick()
}

// tick runs the background clock update loop.
// Stops when stopCh is closed (via Stop).
func tick() {
	t := time.NewTicker(500 * time.Microsecond)
	defer t.Stop()
	for {
		select {
		case now := <-t.C:
			nanotime.Store(now.UnixNano())
		case <-stopCh:
			return
		}
	}
}

// Stop 停止后台 goroutine（幂等，多次调用安全）。
//
// 适用于测试清理、优雅关机等场景。Stop 后 NowNano/Now 仍可调用，
// 返回停止前最后一次更新的时间戳（不再推进）。
// 注意：Stop 后无法重启（进程生命周期内只能停一次）。
func Stop() {
	stopOnce.Do(func() { close(stopCh) })
}

// NowNano returns current Unix nanosecond timestamp (precision <= 500us).
// Thread-safe, zero allocation. Suitable for hot path calls.
func NowNano() int64 {
	return nanotime.Load()
}

// Now returns time.Time (same precision as NowNano).
// Heavier than NowNano due to time.Time construction.
func Now() time.Time {
	return time.Unix(0, nanotime.Load())
}
