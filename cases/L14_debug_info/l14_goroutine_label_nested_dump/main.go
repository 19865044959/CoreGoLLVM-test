// L14: 嵌套 pprof 标签与崩溃栈的组合边界。一个 goroutine 在外层 pprof.Do 内再
// 嵌套一层 pprof.Do：内层覆盖同名键、保留外层其它键；该 goroutine 先在标签作用域
// 内 panic/recover 并抓 debug.Stack，再在标签作用域内阻塞。另一个只带外层标签的
// goroutine 与一个无标签 goroutine 同时阻塞。goroutine profile 只应出现两条精确
// 标签行；debug.Stack 不应合成标签行；崩溃栈仍须含真实闭包/panic 符号与行号。
package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"regexp"
	"runtime"
	"runtime/debug"
	"runtime/pprof"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type glPayload struct {
	id   int64
	name string
	next *glPayload
}

type glResult struct {
	stack  string
	frames []runtime.Frame
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
func glLeaf(p *glPayload) { panic(p) }

//go:noinline
func glPanicWork(p *glPayload) (res glResult) {
	defer func() {
		e := recover()
		q, isPayload := e.(*glPayload)
		res.ok = isPayload && q == p
		res.stack = string(debug.Stack())
		pcs := make([]uintptr, 32)
		n := runtime.Callers(2, pcs)
		if n == 0 {
			return
		}
		it := runtime.CallersFrames(pcs[:n])
		for {
			f, more := it.Next()
			res.frames = append(res.frames, f)
			if !more {
				break
			}
		}
	}()
	glLeaf(p)
	return res
}

//go:noinline
func glBlock(release <-chan struct{}) { <-release }

func glFrameNamed(frames []runtime.Frame, symbol string) (runtime.Frame, bool) {
	for _, f := range frames {
		if f.Function == symbol {
			return f, true
		}
	}
	return runtime.Frame{}, false
}

func main() {
	pA := &glPayload{id: 811, name: "label-nested"}
	pA.next = &glPayload{id: 812, name: "label-nested-next"}
	pB := &glPayload{id: 821, name: "label-outer"}
	pC := &glPayload{id: 831, name: "label-none"}
	gcStress()

	release := make(chan struct{})
	ready := make(chan struct{}, 3)
	resA := make(chan glResult, 1)
	var wg sync.WaitGroup
	wg.Add(3)

	// A：外层标签 + 内层标签；内层覆盖 outer，保留 keep，并新增 inner。
	go func() {
		defer wg.Done()
		pprof.Do(context.Background(), pprof.Labels("outer", "v1", "keep", "k"), func(ctx context.Context) {
			pprof.Do(ctx, pprof.Labels("outer", "v1b", "inner", "i1"), func(ctx context.Context) {
				resA <- glPanicWork(pA)
				ready <- struct{}{}
				glBlock(release)
			})
		})
	}()

	// B：只带外层标签；C：无标签。
	go func() {
		defer wg.Done()
		pprof.Do(context.Background(), pprof.Labels("keep", "k2", "outer", "v2"), func(ctx context.Context) {
			ready <- struct{}{}
			glBlock(release)
		})
	}()
	go func() {
		defer wg.Done()
		ready <- struct{}{}
		glBlock(release)
	}()

	for i := 0; i < 3; i++ {
		<-ready
	}
	res := <-resA

	// 1) 标签作用域内 panic/recover 的栈必须是真实符号与行号，且 debug.Stack 不带标签。
	ck(res.ok, "labeled goroutine recovered its payload")
	ck(strings.Contains(res.stack, "main.glLeaf("), "panic stack has leaf symbol")
	ck(strings.Contains(res.stack, "main.glPanicWork("), "panic stack has recovering worker symbol")
	ck(strings.Contains(res.stack, "goroutine "), "panic stack has goroutine header")
	ck(lineRe.MatchString(res.stack), "panic stack has main.go:line")
	ck(!strings.Contains(res.stack, "# labels:"), "debug.Stack does not synthesize pprof labels")
	for _, sym := range []string{"main.glLeaf", "main.glPanicWork"} {
		f, found := glFrameNamed(res.frames, sym)
		ck(found, "panic frames contain "+sym)
		ck(strings.HasSuffix(f.File, "main.go") && f.Line > 0, sym+" frame has file and line")
		ck(runtime.FuncForPC(f.Entry).Name() == f.Function, sym+" entry resolves to same symbol")
	}

	// 2) goroutine profile：恰好两条标签行，嵌套标签按内层覆盖合并。
	var prof string
	deadline := time.Now().Add(5 * time.Second)
	for {
		var buf bytes.Buffer
		if err := pprof.Lookup("goroutine").WriteTo(&buf, 1); err != nil {
			ck(false, "goroutine profile write succeeds")
			break
		}
		prof = buf.String()
		if strings.Count(prof, "# labels:") == 2 && strings.Count(prof, "main.glBlock") >= 3 {
			break
		}
		if time.Now().After(deadline) {
			break
		}
		runtime.Gosched()
		time.Sleep(time.Millisecond)
	}
	ck(strings.Count(prof, "# labels:") == 2, "exactly two labeled goroutines in profile")
	ck(strings.Count(prof, "main.glBlock") == 3, "profile includes all three blocked workers")
	ck(lineRe.MatchString(prof), "profile has main.go:line")
	ck(strings.Contains(prof, "# labels: {\"inner\":\"i1\", \"keep\":\"k\", \"outer\":\"v1b\"}"),
		"nested labels merge with inner overriding outer")
	ck(strings.Contains(prof, "# labels: {\"keep\":\"k2\", \"outer\":\"v2\"}"),
		"outer-only labeled goroutine keeps its label set")
	ck(strings.Count(prof, `"inner":"i1"`) == 1, "inner label appears exactly once")
	ck(!strings.Contains(prof, `"outer":"v1"`), "overridden outer label value is not reported")
	ck(strings.Count(prof, `"keep":"k2"`) == 1, "second label set is not duplicated")

	close(release)
	wg.Wait()

	gcStress()
	ck(pA.id == 811 && pA.name == "label-nested", "labeled panic payload after GC")
	ck(pA.next != nil && pA.next.id == 812 && pA.next.name == "label-nested-next",
		"labeled panic payload chain after GC")
	ck(pB.id == 821 && pB.name == "label-outer", "outer-label payload after GC")
	ck(pC.id == 831 && pC.name == "label-none", "unlabeled payload after GC")
	finish("l14_goroutine_label_nested_dump")
}
