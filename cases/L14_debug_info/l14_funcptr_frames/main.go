// L14: 函数指针调用帧。普通函数值、函数表、结构体字段与方法值经间接调用后
// panic，traceback 必须给出目标函数的真实符号而不是占位帧。
// TORTURE_ROUNDS 放大间接调用轮数。
package main

import (
	"fmt"
	"os"
	"reflect"
	"regexp"
	"runtime"
	"runtime/debug"
	"strconv"
	"strings"
	"sync/atomic"
)

type fpPayload struct {
	id   int64
	name string
	next *fpPayload
}

type fpWorker struct {
	p   *fpPayload
	pid int64
}

type fpHolder struct {
	fn func(*fpPayload)
	p  *fpPayload
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

func tortureRounds(def int) int {
	s := os.Getenv("TORTURE_ROUNDS")
	if s == "" {
		return def
	}
	n, err := strconv.Atoi(s)
	if err != nil || n <= 0 {
		return def
	}
	if n > 16 {
		n = 16
	}
	return n
}

//go:noinline
func fpLeafA(p *fpPayload) { panic(p) }

//go:noinline
func fpLeafB(p *fpPayload) { panic(p) }

//go:noinline
func fpInvoke(f func(*fpPayload), p *fpPayload) { f(p) }

//go:noinline
func fpRun(f func(*fpPayload), p *fpPayload) (stack string, ok bool) {
	defer func() {
		if e := recover(); e != nil {
			q, is := e.(*fpPayload)
			ok = is && q == p
			stack = string(debug.Stack())
		}
	}()
	fpInvoke(f, p)
	return "", false
}

//go:noinline
func (w *fpWorker) IndirectMethod(_ *fpPayload) { panic(w.p) }

func funcName(f any) string {
	return runtime.FuncForPC(reflect.ValueOf(f).Pointer()).Name()
}

func checkStack(prefix, stack, symbol string) {
	ck(strings.Contains(stack, symbol+"("), prefix+" traceback has "+symbol+" symbol")
	ck(strings.Contains(stack, "main.fpInvoke("), prefix+" traceback has indirect caller symbol")
	ck(strings.Contains(stack, "main.fpRun("), prefix+" traceback has recovering symbol")
	ck(strings.Contains(stack, "goroutine "), prefix+" traceback has goroutine header")
	ck(lineRe.MatchString(stack), prefix+" traceback has main.go:line")
}

func runRound(round int) {
	p := &fpPayload{id: int64(round), name: "funcptr"}
	p.next = &fpPayload{id: int64(round) + 1, name: "next"}

	// 1) 普通函数值。
	fn := fpLeafA
	ck(funcName(fn) == "main.fpLeafA", "func value resolves to real symbol")
	stack, ok := fpRun(fn, p)
	ck(ok, "func value payload identity")
	checkStack("func-value", stack, "main.fpLeafA")

	// 2) 函数表下标间接调用。
	table := []func(*fpPayload){fpLeafA, fpLeafB}
	stack, ok = fpRun(table[1], p)
	ck(ok, "func table payload identity")
	checkStack("func-table", stack, "main.fpLeafB")
	ck(!strings.Contains(stack, "main.fpLeafA("), "func table selects only the indexed target")

	// 3) 结构体字段中的函数指针。
	holder := &fpHolder{fn: fpLeafA, p: p}
	stack, ok = fpRun(holder.fn, holder.p)
	ck(ok, "struct field func payload identity")
	checkStack("struct-field", stack, "main.fpLeafA")
	ck(holder.p == p && holder.p.next != nil, "struct field payload after indirect call")

	// 4) 方法值间接调用。
	w := &fpWorker{p: p, pid: int64(round)}
	mv := w.IndirectMethod
	stack, ok = fpRun(mv, p)
	ck(ok, "method value payload identity")
	ck(strings.Contains(stack, "main.(*fpWorker).IndirectMethod("), "method value traceback has real method symbol")
	ck(strings.Contains(stack, "main.fpInvoke("), "method value traceback has indirect caller symbol")
	ck(lineRe.MatchString(stack), "method value traceback has main.go:line")

	gcStress()
	ck(w.p != nil && w.p.id == int64(round) && w.p.next != nil && w.p.next.id == int64(round)+1,
		"method receiver payload after GC")
}

func main() {
	rounds := tortureRounds(1)
	for r := 1; r <= rounds; r++ {
		runRound(r)
	}
	finish("l14_funcptr_frames")
}
