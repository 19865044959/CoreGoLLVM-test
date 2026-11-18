// L14: 自定义 Is/As 钩子的 wrapped panic。panic 值被两层 fmt.Errorf("%w") 包装，
// 最内层自定义 error 实现 Is/As 钩子；recover 后 errors.Is/errors.As 必须沿包装链
// 调用钩子并命中目标，traceback 必须保留 panic 路径上的真实符号与 main.go:行号。
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

type cwPayload struct {
	id   int64
	name string
	next *cwPayload
}

type cwSpecial struct {
	id   int64
	name string
}

func (s *cwSpecial) Error() string { return "cw-special:" + s.name }

var (
	failed   int32
	gcSink   [][]byte
	lineRe   = regexp.MustCompile(`main\.go:\d+`)
	cwTarget = errors.New("custom-is-target")
	cwOther  = errors.New("custom-is-other")
)

type cwErr struct {
	inner error
	p     *cwPayload
	sp    *cwSpecial
}

func (e *cwErr) Error() string { return "cw-core:" + e.p.name }

func (e *cwErr) Unwrap() error { return e.inner }

func (e *cwErr) Is(target error) bool {
	atomic.AddInt32(&cwIsCalls, 1)
	return target == cwTarget
}

func (e *cwErr) As(target any) bool {
	atomic.AddInt32(&cwAsCalls, 1)
	q, ok := target.(**cwSpecial)
	if !ok {
		return false
	}
	*q = e.sp
	return true
}

var cwIsCalls, cwAsCalls int32

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
func cwInner(p *cwPayload) error {
	return &cwErr{inner: errors.New("cw-leaf"), p: p, sp: &cwSpecial{id: p.id + 1, name: "special"}}
}

//go:noinline
func cwWrapOuter(p *cwPayload) error {
	return fmt.Errorf("cw-outer: %w", fmt.Errorf("cw-mid: %w", cwInner(p)))
}

//go:noinline
func cwPanicLeaf(p *cwPayload, e error) { panic(e) }

//go:noinline
func cwBoom(p *cwPayload) { cwPanicLeaf(p, cwWrapOuter(p)) }

//go:noinline
func cwRun(p *cwPayload) (stack string, frames []runtime.Frame, got error, ok bool) {
	defer func() {
		e := recover()
		if e == nil {
			return
		}
		got, ok = e.(error)
		if !ok {
			return
		}
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
	cwBoom(p)
	return "", nil, nil, false
}

func cwFrameNamed(frames []runtime.Frame, symbol string) (runtime.Frame, bool) {
	for _, f := range frames {
		if f.Function == symbol {
			return f, true
		}
	}
	return runtime.Frame{}, false
}

func main() {
	p := &cwPayload{id: 411, name: "wrapped-panic"}
	p.next = &cwPayload{id: 412, name: "wrapped-next"}
	gcStress()

	stack, frames, got, ok := cwRun(p)
	ck(ok, "recovered panic value implements error")
	ck(got != nil, "recovered error is non-nil")

	// 1) 包装链文本与 Unwrap 结构。
	ck(strings.Contains(got.Error(), "cw-outer"), "wrapped error text has outer layer")
	ck(strings.Contains(got.Error(), "cw-mid"), "wrapped error text has mid layer")
	ck(strings.Contains(got.Error(), "cw-core:wrapped-panic"), "innermost custom error text is reachable")

	u1 := errors.Unwrap(got)
	u2 := errors.Unwrap(u1)
	inner, isCW := u2.(*cwErr)
	ck(u1 != nil && u2 != nil, "two-level Unwrap reaches custom error")
	ck(isCW && inner.p == p && inner.sp != nil, "custom error retains payload and special pointer")
	ck(inner.sp.id == 412 && inner.sp.name == "special", "custom As target metadata is intact")

	// 2) 自定义 Is 钩子通过包装链被调用；未命中目标也照常调用。
	before := atomic.LoadInt32(&cwIsCalls)
	ck(errors.Is(got, cwTarget), "errors.Is follows wrappers and invokes custom Is")
	ck(atomic.LoadInt32(&cwIsCalls) > before, "custom Is hook was invoked")
	before = atomic.LoadInt32(&cwIsCalls)
	ck(!errors.Is(got, cwOther), "errors.Is misses unrelated target")
	ck(atomic.LoadInt32(&cwIsCalls) > before, "custom Is hook runs for unrelated target too")
	ck(!errors.Is(got, errors.New("fresh-error")), "errors.Is does not match fresh error")

	// 3) 自定义 As 钩子通过包装链被调用，取出的指针身份正确。
	beforeAs := atomic.LoadInt32(&cwAsCalls)
	var sp *cwSpecial
	ck(errors.As(got, &sp), "errors.As follows wrappers and invokes custom As")
	ck(atomic.LoadInt32(&cwAsCalls) > beforeAs, "custom As hook was invoked")
	ck(sp == inner.sp && sp.id == 412, "errors.As returns the original special pointer")

	// 4) traceback 保留 panic 路径真实符号与行号。
	ck(strings.Contains(stack, "main.cwPanicLeaf("), "traceback has panic leaf symbol")
	ck(strings.Contains(stack, "main.cwBoom("), "traceback has panic initiator symbol")
	ck(strings.Contains(stack, "main.cwRun("), "traceback has recovering symbol")
	ck(strings.Contains(stack, "goroutine "), "traceback has goroutine header")
	ck(lineRe.MatchString(stack), "traceback has main.go:line")
	for _, want := range []string{"main.cwPanicLeaf", "main.cwBoom", "main.cwRun"} {
		f, found := cwFrameNamed(frames, want)
		ck(found, "frames contain "+want)
		ck(strings.HasSuffix(f.File, "main.go") && f.Line > 0, want+" frame has file and line")
		ck(runtime.FuncForPC(f.Entry).Name() == f.Function, want+" entry resolves to same symbol")
	}

	// 5) GC 后 payload/special 指针与包装链仍然有效，钩子仍可再次命中。
	gcStress()
	ck(p.id == 411 && p.name == "wrapped-panic", "payload after GC")
	ck(p.next != nil && p.next.id == 412 && p.next.name == "wrapped-next", "payload chain after GC")
	var sp2 *cwSpecial
	ck(errors.As(got, &sp2) && sp2 == inner.sp, "errors.As still works after GC")
	ck(errors.Is(got, cwTarget), "errors.Is still works after GC")
	finish("l14_wrapped_panic_custom_hooks")
}
