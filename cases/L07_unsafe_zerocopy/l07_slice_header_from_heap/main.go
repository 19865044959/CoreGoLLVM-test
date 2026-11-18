// L07: 从堆数组用 unsafe.Slice 构造 slice 并 append，GC 后内容与容量语义正确。
package main

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"sync/atomic"
	"unsafe"
)

type node struct {
	v    int64
	name string
}

type rec struct {
	id   int64
	tag  string
	link *node
	data []byte
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

//go:noinline
func makeArr() *[32]rec {
	a := new([32]rec)
	for i := range a {
		a[i] = rec{
			id:   int64(i),
			tag:  "base-" + strconv.Itoa(i),
			link: &node{v: int64(i * 10), name: "bn-" + strconv.Itoa(i)},
			data: []byte("bd-" + strconv.Itoa(i)),
		}
	}
	return a
}

func checkRec(r rec, id int64, tag string, linkV int64, linkName, data, stage string) {
	ck(r.id == id && r.tag == tag, stage+": id/tag")
	if r.link == nil {
		ck(false, stage+": nil link")
	} else {
		ck(r.link.v == linkV && r.link.name == linkName, stage+": link")
	}
	ck(string(r.data) == data, stage+": data")
}

func main() {
	if s := os.Getenv("UAF_SEED"); s != "" {
		if v, err := strconv.ParseInt(s, 10, 64); err == nil {
			seed = v
		}
	}

	arr := makeArr()
	view := unsafe.Slice(&arr[0], len(arr))
	early := unsafe.Slice(&arr[0], 4)
	ck(len(view) == 32 && cap(view) == 32, "full view shape")
	ck(len(early) == 4 && cap(early) == 4, "early view shape")

	arr = nil // slice 头是唯一引用
	gcStress()
	for i := 0; i < 32; i++ {
		checkRec(view[i], int64(i), "base-"+strconv.Itoa(i), int64(i*10),
			"bn-"+strconv.Itoa(i), "bd-"+strconv.Itoa(i), "base view")
	}

	// 在 cap 内 append：直接写入底层堆数组，完整视图可见。
	v2 := view[:8]
	v2 = append(v2, rec{id: 77, tag: "appended", link: &node{v: 700, name: "an"}, data: []byte("ad")})
	ck(len(v2) == 9, "append within cap length")
	ck(cap(v2) == 32, "append within cap reuses heap array")
	ck(view[8].id == 77 && view[8].tag == "appended", "in-place append visible through full view")

	// cap 用尽后 append 必须重新分配，源数组不得被改写。
	grown := append(early, rec{id: 90, tag: "grown", link: &node{v: 900, name: "gn"}, data: []byte("gd")})
	ck(len(grown) == 5, "reallocating append length")
	ck(view[4].id == 4 && view[4].tag == "base-4", "reallocating append leaves source untouched")

	// 三索引切片把 cap 钉死，append 同样重新分配。
	tight := view[10:12:12]
	t2 := append(tight, rec{id: 88, tag: "tight", link: &node{v: 880, name: "tn"}, data: []byte("td")})
	ck(len(t2) == 3, "three-index append length")
	ck(view[12].id == 12 && view[12].tag == "base-12", "three-index cap append reallocates")

	extra := 4 + int(seed%5)
	for i := 0; i < extra; i++ {
		v2 = append(v2, rec{
			id:   int64(200 + i),
			tag:  "extra-" + strconv.Itoa(i),
			link: &node{v: int64(i), name: "xn-" + strconv.Itoa(i)},
			data: []byte("xd-" + strconv.Itoa(i)),
		})
	}
	ck(len(v2) == 9+extra, "extra append length")

	gcStress()
	for i := 0; i < extra; i++ {
		checkRec(view[9+i], int64(200+i), "extra-"+strconv.Itoa(i), int64(i),
			"xn-"+strconv.Itoa(i), "xd-"+strconv.Itoa(i), "extra view after GC")
	}
	checkRec(grown[4], 90, "grown", 900, "gn", "gd", "grown after GC")
	checkRec(t2[2], 88, "tight", 880, "tn", "td", "tight after GC")

	// 从 slice 基址做单表达式指针算术，定位第 8 个元素。
	p8 := (*rec)(unsafe.Add(unsafe.Pointer(&view[0]), 8*unsafe.Sizeof(view[0])))
	ck(p8 == &view[8], "pointer arithmetic from slice base")
	ck(p8.id == 77 && p8.tag == "appended", "pointer arithmetic target after GC")

	runtime.KeepAlive(view)
	runtime.KeepAlive(v2)
	runtime.KeepAlive(grown)
	runtime.KeepAlive(t2)
	finish("l07_slice_header_from_heap")
}
