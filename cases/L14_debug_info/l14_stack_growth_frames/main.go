// L14: 栈增长后的栈。新 goroutine 初始栈很小，深递归触发 morestack 栈复制后，
// 在被复制的栈上抓取的 debug.Stack 与 runtime.CallersFrames 必须保留全部递归帧、
// 真实符号与 per-frame main.go:行号；递归帧里持有的指针在 GC 后仍有效。
// TORTURE_ROUNDS 加大递归深度（每轮 +256 层，上限 2048）。
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

type sgPayload struct {
	id   int64
	name string
	next *sgPayload
}

type sgResult struct {
	depth  int
	stack  string
	frames []runtime.Frame
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

// sgCapture 在递归叶子上抓 debug.Stack 与 CallersFrames。
//
//go:noinline
func sgCapture(total int, p *sgPayload) (string, []runtime.Frame) {
	pcs := make([]uintptr, total+32)
	n := runtime.Callers(1, pcs)
	fr := runtime.CallersFrames(pcs[:n])
	out := make([]runtime.Frame, 0, total+8)
	for {
		f, more := fr.Next()
		out = append(out, f)
		if !more {
			break
		}
	}
	if p == nil || p.id < 0 || p.next == nil {
		return "", out
	}
	return string(debug.Stack()), out
}

// sgRecurse 每层持有 96 字节局部数组与 payload 指针，迫使 goroutine 栈增长/复制。
//
//go:noinline
func sgRecurse(total, depth int, p *sgPayload) (string, []runtime.Frame) {
	var buf [96]byte
	buf[depth%96] = byte(depth)
	if depth == 0 {
		stack, frames := sgCapture(total, p)
		runtime.KeepAlive(buf)
		return stack, frames
	}
	stack, frames := sgRecurse(total, depth-1, p)
	runtime.KeepAlive(buf)
	return stack, frames
}

//go:noinline
func sgWorker(depth int, p *sgPayload, out chan<- sgResult) {
	stack, frames := sgRecurse(depth, depth, p)
	out <- sgResult{depth: depth, stack: stack, frames: frames}
}

func runRound(round, depth int) {
	p := &sgPayload{id: int64(140 + round), name: "stack-growth"}
	p.next = &sgPayload{id: int64(141 + round), name: "next"}
	gcStress()

	out := make(chan sgResult, 1)
	go sgWorker(depth, p, out)
	res := <-out
	prefix := "round " + strconv.Itoa(round) + " depth " + strconv.Itoa(depth) + ": "

	ck(res.stack != "", prefix+"leaf captured stack")
	ck(len(res.stack) > 5000, prefix+"stack text confirms deep grown stack")
	recFrames := strings.Count(res.stack, "main.sgRecurse(")
	ck(recFrames >= 90, prefix+"debug.Stack keeps deep recursion frames")
	ck(len(lineRe.FindAllString(res.stack, -1)) >= 90, prefix+"debug.Stack keeps per-frame lines")
	ck(strings.Contains(res.stack, "main.sgCapture("), prefix+"stack has leaf capture symbol")
	ck(strings.Contains(res.stack, "main.sgWorker("), prefix+"stack has goroutine entry symbol")
	ck(strings.Contains(res.stack, "goroutine "), prefix+"stack has goroutine header")

	callerFrames := 0
	lineOK, fileOK, symOK := true, true, true
	for _, f := range res.frames {
		if f.Function != "main.sgRecurse" {
			continue
		}
		callerFrames++
		if f.Line <= 0 {
			lineOK = false
		}
		if !strings.HasSuffix(f.File, "main.go") {
			fileOK = false
		}
		if runtime.FuncForPC(f.Entry).Name() != f.Function {
			symOK = false
		}
	}
	ck(callerFrames >= depth-4, prefix+"CallersFrames keeps all recursion frames")
	ck(lineOK, prefix+"grown-stack frames have positive line numbers")
	ck(fileOK, prefix+"grown-stack frames report main.go")
	ck(symOK, prefix+"grown-stack frames resolve to real symbols")

	gcStress()
	ck(p.id == int64(140+round) && p.name == "stack-growth", prefix+"recursion payload after GC")
	ck(p.next != nil && p.next.id == int64(141+round), prefix+"recursion payload chain after GC")
}

func main() {
	rounds := tortureRounds(1)
	depth := 512
	for r := 1; r <= rounds; r++ {
		runRound(r, depth)
		if depth < 2048 {
			depth += 256
		}
	}
	finish("l14_stack_growth_frames")
}
