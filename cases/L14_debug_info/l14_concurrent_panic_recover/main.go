// L14: 多 goroutine 并发 panic/recover。每个 goroutine 的 traceback 必须带真实
// 链路符号与 main.go:行号；recover 到的 payload 指针在并发 GC 后仍有效。
// TORTURE_ROUNDS 放大并发轮数。
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

type prPayload struct {
	id   int64
	tag  string
	pad  []byte
	next *prPayload
}

type prResult struct {
	id    int64
	stack string
	p     *prPayload
	ok    bool
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
	if n > 32 {
		n = 32
	}
	return n
}

//go:noinline
func prLeaf(p *prPayload) { panic(p) }

//go:noinline
func prMid(p *prPayload) { prLeaf(p) }

//go:noinline
func prTop(id int64) (r prResult) {
	p := &prPayload{id: id, tag: "concurrent", pad: make([]byte, 64)}
	p.next = &prPayload{id: id + 1, tag: "chain", pad: make([]byte, 32)}
	r.id = id
	r.p = p
	defer func() {
		if e := recover(); e != nil {
			q, is := e.(*prPayload)
			r.ok = is && q == p && q.id == id && q.next != nil && q.next.id == id+1
			r.stack = string(debug.Stack())
		}
	}()
	prMid(p)
	return
}

func main() {
	rounds := tortureRounds(1)
	const workers = 16

	for r := 0; r < rounds; r++ {
		out := make(chan prResult, workers)
		for i := 0; i < workers; i++ {
			go func(id int64) { out <- prTop(id) }(int64(i))
		}
		seen := make([]prResult, workers)
		for i := 0; i < workers; i++ {
			res := <-out
			if res.id >= 0 && res.id < workers {
				seen[res.id] = res
			}
		}
		for i := int64(0); i < workers; i++ {
			res := seen[i]
			ck(res.p != nil && res.id == i, "goroutine result present")
			ck(res.ok, "concurrent recover payload identity")
			ck(strings.Contains(res.stack, "main.prLeaf("), "stack has prLeaf symbol")
			ck(strings.Contains(res.stack, "main.prMid("), "stack has prMid symbol")
			ck(strings.Contains(res.stack, "main.prTop("), "stack has prTop symbol")
			ck(strings.Contains(res.stack, "goroutine "), "stack has goroutine header")
			ck(lineRe.MatchString(res.stack), "stack has main.go:line")
		}
		gcStress()
		for i := int64(0); i < workers; i++ {
			res := seen[i]
			ck(res.p != nil && res.p.id == i && res.p.tag == "concurrent" &&
				res.p.next != nil && res.p.next.id == i+1, "recovered payload after concurrent GC")
		}
	}
	finish("l14_concurrent_panic_recover")
}
