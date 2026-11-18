// L03: atomic 自旋循环持指针等待 GC worker 放行；随后固定次数的原子累加校验。
// 自旋体无函数调用，依赖异步抢占让 GC worker 推进。
package main

import (
	"fmt"
	"os"
	"runtime"
	"sync"
	"sync/atomic"
)

var (
	failed  int32
	gcKeep  [][]byte
	gate    atomic.Int32
	entered atomic.Int32
)

const (
	slots      = 64
	guardSpins = 1 << 30
	workIters  = 1 << 16
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

func main() {
	buf := make([]int64, slots)
	for i := range buf {
		buf[i] = int64(i) + 1
	}
	p := (*[slots]int64)(buf)

	start := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		<-start
		for entered.Load() == 0 {
			runtime.Gosched()
		}
		for r := 0; r < 8; r++ {
			junk := make([][]byte, 8)
			for i := range junk {
				junk[i] = make([]byte, 8192)
				junk[i][0] = byte(r + i)
			}
			gcKeep = junk
			runtime.GC()
		}
		gcKeep = nil
		gate.Store(1)
	}()
	close(start)

	var spins, acc int64
	for gate.Load() == 0 {
		if spins == 0 {
			entered.Store(1)
		}
		acc += p[spins&(slots-1)]
		spins++
		if spins >= guardSpins {
			break
		}
	}

	ck(gate.Load() == 1, "GC worker released atomic gate")
	ck(spins > 0, "spin loop executed")
	full := spins / slots
	rem := spins % slots
	expectedSpin := full*(slots*(slots+1)/2) + rem*(rem+1)/2
	ck(acc == expectedSpin, "spin-loop pointer checksum")

	var total atomic.Int64
	for i := int64(0); i < workIters; i++ {
		idx := i & (slots - 1)
		total.Add(p[idx]*2 + idx)
	}
	expectedWork := (int64(workIters) / slots) * int64(3*(slots*(slots-1)/2)+2*slots)
	ck(total.Load() == expectedWork, "post-spin atomic accumulation")

	wg.Wait()
	corrupt := false
	for i := range buf {
		if p[i] != int64(i)+1 {
			corrupt = true
		}
	}
	ck(!corrupt, "pointer target intact after spin GC")
	runtime.KeepAlive(buf)
	runtime.KeepAlive(p)
	finish("l03_atomic_spin_loop")
}
