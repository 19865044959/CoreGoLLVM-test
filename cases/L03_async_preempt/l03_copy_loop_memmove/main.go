// L03: 大块 copy（memmove）循环 + 并发 GC；每轮复制后全量校验和对比，
// 默认规模快，TORTURE_ROUNDS 可放大。填充字节由 UAF_SEED 决定。
package main

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"sync"
	"sync/atomic"
)

var (
	failed int32
	gcKeep [][]byte
)

const size = 1 << 20

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

func main() {
	seed := seedFromEnv()
	rounds := tortureRounds(24)
	fill := byte(seed*37 + 11)

	src := make([]byte, size)
	for i := range src {
		src[i] = fill ^ byte(i*7)
	}
	dst := make([]byte, size)

	var refSum uint64
	for i := 0; i < size; i++ {
		refSum += uint64(src[i])
	}

	var epoch atomic.Int64
	var wg sync.WaitGroup
	start := make(chan struct{})
	wg.Add(1)
	go func() {
		defer wg.Done()
		<-start
		for r := 0; r < 8; r++ {
			junk := make([][]byte, 4)
			for i := range junk {
				junk[i] = make([]byte, 32768)
				junk[i][0] = byte(r + i)
			}
			gcKeep = junk
			runtime.GC()
			epoch.Add(1)
		}
		gcKeep = nil
	}()
	close(start)

	var copied int64
	var lastSum uint64
	for r := 0; r < rounds; r++ {
		n := copy(dst, src)
		copied += int64(n)
		var s uint64
		for i := 0; i < size; i++ {
			s += uint64(dst[i])
		}
		lastSum = s
		if r%8 == 7 {
			runtime.GC()
		}
	}
	now := epoch.Load()
	wg.Wait()

	ck(now >= 1, fmt.Sprintf("GC completed during copy loop (seed=%d)", seed))
	ck(copied == int64(rounds)*size, fmt.Sprintf("copy byte count (seed=%d)", seed))
	ck(lastSum == refSum, fmt.Sprintf("copied data checksum (seed=%d)", seed))
	corrupt := false
	for i := 0; i < size; i += 4096 {
		if dst[i] != fill^byte(i*7) {
			corrupt = true
			break
		}
	}
	ck(!corrupt, fmt.Sprintf("copied bytes intact after GC (seed=%d)", seed))
	runtime.KeepAlive(src)
	runtime.KeepAlive(dst)
	finish("l03_copy_loop_memmove")
}
