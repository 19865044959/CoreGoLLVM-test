// L07: 运行时对齐检查。验证基础类型/结构体字段/数组元素/slice 数据指针/手工对齐
// 槽位的地址对齐不变量，并用内部指针跨 GC 校验对象存活。
package main

import (
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
		for i := 0; i < 10; i++ {
			b := make([]byte, 4096+i*7)
			b[0] = byte(i)
			b[len(b)-1] = byte(99 - i)
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

type leaf struct {
	v    int64
	name string
}

type probe struct {
	a uint8
	b uint32
	c uint8
	d uint64
	e uint16
	p *leaf
	s string
}

type nested struct {
	x uint8
	y probe
	z [3]uint64
}

type block struct {
	pad   [3]byte
	slots [8]uint64
}

func isAligned(p unsafe.Pointer, a uintptr) bool {
	return uintptr(p)%a == 0
}

// alignedPtr 用单个表达式完成 align-up，uintptr 不跨安全点保存。
func alignedPtr[T any](base unsafe.Pointer, off uintptr) *T {
	var zero T
	a := unsafe.Alignof(zero)
	return (*T)(unsafe.Pointer((uintptr(base) + off + a - 1) &^ (a - 1)))
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

	// 1) 静态对齐属性。
	ck(unsafe.Alignof(uint8(0)) == 1, "Alignof uint8")
	ck(unsafe.Alignof(uint16(0)) == 2, "Alignof uint16")
	ck(unsafe.Alignof(uint32(0)) == 4, "Alignof uint32")
	ck(unsafe.Alignof(uint64(0)) == 8, "Alignof uint64")
	ck(unsafe.Alignof(int64(0)) == 8, "Alignof int64")
	ck(unsafe.Alignof(uintptr(0)) == unsafe.Alignof((*byte)(nil)), "Alignof uintptr == pointer")
	ck(unsafe.Alignof(probe{}) >= unsafe.Alignof(uint64(0)), "probe alignment")
	ck(unsafe.Alignof([3]probe{}) == unsafe.Alignof(probe{}), "array alignment")
	ck(unsafe.Alignof(nested{}) >= unsafe.Alignof(probe{}), "nested alignment")

	// 2) Offsetof 必须是字段对齐的整数倍，Sizeof 是结构体对齐的整数倍。
	ck(unsafe.Offsetof(probe{}.b)%unsafe.Alignof(uint32(0)) == 0, "b offset aligned")
	ck(unsafe.Offsetof(probe{}.d)%unsafe.Alignof(uint64(0)) == 0, "d offset aligned")
	ck(unsafe.Offsetof(probe{}.e)%unsafe.Alignof(uint16(0)) == 0, "e offset aligned")
	ck(unsafe.Offsetof(probe{}.p)%unsafe.Alignof((*leaf)(nil)) == 0, "p offset aligned")
	ck(unsafe.Offsetof(probe{}.s)%unsafe.Alignof("") == 0, "s offset aligned")
	ck(unsafe.Sizeof(probe{})%unsafe.Alignof(probe{}) == 0, "probe size multiple of align")
	ck(unsafe.Sizeof(nested{})%unsafe.Alignof(nested{}) == 0, "nested size multiple of align")
	ck(unsafe.Offsetof(nested{}.y)%unsafe.Alignof(probe{}) == 0, "nested.y offset aligned")
	ck(unsafe.Offsetof(nested{}.z)%unsafe.Alignof(uint64(0)) == 0, "nested.z offset aligned")
	ck(unsafe.Sizeof(probe{}) == 56, "probe size 64-bit")

	// 3) 堆对象字段地址按类型对齐。
	pr := &probe{
		a: 1, b: 0x11223344, c: 2, d: 0x1122334455667788, e: 0x99aa,
		p: &leaf{v: 7 + seed, name: "leaf-" + strconv.FormatInt(seed, 10)},
		s: "payload-" + strconv.FormatInt(seed, 10),
	}
	ck(isAligned(unsafe.Pointer(&pr.b), unsafe.Alignof(uint32(0))), "address of b aligned")
	ck(isAligned(unsafe.Pointer(&pr.d), unsafe.Alignof(uint64(0))), "address of d aligned")
	ck(isAligned(unsafe.Pointer(&pr.e), unsafe.Alignof(uint16(0))), "address of e aligned")
	ck(isAligned(unsafe.Pointer(pr.p), unsafe.Alignof((*leaf)(nil))), "pointer field value aligned")
	ck(isAligned(unsafe.Pointer(&pr.s), unsafe.Alignof("")), "address of s aligned")

	// 4) 数组元素地址与步长。
	arr := &[5]probe{}
	for i := range arr {
		arr[i] = probe{a: byte(i), b: uint32(i) * 3, d: uint64(i) * 11, e: uint16(i), s: "a-" + strconv.Itoa(i)}
		ck(isAligned(unsafe.Pointer(&arr[i]), unsafe.Alignof(probe{})), "array elem aligned i="+strconv.Itoa(i))
	}
	for i := 0; i+1 < len(arr); i++ {
		delta := uintptr(unsafe.Pointer(&arr[i+1])) - uintptr(unsafe.Pointer(&arr[i]))
		ck(delta == unsafe.Sizeof(probe{}), "array stride i="+strconv.Itoa(i))
	}

	// 5) slice 数据指针与元素字段对齐。
	ss := make([]probe, 4)
	for i := range ss {
		ss[i] = probe{b: uint32(i), d: uint64(i) << 8, s: "s-" + strconv.Itoa(i)}
	}
	sp := unsafe.SliceData(ss)
	ck(isAligned(unsafe.Pointer(sp), unsafe.Alignof(probe{})), "slice data aligned")
	for i := 0; i < len(ss); i++ {
		ep := (*probe)(unsafe.Add(unsafe.Pointer(sp), uintptr(i)*unsafe.Sizeof(probe{})))
		ck(ep == &ss[i], "slice elem identity i="+strconv.Itoa(i))
		ck(isAligned(unsafe.Pointer(&ep.d), unsafe.Alignof(uint64(0))), "slice elem d aligned i="+strconv.Itoa(i))
	}
	view := unsafe.Slice(sp, len(ss))
	ck(&view[3] == &ss[3], "unsafe.Slice view identity")

	// 6) 手工对齐槽位：块内 slots 天然对齐；再对任意字节基址做 align-up。
	blk := &block{}
	for i := range blk.slots {
		p := alignedPtr[uint64](unsafe.Pointer(&blk.slots[0]), uintptr(i)*unsafe.Sizeof(uint64(0)))
		ck(p == &blk.slots[i], "aligned slot identity i="+strconv.Itoa(i))
		ck(isAligned(unsafe.Pointer(p), unsafe.Alignof(uint64(0))), "aligned slot address i="+strconv.Itoa(i))
		*p = uint64(i)*0x0101010101010101 + uint64(seed)
	}
	raw := new([256]byte)
	for i := range raw {
		raw[i] = byte(i)
	}
	base := unsafe.Pointer(&raw[0])
	alignedCount := 0
	misalignedCount := 0
	for off := uintptr(0); off < 128; off += 7 {
		p := alignedPtr[uint32](base, off)
		if isAligned(unsafe.Pointer(p), unsafe.Alignof(uint32(0))) {
			alignedCount++
		} else {
			misalignedCount++
		}
		ck(uintptr(unsafe.Pointer(p))-uintptr(base)+4 <= uintptr(len(raw)),
			"aligned pointer within buffer off="+strconv.FormatUint(uint64(off), 10))
	}
	ck(alignedCount > 0 && misalignedCount == 0, "align-up always yields aligned pointers")

	// 7) 内部指针跨显式 GC：只保留槽位指针与字段指针，丢弃容器变量。
	slotPtr := &blk.slots[3]
	dPtr := &pr.d
	sPtr := &pr.s
	elemPtr := &arr[2]
	blk = nil
	pr = nil
	arr = nil
	gcStress()

	ck(*slotPtr == uint64(3)*0x0101010101010101+uint64(seed), "slot value after GC")
	ck(*dPtr == 0x1122334455667788, "d field after GC")
	ck(*sPtr == "payload-"+strconv.FormatInt(seed, 10), "s field after GC")
	ck(elemPtr.b == 6 && elemPtr.s == "a-2", "array elem after GC")
	ck(*(*uint64)(unsafe.Add(unsafe.Pointer(slotPtr), unsafe.Sizeof(uint64(0)))) ==
		uint64(4)*0x0101010101010101+uint64(seed), "neighbor slot after GC")
	ck(isAligned(unsafe.Pointer(slotPtr), unsafe.Alignof(uint64(0))), "interior slot pointer aligned")

	finish("l07_alignment_runtime_probe")
}
