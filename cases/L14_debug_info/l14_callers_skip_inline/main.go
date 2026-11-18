// L14: Callers skip 与内联帧展开。runtime.Callers(skip) 按逻辑帧（含内联帧）逐层
// 跳过：skip=2 的首帧是内联进 main.skipInlineMid 的 main.skipInlineLeaf，
// skip=3 才到宿主 main.skipInlineMid。CallersFrames 展开内联帧后，两个逻辑帧
// 共享同一物理函数入口，但各自带独立的 main.go 行号。
package main

import (
	"fmt"
	"os"
	"runtime"
	"strings"
	"sync/atomic"
)

type siPayload struct {
	id   int64
	name string
}

type siResult struct {
	pcs    int
	frames []runtime.Frame
}

var (
	failed int32
	gcSink [][]byte
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

// skipCapture 是 runtime.Callers 的直接调用者。
//
//go:noinline
func skipCapture(skip int) siResult {
	pcs := make([]uintptr, 32)
	n := runtime.Callers(skip, pcs)
	if n == 0 {
		return siResult{}
	}
	fr := runtime.CallersFrames(pcs[:n])
	out := make([]runtime.Frame, 0, 8)
	for {
		f, more := fr.Next()
		out = append(out, f)
		if !more {
			break
		}
	}
	return siResult{pcs: n, frames: out}
}

// skipInlineLeaf 仅一行调用，预期被内联进 skipInlineMid。
func skipInlineLeaf(skip int) siResult { return skipCapture(skip) }

//go:noinline
func skipInlineMid(skip int) siResult { return skipInlineLeaf(skip) }

//go:noinline
func skipInlineOuter(skip int) siResult { return skipInlineMid(skip) }

func firstFunc(frames []runtime.Frame) string {
	if len(frames) == 0 {
		return ""
	}
	return frames[0].Function
}

func main() {
	p := &siPayload{id: 151, name: "callers-inline"}
	gcStress()

	zero := skipInlineOuter(0)
	one := skipInlineOuter(1)
	two := skipInlineOuter(2)
	three := skipInlineOuter(3)
	four := skipInlineOuter(4)
	beyond := skipInlineOuter(1000)

	ck(firstFunc(zero.frames) == "runtime.Callers", "skip=0 identifies runtime.Callers itself")
	ck(firstFunc(one.frames) == "main.skipCapture", "skip=1 identifies capture caller")
	ck(firstFunc(two.frames) == "main.skipInlineLeaf", "skip=2 reaches inlined leaf frame")
	ck(firstFunc(three.frames) == "main.skipInlineMid", "skip=3 reaches inlining host frame")
	ck(firstFunc(four.frames) == "main.skipInlineOuter", "skip=4 reaches outer frame")
	ck(beyond.pcs == 0 && len(beyond.frames) == 0, "skip past stack returns zero frames")

	// 内联展开证据：leaf 与 mid 是两个逻辑帧（不同 Function/Line），
	// 但共享同一物理函数入口（内联进 mid）。
	ck(len(two.frames) >= 2, "skip=2 has expanded inline frame group")
	if len(two.frames) >= 2 {
		leaf, mid := two.frames[0], two.frames[1]
		ck(leaf.Function == "main.skipInlineLeaf", "inline group first frame is leaf")
		ck(mid.Function == "main.skipInlineMid", "inline group second frame is host")
		ck(leaf.Entry == mid.Entry, "inlined leaf shares physical entry with host")
		ck(runtime.FuncForPC(leaf.Entry).Name() == "main.skipInlineMid", "inline group entry resolves to host symbol")
		ck(runtime.FuncForPC(leaf.PC).Name() == "main.skipInlineLeaf", "inline leaf PC resolves to leaf symbol")
		ck(runtime.FuncForPC(mid.PC).Name() == "main.skipInlineMid", "inline host PC resolves to host symbol")
		ck(leaf.Line > 0 && mid.Line > 0, "inline frames have positive line numbers")
		ck(leaf.Line != mid.Line, "inline frames have distinct body/call lines")
		ck(strings.HasSuffix(leaf.File, "main.go") && strings.HasSuffix(mid.File, "main.go"),
			"inline frames report main.go")
	}

	// skip 每次前进一个逻辑帧：skip=3 的首帧就是 skip=2 的第二帧。
	ck(three.pcs == two.pcs-1, "skip advances one logical frame per increment")
	ck(firstFunc(three.frames) == two.frames[1].Function, "skip=3 starts at host of skip=2 inline group")

	// 所有 main 帧：符号、文件、行号均可解析。
	for _, f := range two.frames {
		if !strings.HasPrefix(f.Function, "main.") {
			continue
		}
		ck(strings.HasSuffix(f.File, "main.go"), "frame "+f.Function+" reports main.go")
		ck(f.Line > 0, "frame "+f.Function+" has positive line")
		ck(runtime.FuncForPC(f.PC).Name() == f.Function, "frame "+f.Function+" resolves from PC")
	}

	gcStress()
	ck(p.id == 151 && p.name == "callers-inline", "payload after GC")
	ck(firstFunc(two.frames) == "main.skipInlineLeaf", "frame info stable after GC")
	finish("l14_callers_skip_inline")
}
