// L14: 多 goroutine id 的生命周期边界。每批 worker 由同一 main 循环创建，各自
// 自抓 runtime.Stack(buf,false) 解析 goroutine id。Go 1.24 的 per-P goid cache
// （runtime/proc.go:5110-5120 一次预留 16 个 id）意味着 id 只保证唯一、不保证与
// 创建序全局单调；本用例因此断言：id 全部大于 main、批内唯一、跨批次/跨轮次
// 绝不复用；全量 dump 中存活 id 恰好出现一次，整批退出后消失。TORTURE_ROUNDS/
// TORTURE_WORKERS 放大规模。
package main

import (
	"fmt"
	"os"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type gidPayload struct {
	id   int64
	name string
	next *gidPayload
}

type gidRec struct {
	idx   int
	id    int
	valid bool
}

var (
	failed int32
	gcSink [][]byte
	lineRe = regexp.MustCompile(`main\.go:\d+`)
	gidRe  = regexp.MustCompile(`(?m)^goroutine (\d+) \[`)
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

func envInt(key string, def, min, max int) int {
	s := os.Getenv(key)
	if s == "" {
		return def
	}
	v, err := strconv.Atoi(s)
	if err != nil || v < min {
		return def
	}
	if v > max {
		return max
	}
	return v
}

func gidParse(s string) (int, bool) {
	ms := gidRe.FindAllStringSubmatch(s, -1)
	if len(ms) != 1 {
		return 0, false
	}
	id, err := strconv.Atoi(ms[0][1])
	return id, err == nil && id > 0
}

//go:noinline
func gidWorker(idx int, p *gidPayload, ready chan<- gidRec, release <-chan struct{}, wg *sync.WaitGroup) {
	defer wg.Done()
	buf := make([]byte, 1<<14)
	n := runtime.Stack(buf, false)
	id, ok := gidParse(string(buf[:n]))
	ready <- gidRec{idx: idx, id: id, valid: ok}
	<-release
	atomic.AddInt64(&p.id, 1000)
}

func gidDumpAll() string {
	b := make([]byte, 1<<20)
	n := runtime.Stack(b, true)
	ck(n < len(b), "1MiB captures the full goroutine dump")
	return string(b[:n])
}

func gidHeader(id int) string {
	return "goroutine " + strconv.Itoa(id) + " ["
}

//go:noinline
func gidBatch(workers int) ([]int, []*gidPayload) {
	release := make(chan struct{})
	ready := make(chan gidRec, workers)
	payloads := make([]*gidPayload, workers)
	var wg sync.WaitGroup
	wg.Add(workers)
	for i := 0; i < workers; i++ {
		p := &gidPayload{id: int64(i) + 1, name: "gid-" + strconv.Itoa(i)}
		p.next = &gidPayload{id: int64(i) + 101, name: "gid-next-" + strconv.Itoa(i)}
		payloads[i] = p
		go func(idx int, p *gidPayload) {
			gidWorker(idx, p, ready, release, &wg)
		}(i, p)
	}

	recs := make([]gidRec, workers)
	for i := 0; i < workers; i++ {
		recs[i] = <-ready
	}
	ids := make([]int, workers)
	for _, r := range recs {
		ck(r.valid && r.id > 0, "worker self dump parses a positive goroutine id")
		if r.idx < 0 || r.idx >= workers {
			ck(false, "worker reports an in-range launch index")
			continue
		}
		ids[r.idx] = r.id
	}
	seen := map[int]bool{}
	for i, id := range ids {
		ck(id > 0, fmt.Sprintf("worker %d has a parsed id", i))
		ck(!seen[id], fmt.Sprintf("worker %d id is unique within batch", i))
		seen[id] = true
	}

	var full string
	for tries := 0; tries < 5000; tries++ {
		full = gidDumpAll()
		if strings.Count(full, "main.gidWorker(") >= workers &&
			strings.Count(full, "chan receive") >= workers {
			break
		}
		runtime.Gosched()
		time.Sleep(time.Millisecond)
	}
	ck(strings.Count(full, "main.gidWorker(") >= workers, "full dump shows every worker")
	ck(strings.Count(full, "chan receive") >= workers, "full dump shows worker wait states")
	ck(strings.Count(full, "created by main.gidBatch") >= workers, "full dump shows batch launch owner")
	ck(lineRe.MatchString(full), "full dump has main.go:line")
	for i, id := range ids {
		if id == 0 {
			continue
		}
		ck(strings.Count(full, gidHeader(id)) == 1,
			fmt.Sprintf("live worker %d id appears exactly once in full dump", i))
	}

	close(release)
	wg.Wait()
	for tries := 0; tries < 5000; tries++ {
		full = gidDumpAll()
		alive := false
		for _, id := range ids {
			if id > 0 && strings.Contains(full, gidHeader(id)) {
				alive = true
			}
		}
		if !alive {
			break
		}
		runtime.Gosched()
		time.Sleep(time.Millisecond)
	}
	for i, id := range ids {
		if id == 0 {
			continue
		}
		ck(!strings.Contains(full, gidHeader(id)),
			fmt.Sprintf("exited worker %d id disappears from full dump", i))
	}
	return ids, payloads
}

func gidCheckBatch(ids []int, mainID int, seen map[int]bool) {
	batchSeen := map[int]bool{}
	for i, id := range ids {
		ck(id > mainID, fmt.Sprintf("worker %d id is greater than main goroutine id", i))
		ck(!batchSeen[id], fmt.Sprintf("worker %d id is unique within batch", i))
		batchSeen[id] = true
		ck(!seen[id], fmt.Sprintf("worker %d id is not reused across batches", i))
		seen[id] = true
	}
}

func main() {
	mainBuf := make([]byte, 1<<14)
	n := runtime.Stack(mainBuf, false)
	mainID, mainOK := gidParse(string(mainBuf[:n]))
	ck(mainOK && mainID > 0, "main goroutine self dump parses a positive id")
	gcStress()

	rounds := envInt("TORTURE_ROUNDS", 2, 1, 16)
	workers := envInt("TORTURE_WORKERS", 8, 3, 32)
	seenIDs := map[int]bool{}
	for round := 0; round < rounds; round++ {
		ids1, p1 := gidBatch(workers)
		gidCheckBatch(ids1, mainID, seenIDs)

		ids2, p2 := gidBatch(workers)
		gidCheckBatch(ids2, mainID, seenIDs)

		gcStress()
		for i, p := range p1 {
			ck(atomic.LoadInt64(&p.id) == int64(1000+i+1), fmt.Sprintf("batch1 worker %d payload after GC", i))
			ck(p.next != nil && p.next.id == int64(101+i), fmt.Sprintf("batch1 worker %d chain after GC", i))
		}
		for i, p := range p2 {
			ck(atomic.LoadInt64(&p.id) == int64(1000+i+1), fmt.Sprintf("batch2 worker %d payload after GC", i))
			ck(p.next != nil && p.next.id == int64(101+i), fmt.Sprintf("batch2 worker %d chain after GC", i))
		}
	}

	gcStress()
	finish("l14_goroutine_id_batch_lifecycle")
}
