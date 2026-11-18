// L03: 嵌套循环内局部指针跨无调用内层循环存活，另一 goroutine 并发 GC；
// 行顺序由 UAF_SEED 决定，校验和与顺序无关。
package main

import (
	"fmt"
	"math/rand"
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

const (
	rows  = 48
	cols  = 128
	inner = 8
)

type node struct {
	v int64
	s string
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

func main() {
	seed := seedFromEnv()

	backing := make([]node, rows*cols)
	ptrs := make([]*node, rows*cols)
	for i := range backing {
		backing[i] = node{v: int64(i%97)*5 + int64(i/97) + 2, s: "n"}
		ptrs[i] = &backing[i]
	}
	rowPtrs := make([][]*node, rows)
	var base int64
	for r := 0; r < rows; r++ {
		rowPtrs[r] = ptrs[r*cols : (r+1)*cols]
		for _, q := range rowPtrs[r] {
			base += q.v
		}
	}
	rng := rand.New(rand.NewSource(seed))
	order := rng.Perm(rows)

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
	passes := 0
	for (epoch.Load() < 1 || passes < 2) && passes < 512 {
		for _, r := range order {
			for j := 0; j < cols; j++ {
				q := rowPtrs[r][j]
				for k := 0; k < inner; k++ {
					sum += q.v
				}
			}
		}
		passes++
	}
	now := epoch.Load()
	wg.Wait()

	ck(now >= 1, fmt.Sprintf("GC completed during nested loops (seed=%d)", seed))
	ck(sum == int64(passes)*base*inner, fmt.Sprintf("nested-loop checksum (seed=%d)", seed))
	corrupt := false
	for r := 0; r < rows; r++ {
		for _, q := range rowPtrs[r] {
			if q.s != "n" {
				corrupt = true
			}
		}
	}
	ck(!corrupt, fmt.Sprintf("nodes intact after nested-loop GC (seed=%d)", seed))
	runtime.KeepAlive(backing)
	runtime.KeepAlive(ptrs)
	finish("l03_nested_loops_pointers")
}
