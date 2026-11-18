// L14: 接口方法值与嵌入提升方法的符号。接口方法值在运行期是 main.<Iface>.<Method>-fm
// 包装，绑定的是创建时的动态接收者；接口方法表达式不带 -fm。嵌入提升的方法值
// 解析到基类方法的 -fm 包装。panic traceback 必须落到真实实现方法并带 main.go:行号。
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

type imPayload struct {
	id   int64
	name string
	next *imPayload
}

type imBoom struct {
	receiver int64
	p        *imPayload
}

type imPromBoom struct {
	base string
	p    *imPayload
}

type imWorker struct {
	id int64
}

//go:noinline
func (w *imWorker) Handle(p *imPayload) { panic(imBoom{receiver: w.id, p: p}) }

type imIface interface {
	Handle(*imPayload)
}

type imBase struct {
	name string
}

//go:noinline
func (b *imBase) Promoted(p *imPayload) { panic(imPromBoom{base: b.name, p: p}) }

type imDerived struct {
	*imBase
}

type imPromIface interface {
	Promoted(*imPayload)
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
func imInvoke(f func(*imPayload), p *imPayload) { f(p) }

//go:noinline
func imRun(f func(*imPayload), p *imPayload) (stack string, frames []runtime.Frame, recv int64, kind int, ok bool) {
	defer func() {
		e := recover()
		if e == nil {
			return
		}
		switch v := e.(type) {
		case imBoom:
			kind = 1
			recv = v.receiver
			ok = v.p == p
		case imPromBoom:
			kind = 2
			recv = -1
			ok = v.p == p
		default:
			ok = false
		}
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
	imInvoke(f, p)
	return "", nil, 0, 0, false
}

func imName(f any) string {
	p := reflect.ValueOf(f).Pointer()
	fn := runtime.FuncForPC(p)
	if fn == nil {
		return ""
	}
	return fn.Name()
}

func imFrameNamed(frames []runtime.Frame, symbol string) (runtime.Frame, bool) {
	for _, f := range frames {
		if f.Function == symbol {
			return f, true
		}
	}
	return runtime.Frame{}, false
}

func main() {
	p := &imPayload{id: 211, name: "iface-method-value"}
	p.next = &imPayload{id: 212, name: "iface-next"}
	gcStress()

	// 1) 接口方法值/方法表达式/具体方法表达式符号。
	w1 := &imWorker{id: 1}
	var i imIface = w1
	mv := i.Handle
	ck(imName(mv) == "main.imIface.Handle-fm", "interface method value uses -fm wrapper symbol")
	ck(imName(imIface.Handle) == "main.imIface.Handle", "interface method expression keeps plain symbol")
	ck(imName((*imWorker).Handle) == "main.(*imWorker).Handle", "concrete method expression keeps plain symbol")

	// 2) 嵌入提升的方法值符号解析到基类方法包装。
	base := &imBase{name: "base"}
	d := &imDerived{imBase: base}
	var pi imPromIface = d
	pv := pi.Promoted
	ck(imName(pv) == "main.imPromIface.Promoted-fm", "promoted interface method value uses interface -fm wrapper")
	ck(imName(d.Promoted) == "main.(*imBase).Promoted-fm", "promoted concrete method value resolves to base -fm wrapper")
	ck(imName(imPromIface.Promoted) == "main.imPromIface.Promoted", "promoted interface method expression keeps plain symbol")

	// 3) 接口方法值绑定创建时的动态接收者，之后更换接口变量不影响已绑定方法值。
	mvOld := i.Handle
	i = &imWorker{id: 2}
	stack, frames, recv, kind, ok := imRun(mvOld, p)
	ck(ok && kind == 1, "interface method value recovered its payload")
	ck(recv == 1, "interface method value captured dynamic receiver at bind time")
	ck(strings.Contains(stack, "main.(*imWorker).Handle("), "interface method value traceback has concrete target symbol")
	ck(strings.Contains(stack, "main.imInvoke("), "interface method value traceback has indirect invoker")
	ck(lineRe.MatchString(stack), "interface method value traceback has main.go:line")
	f, found := imFrameNamed(frames, "main.(*imWorker).Handle")
	ck(found, "interface method value frames contain concrete target")
	ck(strings.HasSuffix(f.File, "main.go") && f.Line > 0, "concrete target frame has file and line")
	ck(runtime.FuncForPC(f.Entry).Name() == f.Function, "concrete target entry resolves to same symbol")

	// 4) 嵌入提升方法经接口方法值调用，traceback 落到基类方法。
	ps, pf, _, pkind, pok := imRun(pv, p)
	ck(pok && pkind == 2, "promoted interface method value recovered its payload")
	ck(strings.Contains(ps, "main.(*imBase).Promoted("), "promoted method value traceback has base method symbol")
	ck(strings.Contains(ps, "main.imInvoke("), "promoted method value traceback has indirect invoker")
	ck(lineRe.MatchString(ps), "promoted method value traceback has main.go:line")
	pfrm, pfound := imFrameNamed(pf, "main.(*imBase).Promoted")
	ck(pfound, "promoted method value frames contain base method")
	ck(strings.HasSuffix(pfrm.File, "main.go") && pfrm.Line > 0, "promoted base frame has file and line")

	// 5) 接口方法值能观察接收者的后续状态（指针接收者），值接收者测试略；这里验证指针。
	w1.id = 7
	_, _, recv2, kind2, ok2 := imRun(mv, p)
	ck(ok2 && kind2 == 1, "re-bound interface method value recovered payload")
	ck(recv2 == 7, "pointer receiver method value observes current receiver state")

	// 6) GC 后 payload 链与提升字段仍有效。
	gcStress()
	ck(d.imBase == base && base.name == "base", "promoted embedded field after GC")
	ck(p.next != nil && p.next.id == 212 && p.next.name == "iface-next", "interface payload chain after GC")
	ck(w1.id == 7 && mv != nil && pv != nil, "method values survive GC")
	finish("l14_interface_method_value")
}
