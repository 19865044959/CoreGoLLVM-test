// L14: runtime.Stack 缓冲尺寸语义。小缓冲返回 n==len(buf) 表示截断，且已写内容是
// 完整 dump 的前缀；翻倍增长循环直到 n<len(buf) 才结束。all=false 只含当前 goroutine，
// all=true 含全部阻塞 goroutine。验证头部格式、完整行、真实符号与 main.go:行号。
// TORTURE_ROUNDS/TORTURE_WORKERS 放大轮数与阻塞 goroutine 数。
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

type bufPayload struct {
	id int64
}

var (
	failed   int32
	gcSink   [][]byte
	lineRe   = regexp.MustCompile(`main\.go:\d+`)
	headRe   = regexp.MustCompile(`(?m)^goroutine (\d+) \[`)
	bufLimit int
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

// bufSelfDump / bufAllDump 不接受尺寸参数：尺寸走全局变量，保证不同尺寸下
// 除截断外栈文本完全一致，可以严格比较前缀。
//
//go:noinline
func bufSelfDump() (int, []byte) {
	b := make([]byte, bufLimit)
	n := runtime.Stack(b, false)
	return n, b
}

//go:noinline
func bufAllDump() (int, []byte) {
	b := make([]byte, bufLimit)
	n := runtime.Stack(b, true)
	return n, b
}

//go:noinline
func bufBlocker(p *bufPayload, ch <-chan struct{}, ready chan<- struct{}) {
	ready <- struct{}{}
	<-ch
	atomic.AddInt64(&p.id, 1)
}

//go:noinline
func bufSizeProbe(all bool, sizes []int) (full string, fullN int, prefixOK, zeroTail bool) {
	prefixOK = true
	zeroTail = true
	var trunc [][]byte
	for _, sz := range sizes {
		bufLimit = sz
		var n int
		var b []byte
		if all {
			n, b = bufAllDump()
		} else {
			n, b = bufSelfDump()
		}
		ck(n <= len(b), "runtime.Stack never writes beyond buffer")
		for i := n; i < len(b); i++ {
			if b[i] != 0 {
				zeroTail = false
			}
		}
		if n == sz {
			// 截断：记录已写部分，等完整 dump 出现后统一验证前缀关系。
			trunc = append(trunc, append([]byte(nil), b[:n]...))
			continue
		}
		if fullN != 0 {
			continue
		}
		full = string(b[:n])
		fullN = n
		for _, t := range trunc {
			if len(t) > fullN || string(t) != full[:len(t)] {
				prefixOK = false
			}
		}
	}
	return full, fullN, prefixOK, zeroTail
}

//go:noinline
func bufGrowDump(all bool) (size, n int, ok bool, dump string) {
	size = 16
	for {
		bufLimit = size
		var b []byte
		if all {
			n, b = bufAllDump()
		} else {
			n, b = bufSelfDump()
		}
		if n < size {
			return size, n, true, string(b[:n])
		}
		if size >= 1<<20 {
			return size, n, false, string(b[:n])
		}
		size *= 2
	}
}

func main() {
	rounds := envInt("TORTURE_ROUNDS", 2, 1, 16)
	workers := envInt("TORTURE_WORKERS", 8, 1, 32)
	selfSizes := []int{1, 2, 4, 8, 9, 16, 32, 64, 128, 256, 512, 1024, 2048, 4096, 8192}
	allSizes := []int{1, 2, 4, 8, 9, 16, 32, 64, 128, 256, 512, 1024, 2048, 4096, 8192, 16384, 32768, 65536, 131072}
	gcStress()

	for round := 0; round < rounds; round++ {
		// 1) 单 goroutine dump 的截断/完整语义。
		full1, n1, prefixOK, zeroTail := bufSizeProbe(false, selfSizes)
		ck(n1 > 0 && n1 < selfSizes[len(selfSizes)-1], "single dump fits in sufficiently large buffer")
		ck(prefixOK, "truncated single dumps are prefixes of full dump")
		ck(zeroTail, "runtime.Stack leaves bytes beyond n untouched")
		ck(strings.HasPrefix(full1, "goroutine "), "single dump starts with goroutine header")
		ck(strings.Count(full1, "goroutine ") == 1, "single dump has exactly one goroutine header")
		ck(strings.HasSuffix(full1, "\n"), "full single dump ends with newline")
		ck(strings.Contains(full1, "main.bufSelfDump("), "single dump has runtime.Stack caller symbol")
		ck(strings.Contains(full1, "main.main("), "single dump has main symbol")
		ck(lineRe.MatchString(full1), "single dump has main.go:line")

		// 2) 小于 header 长度的缓冲：写入内容就是 "goroutine " 的前缀。
		for _, sz := range []int{1, 4, 8, 9} {
			bufLimit = sz
			n, b := bufSelfDump()
			ck(n == sz, fmt.Sprintf("buffer %d reports truncation with n==len", sz))
			ck(string(b[:n]) == "goroutine "[:n], fmt.Sprintf("buffer %d contains header prefix", sz))
		}

		// 3) 增长循环：从小缓冲开始翻倍，直到 n<len(buf)。
		sz, gn, ok, gdump := bufGrowDump(false)
		ck(ok, "single dump growth loop terminates")
		ck(sz >= 16 && sz <= 1<<20, "single dump growth size is bounded")
		ck(gn < sz && gn > 0, "single dump growth ends with untruncated buffer")
		ck(sz/2 < gn || sz == 16, "single dump growth stops at first sufficient power of two")
		ck(strings.Contains(gdump, "main.bufGrowDump("), "growth dump has caller symbol")
		ck(strings.Contains(gdump, "main.main("), "growth dump has main symbol")
		ck(lineRe.MatchString(gdump), "growth dump has main.go:line")

		// 4) 全量 dump：放置多个阻塞 goroutine，验证尺寸与内容。
		release := make(chan struct{})
		ready := make(chan struct{}, workers)
		payloads := make([]*bufPayload, workers)
		var wg sync.WaitGroup
		wg.Add(workers)
		for i := 0; i < workers; i++ {
			p := &bufPayload{}
			payloads[i] = p
			go func() {
				defer wg.Done()
				bufBlocker(p, release, ready)
			}()
		}
		for i := 0; i < workers; i++ {
			<-ready
		}
		// 轮询到全部 worker 停在 chan receive。
		deadline := time.Now().Add(5 * time.Second)
		for {
			bufLimit = 1 << 20
			n, b := bufAllDump()
			if n < len(b) && strings.Count(string(b[:n]), "main.bufBlocker(") >= workers &&
				strings.Count(string(b[:n]), "chan receive") >= workers {
				break
			}
			if time.Now().After(deadline) {
				break
			}
			runtime.Gosched()
			time.Sleep(time.Millisecond)
		}

		fullAll, nAll, prefixAll, zeroAll := bufSizeProbe(true, allSizes)
		ck(nAll > 0 && nAll < allSizes[len(allSizes)-1], "all dump fits in sufficiently large buffer")
		ck(prefixAll, "truncated all dumps are prefixes of full dump")
		ck(zeroAll, "all dump leaves bytes beyond n untouched")
		ck(strings.HasPrefix(fullAll, "goroutine "), "all dump starts with goroutine header")
		headers := headRe.FindAllStringSubmatch(fullAll, -1)
		ck(len(headers) >= workers+1, "all dump has header for main and every blocked worker")
		seen := map[string]bool{}
		dup := false
		for _, m := range headers {
			if seen[m[1]] {
				dup = true
			}
			seen[m[1]] = true
		}
		ck(!dup, "all dump goroutine ids are unique")
		ck(strings.Count(fullAll, "main.bufBlocker(") >= workers, "all dump shows every blocked worker")
		ck(strings.Count(fullAll, "chan receive") >= workers, "all dump shows worker wait states")
		ck(strings.Count(fullAll, "created by main.main") >= workers, "all dump shows created-by owner")
		ck(strings.HasSuffix(fullAll, "\n"), "full all dump ends with newline")
		ck(lineRe.MatchString(fullAll), "all dump has main.go:line")

		// 5) all=true 的增长循环。
		asz, an, aok, adump := bufGrowDump(true)
		ck(aok, "all dump growth loop terminates")
		ck(an < asz && an > 0, "all dump growth ends with untruncated buffer")
		ck(strings.Count(adump, "main.bufBlocker(") >= workers, "growth all dump shows every blocked worker")
		ck(strings.Count(adump, "chan receive") >= workers, "growth all dump shows worker wait states")
		ck(len(headRe.FindAllStringSubmatch(adump, -1)) >= workers+1, "growth all dump has all headers")
		ck(lineRe.MatchString(adump), "growth all dump has main.go:line")

		close(release)
		wg.Wait()
		for i, p := range payloads {
			ck(atomic.LoadInt64(&p.id) == 1, fmt.Sprintf("worker %d ran after release", i))
		}
		runtime.GC()
		runtime.GC()
	}

	gcStress()
	finish("l14_stack_buf_growth")
}
