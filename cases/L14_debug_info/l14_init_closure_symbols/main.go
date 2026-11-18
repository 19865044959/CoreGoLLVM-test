// L14: init 期闭包与方法作用域闭包的符号。包级函数字面量由 init 生成
// main.init.funcN，包级闭包工厂产出的内层闭包是 main.init.funcN.1，方法内返回的
// 闭包是 main.(*T).method.func1。panic traceback 必须落到这些真实符号并带 main.go:行号。
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

type icPayload struct {
	id   int64
	name string
	next *icPayload
}

type icHolder struct {
	p *icPayload
}

var (
	failed int32
	gcSink [][]byte
	lineRe = regexp.MustCompile(`main\.go:\d+`)
)

// 包级函数字面量 1：init 期间创建。
var initCloOne = func(p *icPayload) { panic(p) }

// 包级函数字面量 2：init 期间创建，调用后返回另一个闭包字面量。
var initCloFactory = func() func(*icPayload) {
	return func(p *icPayload) { panic(p) }
}

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
func (h *icHolder) build() func() {
	return func() { panic(h.p) }
}

//go:noinline
func icRun(f func()) (stack string, frames []runtime.Frame, ok bool) {
	defer func() {
		e := recover()
		if e == nil {
			return
		}
		ok = e != nil
		stack = string(debug.Stack())
		pcs := make([]uintptr, 32)
		n := runtime.Callers(2, pcs)
		if n == 0 {
			return
		}
		it := runtime.CallersFrames(pcs[:n])
		for {
			fr, more := it.Next()
			frames = append(frames, fr)
			if !more {
				break
			}
		}
	}()
	f()
	return "", nil, false
}

func icName(f any) string {
	p := reflect.ValueOf(f).Pointer()
	fn := runtime.FuncForPC(p)
	if fn == nil {
		return ""
	}
	return fn.Name()
}

func icFrameNamed(frames []runtime.Frame, symbol string) (runtime.Frame, bool) {
	for _, f := range frames {
		if f.Function == symbol {
			return f, true
		}
	}
	return runtime.Frame{}, false
}

func icCheckSymbol(tag, symbol, stack string, frames []runtime.Frame) {
	ck(strings.HasPrefix(symbol, "main."), tag+" has main package symbol")
	ck(strings.Contains(symbol, "func"), tag+" symbol is a closure")
	ck(strings.Contains(stack, symbol+"("), tag+" traceback has closure symbol")
	ck(lineRe.MatchString(stack), tag+" traceback has main.go:line")
	f, found := icFrameNamed(frames, symbol)
	ck(found, tag+" frames contain closure symbol")
	ck(strings.HasSuffix(f.File, "main.go"), tag+" frame has main.go file")
	ck(f.Line > 0, tag+" frame has positive line")
	ck(runtime.FuncForPC(f.Entry).Name() == f.Function, tag+" entry resolves to same symbol")
}

func main() {
	p := &icPayload{id: 91, name: "init-closure"}
	p.next = &icPayload{id: 92, name: "init-next"}
	h := &icHolder{p: p}
	gcStress()

	// 1) 包级闭包符号由 init 生成，且两个包级字面量符号不同。
	oneName := icName(initCloOne)
	factoryName := icName(initCloFactory)
	inner := initCloFactory()
	innerName := icName(inner)
	methodClo := h.build()
	methodName := icName(methodClo)

	ck(initCloOne != nil && initCloFactory != nil, "package-level closures initialized before main")
	ck(strings.HasPrefix(oneName, "main.init."), "package-level closure symbol lives in main.init")
	ck(strings.HasPrefix(factoryName, "main.init."), "package-level factory symbol lives in main.init")
	ck(oneName != factoryName, "two package-level closure literals have distinct symbols")
	ck(strings.HasPrefix(innerName, factoryName+"."), "nested package-level closure is named under its factory")
	ck(innerName != factoryName, "nested package-level closure has its own symbol")
	ck(methodName == "main.(*icHolder).build.func1", "method-scope closure has real method-scoped symbol")
	ck(oneName != innerName && oneName != methodName && innerName != methodName,
		"init, nested-init and method closures use distinct symbols")

	// 2) 三类闭包的 panic traceback 都必须带真实符号与行号。
	s1, f1, ok1 := icRun(func() { initCloOne(p) })
	ck(ok1, "init closure recovered payload")
	icCheckSymbol("init closure", oneName, s1, f1)

	s2, f2, ok2 := icRun(func() { inner(p) })
	ck(ok2, "nested init closure recovered payload")
	icCheckSymbol("nested init closure", innerName, s2, f2)

	s3, f3, ok3 := icRun(func() { methodClo() })
	ck(ok3, "method closure recovered payload")
	icCheckSymbol("method closure", methodName, s3, f3)

	// 3) 包级闭包在 init 阶段创建、main 阶段才被间接调用，帧仍指向闭包体行号。
	ck(strings.Contains(s1, "main.init."), "init closure traceback keeps init-time symbol")
	ck(strings.Contains(s1, "goroutine "), "init closure traceback has goroutine header")
	ck(strings.Contains(s2, "main.init."), "nested init closure traceback keeps init-time symbol")

	// 4) GC 后闭包捕获的 payload 链仍然有效。
	gcStress()
	ck(h.p == p && p.id == 91 && p.name == "init-closure", "method closure capture after GC")
	ck(p.next != nil && p.next.id == 92 && p.next.name == "init-next", "init payload chain after GC")
	ck(initCloOne != nil && initCloFactory != nil, "package closures survive GC")
	finish("l14_init_closure_symbols")
}
