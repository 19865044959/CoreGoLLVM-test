// L03: 长循环每轮用变量除数做除法/取模，再由商与余数计算节点下标；
// 除数与模数由 UAF_SEED 决定、运行时不可常量折叠；GC pump 并发且周期显式 GC。
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
	dvKeep []byte
)

const (
	dvSlots = 4093
	dvMin   = 1 << 17
	dvCap   = 1 << 23
	dvGCDiv = 1 << 15
)

type dvNode struct {
	v   int64
	tag string
}

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

func dvIdxAt(i, div, mul, off, mod int64) int {
	q := i / div
	r := i % div
	return int((q*mul + r*7 + off) % mod)
}

func dvExpected(i, div, mul, off, mod int64) int64 {
	idx := dvIdxAt(i, div, mul, off, mod)
	v := int64(idx)*5 + 2
	return v + (v >> 3)
}

func main() {
	seed := seedFromEnv()
	if seed < 0 {
		seed = -seed
	}
	div := int64(5 + seed%7)
	mul := int64(13 + seed%5)
	mod := int64(dvSlots + seed%2)
	off := seed % mod

	backing := make([]dvNode, dvSlots+1)
	for i := range backing {
		backing[i].v = int64(i)*5 + 2
		backing[i].tag = "v"
	}

	var epoch atomic.Int64
	var wg sync.WaitGroup
	start := make(chan struct{})
	wg.Add(1)
	go func() {
		defer wg.Done()
		<-start
		for r := 0; r < 8; r++ {
			junk := make([][]byte, 8)
			for i := range junk {
				junk[i] = make([]byte, 8192)
				junk[i][0] = byte(r + i)
			}
			gcKeep = junk
			runtime.GC()
			epoch.Add(1)
		}
		gcKeep = nil
	}()
	close(start)

	var sum int64
	var done int64
	for (epoch.Load() < 1 || done < dvMin) && done < dvCap {
		idx := dvIdxAt(done, div, mul, off, mod)
		p := &backing[idx]
		sum += p.v + (p.v >> 3)
		if done%dvGCDiv == 0 {
			dvKeep = make([]byte, 16384)
			dvKeep[0] = byte(done)
			runtime.GC()
		}
		done++
	}
	now := epoch.Load()
	wg.Wait()

	var expected int64
	for i := int64(0); i < done; i++ {
		expected += dvExpected(i, div, mul, off, mod)
	}

	ck(now >= 1, fmt.Sprintf("GC pump completed during div/mod loop (seed=%d)", seed))
	ck(done >= dvMin, fmt.Sprintf("div/mod loop executed (seed=%d)", seed))
	ck(sum == expected, fmt.Sprintf("div/mod index checksum (seed=%d)", seed))

	corrupt := false
	for i := range backing {
		if backing[i].v != int64(i)*5+2 || backing[i].tag != "v" {
			corrupt = true
		}
	}
	ck(!corrupt, fmt.Sprintf("div/mod nodes intact after GC (seed=%d)", seed))
	runtime.KeepAlive(backing)
	runtime.KeepAlive(dvKeep)
	finish("l03_divmod_index_loop")
}
