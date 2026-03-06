package hash

import "hash/maphash"

// 包级全局 Seed，程序启动时由运行时随机初始化。
// maphash.Seed 不可复制；此处用指针持有以允许包级 var。
// Sum64Map / Sum64sMap 的哈希值在进程间不可比较（seed 不同）。
var seed = maphash.MakeSeed()

// Sum64Map 返回 b 的 maphash 64-bit 哈希值。
//
// 利用运行时内置随机 seed，在支持 AES-NI 的平台上速度显著优于 FNV-1a。
// 注意：seed 在进程重启后变化，哈希值不可持久化或跨进程比较。
func Sum64Map(b []byte) uint64 {
	return maphash.Bytes(seed, b)
}

// Sum64sMap 返回 s 的 maphash 64-bit 哈希值（零分配）。
//
// 同 Sum64Map，但接受 string 避免 []byte 转换分配。
func Sum64sMap(s string) uint64 {
	return maphash.String(seed, s)
}
