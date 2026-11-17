// P1-21 (报告风险 D18/D19 + 零大小/大分配): 分配形状。
package main

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"sync/atomic"
)

type node struct{ v int64 }

type zs struct{}

type kv struct {
	k, v string
	p    *node
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

func main() {
	// 零大小对象/容器。
	zp := new(zs)
	var za [0]*node
	zs0 := make([]zs, 100)
	em := map[int]struct{}{}
	for i := 0; i < 100; i++ {
		em[i] = struct{}{}
	}
	var es interface{} = zp
	gcStress()
	ck(zp != nil && es != nil, "zero-size alloc")
	ck(len(za) == 0 && len(zs0) == 100 && len(em) == 100, "zero-size containers")

	// 大指针切片。
	const n = 50000
	big := make([]*node, n)
	for i := range big {
		big[i] = &node{v: int64(i)}
	}
	gcStress()
	ck(big[n-1].v == n-1 && big[12345].v == 12345, "large makeslice")

	// append 扩容。
	var grow []*node
	for i := 0; i < 5000; i++ {
		grow = append(grow, &node{v: int64(i)})
	}
	gcStress()
	var sum int64
	for _, p := range grow {
		sum += p.v
	}
	ck(sum == int64(4999*5000/2), "append growth")

	// append 超过预分配容量。
	capped := make([]*node, 0, 16)
	for i := 0; i < 100; i++ {
		capped = append(capped, &node{v: int64(i)})
	}
	gcStress()
	ck(len(capped) == 100 && capped[99].v == 99, "append beyond cap")

	// copy 指针切片。
	dst := make([]*node, n)
	copy(dst, big)
	gcStress()
	ck(dst[n-1].v == n-1, "copy pointer slice")

	// 堆上的大结构数组（含 string/ptr）。
	arr := make([]kv, 2000)
	for i := range arr {
		arr[i] = kv{k: "k" + strconv.Itoa(i), v: "v", p: &node{v: int64(i)}}
	}
	gcStress()
	ck(arr[1999].p.v == 1999 && arr[1000].k == "k1000", "large heap struct array")
	finish("p1_21_alloc_shapes")
}
