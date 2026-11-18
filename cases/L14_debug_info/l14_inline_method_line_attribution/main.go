// L14: 内联方法行号归属的组合边界。mark() 内部用 runtime.Caller(1) 记录调用点，
// boom() 内 panic；两者都是小方法，会被内联进 noinline 宿主 ilHostA/ilHostB。
// 同一 boom 从两个宿主进入时：boom 的逻辑帧行号固定（方法体行），宿主帧行号
// 分别等于 mark 记录的调用点行；traceback 必须用 "boom(...)" 标记内联帧。
// 直接在 main 调用 mark 时又得到第三个不同的调用点行号。
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
	id       int64
	name     string
	next     *ilPayload
	markLine int
}

type ilProbe struct {
	id int64
	p  *ilPayload
}

// mark 与 boom 都保持极小，保证在 Go 1.24.1 默认内联策略下可被内联。
func (x *ilProbe) mark() { _, _, x.p.markLine, _ = runtime.Caller(1) }

func (x *ilProbe) boom() { panic(x.p) }

// 两个宿主都在同一源码行完成 mark+boom，因此 Caller(1) 与 traceback 行可精确比较。
//
//go:noinline
func ilHostA(x *ilProbe) { x.mark(); x.boom() }

//go:noinline
func ilHostB(x *ilProbe) {
	if x == nil {
		return
	}
	x.mark(); x.boom()
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
func ilRun(host func(*ilProbe), x *ilProbe) (stack string, frames []runtime.Frame, ok bool) {
	defer func() {
		e := recover()
		if e == nil {
			return
		}
		q, isPayload := e.(*ilPayload)
		ok = isPayload && q == x.p
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
	host(x)
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

func main() {
	p := &ilPayload{id: 631, name: "inline-method"}
	p.next = &ilPayload{id: 632, name: "inline-method-next"}
	x := &ilProbe{id: 7, p: p}
	gcStress()

	// 1) 第一个宿主：boom 被内联，宿主行号等于 mark 记录的调用点。
	stackA, framesA, okA := ilRun(ilHostA, x)
	lineA := p.markLine
	ck(okA, "hostA recovered payload")
	ck(lineA > 0, "inlined mark recorded hostA call-site line")
	ck(strings.Contains(stackA, "main.(*ilProbe).boom(...)"), "traceback marks boom as an inlined frame")
	ck(strings.Count(stackA, "main.(*ilProbe).boom(...)") == 1, "inlined boom frame appears once")
	ck(strings.Contains(stackA, "main.ilHostA("), "traceback has hostA frame")
	ck(strings.Contains(stackA, "goroutine "), "hostA traceback has goroutine header")
	ck(lineRe.MatchString(stackA), "hostA traceback has main.go:line")
	boomA, boomIdx, foundBoomA := ilFrame(framesA, "main.(*ilProbe).boom")
	hostA, hostIdx, foundHostA := ilFrame(framesA, "main.ilHostA")
	ck(foundBoomA, "frames contain inlined boom")
	ck(foundHostA, "frames contain hostA")
	ck(boomA.File != "" && strings.HasSuffix(boomA.File, "main.go") && boomA.Line > 0,
		"inlined boom frame has file and line")
	ck(hostA.File != "" && strings.HasSuffix(hostA.File, "main.go") && hostA.Line > 0,
		"hostA frame has file and line")
	ck(hostA.Line == lineA, "hostA frame line equals inlined Caller(1) call-site line")
	ck(boomA.Line < hostA.Line, "inlined boom line precedes host call line")
	ck(boomIdx > -1 && hostIdx > boomIdx, "frames order inlined boom before host")
	ck(runtime.FuncForPC(hostA.Entry).Name() == hostA.Function, "hostA entry resolves to same symbol")

	// 2) 第二个宿主：boom 行号固定，宿主行号随调用点变化。
	p.markLine = 0
	stackB, framesB, okB := ilRun(ilHostB, x)
	lineB := p.markLine
	ck(okB, "hostB recovered payload")
	ck(lineB > 0, "inlined mark recorded hostB call-site line")
	ck(lineB != lineA, "different host call sites produce different Caller lines")
	ck(strings.Contains(stackB, "main.(*ilProbe).boom(...)"), "hostB traceback marks inlined boom")
	ck(strings.Contains(stackB, "main.ilHostB("), "hostB traceback has hostB frame")
	boomB, _, foundBoomB := ilFrame(framesB, "main.(*ilProbe).boom")
	hostB, _, foundHostB := ilFrame(framesB, "main.ilHostB")
	ck(foundBoomB && foundHostB, "hostB frames contain boom and host")
	ck(boomB.Line == boomA.Line, "same inlined method keeps its own body line across hosts")
	ck(hostB.Line == lineB, "hostB frame line equals inlined Caller(1) call-site line")
	ck(hostB.Line != hostA.Line, "hostB frame line differs from hostA")

	// 3) 在第三个调用点直接调用 mark：行号再次变化，证明内联 Caller 归属到逻辑调用点。
	p.markLine = 0
	x.mark()
	lineMain := p.markLine
	ck(lineMain > 0, "direct mark call recorded a line")
	ck(lineMain != lineA && lineMain != lineB, "direct call site has its own Caller line")

	// 4) GC 后 payload 与行号信息仍有效。
	gcStress()
	ck(p.id == 631 && p.name == "inline-method", "inline payload after GC")
	ck(p.next != nil && p.next.id == 632 && p.next.name == "inline-method-next", "inline payload chain after GC")
	ck(p.markLine == lineMain, "mark line survives GC")
	finish("l14_inline_method_line_attribution")
}
