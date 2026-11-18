// L17: 取地址的局部/字段数组跨 GC。
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
func fill(a *[32]*node) int64 {
	for i := range a {
		a[i] = &node{v: int64(i), name: "arr"}
	}
	gcStress()
	var s int64
	for i := range a {
		if a[i] == nil || a[i].v != int64(i) {
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

func main() {
	var a [32]*node
	ck(fill(&a) == 496, "address-taken array across GC")
	gcStress()
	ck(a[0].v == 0 && a[31].v == 31, "caller array after GC")

	h := &holder{tail: "abc"}
	h.arr[7] = &node{v: 777, name: "f"}
	gcStress()
	ck(touchHolder(h) == 780, "struct field array across GC")
	ck(h.arr[7].v == 777, "field array after GC")

	for k := 0; k < 50; k++ {
		var la [8]*node
		la[0] = &node{v: int64(k)}
		la[7] = &node{v: int64(k + 100)}
		gcStress()
		ck(la[0].v == int64(k) && la[7].v == int64(k+100), "loop local array")
	}
	finish("l17_addrtaken_local_array")
}
