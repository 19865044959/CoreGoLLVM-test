// L14: 接口分派递归触发栈增长时的 PC/行号边界。递归链 hit -> call -> iface.hit
// 全部经接口动态分派，每层 hit 持有 192 字节局部数组。叶子先抓 runtime.Callers
// PC 与 debug.Stack，返回过程中逐层校验局部数组。展开后的 PC 必须包含 depth 个
// hit/call 帧、call 帧行号等于 runtime.Caller(1) 记录的接口调用点，且返回、栈
// 收缩并显式 GC 后同一批 PC 展开结果逐项不变。TORTURE_ROUNDS 放大深度。
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

type sgResult struct {
	pcs     []uintptr
	line    int
	stack   string
	corrupt bool
}

type sgIface interface {
	hit(depth int, p *sgNode) sgResult
}

type sgImpl struct {
	id int64
}

var (
	failed   int32
	gcSink   [][]byte
	lineRe   = regexp.MustCompile(`main\.go:\d+`)
	sgGlobal sgIface
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
func sgCapture() []uintptr {
	pcs := make([]uintptr, 8192)
	n := runtime.Callers(3, pcs)
	return append([]uintptr(nil), pcs[:n]...)
}

//go:noinline
func (x *sgImpl) hit(depth int, p *sgNode) sgResult {
	var pad [192]byte
	for i := range pad {
		pad[i] = byte(depth)
	}
	if depth == 0 {
		_, _, line, _ := runtime.Caller(1)
		return sgResult{pcs: sgCapture(), line: line, stack: string(debug.Stack())}
	}
	res := x.call(depth-1, p)
	for i := range pad {
		if pad[i] != byte(depth) {
			res.corrupt = true
		}
	}
	return res
}

//go:noinline
func (x *sgImpl) call(depth int, p *sgNode) sgResult {
	return sgGlobal.hit(depth, p)
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

func sgSame(a, b []runtime.Frame) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Function != b[i].Function || a[i].File != b[i].File ||
			a[i].Line != b[i].Line || a[i].Entry != b[i].Entry {
			return false
		}
	}
	return true
}

func main() {
	p := &sgNode{id: 511, name: "iface-growth"}
	p.next = &sgNode{id: 512, name: "iface-growth-next"}
	sgGlobal = &sgImpl{id: 9}
	gcStress()

	rounds := envInt("TORTURE_ROUNDS", 2, 1, 16)
	for round := 0; round < rounds; round++ {
		depth := 256 + round*128
		if depth > 1024 {
			depth = 1024
		}
		res := sgGlobal.hit(depth, p)

		// 1) 栈复制后每层局部数组保持完整。
		ck(!res.corrupt, "per-frame locals survive interface-dispatched stack copies")
		ck(len(res.pcs) > depth*2, "captured PC slice covers every recursion level")

		// 2) 帧集合与行号归属。
		frames := sgExpand(res.pcs)
		hits, calls := 0, 0
		fileLinesOK := true
		for _, f := range frames {
			switch f.Function {
			case "main.(*sgImpl).hit":
				hits++
				ck(strings.HasSuffix(f.File, "main.go") && f.Line > 0, "hit frame has file and line")
				ck(runtime.FuncForPC(f.Entry).Name() == f.Function, "hit frame entry resolves")
			case "main.(*sgImpl).call":
				calls++
				ck(strings.HasSuffix(f.File, "main.go"), "call frame has main.go file")
				ck(f.Line == res.line, "interface call frame keeps call-site line")
				fn := runtime.FuncForPC(f.PC)
				file, line := fn.FileLine(f.PC)
				if line != f.Line || !strings.HasSuffix(file, "main.go") {
					fileLinesOK = false
				}
			}
		}
		ck(hits == depth, "one hit frame per recursion level")
		ck(calls == depth, "one interface call frame per recursion level")
		ck(res.line > 0, "runtime.Caller line inside leaf is positive")
		ck(fileLinesOK, "FuncForPC/FileLine agrees with expanded frames")
		if len(frames) > 0 {
			ck(frames[0].Function == "main.(*sgImpl).call", "captured chain starts at interface call helper")
		}

		// 3) 深栈 debug.Stack 也保留接口分派帧与行号（受 traceback 100 帧上限）。
		ck(strings.Contains(res.stack, "main.(*sgImpl).hit("), "debug.Stack has hit frames")
		ck(strings.Contains(res.stack, "main.(*sgImpl).call("), "debug.Stack has call frames")
		ck(strings.Contains(res.stack, "goroutine "), "debug.Stack has goroutine header")
		ck(lineRe.MatchString(res.stack), "debug.Stack has main.go:line")
		ck(strings.Count(res.stack, "main.(*sgImpl).hit(") >= 20, "deep debug.Stack keeps many hit frames")

		// 4) 返回、栈收缩并显式 GC 后，旧 PC 的展开结果逐项不变。
		runtime.GC()
		runtime.GC()
		after := sgExpand(res.pcs)
		ck(sgSame(frames, after), "captured PCs expand identically after unwind and GC")
		ck(len(after) == len(frames), "post-GC frame count is unchanged")
		ck(p.name == "iface-growth" && p.next != nil && p.next.name == "iface-growth-next",
			"payload chain per round")
		runtime.KeepAlive(p)
	}

	gcStress()
	ck(p.id == 511 && p.name == "iface-growth", "payload after explicit GC")
	ck(p.next != nil && p.next.id == 512 && p.next.name == "iface-growth-next", "payload chain after explicit GC")
	finish("l14_stack_growth_iface_pc")
}
