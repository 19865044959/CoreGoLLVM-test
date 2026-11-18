// L14: 多 goroutine 栈自识别。N 个 worker 分别从 mgAlpha/mgBeta/mgGamma 进入并在
// channel 上阻塞；每个 worker 先抓自己的单 goroutine dump，解析 goroutine id；随后
// 全量 dump 中该 id 必须恰好出现一次、记录里必须是自己的真实符号、created by 归属
// 与 main.go:行号，且不同 goroutine 的栈不会串扰。TORTURE_ROUNDS/TORTURE_WORKERS
// 放大轮数与并发的 goroutine 数。
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

type mgPayload struct {
	id   int64
	name string
	next *mgPayload
}

type mgRecord struct {
	id     int
	self   string
	symbol string
	p      *mgPayload
}

var (
	failed int32
	gcSink [][]byte
	lineRe = regexp.MustCompile(`main\.go:\d+`)
	headRe = regexp.MustCompile(`(?m)^goroutine (\d+) \[`)
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

//go:noinline
func mgCapture(p *mgPayload, symbol string, ready chan<- mgRecord, release <-chan struct{}) {
	buf := make([]byte, 1<<16)
	n := runtime.Stack(buf, false)
	self := string(buf[:n])
	id := 0
	if m := headRe.FindStringSubmatch(self); m != nil {
		id, _ = strconv.Atoi(m[1])
	}
	ready <- mgRecord{id: id, self: self, symbol: symbol, p: p}
	<-release
}

//go:noinline
func mgAlpha(p *mgPayload, ready chan<- mgRecord, release <-chan struct{}) {
	mgCapture(p, "main.mgAlpha", ready, release)
}

//go:noinline
func mgBeta(p *mgPayload, ready chan<- mgRecord, release <-chan struct{}) {
	mgCapture(p, "main.mgBeta", ready, release)
}

//go:noinline
func mgGamma(p *mgPayload, ready chan<- mgRecord, release <-chan struct{}) {
	mgCapture(p, "main.mgGamma", ready, release)
}

//go:noinline
func mgLaunch(n int) ([]mgRecord, chan struct{}, <-chan struct{}, []*mgPayload) {
	release := make(chan struct{})
	ready := make(chan mgRecord, n)
	done := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(n)
	payloads := make([]*mgPayload, n)
	for i := 0; i < n; i++ {
		p := &mgPayload{id: int64(1000 + i), name: "multi-" + strconv.Itoa(i)}
		p.next = &mgPayload{id: int64(2000 + i), name: "multi-next-" + strconv.Itoa(i)}
		payloads[i] = p
		switch i % 3 {
		case 0:
			go func() { defer wg.Done(); mgAlpha(p, ready, release) }()
		case 1:
			go func() { defer wg.Done(); mgBeta(p, ready, release) }()
		default:
			go func() { defer wg.Done(); mgGamma(p, ready, release) }()
		}
	}
	recs := make([]mgRecord, 0, n)
	for i := 0; i < n; i++ {
		recs = append(recs, <-ready)
	}
	go func() { wg.Wait(); close(done) }()
	return recs, release, done, payloads
}

func mgDumpAll() string {
	b := make([]byte, 1<<20)
	n := runtime.Stack(b, true)
	ck(n < len(b), "1MiB captures full multi-goroutine dump")
	return string(b[:n])
}

func main() {
	rounds := envInt("TORTURE_ROUNDS", 2, 1, 16)
	workers := envInt("TORTURE_WORKERS", 12, 3, 48)
	gcStress()

	for round := 0; round < rounds; round++ {
		recs, release, done, payloads := mgLaunch(workers)

		var full string
		deadline := time.Now().Add(5 * time.Second)
		for {
			full = mgDumpAll()
			if strings.Count(full, "created by main.mgLaunch") >= workers &&
				strings.Count(full, "chan receive") >= workers {
				break
			}
			if time.Now().After(deadline) {
				break
			}
			runtime.Gosched()
			time.Sleep(time.Millisecond)
		}

		// 1) 全量 dump 的 goroutine id 唯一。
		headers := headRe.FindAllStringSubmatch(full, -1)
		ck(len(headers) >= workers+1, "full dump has main plus every worker header")
		uniq := map[int]bool{}
		dup := false
		for _, m := range headers {
			id, _ := strconv.Atoi(m[1])
			if uniq[id] {
				dup = true
			}
			uniq[id] = true
		}
		ck(!dup, "full dump goroutine ids are unique")
		ck(strings.Count(full, "created by main.mgLaunch") >= workers, "full dump shows launch owner")
		ck(strings.Count(full, "main.mgCapture(") >= workers, "full dump shows every worker frame")
		ck(lineRe.MatchString(full), "full dump has main.go:line")

		// 2) 切分全量 dump，按 id 建立记录索引。
		byID := map[int]string{}
		for _, part := range strings.Split(full, "\n\ngoroutine ") {
			if !strings.HasPrefix(part, "goroutine ") {
				part = "goroutine " + part
			}
			m := headRe.FindStringSubmatch(part)
			if m == nil {
				continue
			}
			id, _ := strconv.Atoi(m[1])
			byID[id] = part
		}
		ck(len(byID) == len(headers), "record index covers every goroutine header")

		// 3) 每个 worker 的自抓栈 id 必须与全量 dump 中唯一记录一致。
		kinds := 0
		for _, rec := range recs {
			selfHeaders := headRe.FindAllStringSubmatch(rec.self, -1)
			ck(len(selfHeaders) == 1, "self dump has exactly one goroutine header")
			if len(selfHeaders) != 1 {
				continue
			}
			id, _ := strconv.Atoi(selfHeaders[0][1])
			ck(rec.id != 0 && id == rec.id, "self dump id parses consistently")
			part, found := byID[id]
			ck(found, "self dump id appears in full dump")
			if !found {
				continue
			}
			ck(strings.Contains(rec.self, rec.symbol+"("), "self dump contains worker kind symbol")
			ck(strings.Contains(part, rec.symbol+"("), "full dump record contains same worker kind symbol")
			ck(strings.Contains(part, "created by main.mgLaunch"), "worker record has created-by owner")
			ck(strings.Contains(part, "chan receive"), "worker record shows blocked state")
			ck(lineRe.MatchString(part), "worker record has main.go:line")
			if strings.Contains(part, "main.mgAlpha(") {
				kinds |= 1
			}
			if strings.Contains(part, "main.mgBeta(") {
				kinds |= 2
			}
			if strings.Contains(part, "main.mgGamma(") {
				kinds |= 4
			}
			ck(rec.p != nil && rec.p.id >= 1000, "worker record carries payload pointer")
		}
		ck(kinds == 7, "all three worker kinds appear in records")

		// 4) 每个 worker 的 id 在完整 dump 中只出现一次（header 计数）。
		for _, rec := range recs {
			needle := "goroutine " + strconv.Itoa(rec.id) + " ["
			ck(strings.Count(full, needle) == 1, "worker id appears exactly once in full dump")
		}

		close(release)
		<-done
		gcStress()
		ck(len(payloads) == workers, "payload slice covers all workers")
		for i, p := range payloads {
			ck(p.id == int64(1000+i) && p.name == "multi-"+strconv.Itoa(i), fmt.Sprintf("worker %d payload after GC", i))
			ck(p.next != nil && p.next.id == int64(2000+i), fmt.Sprintf("worker %d payload chain after GC", i))
		}
	}

	finish("l14_multi_goroutine_stack_ids")
}
