// L03: 每轮调用 noinline 递归函数（大栈帧触发栈增长/复制），主循环持有指针
// 跨 runtime.GC()，另有 GC pump 并发；栈反复收缩再增长。
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

const (
	depth = 12
	iters = 512
	slots = 64
)

type node struct {
	v int64
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

//go:noinline
func recurse(level int, v int64) int64 {
	var pad [512]int64
	pad[level] = v
	if level == 0 {
		return v
	}
	return recurse(level-1, v+1) + pad[level]
}

func main() {
	backing := make([]node, slots)
	for i := range backing {
		backing[i] = node{v: int64(i) + 1}
	}
	p := (*[slots]node)(backing)

	var epoch atomic.Int64
	var wg sync.WaitGroup
	start := make(chan struct{})
	wg.Add(1)
	go func() {
		defer wg.Done()
		<-start
		for r := 0; r < 6; r++ {
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

	var sum, nodeSum int64
	for i := 0; i < iters; i++ {
		r := recurse(depth, int64(i&7))
		q := &p[i&(slots-1)]
		v1 := q.v
		if i%32 == 0 {
			runtime.GC()
		}
		v2 := q.v
		sum += r
		nodeSum += v1 + v2
	}
	now := epoch.Load()
	wg.Wait()

	var expectedR, expectedN int64
	for i := 0; i < iters; i++ {
		expectedR += int64(depth+1)*int64(i&7) + int64(depth*(depth+1)/2)
		expectedN += 2 * (int64(i&(slots-1)) + 1)
	}
	ck(now >= 1, "GC completed during stack-growth loop")
	ck(sum == expectedR, "recursive-call checksum across stack growth")
	ck(nodeSum == expectedN, "pointer value stable across GC calls")
	corrupt := false
	for i := range backing {
		if p[i].v != int64(i)+1 {
			corrupt = true
		}
	}
	ck(!corrupt, "nodes intact after stack-growth GC")
	runtime.KeepAlive(backing)
	runtime.KeepAlive(p)
	finish("l03_loop_with_calls_stack")
}
