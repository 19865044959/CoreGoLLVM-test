// L09: C 堆分配对齐：posix_memalign 按 8..4096 字节对齐分配，C 侧填充
// 模式并由 Go 直接读回；跨多轮显式 GC 校验内容/哈希/对齐；另验证
// malloc 的 max_align_t 对齐与非法对齐参数（EINVAL）。C 只持有自己
// 分配的内存，不保留 Go 指针。
package main

/*
#include <stdint.h>
#include <stddef.h>
#include "ma.h"
*/
import "C"

import (
	"fmt"
	"os"
	"runtime"
	"sync/atomic"
	"unsafe"
)

var (
	failed int32
	gcSink [][]byte
)

func ck(ok bool, msg string) {
	if !ok {
		atomic.AddInt32(&failed, 1)
		fmt.Println("FAIL:", msg)
	}
}

func gcStress() {
	for i := 0; i < 16; i++ {
		b := make([]byte, 4096)
		b[0] = byte(i)
		gcSink = append(gcSink[:0], b)
	}
	gcSink = gcSink[:0]
	runtime.GC()
	runtime.GC()
}

func finish(name string) {
	if n := atomic.LoadInt32(&failed); n != 0 {
		fmt.Printf("FAILED %s (%d failed checks)\n", name, n)
		os.Exit(1)
	}
	fmt.Println("ALL PASS " + name)
}

type blk struct {
	p     unsafe.Pointer
	n     int
	align int
	seed  uint32
	hash  uint32
}

func patByte(seed uint32, i int) byte {
	return byte(seed ^ (uint32(i)*131 + 17))
}

func expectedHash(n int, seed uint32) uint32 {
	h := uint32(2166136261)
	for i := 0; i < n; i++ {
		h ^= uint32(patByte(seed, i))
		h *= 16777619
	}
	return h
}

func main() {
	const cname = "l09_cgo_malloc_align"

	aligns := []int{8, 16, 32, 64, 128, 256, 1024, 4096}
	sizes := []int{1, 17, 64, 4096, 65535}
	var blocks []blk
	allocCount := 0
	for ai, align := range aligns {
		for si, size := range sizes {
			var rc C.int = -1
			p := C.ma_alloc(C.size_t(align), C.size_t(size), &rc)
			ck(rc == 0 && p != nil, fmt.Sprintf("posix_memalign align=%d size=%d", align, size))
			if p == nil {
				continue
			}
			allocCount++
			ck(int(C.ma_mod(p, C.size_t(align))) == 0, fmt.Sprintf("alignment align=%d", align))
			seed := uint32(align*1000 + si*7 + ai)
			C.ma_fill((*C.uint8_t)(p), C.size_t(size), C.uint32_t(seed))
			mem := unsafe.Slice((*byte)(p), size)
			ck(mem[0] == patByte(seed, 0), fmt.Sprintf("first byte align=%d size=%d", align, size))
			ck(mem[size-1] == patByte(seed, size-1), fmt.Sprintf("last byte align=%d size=%d", align, size))
			want := expectedHash(size, seed)
			ck(uint32(C.ma_hash((*C.uint8_t)(p), C.size_t(size))) == want, fmt.Sprintf("C hash align=%d size=%d", align, size))
			blocks = append(blocks, blk{p: p, n: size, align: align, seed: seed, hash: want})
		}
	}
	ck(allocCount == len(aligns)*len(sizes), "all aligned allocations succeeded")

	// 大块（256 KiB，4 KiB 对齐）也要跨 GC 保持。
	var rc C.int
	big := C.ma_alloc(4096, 256*1024, &rc)
	ck(rc == 0 && big != nil, "256KiB aligned allocation")
	if big != nil {
		C.ma_fill((*C.uint8_t)(big), C.size_t(256*1024), C.uint32_t(0xabcdef01))
		blocks = append(blocks, blk{p: big, n: 256 * 1024, align: 4096, seed: 0xabcdef01, hash: expectedHash(256*1024, 0xabcdef01)})
	}

	// 非法对齐：3（非 2 的幂）、24（非 2 的幂）、4（小于 sizeof(void*)）、0。
	einval := int32(C.ma_einval())
	for _, bad := range []int{3, 24, 4, 0} {
		var brc C.int = -1
		bp := C.ma_alloc(C.size_t(bad), 64, &brc)
		ck(bp == nil && int32(brc) == einval, fmt.Sprintf("invalid alignment %d returns EINVAL", bad))
	}

	// malloc 默认对齐到 alignof(max_align_t)。
	amax := int(C.ma_alignof_max())
	ck(amax >= 8, "alignof(max_align_t) >= 8")
	mp := C.ma_malloc(24)
	ck(mp != nil, "malloc 24 bytes")
	if mp != nil {
		ck(int(C.ma_mod(mp, C.size_t(amax))) == 0, "malloc respects max_align_t")
		C.ma_fill((*C.uint8_t)(mp), C.size_t(24), C.uint32_t(0x55aa55aa))
		mem := unsafe.Slice((*byte)(mp), 24)
		for i := 0; i < 24; i++ {
			ck(mem[i] == patByte(0x55aa55aa, i), fmt.Sprintf("malloc byte[%d]", i))
		}
		C.ma_free(mp)
	}

	// 12 轮显式 GC：C 内存内容、哈希、对齐全部稳定。
	for round := 0; round < 12; round++ {
		gcStress()
		for i, b := range blocks {
			ck(int(C.ma_mod(b.p, C.size_t(b.align))) == 0, fmt.Sprintf("round %d block %d alignment", round, i))
			mem := unsafe.Slice((*byte)(b.p), b.n)
			ck(mem[0] == patByte(b.seed, 0), fmt.Sprintf("round %d block %d first byte", round, i))
			ck(mem[b.n-1] == patByte(b.seed, b.n-1), fmt.Sprintf("round %d block %d last byte", round, i))
			ck(uint32(C.ma_hash((*C.uint8_t)(b.p), C.size_t(b.n))) == b.hash, fmt.Sprintf("round %d block %d hash", round, i))
		}
	}

	for _, b := range blocks {
		C.ma_free(b.p)
	}
	C.ma_free(nil)
	C.ma_free(C.ma_alloc(16, 8, &rc))
	finish(cname)
}
