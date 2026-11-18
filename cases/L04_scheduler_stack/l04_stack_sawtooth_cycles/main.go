// L04: 栈增长/收缩锯齿模式：同一个 worker goroutine 反复经历
// 深递归（A/B/C 三种大小帧交替，栈涨到数百 KB）-> 峰值处阻塞并 GC -> 返回后低栈处再 GC。
// 每层帧内保存指针槽，回溯与收缩后逐槽校验指针身份。
package main

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"sync/atomic"
)

type stnode struct {
	v    int64
	next *stnode
}

type sawCtl struct {
	peak    chan struct{}
	release chan struct{}
}

type sawJob struct {
	depth int
	chain []*stnode
	ctl   *sawCtl
	res   chan int64
	ack   chan struct{}
}

var (
	failed int32
	gcSink [][]byte
	sawBad int32
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

//go:noinline
func scanA(f *[768]*stnode, chain []*stnode, level int) int64 {
	var s int64
	for i := range f {
		if f[i] != chain[(i+level)&3] {
			atomic.AddInt32(&sawBad, 1)
			continue
		}
		s += f[i].v
	}
	return s
}

//go:noinline
func scanB(f *[192]*stnode, chain []*stnode, level int) int64 {
	var s int64
	for i := range f {
		if f[i] != chain[(i+level)&3] {
			atomic.AddInt32(&sawBad, 1)
			continue
		}
		s += f[i].v
	}
	return s
}

//go:noinline
func scanC(f *[48]*stnode, chain []*stnode, level int) int64 {
	var s int64
	for i := range f {
		if f[i] != chain[(i+level)&3] {
			atomic.AddInt32(&sawBad, 1)
			continue
		}
		s += f[i].v
	}
	return s
}

//go:noinline
func scanLeaf(f *[1024]*stnode, chain []*stnode, level int) int64 {
	var s int64
	for i := range f {
		if f[i] != chain[(i+level)&3] {
			atomic.AddInt32(&sawBad, 1)
			continue
		}
		s += f[i].v
	}
	return s
}

//go:noinline
func sawA(level, depth int, chain []*stnode, ctl *sawCtl) int64 {
	var f [768]*stnode
	for i := range f {
		f[i] = chain[(i+level)&3]
	}
	var child int64
	if depth == 0 {
		child = sawLeaf(level, chain, ctl)
	} else {
		child = sawB(level+1, depth-1, chain, ctl)
	}
	// 回溯时校验：此时帧槽已跨过峰值 GC，仍是活跃栈数据。
	return scanA(&f, chain, level) + child
}

//go:noinline
func sawB(level, depth int, chain []*stnode, ctl *sawCtl) int64 {
	var f [192]*stnode
	for i := range f {
		f[i] = chain[(i+level)&3]
	}
	var child int64
	if depth == 0 {
		child = sawLeaf(level, chain, ctl)
	} else {
		child = sawC(level+1, depth-1, chain, ctl)
	}
	return scanB(&f, chain, level) + child
}

//go:noinline
func sawC(level, depth int, chain []*stnode, ctl *sawCtl) int64 {
	var f [48]*stnode
	for i := range f {
		f[i] = chain[(i+level)&3]
	}
	var child int64
	if depth == 0 {
		child = sawLeaf(level, chain, ctl)
	} else {
		child = sawA(level+1, depth-1, chain, ctl)
	}
	return scanC(&f, chain, level) + child
}

//go:noinline
func sawLeaf(level int, chain []*stnode, ctl *sawCtl) int64 {
	var f [1024]*stnode
	for i := range f {
		f[i] = chain[(i+level)&3]
	}
	// 峰值：所有父帧与叶帧的指针槽都已填充，停在协调点上等主协程 GC。
	ctl.peak <- struct{}{}
	<-ctl.release
	return scanLeaf(&f, chain, level)
}

func sawFrameSize(level int) int {
	switch level % 3 {
	case 0:
		return 768
	case 1:
		return 192
	default:
		return 48
	}
}

// sawWant 独立计算一次锯齿递归的期望返回值。
func sawWant(depth int, chainSum int64) int64 {
	var t int64
	if depth > 0 {
		for l := 0; l <= depth; l++ {
			t += int64(sawFrameSize(l)) / 4 * chainSum
		}
	}
	t += int64(1024) / 4 * chainSum
	return t
}

func runSawtooth(cycles int, seed int64) {
	jobs := make(chan *sawJob)
	workerDone := make(chan struct{})
	go func() {
		defer close(workerDone)
		for job := range jobs {
			var res int64
			if job.depth == 0 {
				res = sawLeaf(0, job.chain, job.ctl)
			} else {
				res = sawA(0, job.depth, job.chain, job.ctl)
			}
			job.res <- res
			close(job.ack)
		}
	}()

	for c := 0; c < cycles; c++ {
		depth := 180
		if c%2 == 1 {
			depth = 24
		}
		b := seed*1000003 + int64(c)*1009
		chain := make([]*stnode, 4)
		for i := 0; i < 4; i++ {
			chain[i] = &stnode{v: b + int64(i)}
		}
		ctl := &sawCtl{peak: make(chan struct{}), release: make(chan struct{})}
		job := &sawJob{
			depth: depth,
			chain: chain,
			ctl:   ctl,
			res:   make(chan int64, 1),
			ack:   make(chan struct{}),
		}
		jobs <- job

		<-ctl.peak
		// 峰值 GC：depth 层大帧 + 32KB 叶帧同时活跃，栈处于最大使用量。
		gcStress()
		close(ctl.release)
		res := <-job.res
		<-job.ack
		// 返回后低栈 GC：runtime 有机会收缩同一个 goroutine 的栈。
		gcStress()

		sum := int64(0)
		for _, n := range chain {
			if n == nil {
				ckf(false, "cycle %d chain node missing after unwind", c)
				continue
			}
			sum += n.v
		}
		ckf(res == sawWant(depth, sum), "cycle %d depth=%d res=%d want=%d", c, depth, res, sawWant(depth, sum))
		for i := 0; i < 4; i++ {
			ckf(chain[i] != nil && chain[i].v == b+int64(i), "cycle %d chain[%d] corrupted", c, i)
		}
	}

	close(jobs)
	<-workerDone
	gcStress()
	ckf(atomic.LoadInt32(&sawBad) == 0, "pointer slots corrupted during grow/shrink cycles")
}

func main() {
	seed := envSeed()
	runSawtooth(6, seed)
	finish("l04_stack_sawtooth_cycles")
}
