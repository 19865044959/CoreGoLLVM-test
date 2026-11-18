// L14: defer 中 panic。原始 panic 尚未结束时，defer 内再次 panic 必须替换 panic
// 值；外层 defer 能 recover 到新值，traceback 保留两个 defer 闭包的真实符号与
// main.go:行号。
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

type defPayload struct {
	id   int64
	name string
	next *defPayload
}

type defResult struct {
	firstOK  bool
	final    any
	stack    string
	onlyRec  any
	onlyStck string
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

// panicInDeferRun：func2 先执行，recover 原始 panic 后再次 panic(p2)；
// 随后 func1 执行并 recover 到 p2。栈中应同时出现两个 defer 闭包符号。
//
//go:noinline
func panicInDeferRun(p1, p2 *defPayload, out *defResult) {
	defer func() {
		out.final = recover()
		out.stack = string(debug.Stack())
	}()
	defer func() {
		if e := recover(); e != nil {
			q, is := e.(*defPayload)
			out.firstOK = is && q == p1
			panic(p2)
		}
	}()
	panic(p1)
}

// deferOnlyPanic：没有原始 panic，仅由 defer 里的函数 panic，由更早注册的
// defer recover；覆盖“纯 defer panic”路径。
//
//go:noinline
func deferOnlyPanic(p *defPayload, out *defResult) {
	defer func() {
		out.onlyRec = recover()
		out.onlyStck = string(debug.Stack())
	}()
	defer func() { panic(p) }()
}

func main() {
	p1 := &defPayload{id: 91, name: "first"}
	p2 := &defPayload{id: 92, name: "second"}
	p1.next = p2
	p2.next = p1
	gcStress()

	out := &defResult{}
	panicInDeferRun(p1, p2, out)

	ck(out.firstOK, "defer recovered original panic value")
	final, isPayload := out.final.(*defPayload)
	ck(isPayload && final == p2 && final.id == 92, "second panic in defer replaces panic value")
	ck(strings.Contains(out.stack, "main.panicInDeferRun.func1("), "traceback has recovering defer symbol")
	ck(strings.Contains(out.stack, "main.panicInDeferRun.func2("), "traceback has panicking defer symbol")
	ck(strings.Contains(out.stack, "main.panicInDeferRun("), "traceback has host function symbol")
	ck(strings.Contains(out.stack, "goroutine "), "traceback has goroutine header")
	ck(len(lineRe.FindAllString(out.stack, -1)) >= 2, "traceback has main.go:line for defer frames")

	// 第二个场景：只有 defer panic。
	out2 := &defResult{}
	deferOnlyPanic(p1, out2)
	only, isPayload := out2.onlyRec.(*defPayload)
	ck(isPayload && only == p1, "defer-only panic recovered")
	ck(strings.Contains(out2.onlyStck, "main.deferOnlyPanic.func2("), "defer-only traceback has panicking defer symbol")
	ck(strings.Contains(out2.onlyStck, "main.deferOnlyPanic("), "defer-only traceback has host function symbol")
	ck(lineRe.MatchString(out2.onlyStck), "defer-only traceback has main.go:line")

	gcStress()
	ck(p1.id == 91 && p1.name == "first" && p1.next == p2, "first payload after GC")
	ck(p2.id == 92 && p2.name == "second" && p2.next == p1, "second payload after GC")
	finish("l14_panic_in_defer")
}
