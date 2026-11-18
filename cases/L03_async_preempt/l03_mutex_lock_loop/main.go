// L03: mutex 加解锁长循环。主 goroutine 与 helper 每轮各自 Lock/Unlock 共享结构，
// 主 goroutine 在锁内取节点指针、解锁后跨显式 GC 做无调用解引用；helper 在锁内
// 轮换节点指针并累加计数。结束后校验总计数、指针目标与校验和。
package main

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"sync"
	"sync/atomic"
)

var failed int32

const (
	muIters       = 131072
	muGCDiv       = 4096
	muHelperGCDiv = 16384
	muInner       = 16
	muVal         = 100
)

type muNode struct {
	v   int64
	tag string
}

type muShared struct {
	mu      sync.Mutex
	counter int64
	node    *muNode
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
	if seed < 0 {
		seed = -seed
	}

	startNode := int(seed % 2)
	nodes := []muNode{{v: muVal, tag: "mu"}, {v: muVal, tag: "mu"}}
	s := &muShared{node: &nodes[startNode]}

	var epoch atomic.Int64
	var pumpWG sync.WaitGroup
	start := make(chan struct{})
	pumpWG.Add(1)
	go func() {
		defer pumpWG.Done()
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

	var helperWG sync.WaitGroup
	var helperGC int
	helperWG.Add(1)
	go func() {
		defer helperWG.Done()
		<-start
		for i := 0; i < muIters; i++ {
			s.mu.Lock()
			s.counter++
			s.node = &nodes[i&1]
			s.mu.Unlock()
			if i%muHelperGCDiv == 0 {
				junk := make([]byte, 8192)
				junk[0] = byte(i)
				runtime.GC()
				helperGC++
				runtime.KeepAlive(junk)
			}
		}
	}()
	close(start)

	var sum int64
	var bad, mainGC int
	var sink []byte
	for i := 0; i < muIters; i++ {
		s.mu.Lock()
		q := s.node
		s.counter++
		s.mu.Unlock()

		if i%muGCDiv == 0 {
			sink = make([]byte, 8192)
			sink[0] = byte(i)
			runtime.GC()
			mainGC++
		}

		var local int64
		for k := 0; k < muInner; k++ {
			local += q.v
		}
		if local != muInner*muVal || q.tag != "mu" {
			bad++
		}
		sum += local + int64(q.tag[0])
	}
	for epoch.Load() < 1 {
		runtime.Gosched()
	}
	helperWG.Wait()
	pumpWG.Wait()

	ck(epoch.Load() >= 1, fmt.Sprintf("concurrent GC pump ran (seed=%d)", seed))
	ck(s.counter == 2*muIters, fmt.Sprintf("mutex-protected counter lost no updates (seed=%d)", seed))
	ck(bad == 0, fmt.Sprintf("node pointer readable after unlock+GC (seed=%d)", seed))
	ck(sum == int64(muIters)*(muInner*muVal+int64("mu"[0])),
		fmt.Sprintf("mutex loop checksum (seed=%d)", seed))
	ck(mainGC == muIters/muGCDiv, fmt.Sprintf("main explicit GC every %d locks (seed=%d)", muGCDiv, seed))
	ck(helperGC == (muIters+muHelperGCDiv-1)/muHelperGCDiv,
		fmt.Sprintf("helper explicit GC every %d locks (seed=%d)", muHelperGCDiv, seed))
	ck(s.node != nil && s.node.v == muVal && s.node.tag == "mu",
		fmt.Sprintf("shared node intact after final GC (seed=%d)", seed))
	corrupt := false
	for i := range nodes {
		if nodes[i].v != muVal || nodes[i].tag != "mu" {
			corrupt = true
		}
	}
	ck(!corrupt, fmt.Sprintf("mutex nodes intact after GC (seed=%d)", seed))
	runtime.KeepAlive(nodes)
	runtime.KeepAlive(s)
	runtime.KeepAlive(sink)
	finish("l03_mutex_lock_loop")
}
