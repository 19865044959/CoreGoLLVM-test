// L14: wrapped error panic（multi-unwrap / errors.Join）。panic 携带由 errors.Join
// 合并、并包含自定义 Unwrap() []error 与 Unwrap() error 的复合错误。recover 后
// errors.Is 必须同时命中两条分支的 sentinel，errors.As 必须取出带 payload 的
// 自定义类型；debug.Stack 给出包装链上每一层的真实符号与 main.go:行号。
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

var (
	errAlpha = errors.New("alpha-sentinel")
	errBeta  = errors.New("beta-sentinel")
)

type jpPayload struct {
	id   int64
	name string
	next *jpPayload
}

// multiWrap 实现 Unwrap() []error（多分支展开）。
type multiWrap []error

func (m multiWrap) Error() string {
	parts := make([]string, 0, len(m))
	for _, e := range m {
		parts = append(parts, e.Error())
	}
	return strings.Join(parts, " | ")
}

func (m multiWrap) Unwrap() []error { return m }

// joinTyped 实现 Unwrap() error，并携带需要 errors.As 取出的 payload。
type joinTyped struct {
	code int
	node *jpPayload
	err  error
}

func (e *joinTyped) Error() string { return fmt.Sprintf("typed-%d: %v", e.code, e.err) }

func (e *joinTyped) Unwrap() error { return e.err }

//go:noinline
func joinLeaf(n *jpPayload) {
	joined := errors.Join(
		multiWrap{
			fmt.Errorf("alpha-wrap: %w", errAlpha),
			fmt.Errorf("beta-wrap: %w", errBeta),
		},
		&joinTyped{code: 77, node: n, err: fmt.Errorf("typed-wrap: %w", errBeta)},
	)
	panic(joined)
}

//go:noinline
func joinMid(n *jpPayload) { joinLeaf(n) }

//go:noinline
func joinTop(n *jpPayload) (stack string, ok bool, isA, isB bool, typedOK bool) {
	defer func() {
		if e := recover(); e != nil {
			err, isErr := e.(error)
			if !isErr {
				return
			}
			isA = errors.Is(err, errAlpha)
			isB = errors.Is(err, errBeta)
			var jt *joinTyped
			if errors.As(err, &jt) && jt != nil && jt.node == n && jt.code == 77 {
				typedOK = jt.node.next != nil && jt.node.next.id == n.id+1
			}
			ok = strings.Contains(err.Error(), "alpha-wrap") &&
				strings.Contains(err.Error(), "beta-wrap") &&
				strings.Contains(err.Error(), "typed-wrap")
			stack = string(debug.Stack())
		}
	}()
	joinMid(n)
	return "", false, false, false, false
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
	n := &jpPayload{id: 161, name: "join-error"}
	n.next = &jpPayload{id: 162, name: "next"}
	gcStress()

	stack, ok, isA, isB, typedOK := joinTop(n)
	ck(ok, "recovered joined error renders all wrap branches")
	ck(isA, "errors.Is finds alpha sentinel through joined branches")
	ck(isB, "errors.Is finds beta sentinel through joined branches")
	ck(typedOK, "errors.As extracts typed wrapper with payload")

	// 静态校验：Join + multi-unwrap + %w 的 Is 语义（不经过 panic）。
	static := errors.Join(
		multiWrap{fmt.Errorf("static-alpha: %w", errAlpha)},
		fmt.Errorf("static-beta: %w", errBeta),
	)
	ck(errors.Is(static, errAlpha), "joined multi-unwrap finds alpha sentinel")
	ck(errors.Is(static, errBeta), "joined multi-unwrap finds beta sentinel")
	ck(errors.Is(fmt.Errorf("outer: %w", static), errAlpha), "outer %w wrap around join keeps alpha sentinel")
	ck(!errors.Is(static, errors.New("alpha-sentinel")), "distinct error instance is not the sentinel")

	ck(strings.Contains(stack, "main.joinLeaf("), "traceback has wrapping leaf symbol")
	ck(strings.Contains(stack, "main.joinMid("), "traceback has wrapping middle symbol")
	ck(strings.Contains(stack, "main.joinTop("), "traceback has recovering symbol")
	ck(strings.Contains(stack, "goroutine "), "traceback has goroutine header")
	ck(len(lineRe.FindAllString(stack, -1)) >= 3, "traceback has main.go:line for wrap chain")

	gcStress()
	ck(n.id == 161 && n.name == "join-error", "joined error payload after GC")
	ck(n.next != nil && n.next.id == 162 && n.next.name == "next", "joined error payload chain after GC")
	finish("l14_join_error_panic")
}
