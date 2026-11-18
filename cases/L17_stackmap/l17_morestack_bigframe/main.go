// L17: 深递归/大栈帧触发 morestack 栈复制，指针必须被正确重定位。
package main

import (
	"fmt"
	"os"
	"runtime"
	"sync/atomic"
)

type node struct{ v int64 }

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
func deep(depth int, p *node) int64 {
	var buf [64]*node
	buf[0] = &node{v: int64(depth)}
	buf[63] = &node{v: int64(depth) + 1000000}
	local := &node{v: int64(depth) * 3}
	if depth == 0 {
		gcStress()
		return buf[0].v + buf[63].v + local.v + p.v
	}
	r := deep(depth-1, local)
	if depth%511 == 0 {
		gcStress()
	}
	if buf[0].v != int64(depth) || buf[63].v != int64(depth)+1000000 || local.v != int64(depth)*3 {
		return -1 << 60
	}
	return r + buf[0].v + local.v
}

//go:noinline
func bigFrame(seed *node) int64 {
	var big [8192]*node // 64KB 帧
	for i := 0; i < len(big); i += 64 {
		big[i] = &node{v: int64(i) + seed.v}
	}
	gcStress()
	var s int64
	for i := 0; i < len(big); i += 64 {
		if big[i] == nil {
			return -1
		}
		s += big[i].v
	}
	return s
}

func expectedDeep(max int) int64 {
	exp := int64(1000003) // depth 0: 0 + 1000000 + 0 + 3
	for d := 1; d <= max; d++ {
		exp += 4 * int64(d)
	}
	return exp
}

func expectedBig(seed int64) int64 {
	var s int64
	for i := 0; i < 8192; i += 64 {
		s += int64(i) + seed
	}
	return s
}

func main() {
	const depth = 2000
	for round := 0; round < 2; round++ {
		ck(deep(depth, &node{v: 5}) == expectedDeep(depth), "deep recursion pointers after stack growth")
	}
	ck(bigFrame(&node{v: 7}) == expectedBig(7), "64KB stack frame with GC")
	finish("l17_morestack_bigframe")
}
