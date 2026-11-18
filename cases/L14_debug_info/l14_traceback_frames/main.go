// L14: panic/debug.Stack 与 runtime.CallersFrames 的符号/行号/文件信息。
package main

import (
	"fmt"
	"os"
	"regexp"
	"runtime"
	"runtime/debug"
	"strings"
	"sync/atomic"
)

type node struct {
	v    int64
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

//go:noinline
func tbInner(p *node) {
	if p == nil {
		panic("nil node")
	}
	panic("boom")
}

//go:noinline
func tbMiddle(p *node) { tbInner(p) }

//go:noinline
func tbOuter(p *node) (stack string, ok bool) {
	defer func() {
		if e := recover(); e != nil {
			stack = string(debug.Stack())
			ok = p != nil && p.v == 7 && p.name == "tb"
		}
	}()
	tbMiddle(p)
	return "", false
}

//go:noinline
func callersInfo() (funcs []string, files []string, lines []int) {
	pcs := make([]uintptr, 16)
	n := runtime.Callers(1, pcs)
	frames := runtime.CallersFrames(pcs[:n])
	for {
		f, more := frames.Next()
		funcs = append(funcs, f.Function)
		files = append(files, f.File)
		lines = append(lines, f.Line)
		if !more {
			break
		}
	}
	return
}

func main() {
	p := &node{v: 7, name: "tb"}
	stack, ok := tbOuter(p)
	gcStress()
	ck(ok, "recover saw valid pointer")
	ck(strings.Contains(stack, "tbInner"), "stack has tbInner")
	ck(strings.Contains(stack, "tbMiddle"), "stack has tbMiddle")
	ck(strings.Contains(stack, "tbOuter"), "stack has tbOuter")
	ck(strings.Contains(stack, "goroutine"), "stack has goroutine header")
	ck(regexp.MustCompile(`main\.go:\d+`).MatchString(stack), "stack has file:line")

	funcs, files, lines := callersInfo()
	ck(len(funcs) >= 1, "callers frames present")
	joined := strings.Join(funcs, ",")
	ck(strings.Contains(joined, "callersInfo"), "callers include callersInfo")
	fileOK, lineOK := false, false
	for i := range funcs {
		if strings.HasSuffix(files[i], "main.go") {
			fileOK = true
		}
		if lines[i] > 0 {
			lineOK = true
		}
	}
	ck(fileOK, "callers have main.go file")
	ck(lineOK, "callers have positive line numbers")

	// panic 在 goroutine 中 recover，同样可取到栈。
	done := make(chan string, 1)
	go func() {
		defer func() {
			if e := recover(); e != nil {
				done <- string(debug.Stack())
			}
		}()
		tbMiddle(p)
	}()
	s := <-done
	gcStress()
	ck(strings.Contains(s, "tbInner") && strings.Contains(s, "tbMiddle"), "goroutine panic stack symbols")
	finish("l14_traceback_frames")
}
