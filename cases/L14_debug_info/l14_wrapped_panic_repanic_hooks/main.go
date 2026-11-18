// L14: wrapped panic 在 defer 内二次 panic 的钩子边界。第一次 panic 携带自定义
// wpErr（sentinel1），内层 defer recover 后立刻 panic 一个 fmt.Errorf("%w") 包装的
// 新 wpErr（sentinel2，inner 链回 sentinel1）。最终 recover 必须拿到第二层错误，
// errors.Is 穿过包装链调用自定义 Is 钩子同时命中 sentinel1/sentinel2；errors.As
// 走自定义 As 取回第二层 special 指针。debug.Stack 必须保留两次 panic 的路径。
package main

import (
	"errors"
	"fmt"
	"os"
	"regexp"
	"runtime"
	"runtime/debug"
	"strings"
	"sync/atomic"
)

type wpPayload struct {
	id   int64
	name string
	next *wpPayload
}

type wpSpecial struct {
	id   int64
	name string
}

func (s *wpSpecial) Error() string { return "wp-special:" + s.name }

type wpErr struct {
	tag      string
	inner    error
	sentinel error
	special  *wpSpecial
}

func (e *wpErr) Error() string { return "wp:" + e.tag }

func (e *wpErr) Unwrap() error { return e.inner }

func (e *wpErr) Is(target error) bool {
	atomic.AddInt32(&wpIsCalls, 1)
	return target == e.sentinel
}

func (e *wpErr) As(target any) bool {
	atomic.AddInt32(&wpAsCalls, 1)
	p, ok := target.(**wpSpecial)
	if !ok {
		return false
	}
	*p = e.special
	return true
}

var (
	failed int32
	gcSink [][]byte
	lineRe = regexp.MustCompile(`main\.go:\d+`)

	wpSent1 = errors.New("wp-sentinel-1")
	wpSent2 = errors.New("wp-sentinel-2")
	wpSent3 = errors.New("wp-sentinel-3")

	wpIsCalls, wpAsCalls int32
	wpFirstErr           *wpErr
	wpSecondSpecial      *wpSpecial
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
func wpFirst(p *wpPayload) {
	e := &wpErr{tag: "first", sentinel: wpSent1, special: &wpSpecial{id: p.id + 1, name: "first-special"}}
	wpFirstErr = e
	panic(e)
}

//go:noinline
func wpSecond(p *wpPayload) {
	inner := &wpErr{tag: "first-inner", sentinel: wpSent1, special: wpFirstErr.special}
	sp := &wpSpecial{id: p.id + 2, name: "second-special"}
	wpSecondSpecial = sp
	mid := &wpErr{tag: "second", inner: inner, sentinel: wpSent2, special: sp}
	panic(fmt.Errorf("wp-second-wrap: %w", mid))
}

//go:noinline
func wpRun(p *wpPayload) (stack string, frames []runtime.Frame, final error, ok bool) {
	defer func() {
		e := recover()
		if e == nil {
			return
		}
		final, ok = e.(error)
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
	defer func() {
		if e := recover(); e != nil {
			wpSecond(p)
		}
	}()
	wpFirst(p)
	return "", nil, nil, false
}

func wpFrameNamed(frames []runtime.Frame, symbol string) (runtime.Frame, bool) {
	for _, f := range frames {
		if f.Function == symbol {
			return f, true
		}
	}
	return runtime.Frame{}, false
}

func main() {
	p := &wpPayload{id: 941, name: "repanic"}
	p.next = &wpPayload{id: 942, name: "repanic-next"}
	gcStress()

	stack, frames, final, ok := wpRun(p)
	ck(ok, "final re-panic value implements error")
	ck(final != nil, "final re-panic error is non-nil")
	ck(final != error(wpFirstErr), "final value is the second panic, not the first")
	ck(strings.Contains(final.Error(), "wp-second-wrap"), "final value keeps the second wrapper text")
	ck(strings.Contains(final.Error(), "wp:second"), "wrapped chain reaches second custom error")

	// 1) errors.Is 沿 fmt 包装链调用两层自定义 Is；sentinel1 只能经 inner 链命中。
	before := atomic.LoadInt32(&wpIsCalls)
	ck(errors.Is(final, wpSent2), "errors.Is hits second sentinel")
	ck(atomic.LoadInt32(&wpIsCalls) == before+1, "second sentinel hits one custom Is hook")
	before = atomic.LoadInt32(&wpIsCalls)
	ck(errors.Is(final, wpSent1), "errors.Is reaches first sentinel through inner chain")
	ck(atomic.LoadInt32(&wpIsCalls) == before+2, "first sentinel traverses both custom Is hooks")
	before = atomic.LoadInt32(&wpIsCalls)
	ck(!errors.Is(final, wpSent3), "errors.Is misses unrelated sentinel")
	ck(atomic.LoadInt32(&wpIsCalls) == before+2, "unrelated sentinel still traverses both hooks")

	// 2) errors.As 穿过 re-panic 链调用第二层自定义 As。
	beforeAs := atomic.LoadInt32(&wpAsCalls)
	var sp *wpSpecial
	ck(errors.As(final, &sp), "errors.As finds special through second custom As")
	ck(atomic.LoadInt32(&wpAsCalls) == beforeAs+1, "second custom As hook was invoked exactly once")
	ck(sp == wpSecondSpecial && sp.id == 943 && sp.name == "second-special",
		"errors.As returns the second panic's special pointer")

	// 3) traceback 同时保留第一次与第二次 panic 的路径。
	for _, want := range []string{
		"main.wpFirst(",
		"main.wpSecond(",
		"main.wpRun.func1(",
		"main.wpRun.func2(",
		"main.wpRun(",
	} {
		ck(strings.Contains(stack, want), "traceback has "+want)
	}
	ck(strings.Count(stack, "panic(") >= 2, "traceback records both panic frames")
	ck(strings.Contains(stack, "goroutine "), "traceback has goroutine header")
	ck(lineRe.MatchString(stack), "traceback has main.go:line")
	for _, sym := range []string{"main.wpFirst", "main.wpSecond", "main.wpRun"} {
		f, found := wpFrameNamed(frames, sym)
		ck(found, "frames contain "+sym)
		ck(strings.HasSuffix(f.File, "main.go") && f.Line > 0, sym+" frame has file and line")
		ck(runtime.FuncForPC(f.Entry).Name() == f.Function, sym+" entry resolves to same symbol")
	}
	ck(strings.Contains(stack, "main.wpRun.func1("), "traceback has outer recover closure")
	_, foundRepanic := wpFrameNamed(frames, "main.wpRun.func2")
	ck(foundRepanic, "frames contain repanic closure")

	// 4) GC 后错误链、钩子与 payload 仍有效。
	gcStress()
	ck(errors.Is(final, wpSent1), "errors.Is still reaches first sentinel after GC")
	var sp2 *wpSpecial
	ck(errors.As(final, &sp2) && sp2 == wpSecondSpecial, "errors.As still works after GC")
	ck(p.id == 941 && p.name == "repanic", "repanic payload after GC")
	ck(p.next != nil && p.next.id == 942 && p.next.name == "repanic-next", "repanic payload chain after GC")
	finish("l14_wrapped_panic_repanic_hooks")
}
