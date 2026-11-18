// L14: runtime.Stack 缓冲尺寸的精确边界。尺寸通过全局变量传入，保证 full dump 与
// 边界尺寸 dump 来自同一调用点、栈文本可逐字节比较：
//   - len == full-1：n == len(buf)，内容是完整 dump 的严格前缀；
//   - len == full  ：n == len(buf)，但内容恰好是完整 dump（截断标志与完整性歧义）；
//   - len == full+1：n == full < len(buf)，内容完整且 n 之后字节为零。
//
// single 与 all=true（多个阻塞 worker）两条路径都覆盖。TORTURE_ROUNDS 放大轮数。
package main

import (
	"fmt"
	"os"
	"regexp"
	"runtime"
	"runtime/debug"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type sbPayload struct {
	id int64
}

var (
	failed int32
	gcSink [][]byte
	lineRe = regexp.MustCompile(`main\.go:\d+`)
	headRe = regexp.MustCompile(`(?m)^goroutine (\d+) \[`)

	sbLimit int
	sbAll   bool
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

// 尺寸与 all 都走全局变量，栈帧没有随尺寸变化的参数，dump 文本才能稳定比较。
//
//go:noinline
func sbDump() (int, []byte) {
	b := make([]byte, sbLimit)
	n := runtime.Stack(b, sbAll)
	return n, b
}

//go:noinline
func sbCapture() (int, string, []byte) {
	n, b := sbDump()
	return n, string(b[:n]), b
}

//go:noinline
func sbBlock(p *sbPayload, release <-chan struct{}, ready chan<- struct{}) {
	ready <- struct{}{}
	<-release
	atomic.AddInt64(&p.id, 1)
}

// sbProbe 在同一个调用点依次抓取 full 与三个边界尺寸，返回完整 dump。
//
//go:noinline
func sbProbe() (int, string) {
	fullN, full := 0, ""
	for i := 0; i < 6; i++ {
		sz := 1 << 20
		switch i {
		case 1:
			sz = fullN - 1
		case 2:
			sz = fullN
		case 3:
			sz = fullN + 1
		case 4:
			sz = fullN + 2
		case 5:
			sz = fullN * 2
		}
		sbLimit = sz
		n, s, raw := sbCapture()
		ck(n <= len(raw), "runtime.Stack never writes beyond buffer")
		zeroTail := true
		for j := n; j < len(raw); j++ {
			if raw[j] != 0 {
				zeroTail = false
			}
		}
		ck(zeroTail, "runtime.Stack leaves bytes beyond n untouched")
		if i == 0 {
			fullN, full = n, s
			ck(n > 0 && n < sz, "large buffer captures a complete dump")
			ck(strings.HasPrefix(s, "goroutine "), "full dump starts with goroutine header")
			ck(strings.HasSuffix(s, "\n"), "full dump ends with newline")
			ck(n == len(s), "full dump length matches returned n")
			continue
		}
		ck(n > 0 && n <= sz, "runtime.Stack returns bytes within buffer")
		ck(strings.HasPrefix(s, "goroutine "), "dump starts with goroutine header")
		if sbAll {
			// all=true 时 goroutine 集合可能在两次采样之间变化（GC worker、timer 等），
			// 不做逐字节比较；精确截断语义由 single 模式覆盖。
			continue
		}
		switch i {
		case 1:
			ck(n == sz, "one byte short reports n==len(buf)")
			ck(len(s) == n, "truncated dump uses exactly n bytes")
			ck(n < fullN, "truncated dump is shorter than full dump")
			ck(s == full[:n], "truncated dump is a strict prefix of full dump")
			ck(s != full, "truncated content differs from full content")
		case 2:
			ck(n == sz, "exact full size reports n==len(buf) (ambiguous truncation flag)")
			ck(n == fullN, "exact full size equals full dump length")
			ck(s == full, "exact full size still contains the complete dump")
		default:
			ck(n < sz, "buffer larger than full dump reports n<len(buf)")
			ck(n == fullN, "oversized buffers return the same full length")
			ck(s == full, "oversized buffers return identical full content")
		}
	}
	return fullN, full
}

func sbCheckSingle(full string) {
	ck(strings.Count(full, "goroutine ") == 1, "single dump has exactly one goroutine header")
	ck(strings.Contains(full, "main.sbDump("), "single dump has dump caller symbol")
	ck(strings.Contains(full, "main.sbCapture("), "single dump has capture symbol")
	ck(strings.Contains(full, "main.sbProbe("), "single dump has probe symbol")
	ck(strings.Contains(full, "main.main("), "single dump has main symbol")
	ck(lineRe.MatchString(full), "single dump has main.go:line")
}

func sbCheckAll(full string, workers int) {
	headers := headRe.FindAllStringSubmatch(full, -1)
	ck(len(headers) >= workers+1, "all dump has main plus every blocked worker")
	seen := map[int]bool{}
	dup := false
	for _, m := range headers {
		id, _ := strconv.Atoi(m[1])
		if seen[id] {
			dup = true
		}
		seen[id] = true
	}
	ck(!dup, "all dump goroutine ids are unique")
	ck(strings.Count(full, "main.sbBlock(") == workers, "all dump has exactly one frame per worker")
	ck(strings.Count(full, "chan receive") >= workers, "all dump shows every worker wait state")
	ck(strings.Count(full, "created by main.sbLaunch") >= workers, "all dump shows launch owner")
	ck(strings.Contains(full, "main.sbProbe("), "all dump has probe symbol")
	ck(lineRe.MatchString(full), "all dump has main.go:line")
}

//go:noinline
func sbLaunch(workers int) ([]*sbPayload, chan struct{}, <-chan struct{}) {
	release := make(chan struct{})
	ready := make(chan struct{}, workers)
	payloads := make([]*sbPayload, workers)
	var wg sync.WaitGroup
	wg.Add(workers)
	for i := 0; i < workers; i++ {
		p := &sbPayload{}
		payloads[i] = p
		go func() {
			defer wg.Done()
			sbBlock(p, release, ready)
		}()
	}
	for i := 0; i < workers; i++ {
		<-ready
	}
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	return payloads, release, done
}

func main() {
	rounds := envInt("TORTURE_ROUNDS", 2, 1, 16)
	workers := envInt("TORTURE_WORKERS", 6, 2, 32)
	gcStress()

	for round := 0; round < rounds; round++ {
		// 探测期间关闭自动 GC，避免 GC worker 状态变化导致全量 dump 不稳定；
		// 前后仍显式执行 GC。
		oldGC := debug.SetGCPercent(-1)

		// 1) 单 goroutine 的精确边界。
		sbAll = false
		fullN1, full1 := sbProbe()
		ck(fullN1 == len(full1), "single probe returns consistent full length")
		sbCheckSingle(full1)

		// 2) all=true：先让所有 worker 稳定阻塞，再做同样的精确边界探测。
		payloads, release, done := sbLaunch(workers)
		sbAll = true
		var readyDump string
		for tries := 0; tries < 5000; tries++ {
			sbLimit = 1 << 20
			_, s, _ := sbCapture()
			readyDump = s
			if strings.Count(readyDump, "main.sbBlock(") >= workers &&
				strings.Count(readyDump, "chan receive") >= workers {
				break
			}
			runtime.Gosched()
			time.Sleep(time.Millisecond)
		}
		ck(strings.Count(readyDump, "main.sbBlock(") >= workers, "workers reached blocked state before probing")

		fullN2, full2 := sbProbe()
		ck(fullN2 == len(full2), "all probe returns consistent full length")
		sbCheckAll(full2, workers)

		close(release)
		<-done
		debug.SetGCPercent(oldGC)
		for i, p := range payloads {
			ck(atomic.LoadInt64(&p.id) == 1, fmt.Sprintf("worker %d ran after release", i))
		}
		runtime.GC()
		runtime.GC()
	}

	gcStress()
	finish("l14_stack_buf_exact_boundary")
}
