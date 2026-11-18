// L03: 长 []*node 指针遍历，期间另一 goroutine 反复 GC；扫描多轮后校验和仍确定。
package main

import (
	"fmt"
	"os"
	"runtime"
	"sync"
	"sync/atomic"
)

var (
	failed int32
	gcKeep [][]byte
)

const n = 1 << 15

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

func main() {
	backing := make([]node, n)
	ptrs := make([]*node, n)
	for i := range backing {
		backing[i] = node{v: int64(i)*3 + 7, s: "n"}
		ptrs[i] = &backing[i]
	}
	var base int64
	for i := range backing {
		base += backing[i].v
	}

	var epoch atomic.Int64
	var wg sync.WaitGroup
	start := make(chan struct{})
	wg.Add(1)
	go func() {
		defer wg.Done()
		<-start
		for r := 0; r < 8; r++ {
			junk := make([][]byte, 16)
			for i := range junk {
				junk[i] = make([]byte, 16384)
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
	for (epoch.Load() < 1 || passes < 3) && passes < 1024 {
		for _, q := range ptrs {
			sum += q.v
		}
		passes++
	}
	now := epoch.Load()
	wg.Wait()

	ck(now >= 1, "GC completed during slice traversal")
	ck(sum == int64(passes)*base, "slice-pointer checksum across GC")
	corrupt := false
	for i := 0; i < n; i++ {
		if ptrs[i].v != int64(i)*3+7 || ptrs[i].s != "n" {
			corrupt = true
			break
		}
	}
	ck(!corrupt, "all nodes intact after GC")
	runtime.KeepAlive(backing)
	runtime.KeepAlive(ptrs)
	finish("l03_long_slice_iteration")
}
