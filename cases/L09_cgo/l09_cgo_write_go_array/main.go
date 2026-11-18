// L09: C 写 Go 数值数组并返回写入长度：固定容量填充、偏移写入、
// LEB128 变长编码。Go 侧校验返回长度、越界哨兵未被改写、
// 容量不足返回 -1，并跨 GC 复核数组内容。
package main

import (
	"fmt"
	"os"
	"runtime"
	"sync/atomic"
)

/*
#include <stdint.h>
#include <stddef.h>

// 最多写 16 个元素，返回实际写入数；cap<0 返回 -1。
static int32_t c_fill_i32(int32_t *dst, int32_t cap, int32_t seed) {
	if (cap < 0) return -1;
	int32_t n = cap > 16 ? 16 : cap;
	for (int32_t i = 0; i < n; i++) dst[i] = seed + i * 7;
	return n;
}

// 从 dst 起写 count 个元素，要求 count<=cap，否则不写并返回 -1。
static int32_t c_fill_i32_at(int32_t *dst, int32_t cap, int32_t count, int32_t seed) {
	if (count < 0 || count > cap) return -1;
	for (int32_t i = 0; i < count; i++) dst[i] = seed - i * 3;
	return count;
}

// LEB128 无符号变长编码；容量不足返回 -1 且不写越界。
static int32_t c_encode_u64(uint8_t *dst, int32_t cap, uint64_t v) {
	int32_t n = 0;
	for (;;) {
		if (n >= cap) return -1;
		uint8_t b = (uint8_t)(v & 0x7f);
		v >>= 7;
		if (v) b |= 0x80;
		dst[n++] = b;
		if (!v) break;
	}
	return n;
}
*/
import "C"

import "unsafe"

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

const sentinel = int32(0x7f7f7f7f)

func fillArray() ([]int32, int32) {
	var arr [32]int32
	for i := range arr {
		arr[i] = sentinel
	}
	n := int32(C.c_fill_i32((*C.int32_t)(unsafe.Pointer(&arr[0])), C.int32_t(len(arr)), 100))
	return arr[:], n
}

func main() {
	arr, n := fillArray()
	ck(n == 16, "fill returns capped length 16")
	for i := 0; i < 32; i++ {
		if i < 16 {
			ck(arr[i] == int32(100+i*7), fmt.Sprintf("filled arr[%d]", i))
		} else {
			ck(arr[i] == sentinel, fmt.Sprintf("sentinel arr[%d]", i))
		}
	}
	gcStress()
	for i := 0; i < 16; i++ {
		ck(arr[i] == int32(100+i*7), fmt.Sprintf("filled after GC arr[%d]", i))
	}
	runtime.KeepAlive(arr)

	// 容量 5、容量 0、负容量。
	var a2 [8]int32
	for i := range a2 {
		a2[i] = sentinel
	}
	ck(int32(C.c_fill_i32((*C.int32_t)(unsafe.Pointer(&a2[0])), 5, -3)) == 5, "cap=5 length")
	for i := 0; i < 5; i++ {
		ck(a2[i] == int32(-3+i*7), fmt.Sprintf("cap5 arr[%d]", i))
	}
	ck(a2[5] == sentinel && a2[7] == sentinel, "cap5 sentinels")
	ck(int32(C.c_fill_i32((*C.int32_t)(unsafe.Pointer(&a2[0])), 0, 9)) == 0, "cap=0 length")
	ck(a2[0] == int32(-3), "cap=0 no write")
	ck(int32(C.c_fill_i32((*C.int32_t)(unsafe.Pointer(&a2[0])), -1, 9)) == -1, "negative cap")
	ck(a2[0] == int32(-3), "negative cap no write")

	// 偏移写入：只动 [3,8)，其余哨兵不变。
	var a3 [12]int32
	for i := range a3 {
		a3[i] = sentinel
	}
	cnt := int32(C.c_fill_i32_at((*C.int32_t)(unsafe.Pointer(&a3[3])), 5, 5, 42))
	ck(cnt == 5, "offset fill length")
	for i := 0; i < 12; i++ {
		if i >= 3 && i < 8 {
			ck(a3[i] == int32(42-(i-3)*3), fmt.Sprintf("offset a3[%d]", i))
		} else {
			ck(a3[i] == sentinel, fmt.Sprintf("offset sentinel a3[%d]", i))
		}
	}
	ck(int32(C.c_fill_i32_at((*C.int32_t)(unsafe.Pointer(&a3[3])), 5, 6, 1)) == -1, "offset count>cap returns -1")
	ck(a3[3] == int32(42) && a3[8] == sentinel, "offset overflow rejected")

	// LEB128 编码：返回长度、尾部哨兵、容量不足不越界。
	values := []uint64{0, 1, 127, 128, 300, 16384, 1 << 40, 1<<63 + 12345, ^uint64(0)}
	for vi, v := range values {
		var buf [32]byte
		for i := range buf {
			buf[i] = 0xcc
		}
		bn := int32(C.c_encode_u64((*C.uint8_t)(unsafe.Pointer(&buf[0])), C.int32_t(len(buf)), C.uint64_t(v)))
		ck(bn > 0 && bn <= 10, fmt.Sprintf("varint len v=%d n=%d", vi, bn))
		dec, dn := decodeU64(buf[:bn])
		ck(dec == v && dn == int(bn), fmt.Sprintf("varint roundtrip v=%d", vi))
		for i := int(bn); i < len(buf); i++ {
			ck(buf[i] == 0xcc, fmt.Sprintf("varint sentinel v=%d i=%d", vi, i))
		}
		// 容量恰好 / 不足。
		exact := uint64(1) << 40
		need := int32(6)
		var b2 [8]byte
		for i := range b2 {
			b2[i] = 0xdd
		}
		ck(int32(C.c_encode_u64((*C.uint8_t)(unsafe.Pointer(&b2[0])), C.int32_t(need), C.uint64_t(exact))) == need,
			"exact capacity")
		b2[need-1] = 0xdd
		ck(int32(C.c_encode_u64((*C.uint8_t)(unsafe.Pointer(&b2[0])), C.int32_t(need-1), C.uint64_t(exact))) == -1,
			"short capacity returns -1")
		ck(b2[need-1] == 0xdd, "short capacity no write")
	}

	gcStress()
	ck(int32(C.c_fill_i32((*C.int32_t)(unsafe.Pointer(&a3[0])), C.int32_t(len(a3)), 7)) == 12, "fill after GC")
	ck(a3[0] == 7 && a3[11] == 7+11*7, "values after GC")
	runtime.KeepAlive(a3)

	finish("l09_cgo_write_go_array")
}

func decodeU64(b []byte) (uint64, int) {
	var v uint64
	for i, c := range b {
		if i >= 10 {
			return 0, i
		}
		v |= uint64(c&0x7f) << (7 * uint(i))
		if c&0x80 == 0 {
			return v, i + 1
		}
	}
	return 0, 0
}
