// L04: 深调用返回后显式 GC 触发 goroutine 栈收缩，再进入/返回，循环校验
// 跨收缩仍存活的指针与每轮递归结果。
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
func shrinkDeep(depth int, salt int64) int64 {
	// 32 个指针槽 × 600+ 层，栈扩张到约 200KB；返回后 GC 会收缩该 goroutine 的栈。
	var buf [32]*node
	local := &node{v: int64(depth)*5 + salt}
	for i := 0; i < len(buf); i++ {
		buf[i] = local
	}
	if depth == 0 {
		gcStress()
		return local.v
	}
	child := shrinkDeep(depth-1, salt)
	if child == -1<<60 {
		return child
	}
	if buf[0] != local || buf[len(buf)-1] != local || local.v != int64(depth)*5+salt {
		return -1 << 60
	}
	return child + local.v
}

func expectedShrink(depth int, salt int64) int64 {
	var s int64
	for d := 0; d <= depth; d++ {
		s += int64(d)*5 + salt
	}
	return s
}

func main() {
	seed := envSeed()
	depthBase := 600 + int(seed%300)
	anchor := &node{v: seed*11 + 5}
	keep := make([]*node, 8)
	for i := range keep {
		keep[i] = &node{v: seed*19 + int64(i)*3}
	}

	for c := 0; c < 6; c++ {
		depth := depthBase + c*37
		salt := seed*7 + int64(c)*13
		got := shrinkDeep(depth, salt)
		want := expectedShrink(depth, salt)
		ckf(got == want, "cycle=%d depth=%d got=%d want=%d", c, depth, got, want)
		// 返回后栈使用量骤降；两次 GC 给 runtime 足够机会执行 shrinkstack。
		gcStress()
		ckf(anchor.v == seed*11+5, "cycle=%d anchor pointer corrupted after stack shrink", c)
		for i := range keep {
			if keep[i] == nil || keep[i].v != seed*19+int64(i)*3 {
				ckf(false, "cycle=%d keep[%d] pointer corrupted after stack shrink", c, i)
			}
		}
	}
	// 收缩后再次进入深递归，确认重新增长路径正常。
	depth := depthBase + 211
	salt := seed*7 + 999
	ckf(shrinkDeep(depth, salt) == expectedShrink(depth, salt), "re-entry after shrink cycles")
	gcStress()
	ckf(anchor.v == seed*11+5, "anchor after final GC")
	finish("l04_stack_shrink_cycles")
}
