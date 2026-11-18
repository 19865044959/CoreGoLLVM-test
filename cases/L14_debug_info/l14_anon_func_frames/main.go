// L14: 匿名函数帧。立即执行的匿名函数（IIFE）与作为参数/表元素传递的匿名函数，
// 在 traceback 与 runtime.Callers 中都必须解析为真实 .funcN 符号并带 main.go:行号。
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

type afPayload struct {
	id   int64
	name string
	next *afPayload
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

// anonIIFE 内部是立即执行的匿名函数；panic 发生在该匿名函数帧内。
//
//go:noinline
func anonIIFE(p *afPayload) {
	func() { panic(p) }()
}

//go:noinline
func anonInvoke(f func(*afPayload), p *afPayload) { f(p) }

//go:noinline
func anonRun(f func(*afPayload), p *afPayload) (stack string, ok bool) {
	defer func() {
		if e := recover(); e != nil {
			q, is := e.(*afPayload)
			ok = is && q == p
			stack = string(debug.Stack())
		}
	}()
	anonInvoke(f, p)
	return "", false
}

//go:noinline
func anonFrames() []runtime.Frame {
	pcs := make([]uintptr, 16)
	n := runtime.Callers(1, pcs)
	fr := runtime.CallersFrames(pcs[:n])
	out := make([]runtime.Frame, 0, 8)
	for {
		f, more := fr.Next()
		out = append(out, f)
		if !more {
			break
		}
	}
	return out
}

func main() {
	p := &afPayload{id: 81, name: "anon"}
	p.next = &afPayload{id: 82, name: "next"}
	gcStress()

	// 1) IIFE 帧：符号为 main.anonIIFE.func1。
	iifeStack, iifeOK := anonRun(func(p *afPayload) { anonIIFE(p) }, p)
	ck(iifeOK, "IIFE payload identity")
	ck(strings.Contains(iifeStack, "main.anonIIFE.func1("), "traceback has IIFE anonymous symbol")
	ck(strings.Contains(iifeStack, "main.anonIIFE("), "traceback has IIFE host symbol")
	ck(strings.Contains(iifeStack, "main.anonInvoke("), "traceback has invoker symbol")
	ck(strings.Contains(iifeStack, "main.anonRun("), "traceback has recovering symbol")
	ck(lineRe.MatchString(iifeStack), "IIFE traceback has main.go:line")

	// 2) 匿名函数作为参数：从匿名函数内部抓自身帧，校验真实 .func 符号。
	var frames []runtime.Frame
	_, ok := anonRun(func(p *afPayload) {
		frames = anonFrames()
		panic(p)
	}, p)
	ck(ok, "anonymous parameter payload identity")
	ck(anonFramesResolve(frames, "main.main.func"), "anonymous parameter frame resolves to real symbol")

	// 3) 匿名函数放在函数表中：两个不同字面量的符号都必须真实可解析。
	table := []func(*afPayload){
		func(p *afPayload) { panic(p) },
		func(p *afPayload) { panic(p) },
	}
	var names []string
	for _, fn := range table {
		st, ok := anonRun(fn, p)
		ck(ok, "anonymous table payload identity")
		ck(lineRe.MatchString(st), "anonymous table traceback has main.go:line")
		name := anonFrameName(fn)
		ck(strings.HasPrefix(name, "main.main.func"), "anonymous table funcval has real .func symbol")
		names = append(names, name)
	}
	ck(len(names) == 2 && names[0] != names[1], "distinct anonymous literals have distinct symbols")

	gcStress()
	ck(p.id == 81 && p.name == "anon" && p.next != nil && p.next.id == 82, "payload after GC")
	finish("l14_anon_func_frames")
}

func anonFramesResolve(frames []runtime.Frame, prefix string) bool {
	if len(frames) < 2 {
		return false
	}
	f := frames[1] // frames[0] 是 anonFrames 自身
	if !strings.HasPrefix(f.Function, prefix) {
		return false
	}
	if !strings.HasSuffix(f.File, "main.go") || f.Line <= 0 {
		return false
	}
	return runtime.FuncForPC(f.Entry).Name() == f.Function
}

func anonFrameName(f func(*afPayload)) string {
	if f == nil {
		return ""
	}
	return runtime.FuncForPC(reflect.ValueOf(f).Pointer()).Name()
}
