// P0-07 (报告风险 A6/A7): 派生指针/内部指针单独存活（含子切片）。
package main

import (
	"fmt"
	"os"
	"runtime"
	"sync/atomic"
)

type node struct{ v int64 }

type store struct {
	pad  [4]int64
	arr  [16]*node
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
func makeView() []*node {
	s := &store{tail: "view-tail"}
	for i := range s.arr {
		s.arr[i] = &node{v: int64(i)}
	}
	return s.arr[:8]
}

//go:noinline
func makeSlot() **node {
	s := &store{}
	for i := range s.arr {
		s.arr[i] = &node{v: int64(100 + i)}
	}
	return &s.arr[5]
}

//go:noinline
func makeTail() *string {
	s := &store{tail: "interior-string"}
	return &s.tail
}

func main() {
	for round := 0; round < 10; round++ {
		view := makeView()
		gcStress()
		var sum int64
		for _, p := range view {
			sum += p.v
		}
		ck(sum == 28, "slice view keeps base alive")

		slot := makeSlot()
		gcStress()
		ck((*slot).v == 105, "interior slot pointer")

		ts := makeTail()
		gcStress()
		ck(*ts == "interior-string", "interior string pointer")

		base := make([]*node, 10)
		for i := range base {
			base[i] = &node{v: int64(i)}
		}
		head := base[2:5]
		gcStress()
		ck(head[0].v == 2 && head[2].v == 4, "sub-slice after GC")

		str := string([]byte{'a', 'b', 'c', 'd', 'e', 'f', 'g', 'h'})
		sub := str[2:6]
		gcStress()
		ck(sub == "cdef", "string sub-slice after GC")
	}
	finish("p0_07_derived_interior_ptr")
}
