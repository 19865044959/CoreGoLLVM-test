// L14: 泛型实例化的嵌套类型边界。gnOuter[int] 的实例化体调用 gnMid[int]，
// 而 gnMid[int] 固定调用 gnInner[string]：一次 panic traceback 同时展开三个
// 泛型实例化符号。payload 中的 %T 证明叶子确为 string 实例化（不是 outer 的 int），
// 帧行号必须按 gnInner < gnMid < gnOuter 递增；int/string 实例化入口必须不同。
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

type gnNode struct {
	id   int64
	name string
	next *gnNode
}

type gnMark struct {
	id  int64
	typ string
	p   *gnNode
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
func gnInner[T any](p *gnNode) {
	var zero T
	panic(gnMark{id: p.id, typ: fmt.Sprintf("%T", zero), p: p})
}

// gnMid 对所有 T 都固定调用 string 实例化。
//
//go:noinline
func gnMid[T any](p *gnNode) { gnInner[string](p) }

//go:noinline
func gnOuter[T any](p *gnNode) (stack string, frames []runtime.Frame, mark gnMark, ok bool) {
	defer func() {
		e := recover()
		if e == nil {
			return
		}
		m, isMark := e.(gnMark)
		mark = m
		ok = isMark && m.p == p
		stack = string(debug.Stack())
		pcs := make([]uintptr, 64)
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
	gnMid[T](p)
	return "", nil, gnMark{}, false
}

func gnName(f any) string {
	fn := runtime.FuncForPC(reflect.ValueOf(f).Pointer())
	if fn == nil {
		return ""
	}
	return fn.Name()
}

func gnFrameNamed(frames []runtime.Frame, symbol string) (runtime.Frame, bool) {
	for _, f := range frames {
		if f.Function == symbol {
			return f, true
		}
	}
	return runtime.Frame{}, false
}

func main() {
	p := &gnNode{id: 611, name: "generic-nested"}
	p.next = &gnNode{id: 612, name: "generic-nested-next"}
	gcStress()

	// 1) 实例化符号与入口。三个实例化符号都带方括号，int/string 入口不同。
	innerInt := gnName(gnInner[int])
	innerStr := gnName(gnInner[string])
	midInt := gnName(gnMid[int])
	midStr := gnName(gnMid[string])
	outerInt := gnName(gnOuter[int])
	ck(strings.HasPrefix(innerInt, "main.gnInner[") && strings.HasSuffix(innerInt, "]"),
		"inner int instantiation has bracketed symbol")
	ck(strings.HasPrefix(innerStr, "main.gnInner[") && strings.HasSuffix(innerStr, "]"),
		"inner string instantiation has bracketed symbol")
	ck(strings.HasPrefix(midInt, "main.gnMid["), "mid int instantiation has bracketed symbol")
	ck(strings.HasPrefix(midStr, "main.gnMid["), "mid string instantiation has bracketed symbol")
	ck(strings.HasPrefix(outerInt, "main.gnOuter["), "outer int instantiation has bracketed symbol")
	ck(reflect.ValueOf(gnInner[int]).Pointer() != reflect.ValueOf(gnInner[string]).Pointer(),
		"inner int and string instantiations use distinct code entries")
	ck(reflect.ValueOf(gnMid[int]).Pointer() != reflect.ValueOf(gnMid[string]).Pointer(),
		"mid int and string instantiations use distinct code entries")

	// 2) outer[int] -> mid[int] -> inner[string]：类型标记证明跨实例化嵌套。
	stack, frames, mark, ok := gnOuter[int](p)
	ck(ok, "nested generic recovered payload identity")
	ck(mark.id == 611, "nested generic payload id")
	ck(mark.typ == "string", "outer[int] reaches the string instantiation at the leaf")
	ck(mark.p == p, "nested generic payload pointer preserved")

	// 3) 同一个 traceback 同时包含三个实例化符号；gnOuter 还有 recover 闭包帧，
	// 因此按 "符号(" 精确计数各自的主函数帧。
	for _, sym := range []string{"main.gnInner[...]", "main.gnMid[...]", "main.gnOuter[...]"} {
		ck(strings.Contains(stack, sym), "traceback contains "+sym)
		ck(strings.Count(stack, sym+"(") == 1, "traceback contains exactly one "+sym+" frame")
	}
	ck(strings.Contains(stack, "goroutine "), "nested generic traceback has goroutine header")
	ck(lineRe.MatchString(stack), "nested generic traceback has main.go:line")

	// 4) CallersFrames 中三个逻辑帧的符号、文件、行号与顺序。
	innerF, foundInner := gnFrameNamed(frames, "main.gnInner[...]")
	midF, foundMid := gnFrameNamed(frames, "main.gnMid[...]")
	outerF, foundOuter := gnFrameNamed(frames, "main.gnOuter[...]")
	ck(foundInner, "frames contain inner instantiation")
	ck(foundMid, "frames contain mid instantiation")
	ck(foundOuter, "frames contain outer instantiation")
	for _, tc := range []struct {
		sym string
		f   runtime.Frame
	}{
		{"main.gnInner[...]", innerF},
		{"main.gnMid[...]", midF},
		{"main.gnOuter[...]", outerF},
	} {
		ck(strings.HasSuffix(tc.f.File, "main.go"), tc.sym+" frame has main.go file")
		ck(tc.f.Line > 0, tc.sym+" frame has positive line")
		ck(runtime.FuncForPC(tc.f.Entry).Name() == tc.sym, tc.sym+" entry resolves to same symbol")
	}
	ck(innerF.Line < midF.Line, "panic line precedes nested string-instantiation call line")
	ck(midF.Line < outerF.Line, "mid call line precedes outer call line")

	// 5) GC 后嵌套实例化仍持有有效 payload 链。
	gcStress()
	ck(p.id == 611 && p.name == "generic-nested", "nested generic payload after GC")
	ck(p.next != nil && p.next.id == 612 && p.next.name == "generic-nested-next",
		"nested generic payload chain after GC")
	ck(mark.p == p && mark.typ == "string", "recovered nested generic mark after GC")
	finish("l14_generic_nested_instantiation")
}
