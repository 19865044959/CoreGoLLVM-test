// L14: 带 pprof 标签的 goroutine dump。runtime.Stack 打印阻塞 goroutine 的真实符号、
// 状态与 created by；pprof.Lookup("goroutine").WriteTo(debug=1) 额外给出每个 goroutine
// 的 "# labels:" 行。验证标签集合（键排序、值精确）、标签数量、以及未打标签的
// goroutine 不被误标。
package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"regexp"
	"runtime"
	"runtime/pprof"
	"strings"
	"sync/atomic"
	"time"
)

type glPayload struct {
	id   int64
	name string
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
func glBlocker(p *glPayload, ch <-chan struct{}) {
	<-ch
	atomic.AddInt64(&p.id, 1)
}

//go:noinline
func glLaunch(payloads []*glPayload, labeled []bool, release <-chan struct{}, ready chan<- struct{}) {
	for i, p := range payloads {
		i := i
		p := p
		if !labeled[i] {
			go func() {
				ready <- struct{}{}
				glBlocker(p, release)
			}()
			continue
		}
		go func() {
			pprof.Do(context.Background(), pprof.Labels("request", fmt.Sprintf("req-%d", i), "worker", fmt.Sprintf("w%d", i)), func(ctx context.Context) {
				ready <- struct{}{}
				glBlocker(p, release)
			})
		}()
	}
}

func glCount(s, sub string) int {
	return strings.Count(s, sub)
}

func main() {
	payloads := []*glPayload{
		{id: 0, name: "label-0"},
		{id: 0, name: "label-1"},
		{id: 0, name: "label-2"},
		{id: 0, name: "unlabeled"},
	}
	labeled := []bool{true, true, true, false}
	release := make(chan struct{})
	ready := make(chan struct{}, len(payloads))
	glLaunch(payloads, labeled, release, ready)
	for range payloads {
		<-ready
	}

	// 轮询到 4 个 worker 都停在 glBlocker 的 channel 接收上。
	dumpAll := func() string {
		b := make([]byte, 1<<20)
		n := runtime.Stack(b, true)
		ck(n < len(b), "1MiB is enough for full goroutine dump")
		return string(b[:n])
	}
	var full string
	deadline := time.Now().Add(5 * time.Second)
	for {
		full = dumpAll()
		if glCount(full, "main.glBlocker(") >= len(payloads) && glCount(full, "chan receive") >= len(payloads) {
			break
		}
		if time.Now().After(deadline) {
			break
		}
		runtime.Gosched()
		time.Sleep(time.Millisecond)
	}

	// 1) runtime.Stack 全量 dump：真实阻塞符号、状态、created by 归属与行号。
	ck(glCount(full, "main.glBlocker(") >= len(payloads), "runtime.Stack shows every blocked worker")
	ck(glCount(full, "chan receive") >= len(payloads), "runtime.Stack shows chan receive states")
	ck(glCount(full, "created by main.glLaunch") >= len(payloads), "runtime.Stack shows created-by owner")
	ck(glCount(full, "goroutine ") >= len(payloads)+1, "runtime.Stack has goroutine headers")
	ck(lineRe.MatchString(full), "runtime.Stack has main.go:line")

	// 2) pprof goroutine profile：标签行逐字匹配（键按字典序，逗号后有一个空格）。
	var pb bytes.Buffer
	ck(pprof.Lookup("goroutine").WriteTo(&pb, 1) == nil, "goroutine profile write succeeds")
	prof := pb.String()
	ck(glCount(prof, "# labels:") == 3, "exactly three labeled goroutines in profile")
	for i := 0; i < 3; i++ {
		want := fmt.Sprintf(`# labels: {"request":"req-%d", "worker":"w%d"}`, i, i)
		ck(strings.Contains(prof, want), "profile has exact label set "+want)
	}
	ck(glCount(prof, "main.glBlocker") >= len(payloads), "profile includes every blocked worker")
	ck(lineRe.MatchString(prof), "profile has main.go:line")

	// 3) 未打标签的 goroutine 不贡献第 4 个标签行；三组标签互不串扰。
	ck(glCount(prof, `"worker":"w3"`) == 0, "unlabeled worker has no synthetic label")
	ck(glCount(prof, `"request":"req-0"`) == 1, "label set 0 appears once")
	ck(glCount(prof, `"request":"req-1"`) == 1, "label set 1 appears once")
	ck(glCount(prof, `"request":"req-2"`) == 1, "label set 2 appears once")

	close(release)
	// 等待所有 worker 退出：payload.id 被递增一次。
	releaseDone := make(chan struct{})
	go func() {
		for {
			ok := true
			for _, p := range payloads {
				if atomic.LoadInt64(&p.id) == 0 {
					ok = false
				}
			}
			if ok {
				close(releaseDone)
				return
			}
			runtime.Gosched()
			time.Sleep(time.Millisecond)
		}
	}()
	<-releaseDone

	gcStress()
	ck(atomic.LoadInt64(&payloads[0].id) == 1 && atomic.LoadInt64(&payloads[1].id) == 1 &&
		atomic.LoadInt64(&payloads[2].id) == 1 && atomic.LoadInt64(&payloads[3].id) == 1,
		"all workers ran after release")
	finish("l14_goroutine_label_dump")
}
