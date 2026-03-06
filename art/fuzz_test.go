package art

import (
	"testing"
)

// FuzzPutGet 验证 Put 后 Get 总能取回相同值。
// corpus 包含空串、单字节、高重复前缀、二进制 0 字节等边界情况。
func FuzzPutGet(f *testing.F) {
	// 种子语料
	seeds := []string{"", "a", "ab", "abc", "\x00", "\xff", "foo/bar", "foo/baz", "foobar"}
	for _, s := range seeds {
		f.Add(s, s+"_val")
	}
	f.Fuzz(func(t *testing.T, key, val string) {
		var tree Tree[string]
		tree.Put(key, val)
		got, ok := tree.Get(key)
		if !ok {
			t.Errorf("Get(%q) not found after Put", key)
		}
		if got != val {
			t.Errorf("Get(%q) = %q, want %q", key, got, val)
		}
	})
}

// FuzzPutDeleteGet 验证 Put 后 Delete 再 Get 应返回 not-found，
// 且 Delete 返回的旧值与 Put 的值一致。
func FuzzPutDeleteGet(f *testing.F) {
	seeds := []string{"", "a", "hello", "\x00\x01", "prefix/key"}
	for _, s := range seeds {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, key string) {
		var tree Tree[int]
		const val = 42
		tree.Put(key, val)
		old, ok := tree.Delete(key)
		if !ok {
			t.Errorf("Delete(%q) returned ok=false", key)
		}
		if old != val {
			t.Errorf("Delete(%q) old=%d, want %d", key, old, val)
		}
		_, found := tree.Get(key)
		if found {
			t.Errorf("Get(%q) found after Delete", key)
		}
	})
}

// FuzzForEachConsistency 将 fuzz 字节流拆分为多个 key，
// 插入树后验证 ForEach 遍历的 key 集合与插入集合一致。
func FuzzForEachConsistency(f *testing.F) {
	f.Add([]byte("a\x00b\x00c\x00"))
	f.Add([]byte("foo\x00bar\x00baz\x00"))
	f.Add([]byte(""))
	f.Add([]byte("\x00\x00\x00"))
	f.Fuzz(func(t *testing.T, data []byte) {
		// 以 0x00 分割，得到 key 列表
		var keys []string
		start := 0
		for i, b := range data {
			if b == 0 {
				keys = append(keys, string(data[start:i]))
				start = i + 1
			}
		}
		if start < len(data) {
			keys = append(keys, string(data[start:]))
		}

		var tree Tree[int]
		inserted := make(map[string]bool)
		for i, k := range keys {
			tree.Put(k, i)
			inserted[k] = true
		}

		// ForEach 必须恰好访问所有插入的 key
		visited := make(map[string]bool)
		tree.ForEach(func(k string, _ int) bool {
			visited[k] = true
			return true
		})
		for k := range inserted {
			if !visited[k] {
				t.Errorf("ForEach missed key %q", k)
			}
		}
		if len(visited) != len(inserted) {
			t.Errorf("ForEach visited %d keys, inserted %d", len(visited), len(inserted))
		}
	})
}

// FuzzScanPrefixSubset 验证 ScanPrefix 只返回有对应前缀的 key。
func FuzzScanPrefixSubset(f *testing.F) {
	f.Add("fo", []byte("foo\x00foobar\x00bar\x00"))
	f.Add("", []byte("a\x00b\x00"))
	f.Fuzz(func(t *testing.T, prefix string, data []byte) {
		var keys []string
		start := 0
		for i, b := range data {
			if b == 0 {
				keys = append(keys, string(data[start:i]))
				start = i + 1
			}
		}
		if start < len(data) {
			keys = append(keys, string(data[start:]))
		}

		var tree Tree[struct{}]
		inserted := make(map[string]bool)
		for _, k := range keys {
			tree.Put(k, struct{}{})
			inserted[k] = true
		}

		tree.ScanPrefix(prefix, func(k string, _ struct{}) bool {
			if len(k) < len(prefix) || k[:len(prefix)] != prefix {
				t.Errorf("ScanPrefix(%q) returned non-matching key %q", prefix, k)
			}
			return true
		})
	})
}
