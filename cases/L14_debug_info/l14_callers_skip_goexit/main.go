// L14: goroutine 入口处的 Callers skip 语义。在子 goroutine 的固定 noinline 调用链上，
// skip 逐层外移，最后一帧必须是 runtime.goexit；skip 恰好越过 goexit 时返回 0 帧。
// 单槽 PC 缓冲也必须遵守同样的 skip 语义，skip 不会因缓冲截断而改变首帧。
package main

import (
	"fmt"
	"os"
	"regexp"
	"runtime"
	"strings"
	"sync/atomic"
)

type csPayload struct {
	id   int64
	name string
	next *csPayload
}

type csResult struct {
	frames [][]runtime.Frame
	one    []runtime.Frame
	stack  string
	ok     bool
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
func csCapture(skip int) ([]runtime.Frame, []runtime.Frame) {
	pcs := make([]uintptr, 32)
	n := runtime.Callers(skip, pcs)
	var full []runtime.Frame
	if n > 0 {
		it := runtime.CallersFrames(pcs[:n])
		for {
			f, more := it.Next()
			full = append(full, f)
			if !more {
				break
			}
		}
	}

	onePC := make([]uintptr, 1)
	oneN := runtime.Callers(skip, onePC)
	var one []runtime.Frame
	if oneN > 0 {
		it := runtime.CallersFrames(onePC[:oneN])
		f, _ := it.Next()
		one = append(one, f)
	}
	return full, one
}

//go:noinline
func csLeaf(skip int) ([]runtime.Frame, []runtime.Frame) { return csCapture(skip) }

//go:noinline
func csMid(skip int) ([]runtime.Frame, []runtime.Frame) { return csLeaf(skip) }

//go:noinline
func csTop(skip int) ([]runtime.Frame, []runtime.Frame) { return csMid(skip) }

//go:noinline
func csEntry(ch chan<- csResult) {
	var r csResult
	r.ok = true
	for skip := 0; skip <= 7; skip++ {
		full, one := csTop(skip)
		r.frames = append(r.frames, full)
		if len(one) > 0 {
			r.one = append(r.one, one[0])
		}
	}
	r.frames = append(r.frames, nil) // skip=1000 的占位
	if full, _ := csTop(1000); len(full) != 0 {
		r.ok = false
	}
	ch <- r
}

func csFirst(frames []runtime.Frame) string {
	if len(frames) == 0 {
		return ""
	}
	return frames[0].Function
}

func csLast(frames []runtime.Frame) string {
	if len(frames) == 0 {
		return ""
	}
	return frames[len(frames)-1].Function
}

func main() {
	p := &csPayload{id: 331, name: "callers-goexit"}
	p.next = &csPayload{id: 332, name: "goexit-next"}
	gcStress()

	ch := make(chan csResult, 1)
	go csEntry(ch)
	r := <-ch
	ck(r.ok, "skip beyond goroutine stack returns zero frames")
	ck(len(r.frames) == 9, "captures for skip 0..7 plus beyond placeholder")

	want := []string{
		"runtime.Callers",
		"main.csCapture",
		"main.csLeaf",
		"main.csMid",
		"main.csTop",
		"main.csEntry",
		"runtime.goexit",
	}
	for skip := 0; skip <= 6; skip++ {
		got := r.frames[skip]
		ck(csFirst(got) == want[skip], fmt.Sprintf("skip=%d first frame is %s", skip, want[skip]))
		ck(len(got) == 7-skip, fmt.Sprintf("skip=%d frame count reflects skipped levels", skip))
		ck(csLast(got) == "runtime.goexit", fmt.Sprintf("skip=%d capture ends at runtime.goexit", skip))
		ck(got[len(got)-1].Line > 0, fmt.Sprintf("skip=%d goexit frame has positive line", skip))
	}
	ck(len(r.frames[7]) == 0, "skip=7 past goexit returns zero frames")
	ck(len(r.frames[8]) == 0, "skip=1000 past goexit returns zero frames")
	ck(len(r.one) == 7 && r.one[6].Function == "runtime.goexit",
		"single-slot PC buffer preserves skip semantics through goexit")

	// 中间帧的真实符号、文件、行号均可解析；Entry 反查与帧名一致。
	for _, f := range r.frames[2] {
		if f.Function == "runtime.Callers" || f.Function == "runtime.gopanic" {
			continue
		}
		if f.Function == "runtime.goexit" {
			continue
		}
		ck(strings.HasPrefix(f.Function, "main."), "worker frame belongs to main package: "+f.Function)
		ck(strings.HasSuffix(f.File, "main.go"), "worker frame has main.go file: "+f.Function)
		ck(f.Line > 0, "worker frame has positive line: "+f.Function)
		ck(runtime.FuncForPC(f.Entry).Name() == f.Function, "worker frame entry resolves: "+f.Function)
	}
	ck(lineRe.MatchString(csJoin(r.frames[2])), "captured worker frames carry main.go:line")

	// 同一位置重复抓取，函数序列与行号一致。
	againCh := make(chan csResult, 1)
	go csEntry(againCh)
	r2 := <-againCh
	for skip := 0; skip <= 7; skip++ {
		ck(csSeq(r.frames[skip]) == csSeq(r2.frames[skip]), fmt.Sprintf("skip=%d capture is stable across goroutines", skip))
	}

	gcStress()
	ck(p.id == 331 && p.name == "callers-goexit", "payload after GC")
	ck(p.next != nil && p.next.id == 332 && p.next.name == "goexit-next", "payload chain after GC")
	finish("l14_callers_skip_goexit")
}

func csSeq(frames []runtime.Frame) string {
	var b strings.Builder
	for _, f := range frames {
		b.WriteString(f.Function)
		fmt.Fprintf(&b, "@%s:%d;", f.File, f.Line)
	}
	return b.String()
}

func csJoin(frames []runtime.Frame) string {
	var b strings.Builder
	for _, f := range frames {
		fmt.Fprintf(&b, "%s %s:%d\n", f.Function, f.File, f.Line)
	}
	return b.String()
}
