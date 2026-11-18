// L07: 手工 slice header 的 len/cap 操作：三索引、cap 内追加、header 复制与 GC 保活。
package main

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"sync/atomic"
	"unsafe"
)

type lnode struct {
	v   int64
	tag string
}

type slot struct {
	id   int64
	tag  string
	link *lnode
}

type sliceHeader struct {
	data unsafe.Pointer
	len  int
	cap  int
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

func newSlot(i int) slot {
	return slot{id: int64(i), tag: "s-" + strconv.Itoa(i), link: &lnode{v: int64(i * 3), tag: "l-" + strconv.Itoa(i)}}
}

func checkSlot(s slot, i int, stage string) {
	ck(s.id == int64(i) && s.tag == "s-"+strconv.Itoa(i), stage+": id/tag i="+strconv.Itoa(i))
	if s.link == nil {
		ck(false, stage+": nil link i="+strconv.Itoa(i))
		return
	}
	ck(s.link.v == int64(i*3) && s.link.tag == "l-"+strconv.Itoa(i), stage+": link i="+strconv.Itoa(i))
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

	const total = 40
	arr := new([total]slot)
	for i := range arr {
		arr[i] = newSlot(i)
	}
	l0 := 10 + int(seed%4)     // 10..13
	c0 := l0 + 6 + int(seed%4) // 16..20
	if c0 > total {
		c0 = total
	}
	stride := int(unsafe.Sizeof(slot{}))

	// 1) 手工 header 构造切片：len=cap 语义与元素地址精确。
	h := sliceHeader{data: unsafe.Pointer(&arr[0]), len: l0, cap: c0}
	view := *(*[]slot)(unsafe.Pointer(&h))
	ck(len(view) == l0 && cap(view) == c0, "manual header len/cap")
	ck(unsafe.Pointer(&view[0]) == unsafe.Pointer(&arr[0]), "header data pointer")
	k := int(seed) % l0
	ck(unsafe.Pointer(&view[k]) == unsafe.Pointer(&arr[k]), "element address k")
	ck(unsafe.Pointer(&view[k]) == unsafe.Add(unsafe.Pointer(&arr[0]), k*stride), "element GEP")
	checkSlot(view[k], k, "manual header")

	// header 回读与 unsafe.Slice 等价。
	gh := *(*sliceHeader)(unsafe.Pointer(&view))
	ck(gh.data == unsafe.Pointer(&arr[0]) && gh.len == l0 && gh.cap == c0, "header read back")
	us := unsafe.Slice((*slot)(unsafe.Pointer(&arr[0])), c0)
	ck(len(us) == c0 && cap(us) == c0, "unsafe.Slice shape")
	ck(unsafe.Pointer(&us[0]) == unsafe.Pointer(&view[0]), "unsafe.Slice same base")

	// 2) 丢弃数组变量，仅由 header 构造的切片保活。
	arr = nil
	gcStress()
	for i := 0; i < l0; i++ {
		checkSlot(view[i], i, "view after GC")
	}

	// 3) 通过视图重建整块底层数组（长度 total），用于检查未覆盖区域。
	raw := unsafe.Slice((*slot)(unsafe.Pointer(&view[0])), total)
	ck(len(raw) == total, "raw view length")

	// 4) len 在 cap 内扩展、收缩，数据指针保持不变。
	base := unsafe.Pointer(unsafe.SliceData(view))
	view = view[:c0]
	ck(len(view) == c0 && cap(view) == c0, "extend len to cap")
	checkSlot(view[c0-1], c0-1, "extended view")
	view = view[:l0]
	ck(unsafe.Pointer(unsafe.SliceData(view)) == base, "data pointer stable after reslice")
	ck(len(view) == l0 && cap(view) == c0, "shrink len")

	// 5) 三索引切片钉死 cap；cap 内 append 原地写底层数组。
	tight := view[l0-2 : l0 : l0+2]
	ck(len(tight) == 2 && cap(tight) == 4, "three-index shape")
	ap1 := slot{id: 900, tag: "ap1", link: &lnode{v: 9000, tag: "ap1n"}}
	ap2 := slot{id: 901, tag: "ap2", link: &lnode{v: 9001, tag: "ap2n"}}
	tight = append(tight, ap1)
	ck(len(tight) == 3, "append within cap len")
	ck(raw[l0].id == 900 && raw[l0].tag == "ap1", "append wrote backing at l0")
	tight = append(tight, ap2)
	ck(raw[l0+1].id == 901 && raw[l0+1].tag == "ap2", "append wrote backing at l0+1")
	// 第三次 append 超出 cap，必须重新分配，底层数组 l0+2 不得被改写。
	orig := raw[l0+2]
	tight = append(tight, slot{id: 902, tag: "ap3", link: &lnode{v: 9002, tag: "ap3n"}})
	ck(len(tight) == 5, "reallocating append len")
	ck(raw[l0+2].id == orig.id && raw[l0+2].tag == orig.tag, "reallocating append leaves source")

	// 6) 满 cap 追加必须重新分配；底层数组 cap 之后位置不动。
	full := view[:c0]
	before := raw[c0]
	grown := append(full, slot{id: 700, tag: "grow", link: &lnode{v: 7000, tag: "grown"}})
	ck(len(grown) == c0+1, "grown len")
	ck(raw[c0].id == before.id && raw[c0].tag == before.tag, "grown append leaves source")
	ck(grown[c0].id == 700 && grown[c0].tag == "grow" &&
		grown[c0].link != nil && grown[c0].link.v == 7000 && grown[c0].link.tag == "grown",
		"grown element")

	// 7) header 复制后仍是同一底层数组，且跨 GC 独立有效。
	h2 := *(*sliceHeader)(unsafe.Pointer(&tight))
	tv := *(*[]slot)(unsafe.Pointer(&h2))
	ck(len(tv) == len(tight) && cap(tv) == cap(tight), "copied header shape")
	ck(unsafe.Pointer(&tv[0]) == unsafe.Pointer(&tight[0]), "copied header same data")
	checkSlot(tv[0], l0-2, "copied header first")

	// 8) 尾部零长度视图：len/cap 计算正确，append 落在剩余 cap 内并原地写入。
	tail := view[len(view):]
	ck(len(tail) == 0 && cap(tail) == c0-l0, "tail view shape")
	tail2 := append(tail, slot{id: 444, tag: "tail", link: &lnode{v: 4440, tag: "tn"}})
	ck(len(tail2) == 1, "tail append len")
	ck(tail2[0].id == 444 && tail2[0].tag == "tail" &&
		tail2[0].link != nil && tail2[0].link.v == 4440 && tail2[0].link.tag == "tn",
		"tail append element")
	ck(raw[l0].id == 444, "tail append used remaining cap")

	// 9) 只保留 header 派生切片与复制 header，GC 后全部有效。
	view = nil
	full = nil
	raw = nil
	gcStress()
	ck(len(tv) == 5 && cap(tv) == cap(tight), "tv len/cap after GC")
	checkSlot(tv[0], l0-2, "tv[0] after GC")
	checkSlot(tv[1], l0-1, "tv[1] after GC")
	ck(tv[2].id == 900 && tv[2].tag == "ap1" && tv[2].link != nil && tv[2].link.tag == "ap1n",
		"tv[2] appended after GC")
	ck(tv[3].id == 901 && tv[3].tag == "ap2" && tv[3].link != nil && tv[3].link.tag == "ap2n",
		"tv[3] appended after GC")
	ck(tv[4].id == 902 && tv[4].tag == "ap3" && tv[4].link != nil && tv[4].link.tag == "ap3n",
		"tv[4] reallocated append after GC")
	for i := 0; i < l0; i++ {
		checkSlot(us[i], i, "us before append region after GC")
	}
	ck(us[l0].id == 444 && us[l0].tag == "tail", "us sees tail append at l0")
	ck(us[l0+1].id == 901 && us[l0+1].tag == "ap2", "us sees in-place append at l0+1")
	for i := l0 + 2; i < c0; i++ {
		checkSlot(us[i], i, "us after append region after GC")
	}

	runtime.KeepAlive(tv)
	runtime.KeepAlive(us)
	runtime.KeepAlive(tail2)
	runtime.KeepAlive(grown)
	runtime.KeepAlive(tight)
	finish("l07_slice_header_lencap_ops")
}
