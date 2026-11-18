// L04: goroutine 定时睡眠/唤醒，栈上持指针与堆切片；唤醒点插入 GC，
// 每轮睡眠后校验局部数据仍是自己写入的值。
package main

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"sync/atomic"
	"time"
)

type node struct{ v int64 }

var (
	failed int32
	gcSink [][]byte
)

func ck(ok bool, msg string) {
	if !ok {
		atomic.AddInt32(&failed, 1)
		fmt.Println("FAIL:", msg)
	}
}

func ckf(ok bool, format string, args ...any) {
	if !ok {
		atomic.AddInt32(&failed, 1)
		fmt.Printf("FAIL: "+format+"\n", args...)
	}
}

func gcStress() {
	for i := 0; i < 8; i++ {
		b := make([]byte, 4096)
		b[0] = byte(i)
		gcSink = append(gcSink[:0], b)
	}
	gcSink = gcSink[:0]
	runtime.GC()
	runtime.GC()
}

func finish(name string) {
	if n := atomic.LoadInt32(&failed); n != 0 {
		fmt.Printf("FAILED %s (%d failed checks)\n", name, n)
		os.Exit(1)
	}
	fmt.Println("ALL PASS " + name)
}

func envSeed() int64 {
	s := os.Getenv("UAF_SEED")
	if s == "" {
		return 1
	}
	v, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 1
	}
	return v
}

type sleepResult struct {
	id    int
	final int64
	vals  [8]int64
	bad   int32
}

func sleepWorker(id, cycles int, seed int64, ready chan<- int, release <-chan struct{}, out chan<- sleepResult) {
	initial := int64(id)*17 + seed*3
	p := &node{v: initial}
	vals := make([]int64, 8)
	var bad int32

	for c := 0; c < cycles; c++ {
		want := initial + int64(c+1)
		p.v = want
		vals[c%8] = want
		scratch := make([]byte, 512)
		scratch[c%len(scratch)] = byte(c + id)
		runtime.KeepAlive(scratch)
		// 固定次数的短睡眠：只为制造“休眠—唤醒”调度，不作为通过/失败判据。
		time.Sleep(time.Millisecond)
		if p.v != want || vals[c%8] != want {
			bad = 1
		}
		if c == cycles/2 {
			ready <- id
			<-release
			// 主协程已在所有 worker 睡眠/阻塞时执行过 GC。
			if p.v != want || vals[c%8] != want {
				bad = 1
			}
		}
	}

	res := sleepResult{id: id, final: p.v, bad: bad}
	copy(res.vals[:], vals)
	out <- res
}

func main() {
	seed := envSeed()
	const workers = 8
	const cycles = 24

	ready := make(chan int, workers)
	release := make(chan struct{})
	out := make(chan sleepResult, workers)
	for id := 0; id < workers; id++ {
		go sleepWorker(id, cycles, seed, ready, release, out)
	}
	for i := 0; i < workers; i++ {
		<-ready
	}
	gcStress()
	close(release)

	seen := make([]bool, workers)
	for i := 0; i < workers; i++ {
		r := <-out
		if r.id < 0 || r.id >= workers {
			ckf(false, "bad worker id %d", r.id)
			continue
		}
		seen[r.id] = true
		initial := int64(r.id)*17 + seed*3
		ckf(r.bad == 0, "worker %d pointer/value corrupted across sleep+GC", r.id)
		ckf(r.final == initial+cycles, "worker %d final=%d want=%d", r.id, r.final, initial+cycles)
		for j := 0; j < len(r.vals); j++ {
			last := cycles - len(r.vals) + j
			want := initial + int64(last+1)
			ckf(r.vals[j] == want, "worker %d vals[%d]=%d want=%d", r.id, j, r.vals[j], want)
		}
	}
	for id, ok := range seen {
		ckf(ok, "worker %d missing result", id)
	}
	finish("l04_sleep_wake_pointer")
}
