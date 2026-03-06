// Package hash 提供高性能哈希函数。
package hash

// FNV-1a 64-bit 常量
const (
	offset64 = 14695981039346656037
	prime64  = 1099511628211
)

// Sum64 返回 b 的 FNV-1a 64-bit 哈希值。
func Sum64(b []byte) uint64 {
	h := uint64(offset64)
	for _, c := range b {
		h ^= uint64(c)
		h *= prime64
	}
	return h
}

// Sum64s 返回 s 的 FNV-1a 64-bit 哈希值（零分配）。
func Sum64s(s string) uint64 {
	h := uint64(offset64)
	for i := 0; i < len(s); i++ {
		h ^= uint64(s[i])
		h *= prime64
	}
	return h
}
