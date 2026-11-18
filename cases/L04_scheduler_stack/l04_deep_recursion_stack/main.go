// L04: 深递归逐层放大栈帧，触发多次 morestack 栈复制；递归途中与叶子处强制 GC，
// 回溯时逐层校验栈上指针仍指向本帧对象。
package main

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
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

func ckf(ok bool, format string, args ...any) {
	if !ok {
		atomic.AddInt32(&failed, 1)
		fmt.Printf("FAIL: "+format+"\n", args...)
	}
}

func gcStress() {
	for i := 0; i < 8; i++ {
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

func envSeed() int64 {
	s := os.Getenv("UAF_SEED")
	if s == "" {
		return 1
	}
	v, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 1
	}
	return v
}

//go:noinline
func descend(depth int, salt int64) int64 {
	// 每帧 48 个指针（384B）加上局部对象；层级加深迫使 morestack 复制整段栈。
	var buf [48]*node
	local := &node{v: int64(depth)*3 + 1 + salt}
	top := &node{v: int64(depth)}
	hi := &node{v: int64(depth) + 7}
	for i := 0; i < len(buf); i++ {
		buf[i] = local
	}
	buf[0] = top
	buf[len(buf)-1] = hi

	if depth == 0 {
		gcStress()
		return top.v + hi.v + local.v
	}
	if depth%257 == 0 {
		gcStress()
	}
	child := descend(depth-1, salt)
	if child == -1<<60 {
		return child
	}
	// 栈增长/复制以及 GC 之后，本帧的所有指针槽必须还原为原对象。
	if buf[0] != top || buf[len(buf)-1] != hi || buf[1] != local || buf[len(buf)-2] != local {
		return -1 << 60
	}
	if top.v != int64(depth) || hi.v != int64(depth)+7 || local.v != int64(depth)*3+1+salt {
		return -1 << 60
	}
	return child + top.v + local.v
}

func expectedDeep(depth int, salt int64) int64 {
	exp := int64(8) + salt // 叶子帧: depth=0
	for d := 1; d <= depth; d++ {
		exp += int64(d) + int64(d)*3 + 1 + salt // top + local
	}
	return exp
}

func run(depth int, salt int64) {
	got := descend(depth, salt)
	want := expectedDeep(depth, salt)
	ckf(got == want, "depth=%d salt=%d deep recursion corrupted: got=%d want=%d", depth, salt, got, want)
}

func main() {
	seed := envSeed()
	salt := seed*7 + 3
	depth := 1800 + int(seed%600) // 1800..2399，约 0.8~1.1MB 栈
	run(depth, salt)
	run(depth+137, salt+29)
	gcStress()
	ckf(depth > 0, "invalid depth")
	finish("l04_deep_recursion_stack")
}
