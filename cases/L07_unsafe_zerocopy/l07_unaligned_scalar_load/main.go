// L07: 非对齐标量读写。在字节缓冲的奇数偏移上按 uint16/uint32/uint64 类型读写，
// 与逐字节小端拼装对照；只保留内部指针跨显式 GC 校验底层对象存活。
package main

import (
	"encoding/binary"
	"fmt"
	"os"
	"runtime"
	"strconv"
	"sync/atomic"
	"unsafe"
)

var (
	failed int32
	gcSink [][]byte
	seed   int64
)

func ck(ok bool, msg string) {
	if !ok {
		atomic.AddInt32(&failed, 1)
		fmt.Printf("FAIL: %s (seed=%d)\n", msg, seed)
	}
}

func gcStress() {
	for r := 0; r < 2; r++ {
		for i := 0; i < 12; i++ {
			b := make([]byte, 6144+i)
			b[0] = byte(i)
			b[len(b)-1] = byte(200 - i)
			gcSink = append(gcSink[:0], b)
		}
		gcSink = gcSink[:0]
		runtime.GC()
	}
}

func finish(name string) {
	if n := atomic.LoadInt32(&failed); n != 0 {
		fmt.Printf("FAILED %s (%d failed checks)\n", name, n)
		os.Exit(1)
	}
	fmt.Println("ALL PASS " + name)
}

func pat(i int, s int64) byte {
	return byte((uint64(i)*37 + uint64(s)*11 + 5) & 0xff)
}

func fill(buf []byte, s int64) {
	for i := range buf {
		buf[i] = pat(i, s)
	}
}

func want16(off int, s int64) uint16 {
	return uint16(pat(off, s)) | uint16(pat(off+1, s))<<8
}

func want32(off int, s int64) uint32 {
	return uint32(pat(off, s)) |
		uint32(pat(off+1, s))<<8 |
		uint32(pat(off+2, s))<<16 |
		uint32(pat(off+3, s))<<24
}

func want64(off int, s int64) uint64 {
	var v uint64
	for k := 0; k < 8; k++ {
		v |= uint64(pat(off+k, s)) << (8 * uint(k))
	}
	return v
}

func main() {
	if s := os.Getenv("UAF_SEED"); s != "" {
		if v, err := strconv.ParseInt(s, 10, 64); err == nil {
			seed = v
		}
	}
	if seed < 0 {
		seed = -seed
	}

	buf := new([96]byte)
	fill(buf[:], seed)
	orig := append([]byte(nil), buf[:]...)

	base := unsafe.Pointer(&buf[0])

	// 1) 奇数偏移上的非对齐读取：与 encoding/binary 及逐字节拼装对照。
	misaligned4 := 0
	misaligned8 := 0
	for _, off := range []int{0, 1, 2, 3, 5, 7, 9, 13, 17, 23} {
		p4 := unsafe.Add(base, uintptr(off))
		if uintptr(p4)%unsafe.Alignof(uint32(0)) != 0 {
			misaligned4++
		}
		got4 := *(*uint32)(p4)
		ck(got4 == want32(off, seed), "unaligned uint32 value off="+strconv.Itoa(off))
		ck(got4 == binary.LittleEndian.Uint32(buf[off:off+4]), "unaligned uint32 vs binary off="+strconv.Itoa(off))

		p2 := unsafe.Add(base, uintptr(off))
		got2 := *(*uint16)(p2)
		ck(got2 == want16(off, seed), "unaligned uint16 value off="+strconv.Itoa(off))
		ck(got2 == binary.LittleEndian.Uint16(buf[off:off+2]), "unaligned uint16 vs binary off="+strconv.Itoa(off))
	}
	for _, off := range []int{0, 1, 3, 5, 8, 11, 16, 21} {
		p8 := unsafe.Add(base, uintptr(off))
		if uintptr(p8)%unsafe.Alignof(uint64(0)) != 0 {
			misaligned8++
		}
		got8 := *(*uint64)(p8)
		ck(got8 == want64(off, seed), "unaligned uint64 value off="+strconv.Itoa(off))
		ck(got8 == binary.LittleEndian.Uint64(buf[off:off+8]), "unaligned uint64 vs binary off="+strconv.Itoa(off))
	}
	ck(misaligned4 > 0, "at least one uint32 offset is misaligned")
	ck(misaligned8 > 0, "at least one uint64 offset is misaligned")

	// 2) 非对齐写入：写值按小端落字节，读回一致，恢复后输入不变。
	for _, off := range []int{1, 3, 5, 9} {
		v := want32(off, seed) ^ 0xa5a5a5a5
		p := unsafe.Add(base, uintptr(off))
		*(*uint32)(p) = v
		ck(binary.LittleEndian.Uint32(buf[off:off+4]) == v, "unaligned uint32 store bytes off="+strconv.Itoa(off))
		ck(*(*uint32)(p) == v, "unaligned uint32 store readback off="+strconv.Itoa(off))
		copy(buf[off:off+4], orig[off:off+4])
	}
	for _, off := range []int{1, 3, 7, 13} {
		v := want64(off, seed) ^ 0x5a5a5a5a5a5a5a5a
		p := unsafe.Add(base, uintptr(off))
		*(*uint64)(p) = v
		ck(binary.LittleEndian.Uint64(buf[off:off+8]) == v, "unaligned uint64 store bytes off="+strconv.Itoa(off))
		ck(*(*uint64)(p) == v, "unaligned uint64 store readback off="+strconv.Itoa(off))
		copy(buf[off:off+8], orig[off:off+8])
	}
	ck(string(buf[:]) == string(orig), "buffer restored after unaligned stores")

	// 3) 结构体字节视图上的非对齐字段读取。
	type mixed struct {
		a uint8
		b [3]byte
		c uint64
		d uint16
		e [7]byte
	}
	m := &mixed{a: 0x11, b: [3]byte{0x22, 0x33, 0x44}, c: 0x1122334455667788, d: 0x99aa}
	raw := unsafe.Slice((*byte)(unsafe.Pointer(m)), int(unsafe.Sizeof(*m)))
	for i := 1; i+4 <= len(raw); i += 3 {
		v := *(*uint32)(unsafe.Pointer(&raw[i]))
		want := uint32(raw[i]) | uint32(raw[i+1])<<8 | uint32(raw[i+2])<<16 | uint32(raw[i+3])<<24
		ck(v == want, "struct raw unaligned uint32 i="+strconv.Itoa(i))
	}

	// 4) 只保留内部指针，丢弃缓冲变量，跨显式 GC 仍可读。
	off := 5 + int(seed%3)
	keep := (*uint32)(unsafe.Add(unsafe.Pointer(&buf[0]), uintptr(off)))
	keep8 := (*uint64)(unsafe.Add(unsafe.Pointer(&buf[0]), uintptr(off+3)))
	want := want32(off, seed)
	want8 := want64(off+3, seed)
	buf = nil
	orig = nil
	gcStress()

	ck(*keep == want, "uint32 interior pointer after GC off="+strconv.Itoa(off))
	ck(*keep8 == want8, "uint64 interior pointer after GC off="+strconv.Itoa(off+3))
	view := unsafe.Slice((*byte)(unsafe.Pointer(keep)), 8)
	for k := 0; k < 8; k++ {
		ck(view[k] == pat(off+k, seed), "byte view after GC k="+strconv.Itoa(k))
	}
	ck(uintptr(unsafe.Pointer(keep8))-uintptr(unsafe.Pointer(keep)) == 3, "interior pointer offset delta")

	finish("l07_unaligned_scalar_load")
}
