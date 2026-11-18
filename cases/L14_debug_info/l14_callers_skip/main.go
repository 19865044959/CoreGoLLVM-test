// L14: runtime.Callers skip 语义。skip=0 指向 runtime.Callers 自身，skip=1 指向
// 直接调用者，逐层递增；越界 skip 返回 0 帧。每层都必须是真实符号且带 main.go:行号。
package main

import (
	"fmt"
	"os"
	"runtime"
	"strings"
	"sync/atomic"
)

type skipPayload struct {
	id   int64
	name string
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

// captureSkip 在自身帧内调用 runtime.Callers(skip, ...)，因此：
// skip=0 -> runtime.Callers；skip=1 -> captureSkip；skip=2 -> levelC。
//
//go:noinline
func captureSkip(skip int) []runtime.Frame {
	pcs := make([]uintptr, 32)
	n := runtime.Callers(skip, pcs)
	if n == 0 {
		return nil
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
	return out
}

//go:noinline
func levelC(skip int) []runtime.Frame { return captureSkip(skip) }

//go:noinline
func levelB(skip int) []runtime.Frame { return levelC(skip) }

//go:noinline
func levelA(skip int) []runtime.Frame { return levelB(skip) }

func firstFunc(frames []runtime.Frame) string {
	if len(frames) == 0 {
		return ""
	}
	return frames[0].Function
}

func main() {
	p := &skipPayload{id: 61, name: "callers-skip"}
	gcStress()

	zero := levelA(0)
	one := levelA(1)
	two := levelA(2)
	three := levelA(3)
	four := levelA(4)
	beyond := levelA(1000)

	ck(firstFunc(zero) == "runtime.Callers", "skip=0 identifies runtime.Callers itself")
	ck(firstFunc(one) == "main.captureSkip", "skip=1 identifies direct caller")
	ck(firstFunc(two) == "main.levelC", "skip=2 skips one more frame")
	ck(firstFunc(three) == "main.levelB", "skip=3 skips two more frames")
	ck(firstFunc(four) == "main.levelA", "skip=4 reaches outermost chain frame")
	ck(len(beyond) == 0, "skip past stack returns zero frames")

	ck(len(two) >= 2 && two[0].Function == "main.levelC" && two[1].Function == "main.levelB", "skip=2 frame order inner to outer")
	ck(len(four) >= 2 && four[0].Function == "main.levelA" && four[1].Function == "main.main", "skip=4 frame order reaches main")

	for _, f := range two {
		if !strings.HasPrefix(f.Function, "main.") {
			continue // 调用链尾部是 runtime.main/runtime.goexit，不属于 main.go
		}
		ck(strings.HasSuffix(f.File, "main.go"), "skip=2 frame "+f.Function+" has main.go file")
		ck(f.Line > 0, "skip=2 frame "+f.Function+" has positive line")
		ck(runtime.FuncForPC(f.Entry).Name() == f.Function, "skip=2 frame "+f.Function+" resolves to real symbol")
	}

	// skip=0 的首帧属于 runtime，文件/行号仍必须可解析。
	ck(len(zero) > 0 && zero[0].Line > 0, "runtime.Callers frame has positive line")

	gcStress()
	ck(p.id == 61 && p.name == "callers-skip", "payload after GC")
	finish("l14_callers_skip")
}
