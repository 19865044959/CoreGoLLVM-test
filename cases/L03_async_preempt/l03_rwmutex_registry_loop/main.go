// L03: RWMutex 读多写少循环。writer 每轮分配新的不可变节点并替换注册表
// 槽位；主 goroutine 与第二个 reader 在 RLock 内取节点指针，RUnlock 后跨
// 显式 GC 解引用。另有 GC pump 并发，校验读和、写次数与最终注册表状态。
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
	rwKeep [][]byte
)

const (
	rwSlots       = 128
	rwIters       = 20000
	rwReaderIters = 20000
	rwWriterIters = 20000
	rwGCDiv       = 2048
	rwWriterGCDiv = 8192
	rwInner       = 8
)

type rwNode struct {
	id  int64
	gen int64
	v   int64
	tag string
}

type rwReg struct {
	mu    sync.RWMutex
	slots []*rwNode
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

func rwVal(id int64) int64 { return id*3 + 1 }

func main() {
	seed := seedFromEnv()
	if seed < 0 {
		seed = -seed
	}
	rng := rand.New(rand.NewSource(seed))
	step := int64(2*rng.Intn(31) + 1)
	off := int64(rng.Intn(rwSlots))

	reg := &rwReg{slots: make([]*rwNode, rwSlots)}
	for i := 0; i < rwSlots; i++ {
		reg.slots[i] = &rwNode{id: int64(i), gen: 0, v: rwVal(int64(i)), tag: "rw"}
	}

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
			rwKeep = junk
			runtime.GC()
			epoch.Add(1)
		}
		rwKeep = nil
	}()

	var writerWG sync.WaitGroup
	var writerCount, writerGC int64
	writerWG.Add(1)
	go func() {
		defer writerWG.Done()
		<-start
		for i := 0; i < rwWriterIters; i++ {
			idx := i & (rwSlots - 1)
			nd := &rwNode{id: int64(idx), gen: int64(i), v: rwVal(int64(idx)), tag: "rw"}
			reg.mu.Lock()
			reg.slots[idx] = nd
			reg.mu.Unlock()
			writerCount++
			if i%rwWriterGCDiv == 0 {
				junk := make([]byte, 8192)
				junk[0] = byte(i)
				runtime.GC()
				writerGC++
				runtime.KeepAlive(junk)
			}
		}
	}()

	var reader2WG sync.WaitGroup
	var reader2Count, reader2Bad int64
	reader2WG.Add(1)
	go func() {
		defer reader2WG.Done()
		<-start
		var bad int64
		for i := 0; i < rwReaderIters; i++ {
			idx := (i / 3) & (rwSlots - 1)
			reg.mu.RLock()
			p := reg.slots[idx]
			reg.mu.RUnlock()
			if p == nil || p.id != int64(idx) || p.v != rwVal(int64(idx)) || p.tag != "rw" {
				bad++
			}
			reader2Count++
		}
		reader2Bad = bad
	}()
	close(start)

	var sum, expSum int64
	var mainBad, mainGC int64
	for i := 0; i < rwIters; i++ {
		idx := int((int64(i)*step + off) % rwSlots)
		reg.mu.RLock()
		p := reg.slots[idx]
		reg.mu.RUnlock()

		if i%rwGCDiv == 0 {
			junk := make([]byte, 16384)
			junk[0] = byte(i)
			runtime.GC()
			mainGC++
			runtime.KeepAlive(junk)
		}
		var local int64
		if p != nil {
			for k := 0; k < rwInner; k++ {
				local += p.v
			}
		}
		if p == nil || p.id != int64(idx) || p.v != rwVal(int64(idx)) || p.tag != "rw" ||
			local != int64(rwInner)*rwVal(int64(idx)) {
			mainBad++
			continue
		}
		sum += local + p.id
		expSum += int64(rwInner)*rwVal(int64(idx)) + int64(idx)
	}
	writerWG.Wait()
	reader2WG.Wait()
	for epoch.Load() < 1 {
		runtime.Gosched()
	}
	now := epoch.Load()
	pumpWG.Wait()

	var finalBad int64
	reg.mu.RLock()
	for i, p := range reg.slots {
		if p == nil || p.id != int64(i) || p.v != rwVal(int64(i)) || p.tag != "rw" {
			finalBad++
		}
	}
	reg.mu.RUnlock()

	ck(now >= 1, fmt.Sprintf("GC pump completed during RWMutex loop (seed=%d)", seed))
	ck(mainBad == 0, fmt.Sprintf("pointer read under RLock survives unlock+GC (seed=%d)", seed))
	ck(sum == expSum, fmt.Sprintf("RWMutex read checksum (seed=%d)", seed))
	ck(writerCount == rwWriterIters, fmt.Sprintf("writer update count (seed=%d)", seed))
	ck(writerGC == (rwWriterIters-1)/rwWriterGCDiv+1,
		fmt.Sprintf("writer explicit GC count (seed=%d)", seed))
	ck(reader2Count == rwReaderIters && reader2Bad == 0,
		fmt.Sprintf("second reader checksum (seed=%d)", seed))
	ck(mainGC == (rwIters-1)/rwGCDiv+1, fmt.Sprintf("main explicit GC during loop (seed=%d)", seed))
	ck(finalBad == 0, fmt.Sprintf("final registry intact after GC (seed=%d)", seed))
	runtime.KeepAlive(reg)
	runtime.KeepAlive(rwKeep)
	finish("l03_rwmutex_registry_loop")
}
