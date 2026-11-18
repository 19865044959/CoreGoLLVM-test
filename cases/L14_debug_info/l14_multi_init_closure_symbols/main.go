// L14: 多个显式 init 函数中的闭包符号边界。同一文件里两个 func init() 的闭包
// 分别落在 main.init.0.* / main.init.1.* 命名空间；包级 var 初始化器的闭包落在
// main.init.*（不带显式 init 序号）。init 内调用的包级工厂返回的闭包挂在
// main.init.N.<factory> 下。四类闭包 panic 后 traceback 必须给出各自的真实符号。
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

type idPayload struct {
	id   int64
	name string
	next *idPayload
}

type idMark struct {
	id int64
	p  *idPayload
}

var (
	failed int32
	gcSink [][]byte
	lineRe = regexp.MustCompile(`main\.go:\d+`)

	// 包级 var 初始化器闭包：符号为 main.init.funcN。
	idVarClosure = func(p *idPayload) { panic(idMark{id: p.id, p: p}) }

	// 显式 init 函数中注册的三类闭包。
	idClosures []func(*idPayload)
)

func init() {
	idClosures = append(idClosures, func(p *idPayload) { panic(idMark{id: p.id, p: p}) })
	idClosures = append(idClosures, idMakeClosure())
}

func init() {
	idClosures = append(idClosures, func(p *idPayload) { panic(idMark{id: p.id, p: p}) })
}

// idMakeClosure 由第一个显式 init 调用，返回闭包挂在 init.0 命名空间下。
func idMakeClosure() func(*idPayload) {
	return func(p *idPayload) { panic(idMark{id: p.id, p: p}) }
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
func idRun(f func(*idPayload), p *idPayload) (stack string, frames []runtime.Frame, ok bool) {
	defer func() {
		e := recover()
		if e == nil {
			return
		}
		m, isMark := e.(idMark)
		ok = isMark && m.p == p && m.id == p.id
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

func idName(f any) string {
	fn := runtime.FuncForPC(reflect.ValueOf(f).Pointer())
	if fn == nil {
		return ""
	}
	return fn.Name()
}

func idFrameNamed(frames []runtime.Frame, symbol string) (runtime.Frame, bool) {
	for _, f := range frames {
		if f.Function == symbol {
			return f, true
		}
	}
	return runtime.Frame{}, false
}

func main() {
	p := &idPayload{id: 921, name: "init-func"}
	p.next = &idPayload{id: 922, name: "init-func-next"}
	gcStress()

	ck(len(idClosures) == 3, "both init functions registered their closures")
	varName := idName(idVarClosure)
	n0, n1, n2 := "", "", ""
	if len(idClosures) == 3 {
		n0 = idName(idClosures[0])
		n1 = idName(idClosures[1])
		n2 = idName(idClosures[2])
	}

	// 1) 显式 init 函数按源码顺序占用 init.0 / init.1 命名空间。
	ck(strings.HasPrefix(varName, "main.init."), "var initializer closure lives in main.init")
	ck(!strings.HasPrefix(varName, "main.init.0.") && !strings.HasPrefix(varName, "main.init.1."),
		"var initializer closure has no explicit init ordinal")
	ck(strings.HasPrefix(n0, "main.init.0."), "first explicit init closure lives in init.0")
	ck(strings.HasPrefix(n1, "main.init.0."), "factory closure called from first init lives in init.0")
	ck(strings.Contains(n1, "idMakeClosure.func"), "factory closure is named under idMakeClosure")
	ck(strings.HasPrefix(n2, "main.init.1."), "second explicit init closure lives in init.1")
	all := []string{varName, n0, n1, n2}
	for i := 0; i < len(all); i++ {
		ck(strings.HasPrefix(all[i], "main."), fmt.Sprintf("closure %d has main symbol", i))
		for j := i + 1; j < len(all); j++ {
			ck(all[i] != all[j], fmt.Sprintf("closures %d and %d have distinct symbols", i, j))
		}
	}

	// 2) 四类 init 期闭包 panic 后的 traceback 与帧都落到各自真实符号。
	cases := []struct {
		tag string
		f   func(*idPayload)
		sym string
	}{
		{"var-initializer", idVarClosure, varName},
		{"init.0-literal", idClosures[0], n0},
		{"init.0-factory", idClosures[1], n1},
		{"init.1-literal", idClosures[2], n2},
	}
	for _, tc := range cases {
		stack, frames, ok := idRun(tc.f, p)
		ck(ok, tc.tag+" recovered init-time payload")
		if tc.sym == "" {
			continue
		}
		ck(strings.Contains(stack, tc.sym+"("), tc.tag+" traceback has closure symbol")
		ck(lineRe.MatchString(stack), tc.tag+" traceback has main.go:line")
		f, found := idFrameNamed(frames, tc.sym)
		ck(found, tc.tag+" frames contain closure symbol")
		ck(strings.HasSuffix(f.File, "main.go"), tc.tag+" frame has main.go file")
		ck(f.Line > 0, tc.tag+" frame has positive line")
		ck(runtime.FuncForPC(f.Entry).Name() == f.Function, tc.tag+" entry resolves to same symbol")
		ck(strings.Contains(stack, "goroutine "), tc.tag+" traceback has goroutine header")
	}

	// 3) 显式 init 与 var 初始化器的闭包符号不会互相覆盖。
	ck(varName != n0 && varName != n1 && varName != n2, "var closure symbol is not reused by init.0/init.1")
	ck(n0 != n2, "init.0 and init.1 closures have different symbols")
	ck(strings.Contains(n0, "func"), "init.0 closure symbol has func marker")
	ck(strings.Contains(n2, "func"), "init.1 closure symbol has func marker")

	// 4) GC 后 init 期捕获状态仍有效。
	gcStress()
	ck(p.id == 921 && p.name == "init-func", "init payload after GC")
	ck(p.next != nil && p.next.id == 922 && p.next.name == "init-func-next", "init payload chain after GC")
	ck(idVarClosure != nil && idClosures[0] != nil && idClosures[1] != nil && idClosures[2] != nil,
		"init-time closures survive GC")
	finish("l14_multi_init_closure_symbols")
}
