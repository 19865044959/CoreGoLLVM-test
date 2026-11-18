// L07: 非对齐的紧凑记录流。13 字节步长记录（tag/u32/u64）中没有字段天然 8 字节对齐，
// 用类型化指针在跨记录的每个非对齐偏移读写，与 encoding/binary 和逐字节视图对照。
package main

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"os"
	"runtime"
	"strconv"
	"sync/atomic"
	"unsafe"
)

const recStride = 13

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
			b := make([]byte, 4096+i*17)
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
	return byte((uint64(i)*53 + uint64(s)*7 + 3) & 0xff)
}

func val32(i int, s int64) uint32 {
	return uint32(i)*0x01010101 ^ uint32(s)*0x9e3779b1
}

func val64(i int, s int64) uint64 {
	return uint64(i)*0x0101010101010101 ^ uint64(s)*0x9e3779b97f4a7c15
}

func wantTag(i int) byte { return byte(i*7 + 1) }

func main() {
	if env := os.Getenv("UAF_SEED"); env != "" {
		if v, err := strconv.ParseInt(env, 10, 64); err == nil {
			seed = v
		}
	}
	if seed < 0 {
		seed = -seed
	}

	count := 12 + int(seed%5)
	buf := make([]byte, recStride*count+8)
	for i := range buf {
		buf[i] = pat(i, seed)
	}
	for i := 0; i < count; i++ {
		base := i * recStride
		buf[base] = wantTag(i)
		binary.LittleEndian.PutUint32(buf[base+1:base+5], val32(i, seed))
		binary.LittleEndian.PutUint64(buf[base+5:base+13], val64(i, seed))
	}
	first := append([]byte(nil), buf...)

	// 1) 每条记录的 u32/u64 都通过类型化指针读取；u64 地址覆盖全部 8 个对齐类。
	classesSeen := make([]bool, 8)
	aligned, misaligned := 0, 0
	for i := 0; i < count; i++ {
		base := i * recStride
		p8 := (*uint64)(unsafe.Pointer(&buf[base+5]))
		got8 := *p8
		ck(got8 == val64(i, seed), "u64 typed value i="+strconv.Itoa(i))
		ck(got8 == binary.LittleEndian.Uint64(buf[base+5:base+13]), "u64 vs binary i="+strconv.Itoa(i))
		p4 := (*uint32)(unsafe.Pointer(&buf[base+1]))
		got4 := *p4
		ck(got4 == val32(i, seed), "u32 typed value i="+strconv.Itoa(i))
		ck(got4 == binary.LittleEndian.Uint32(buf[base+1:base+5]), "u32 vs binary i="+strconv.Itoa(i))
		ck(buf[base] == wantTag(i), "tag i="+strconv.Itoa(i))

		cls := int(uintptr(unsafe.Pointer(p8)) & 7)
		classesSeen[cls] = true
		if cls == 0 {
			aligned++
		} else {
			misaligned++
		}
	}
	ck(aligned > 0, "has aligned u64 record")
	ck(misaligned > 0, "has misaligned u64 record")
	for cls := 0; cls < 8; cls++ {
		ck(classesSeen[cls], "u64 address class seen cls="+strconv.Itoa(cls))
	}

	// 2) 起点偏移阶梯：从 0..4 起步按同样步长切记录，u64 字段全部非对齐读取。
	for start := 0; start < 5; start++ {
		for i := 0; ; i++ {
			base := start + i*recStride
			if base+recStride > len(buf) {
				break
			}
			p8 := (*uint64)(unsafe.Pointer(&buf[base+5]))
			ck(*p8 == binary.LittleEndian.Uint64(buf[base+5:base+13]),
				"ladder u64 start="+strconv.Itoa(start)+" i="+strconv.Itoa(i))
			p4 := (*uint32)(unsafe.Pointer(&buf[base+1]))
			ck(*p4 == binary.LittleEndian.Uint32(buf[base+1:base+5]),
				"ladder u32 start="+strconv.Itoa(start)+" i="+strconv.Itoa(i))
		}
	}
	ck(bytes.Equal(buf, first), "buffer unchanged after unaligned reads")

	// 3) 非对齐写入：字节层小端正确，相邻字段不被破坏，恢复后整体一致。
	for i := 0; i < count; i += 3 {
		base := i * recStride
		v8 := val64(i, seed) ^ 0xa5a5a5a5a5a5a5a5
		p8 := (*uint64)(unsafe.Pointer(&buf[base+5]))
		*p8 = v8
		ck(binary.LittleEndian.Uint64(buf[base+5:base+13]) == v8, "unaligned u64 store bytes i="+strconv.Itoa(i))
		ck(*p8 == v8, "unaligned u64 store readback i="+strconv.Itoa(i))
		ck(buf[base] == wantTag(i), "tag preserved after u64 store i="+strconv.Itoa(i))
		copy(buf[base+5:base+13], first[base+5:base+13])

		v4 := val32(i, seed) ^ 0x5a5a5a5a
		p4 := (*uint32)(unsafe.Pointer(&buf[base+1]))
		*p4 = v4
		ck(binary.LittleEndian.Uint32(buf[base+1:base+5]) == v4, "unaligned u32 store bytes i="+strconv.Itoa(i))
		ck(buf[base] == wantTag(i), "tag preserved after u32 store i="+strconv.Itoa(i))
		copy(buf[base+1:base+5], first[base+1:base+5])
	}
	ck(bytes.Equal(buf, first), "buffer restored after unaligned stores")

	// 4) 末记录边界：最后一个 u64 恰在缓冲有效区末尾，读取不越界。
	lastBase := (count - 1) * recStride
	last := (*uint64)(unsafe.Pointer(&buf[lastBase+5]))
	ck(*last == val64(count-1, seed), "last record u64")
	ck(lastBase+recStride == recStride*count, "last record at payload end")
	ck(buf[lastBase+recStride-1] == first[lastBase+recStride-1], "last record tail intact")

	// 5) 只保留内部指针，丢弃缓冲变量，跨显式 GC 校验。
	k := count / 2
	kp := (*uint64)(unsafe.Pointer(&buf[k*recStride+5]))
	kp4 := (*uint32)(unsafe.Pointer(&buf[k*recStride+1]))
	want8 := *kp
	want4 := *kp4
	wantTag := buf[k*recStride]
	buf = nil
	first = nil
	gcStress()

	ck(*kp == want8, "u64 interior pointer after GC")
	ck(*kp4 == want4, "u32 interior pointer after GC")
	ck(uintptr(unsafe.Pointer(kp))-uintptr(unsafe.Pointer(kp4)) == 4, "interior pointer delta")
	view := unsafe.Slice((*byte)(unsafe.Pointer(kp)), 8)
	var re uint64
	for b := 0; b < 8; b++ {
		re |= uint64(view[b]) << (8 * uint(b))
	}
	ck(re == want8, "byte view of u64 after GC")
	prev := (*byte)(unsafe.Add(unsafe.Pointer(kp), -5))
	ck(*prev == wantTag, "tag before u64 after GC")

	finish("l07_unaligned_packed_stream")
}
