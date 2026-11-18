// L14: 显式 GC 之后的 panic 栈仍必须包含真实函数符号、main.go:行号；
// panic 携带的指针链在后续 GC 与 clobberfree 下保持有效。
package main

import (
	"fmt"
	"os"
	"regexp"
	"runtime"
	"runtime/debug"
	"strings"
	"sync/atomic"
)

type gcNode struct {
	v    int64
	name string
	next *gcNode
}

type gcBoom struct {
	msg  string
	node *gcNode
}

var (
	failed int32
	gcSink [][]byte
	lineRe = regexp.MustCompile(`main\.go:\d+`)
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
func gcPanicLeaf(n *gcNode) { panic(gcBoom{msg: "after-gc", node: n}) }

//go:noinline
func gcPanicMiddle(n *gcNode) { gcPanicLeaf(n) }

//go:noinline
func gcPanicOuter(n *gcNode) (stack string, ok bool) {
	defer func() {
		if e := recover(); e != nil {
			if b, is := e.(gcBoom); is {
				ok = b.msg == "after-gc" && b.node != nil &&
					b.node.v == 1 && b.node.next != nil && b.node.next.v == 2
			}
			stack = string(debug.Stack())
		}
	}()
	gcPanicMiddle(n)
	return "", false
}

func main() {
	n1 := &gcNode{v: 1, name: "one"}
	n2 := &gcNode{v: 2, name: "two", next: n1}
	n1.next = n2

	// 关键点：先触发 GC 与垃圾分配，再走 panic 栈。
	gcStress()
	for i := 0; i < 32; i++ {
		garbage := make([]*gcNode, 64)
		for j := range garbage {
			garbage[j] = &gcNode{v: int64(j), name: "garbage"}
		}
		gcSink = append(gcSink, make([]byte, 2048))
		_ = garbage
	}
	runtime.GC()

	stack, ok := gcPanicOuter(n1)
	ck(ok, "panic payload valid after GC")
	ck(strings.Contains(stack, "main.gcPanicLeaf("), "stack has gcPanicLeaf symbol")
	ck(strings.Contains(stack, "main.gcPanicMiddle("), "stack has gcPanicMiddle symbol")
	ck(strings.Contains(stack, "main.gcPanicOuter("), "stack has gcPanicOuter symbol")
	ck(strings.Contains(stack, "goroutine "), "stack has goroutine header")
	ck(lineRe.MatchString(stack), "stack has main.go:line")
	ck(len(lineRe.FindAllString(stack, -1)) >= 3, "stack has line info for every frame")

	gcStress()
	ck(n1.v == 1 && n1.next == n2 && n1.next.v == 2, "panic payload chain after another GC")
	ck(n2.name == "two" && n2.next.name == "one", "panic payload names after another GC")
	finish("l14_gc_panic_stack")
}
