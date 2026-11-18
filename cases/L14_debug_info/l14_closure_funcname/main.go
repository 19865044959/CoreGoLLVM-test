// L14: 闭包函数名。运行时必须能把闭包 funcval 解析为真实符号
// （main.makeAdder.func1 / main.makePanicClosure.func1），同一工厂的闭包共享符号，
// panic traceback 中的闭包帧也带真实符号与 main.go:行号。
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

type closurePayload struct {
	id   int64
	name string
	next *closurePayload
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
func makeAdder(delta int64) func(int64) int64 {
	return func(v int64) int64 { return v + delta }
}

//go:noinline
func makePanicClosure(p *closurePayload) func() {
	return func() { panic(p) }
}

//go:noinline
func callAdder(f func(int64) int64, v int64) int64 { return f(v) }

//go:noinline
func callPanicClosure(f func()) (stack string, ok bool) {
	defer func() {
		if e := recover(); e != nil {
			q, is := e.(*closurePayload)
			ok = is && q != nil && q.id == 41 && q.next != nil && q.next.id == 42
			stack = string(debug.Stack())
		}
	}()
	f()
	return "", false
}

func closureName(f any) string {
	return runtime.FuncForPC(reflect.ValueOf(f).Pointer()).Name()
}

func main() {
	p := &closurePayload{id: 41, name: "closure-name"}
	p.next = &closurePayload{id: 42, name: "next"}

	add3 := makeAdder(3)
	add9 := makeAdder(9)
	gcStress()

	ck(callAdder(add3, 4) == 7, "closure captures first delta")
	ck(callAdder(add9, 4) == 13, "closure captures second delta")

	nameAdd3 := closureName(add3)
	nameAdd9 := closureName(add9)
	ck(nameAdd3 == "main.makeAdder.func1", "closure funcval resolves to real symbol")
	ck(nameAdd9 == nameAdd3, "closures from same factory share code symbol")

	boom := makePanicClosure(p)
	nameBoom := closureName(boom)
	ck(nameBoom == "main.makePanicClosure.func1", "panic closure funcval resolves to real symbol")

	stack, ok := callPanicClosure(boom)
	ck(ok, "panic closure payload identity")
	ck(strings.Contains(stack, "main.makePanicClosure.func1("), "traceback has closure symbol")
	ck(strings.Contains(stack, "main.callPanicClosure("), "traceback has invoking function symbol")
	ck(strings.Contains(stack, "goroutine "), "traceback has goroutine header")
	ck(lineRe.MatchString(stack), "traceback has closure main.go:line")

	gcStress()
	ck(p.id == 41 && p.name == "closure-name" && p.next != nil && p.next.id == 42, "captured payload after GC")
	finish("l14_closure_funcname")
}
