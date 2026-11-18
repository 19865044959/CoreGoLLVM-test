// L04: 固定 worker 池跨波次复用 goroutine；每个任务在 worker 栈上进入深递归，
// 大指针帧触发多次 morestack/栈复制。所有 worker 在叶帧阻塞时 GC，返回后再 GC
// 触发栈收缩，下一波复用同一批 goroutine，校验帧内指针与跨任务隔离。
package main

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"sync"
	"sync/atomic"
)

type pnode struct {
	v    int64
	next *pnode
}

type poolTask struct {
	rot     int
	depth   int
	chain   []*pnode
	peak    chan<- int
	release <-chan struct{}
	result  chan int64
}

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

func envInt(name string, def, lo, hi int) int {
	s := os.Getenv(name)
	if s == "" {
		return def
	}
	n, err := strconv.Atoi(s)
	if err != nil || n < lo {
		return def
	}
	if n > hi {
		n = hi
	}
	return n
}

func chainAt(chain []*pnode, idx int) *pnode {
	return chain[idx&3]
}

//go:noinline
func poolRec(level, depth, rot int, chain []*pnode, peak chan<- int, release <-chan struct{}) int64 {
	var frame [16]*pnode
	for i := range frame {
		frame[i] = chainAt(chain, i+level+rot)
	}
	if level == depth {
		var leaf [2048]*pnode
		for i := range leaf {
			leaf[i] = chainAt(chain, i+level+rot)
		}
		// 最深层：所有父帧与叶帧的指针槽都活跃，等主协程 GC。
		peak <- rot
		<-release
		var s int64
		for i := range leaf {
			if leaf[i] != chainAt(chain, i+level+rot) {
				return -1 << 60
			}
			s += leaf[i].v
		}
		for i := range frame {
			if frame[i] != chainAt(chain, i+level+rot) {
				return -1 << 60
			}
			s += frame[i].v
		}
		return s
	}
	child := poolRec(level+1, depth, rot, chain, peak, release)
	if child == -1<<60 {
		return child
	}
	var s int64
	for i := range frame {
		if frame[i] != chainAt(chain, i+level+rot) {
			return -1 << 60
		}
		s += frame[i].v
	}
	return s + child
}

func poolWant(depth, rot int, chain []*pnode) int64 {
	var s int64
	for d := 0; d <= depth; d++ {
		for i := 0; i < 16; i++ {
			s += chainAt(chain, i+d+rot).v
		}
	}
	for i := 0; i < 2048; i++ {
		s += chainAt(chain, i+depth+rot).v
	}
	return s
}

func makeChain(seed int64, rot int) []*pnode {
	b := seed*100003 + int64(rot)*7919
	return []*pnode{
		{v: b + 3},
		{v: b + 5},
		{v: b + 7},
		{v: b + 11},
	}
}

func runPoolWaves(seed int64, workers, waves int) {
	jobs := make(chan poolTask)
	var wg sync.WaitGroup
	wg.Add(workers)
	for w := 0; w < workers; w++ {
		go func() {
			defer wg.Done()
			for t := range jobs {
				t.result <- poolRec(0, t.depth, t.rot, t.chain, t.peak, t.release)
			}
		}()
	}

	var keep []*pnode
	var keepWant []int64
	var total, wantTotal int64
	for w := 0; w < waves; w++ {
		depth := 230 + w*17 + int(seed%23)
		peak := make(chan int, workers)
		release := make(chan struct{})
		tasks := make([]poolTask, workers)
		for id := 0; id < workers; id++ {
			rot := w*100 + id
			tasks[id] = poolTask{
				rot:     rot,
				depth:   depth,
				chain:   makeChain(seed+int64(w), rot),
				peak:    peak,
				release: release,
				result:  make(chan int64, 1),
			}
			for _, n := range tasks[id].chain {
				keep = append(keep, n)
				keepWant = append(keepWant, n.v)
			}
			jobs <- tasks[id]
		}
		// 全部 worker 停在最深叶帧上时 GC。
		for i := 0; i < workers; i++ {
			<-peak
		}
		gcStress()
		close(release)
		for id := 0; id < workers; id++ {
			got := <-tasks[id].result
			want := poolWant(depth, tasks[id].rot, tasks[id].chain)
			ckf(got == want, "wave %d task %d got=%d want=%d", w, id, got, want)
			total += got
			wantTotal += want
		}
		// worker 已返回并阻塞在 jobs 上，GC 触发其栈收缩；下一波复用同一 goroutine。
		gcStress()
	}
	close(jobs)
	wg.Wait()

	ckf(total == wantTotal, "total=%d want=%d", total, wantTotal)
	gcStress()
	for i, n := range keep {
		if n == nil {
			ckf(false, "keep[%d] pointer missing after pool+shrink GC", i)
			continue
		}
		ckf(n.v == keepWant[i], "keep[%d] value=%d want=%d after pool+shrink GC", i, n.v, keepWant[i])
	}
	finish("l04_pool_stack_growth")
}

func main() {
	seed := envSeed()
	workers := envInt("TORTURE_WORKERS", 6, 2, 16)
	waves := envInt("TORTURE_ROUNDS", 4, 1, 12)
	runPoolWaves(seed, workers, waves)
}
