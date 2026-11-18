// L14: defer 中 panic 的多层链。三个 defer 按 LIFO 执行：先 recover 原始 panic
// 再重新 panic，接着另一个 defer 再次 panic 覆盖值，最后由最早注册的 defer
// recover 最终值。每个阶段抓取的 traceback 都必须带真实 defer 闭包/普通函数的
// 符号与 main.go:行号。另覆盖“普通函数作为 defer 直接 panic”的路径。
// TORTURE_ROUNDS 放大轮数。
package main

import (
	"fmt"
	"os"
	"regexp"
	"runtime"
	"runtime/debug"
	"strconv"
	"strings"
	"sync/atomic"
)

type dcPayload struct {
	id   int64
	name string
	next *dcPayload
}

type dcResult struct {
	origOK     bool
	orig       any
	final      any
	finalOK    bool
	innerStack string
	finalStack string
	order      []string
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
	if n > 8 {
		n = 8
	}
	return n
}

// panicOnCleanup 作为普通函数被 defer 直接调用，自身 panic。
//
//go:noinline
func panicOnCleanup(p *dcPayload) { panic(p) }

// deferChain 注册三个 defer（后注册先执行）：
// func3: recover p1 -> 抓栈 -> panic(p3)
// func2: panic(p2) 覆盖 panic 值
// func1: recover 最终值 p2 并抓栈
//
//go:noinline
func deferChain(p1, p2, p3 *dcPayload, out *dcResult) {
	out.order = out.order[:0]
	defer func() {
		out.order = append(out.order, "recover-last")
		out.final = recover()
		out.finalOK = out.final == p2
		out.finalStack = string(debug.Stack())
	}()
	defer func() {
		out.order = append(out.order, "panic-p2")
		panic(p2)
	}()
	defer func() {
		out.order = append(out.order, "recover-then-panic-p3")
		if e := recover(); e != nil {
			q, is := e.(*dcPayload)
			out.orig = e
			out.origOK = is && q == p1
			out.innerStack = string(debug.Stack())
			panic(p3)
		}
	}()
	panic(p1)
}

// deferOrdinary 只由普通函数作为 defer 触发 panic。
//
//go:noinline
func deferOrdinary(p *dcPayload, out *dcResult) {
	defer func() {
		out.final = recover()
		out.finalOK = out.final == p
		out.finalStack = string(debug.Stack())
	}()
	defer panicOnCleanup(p)
}

func runRound(round int) {
	p1 := &dcPayload{id: int64(100 + round*3), name: "first"}
	p2 := &dcPayload{id: int64(101 + round*3), name: "second"}
	p3 := &dcPayload{id: int64(102 + round*3), name: "third"}
	p1.next = p2
	p2.next = p3
	p3.next = p1

	out := &dcResult{}
	deferChain(p1, p2, p3, out)
	prefix := "round " + strconv.Itoa(round) + ": "

	ck(out.origOK, prefix+"first deferred recover sees original panic")
	ck(len(out.order) == 3 && out.order[0] == "recover-then-panic-p3" &&
		out.order[1] == "panic-p2" && out.order[2] == "recover-last",
		prefix+"defer LIFO order")
	final, isPayload := out.final.(*dcPayload)
	ck(isPayload && final == p2 && out.finalOK, prefix+"final panic in defer replaces earlier panic value")

	ck(strings.Contains(out.innerStack, "main.deferChain.func3("), prefix+"inner stack has recovering defer symbol")
	ck(strings.Contains(out.innerStack, "main.deferChain("), prefix+"inner stack has host function symbol")
	ck(strings.Contains(out.innerStack, "goroutine "), prefix+"inner stack has goroutine header")
	ck(lineRe.MatchString(out.innerStack), prefix+"inner stack has main.go:line")

	ck(strings.Contains(out.finalStack, "main.deferChain.func1("), prefix+"final stack has last defer symbol")
	ck(strings.Contains(out.finalStack, "main.deferChain.func2("), prefix+"final stack has panicking defer symbol")
	ck(strings.Contains(out.finalStack, "main.deferChain("), prefix+"final stack has host function symbol")
	ck(lineRe.MatchString(out.finalStack), prefix+"final stack has main.go:line")
	ck(len(lineRe.FindAllString(out.finalStack, -1)) >= 2, prefix+"final stack has multiple line infos")

	// 普通函数 defer 直接 panic。
	out2 := &dcResult{}
	deferOrdinary(p1, out2)
	ck(out2.finalOK, prefix+"ordinary deferred function panic recovered")
	ck(strings.Contains(out2.finalStack, "main.panicOnCleanup("), prefix+"ordinary defer stack has panicking function symbol")
	ck(strings.Contains(out2.finalStack, "main.deferOrdinary.func1("), prefix+"ordinary defer stack has recovering defer symbol")
	ck(strings.Contains(out2.finalStack, "main.deferOrdinary("), prefix+"ordinary defer stack has host function symbol")
	ck(lineRe.MatchString(out2.finalStack), prefix+"ordinary defer stack has main.go:line")

	gcStress()
	ck(p1.id == int64(100+round*3) && p1.next == p2, prefix+"defer payload chain after GC")
	ck(p2.next == p3 && p3.next == p1, prefix+"defer payload ring after GC")
}

func main() {
	rounds := tortureRounds(1)
	for r := 1; r <= rounds; r++ {
		runRound(r)
	}
	finish("l14_defer_chain_panic")
}
