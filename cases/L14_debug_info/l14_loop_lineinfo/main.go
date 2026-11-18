// L14: 循环内行号。同一循环体内四个不同源码行的调用点必须各自映射到固定且递增的
// main.go 行号；循环内 panic 的 traceback 也带真实符号与 main.go:行号。
// TORTURE_ROUNDS 放大循环轮数。
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

type loopPayload struct {
	id   int64
	name string
	next *loopPayload
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

func tortureRounds(def int) int {
	s := os.Getenv("TORTURE_ROUNDS")
	if s == "" {
		return def
	}
	n, err := strconv.Atoi(s)
	if err != nil || n <= 0 {
		return def
	}
	if n > 16 {
		n = 16
	}
	return n
}

//go:noinline
func loopSite() (string, int) {
	_, file, line, _ := runtime.Caller(1)
	return file, line
}

//go:noinline
func loopPanic(p *loopPayload) { panic(p) }

func runRound(round, iterations int) {
	p := &loopPayload{id: int64(round), name: "loop-line"}
	p.next = &loopPayload{id: int64(round) + 1, name: "next"}

	var first [4]int
	var files [4]string
	var panicStack string
	panicAt := iterations / 2

	for i := 0; i < iterations; i++ {
		func() {
			defer func() {
				if e := recover(); e != nil {
					q, is := e.(*loopPayload)
					ck(is && q == p, "loop panic payload identity")
					panicStack = string(debug.Stack())
				}
			}()

			f0, l0 := loopSite()
			f1, l1 := loopSite()
			f2, l2 := loopSite()
			f3, l3 := loopSite()
			files[0], files[1], files[2], files[3] = f0, f1, f2, f3

			if i == 0 {
				first[0], first[1], first[2], first[3] = l0, l1, l2, l3
			} else {
				ck(l0 == first[0] && l1 == first[1] && l2 == first[2] && l3 == first[3],
					"loop call-site lines stable across iterations")
			}

			if i == panicAt {
				loopPanic(p)
			}
			if i%32 == 0 {
				gcSink = append(gcSink, make([]byte, 1024))
			}
		}()
	}

	prefix := "round " + strconv.Itoa(round) + ": "
	for i := 0; i < 4; i++ {
		ck(strings.HasSuffix(files[i], "main.go"), prefix+"loop call site reports main.go")
		ck(first[i] > 0, prefix+"loop call site reports positive line")
	}
	ck(first[0] < first[1] && first[1] < first[2] && first[2] < first[3],
		prefix+"loop call-site lines increase with source order")

	ck(panicStack != "", prefix+"panic inside loop produced stack")
	ck(strings.Contains(panicStack, "main.loopPanic("), prefix+"loop panic stack has leaf symbol")
	ck(strings.Contains(panicStack, "main.runRound.func1("), prefix+"loop panic stack has anonymous loop frame")
	ck(lineRe.MatchString(panicStack), prefix+"loop panic stack has main.go:line")
	ck(panicStack != "" && strings.Index(panicStack, "main.loopPanic(") < strings.Index(panicStack, "main.runRound.func1("),
		prefix+"loop panic frames ordered inner to outer")

	gcStress()
	ck(p.id == int64(round) && p.name == "loop-line" && p.next != nil && p.next.id == int64(round)+1,
		prefix+"loop payload after GC")
}

func main() {
	rounds := tortureRounds(1)
	iterations := 64
	for r := 1; r <= rounds; r++ {
		runRound(r, iterations)
	}
	finish("l14_loop_lineinfo")
}
