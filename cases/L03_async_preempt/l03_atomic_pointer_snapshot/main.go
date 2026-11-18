// L03: atomic.Pointer 快照发布循环。主 goroutine 每轮构造不可变配置并用
// CompareAndSwap 发布，随后 Load 取回快照、跨显式 GC 解引用节点；后台
// reader 持续 Load 并校验快照自洽。另有 GC pump 并发。
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
	apKeep [][]byte
)

const (
	apIters       = 20000
	apReaderIters = 20000
	apGCDiv       = 1024
	apReaderGCDiv = 4096
)

type apNode struct {
	v   int64
	tag string
}

type apCfg struct {
	ver  int64
	sum  int64
	node *apNode
	tag  string
}

var apPub atomic.Pointer[apCfg]

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

func apVal(ver int64) int64 { return ver*7 + 1 }

func main() {
	seed := seedFromEnv()
	if seed < 0 {
		seed = -seed
	}
	yieldEvery := apReaderGCDiv + int(seed%64)

	initCfg := &apCfg{ver: 0, sum: 5, node: &apNode{v: apVal(0), tag: "ap"}, tag: "cfg"}
	apPub.Store(initCfg)

	var epoch atomic.Int64
	var pumpWG sync.WaitGroup
	start := make(chan struct{})
	pumpWG.Add(1)
	go func() {
		defer pumpWG.Done()
		<-start
		for r := 0; r < 8; r++ {
			junk := make([][]byte, 8)
			for i := range junk {
				junk[i] = make([]byte, 8192)
				junk[i][0] = byte(r + i)
			}
			apKeep = junk
			runtime.GC()
			epoch.Add(1)
		}
		apKeep = nil
	}()

	var readerWG sync.WaitGroup
	var readerCount, readerBad int64
	readerWG.Add(1)
	go func() {
		defer readerWG.Done()
		<-start
		var bad int64
		for i := 0; i < apReaderIters; i++ {
			c := apPub.Load()
			if c == nil || c.node == nil || c.ver < 0 || c.ver > apIters ||
				c.node.v != apVal(c.ver) || c.sum != c.node.v*2+3 ||
				c.tag != "cfg" || c.node.tag != "ap" {
				bad++
			}
			if i%yieldEvery == 0 {
				runtime.Gosched()
			}
			readerCount++
		}
		readerBad = bad
	}()
	close(start)

	var sum, expSum int64
	var badSwap, badUse, mainGC int64
	for i := int64(1); i <= apIters; i++ {
		nd := &apNode{v: apVal(i), tag: "ap"}
		cfg := &apCfg{ver: i, sum: nd.v*2 + 3, node: nd, tag: "cfg"}
		for {
			old := apPub.Load()
			if apPub.CompareAndSwap(old, cfg) {
				break
			}
		}
		got := apPub.Load()
		if got != cfg || got.ver != i {
			badSwap++
		}
		if i%apGCDiv == 0 {
			junk := make([]byte, 16384)
			junk[0] = byte(i)
			runtime.GC()
			mainGC++
			runtime.KeepAlive(junk)
		}
		q := got.node
		var local int64
		if q != nil {
			for k := 0; k < 4; k++ {
				local += q.v
			}
		}
		if q == nil || q.v != apVal(i) || q.tag != "ap" ||
			got.sum != q.v*2+3 || local != 4*apVal(i) {
			badUse++
			continue
		}
		sum += local + got.ver
	}
	expSum = 0
	for i := int64(1); i <= apIters; i++ {
		expSum += 4*apVal(i) + i
	}
	readerWG.Wait()
	for epoch.Load() < 1 {
		runtime.Gosched()
	}
	now := epoch.Load()
	pumpWG.Wait()

	final := apPub.Load()

	ck(now >= 1, fmt.Sprintf("GC pump completed during atomic.Pointer loop (seed=%d)", seed))
	ck(badSwap == 0, fmt.Sprintf("CompareAndSwap published each snapshot (seed=%d)", seed))
	ck(badUse == 0, fmt.Sprintf("snapshot node readable after GC (seed=%d)", seed))
	ck(sum == expSum, fmt.Sprintf("atomic snapshot checksum (seed=%d)", seed))
	ck(readerCount == apReaderIters && readerBad == 0,
		fmt.Sprintf("concurrent reader saw only consistent snapshots (seed=%d)", seed))
	ck(mainGC == apIters/apGCDiv, fmt.Sprintf("main explicit GC during loop (seed=%d)", seed))
	ck(final != nil && final.ver == apIters && final.node != nil &&
		final.node.v == apVal(apIters) && final.node.tag == "ap",
		fmt.Sprintf("final published snapshot intact (seed=%d)", seed))
	runtime.KeepAlive(initCfg)
	runtime.KeepAlive(final)
	runtime.KeepAlive(apKeep)
	finish("l03_atomic_pointer_snapshot")
}
