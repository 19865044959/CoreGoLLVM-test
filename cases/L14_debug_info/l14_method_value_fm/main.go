// L14: 方法值帧。绑定接收者的方法值在运行期是 -fm 包装函数（reflect 反查符号为
// main.(*T).M-fm / main.T.M-fm），经函数值间接调用后 panic，traceback 必须落到
// 真实方法符号（含接收者形态）并带 main.go:行号；方法表达式符号不带 -fm。
// 同时校验值接收者方法值在绑定时复制接收者、指针接收者方法值观察后续修改。
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

type mvPayload struct {
	id   int64
	name string
	next *mvPayload
}

type mvWorker struct {
	seq int64
	p   *mvPayload
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
func (w *mvWorker) Handle(p *mvPayload) { panic(p) }

//go:noinline
func (w mvWorker) Snapshot() int64 { return w.seq }

//go:noinline
func (w *mvWorker) Current() int64 { return w.seq }

//go:noinline
func mvInvoke(f func(*mvPayload), p *mvPayload) { f(p) }

//go:noinline
func mvRun(f func(*mvPayload), p *mvPayload) (stack string, ok bool) {
	defer func() {
		if e := recover(); e != nil {
			q, is := e.(*mvPayload)
			ok = is && q == p
			stack = string(debug.Stack())
		}
	}()
	mvInvoke(f, p)
	return "", false
}

func mvName(f any) string {
	return runtime.FuncForPC(reflect.ValueOf(f).Pointer()).Name()
}

func main() {
	p := &mvPayload{id: 101, name: "method-value"}
	p.next = &mvPayload{id: 102, name: "next"}
	w := &mvWorker{seq: 7, p: p}
	gcStress()

	// 1) 方法值：绑定接收者，符号带 -fm 包装后缀。
	mv := w.Handle
	vv := w.Snapshot
	ck(mvName(mv) == "main.(*mvWorker).Handle-fm", "pointer receiver method value resolves to -fm symbol")
	ck(mvName(vv) == "main.mvWorker.Snapshot-fm", "value receiver method value resolves to -fm symbol")

	// 2) 方法表达式：未绑定接收者，符号不带 -fm。
	exprPtr := (*mvWorker).Handle
	exprVal := mvWorker.Snapshot
	ck(mvName(exprPtr) == "main.(*mvWorker).Handle", "method expression keeps plain method symbol")
	ck(mvName(exprVal) == "main.mvWorker.Snapshot", "value method expression keeps plain method symbol")

	// 3) 绑定语义：值接收者方法值在绑定时复制接收者；指针接收者方法值看到后续修改。
	cur := w.Current
	w.seq = 23
	ck(vv() == 7, "value receiver method value captured receiver copy at bind time")
	ck(cur() == 23, "pointer receiver method value observes receiver state at call time")
	w.seq = 7

	// 4) 经方法值间接调用 panic：traceback 落到真实方法符号与 main.go 行号。
	stack, ok := mvRun(mv, p)
	ck(ok, "method value recovered payload identity")
	ck(strings.Contains(stack, "main.(*mvWorker).Handle("), "traceback has bound method symbol")
	ck(strings.Contains(stack, "main.mvInvoke("), "traceback has indirect invoker symbol")
	ck(strings.Contains(stack, "main.mvRun("), "traceback has recovering symbol")
	ck(strings.Contains(stack, "goroutine "), "traceback has goroutine header")
	ck(lineRe.MatchString(stack), "traceback has main.go:line for method frame")

	// 5) 经方法表达式调用 panic：仍落到真实方法符号。
	exprStack, exprOK := mvRun(func(q *mvPayload) { exprPtr(w, q) }, p)
	ck(exprOK, "method expression recovered payload identity")
	ck(strings.Contains(exprStack, "main.(*mvWorker).Handle("), "method expression traceback has method symbol")
	ck(lineRe.MatchString(exprStack), "method expression traceback has main.go:line")

	gcStress()
	ck(w.p != nil && w.p.id == 101 && w.p.name == "method-value", "method receiver payload after GC")
	ck(w.p.next != nil && w.p.next.id == 102 && w.p.next.name == "next", "method receiver payload chain after GC")
	ck(w.seq == 7 && vv() == 7, "bound value receiver copy survives GC")
	finish("l14_method_value_fm")
}
