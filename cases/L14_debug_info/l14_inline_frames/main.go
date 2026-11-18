// L14: 内联帧与多层调用帧在 traceback/CallersFrames 中的符号和行号。
package main

import (
	"fmt"
	"os"
	"runtime"
	"runtime/debug"
	"strings"
	"sync/atomic"
)

type node struct {
	v    int64
	name string
}

type wrap struct{ P *node }

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

// 小函数，可能被内联；无论是否内联，traceback 都应给出其符号。
func smallOne(p *node) int64 { panic(wrap{P: p}) }

func smallTwo(p *node) int64 { return smallOne(p) + 1 }

func smallThree(p *node) (ok bool) {
	defer func() {
		if e := recover(); e != nil {
			stack := string(debug.Stack())
			ok = strings.Contains(stack, "smallOne") &&
				strings.Contains(stack, "smallTwo") &&
				strings.Contains(stack, "smallThree") &&
				p != nil && p.v == 21
		}
	}()
	return smallTwo(p) == 0
}

//go:noinline
func tinyA() []runtime.Frame { return inlineFrames() }

//go:noinline
func tinyB() []runtime.Frame { return tinyA() }

//go:noinline
func tinyC() []runtime.Frame { return tinyB() }

//go:noinline
func inlineFrames() []runtime.Frame {
	pcs := make([]uintptr, 32)
	n := runtime.Callers(1, pcs)
	fr := runtime.CallersFrames(pcs[:n])
	out := make([]runtime.Frame, 0, 8)
	for {
		f, more := fr.Next()
		out = append(out, f)
		if !more {
			break
		}
	}
	return out
}

func main() {
	p := &node{v: 21, name: "inline"}
	ck(smallThree(p), "inline-chain panic stack symbols")
	gcStress()
	ck(p.v == 21, "panic payload pointer after GC")

	frames := tinyC()
	names := make([]string, 0, len(frames))
	lineOK, fileOK := false, false
	for _, f := range frames {
		names = append(names, f.Function)
		if f.Line > 0 {
			lineOK = true
		}
		if strings.HasSuffix(f.File, "main.go") {
			fileOK = true
		}
	}
	joined := strings.Join(names, ",")
	ck(strings.Contains(joined, "tinyC") && strings.Contains(joined, "tinyB") && strings.Contains(joined, "tinyA"), "multi-level frames present")
	ck(lineOK, "frames have line numbers")
	ck(fileOK, "frames have main.go")
	finish("l14_inline_frames")
}
