// P0-03 (报告风险 A3): 取地址的局部/字段数组跨 GC。
package main

import (
	"fmt"
	"os"
	"runtime"
	"sync/atomic"
)

type node struct {
	v    int64
	name string
}

type holder struct {
	n    int64
	arr  [32]*node
	tail string
}

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

//go:noinline
func fill32(a *[32]*node) int64 {
	for i := range a {
		a[i] = &node{v: int64(i)}
	}
	gcStress()
	var s int64
	for i := range a {
		if a[i] == nil {
			return -1
		}
		s += a[i].v
	}
	return s
}

//go:noinline
func touchHolder(h *holder) int64 {
	gcStress()
	return h.arr[7].v + int64(len(h.tail))
}

//go:noinline
func fillStrings(a *[16]string) int {
	for i := range a {
		a[i] = "str-" + string(rune('a'+i))
	}
	gcStress()
	n := 0
	for _, s := range a {
		n += len(s)
	}
	return n
}

func main() {
	var a [32]*node
	ck(fill32(&a) == 496, "fill32 sum")
	gcStress()
	ck(a[0].v == 0 && a[31].v == 31, "array after GC")

	h := &holder{tail: "abc"}
	h.arr[7] = &node{v: 777}
	ck(touchHolder(h) == 780, "holder field across GC")
	gcStress()
	ck(h.arr[7].v == 777, "holder after GC")

	for k := 0; k < 50; k++ {
		var la [8]*node
		la[0] = &node{v: int64(k)}
		la[7] = &node{v: int64(k + 100)}
		gcStress()
		ck(la[0].v == int64(k), "loop local array [0]")
		ck(la[7].v == int64(k+100), "loop local array [7]")
	}

	var sa [16]string
	ck(fillStrings(&sa) == 80, "string array sum")
	gcStress()
	ck(sa[0] == "str-a" && sa[15] == "str-p", "string array after GC")
	finish("p0_03_addrtaken_array")
}
