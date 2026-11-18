// L14: 内联帧的行号归属。ilBoom→ilMid→ilHostA→ilHostC 是单行调用的可内联链，
// panic traceback 必须把每个逻辑帧展开到各自源码行：panic 行 < mid 调用行 <
// host 调用行，且同一内联函数经不同调用点进入时行号随调用点变化、panic 行不变。
package main

import (
	"fmt"
	"os"
	"regexp"
	"runtime"
	"runtime/debug"
	"strings"
	"sync/atomic"
)

type ilPayload struct {
	id   int64
	name string
	next *ilPayload
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

// 以下四个函数都只有一次调用，构造成可内联的调用链。
func ilBoom(p *ilPayload) { panic(p) }

func ilMid(p *ilPayload) { ilBoom(p) }

func ilHostA(p *ilPayload) { ilMid(p) }

func ilHostC(p *ilPayload) { ilHostA(p) }

func ilHostB(p *ilPayload) {
	if p.id < 0 { // 阻止完全退化为单表达式，但不影响内联
		return
	}
	ilMid(p)
}

//go:noinline
func ilRun(p *ilPayload, kind int) (stack string, frames []runtime.Frame, ok bool) {
	defer func() {
		e := recover()
		if e == nil {
			return
		}
		q, isNode := e.(*ilPayload)
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
	switch kind {
	case 0:
		ilHostA(p)
	case 1:
		ilHostC(p)
	default:
		ilHostB(p)
	}
	return "", nil, false
}

func ilFrame(frames []runtime.Frame, symbol string) (runtime.Frame, int, bool) {
	for i, f := range frames {
		if f.Function == symbol {
			return f, i, true
		}
	}
	return runtime.Frame{}, -1, false
}

func ilHasSymbol(frames []runtime.Frame, symbol string) bool {
	for _, f := range frames {
		if f.Function == symbol {
			return true
		}
	}
	return false
}

func ilCheck(tag, stack string, frames []runtime.Frame, symbols []string) {
	prev := -1
	for _, sym := range symbols {
		f, idx, found := ilFrame(frames, sym)
		ck(found, tag+" frames contain "+sym)
		ck(idx > prev, tag+" frame order inner-to-outer at "+sym)
		prev = idx
		ck(strings.HasSuffix(f.File, "main.go"), tag+" frame "+sym+" has main.go file")
		ck(f.Line > 0, tag+" frame "+sym+" has positive line")
		fn := runtime.FuncForPC(f.Entry)
		ck(fn != nil && fn.Name() != "", tag+" frame "+sym+" has resolvable entry")
		ck(ilHasSymbol(frames, fn.Name()), tag+" inlined frame shares a real host symbol")
		ck(strings.Contains(stack, sym+"(...)"), tag+" traceback marks "+sym+" as inlined")
	}
	ck(lineRe.MatchString(stack), tag+" traceback has main.go:line")
	ck(strings.Contains(stack, "goroutine "), tag+" traceback has goroutine header")
}

func main() {
	p := &ilPayload{id: 521, name: "inline-lines"}
	p.next = &ilPayload{id: 522, name: "inline-next"}
	gcStress()

	// 1) 四层内联链：行号按源码顺序递增。
	sA, fA, okA := ilRun(p, 0)
	ck(okA, "single-host inline chain recovered payload")
	ilCheck("hostA", sA, fA, []string{"main.ilBoom", "main.ilMid", "main.ilHostA"})
	boomA, _, _ := ilFrame(fA, "main.ilBoom")
	midA, _, _ := ilFrame(fA, "main.ilMid")
	hostA, _, _ := ilFrame(fA, "main.ilHostA")
	ck(boomA.Line < midA.Line && midA.Line < hostA.Line,
		"inline frame lines increase from panic to caller")

	sC, fC, okC := ilRun(p, 1)
	ck(okC, "nested inline chain recovered payload")
	ilCheck("hostC", sC, fC, []string{"main.ilBoom", "main.ilMid", "main.ilHostA", "main.ilHostC"})
	boomC, _, _ := ilFrame(fC, "main.ilBoom")
	midC, _, _ := ilFrame(fC, "main.ilMid")
	hostAC, _, _ := ilFrame(fC, "main.ilHostA")
	hostC, _, _ := ilFrame(fC, "main.ilHostC")
	ck(boomC.Line < midC.Line && midC.Line < hostAC.Line && hostAC.Line < hostC.Line,
		"nested inline frame lines follow source order")
	ck(boomA.Line == boomC.Line && midA.Line == midC.Line,
		"same inlined functions keep their own source lines across call chains")

	// 2) 同一 ilMid 从另一宿主调用：ilMid 行号固定，宿主调用行随调用点变化。
	sB, fB, okB := ilRun(p, 2)
	ck(okB, "alternate host inline chain recovered payload")
	ilCheck("hostB", sB, fB, []string{"main.ilBoom", "main.ilMid", "main.ilHostB"})
	boomB, _, _ := ilFrame(fB, "main.ilBoom")
	midB, _, _ := ilFrame(fB, "main.ilMid")
	hostB, _, _ := ilFrame(fB, "main.ilHostB")
	ck(boomB.Line == boomA.Line, "panic line is identical for alternate host")
	ck(midB.Line == midA.Line, "ilMid call line is identical for alternate host")
	ck(hostB.Line != hostA.Line, "host call line changes with call site")
	ck(hostB.Line > midB.Line, "alternate host line follows its callee line")

	// 3) 每个内联符号只展开一次，traceback 不丢帧也不重复。
	for _, sym := range []string{"main.ilBoom", "main.ilMid", "main.ilHostA", "main.ilHostB"} {
		ck(strings.Count(sC, sym+"(") <= 1, "no duplicate inline frame "+sym)
	}
	ck(strings.Count(sA, "main.ilBoom(") == 1, "hostA traceback has exactly one boom frame")
	ck(strings.Count(sC, "main.ilHostA(") == 1, "hostC traceback has exactly one hostA frame")

	gcStress()
	ck(p.id == 521 && p.name == "inline-lines", "payload after GC")
	ck(p.next != nil && p.next.id == 522 && p.next.name == "inline-next", "payload chain after GC")
	finish("l14_inline_callline_attribution")
}
