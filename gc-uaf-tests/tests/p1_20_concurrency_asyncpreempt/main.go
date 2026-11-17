// P1-20 (报告风险 A13 异步抢占 + 并发): 多 goroutine 分配/持针，主 goroutine 持续 GC。
package main

import (
	"fmt"
	"os"
	"runtime"
	"sync"
	"sync/atomic"
)

type node struct {
	v    int64
	name string
}

var failed int32

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

// 每个 goroutine 用自己的 sink，避免数据竞争。
func gcChurn(stop <-chan struct{}) {
	var sink [][]byte
	for {
		select {
		case <-stop:
			return
		default:
		}
		for i := 0; i < 8; i++ {
			b := make([]byte, 4096)
			b[0] = byte(i)
			sink = append(sink[:0], b)
		}
		sink = sink[:0]
		runtime.GC()
	}
}

//go:noinline
func worker(id int64, rounds int, errs *int32) {
	p := &node{v: id * 1000, name: "w"}
	sl := make([]*node, 0, 8)
	m := make(map[int]*node)
	for i := 0; i < rounds; i++ {
		p.v++
		sl = append(sl, &node{v: int64(i)})
		m[i] = &node{v: int64(i) + 1}
		q := &node{v: p.v}
		if q.v != p.v || p.name != "w" {
			atomic.AddInt32(errs, 1)
		}
		if i%64 == 0 {
			runtime.GC()
		}
	}
	var sum int64
	for _, e := range sl {
		sum += e.v
	}
	if sum != int64(rounds*(rounds-1)/2) {
		atomic.AddInt32(errs, 1)
	}
	for i := 0; i < rounds; i++ {
		if m[i] == nil || m[i].v != int64(i)+1 {
			atomic.AddInt32(errs, 1)
			break
		}
	}
	// 紧循环：迫使异步抢占落在只持寄存器指针的位置。
	for i := 0; i < 2000000; i++ {
		sum += p.v
	}
	if sum != int64(rounds*(rounds-1)/2)+2000000*p.v {
		atomic.AddInt32(errs, 1)
	}
}

func main() {
	for round := 0; round < 3; round++ {
		errs := int32(0)
		stop := make(chan struct{})
		var wg sync.WaitGroup
		go gcChurn(stop)
		for i := 0; i < 4; i++ {
			wg.Add(1)
			go func(id int) {
				defer wg.Done()
				worker(int64(id), 300, &errs)
			}(i)
		}
		wg.Wait()
		close(stop)
		ck(atomic.LoadInt32(&errs) == 0, "concurrent workers saw corruption")
	}
	finish("p1_20_concurrency_asyncpreempt")
}
