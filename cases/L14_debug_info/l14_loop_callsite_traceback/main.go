// L14: 循环行号与 traceback 归属。循环体内同一 helper 在三个不同源码行被调用，
// 每次调用都 panic 并在 defer 中 recover；runtime.Caller 记录的调用点行号必须与
// traceback 中宿主帧（main.loopOne）报告的行号一致、循环多次迭代保持稳定，
// 三个调用点行号互不相同且随源码顺序递增（只比较相对关系，不依赖具体行号值）。
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

type lpPayload struct {
	id   int64
	name string
	next *lpPayload
}

var (
	failed       int32
	gcSink       [][]byte
	loopLastSite int
	loopLastFile string
	lineRe       = regexp.MustCompile(`main\.go:\d+`)
	hostLineRe   = regexp.MustCompile(`(?m)^main\.loopOne\([^\n]*\n\t[^\n]*main\.go:(\d+)`)
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

// loopPanicAt 先记录直接调用者（循环体中的某个 case 行）的文件/行号，再 panic。
//
//go:noinline
func loopPanicAt(p *lpPayload) {
	_, f, l, _ := runtime.Caller(1)
	loopLastFile = f
	loopLastSite = l
	panic(p)
}

// loopOne 在 switch 的三个 case 中调用 loopPanicAt，三个调用点位于不同源码行。
//
//go:noinline
func loopOne(i int, p *lpPayload) (stack string, site int, ok bool) {
	defer func() {
		if e := recover(); e != nil {
			q, is := e.(*lpPayload)
			ok = is && q == p
			stack = string(debug.Stack())
			site = loopLastSite
		}
	}()
	switch i % 3 {
	case 0:
		loopPanicAt(p)
	case 1:
		loopPanicAt(p)
	case 2:
		loopPanicAt(p)
	}
	return "", 0, false
}

func stackHostLine(stack string) int {
	m := hostLineRe.FindStringSubmatch(stack)
	if len(m) != 2 {
		return -1
	}
	n, err := strconv.Atoi(m[1])
	if err != nil {
		return -1
	}
	return n
}

func main() {
	p := &lpPayload{id: 121, name: "loop-site"}
	p.next = &lpPayload{id: 122, name: "next"}
	gcStress()

	const iterations = 48
	var first [3]int
	var files [3]string
	for i := 0; i < iterations; i++ {
		k := i % 3
		stack, site, ok := loopOne(i, p)
		ck(ok, "loop panic payload identity")
		ck(strings.HasSuffix(loopLastFile, "main.go"), "loop call site reports main.go")
		ck(site > 0, "loop call site reports positive line")
		stackLine := stackHostLine(stack)
		ck(stackLine == site, "traceback host frame line matches runtime.Caller call site")
		ck(strings.Contains(stack, "main.loopPanicAt("), "loop traceback has panic leaf symbol")
		ck(strings.Contains(stack, "main.loopOne.func1("), "loop traceback has recovering defer symbol")
		ck(strings.Contains(stack, "goroutine "), "loop traceback has goroutine header")
		ck(lineRe.MatchString(stack), "loop traceback has main.go:line")

		if i < 3 {
			first[k] = site
			files[k] = loopLastFile
		} else {
			ck(site == first[k], "loop call-site line stable across iterations")
			ck(loopLastFile == files[k], "loop call-site file stable across iterations")
		}
	}

	ck(first[0] > 0 && first[1] > 0 && first[2] > 0, "all three loop call sites reported")
	ck(first[0] < first[1] && first[1] < first[2], "loop call-site lines increase with source order")
	ck(first[0] != first[1] && first[1] != first[2] && first[0] != first[2], "loop call-site lines are distinct")
	gcStress()
	ck(p.id == 121 && p.name == "loop-site", "loop payload after GC")
	ck(p.next != nil && p.next.id == 122 && p.next.name == "next", "loop payload chain after GC")
	finish("l14_loop_callsite_traceback")
}
