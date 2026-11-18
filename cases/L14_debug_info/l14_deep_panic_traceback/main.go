// L14: 深 goroutine panic 链的 traceback。debug.Stack 必须给出每一层递归帧、
// 真实函数符号名与 main.go:行号；panic payload 里的指针链在 GC 后仍有效。
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

type deepNode struct {
	id   int64
	name string
	next *deepNode
}

type deepBoom struct {
	msg  string
	node *deepNode
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
func deepLeaf(b *deepBoom) { panic(b) }

//go:noinline
func deepRecurse(depth int, b *deepBoom) {
	if depth == 0 {
		deepLeaf(b)
	}
	deepRecurse(depth-1, b)
}

//go:noinline
func deepChainEntry(depth int, b *deepBoom) { deepRecurse(depth, b) }

type chainResult struct {
	stack string
	boom  *deepBoom
	ok    bool
}

// deepChainRun 在子 goroutine 中走完深链后 panic，并在 defer 中 recover 抓栈。
func deepChainRun(depth int, b *deepBoom, out chan<- chainResult) {
	defer func() {
		if e := recover(); e != nil {
			r := chainResult{boom: b}
			if q, isBoom := e.(*deepBoom); isBoom && q != nil && q.node != nil && q.node.id == 48 {
				r.ok = true
			}
			r.stack = string(debug.Stack())
			out <- r
		}
	}()
	deepChainEntry(depth, b)
}

func main() {
	const depth = 48
	head := &deepNode{id: 1, name: "deep"}
	for i := int64(2); i <= 48; i++ {
		head = &deepNode{id: i, name: "deep", next: head}
	}
	b := &deepBoom{msg: "deep-boom", node: head}
	gcStress()

	out := make(chan chainResult, 1)
	go deepChainRun(depth, b, out)
	r := <-out

	ck(r.ok, "recovered deepBoom with valid node")
	ck(r.boom == b && b.msg == "deep-boom", "panic payload identity")
	ck(strings.Contains(r.stack, "main.deepLeaf("), "stack has deepLeaf symbol")
	ck(strings.Contains(r.stack, "main.deepRecurse("), "stack has deepRecurse symbol")
	ck(strings.Contains(r.stack, "main.deepChainEntry("), "stack has deepChainEntry symbol")
	ck(strings.Contains(r.stack, "main.deepChainRun("), "stack has goroutine entry symbol")
	ck(strings.Count(r.stack, "main.deepRecurse(") >= 40, "stack keeps deep recursion frames")
	ck(strings.Contains(r.stack, "goroutine "), "stack has goroutine header")
	ck(lineRe.MatchString(r.stack), "stack has main.go:line")
	ck(len(lineRe.FindAllString(r.stack, -1)) >= 40, "stack has per-frame line info")

	gcStress()
	ck(b.node != nil && b.node.id == 48 && b.node.next != nil && b.node.next.id == 47, "payload pointer chain after GC")
	finish("l14_deep_panic_traceback")
}
