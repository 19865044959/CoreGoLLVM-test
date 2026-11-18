// L14: 多 goroutine dump 的 created-by 归属。由同一 launcher 批量创建的 goroutine
// 阻塞在同一 worker 函数上，runtime.Stack(buf,true) 必须给出每个 goroutine 的
// created by 归属（main.launchDumpWorkers + main.go:行号）、阻塞 worker 的真实
// 符号与 per-frame 行号，且阻塞期间持有的指针在 GC 后有效。
// TORTURE_ROUNDS 放大轮数。
package main

import (
	"fmt"
	"os"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

type gdPayload struct {
	id   int64
	name string
	next *gdPayload
}

var (
	failed int32
	gcSink [][]byte
	lineRe = regexp.MustCompile(`main\.go:\d+`)
	// main.dumpWorker(...) 下一行必须是 main.go:行号。
	workerFrameRe = regexp.MustCompile(`(?m)^main\.dumpWorker\([^\n]*\n\t[^\n]*main\.go:\d+`)
	// created by main.launchDumpWorkers ... 下一行必须是 main.go:行号。
	createdByRe = regexp.MustCompile(`(?m)^created by main\.launchDumpWorkers[^\n]*\n\t[^\n]*main\.go:\d+`)
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
	if n > 8 {
		n = 8
	}
	return n
}

//go:noinline
func dumpWorker(ch <-chan struct{}, ready chan<- struct{}, done chan<- struct{}, p *gdPayload, errs *int32) {
	ready <- struct{}{}
	<-ch
	if p == nil || p.next == nil {
		atomic.AddInt32(errs, 1)
	}
	done <- struct{}{}
}

// launchDumpWorkers 是 created by 归属的 launcher：所有 worker goroutine 都由
// 本函数中的 go 语句创建。
//
//go:noinline
func launchDumpWorkers(n int, ch <-chan struct{}, ready chan<- struct{}, done chan<- struct{}, ps []*gdPayload, errs *int32) {
	for i := 0; i < n; i++ {
		go dumpWorker(ch, ready, done, ps[i], errs)
	}
}

func runRound(round, workers int) {
	ch := make(chan struct{})
	ready := make(chan struct{}, workers)
	done := make(chan struct{}, workers)
	errs := int32(0)
	ps := make([]*gdPayload, workers)
	for i := 0; i < workers; i++ {
		ps[i] = &gdPayload{id: int64(round*100 + i), name: "dump-created-by"}
		ps[i].next = &gdPayload{id: int64(round*100 + i + 1), name: "next"}
	}
	launchDumpWorkers(workers, ch, ready, done, ps, &errs)
	for i := 0; i < workers; i++ {
		<-ready
	}
	gcStress()

	buf := make([]byte, 1<<20)
	deadline := time.Now().Add(3 * time.Second)
	var dump string
	dumpLen := 0
	need := workers
	for {
		dumpLen = runtime.Stack(buf, true)
		dump = string(buf[:dumpLen])
		if strings.Count(dump, "main.dumpWorker(") >= need &&
			strings.Count(dump, "created by main.launchDumpWorkers") >= need {
			break
		}
		if time.Now().After(deadline) {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}

	prefix := "round " + strconv.Itoa(round) + ": "
	ck(dumpLen < len(buf), prefix+"large goroutine dump not truncated")
	ck(strings.Count(dump, "created by main.launchDumpWorkers") >= workers,
		prefix+"dump attributes all workers to launcher")
	ck(strings.Count(dump, "main.dumpWorker(") >= workers, prefix+"dump has every blocked worker symbol")
	ck(len(workerFrameRe.FindAllString(dump, -1)) >= workers, prefix+"each worker frame has main.go:line")
	ck(len(createdByRe.FindAllString(dump, -1)) >= workers, prefix+"each created-by frame has main.go:line")
	ck(strings.Contains(dump, "main.main("), prefix+"dump has main.main symbol")
	ck(strings.Contains(dump, "chan receive"), prefix+"dump has blocked state")
	ck(lineRe.MatchString(dump), prefix+"dump has main.go:line")

	close(ch)
	for i := 0; i < workers; i++ {
		<-done
	}
	ck(atomic.LoadInt32(&errs) == 0, prefix+"blocked worker pointers after release")
	for i := 0; i < workers; i++ {
		ck(ps[i].id == int64(round*100+i) && ps[i].name == "dump-created-by" &&
			ps[i].next != nil && ps[i].next.id == int64(round*100+i+1),
			prefix+"worker payload after GC and release")
	}
}

func main() {
	rounds := tortureRounds(1)
	const workers = 12
	for r := 1; r <= rounds; r++ {
		runRound(r, workers)
	}
	finish("l14_goroutine_dump_created_by")
}
