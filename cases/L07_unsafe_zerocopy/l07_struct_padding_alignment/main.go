// L07: struct padding/对齐读取：Offsetof/Sizeof/Alignof、padding 字节、嵌套与数组步长。
package main

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"sync/atomic"
	"unsafe"
)

type pnode struct {
	v   int64
	tag string
}

type padded struct {
	a uint8
	b uint64
	c uint16
	d *pnode
	e uint32
}

type outer struct {
	tag   byte
	inner padded
	code  uint16
}

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
			b := make([]byte, 8192)
			b[0] = byte(i)
			b[len(b)-1] = byte(255 - i)
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

func paddingZero(raw []byte, lo, hi int, stage string) {
	zero := true
	for i := lo; i < hi; i++ {
		if raw[i] != 0 {
			zero = false
		}
	}
	ck(zero, stage+" padding ["+strconv.Itoa(lo)+","+strconv.Itoa(hi)+")")
}

// paddedView 只返回结构体的字节视图；结构体本身离开作用域后只能由视图保活。
//
//go:noinline
func paddedView(bVal uint64, eVal uint32) []byte {
	q := &padded{a: 1, b: bVal, c: 2, e: eVal}
	return unsafe.Slice((*byte)(unsafe.Pointer(q)), int(unsafe.Sizeof(*q)))
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

	// 1) 64 位布局断言：字段偏移、内部 padding、末尾 padding 与总大小。
	p := new(padded)
	ck(unsafe.Sizeof(*p) == 40, "struct size")
	ck(unsafe.Alignof(*p) == 8, "struct align")
	ck(unsafe.Offsetof(p.a) == 0, "offset a")
	ck(unsafe.Offsetof(p.b) == 8, "offset b")
	ck(unsafe.Offsetof(p.c) == 16, "offset c")
	ck(unsafe.Offsetof(p.d) == 24, "offset d")
	ck(unsafe.Offsetof(p.e) == 32, "offset e")

	// 字段地址按 8 字节对齐（单表达式取模，不保存 uintptr）。
	ck(uintptr(unsafe.Pointer(&p.b))%8 == 0, "b aligned")
	ck(uintptr(unsafe.Pointer(&p.d))%8 == 0, "d aligned")
	ck(uintptr(unsafe.Pointer(p))%8 == 0, "struct aligned")

	raw := unsafe.Slice((*byte)(unsafe.Pointer(p)), int(unsafe.Sizeof(*p)))
	ck(len(raw) == 40, "byte view length")
	paddingZero(raw, 1, 8, "zero struct")
	paddingZero(raw, 18, 24, "zero struct")
	paddingZero(raw, 36, 40, "zero struct")

	// 2) 普通字段写入后，padding 不得被触碰。
	bVal := uint64(seed*3 + 7)
	eVal := uint32(seed) + 11
	p.a = 0xA5
	p.b = bVal
	p.c = 0x1234
	p.e = eVal
	ck(p.a == 0xA5 && p.b == bVal && p.c == 0x1234 && p.e == eVal, "field values")
	paddingZero(raw, 1, 8, "after field writes")
	paddingZero(raw, 18, 24, "after field writes")
	paddingZero(raw, 36, 40, "after field writes")

	// 3) 通过 unsafe.Add + Offsetof 写字段，读取结果与普通写一致。
	*(*uint64)(unsafe.Add(unsafe.Pointer(p), unsafe.Offsetof(p.b))) = bVal + 100
	*(*uint16)(unsafe.Add(unsafe.Pointer(p), unsafe.Offsetof(p.c))) = 0xBEEF
	ck(p.b == bVal+100 && p.c == 0xBEEF, "unsafe field writes")
	paddingZero(raw, 1, 8, "after unsafe writes")
	paddingZero(raw, 36, 40, "after unsafe writes")

	// 4) 指针字段经类型化 unsafe 槽位写入，旧值在局部引用下跨 GC 有效。
	old := &pnode{v: int64(seed) + 1, tag: "old"}
	p.d = old
	oldRef := p.d
	gcStress() // 让 p 与 old 晋升，后续 unsafe 写入构成老->新引用
	newD := &pnode{v: int64(seed) + 2, tag: "new"}
	*(**pnode)(unsafe.Add(unsafe.Pointer(p), unsafe.Offsetof(p.d))) = newD
	ck(p.d == newD && oldRef != newD, "pointer slot write")

	// 5) 只修改 padding 字节，不影响任何字段。
	savedA, savedB, savedC, savedE := p.a, p.b, p.c, p.e
	raw[1] = 0xAA
	raw[19] = 0x55
	raw[37] = 0x77
	ck(p.a == savedA && p.b == savedB && p.c == savedC && p.e == savedE, "padding writes isolated")
	raw[1], raw[19], raw[37] = 0, 0, 0
	paddingZero(raw, 1, 8, "restored")

	// 6) 嵌套结构体布局与内层字段写入。
	o := new(outer)
	ck(unsafe.Offsetof(o.tag) == 0, "outer tag offset")
	ck(unsafe.Offsetof(o.inner) == 8, "outer inner offset")
	ck(unsafe.Offsetof(o.code) == 48, "outer code offset")
	ck(unsafe.Sizeof(*o) == 56, "outer size")
	*(*uint64)(unsafe.Add(unsafe.Pointer(&o.inner), unsafe.Offsetof(o.inner.b))) = bVal + 5
	o.inner.d = &pnode{v: 9, tag: "nested"}
	o.code = 0xCAFE
	ck(o.inner.b == bVal+5 && o.inner.d.v == 9 && o.code == 0xCAFE, "nested writes")

	// 7) 数组步长等于 Sizeof，元素地址与 GEP 一致。
	arr := new([6]padded)
	for i := range arr {
		arr[i] = padded{a: byte(i + 1), b: uint64(i * 17), c: uint16(i), e: uint32(i * 3)}
	}
	stride := int(unsafe.Sizeof(padded{}))
	for k := 0; k < 6; k++ {
		q := (*padded)(unsafe.Add(unsafe.Pointer(&arr[0]), k*stride))
		ck(q == &arr[k], "array GEP identity k="+strconv.Itoa(k))
		ck(q.b == uint64(k*17) && q.c == uint16(k), "array element value")
	}

	// 8) 先保留 p 验证 unsafe 指针槽位写入经过 GC；再丢弃基址只留派生视图。
	idx := int(seed) % 6
	ep := &arr[idx]
	ev := unsafe.Slice((*byte)(unsafe.Pointer(ep)), stride)
	oldD := oldRef
	gcStress()
	ck(p.d == newD && p.d.v == int64(seed)+2 && p.d.tag == "new", "pointer slot after GC")
	ck(ep.b == uint64(idx*17) && ep.c == uint16(idx) && ep.a == byte(idx+1), "elem ptr after GC")
	p = nil
	arr = nil
	gcStress()
	ck(ep.b == uint64(idx*17) && ep.c == uint16(idx) && ep.a == byte(idx+1), "elem ptr after base drop")
	paddingZero(ev, 1, 8, "elem view after GC")
	paddingZero(ev, 18, 24, "elem view after GC")
	back := (*padded)(unsafe.Pointer(unsafe.SliceData(ev)))
	ck(back.b == uint64(idx*17) && back.c == uint16(idx), "view reinterpret after GC")
	ck(oldD != nil && oldD.v == int64(seed)+1 && oldD.tag == "old", "replaced pointer target after GC")

	// 9) 纯视图保活：结构体变量消失，只有 []byte 视图可达。
	view := paddedView(bVal+3, eVal+4)
	gcStress()
	q := (*padded)(unsafe.Pointer(unsafe.SliceData(view)))
	ck(q.b == bVal+3 && q.e == eVal+4 && q.a == 1 && q.c == 2, "view keeps struct alive")
	ck(q.d == nil, "fresh struct pointer field nil")
	paddingZero(view, 1, 8, "view alive")
	paddingZero(view, 18, 24, "view alive")

	// 10) 多次读取 padding，确认无残留写入。
	for i := 0; i < 128; i++ {
		paddingZero(view, 1, 8, "repeat")
		paddingZero(view, 36, 40, "repeat")
	}

	runtime.KeepAlive(ep)
	runtime.KeepAlive(ev)
	runtime.KeepAlive(view)
	runtime.KeepAlive(oldD)
	finish("l07_struct_padding_alignment")
}
