// L09: C memcpy：Go slice <-> Go slice、Go <-> C malloc 缓冲、结构体数组
// 批量复制。所有区间互不重叠，C 只在调用期内访问指针，不保留 Go 指针。
package main

/*
#include <stdint.h>
#include <stdlib.h>
#include <string.h>

typedef struct {
	int32_t a;
	int32_t b;
	int64_t c;
} rec_t;

static void c_memcpy(void *dst, const void *src, size_t n) {
	if (n > 0) {
		memcpy(dst, src, n);
	}
}

static void *c_memcpy_alloc(const void *src, size_t n) {
	if (n == 0) {
		return NULL;
	}
	void *p = malloc(n);
	if (p != NULL) {
		memcpy(p, src, n);
	}
	return p;
}
*/
import "C"

import (
	"bytes"
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

func fnv64(b []byte) uint64 {
	h := uint64(1469598103934665603)
	for _, c := range b {
		h ^= uint64(c)
		h *= 1099511628211
	}
	return h
}

func pattern(n, seed int) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = byte((i*37 + seed*91) % 251)
	}
	return b
}

func main() {
	// 带哨兵的中间区间复制，检测越界写。
	src := pattern(16, 1)
	dst := make([]byte, 32)
	for i := range dst {
		dst[i] = 0xEE
	}
	for i := 0; i < 8; i++ {
		dst[i] = byte(0xA0 + i)
	}
	for i := 24; i < 32; i++ {
		dst[i] = byte(0xB0 + i)
	}
	C.c_memcpy(unsafe.Pointer(&dst[8]), unsafe.Pointer(&src[0]), 16)
	ck(bytes.Equal(dst[8:24], src), "memcpy middle equals src")
	for i := 0; i < 8; i++ {
		ck(dst[i] == byte(0xA0+i), fmt.Sprintf("prefix sentinel[%d]", i))
	}
	for i := 24; i < 32; i++ {
		ck(dst[i] == byte(0xB0+i), fmt.Sprintf("suffix sentinel[%d]", i))
	}
	runtime.KeepAlive(src)
	runtime.KeepAlive(dst)

	// 零长度复制不修改目标。
	before := append([]byte(nil), dst...)
	C.c_memcpy(unsafe.Pointer(&dst[0]), unsafe.Pointer(&src[0]), 0)
	ck(bytes.Equal(dst, before), "zero-length memcpy leaves dst unchanged")

	// 子区间复制：源区间外哨兵保持不变。
	src2 := pattern(24, 2)
	src2[0] = 0x11
	src2[23] = 0x22
	dst2 := make([]byte, 16)
	C.c_memcpy(unsafe.Pointer(&dst2[0]), unsafe.Pointer(&src2[4]), 8)
	ck(bytes.Equal(dst2[:8], src2[4:12]), "memcpy subrange")
	ck(dst2[8] == 0 && dst2[15] == 0, "subrange dst tail untouched")
	ck(src2[0] == 0x11 && src2[23] == 0x22, "source sentinels untouched")

	// 64 KiB 复制 + FNV-1a 校验。
	big := pattern(65536, 3)
	bigDst := make([]byte, len(big))
	C.c_memcpy(unsafe.Pointer(&bigDst[0]), unsafe.Pointer(&big[0]), C.size_t(len(big)))
	ck(bytes.Equal(bigDst, big), "64KiB memcpy bytes")
	ck(fnv64(bigDst) == fnv64(big), "64KiB memcpy hash")
	ck(bigDst[0] == big[0] && bigDst[len(big)-1] == big[len(big)-1], "64KiB memcpy endpoints")

	// C malloc 缓冲：Go -> C -> Go，复制后 free，C 不保留 Go 指针。
	cbuf := C.c_memcpy_alloc(unsafe.Pointer(&big[0]), C.size_t(len(big)))
	ck(cbuf != nil, "C malloc copy buffer")
	roundtrip := make([]byte, len(big))
	if cbuf != nil {
		C.c_memcpy(unsafe.Pointer(&roundtrip[0]), cbuf, C.size_t(len(big)))
		ck(bytes.Equal(roundtrip, big), "Go->C->Go memcpy roundtrip")
		ck(fnv64(roundtrip) == fnv64(big), "roundtrip hash")
	}

	// 结构体数组批量复制。
	const rn = 40
	srcRecs := make([]C.rec_t, rn)
	dstRecs := make([]C.rec_t, rn)
	for i := range srcRecs {
		srcRecs[i].a = C.int32_t(i)
		srcRecs[i].b = C.int32_t(i * 3)
		srcRecs[i].c = C.int64_t(int64(i)*1000 + 7)
	}
	nbytes := C.size_t(uintptr(rn) * unsafe.Sizeof(srcRecs[0]))
	C.c_memcpy(unsafe.Pointer(&dstRecs[0]), unsafe.Pointer(&srcRecs[0]), nbytes)
	for i := range dstRecs {
		ck(int32(dstRecs[i].a) == int32(i), fmt.Sprintf("rec[%d].a", i))
		ck(int32(dstRecs[i].b) == int32(i*3), fmt.Sprintf("rec[%d].b", i))
		ck(int64(dstRecs[i].c) == int64(i)*1000+7, fmt.Sprintf("rec[%d].c", i))
	}

	// 跨 GC 复核所有副本与 C 缓冲内容。
	for round := 0; round < 8; round++ {
		gcStress()
		ck(bytes.Equal(dst[8:24], src), fmt.Sprintf("sentinel copy round=%d", round))
		ck(bytes.Equal(bigDst, big), fmt.Sprintf("64KiB copy round=%d", round))
		ck(int32(dstRecs[7].a) == 7 && int64(dstRecs[39].c) == 39007, fmt.Sprintf("struct copy round=%d", round))
		if cbuf != nil {
			ck(*(*byte)(cbuf) == big[0], fmt.Sprintf("C buffer first byte round=%d", round))
			ck(*(*byte)(unsafe.Add(cbuf, len(big)-1)) == big[len(big)-1],
				fmt.Sprintf("C buffer last byte round=%d", round))
		}
		runtime.KeepAlive(big)
		runtime.KeepAlive(bigDst)
		runtime.KeepAlive(srcRecs)
		runtime.KeepAlive(dstRecs)
	}
	if cbuf != nil {
		C.free(cbuf)
	}

	finish("l09_cgo_memcpy")
}
