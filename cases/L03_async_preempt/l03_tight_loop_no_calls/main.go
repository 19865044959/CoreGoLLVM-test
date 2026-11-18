// L03: 零调用紧循环持续持有堆指针，另一 goroutine 反复触发 GC；
// 循环回边是异步抢占点，GC 完成后校验指针目标与校验和。
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

const (
	slots = 64
	chunk = 1 << 16
)

func main() {
	buf := make([]int64, slots)
	for i := range buf {
		buf[i] = int64(i) + 1
	}
	p := (*[slots]int64)(buf)

	var (
		epoch atomic.Int64
		wg    sync.WaitGroup
	)
	start := make(chan struct{})
	gcBegin := make(chan struct{})
	wg.Add(1)
	go func() {
		defer wg.Done()
		<-start
		close(gcBegin)
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
	<-gcBegin

	// 循环跑到至少一轮完整 GC 结束；块大小固定，校验和可闭式核对。
	var sum, done int64
	for epoch.Load() < 1 && done < 1<<26 {
		for i := int64(0); i < chunk; i++ {
			idx := (done + i) & (slots - 1)
			sum += p[idx] + idx
		}
		done += chunk
	}
	now := epoch.Load()
	wg.Wait()

	ck(now >= 1, "GC completed during tight loop")
	ck(done%slots == 0, "chunk keeps checksum formula exact")
	ck(sum == done*slots, "tight-loop checksum after concurrent GC")
	corrupt := false
	for i := 0; i < slots; i++ {
		if p[i] != int64(i)+1 {
			corrupt = true
		}
	}
	ck(!corrupt, "pointer target intact after GC")
	ck(done > 0, "loop executed")
	runtime.KeepAlive(buf)
	runtime.KeepAlive(p)
	finish("l03_tight_loop_no_calls")
}
