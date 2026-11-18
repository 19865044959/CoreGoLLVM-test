// L14: 值接收者接口方法值的符号与绑定边界。动态类型是值类型 ivWorker（非指针），
// 接口方法值 i.Do 是 main.ivIface.Do-fm 包装，接口方法表达式是 main.ivIface.Do，
// 具体值方法值是 main.ivWorker.Do-fm。接口保存的是赋值时的值拷贝，重新赋值接口
// 不影响已绑定的方法值；经方法值 panic 后 traceback 必须落到 main.ivWorker.Do
// 与 main.go:行号。
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

type ivPayload struct {
	id   int64
	name string
	next *ivPayload
}

type ivWorker struct {
	id   int64
	tags string
}

type ivBoom struct {
	worker int64
	tags   string
	p      *ivPayload
}

//go:noinline
func (w ivWorker) Do(p *ivPayload) {
	panic(ivBoom{worker: w.id, tags: w.tags, p: p})
}

type ivIface interface {
	Do(*ivPayload)
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
func ivInvoke(f func(*ivPayload), p *ivPayload) { f(p) }

//go:noinline
func ivRun(f func(*ivPayload), p *ivPayload) (stack string, frames []runtime.Frame, boom ivBoom, ok bool) {
	defer func() {
		e := recover()
		if e == nil {
			return
		}
		b, isBoom := e.(ivBoom)
		boom = b
		ok = isBoom && b.p == p
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
	ivInvoke(f, p)
	return "", nil, ivBoom{}, false
}

func ivName(f any) string {
	fn := runtime.FuncForPC(reflect.ValueOf(f).Pointer())
	if fn == nil {
		return ""
	}
	return fn.Name()
}

func ivFrameNamed(frames []runtime.Frame, symbol string) (runtime.Frame, bool) {
	for _, f := range frames {
		if f.Function == symbol {
			return f, true
		}
	}
	return runtime.Frame{}, false
}

func main() {
	p := &ivPayload{id: 731, name: "iface-value"}
	p.next = &ivPayload{id: 732, name: "iface-value-next"}
	gcStress()

	// 1) 值接收者的四类符号：接口方法值/接口方法表达式/具体方法值/具体方法表达式。
	w1 := ivWorker{id: 1, tags: "v1"}
	var i ivIface = w1
	mv := i.Do
	ck(ivName(mv) == "main.ivIface.Do-fm", "value-receiver interface method value uses -fm wrapper")
	ck(ivName(ivIface.Do) == "main.ivIface.Do", "interface method expression keeps plain symbol")
	ck(ivName(w1.Do) == "main.ivWorker.Do-fm", "value-receiver concrete method value uses -fm wrapper")
	ck(ivName(ivWorker.Do) == "main.ivWorker.Do", "value-receiver concrete method expression keeps plain symbol")

	// 2) 接口保存值拷贝：改原变量、重绑接口都不影响已绑定的方法值。
	w1.id = 9
	i = ivWorker{id: 2, tags: "v2"}
	stack, frames, boom, ok := ivRun(mv, p)
	ck(ok, "value-receiver interface method value recovered payload")
	ck(boom.worker == 1, "interface method value captured the value assigned at bind time")
	ck(boom.tags == "v1", "interface method value captured the tags snapshot")
	ck(strings.Contains(stack, "main.ivWorker.Do("), "traceback has concrete value-receiver method")
	ck(strings.Contains(stack, "main.ivInvoke("), "traceback has indirect invoker")
	ck(strings.Contains(stack, "goroutine "), "traceback has goroutine header")
	ck(lineRe.MatchString(stack), "traceback has main.go:line")
	f, found := ivFrameNamed(frames, "main.ivWorker.Do")
	ck(found, "frames contain concrete value-receiver method")
	ck(strings.HasSuffix(f.File, "main.go") && f.Line > 0, "concrete method frame has file and line")
	ck(runtime.FuncForPC(f.Entry).Name() == f.Function, "concrete method entry resolves to same symbol")

	// 3) 接口方法表达式带显式接收者：调用当前接口中的值（id=2），而不是旧绑定。
	stack2, frames2, boom2, ok2 := ivRun(func(q *ivPayload) { ivIface.Do(i, q) }, p)
	ck(ok2, "interface method expression recovered payload")
	ck(boom2.worker == 2, "interface method expression uses the current dynamic value")
	ck(boom2.tags == "v2", "interface method expression sees reassigned tags")
	ck(strings.Contains(stack2, "main.ivWorker.Do("), "method-expression traceback has concrete method")
	_, found2 := ivFrameNamed(frames2, "main.ivWorker.Do")
	ck(found2, "method-expression frames contain concrete method")
	ck(lineRe.MatchString(stack2), "method-expression traceback has main.go:line")

	// 4) 方法值存入结构体字段后仍落到真实方法。
	holder := struct {
		fn func(*ivPayload)
	}{fn: mv}
	stack3, _, boom3, ok3 := ivRun(holder.fn, p)
	ck(ok3 && boom3.worker == 1, "struct-field method value keeps bind-time receiver")
	ck(strings.Contains(stack3, "main.ivWorker.Do("), "struct-field method value traceback has concrete method")

	// 5) GC 后接口值、方法值与 payload 链仍有效。
	gcStress()
	ck(p.id == 731 && p.name == "iface-value", "iface payload after GC")
	ck(p.next != nil && p.next.id == 732 && p.next.name == "iface-value-next", "iface payload chain after GC")
	ck(mv != nil && holder.fn != nil && i != nil, "interface method values survive GC")
	_, _, boom4, ok4 := ivRun(holder.fn, p)
	ck(ok4 && boom4.worker == 1, "struct-field method value still calls original receiver after GC")
	finish("l14_iface_value_receiver_method_value")
}
