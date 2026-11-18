// L14: 泛型实例化函数的调试信息。泛型函数/泛型方法/泛型方法内闭包在运行期有真实
// 实例化符号（main.gpLeaf[...] / main.(*gpBox[...]).Boom-fm），panic traceback 必须
// 展开到这些符号并带 main.go:行号；同一泛型函数的 int/string 实例必须是不同的代码入口。
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

type gpNode struct {
	id   int64
	name string
	next *gpNode
}

type gpBox[T any] struct {
	v T
	p *gpNode
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
func gpLeaf[T any](p *gpNode) { panic(p) }

//go:noinline
func gpMid[T any](p *gpNode) { gpLeaf[T](p) }

//go:noinline
func gpOuter[T any](p *gpNode) (stack string, frames []runtime.Frame, ok bool) {
	defer func() {
		e := recover()
		if e == nil {
			return
		}
		q, isNode := e.(*gpNode)
		ok = isNode && q == p
		stack = string(debug.Stack())
		pcs := make([]uintptr, 32)
		n := runtime.Callers(2, pcs)
		if n == 0 {
			return
		}
		it := runtime.CallersFrames(pcs[:n])
		for {
			f, more := it.Next()
			frames = append(frames, f)
			if !more {
				break
			}
		}
	}()
	gpMid[T](p)
	return "", nil, false
}

//go:noinline
func (b *gpBox[T]) Boom(p *gpNode) { panic(p) }

//go:noinline
func (b *gpBox[T]) Make() func(*gpNode) {
	return func(q *gpNode) { panic(q) }
}

//go:noinline
func gpRun(f func(*gpNode), p *gpNode) (stack string, frames []runtime.Frame, ok bool) {
	defer func() {
		e := recover()
		if e == nil {
			return
		}
		q, isNode := e.(*gpNode)
		ok = isNode && q == p
		stack = string(debug.Stack())
		pcs := make([]uintptr, 32)
		n := runtime.Callers(2, pcs)
		if n == 0 {
			return
		}
		it := runtime.CallersFrames(pcs[:n])
		for {
			f, more := it.Next()
			frames = append(frames, f)
			if !more {
				break
			}
		}
	}()
	f(p)
	return "", nil, false
}

func gpName(f any) string {
	p := reflect.ValueOf(f).Pointer()
	fn := runtime.FuncForPC(p)
	if fn == nil {
		return ""
	}
	return fn.Name()
}

func gpFrameNamed(frames []runtime.Frame, symbol string) (runtime.Frame, bool) {
	for _, f := range frames {
		if f.Function == symbol {
			return f, true
		}
	}
	return runtime.Frame{}, false
}

func main() {
	p := &gpNode{id: 701, name: "generic-int"}
	p.next = &gpNode{id: 702, name: "generic-next"}
	q := &gpNode{id: 801, name: "generic-string"}
	gcStress()

	// 1) 泛型函数的运行期名字带实例化括号，且 int/string 是不同入口。
	intName := gpName(gpLeaf[int])
	strName := gpName(gpLeaf[string])
	ck(strings.HasPrefix(intName, "main.gpLeaf[") && strings.HasSuffix(intName, "]"),
		"generic int instantiation has bracketed runtime symbol")
	ck(strings.HasPrefix(strName, "main.gpLeaf[") && strings.HasSuffix(strName, "]"),
		"generic string instantiation has bracketed runtime symbol")
	ck(reflect.ValueOf(gpLeaf[int]).Pointer() != reflect.ValueOf(gpLeaf[string]).Pointer(),
		"int and string instantiations use distinct code entries")
	ck(strings.HasPrefix(gpName(gpMid[int]), "main.gpMid["),
		"generic mid function has instantiated symbol")

	// 2) 泛型方法与泛型方法内的闭包符号。
	box := &gpBox[int]{v: 11, p: p}
	mv := box.Boom
	expr := (*gpBox[int]).Boom
	mk := box.Make()
	ck(strings.Contains(gpName(mv), "gpBox[") && strings.HasSuffix(gpName(mv), "]).Boom-fm"),
		"generic method value resolves to bracketed -fm wrapper")
	ck(strings.Contains(gpName(expr), "gpBox[") && strings.HasSuffix(gpName(expr), "]).Boom"),
		"generic method expression resolves to bracketed method symbol")
	ck(strings.Contains(gpName(mk), "gpBox[") && strings.HasSuffix(gpName(mk), "]).Make.func1"),
		"closure inside generic method has bracketed parent symbol")

	// 3) 两种实例化的 panic traceback 都展开到真实泛型符号与行号。
	for _, tc := range []struct {
		name string
		run  func() (string, []runtime.Frame, bool)
		pay  *gpNode
	}{
		{"int", func() (string, []runtime.Frame, bool) { return gpOuter[int](p) }, p},
		{"string", func() (string, []runtime.Frame, bool) { return gpOuter[string](q) }, q},
	} {
		stack, frames, ok := tc.run()
		ck(ok, "generic "+tc.name+" recovered payload identity")
		ck(strings.Contains(stack, "main.gpLeaf["), "generic "+tc.name+" traceback has leaf symbol")
		ck(strings.Contains(stack, "main.gpMid["), "generic "+tc.name+" traceback has mid symbol")
		ck(strings.Contains(stack, "main.gpOuter["), "generic "+tc.name+" traceback has outer symbol")
		ck(strings.Contains(stack, "goroutine "), "generic "+tc.name+" traceback has goroutine header")
		ck(lineRe.MatchString(stack), "generic "+tc.name+" traceback has main.go:line")
		for _, want := range []string{"main.gpLeaf[...]", "main.gpMid[...]", "main.gpOuter[...]"} {
			f, found := gpFrameNamed(frames, want)
			ck(found, "generic "+tc.name+" frames contain "+want)
			ck(strings.HasSuffix(f.File, "main.go"), "generic "+tc.name+" frame "+want+" has main.go file")
			ck(f.Line > 0, "generic "+tc.name+" frame "+want+" has positive line")
			ck(runtime.FuncForPC(f.Entry).Name() == f.Function,
				"generic "+tc.name+" frame "+want+" entry resolves to same symbol")
		}
	}

	// 4) 泛型方法值与泛型闭包的 panic traceback。
	ms, mf, mok := gpRun(mv, p)
	ck(mok, "generic method value recovered payload identity")
	ck(strings.Contains(ms, "main.(*gpBox[...]).Boom("), "generic method value traceback has method symbol")
	ck(lineRe.MatchString(ms), "generic method value traceback has main.go:line")
	_, foundBoom := gpFrameNamed(mf, "main.(*gpBox[...]).Boom")
	ck(foundBoom, "generic method value frames contain real instantiated method")

	cs, cf, cok := gpRun(mk, p)
	ck(cok, "generic closure recovered payload identity")
	ck(strings.Contains(cs, "main.(*gpBox[...]).Make.func1("), "generic closure traceback has closure symbol")
	_, foundClo := gpFrameNamed(cf, "main.(*gpBox[...]).Make.func1")
	ck(foundClo, "generic closure frames contain real closure symbol")
	ck(lineRe.MatchString(cs), "generic closure traceback has main.go:line")

	// 5) GC 后泛型容器仍持有有效 payload 链。
	gcStress()
	ck(box.p == p && box.p.id == 701 && box.p.name == "generic-int", "generic box payload after GC")
	ck(box.p.next != nil && box.p.next.id == 702, "generic box payload chain after GC")
	ck(p.next != nil && p.next.name == "generic-next", "generic payload chain after GC")
	ck(q.id == 801 && q.name == "generic-string", "generic string payload after GC")
	finish("l14_generic_func_traceback")
}
