// Package art 实现自适应基数树（Adaptive Radix Tree / ART）。
//
// ART 是一种高效的有序字典数据结构，相比传统 hash map：
//   - 支持前缀查询和有序遍历（O(|prefix| + |result|)）
//   - 增量增长，无 rehash 停顿
//   - 内存效率高：节点自适应（4/16/48/256 子节点）
//
// 时间复杂度：
//   - Get/Put/Delete: O(k)，k = key 长度
//   - ForEach: O(N)，N = 总条目数
//   - Seek/ScanPrefix: O(k + |result|)
//
// 本实现支持二进制安全 key（key 中可含任意字节，包括 0x00）。
// 内部通过节点级 leaf 指针处理"一个 key 是另一 key 前缀"的情况。
//
// 并发安全：非线程安全。调用方需自行加锁（如 sync.RWMutex）。
package art

import "math/bits"

// Tree 是泛型自适应基数树。
//
// 零值可用（空树）。
type Tree[V any] struct {
	root        any // nil | *leaf[V] | *node4[V] | *node16[V] | *node48[V] | *node256[V]
	size        int
	slabBuf     []leaf[V] // 叶节点 slab 块
	slabPos     int
	prefixArena []byte // 前缀字节 arena：所有 inner 节点的 prefix 集中存储。
	// 替代每条前缀的独立堆分配，减少 GC 对象数量，提升缓存局部性。
	// 字节永久保留直到 Reset()，与节点生命周期一致。
}

const leafSlabSize = 64

// Len 返回树中的条目数。O(1)。
func (t *Tree[V]) Len() int { return t.size }

// Reset 清空树，释放所有节点和前缀 arena。
func (t *Tree[V]) Reset() {
	*t = Tree[V]{}
}

// PrefixArenaBytes 返回前缀 arena 当前占用的字节数。
// 用于监控：当 PrefixArenaBytes() 远大于 Size*平均前缀长度 时，
// 说明大量删除导致 arena 存在死字节，建议调用 CompactPrefixArena。
func (t *Tree[V]) PrefixArenaBytes() int {
	return len(t.prefixArena)
}

// CompactPrefixArena 重建前缀 arena，回收已删除节点遗留的死字节。
//
// 遍历所有内部节点，将存活前缀紧凑存入新 arena，更新节点中的 prefix 指针。
// 时间复杂度 O(N × avg_prefix_len)，N = 内部节点数；会分配一次新 arena。
// 业务低峰期定期调用（如每 10 万次删除后）。
// 非并发安全，与树的其他操作使用相同的外部互斥保护即可。
func (t *Tree[V]) CompactPrefixArena() {
	if t.root == nil {
		t.prefixArena = nil
		return
	}
	// 估算新 arena 大小（当前 arena 的一半作为起始容量）
	newArena := make([]byte, 0, max(len(t.prefixArena)/2, 64))
	t.compactNode(t.root, &newArena)
	t.prefixArena = newArena
}

// compactNode 深度优先遍历，将内部节点 prefix 迁移到 newArena。
func (t *Tree[V]) compactNode(n any, newArena *[]byte) {
	nd, ok := n.(artNode[V])
	if !ok {
		return // *leaf[V] 无前缀，跳过
	}
	hdr := nd.getInner()
	hdr.prefix = migratePrefix(hdr.prefix, newArena)
	nd.rangeChildren(func(_ byte, ch any) bool {
		t.compactNode(ch, newArena)
		return true
	})
}

// migratePrefix 将 src 追加到 newArena，返回指向 newArena 内部的切片。
func migratePrefix(src []byte, dst *[]byte) []byte {
	if len(src) == 0 {
		return nil
	}
	base := len(*dst)
	*dst = append(*dst, src...)
	return (*dst)[base : base+len(src)]
}

// ─── 前缀 Arena 分配辅助 ──────────────────────────────────────────────────────

// allocPfxStr 将 key[lo:hi] 追加到前缀 arena，返回指向 arena 内部的切片。
// 所有内部节点的前缀字节集中在同一 arena，减少 GC 堆对象数（N 个小对象 → 1 个 arena）。
func (t *Tree[V]) allocPfxStr(key string, lo, hi int) []byte {
	if lo >= hi {
		return nil
	}
	base := len(t.prefixArena)
	t.prefixArena = append(t.prefixArena, key[lo:hi]...)
	return t.prefixArena[base : base+(hi-lo)]
}

// allocPfxBytes 将 src 追加到前缀 arena，返回指向 arena 内部的切片。
func (t *Tree[V]) allocPfxBytes(src []byte) []byte {
	if len(src) == 0 {
		return nil
	}
	base := len(t.prefixArena)
	t.prefixArena = append(t.prefixArena, src...)
	return t.prefixArena[base : base+len(src)]
}

// allocPfxMerge 将 parent + splitByte + child 顺序追加到 arena，一次分配完成前缀合并。
// 替代原 mergePrefix 函数，消除 make([]byte, n) 的独立堆分配。
func (t *Tree[V]) allocPfxMerge(parent []byte, splitByte byte, child []byte) []byte {
	n := len(parent) + 1 + len(child)
	base := len(t.prefixArena)
	t.prefixArena = append(t.prefixArena, parent...)
	t.prefixArena = append(t.prefixArena, splitByte)
	t.prefixArena = append(t.prefixArena, child...)
	return t.prefixArena[base : base+n]
}

// allocLeaf 从 slab 分配一个 leaf 节点，摊销分配开销。
func (t *Tree[V]) allocLeaf(key string, val V) *leaf[V] {
	if t.slabPos >= len(t.slabBuf) {
		t.slabBuf = make([]leaf[V], leafSlabSize)
		t.slabPos = 0
	}
	lf := &t.slabBuf[t.slabPos]
	t.slabPos++
	lf.key = key
	lf.val = val
	return lf
}

// Get 查找 key 对应的值。
//
// 返回 (val, true) 若找到，否则 (zero, false)。
// 时间复杂度 O(k)，k = len(key)。
func (t *Tree[V]) Get(key string) (val V, ok bool) {
	if t.root == nil {
		return
	}
	return t.get(t.root, key, 0)
}

func (t *Tree[V]) get(n any, key string, depth int) (val V, ok bool) {
	for {
		switch nd := n.(type) {
		case *leaf[V]:
			if nd.key == key {
				return nd.val, true
			}
			return // key mismatch
		case artNode[V]:
			hdr := nd.getInner()
			if !hdr.checkPrefix(key, depth) {
				return
			}
			depth += len(hdr.prefix)
			if depth == len(key) {
				if hdr.leaf != nil {
					return hdr.leaf.val, true
				}
				return
			}
			n = nd.findChild(key[depth])
			if n == nil {
				return
			}
			depth++
		default:
			return
		}
	}
}

// Put 插入或更新 key-value 对。
//
// 返回 (old, true) 若替换了已有条目，否则 (zero, false)。
// 时间复杂度 O(k)，k = len(key)。
func (t *Tree[V]) Put(key string, val V) (old V, replaced bool) {
	old, replaced, t.root = t.put(t.root, key, val, 0)
	if !replaced {
		t.size++
	}
	return
}

func (t *Tree[V]) put(n any, key string, val V, depth int) (old V, replaced bool, out any) {
	if n == nil {
		// 空位：插入叶节点
		return old, false, t.allocLeaf(key, val)
	}

	switch nd := n.(type) {
	case *leaf[V]:
		if nd.key == key {
			// 替换已有叶节点
			old = nd.val
			nd.val = val
			return old, true, nd
		}
		// 分裂：创建新内部节点
		return t.splitLeaf(nd, key, val, depth)
	case *node4[V]:
		return t.putInner4(nd, key, val, depth)
	case *node16[V]:
		return t.putInner16(nd, key, val, depth)
	case *node48[V]:
		return t.putInner48(nd, key, val, depth)
	case *node256[V]:
		return t.putInner256(nd, key, val, depth)
	}
	return old, false, n
}

// splitLeaf 将叶节点和新 key 分裂为内部节点 + 两个子节点。
func (t *Tree[V]) splitLeaf(existing *leaf[V], key string, val V, depth int) (old V, replaced bool, out any) {
	existKey := existing.key

	// 找到公共前缀长度
	prefixEnd := depth
	for prefixEnd < len(existKey) && prefixEnd < len(key) && existKey[prefixEnd] == key[prefixEnd] {
		prefixEnd++
	}

	newNode := &node4[V]{}
	if prefixEnd > depth {
		newNode.prefix = t.allocPfxStr(key, depth, prefixEnd)
	}

	// 情况 1：新 key 是已有 key 的前缀
	if prefixEnd == len(key) {
		newNode.leaf = t.allocLeaf(key, val)
		if prefixEnd < len(existKey) {
			newNode.addChild(existKey[prefixEnd], existing)
		} else {
			// 两个 key 完全相同（不应到这里，在 leaf case 已处理）
			old = existing.val
			existing.val = val
			return old, true, existing
		}
		return old, false, newNode
	}

	// 情况 2：已有 key 是新 key 的前缀
	if prefixEnd == len(existKey) {
		newNode.leaf = existing
		newNode.addChild(key[prefixEnd], t.allocLeaf(key, val))
		return old, false, newNode
	}

	// 情况 3：两个 key 在 prefixEnd 处分叉
	newNode.addChild(existKey[prefixEnd], existing)
	newNode.addChild(key[prefixEnd], t.allocLeaf(key, val))
	return old, false, newNode
}

func (t *Tree[V]) putInner4(nd *node4[V], key string, val V, depth int) (old V, replaced bool, out any) {
	mismatch := nd.prefixMismatch(key, depth)
	if mismatch < len(nd.prefix) {
		return t.splitNode(nd, &nd.inner, key, val, depth, mismatch)
	}
	depth += len(nd.prefix)

	if depth == len(key) {
		// key 终止于此节点
		if nd.leaf != nil {
			old = nd.leaf.val
			nd.leaf.val = val
			return old, true, nd
		}
		nd.leaf = t.allocLeaf(key, val)
		return old, false, nd
	}

	// 查找子节点
	c := key[depth]
	for i := 0; i < int(nd.num); i++ {
		if nd.keys[i] == c {
			old, replaced, nd.children[i] = t.put(nd.children[i], key, val, depth+1)
			return old, replaced, nd
		}
	}

	// 无匹配子节点，添加新子节点
	if nd.num < 4 {
		nd.addChild(c, t.allocLeaf(key, val))
		return old, false, nd
	}
	// node4 已满，升级为 node16
	n16 := nd.grow()
	n16.addChild(c, t.allocLeaf(key, val))
	return old, false, n16
}

func (t *Tree[V]) putInner16(nd *node16[V], key string, val V, depth int) (old V, replaced bool, out any) {
	mismatch := nd.prefixMismatch(key, depth)
	if mismatch < len(nd.prefix) {
		return t.splitNode(nd, &nd.inner, key, val, depth, mismatch)
	}
	depth += len(nd.prefix)

	if depth == len(key) {
		if nd.leaf != nil {
			old = nd.leaf.val
			nd.leaf.val = val
			return old, true, nd
		}
		nd.leaf = t.allocLeaf(key, val)
		return old, false, nd
	}

	c := key[depth]
	for i := 0; i < int(nd.num); i++ {
		if nd.keys[i] == c {
			old, replaced, nd.children[i] = t.put(nd.children[i], key, val, depth+1)
			return old, replaced, nd
		}
	}

	if nd.num < 16 {
		nd.addChild(c, t.allocLeaf(key, val))
		return old, false, nd
	}
	n48 := nd.grow()
	n48.addChild(c, t.allocLeaf(key, val))
	return old, false, n48
}

func (t *Tree[V]) putInner48(nd *node48[V], key string, val V, depth int) (old V, replaced bool, out any) {
	mismatch := nd.prefixMismatch(key, depth)
	if mismatch < len(nd.prefix) {
		return t.splitNode(nd, &nd.inner, key, val, depth, mismatch)
	}
	depth += len(nd.prefix)

	if depth == len(key) {
		if nd.leaf != nil {
			old = nd.leaf.val
			nd.leaf.val = val
			return old, true, nd
		}
		nd.leaf = t.allocLeaf(key, val)
		return old, false, nd
	}

	c := key[depth]
	idx := nd.index[c]
	if idx != 0 {
		old, replaced, nd.children[idx-1] = t.put(nd.children[idx-1], key, val, depth+1)
		return old, replaced, nd
	}

	if nd.num < 48 {
		nd.addChild(c, t.allocLeaf(key, val))
		return old, false, nd
	}
	n256 := nd.grow()
	n256.addChild(c, t.allocLeaf(key, val))
	return old, false, n256
}

func (t *Tree[V]) putInner256(nd *node256[V], key string, val V, depth int) (old V, replaced bool, out any) {
	mismatch := nd.prefixMismatch(key, depth)
	if mismatch < len(nd.prefix) {
		return t.splitNode(nd, &nd.inner, key, val, depth, mismatch)
	}
	depth += len(nd.prefix)

	if depth == len(key) {
		if nd.leaf != nil {
			old = nd.leaf.val
			nd.leaf.val = val
			return old, true, nd
		}
		nd.leaf = t.allocLeaf(key, val)
		return old, false, nd
	}

	c := key[depth]
	if nd.children[c] == nil {
		nd.children[c] = t.allocLeaf(key, val)
		nd.present[c/64] |= 1 << (c % 64)
		nd.num++
		return old, false, nd
	}
	old, replaced, nd.children[c] = t.put(nd.children[c], key, val, depth+1)
	return old, replaced, nd
}

// splitNode 在前缀不匹配处分裂内部节点。
//
// 该方法处理所有内部节点类型的前缀分裂逻辑。
// mismatch 是 prefix 中首个不匹配的字节位置。
func (t *Tree[V]) splitNode(origNode any, origInner *inner[V], key string, val V, depth int, mismatch int) (old V, replaced bool, out any) {
	newNode := &node4[V]{}
	// 新节点接管前缀中匹配的部分，从 arena 分配（零独立堆对象）
	if mismatch > 0 {
		newNode.prefix = t.allocPfxBytes(origInner.prefix[:mismatch])
	}

	// 调整原节点的前缀：去掉公共部分和分叉字节，重新从 arena 分配
	splitByte := origInner.prefix[mismatch]
	origInner.prefix = t.allocPfxBytes(origInner.prefix[mismatch+1:])

	// 原节点作为新节点的子节点
	newNode.addChild(splitByte, origNode)

	// 新 key 在分叉处的处理
	actualDepth := depth + mismatch
	if actualDepth == len(key) {
		// 新 key 在分叉处终止
		newNode.leaf = t.allocLeaf(key, val)
	} else {
		newNode.addChild(key[actualDepth], t.allocLeaf(key, val))
	}

	return old, false, newNode
}

// Delete 删除 key。
//
// 返回 (old, true) 若删除了已有条目，否则 (zero, false)。
// 时间复杂度 O(k)，k = len(key)。
func (t *Tree[V]) Delete(key string) (old V, ok bool) {
	if t.root == nil {
		return
	}
	old, ok, t.root = t.del(t.root, key, 0)
	if ok {
		t.size--
	}
	return
}

func (t *Tree[V]) del(n any, key string, depth int) (old V, ok bool, out any) {
	switch nd := n.(type) {
	case *leaf[V]:
		if nd.key == key {
			old = nd.val
			// 清零释放 slab 中的引用，防止持有已删除 key 和 value
			nd.key = ""
			var zero V
			nd.val = zero
			return old, true, nil
		}
		return old, false, nd
	case *node4[V]:
		return t.delInner4(nd, key, depth)
	case *node16[V]:
		return t.delInner16(nd, key, depth)
	case *node48[V]:
		return t.delInner48(nd, key, depth)
	case *node256[V]:
		return t.delInner256(nd, key, depth)
	}
	return old, false, n
}

func (t *Tree[V]) delInner4(nd *node4[V], key string, depth int) (old V, ok bool, out any) {
	if !nd.checkPrefix(key, depth) {
		return old, false, nd
	}
	depth += len(nd.prefix)

	if depth == len(key) {
		if nd.leaf == nil {
			return old, false, nd
		}
		old = nd.leaf.val
		nd.leaf.key = ""
		var zero V
		nd.leaf.val = zero
		nd.leaf = nil
		return old, true, t.shrinkAfterDelete4(nd)
	}

	c := key[depth]
	for i := 0; i < int(nd.num); i++ {
		if nd.keys[i] == c {
			old, ok, nd.children[i] = t.del(nd.children[i], key, depth+1)
			if !ok {
				return old, false, nd
			}
			if nd.children[i] == nil {
				nd.removeChildAt(i)
				return old, true, t.shrinkAfterDelete4(nd)
			}
			return old, true, nd
		}
	}
	return old, false, nd
}

// shrinkAfterDelete4 在 node4 删除子节点或 leaf 后，尝试缩减节点。
func (t *Tree[V]) shrinkAfterDelete4(nd *node4[V]) any {
	if nd.num == 0 && nd.leaf == nil {
		return nil
	}
	if nd.num == 0 && nd.leaf != nil {
		// 只剩 leaf，返回 leaf
		return nd.leaf
	}
	if nd.num == 1 && nd.leaf == nil {
		// 只剩一个子节点，尝试合并路径
		child := nd.children[0]
		return t.mergeWithChild(nd.prefix, nd.keys[0], child)
	}
	return nd
}

// mergeWithChild 将父节点的前缀+分叉字节与子节点合并。
// 使用 arena 分配合并后的前缀（零独立堆分配）。
func (t *Tree[V]) mergeWithChild(parentPrefix []byte, key byte, child any) any {
	// 构造合并后的前缀：parentPrefix + key + childPrefix
	if _, isLeaf := child.(*leaf[V]); isLeaf {
		// 叶节点自带完整 key，无需修改前缀
		return child
	}
	if c, ok := child.(artNode[V]); ok {
		hdr := c.getInner()
		hdr.prefix = t.allocPfxMerge(parentPrefix, key, hdr.prefix)
	}
	return child
}

func (t *Tree[V]) delInner16(nd *node16[V], key string, depth int) (old V, ok bool, out any) {
	if !nd.checkPrefix(key, depth) {
		return old, false, nd
	}
	depth += len(nd.prefix)

	if depth == len(key) {
		if nd.leaf == nil {
			return old, false, nd
		}
		old = nd.leaf.val
		nd.leaf.key = ""
		var zero16 V
		nd.leaf.val = zero16
		nd.leaf = nil
		return old, true, nd
	}

	c := key[depth]
	for i := 0; i < int(nd.num); i++ {
		if nd.keys[i] == c {
			old, ok, nd.children[i] = t.del(nd.children[i], key, depth+1)
			if !ok {
				return old, false, nd
			}
			if nd.children[i] == nil {
				nd.removeChildAt(i)
				if nd.num <= 4 {
					return old, true, nd.shrink()
				}
			}
			return old, true, nd
		}
	}
	return old, false, nd
}

func (t *Tree[V]) delInner48(nd *node48[V], key string, depth int) (old V, ok bool, out any) {
	if !nd.checkPrefix(key, depth) {
		return old, false, nd
	}
	depth += len(nd.prefix)

	if depth == len(key) {
		if nd.leaf == nil {
			return old, false, nd
		}
		old = nd.leaf.val
		nd.leaf.key = ""
		var zero48 V
		nd.leaf.val = zero48
		nd.leaf = nil
		return old, true, nd
	}

	c := key[depth]
	idx := nd.index[c]
	if idx == 0 {
		return old, false, nd
	}
	old, ok, nd.children[idx-1] = t.del(nd.children[idx-1], key, depth+1)
	if !ok {
		return old, false, nd
	}
	if nd.children[idx-1] == nil {
		// 更新索引和空闲 bitmap
		nd.index[c] = 0
		nd.free |= 1 << (idx - 1)
		// 从 sorted 数组中移除 c
		for i := 0; i < int(nd.num); i++ {
			if nd.sorted[i] == c {
				for j := i; j < int(nd.num)-1; j++ {
					nd.sorted[j] = nd.sorted[j+1]
				}
				nd.sorted[nd.num-1] = 0
				break
			}
		}
		nd.num--
		if nd.num <= 16 {
			return old, true, nd.shrink()
		}
	}
	return old, true, nd
}

func (t *Tree[V]) delInner256(nd *node256[V], key string, depth int) (old V, ok bool, out any) {
	if !nd.checkPrefix(key, depth) {
		return old, false, nd
	}
	depth += len(nd.prefix)

	if depth == len(key) {
		if nd.leaf == nil {
			return old, false, nd
		}
		old = nd.leaf.val
		nd.leaf.key = ""
		var zero256 V
		nd.leaf.val = zero256
		nd.leaf = nil
		return old, true, nd
	}

	c := key[depth]
	if nd.children[c] == nil {
		return old, false, nd
	}
	old, ok, nd.children[c] = t.del(nd.children[c], key, depth+1)
	if !ok {
		return old, false, nd
	}
	if nd.children[c] == nil {
		nd.present[c/64] &^= 1 << (c % 64)
		nd.num--
		if nd.num <= 48 {
			return old, true, nd.shrink()
		}
	}
	return old, true, nd
}

// ─── 遍历 ────────────────────────────────────────────────────────────────────

// ForEach 按字典序遍历所有条目。
//
// fn 返回 false 时停止遍历。
// 时间复杂度 O(N)，N = 总条目数。
func (t *Tree[V]) ForEach(fn func(key string, val V) bool) {
	if t.root != nil {
		t.forEach(t.root, fn)
	}
}

func (t *Tree[V]) forEach(n any, fn func(string, V) bool) bool {
	switch nd := n.(type) {
	case *leaf[V]:
		return fn(nd.key, nd.val)
	case artNode[V]:
		hdr := nd.getInner()
		if hdr.leaf != nil {
			if !fn(hdr.leaf.key, hdr.leaf.val) {
				return false
			}
		}
		return nd.rangeChildren(func(_ byte, ch any) bool {
			if lf, ok := ch.(*leaf[V]); ok {
				return fn(lf.key, lf.val)
			}
			return t.forEach(ch, fn)
		})
	}
	return true
}

// Seek 按字典序遍历 key 严格大于 start 的所有条目。
//
// fn 返回 false 时停止遍历。
// 利用 ART 的有序性，时间复杂度 O(|start| + |result|)。
func (t *Tree[V]) Seek(start string, fn func(key string, val V) bool) {
	if t.root != nil {
		t.seek(t.root, start, 0, fn)
	}
}

func (t *Tree[V]) seek(n any, start string, depth int, fn func(string, V) bool) bool {
	switch nd := n.(type) {
	case *leaf[V]:
		if nd.key > start {
			return fn(nd.key, nd.val)
		}
		return true
	case *node4[V]:
		return t.seekInner(&nd.inner, nd.keys[:nd.num], nd.children[:nd.num], nil, start, depth, fn)
	case *node16[V]:
		return t.seekInner(&nd.inner, nd.keys[:nd.num], nd.children[:nd.num], nil, start, depth, fn)
	case *node48[V]:
		return t.seekNode48(nd, start, depth, fn)
	case *node256[V]:
		return t.seekNode256(nd, start, depth, fn)
	}
	return true
}

// seekInner 处理 node4/node16 的 Seek 逻辑。
func (t *Tree[V]) seekInner(hdr *inner[V], keys []byte, children []any, _ any, start string, depth int, fn func(string, V) bool) bool {
	pfx := hdr.prefix
	pfxLen := len(pfx)

	// 比较前缀
	cmp := comparePrefixRange(pfx, start, depth)

	if cmp > 0 {
		// 前缀 > start 的对应部分 → 此子树全部输出
		if hdr.leaf != nil {
			if !fn(hdr.leaf.key, hdr.leaf.val) {
				return false
			}
		}
		for i := range children {
			if !t.forEach(children[i], fn) {
				return false
			}
		}
		return true
	}
	if cmp < 0 {
		// 前缀 < start 的对应部分 → 跳过整个子树
		return true
	}

	// 前缀匹配
	depth += pfxLen

	if depth >= len(start) {
		// start 已耗尽：leaf > start 当且仅当 leaf 存在
		if hdr.leaf != nil && hdr.leaf.key > start {
			if !fn(hdr.leaf.key, hdr.leaf.val) {
				return false
			}
		}
		// 遍历所有子节点
		for i := range children {
			if !t.forEach(children[i], fn) {
				return false
			}
		}
		return true
	}

	// 继续在子节点中 seek
	target := start[depth]
	// 先输出 leaf（如果 > start）
	if hdr.leaf != nil && hdr.leaf.key > start {
		if !fn(hdr.leaf.key, hdr.leaf.val) {
			return false
		}
	}
	for i := range keys {
		if keys[i] < target {
			continue // 子节点 key < target → 跳过
		}
		if keys[i] == target {
			// 可能部分匹配，递归 seek
			if !t.seek(children[i], start, depth+1, fn) {
				return false
			}
		} else {
			// keys[i] > target → 输出整棵子树
			if !t.forEach(children[i], fn) {
				return false
			}
		}
	}
	return true
}

func (t *Tree[V]) seekNode48(nd *node48[V], start string, depth int, fn func(string, V) bool) bool {
	pfx := nd.prefix
	cmp := comparePrefixRange(pfx, start, depth)
	if cmp > 0 {
		if nd.leaf != nil {
			if !fn(nd.leaf.key, nd.leaf.val) {
				return false
			}
		}
		for i := 0; i < int(nd.num); i++ {
			b := nd.sorted[i]
			if !t.forEach(nd.children[nd.index[b]-1], fn) {
				return false
			}
		}
		return true
	}
	if cmp < 0 {
		return true
	}
	depth += len(pfx)
	if depth >= len(start) {
		if nd.leaf != nil && nd.leaf.key > start {
			if !fn(nd.leaf.key, nd.leaf.val) {
				return false
			}
		}
		for i := 0; i < int(nd.num); i++ {
			b := nd.sorted[i]
			if !t.forEach(nd.children[nd.index[b]-1], fn) {
				return false
			}
		}
		return true
	}

	target := start[depth]
	if nd.leaf != nil && nd.leaf.key > start {
		if !fn(nd.leaf.key, nd.leaf.val) {
			return false
		}
	}
	for i := 0; i < int(nd.num); i++ {
		b := nd.sorted[i]
		if b < target {
			continue
		}
		idx := nd.index[b]
		if b == target {
			if !t.seek(nd.children[idx-1], start, depth+1, fn) {
				return false
			}
		} else {
			if !t.forEach(nd.children[idx-1], fn) {
				return false
			}
		}
	}
	return true
}

func (t *Tree[V]) seekNode256(nd *node256[V], start string, depth int, fn func(string, V) bool) bool {
	pfx := nd.prefix
	cmp := comparePrefixRange(pfx, start, depth)
	if cmp > 0 {
		if nd.leaf != nil {
			if !fn(nd.leaf.key, nd.leaf.val) {
				return false
			}
		}
		for w := 0; w < 4; w++ {
			word := nd.present[w]
			for word != 0 {
				bit := bits.TrailingZeros64(word)
				b := byte(w*64 + bit)
				if !t.forEach(nd.children[b], fn) {
					return false
				}
				word &= word - 1
			}
		}
		return true
	}
	if cmp < 0 {
		return true
	}
	depth += len(pfx)
	if depth >= len(start) {
		if nd.leaf != nil && nd.leaf.key > start {
			if !fn(nd.leaf.key, nd.leaf.val) {
				return false
			}
		}
		for w := 0; w < 4; w++ {
			word := nd.present[w]
			for word != 0 {
				bit := bits.TrailingZeros64(word)
				b := byte(w*64 + bit)
				if !t.forEach(nd.children[b], fn) {
					return false
				}
				word &= word - 1
			}
		}
		return true
	}

	target := start[depth]
	if nd.leaf != nil && nd.leaf.key > start {
		if !fn(nd.leaf.key, nd.leaf.val) {
			return false
		}
	}
	// 遍历 target 所在 word 及之后的 word
	startWord := int(target) / 64
	for w := startWord; w < 4; w++ {
		word := nd.present[w]
		if w == startWord {
			// 屏蔽掉 < target 的位
			mask := uint64(target) % 64
			word &= ^((1 << mask) - 1)
		}
		for word != 0 {
			bit := bits.TrailingZeros64(word)
			b := byte(w*64 + bit)
			if b == target {
				if !t.seek(nd.children[b], start, depth+1, fn) {
					return false
				}
			} else {
				if !t.forEach(nd.children[b], fn) {
					return false
				}
			}
			word &= word - 1
		}
	}
	return true
}

// ScanPrefix 按字典序遍历所有以 prefix 为前缀的条目。
//
// fn 返回 false 时停止遍历。
// 时间复杂度 O(|prefix| + |result|)。
func (t *Tree[V]) ScanPrefix(prefix string, fn func(key string, val V) bool) {
	if t.root != nil {
		t.scanPrefix(t.root, prefix, 0, fn)
	}
}

func (t *Tree[V]) scanPrefix(n any, prefix string, depth int, fn func(string, V) bool) bool {
	switch nd := n.(type) {
	case *leaf[V]:
		if len(nd.key) >= len(prefix) && nd.key[:len(prefix)] == prefix {
			return fn(nd.key, nd.val)
		}
		return true
	case *node4[V]:
		return t.scanPrefixInner(&nd.inner, nd.keys[:nd.num], nd.children[:nd.num], prefix, depth, fn)
	case *node16[V]:
		return t.scanPrefixInner(&nd.inner, nd.keys[:nd.num], nd.children[:nd.num], prefix, depth, fn)
	case *node48[V]:
		return t.scanPrefixNode48(nd, prefix, depth, fn)
	case *node256[V]:
		return t.scanPrefixNode256(nd, prefix, depth, fn)
	}
	return true
}

func (t *Tree[V]) scanPrefixInner(hdr *inner[V], keys []byte, children []any, prefix string, depth int, fn func(string, V) bool) bool {
	pfx := hdr.prefix
	// 检查前缀匹配
	for i := 0; i < len(pfx); i++ {
		if depth+i >= len(prefix) {
			// prefix 已耗尽，此子树内所有条目都匹配
			if hdr.leaf != nil {
				if !fn(hdr.leaf.key, hdr.leaf.val) {
					return false
				}
			}
			for j := range children {
				if !t.forEach(children[j], fn) {
					return false
				}
			}
			return true
		}
		if pfx[i] != prefix[depth+i] {
			return true // 前缀不匹配，跳过
		}
	}
	depth += len(pfx)

	if depth >= len(prefix) {
		// prefix 已耗尽，输出此子树全部
		if hdr.leaf != nil {
			if !fn(hdr.leaf.key, hdr.leaf.val) {
				return false
			}
		}
		for j := range children {
			if !t.forEach(children[j], fn) {
				return false
			}
		}
		return true
	}

	// 继续按 prefix 的下一个字节查找子节点
	target := prefix[depth]
	for i := range keys {
		if keys[i] == target {
			return t.scanPrefix(children[i], prefix, depth+1, fn)
		}
	}
	return true // 无匹配子节点
}

func (t *Tree[V]) scanPrefixNode48(nd *node48[V], prefix string, depth int, fn func(string, V) bool) bool {
	pfx := nd.prefix
	for i := 0; i < len(pfx); i++ {
		if depth+i >= len(prefix) {
			if nd.leaf != nil {
				if !fn(nd.leaf.key, nd.leaf.val) {
					return false
				}
			}
			for j := 0; j < int(nd.num); j++ {
				b := nd.sorted[j]
				if !t.forEach(nd.children[nd.index[b]-1], fn) {
					return false
				}
			}
			return true
		}
		if pfx[i] != prefix[depth+i] {
			return true
		}
	}
	depth += len(pfx)
	if depth >= len(prefix) {
		if nd.leaf != nil {
			if !fn(nd.leaf.key, nd.leaf.val) {
				return false
			}
		}
		for j := 0; j < int(nd.num); j++ {
			b := nd.sorted[j]
			if !t.forEach(nd.children[nd.index[b]-1], fn) {
				return false
			}
		}
		return true
	}
	target := prefix[depth]
	idx := nd.index[target]
	if idx == 0 {
		return true
	}
	return t.scanPrefix(nd.children[idx-1], prefix, depth+1, fn)
}

func (t *Tree[V]) scanPrefixNode256(nd *node256[V], prefix string, depth int, fn func(string, V) bool) bool {
	pfx := nd.prefix
	for i := 0; i < len(pfx); i++ {
		if depth+i >= len(prefix) {
			if nd.leaf != nil {
				if !fn(nd.leaf.key, nd.leaf.val) {
					return false
				}
			}
			for w := 0; w < 4; w++ {
				word := nd.present[w]
				for word != 0 {
					bit := bits.TrailingZeros64(word)
					b := byte(w*64 + bit)
					if !t.forEach(nd.children[b], fn) {
						return false
					}
					word &= word - 1
				}
			}
			return true
		}
		if pfx[i] != prefix[depth+i] {
			return true
		}
	}
	depth += len(pfx)
	if depth >= len(prefix) {
		if nd.leaf != nil {
			if !fn(nd.leaf.key, nd.leaf.val) {
				return false
			}
		}
		for w := 0; w < 4; w++ {
			word := nd.present[w]
			for word != 0 {
				bit := bits.TrailingZeros64(word)
				b := byte(w*64 + bit)
				if !t.forEach(nd.children[b], fn) {
					return false
				}
				word &= word - 1
			}
		}
		return true
	}
	target := prefix[depth]
	if nd.present[target/64]&(1<<(target%64)) == 0 {
		return true
	}
	return t.scanPrefix(nd.children[target], prefix, depth+1, fn)
}

// ─── 前缀比较辅助 ────────────────────────────────────────────────────────────

// comparePrefixRange 比较节点前缀与 start[depth:] 的字典序关系。
//
// 返回值：
//
//	-1：prefix < start 对应部分
//	 0：prefix == start 对应部分（或 prefix 是 start 的前缀）
//	+1：prefix > start 对应部分
func comparePrefixRange(prefix []byte, start string, depth int) int {
	for i := 0; i < len(prefix); i++ {
		si := depth + i
		if si >= len(start) {
			return 1 // prefix 更长 → prefix > start
		}
		if prefix[i] < start[si] {
			return -1
		}
		if prefix[i] > start[si] {
			return 1
		}
	}
	return 0
}
