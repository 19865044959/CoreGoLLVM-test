// L14: Callers skip 穿越闭包与方法值到 goexit 的边界。子 goroutine 入口 skGo 调用
// skEntry；skEntry 内定义闭包 inner，inner 通过方法值 mv 调用 (*skWorker).leaf，
// 最终在 skCapture 中执行 runtime.Callers。skip=0..6 必须依次给出
// runtime.Callers / skCapture / (*skWorker).leaf / skEntry.func1 / skEntry / skGo /
// runtime.goexit；skip=7 起返回 0 帧。单槽 PC 缓冲与两次 goroutine 运行结果一致。
package main

import (
	"fmt"
	"os"
	"regexp"
	"runtime"
	"strings"
	"sync/atomic"
)

type skPayload struct {
	id   int64
	name string
	next *skPayload
}

type skResult struct {
	frames [][]runtime.Frame
	one    []runtime.Frame
	ok     bool
}

type skWorker struct {
	id int64
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

func skExpand(pcs []uintptr) []runtime.Frame {
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

//go:noinline
func skCapture(skip int) (full []runtime.Frame, one []runtime.Frame) {
	pcs := make([]uintptr, 32)
	n := runtime.Callers(skip, pcs)
	full = skExpand(pcs[:n])

	onePC := make([]uintptr, 1)
	oneN := runtime.Callers(skip, onePC)
	one = skExpand(onePC[:oneN])
	return full, one
}

//go:noinline
func (w *skWorker) leaf(skip int) ([]runtime.Frame, []runtime.Frame) {
	return skCapture(skip)
}

//go:noinline
func skEntry() skResult {
	w := &skWorker{id: 1}
	mv := w.leaf
	inner := func(skip int) ([]runtime.Frame, []runtime.Frame) { return mv(skip) }
	var r skResult
	r.ok = true
	for skip := 0; skip <= 8; skip++ {
		full, one := inner(skip)
		r.frames = append(r.frames, full)
		if len(one) > 0 {
			r.one = append(r.one, one[0])
		}
	}
	if full, _ := inner(1000); len(full) != 0 {
		r.ok = false
	}
	return r
}

//go:noinline
func skGo(ch chan<- skResult) {
	ch <- skEntry()
}

func skFirst(frames []runtime.Frame) string {
	if len(frames) == 0 {
		return ""
	}
	return frames[0].Function
}

func skLast(frames []runtime.Frame) string {
	if len(frames) == 0 {
		return ""
	}
	return frames[len(frames)-1].Function
}

func skSeq(frames []runtime.Frame) string {
	var b strings.Builder
	for _, f := range frames {
		b.WriteString(f.Function)
		fmt.Fprintf(&b, "@%s:%d;", f.File, f.Line)
	}
	return b.String()
}

func main() {
	p := &skPayload{id: 661, name: "callers-closure-goexit"}
	p.next = &skPayload{id: 662, name: "callers-closure-next"}
	gcStress()

	ch := make(chan skResult, 1)
	go skGo(ch)
	r := <-ch
	ck(r.ok, "skip beyond goroutine stack returns zero frames")
	ck(len(r.frames) == 9, "captures for skip 0..8 plus beyond placeholder")

	want := []string{
		"runtime.Callers",
		"main.skCapture",
		"main.(*skWorker).leaf",
		"main.skEntry.func1",
		"main.skEntry",
		"main.skGo",
		"runtime.goexit",
	}
	for skip := 0; skip <= 6; skip++ {
		got := r.frames[skip]
		ck(skFirst(got) == want[skip], fmt.Sprintf("skip=%d first frame is %s", skip, want[skip]))
		ck(len(got) == 7-skip, fmt.Sprintf("skip=%d frame count reflects skipped logical frames", skip))
		ck(skLast(got) == "runtime.goexit", fmt.Sprintf("skip=%d capture ends at runtime.goexit", skip))
		ck(got[len(got)-1].Line > 0, fmt.Sprintf("skip=%d goexit frame has positive line", skip))
	}
	ck(len(r.frames[7]) == 0, "skip=7 past goexit returns zero frames")
	ck(len(r.frames[8]) == 0, "skip=8 past goexit returns zero frames")
	ck(len(r.one) == 7 && r.one[6].Function == "runtime.goexit",
		"single-slot PC buffer preserves skip semantics through closure and goexit")

	// 闭包/方法值链上的每个 main 帧都必须可解析，且不出现 -fm 逻辑帧。
	for _, f := range r.frames[3] {
		if f.Function == "runtime.goexit" {
			continue
		}
		ck(strings.HasPrefix(f.Function, "main."), "captured frame belongs to main: "+f.Function)
		ck(!strings.Contains(f.Function, "-fm"), "method value wrapper is not a logical frame")
		ck(strings.HasSuffix(f.File, "main.go"), "captured frame has main.go file: "+f.Function)
		ck(f.Line > 0, "captured frame has positive line: "+f.Function)
		fn := runtime.FuncForPC(f.Entry)
		ck(fn != nil && (fn.Name() == f.Function || strings.HasPrefix(f.Function, fn.Name()+".")),
			"captured frame entry resolves to its host or exact symbol: "+f.Function)
	}
	ck(lineRe.MatchString(skSeq(r.frames[3])), "captured closure frames carry main.go:line")

	// 同一入口在另一个 goroutine 上重复抓取，逻辑帧序列与行号完全一致。
	ch2 := make(chan skResult, 1)
	go skGo(ch2)
	r2 := <-ch2
	for skip := 0; skip <= 8; skip++ {
		ck(skSeq(r.frames[skip]) == skSeq(r2.frames[skip]),
			fmt.Sprintf("skip=%d closure capture is stable across goroutines", skip))
	}

	gcStress()
	ck(p.id == 661 && p.name == "callers-closure-goexit", "payload after GC")
	ck(p.next != nil && p.next.id == 662 && p.next.name == "callers-closure-next", "payload chain after GC")
	finish("l14_callers_skip_closure_goexit")
}
