// L14: runtime.CallersFrames 必须给出真实的方法符号（指针/值接收者区分）与
// 闭包符号；所有帧带 main.go 文件与正的行号。
package main

import (
	"fmt"
	"os"
	"runtime"
	"strings"
	"sync/atomic"
)

type methodPayload struct {
	id   int64
	name string
}

type stackWorker struct {
	id      int64
	payload *methodPayload
}

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

func captureFrames() []runtime.Frame {
	pcs := make([]uintptr, 32)
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

// 指针接收者方法：运行时符号应为 main.(*stackWorker).methodChain。
//
//go:noinline
func (w *stackWorker) methodChain() []runtime.Frame { return w.pointerStep() }

//go:noinline
func (w *stackWorker) pointerStep() []runtime.Frame { return w.valueStep() }

// 值接收者方法：运行时符号应为 main.stackWorker.valueStep。
//
//go:noinline
func (w stackWorker) valueStep() []runtime.Frame { return captureFrames() }

// 闭包工厂：noinline 保证闭包符号稳定为 main.makeClosureFunc.func1。
//
//go:noinline
func makeClosureFunc() func() []runtime.Frame {
	return func() []runtime.Frame { return captureFrames() }
}

//go:noinline
func invokeClosure(f func() []runtime.Frame) []runtime.Frame { return f() }

func findFrame(frames []runtime.Frame, fn string) (runtime.Frame, bool) {
	for _, f := range frames {
		if f.Function == fn {
			return f, true
		}
	}
	return runtime.Frame{}, false
}

func checkFrame(frames []runtime.Frame, fn string) {
	f, ok := findFrame(frames, fn)
	ck(ok, "missing frame symbol "+fn)
	if !ok {
		return
	}
	ck(strings.HasSuffix(f.File, "main.go"), "frame "+fn+" has main.go file")
	ck(f.Line > 0, "frame "+fn+" has positive line")
	ck(runtime.FuncForPC(f.Entry).Name() == fn, "frame "+fn+" resolves to real symbol")
}

func joinNames(frames []runtime.Frame) string {
	names := make([]string, 0, len(frames))
	for _, f := range frames {
		names = append(names, f.Function)
	}
	return strings.Join(names, ",")
}

func main() {
	w := &stackWorker{id: 5, payload: &methodPayload{id: 5, name: "methods"}}
	gcStress()

	methodFrames := w.methodChain()
	ck(len(methodFrames) >= 4, "method chain frames present")
	checkFrame(methodFrames, "main.(*stackWorker).methodChain")
	checkFrame(methodFrames, "main.(*stackWorker).pointerStep")
	checkFrame(methodFrames, "main.stackWorker.valueStep")
	checkFrame(methodFrames, "main.captureFrames")
	joined := joinNames(methodFrames)
	ck(strings.Contains(joined, "main.captureFrames,main.stackWorker.valueStep,main.(*stackWorker).pointerStep,main.(*stackWorker).methodChain"), "method frame order")

	closureFrames := invokeClosure(makeClosureFunc())
	checkFrame(closureFrames, "main.makeClosureFunc.func1")
	checkFrame(closureFrames, "main.invokeClosure")
	checkFrame(closureFrames, "main.captureFrames")

	ck(w.payload != nil && w.payload.id == 5 && w.payload.name == "methods", "method receiver payload")
	finish("l14_callers_methods_closures")
}
