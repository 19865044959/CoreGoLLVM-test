// L14: debug.Stack / runtime.Stack 在大缓冲下的行为：全量 goroutine dump 不截断、
// 深调用 debug.Stack 保留全部帧与行号。TORTURE_ROUNDS 放大 dump 轮数。
package main

import (
	"fmt"
	"os"
	"regexp"
	"runtime"
	"runtime/debug"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
)

type dumpNode struct {
	v    int64
	name string
}

var (
	failed     int32
	gcSink     [][]byte
	chanRecvRe = regexp.MustCompile(`goroutine \d+ \[chan receive\]:`)
	lineRe     = regexp.MustCompile(`main\.go:\d+`)
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

func tortureRounds(def int) int {
	s := os.Getenv("TORTURE_ROUNDS")
	if s == "" {
		return def
	}
	n, err := strconv.Atoi(s)
	if err != nil || n <= 0 {
		return def
	}
	if n > 16 {
		n = 16
	}
	return n
}

//go:noinline
func dumpBlocker(ch <-chan struct{}, ready chan<- struct{}, p *dumpNode, errs *int32) {
	ready <- struct{}{}
	<-ch
	if p == nil || p.v != 7 || p.name != "blocked-in-large-dump" {
		atomic.AddInt32(errs, 1)
	}
}

//go:noinline
func stackRec(depth int) string {
	if depth == 0 {
		return string(debug.Stack())
	}
	return stackRec(depth - 1)
}

//go:noinline
func stackProbe(depth int) string { return stackRec(depth) }

func main() {
	rounds := tortureRounds(1)
	const workers = 64
	const depth = 32

	buf := make([]byte, 2<<20)
	for r := 0; r < rounds; r++ {
		ch := make(chan struct{})
		ready := make(chan struct{}, workers)
		var wg sync.WaitGroup
		errs := int32(0)
		for i := 0; i < workers; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				dumpBlocker(ch, ready, &dumpNode{v: 7, name: "blocked-in-large-dump"}, &errs)
			}(i)
		}
		for i := 0; i < workers; i++ {
			<-ready
		}
		gcStress()

		n := runtime.Stack(buf, true)
		ck(n < len(buf), "large stack dump not truncated")
		dump := string(buf[:n])
		ck(strings.Contains(dump, "main.dumpBlocker("), "large dump has blocker symbol")
		ck(strings.Count(dump, "main.dumpBlocker(") >= workers, "large dump has all blocked goroutines")
		ck(len(chanRecvRe.FindAllString(dump, -1)) >= workers, "all blocked goroutines report chan receive")
		ck(lineRe.MatchString(dump), "large dump has main.go:line")

		close(ch)
		wg.Wait()
		ck(atomic.LoadInt32(&errs) == 0, "blocked pointers after GC")
	}

	stack := stackProbe(depth)
	ck(len(stack) > 1024, "debug.Stack grows beyond initial small buffer")
	ck(strings.Contains(stack, "main.stackProbe("), "deep debug.Stack has probe symbol")
	ck(strings.Contains(stack, "main.stackRec("), "deep debug.Stack has recursion symbol")
	ck(strings.Count(stack, "main.stackRec(") >= depth, "deep debug.Stack keeps all recursion frames")
	ck(len(lineRe.FindAllString(stack, -1)) >= depth, "deep debug.Stack keeps per-frame line info")
	finish("l14_debug_stack_large_buf")
}
