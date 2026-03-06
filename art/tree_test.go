package art

import (
	"fmt"
	"math/rand/v2"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// ─── 基础 CRUD ───────────────────────────────────────────────────────────────

func TestTree_Empty(t *testing.T) {
	var tr Tree[int]
	if tr.Len() != 0 {
		t.Fatal("empty tree len != 0")
	}
	v, ok := tr.Get("anything")
	if ok || v != 0 {
		t.Fatalf("Get on empty: %v %v", v, ok)
	}
}

func TestTree_PutGet(t *testing.T) {
	var tr Tree[string]
	old, replaced := tr.Put("hello", "world")
	if replaced || old != "" {
		t.Fatalf("first Put: old=%q replaced=%v", old, replaced)
	}
	if tr.Len() != 1 {
		t.Fatalf("Len = %d, want 1", tr.Len())
	}

	v, ok := tr.Get("hello")
	if !ok || v != "world" {
		t.Fatalf("Get hello = %q %v", v, ok)
	}

	// 替换
	old, replaced = tr.Put("hello", "updated")
	if !replaced || old != "world" {
		t.Fatalf("replace Put: old=%q replaced=%v", old, replaced)
	}
	if tr.Len() != 1 {
		t.Fatalf("Len after replace = %d", tr.Len())
	}
	v, _ = tr.Get("hello")
	if v != "updated" {
		t.Fatalf("Get after replace = %q", v)
	}
}

func TestTree_Delete(t *testing.T) {
	var tr Tree[int]
	tr.Put("a", 1)
	tr.Put("b", 2)
	tr.Put("c", 3)

	old, ok := tr.Delete("b")
	if !ok || old != 2 {
		t.Fatalf("Delete b: %v %v", old, ok)
	}
	if tr.Len() != 2 {
		t.Fatalf("Len = %d", tr.Len())
	}

	_, ok = tr.Get("b")
	if ok {
		t.Fatal("b still exists after delete")
	}

	// 删除不存在的 key
	_, ok = tr.Delete("nonexist")
	if ok {
		t.Fatal("deleted non-existent key")
	}
}

func TestTree_OverwriteDelete(t *testing.T) {
	var tr Tree[int]
	keys := []string{"alpha", "beta", "gamma", "delta", "epsilon"}
	for i, k := range keys {
		tr.Put(k, i)
	}
	if tr.Len() != 5 {
		t.Fatalf("Len = %d", tr.Len())
	}
	for _, k := range keys {
		tr.Delete(k)
	}
	if tr.Len() != 0 {
		t.Fatalf("Len after delete all = %d", tr.Len())
	}
}

// ─── 前缀 key 处理 ──────────────────────────────────────────────────────────

func TestTree_PrefixKeys(t *testing.T) {
	// key "ab" 是 "abc" 的前缀 — 测试内部节点 leaf 机制
	var tr Tree[int]
	tr.Put("ab", 1)
	tr.Put("abc", 2)
	tr.Put("abcd", 3)

	if v, ok := tr.Get("ab"); !ok || v != 1 {
		t.Fatalf("Get ab = %v %v", v, ok)
	}
	if v, ok := tr.Get("abc"); !ok || v != 2 {
		t.Fatalf("Get abc = %v %v", v, ok)
	}
	if v, ok := tr.Get("abcd"); !ok || v != 3 {
		t.Fatalf("Get abcd = %v %v", v, ok)
	}
	if tr.Len() != 3 {
		t.Fatalf("Len = %d", tr.Len())
	}

	// 删除中间 key
	tr.Delete("abc")
	if _, ok := tr.Get("abc"); ok {
		t.Fatal("abc not deleted")
	}
	if v, ok := tr.Get("ab"); !ok || v != 1 {
		t.Fatalf("ab after delete abc: %v %v", v, ok)
	}
	if v, ok := tr.Get("abcd"); !ok || v != 3 {
		t.Fatalf("abcd after delete abc: %v %v", v, ok)
	}
}

func TestTree_PrefixKeys_ShortFirst(t *testing.T) {
	var tr Tree[int]
	tr.Put("a", 1)
	tr.Put("ab", 2)

	if v, ok := tr.Get("a"); !ok || v != 1 {
		t.Fatalf("Get a = %v %v", v, ok)
	}
	if v, ok := tr.Get("ab"); !ok || v != 2 {
		t.Fatalf("Get ab = %v %v", v, ok)
	}
}

func TestTree_PrefixKeys_LongFirst(t *testing.T) {
	var tr Tree[int]
	tr.Put("abc", 1)
	tr.Put("ab", 2)
	tr.Put("a", 3)

	for _, tc := range []struct {
		k string
		v int
	}{{"a", 3}, {"ab", 2}, {"abc", 1}} {
		if v, ok := tr.Get(tc.k); !ok || v != tc.v {
			t.Fatalf("Get %q = %v %v, want %d", tc.k, v, ok, tc.v)
		}
	}
}

func TestTree_EmptyKey(t *testing.T) {
	var tr Tree[int]
	tr.Put("", 42)
	if v, ok := tr.Get(""); !ok || v != 42 {
		t.Fatalf("Get empty = %v %v", v, ok)
	}
	tr.Put("a", 1)
	if v, ok := tr.Get(""); !ok || v != 42 {
		t.Fatalf("Get empty after insert a = %v %v", v, ok)
	}
	tr.Delete("")
	if _, ok := tr.Get(""); ok {
		t.Fatal("empty key not deleted")
	}
	if v, ok := tr.Get("a"); !ok || v != 1 {
		t.Fatalf("Get a after delete empty = %v %v", v, ok)
	}
}

// ─── 节点升级/降级 ──────────────────────────────────────────────────────────

func TestTree_NodeGrowth(t *testing.T) {
	var tr Tree[int]
	// 插入足够多的子节点触发节点升级 4 → 16 → 48 → 256
	prefix := "key"
	for i := 0; i < 256; i++ {
		k := prefix + string([]byte{byte(i)})
		tr.Put(k, i)
	}
	if tr.Len() != 256 {
		t.Fatalf("Len = %d, want 256", tr.Len())
	}
	for i := 0; i < 256; i++ {
		k := prefix + string([]byte{byte(i)})
		v, ok := tr.Get(k)
		if !ok || v != i {
			t.Fatalf("Get %q = %v %v, want %d", k, v, ok, i)
		}
	}
}

func TestTree_NodeShrink(t *testing.T) {
	var tr Tree[int]
	prefix := "key"
	for i := 0; i < 256; i++ {
		tr.Put(prefix+string([]byte{byte(i)}), i)
	}
	// 删除至触发逐级降级
	for i := 0; i < 256; i++ {
		tr.Delete(prefix + string([]byte{byte(i)}))
	}
	if tr.Len() != 0 {
		t.Fatalf("Len after delete all = %d", tr.Len())
	}
}

// ─── ForEach 有序遍历 ────────────────────────────────────────────────────────

func TestTree_ForEach(t *testing.T) {
	var tr Tree[int]
	keys := []string{"delta", "alpha", "charlie", "bravo", "echo"}
	for i, k := range keys {
		tr.Put(k, i)
	}

	var got []string
	tr.ForEach(func(k string, _ int) bool {
		got = append(got, k)
		return true
	})
	sort.Strings(keys)
	if len(got) != len(keys) {
		t.Fatalf("ForEach got %d keys, want %d", len(got), len(keys))
	}
	for i, k := range keys {
		if got[i] != k {
			t.Fatalf("ForEach[%d] = %q, want %q", i, got[i], k)
		}
	}
}

func TestTree_ForEach_Stop(t *testing.T) {
	var tr Tree[int]
	for i := 0; i < 10; i++ {
		tr.Put(fmt.Sprintf("key%02d", i), i)
	}
	count := 0
	tr.ForEach(func(string, int) bool {
		count++
		return count < 3
	})
	if count != 3 {
		t.Fatalf("ForEach stopped at %d, want 3", count)
	}
}

// ─── Seek（有序游标）────────────────────────────────────────────────────────

func TestTree_Seek(t *testing.T) {
	var tr Tree[int]
	for i := 0; i < 10; i++ {
		tr.Put(fmt.Sprintf("k%02d", i), i)
	}
	// Seek from "k04" → 应得到 k05, k06, ..., k09
	var got []string
	tr.Seek("k04", func(k string, _ int) bool {
		got = append(got, k)
		return true
	})
	if len(got) != 5 {
		t.Fatalf("Seek from k04: got %v", got)
	}
	if got[0] != "k05" || got[4] != "k09" {
		t.Fatalf("Seek from k04: got %v", got)
	}
}

func TestTree_Seek_Empty(t *testing.T) {
	var tr Tree[int]
	tr.Put("aaa", 1)
	tr.Put("bbb", 2)
	tr.Put("ccc", 3)
	// Seek from "ccc" → nothing (strictly greater)
	var got []string
	tr.Seek("ccc", func(k string, _ int) bool {
		got = append(got, k)
		return true
	})
	if len(got) != 0 {
		t.Fatalf("Seek from last: got %v", got)
	}
}

func TestTree_Seek_BeforeAll(t *testing.T) {
	var tr Tree[int]
	tr.Put("b", 1)
	tr.Put("c", 2)
	var got []string
	tr.Seek("a", func(k string, _ int) bool {
		got = append(got, k)
		return true
	})
	if len(got) != 2 || got[0] != "b" || got[1] != "c" {
		t.Fatalf("Seek before all: got %v", got)
	}
}

// ─── ScanPrefix ─────────────────────────────────────────────────────────────

func TestTree_ScanPrefix(t *testing.T) {
	var tr Tree[int]
	tr.Put("user:1", 1)
	tr.Put("user:2", 2)
	tr.Put("user:3", 3)
	tr.Put("session:a", 10)
	tr.Put("session:b", 20)

	var got []string
	tr.ScanPrefix("user:", func(k string, _ int) bool {
		got = append(got, k)
		return true
	})
	if len(got) != 3 {
		t.Fatalf("ScanPrefix user: → %v", got)
	}
	for _, k := range got {
		if !strings.HasPrefix(k, "user:") {
			t.Fatalf("ScanPrefix returned non-matching key %q", k)
		}
	}
}

func TestTree_ScanPrefix_NoMatch(t *testing.T) {
	var tr Tree[int]
	tr.Put("abc", 1)
	var count int
	tr.ScanPrefix("xyz", func(string, int) bool {
		count++
		return true
	})
	if count != 0 {
		t.Fatalf("ScanPrefix xyz matched %d", count)
	}
}

func TestTree_ScanPrefix_All(t *testing.T) {
	var tr Tree[int]
	tr.Put("a", 1)
	tr.Put("b", 2)
	var count int
	tr.ScanPrefix("", func(string, int) bool {
		count++
		return true
	})
	if count != 2 {
		t.Fatalf("ScanPrefix '' matched %d, want 2", count)
	}
}

// ─── 大规模随机测试 ─────────────────────────────────────────────────────────

func TestTree_RandomOps(t *testing.T) {
	const N = 10000
	var tr Tree[int]
	ref := make(map[string]int) // 参照 map

	rng := rand.New(rand.NewPCG(42, 0))

	for i := 0; i < N; i++ {
		key := randomKey(rng)
		switch rng.IntN(3) {
		case 0, 1: // Put (more puts than deletes)
			refOld, refExists := ref[key]
			old, replaced := tr.Put(key, i)
			if replaced != refExists {
				t.Fatalf("iter %d: Put %q replaced=%v refExists=%v", i, key, replaced, refExists)
			}
			if replaced && old != refOld {
				t.Fatalf("iter %d: Put %q old=%v refOld=%v", i, key, old, refOld)
			}
			ref[key] = i
		case 2: // Delete
			refOld, refExists := ref[key]
			old, ok := tr.Delete(key)
			if ok != refExists {
				t.Fatalf("iter %d: Delete %q ok=%v refExists=%v", i, key, ok, refExists)
			}
			if ok && old != refOld {
				t.Fatalf("iter %d: Delete %q old=%v refOld=%v", i, key, old, refOld)
			}
			delete(ref, key)
		}

		if tr.Len() != len(ref) {
			t.Fatalf("iter %d: Len=%d refLen=%d", i, tr.Len(), len(ref))
		}
	}

	// 验证所有 ref 中的 key 都能 Get 到
	for k, v := range ref {
		got, ok := tr.Get(k)
		if !ok || got != v {
			t.Fatalf("final Get %q = %v %v, want %d", k, got, ok, v)
		}
	}

	// 验证 ForEach 输出有序
	var prev string
	count := 0
	tr.ForEach(func(k string, _ int) bool {
		if k <= prev && prev != "" {
			t.Fatalf("ForEach not sorted: %q after %q", k, prev)
		}
		prev = k
		count++
		return true
	})
	if count != len(ref) {
		t.Fatalf("ForEach count=%d, want %d", count, len(ref))
	}
}

func TestTree_RandomOps_Prefix(t *testing.T) {
	const N = 5000
	var tr Tree[int]
	ref := make(map[string]int)
	rng := rand.New(rand.NewPCG(123, 0))

	// 插入大量带前缀的 key
	prefixes := []string{"user:", "session:", "cache:", "temp:", "config:"}
	for i := 0; i < N; i++ {
		pfx := prefixes[rng.IntN(len(prefixes))]
		key := pfx + strconv.Itoa(rng.IntN(1000))
		tr.Put(key, i)
		ref[key] = i
	}

	// 验证 ScanPrefix
	for _, pfx := range prefixes {
		var artKeys []string
		tr.ScanPrefix(pfx, func(k string, _ int) bool {
			artKeys = append(artKeys, k)
			return true
		})

		var refKeys []string
		for k := range ref {
			if strings.HasPrefix(k, pfx) {
				refKeys = append(refKeys, k)
			}
		}
		sort.Strings(refKeys)

		if len(artKeys) != len(refKeys) {
			t.Fatalf("ScanPrefix %q: art=%d ref=%d", pfx, len(artKeys), len(refKeys))
		}
		for i := range artKeys {
			if artKeys[i] != refKeys[i] {
				t.Fatalf("ScanPrefix %q[%d]: art=%q ref=%q", pfx, i, artKeys[i], refKeys[i])
			}
		}
	}
}

func TestTree_Seek_Consistency(t *testing.T) {
	const N = 2000
	var tr Tree[int]
	rng := rand.New(rand.NewPCG(99, 0))

	keys := make([]string, N)
	for i := 0; i < N; i++ {
		keys[i] = randomKey(rng)
		tr.Put(keys[i], i)
	}

	// 去重并排序
	set := make(map[string]struct{})
	for _, k := range keys {
		set[k] = struct{}{}
	}
	sorted := make([]string, 0, len(set))
	for k := range set {
		sorted = append(sorted, k)
	}
	sort.Strings(sorted)

	// 测试 Seek 从多个游标位置
	for trial := 0; trial < 50; trial++ {
		cursor := sorted[rng.IntN(len(sorted))]
		var artResult []string
		tr.Seek(cursor, func(k string, _ int) bool {
			artResult = append(artResult, k)
			return true
		})

		// 参照结果
		var refResult []string
		for _, k := range sorted {
			if k > cursor {
				refResult = append(refResult, k)
			}
		}

		if len(artResult) != len(refResult) {
			t.Fatalf("Seek(%q): art=%d ref=%d", cursor, len(artResult), len(refResult))
		}
		for i := range artResult {
			if artResult[i] != refResult[i] {
				t.Fatalf("Seek(%q)[%d]: art=%q ref=%q", cursor, i, artResult[i], refResult[i])
			}
		}
	}
}

// ─── 路径压缩正确性 ─────────────────────────────────────────────────────────

func TestTree_PathCompression(t *testing.T) {
	var tr Tree[int]
	// 长公共前缀 → 路径压缩
	tr.Put("prefix_long_common_a", 1)
	tr.Put("prefix_long_common_b", 2)
	tr.Put("prefix_long_common_c", 3)

	for _, tc := range []struct {
		k string
		v int
	}{
		{"prefix_long_common_a", 1},
		{"prefix_long_common_b", 2},
		{"prefix_long_common_c", 3},
	} {
		v, ok := tr.Get(tc.k)
		if !ok || v != tc.v {
			t.Fatalf("Get %q = %v %v", tc.k, v, ok)
		}
	}

	// 分裂压缩路径
	tr.Put("prefix_long_diverge", 4)
	for _, k := range []string{"prefix_long_common_a", "prefix_long_diverge"} {
		if _, ok := tr.Get(k); !ok {
			t.Fatalf("after split: Get %q failed", k)
		}
	}
}

func TestTree_Reset(t *testing.T) {
	var tr Tree[int]
	for i := 0; i < 100; i++ {
		tr.Put(strconv.Itoa(i), i)
	}
	tr.Reset()
	if tr.Len() != 0 {
		t.Fatalf("Len after Reset = %d", tr.Len())
	}
	tr.Put("new", 42)
	if v, ok := tr.Get("new"); !ok || v != 42 {
		t.Fatalf("Get after Reset+Put: %v %v", v, ok)
	}
}

// ─── 二进制安全 key ──────────────────────────────────────────────────────────

func TestTree_BinarySafeKeys(t *testing.T) {
	var tr Tree[int]
	// key 中包含 null 字节
	k1 := "hello\x00world"
	k2 := "hello\x00"
	k3 := "hello"
	tr.Put(k1, 1)
	tr.Put(k2, 2)
	tr.Put(k3, 3)

	if v, ok := tr.Get(k1); !ok || v != 1 {
		t.Fatalf("Get binary key1: %v %v", v, ok)
	}
	if v, ok := tr.Get(k2); !ok || v != 2 {
		t.Fatalf("Get binary key2: %v %v", v, ok)
	}
	if v, ok := tr.Get(k3); !ok || v != 3 {
		t.Fatalf("Get binary key3: %v %v", v, ok)
	}
	if tr.Len() != 3 {
		t.Fatalf("Len = %d, want 3", tr.Len())
	}
}

// ─── CompactPrefixArena ──────────────────────────────────────────────────────

func TestTree_CompactPrefixArena_Empty(t *testing.T) {
	var tr Tree[int]
	tr.CompactPrefixArena() // 空树不应 panic
	if tr.PrefixArenaBytes() != 0 {
		t.Fatalf("empty tree arena bytes = %d", tr.PrefixArenaBytes())
	}
}

func TestTree_CompactPrefixArena_AfterDelete(t *testing.T) {
	var tr Tree[int]
	// 插入大量带公共长前缀的 key，让 arena 积累字节
	prefix := "application/service/module/component/function-"
	const N = 200
	for i := 0; i < N; i++ {
		tr.Put(fmt.Sprintf("%s%03d", prefix, i), i)
	}
	before := tr.PrefixArenaBytes()
	if before == 0 {
		t.Fatal("expected non-zero arena bytes before compact")
	}

	// 删除大部分 key，arena 存在死字节
	for i := 0; i < N-5; i++ {
		tr.Delete(fmt.Sprintf("%s%03d", prefix, i))
	}

	tr.CompactPrefixArena()
	after := tr.PrefixArenaBytes()
	if after >= before {
		t.Fatalf("arena bytes after compact=%d not smaller than before=%d", after, before)
	}

	// 剩余 5 条数据必须仍可访问
	for i := N - 5; i < N; i++ {
		key := fmt.Sprintf("%s%03d", prefix, i)
		v, ok := tr.Get(key)
		if !ok || v != i {
			t.Fatalf("Get(%q) after compact = %v %v, want %d", key, v, ok, i)
		}
	}
}

func TestTree_CompactPrefixArena_DataIntact(t *testing.T) {
	var tr Tree[string]
	keys := []string{
		"long/common/prefix/a",
		"long/common/prefix/b",
		"long/common/prefix/c/deeper",
		"long/common/prefix/d",
	}
	for _, k := range keys {
		tr.Put(k, k+"_val")
	}

	// 删除一条触发重压缩
	tr.Delete("long/common/prefix/b")
	tr.CompactPrefixArena()

	for _, k := range keys {
		if k == "long/common/prefix/b" {
			continue
		}
		v, ok := tr.Get(k)
		if !ok || v != k+"_val" {
			t.Fatalf("Get(%q) after compact = %q %v", k, v, ok)
		}
	}
}

// ─── node48 / node256 边界 ────────────────────────────────────────────────────

func TestTree_Node48_Exact17Keys(t *testing.T) {
	var tr Tree[int]
	// 17 个不同字节的子节点会触发 node16 → node48 升级
	pfx := "p"
	for i := 0; i < 17; i++ {
		tr.Put(pfx+string([]byte{byte(i + 1)}), i)
	}
	if tr.Len() != 17 {
		t.Fatalf("Len = %d, want 17", tr.Len())
	}
	for i := 0; i < 17; i++ {
		k := pfx + string([]byte{byte(i + 1)})
		v, ok := tr.Get(k)
		if !ok || v != i {
			t.Fatalf("Get(%q) = %v %v, want %d", k, v, ok, i)
		}
	}
	// 删除到 15 个，触发 node48 → node16 降级
	for i := 15; i < 17; i++ {
		tr.Delete(pfx + string([]byte{byte(i + 1)}))
	}
	if tr.Len() != 15 {
		t.Fatalf("Len after shrink = %d, want 15", tr.Len())
	}
	for i := 0; i < 15; i++ {
		k := pfx + string([]byte{byte(i + 1)})
		if _, ok := tr.Get(k); !ok {
			t.Fatalf("Get(%q) failed after node48→node16 shrink", k)
		}
	}
}

func TestTree_Node48_ForEachOrder(t *testing.T) {
	var tr Tree[int]
	pfx := "q"
	// 插入 20 个，确保进入 node48 路径
	for i := 19; i >= 0; i-- { // 逆序插入，验证有序性
		tr.Put(pfx+string([]byte{byte(i + 10)}), i)
	}
	var byteKeys []byte
	tr.ForEach(func(k string, _ int) bool {
		byteKeys = append(byteKeys, k[len(pfx)])
		return true
	})
	for i := 1; i < len(byteKeys); i++ {
		if byteKeys[i] <= byteKeys[i-1] {
			t.Fatalf("node48 ForEach not sorted at [%d]: %d <= %d", i, byteKeys[i], byteKeys[i-1])
		}
	}
}

func TestTree_Node256_SeekMiddle(t *testing.T) {
	var tr Tree[int]
	pfx := "n256:"
	// 插入全部 256 个字节子节点，强制升级到 node256
	for i := 0; i < 256; i++ {
		tr.Put(pfx+string([]byte{byte(i)}), i)
	}
	// Seek 从字节 127 开始，应得到字节 128—255（共 128 条）
	cursor := pfx + string([]byte{127})
	var got []string
	tr.Seek(cursor, func(k string, _ int) bool {
		got = append(got, k)
		return true
	})
	if len(got) != 128 {
		t.Fatalf("Seek mid node256: got %d keys, want 128", len(got))
	}
	// 验证有序
	for i := 1; i < len(got); i++ {
		if got[i] <= got[i-1] {
			t.Fatalf("node256 Seek not sorted at %d", i)
		}
	}
}

func TestTree_Node256_ScanPrefix(t *testing.T) {
	var tr Tree[int]
	pfx := "data:"
	for i := 0; i < 256; i++ {
		tr.Put(pfx+string([]byte{byte(i)}), i)
	}
	// 同时插入不同前缀的 key
	for i := 0; i < 50; i++ {
		tr.Put(fmt.Sprintf("other:%d", i), 1000+i)
	}
	var count int
	tr.ScanPrefix(pfx, func(k string, _ int) bool {
		if len(k) < len(pfx) || k[:len(pfx)] != pfx {
			t.Errorf("ScanPrefix returned non-matching key %q", k)
		}
		count++
		return true
	})
	if count != 256 {
		t.Fatalf("ScanPrefix got %d, want 256", count)
	}
}

func TestTree_PrefixArenaBytes_AfterReset(t *testing.T) {
	var tr Tree[int]
	for i := 0; i < 50; i++ {
		tr.Put(fmt.Sprintf("prefix/key/%d", i), i)
	}
	if tr.PrefixArenaBytes() == 0 {
		t.Fatal("expected non-zero arena bytes")
	}
	tr.Reset()
	if tr.PrefixArenaBytes() != 0 {
		t.Fatalf("arena bytes after Reset = %d, want 0", tr.PrefixArenaBytes())
	}
}

// ─── Benchmarks ──────────────────────────────────────────────────────────────

func BenchmarkTree_Put(b *testing.B) {
	keys := makeSeqKeys(b.N)
	var tr Tree[int]
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		tr.Put(keys[i], i)
	}
}

func BenchmarkTree_Get(b *testing.B) {
	const N = 100000
	keys := makeSeqKeys(N)
	var tr Tree[int]
	for i, k := range keys {
		tr.Put(k, i)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		tr.Get(keys[i%N])
	}
}

func BenchmarkTree_Put_Random(b *testing.B) {
	rng := rand.New(rand.NewPCG(42, 0))
	keys := make([]string, b.N)
	for i := range keys {
		keys[i] = randomKey(rng)
	}
	var tr Tree[int]
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		tr.Put(keys[i], i)
	}
}

func BenchmarkTree_Get_Random(b *testing.B) {
	const N = 100000
	rng := rand.New(rand.NewPCG(42, 0))
	keys := make([]string, N)
	for i := range keys {
		keys[i] = randomKey(rng)
	}
	var tr Tree[int]
	for i, k := range keys {
		tr.Put(k, i)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		tr.Get(keys[i%N])
	}
}

func BenchmarkMap_Put(b *testing.B) {
	keys := makeSeqKeys(b.N)
	m := make(map[string]int, b.N)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		m[keys[i]] = i
	}
}

func BenchmarkMap_Get(b *testing.B) {
	const N = 100000
	keys := makeSeqKeys(N)
	m := make(map[string]int, N)
	for i, k := range keys {
		m[k] = i
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = m[keys[i%N]]
	}
}

func BenchmarkTree_ScanPrefix(b *testing.B) {
	const N = 100000
	var tr Tree[int]
	for i := 0; i < N; i++ {
		tr.Put(fmt.Sprintf("user:%05d", i), i)
	}
	// 也插入一些非 user: 前缀的 key
	for i := 0; i < N/10; i++ {
		tr.Put(fmt.Sprintf("session:%05d", i), i)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		count := 0
		tr.ScanPrefix("user:", func(string, int) bool {
			count++
			return count < 100 // 只取前 100 条
		})
	}
}

func BenchmarkMap_PrefixScan(b *testing.B) {
	const N = 100000
	m := make(map[string]int, N+N/10)
	for i := 0; i < N; i++ {
		m[fmt.Sprintf("user:%05d", i)] = i
	}
	for i := 0; i < N/10; i++ {
		m[fmt.Sprintf("session:%05d", i)] = i
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		count := 0
		for k := range m {
			if strings.HasPrefix(k, "user:") {
				count++
				if count >= 100 {
					break
				}
			}
		}
	}
}

// ─── 辅助函数 ────────────────────────────────────────────────────────────────

func randomKey(rng *rand.Rand) string {
	n := rng.IntN(20) + 1
	buf := make([]byte, n)
	for i := range buf {
		buf[i] = byte('a' + rng.IntN(26))
	}
	return string(buf)
}

func makeSeqKeys(n int) []string {
	keys := make([]string, n)
	for i := range keys {
		keys[i] = fmt.Sprintf("key:%08d", i)
	}
	return keys
}
