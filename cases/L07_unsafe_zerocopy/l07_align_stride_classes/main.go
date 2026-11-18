// L07: 内存对齐/padding：多种对齐类结构体的 Sizeof/Alignof/Offsetof、
// 数组步长、padding 字节、含指针与 interface 字段的元素跨 GC。
package main

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"sync/atomic"
	"unsafe"
)

type aleaf struct {
	v   int64
	tag string
}

type a1 struct {
	b byte
}

type a2 struct {
	b byte
	s uint16
}

type a4 struct {
	b byte
	w uint32
	h uint16
}

type a8 struct {
	b byte
	w uint64
	p *aleaf
	s string
	i any
}

type ca struct {
	tag byte
	v4  a4
	v8  a8
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
		for i := 0; i < 10; i++ {
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

func rawOf(p unsafe.Pointer, n uintptr) []byte {
	return unsafe.Slice((*byte)(p), int(n))
}

func zeroBytes(raw []byte, ranges ...int) {
	for i := 0; i+1 < len(ranges); i += 2 {
		for j := ranges[i]; j < ranges[i+1]; j++ {
			raw[j] = 0
		}
	}
}

func paddingZero(raw []byte, lo, hi int, stage string) {
	for i := lo; i < hi; i++ {
		if raw[i] != 0 {
			ck(false, stage+" padding ["+strconv.Itoa(lo)+","+strconv.Itoa(hi)+")")
			return
		}
	}
	ck(true, stage)
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

	// 1) 对齐类矩阵。
	ck(unsafe.Sizeof(a1{}) == 1 && unsafe.Alignof(a1{}) == 1, "a1 size/align")
	ck(unsafe.Sizeof(a2{}) == 4 && unsafe.Alignof(a2{}) == 2, "a2 size/align")
	ck(unsafe.Offsetof(a2{}.s) == 2, "a2 s offset")
	ck(unsafe.Sizeof(a4{}) == 12 && unsafe.Alignof(a4{}) == 4, "a4 size/align")
	ck(unsafe.Offsetof(a4{}.w) == 4 && unsafe.Offsetof(a4{}.h) == 8, "a4 field offsets")
	ck(unsafe.Sizeof(a8{}) == 56 && unsafe.Alignof(a8{}) == 8, "a8 size/align")
	ck(unsafe.Offsetof(a8{}.w) == 8 && unsafe.Offsetof(a8{}.p) == 16, "a8 w/p offsets")
	ck(unsafe.Offsetof(a8{}.s) == 24 && unsafe.Offsetof(a8{}.i) == 40, "a8 s/i offsets")
	ck(unsafe.Sizeof(ca{}) == 72 && unsafe.Alignof(ca{}) == 8, "ca size/align")
	ck(unsafe.Offsetof(ca{}.v4) == 4 && unsafe.Offsetof(ca{}.v8) == 16, "ca field offsets")
	ck(unsafe.Offsetof(ca{}.v8)+unsafe.Offsetof(a8{}.p) == 32, "ca nested p offset")
	ck(unsafe.Offsetof(ca{}.v8)+unsafe.Offsetof(a8{}.i) == 56, "ca nested i offset")
	ck(unsafe.Sizeof(string("")) == 16 && unsafe.Sizeof(any(nil)) == 16, "string/any width")
	ck(unsafe.Alignof(any(nil)) == 8, "any align")

	// 2) 地址对齐与 padding 区间。
	v4 := new(a4)
	p4 := unsafe.Pointer(v4)
	ck(uintptr(unsafe.Pointer(&v4.w))%4 == 0, "a4 w aligned")
	ck(uintptr(unsafe.Pointer(&v4.h))%2 == 0, "a4 h aligned")
	ck(uintptr(unsafe.Pointer(v4))%4 == 0, "a4 base aligned")
	r4 := rawOf(p4, unsafe.Sizeof(*v4))
	zeroBytes(r4, 1, 4, 10, 12) // 栈对象的 padding 内容未定义，先显式置零
	v4.b, v4.w, v4.h = 0x11, 0x22334455, 0x6677
	paddingZero(r4, 1, 4, "a4 leading")
	paddingZero(r4, 10, 12, "a4 trailing")

	v8 := new(a8)
	r8 := rawOf(unsafe.Pointer(v8), unsafe.Sizeof(*v8))
	zeroBytes(r8, 1, 8)
	v8.b = 0x22
	v8.w = uint64(seed)*3 + 11
	v8.p = &aleaf{v: seed + 1, tag: "p"}
	v8.s = "str-" + strconv.FormatInt(seed, 10)
	v8.i = &aleaf{v: seed + 2, tag: "i"}
	paddingZero(r8, 1, 8, "a8 leading")
	ck(uintptr(unsafe.Pointer(&v8.w))%8 == 0 && uintptr(unsafe.Pointer(&v8.p))%8 == 0, "a8 8-byte fields aligned")
	ck(uintptr(unsafe.Pointer(v8))%8 == 0, "a8 base aligned")

	// 3) 通过 unsafe.Add + Offsetof 写字段，padding 不受影响。
	*(*uint64)(unsafe.Add(unsafe.Pointer(v8), unsafe.Offsetof(a8{}.w))) = uint64(seed) + 99
	*(**aleaf)(unsafe.Add(unsafe.Pointer(v8), unsafe.Offsetof(a8{}.p))) = &aleaf{v: seed + 3, tag: "up"}
	*(*string)(unsafe.Add(unsafe.Pointer(v8), unsafe.Offsetof(a8{}.s))) = "up-" + strconv.FormatInt(seed, 10)
	*(*any)(unsafe.Add(unsafe.Pointer(v8), unsafe.Offsetof(a8{}.i))) = "iface-" + strconv.FormatInt(seed, 10)
	ck(v8.w == uint64(seed)+99 && v8.p.tag == "up" && v8.s == "up-"+strconv.FormatInt(seed, 10), "unsafe field writes")
	ck(v8.i.(string) == "iface-"+strconv.FormatInt(seed, 10), "interface field write")
	paddingZero(r8, 1, 8, "a8 after unsafe writes")

	// 4) 嵌套结构体 padding。
	vc := new(ca)
	rc := rawOf(unsafe.Pointer(vc), unsafe.Sizeof(*vc))
	zeroBytes(rc, 1, 4, 17, 24)
	vc.tag = 0x33
	vc.v4 = a4{b: 1, w: 2, h: 3}
	vc.v8 = a8{b: 4, w: 5, p: &aleaf{v: 6, tag: "cp"}, s: "cs", i: &aleaf{v: 7, tag: "ci"}}
	vc.v8.w = 6 // 置零 padding 后再写字段
	paddingZero(rc, 1, 4, "ca leading")
	paddingZero(rc, 17, 24, "ca inner a8 leading")
	ck((*a4)(unsafe.Add(unsafe.Pointer(vc), unsafe.Offsetof(ca{}.v4))).w == 2, "nested a4 via offset")
	ck((*a8)(unsafe.Add(unsafe.Pointer(vc), unsafe.Offsetof(ca{}.v8))).p.tag == "cp", "nested a8 via offset")

	// 5) 数组步长等于 Sizeof，含指针/interface 的元素由元素指针保活。
	arr8 := new([4]a8)
	for i := range arr8 {
		arr8[i] = a8{
			b: byte(i + 1),
			w: uint64(i)*17 + uint64(seed%5),
			p: &aleaf{v: int64(i)*3 + seed, tag: "ap-" + strconv.Itoa(i)},
			s: "as-" + strconv.Itoa(i),
			i: &aleaf{v: int64(i)*5 + seed, tag: "ai-" + strconv.Itoa(i)},
		}
	}
	sz8 := unsafe.Sizeof(a8{})
	for i := range arr8 {
		zeroBytes(rawOf(unsafe.Pointer(&arr8[i]), sz8), 1, 8)
	}
	for k := 1; k < len(arr8); k++ {
		ck(uintptr(unsafe.Pointer(&arr8[k]))-uintptr(unsafe.Pointer(&arr8[0])) == uintptr(k)*sz8,
			"a8 array stride k="+strconv.Itoa(k))
		ck(unsafe.Pointer(unsafe.Add(unsafe.Pointer(&arr8[0]), int(uintptr(k)*sz8))) == unsafe.Pointer(&arr8[k]),
			"a8 array GEP k="+strconv.Itoa(k))
	}

	arr4 := new([5]a4)
	for i := range arr4 {
		arr4[i] = a4{b: byte(i), w: uint32(i) * 3, h: uint16(i) * 7}
	}
	sz4 := unsafe.Sizeof(a4{})
	for k := 1; k < len(arr4); k++ {
		ck(uintptr(unsafe.Pointer(&arr4[k]))-uintptr(unsafe.Pointer(&arr4[0])) == uintptr(k)*sz4,
			"a4 array stride k="+strconv.Itoa(k))
	}

	idx := int(seed) % len(arr8)
	ep := &arr8[idx]
	ev := rawOf(unsafe.Pointer(ep), sz8)
	oldW, oldP, oldS, oldI := ep.w, ep.p, ep.s, ep.i.(*aleaf)

	// 清零另一个元素，邻居和旧值仍有效。
	zidx := (idx + 1) % len(arr8)
	arr8[zidx] = a8{}
	ck(arr8[zidx].p == nil && arr8[zidx].s == "" && arr8[zidx].i == nil, "zeroed element fields")
	ck(ep.p.tag == "ap-"+strconv.Itoa(idx) && ep.i.(*aleaf).tag == "ai-"+strconv.Itoa(idx), "neighbor element intact")

	arr8 = nil
	gcStress()
	ck(ep.w == oldW && ep.p == oldP && ep.s == oldS, "element fields after GC")
	ck(ep.i.(*aleaf).tag == oldI.tag && ep.i.(*aleaf).v == oldI.v, "interface pointer after GC")
	ck(oldP.v == int64(idx)*3+seed && oldI.v == int64(idx)*5+seed, "old leaf refs after GC")
	paddingZero(ev, 1, 8, "element view after GC")
	back := (*a8)(unsafe.Pointer(unsafe.SliceData(ev)))
	ck(back.w == oldW && back.s == oldS && back.p.tag == "ap-"+strconv.Itoa(idx), "view reinterpret after GC")

	vc.v8.i = "plain-" + strconv.Itoa(int(seed))
	gcStress()
	ck(vc.v8.i.(string) == "plain-"+strconv.Itoa(int(seed)), "string interface after GC")
	ck(vc.v8.p.tag == "cp" && vc.v4.h == 3, "nested ca after GC")

	runtime.KeepAlive(ep)
	runtime.KeepAlive(ev)
	runtime.KeepAlive(v8)
	runtime.KeepAlive(vc)
	finish("l07_align_stride_classes")
}
