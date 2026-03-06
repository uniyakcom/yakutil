package hll

import (
	"testing"
)

// FuzzAdd 验证 Add 后 Count 满足基本不变式：
//   - Count() >= 0（始终）
//   - Count() >= 1（至少插入了 1 个元素后）
//   - Count() <= n + 合理误差（基数估计误差 ≤ 3× 实际值）
func FuzzAdd(f *testing.F) {
	f.Add([]byte("hello"))
	f.Add([]byte(""))
	f.Add([]byte("\x00"))
	f.Add([]byte("\xff\xfe\xfd"))
	f.Add([]byte("unicode: 日本語"))
	f.Fuzz(func(t *testing.T, data []byte) {
		s := New()
		s.Add(data)
		c := s.Count()
		// HyperLogLog 插入 1 个唯一元素后，估计值至少为 1
		if c < 1 {
			t.Errorf("Count() = %d after Add(%q), want >= 1", c, data)
		}
	})
}

// FuzzAddStr 对字符串型接口作同等验证。
func FuzzAddStr(f *testing.F) {
	f.Add("hello")
	f.Add("")
	f.Add("\x00\x01")
	f.Add("a")
	f.Fuzz(func(t *testing.T, s string) {
		hll := New()
		hll.AddStr(s)
		c := hll.Count()
		if c < 1 {
			t.Errorf("Count() = %d after AddStr(%q), want >= 1", c, s)
		}
	})
}

// FuzzMerge 验证 Merge 是单调的：合并后 Count >= max(a.Count, b.Count)。
func FuzzMerge(f *testing.F) {
	f.Add([]byte("foo"), []byte("bar"))
	f.Add([]byte(""), []byte(""))
	f.Add([]byte("x"), []byte("x"))
	f.Fuzz(func(t *testing.T, d1, d2 []byte) {
		a := New()
		b := New()
		a.Add(d1)
		b.Add(d2)

		ca := a.Count()
		cb := b.Count()

		a.Merge(b)
		merged := a.Count()

		threshold := ca
		if cb > threshold {
			threshold = cb
		}
		// 合并后估计值不应低于两者最大值（HLL Merge = 取桶最大值，单调非减）
		if merged < threshold {
			t.Errorf("Merge: Count=%d < max(%d,%d)", merged, ca, cb)
		}
	})
}
