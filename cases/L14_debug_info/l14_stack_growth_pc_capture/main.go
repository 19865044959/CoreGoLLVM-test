// L14: 栈增长/收缩前后已捕获 PC 的调试信息稳定性。先在浅栈上捕获调用点 PC 并展开
// 为符号+行号；随后在深层 noinline 递归中触发多次栈复制，在叶子上抓 debug.Stack 与
// Callers 帧；返回并显式 GC 后，重新展开此前捕获的浅层/深层 PC，符号、文件、行号
// 必须完全不变。TORTURE_ROUNDS 放大轮数与递归深度。
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

type sgNode struct {
	id   int64
	name string
	next *sgNode
}

type sgSnap struct {
	fn   string
	file string
	line int
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

func envInt(key string, def, min, max int) int {
	s := os.Getenv(key)
	if s == "" {
		return def
	}
	v, err := strconv.Atoi(s)
	if err != nil || v < min {
		return def
	}
	if v > max {
		return max
	}
	return v
}

//go:noinline
func sgCapturePCs() []uintptr {
	pcs := make([]uintptr, 4096)
	n := runtime.Callers(2, pcs)
	return append([]uintptr(nil), pcs[:n]...)
}

//go:noinline
func sgShallowB() []uintptr { return sgCapturePCs() }

//go:noinline
func sgShallowA() []uintptr { return sgShallowB() }

// sgShallowPair 在同一源码行连续两次抓浅栈，用于验证 PC 展开稳定性。
//
//go:noinline
func sgShallowPair() ([]uintptr, []runtime.Frame, []runtime.Frame) {
	var first, second []uintptr
	for i := 0; i < 2; i++ {
		pcs := sgShallowA()
		if i == 0 {
			first = pcs
		} else {
			second = pcs
		}
	}
	return first, sgExpand(first), sgExpand(second)
}

//go:noinline
func sgGrow(depth int, p *sgNode) (pcs []uintptr, stack string, corrupt bool) {
	var buf [192]byte
	buf[depth&127] = byte(depth)
	if depth == 0 {
		if p != nil && p.id == 0 {
			p.id = 1
		}
		return sgCapturePCs(), string(debug.Stack()), false
	}
	pcs, stack, corrupt = sgGrow(depth-1, p)
	if buf[depth&127] != byte(depth) {
		corrupt = true
	}
	return pcs, stack, corrupt
}

func sgExpand(pcs []uintptr) []runtime.Frame {
	if len(pcs) == 0 {
		return nil
	}
	it := runtime.CallersFrames(pcs)
	var out []runtime.Frame
	for {
		f, more := it.Next()
		out = append(out, f)
		if !more {
			break
		}
	}
	return out
}

func sgSnaps(frames []runtime.Frame) []sgSnap {
	out := make([]sgSnap, 0, len(frames))
	for _, f := range frames {
		out = append(out, sgSnap{fn: f.Function, file: f.File, line: f.Line})
	}
	return out
}

func sgTrimAt(frames []runtime.Frame, fn string) []runtime.Frame {
	for i, f := range frames {
		if f.Function == fn {
			return frames[:i+1]
		}
	}
	return frames
}

func sgSame(a, b []sgSnap) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func sgFirst(frames []runtime.Frame) string {
	if len(frames) == 0 {
		return ""
	}
	return frames[0].Function
}

func main() {
	p := &sgNode{id: 0, name: "stack-growth"}
	p.next = &sgNode{id: 2, name: "stack-next"}
	gcStress()

	rounds := envInt("TORTURE_ROUNDS", 3, 1, 16)
	for round := 0; round < rounds; round++ {
		depth := 64 + round*64
		if depth > 1024 {
			depth = 1024
		}

		// 1) 浅栈 PC：首次展开与再次捕获一致。
		shallowPCs, shallowBefore, shallowAgain := sgShallowPair()
		ck(sgFirst(shallowBefore) == "main.sgShallowB", "shallow capture starts at sgShallowB")
		ck(sgSame(sgSnaps(shallowBefore), sgSnaps(shallowAgain)), "shallow stack frames are stable across captures")
		ck(len(shallowBefore) >= 3, "shallow capture includes caller chain")
		ck(lineRe.MatchString(sgJoin(shallowBefore)), "shallow frames carry main.go:line")

		// 2) 深递归触发栈复制，并在叶子上抓帧。
		deepPCs, deepStack, corrupt := sgGrow(depth, p)
		ck(!corrupt, "per-frame locals survive stack copies")
		deepBefore := sgExpand(deepPCs)
		ck(len(deepBefore) >= depth+1, "deep capture includes every recursion level")
		ck(sgFirst(deepBefore) == "main.sgGrow", "deep capture starts at sgGrow leaf")
		stackFrames := strings.Count(deepStack, "main.sgGrow(")
		minStackFrames := depth + 1
		if minStackFrames > 90 {
			minStackFrames = 90 // runtime.Stack 最多展开 100 帧，深栈走 Callers 帧校验
		}
		ck(stackFrames >= minStackFrames, "deep debug.Stack keeps recursion frames up to traceback cap")
		ck(stackFrames <= depth+1, "deep debug.Stack has no duplicate recursion frames")
		ck(lineRe.MatchString(deepStack), "deep debug.Stack carries main.go:line")
		ck(strings.Contains(deepStack, "goroutine "), "deep debug.Stack has goroutine header")
		growFrames := 0
		for _, f := range deepBefore {
			if f.Function != "main.sgGrow" {
				continue
			}
			growFrames++
			ck(strings.HasSuffix(f.File, "main.go"), "deep sgGrow frame has main.go file")
			ck(f.Line > 0, "deep sgGrow frame has positive line")
			ck(runtime.FuncForPC(f.Entry).Name() == f.Function, "deep sgGrow frame entry resolves")
		}
		ck(growFrames == depth+1, "all deep frames resolve to real sgGrow symbols")

		// 3) 展平栈（返回）并显式 GC；此前捕获的 PC 仍必须展开为完全相同的帧。
		runtime.GC()
		runtime.GC()
		shallowAfter := sgExpand(shallowPCs)
		deepAfter := sgExpand(deepPCs)
		ck(sgSame(sgSnaps(shallowBefore), sgSnaps(shallowAfter)), "shallow frames unchanged after stack growth and GC")
		ck(sgSame(sgSnaps(deepBefore), sgSnaps(deepAfter)), "deep frames unchanged after unwind and GC")
		ck(sgFirst(shallowAfter) == "main.sgShallowB", "post-GC shallow capture starts at sgShallowB")
		_, fresh, _ := sgShallowPair()
		ck(sgSame(sgSnaps(sgTrimAt(shallowBefore, "main.sgShallowPair")), sgSnaps(sgTrimAt(fresh, "main.sgShallowPair"))),
			"fresh post-GC call chain matches up to capture helper")

		// 4) 每轮都检查 payload 链；避免优化器把局部数组/指针消掉。
		ck(p.name == "stack-growth" && p.next != nil && p.next.name == "stack-next", "payload chain per round")
		runtime.KeepAlive(p)
	}

	gcStress()
	ck(p.id == 1 && p.name == "stack-growth", "payload after explicit GC")
	ck(p.next != nil && p.next.id == 2 && p.next.name == "stack-next", "payload chain after explicit GC")
	finish("l14_stack_growth_pc_capture")
}

func sgJoin(frames []runtime.Frame) string {
	var b strings.Builder
	for _, f := range frames {
		b.WriteString(f.Function)
		b.WriteString(" ")
		b.WriteString(f.File)
		fmt.Fprintf(&b, ":%d\n", f.Line)
	}
	return b.String()
}
