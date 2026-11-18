// L14: wrapped error 的 panic 栈。panic 携带多层 %w 包装 + 自定义 Unwrap 的 error，
// recover 后 errors.Is/errors.As 必须正确；debug.Stack 给出包装函数链的真实符号与
// main.go:行号；包装器里的 payload 指针在 GC 后有效。
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

var errSentinel = errors.New("sentinel-failure")

type wrapPayload struct {
	id   int64
	name string
	next *wrapPayload
}

type wrapContext struct {
	op   string
	node *wrapPayload
	err  error
}

func (w *wrapContext) Error() string { return w.op + ": " + w.err.Error() }

func (w *wrapContext) Unwrap() error { return w.err }

//go:noinline
func wrapLeaf(n *wrapPayload) {
	err := &wrapContext{
		op:   "leaf",
		node: n,
		err:  fmt.Errorf("stage2: %w", fmt.Errorf("stage1: %w", errSentinel)),
	}
	panic(err)
}

//go:noinline
func wrapMid(n *wrapPayload) { wrapLeaf(n) }

//go:noinline
func wrapTop(n *wrapPayload) (stack string, ok bool, isSentinel bool, nodeOK bool) {
	defer func() {
		if e := recover(); e != nil {
			if err, isErr := e.(error); isErr {
				isSentinel = errors.Is(err, errSentinel)
				var wc *wrapContext
				if errors.As(err, &wc) && wc.node != nil {
					nodeOK = wc.node == n && wc.node.next != nil && wc.node.next.id == n.id+1
				}
				ok = err.Error() == "leaf: stage2: stage1: sentinel-failure"
			}
			stack = string(debug.Stack())
		}
	}()
	wrapMid(n)
	return "", false, false, false
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

func main() {
	n := &wrapPayload{id: 70, name: "wrapped"}
	n.next = &wrapPayload{id: 71, name: "next"}
	gcStress()

	// 包装链本身的静态校验（不经过 panic）。
	static := fmt.Errorf("outer: %w", fmt.Errorf("inner: %w", errSentinel))
	ck(errors.Is(static, errSentinel), "fmt.Errorf %w chain unwraps to sentinel")

	stack, ok, isSentinel, nodeOK := wrapTop(n)
	ck(ok, "recovered error renders full wrap chain")
	ck(isSentinel, "errors.Is finds sentinel through panic value")
	ck(nodeOK, "errors.As extracts custom wrapper with payload")
	ck(strings.Contains(stack, "main.wrapLeaf("), "traceback has wrapping leaf symbol")
	ck(strings.Contains(stack, "main.wrapMid("), "traceback has wrapping middle symbol")
	ck(strings.Contains(stack, "main.wrapTop("), "traceback has recovering symbol")
	ck(strings.Contains(stack, "goroutine "), "traceback has goroutine header")
	ck(len(lineRe.FindAllString(stack, -1)) >= 3, "traceback has main.go:line for wrap chain")

	gcStress()
	ck(n.id == 70 && n.name == "wrapped" && n.next != nil && n.next.id == 71, "wrapped payload after GC")
	finish("l14_wrapped_error_panic")
}
