// L03: 字符串操作长循环。每轮对 32KiB 大字符串做 IndexByte/Count/切片，
// 用 strings.Builder + strconv 生成 token 并计 FNV 校验；大字符串与切片跨 GC 存活。
// 每 16 轮显式 GC，另有 GC pump 并发；轮数可由 TORTURE_ROUNDS 放大。
package main

import (
	"fmt"
	"math/rand"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
)

var failed int32

const (
	soSize   = 32768
	soPeriod = 26
	soGCDiv  = 16
)

func ck(ok bool, msg string) {
	if !ok {
		atomic.AddInt32(&failed, 1)
		fmt.Println("FAIL:", msg)
	}
}

func finish(name string) {
	if n := atomic.LoadInt32(&failed); n != 0 {
		fmt.Printf("FAILED %s (%d failed checks)\n", name, n)
		os.Exit(1)
	}
	fmt.Println("ALL PASS " + name)
}

func seedFromEnv() int64 {
	if s := os.Getenv("UAF_SEED"); s != "" {
		if v, err := strconv.ParseInt(s, 10, 64); err == nil {
			return v
		}
	}
	return 1
}

func tortureRounds(def int) int {
	if s := os.Getenv("TORTURE_ROUNDS"); s != "" {
		if v, err := strconv.Atoi(s); err == nil && v > 0 {
			return v
		}
	}
	return def
}

func fnv1a(s string) uint64 {
	var h uint64 = 1469598103934665603
	for i := 0; i < len(s); i++ {
		h ^= uint64(s[i])
		h *= 1099511628211
	}
	return h
}

func main() {
	seed := seedFromEnv()
	if seed < 0 {
		seed = -seed
	}
	rng := rand.New(rand.NewSource(seed))
	step := int64(2*rng.Intn(1024) + 1)
	off := int64(rng.Intn(soSize))
	rounds := tortureRounds(4096)

	buf := make([]byte, soSize)
	for i := range buf {
		buf[i] = byte('a' + i%soPeriod)
	}
	markerPos := int(off%int64(soSize-64)) + 32
	buf[markerPos] = 'Z'
	big := string(buf)

	// 独立参考实现：手工扫描 marker 与 "ab" 次数。
	refMarker := -1
	for i := 0; i < len(big); i++ {
		if big[i] == 'Z' {
			refMarker = i
			break
		}
	}
	refAB := 0
	for i := 0; i+1 < len(big); i++ {
		if big[i] == 'a' && big[i+1] == 'b' {
			refAB++
		}
	}

	var epoch atomic.Int64
	var wg sync.WaitGroup
	start := make(chan struct{})
	wg.Add(1)
	go func() {
		defer wg.Done()
		<-start
		for r := 0; r < 8; r++ {
			junk := make([]byte, 16384)
			for i := 0; i < len(junk); i += 512 {
				junk[i] = byte(r + i)
			}
			runtime.GC()
			epoch.Add(1)
			runtime.KeepAlive(junk)
		}
	}()
	close(start)

	var bad int
	var mainGC int
	var hashAcc uint64
	var lastSub string
	var sink []byte

	for r := 0; r < rounds; r++ {
		if strings.IndexByte(big, 'Z') != refMarker {
			bad++
		}
		if strings.Count(big, "ab") != refAB {
			bad++
		}

		startPos := int((int64(r)*step + off) % int64(soSize-1024))
		sub := big[startPos : startPos+1024]
		wantMarker := -1
		if markerPos >= startPos && markerPos < startPos+1024 {
			wantMarker = markerPos - startPos
		}
		if strings.IndexByte(sub, 'Z') != wantMarker {
			bad++
		}
		if len(sub) != 1024 || sub[0] != big[startPos] || sub[1023] != big[startPos+1023] {
			bad++
		}

		var b strings.Builder
		b.Grow(64)
		b.WriteString("r=")
		b.WriteString(strconv.Itoa(r))
		b.WriteByte(':')
		b.WriteString(sub[:16])
		tok := b.String()
		if len(tok) != 2+len(strconv.Itoa(r))+1+16 {
			bad++
		}
		if !strings.HasPrefix(tok, "r=") || !strings.Contains(tok, ":"+sub[:16]) {
			bad++
		}
		hashAcc ^= fnv1a(tok)
		lastSub = sub

		if r%soGCDiv == 0 {
			sink = make([]byte, 8192)
			sink[0] = byte(r)
			runtime.GC()
			mainGC++
		}
	}
	for epoch.Load() < 1 {
		runtime.Gosched()
	}
	now := epoch.Load()
	wg.Wait()

	var refHash uint64
	for r := 0; r < rounds; r++ {
		startPos := int((int64(r)*step + off) % int64(soSize-1024))
		sub := big[startPos : startPos+1024]
		var b strings.Builder
		b.Grow(64)
		b.WriteString("r=")
		b.WriteString(strconv.Itoa(r))
		b.WriteByte(':')
		b.WriteString(sub[:16])
		refHash ^= fnv1a(b.String())
	}
	lastStart := int((int64(rounds-1)*step + off) % int64(soSize-1024))
	wantLast := big[lastStart : lastStart+1024]

	ck(now >= 1, fmt.Sprintf("concurrent GC ran during string loop (seed=%d)", seed))
	ck(refMarker >= 0 && refAB > 0, fmt.Sprintf("reference scan found marker and pairs (seed=%d)", seed))
	ck(bad == 0, fmt.Sprintf("string scan/builder checks (seed=%d)", seed))
	ck(hashAcc == refHash, fmt.Sprintf("builder token hash stable across GC (seed=%d)", seed))
	ck(len(lastSub) == 1024 && lastSub == wantLast, fmt.Sprintf("last slice intact after GC (seed=%d)", seed))
	ck(lastSub == big[lastStart:lastStart+1024], fmt.Sprintf("substring keeps backing string alive (seed=%d)", seed))
	ck(mainGC == (rounds+soGCDiv-1)/soGCDiv, fmt.Sprintf("explicit GC every %d rounds (seed=%d)", soGCDiv, seed))
	ck(strings.IndexByte(big, 'Z') == refMarker, fmt.Sprintf("big string intact after final GC (seed=%d)", seed))
	runtime.KeepAlive(big)
	runtime.KeepAlive(lastSub)
	runtime.KeepAlive(sink)
	finish("l03_string_ops_loop")
}
