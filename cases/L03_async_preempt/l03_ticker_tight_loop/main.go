// L03: ticker 每滴答分配垃圾并 runtime.GC()，同时主 goroutine 跑持指针的
// 无调用紧循环；固定取 4 个 tick，不对墙钟耗时做任何断言。
package main

import (
	"fmt"
	"os"
	"runtime"
	"sync"
	"sync/atomic"
	"time"
)

var (
	failed    int32
	gcKeep    [][]byte
	tickerGCs int64
)

const (
	slots     = 64
	chunk     = 1 << 16
	blocks    = 64
	total     = chunk * blocks
	gcsWanted = 4
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
	done := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		tk := time.NewTicker(time.Millisecond)
		defer tk.Stop()
		<-start
		for n := 0; n < gcsWanted; n++ {
			<-tk.C
			junk := make([][]byte, 4)
			for i := range junk {
				junk[i] = make([]byte, 4096)
				junk[i][0] = byte(n + i)
			}
			gcKeep = junk
			runtime.GC()
			atomic.AddInt64(&tickerGCs, 1)
		}
		close(done)
	}()
	close(start)

	var sum int64
	for base := 0; base < total; base += chunk {
		for i := 0; i < chunk; i++ {
			idx := (base + i) & (slots - 1)
			sum += p[idx] + int64(idx)
		}
	}
	<-done
	wg.Wait()

	ck(atomic.LoadInt64(&tickerGCs) == gcsWanted, "ticker-driven GC count")
	ck(sum == int64(total)*slots, "tight-loop checksum while ticker GCs")
	corrupt := false
	for i := 0; i < slots; i++ {
		if p[i] != int64(i)+1 {
			corrupt = true
		}
	}
	ck(!corrupt, "pointer target intact after ticker GC")
	runtime.KeepAlive(buf)
	runtime.KeepAlive(p)
	finish("l03_ticker_tight_loop")
}
