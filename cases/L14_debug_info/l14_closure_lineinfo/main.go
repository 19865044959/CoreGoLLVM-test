// L14: 闭包行号。三个闭包在源码中位于相邻但不同的行，运行期通过 runtime.Caller
// 取回的文件/行号必须递增且指向 main.go；panic 闭包的 debug.Stack 也带真实闭包符号与行号。
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

type linePayload struct {
	v    int64
	name string
}

type closureLine struct {
	file string
	line int
	fn   string
	v    int64
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

// 三个闭包各占一行：报告行号必须严格递增（只比较相对顺序，不依赖具体行号值）。
// 通过捕获 p 验证闭包环境中的指针在 GC 后仍然有效。
//
//go:noinline
func makeLineClosures(p *linePayload) (func() closureLine, func() closureLine, func() closureLine) {
	a := func() closureLine {
		pc, f, l, _ := runtime.Caller(0)
		return closureLine{file: f, line: l, fn: runtime.FuncForPC(pc).Name(), v: p.v}
	}
	b := func() closureLine {
		pc, f, l, _ := runtime.Caller(0)
		return closureLine{file: f, line: l, fn: runtime.FuncForPC(pc).Name(), v: p.v + 1}
	}
	c := func() closureLine {
		pc, f, l, _ := runtime.Caller(0)
		return closureLine{file: f, line: l, fn: runtime.FuncForPC(pc).Name(), v: p.v + 2}
	}
	return a, b, c
}

//go:noinline
func panicClosureFactory(p *linePayload) func() {
	return func() { panic(p) }
}

//go:noinline
func runClosureRecover(f func(), p *linePayload) (stack string, ok bool) {
	defer func() {
		if e := recover(); e != nil {
			stack = string(debug.Stack())
			ok = p != nil && p.v == 7 && p.name == "closure-lines"
		}
	}()
	f()
	return "", false
}

func checkClosureLine(cl closureLine, fn string) {
	ck(strings.HasSuffix(cl.file, "main.go"), fn+" reports main.go")
	ck(cl.line > 0, fn+" reports positive line")
	ck(cl.fn == fn, fn+" reports real closure symbol")
}

func main() {
	p := &linePayload{v: 7, name: "closure-lines"}
	a, b, c := makeLineClosures(p)
	gcStress()

	ca, cb, cc := a(), b(), c()
	checkClosureLine(ca, "main.makeLineClosures.func1")
	checkClosureLine(cb, "main.makeLineClosures.func2")
	checkClosureLine(cc, "main.makeLineClosures.func3")
	ck(ca.line < cb.line && cb.line < cc.line, "closure line numbers increase with source order")
	ck(ca.v == 7 && cb.v == 8 && cc.v == 9, "closure captured pointers valid after GC")

	stack, ok := runClosureRecover(panicClosureFactory(p), p)
	ck(ok, "closure panic payload valid")
	ck(strings.Contains(stack, "main.panicClosureFactory.func1"), "stack has closure symbol")
	ck(strings.Contains(stack, "main.runClosureRecover("), "stack has recovering function symbol")
	ck(lineRe.MatchString(stack), "stack has main.go:line for closure frame")

	gcStress()
	ck(p.v == 7 && p.name == "closure-lines", "captured payload after another GC")
	finish("l14_closure_lineinfo")
}
