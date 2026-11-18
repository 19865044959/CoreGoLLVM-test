// L14: 嵌套闭包符号。工厂返回外层闭包，外层闭包内再创建内层闭包（含嵌套 IIFE），
// 运行期符号必须逐层真实：main.nestedPanicFactory.func1 与 .func1.1；
// 同一工厂的闭包共享外层符号，嵌套内层的 funcval 也可反查真实符号。
// 内层闭包 panic 的 traceback 必须同时包含两层闭包符号与 main.go:行号。
package main

import (
	"fmt"
	"os"
	"reflect"
	"regexp"
	"runtime"
	"runtime/debug"
	"strings"
	"sync/atomic"
)

type ncPayload struct {
	id   int64
	name string
	next *ncPayload
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

// nestedPanicFactory 外层闭包内再用 IIFE 内层闭包触发 panic。
// 预期符号：外层 main.nestedPanicFactory.func1，内层 main.nestedPanicFactory.func1.1。
//
//go:noinline
func nestedPanicFactory(p *ncPayload) func() {
	return func() {
		func() { panic(p) }()
	}
}

// nestedAdderFactory 返回“外层闭包 -> 内层计数器闭包”，用于验证两级 funcval 符号。
//
//go:noinline
func nestedAdderFactory(base int64) func() func(int64) int64 {
	return func() func(int64) int64 {
		sum := base
		return func(v int64) int64 {
			sum += v
			return sum
		}
	}
}

//go:noinline
func nestedRun(f func()) (stack string, ok bool) {
	defer func() {
		if e := recover(); e != nil {
			q, is := e.(*ncPayload)
			ok = is && q != nil
			stack = string(debug.Stack())
		}
	}()
	f()
	return "", false
}

func ncName(f any) string {
	return runtime.FuncForPC(reflect.ValueOf(f).Pointer()).Name()
}

func main() {
	p := &ncPayload{id: 111, name: "nested"}
	p.next = &ncPayload{id: 112, name: "next"}
	gcStress()

	// 1) 两级嵌套闭包的 funcval 符号。
	outA := nestedAdderFactory(10)
	outB := nestedAdderFactory(100)
	ck(ncName(outA) == "main.nestedAdderFactory.func1", "outer closure resolves to .func1")
	ck(ncName(outB) == ncName(outA), "outer closures from same factory share symbol")
	inA := outA()
	inB := outB()
	ck(ncName(inA) == "main.nestedAdderFactory.func1.1", "inner closure resolves to .func1.1")
	ck(ncName(inB) == ncName(inA), "inner closures from same factory share symbol")
	ck(inA(1) == 11 && inA(2) == 13, "first nested counter keeps own captured state")
	ck(inB(1) == 101 && inB(2) == 103, "second nested counter keeps own captured state")

	// 2) 内层 IIFE 闭包 panic：两层闭包符号都出现在 traceback。
	panicOuter := nestedPanicFactory(p)
	ck(ncName(panicOuter) == "main.nestedPanicFactory.func1", "panic outer closure symbol")
	stack, ok := nestedRun(panicOuter)
	ck(ok, "nested panic payload identity")
	ck(strings.Contains(stack, "main.nestedPanicFactory.func1.1("), "traceback has inner closure symbol")
	ck(strings.Contains(stack, "main.nestedPanicFactory.func1("), "traceback has outer closure symbol")
	ck(strings.Contains(stack, "main.nestedRun("), "traceback has recovering function symbol")
	ck(strings.Contains(stack, "goroutine "), "traceback has goroutine header")
	ck(lineRe.MatchString(stack), "traceback has main.go:line for nested closure frame")

	gcStress()
	ck(p.id == 111 && p.name == "nested", "captured payload after GC")
	ck(p.next != nil && p.next.id == 112 && p.next.name == "next", "captured payload chain after GC")
	ck(inA(0) == 13 && inB(0) == 103, "nested counter state after GC")
	finish("l14_nested_closure_symbols")
}
