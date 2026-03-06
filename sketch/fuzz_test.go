package sketch

import (
	"testing"
)

// FuzzAdd 验证 Add 后 Count 满足不变式：0 ≤ Count ≤ Total，
// 且 Count(key) ≥ 真实计数（单调不低估）。
func FuzzAdd(f *testing.F) {
	f.Add([]byte("hello"), int64(1))
	f.Add([]byte(""), int64(1))
	f.Add([]byte("\x00"), int64(3))
	f.Add([]byte("foo"), int64(100))
	f.Fuzz(func(t *testing.T, key []byte, count int64) {
		if count <= 0 || count > 1<<20 {
			t.Skip()
		}
		cms := New()
		cms.Add(key, count)
		got := cms.Count(key)
		if got < count {
			t.Errorf("Count(%q) = %d < added %d (CMS 单调不低估不变式违反)", key, got, count)
		}
		if got > cms.Total() {
			t.Errorf("Count(%q) = %d > Total %d", key, got, cms.Total())
		}
	})
}

// FuzzAddStr 对字符串型接口作同等验证。
func FuzzAddStr(f *testing.F) {
	f.Add("hello")
	f.Add("")
	f.Add("unicode: 日本語")
	f.Add("\x00\x01\xff")
	f.Fuzz(func(t *testing.T, key string) {
		cms := New()
		cms.AddStr(key)
		got := cms.CountStr(key)
		if got < 1 {
			t.Errorf("CountStr(%q) = %d < 1 after AddStr", key, got)
		}
		if got > cms.Total() {
			t.Errorf("CountStr(%q) = %d > Total %d", key, got, cms.Total())
		}
	})
}

// FuzzMerge 验证合并两个 CMS 后，Count 不低于任一子 CMS 的 Count。
func FuzzMerge(f *testing.F) {
	f.Add([]byte("foo"), []byte("bar"))
	f.Add([]byte(""), []byte(""))
	f.Add([]byte("x"), []byte("x"))
	f.Fuzz(func(t *testing.T, key1, key2 []byte) {
		a := New()
		b := New()
		a.Add(key1, 1)
		b.Add(key2, 1)

		before := a.Count(key1)
		a.Merge(b)
		after := a.Count(key1)
		if after < before {
			t.Errorf("Merge 后 Count(%q) 从 %d 降至 %d", key1, before, after)
		}
	})
}
