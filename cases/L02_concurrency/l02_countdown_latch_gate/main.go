// L02: countdown latch（grpc GracefulStop / callback serializer drain 场景）：
// 主 goroutine 收齐 N 个 ready 后扣减 latch，worker 才允许开始；
// 打开后所有等待者立即通过，重复 CountDown 幂等。
package main

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"sync"
	"sync/atomic"
)

const (
	latchWorkers = 6
	latchTasks   = 3
	latchWaiters = 5
)

type latchTask struct {
	w   int
	t   int
	val int64
	tag string
}

type latch struct {
	mu    sync.Mutex
	count int
	ch    chan struct{}
}

func newLatch(n int) *latch {
	l := &latch{count: n, ch: make(chan struct{})}
	if n == 0 {
		close(l.ch)
	}
	return l
}

func (l *latch) CountDown() {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.count <= 0 {
		return
	}
	l.count--
	if l.count == 0 {
		close(l.ch)
	}
}

func (l *latch) Wait() { <-l.ch }

func (l *latch) Count() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.count
}

var (
	failed int32
	gcMu   sync.Mutex
	gcSink [][]byte
)

func ck(ok bool, msg string) {
	if !ok {
		atomic.AddInt32(&failed, 1)
		fmt.Println("FAIL:", msg)
	}
}

func gcStress() {
	gcMu.Lock()
	for i := 0; i < 8; i++ {
		b := make([]byte, 2048)
		b[0] = byte(i)
		gcSink = append(gcSink[:0], b)
	}
	gcSink = gcSink[:0]
	gcMu.Unlock()
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

func seedVal() int64 {
	if s := os.Getenv("UAF_SEED"); s != "" {
		if n, err := strconv.Atoi(s); err == nil {
			return int64(n)
		}
	}
	return 1
}

func (t *latchTask) want(seed int64) int64 {
	return seed + int64(t.w*latchTasks+t.t)*5
}

func main() {
	seed := seedVal()
	l := newLatch(latchWorkers)
	ready := make(chan struct{}, latchWorkers)
	results := make(chan *latchTask, latchWorkers*latchTasks)
	var started int32
	var wg sync.WaitGroup

	for w := 0; w < latchWorkers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			ready <- struct{}{}
			l.Wait()
			atomic.AddInt32(&started, 1)
			for t := 0; t < latchTasks; t++ {
				task := &latchTask{
					w:   w,
					t:   t,
					val: seed + int64(w*latchTasks+t)*5,
					tag: "task",
				}
				if t == 0 {
					gcStress()
				}
				if task.val != task.want(seed) || task.tag != "task" {
					ck(false, fmt.Sprintf("worker %d task %d corrupted", w, t))
				}
				results <- task
			}
		}(w)
	}

	for i := 0; i < latchWorkers; i++ {
		<-ready
	}
	if n := l.Count(); n != latchWorkers {
		ck(false, fmt.Sprintf("latch opened before all readiness signals: count=%d", n))
	}
	if n := atomic.LoadInt32(&started); n != 0 {
		ck(false, fmt.Sprintf("workers started before latch opened: %d", n))
	}
	gcStress()
	for i := 0; i < latchWorkers; i++ {
		l.CountDown()
	}
	if n := l.Count(); n != 0 {
		ck(false, fmt.Sprintf("latch count=%d want=0", n))
	}
	// 额外 CountDown 必须幂等，不能把计数扣成负数或重复关闭。
	l.CountDown()
	l.CountDown()
	l.Wait()

	wg.Wait()
	close(results)

	seen := make([][]bool, latchWorkers)
	for i := range seen {
		seen[i] = make([]bool, latchTasks)
	}
	all := make([]*latchTask, 0, latchWorkers*latchTasks)
	for task := range results {
		if task.w < 0 || task.w >= latchWorkers || task.t < 0 || task.t >= latchTasks {
			ck(false, "task index out of range")
			continue
		}
		if seen[task.w][task.t] {
			ck(false, fmt.Sprintf("duplicate task w=%d t=%d", task.w, task.t))
		}
		seen[task.w][task.t] = true
		if task.val != task.want(seed) || task.tag != "task" {
			ck(false, fmt.Sprintf("task payload corrupted w=%d t=%d", task.w, task.t))
		}
		all = append(all, task)
	}
	if len(all) != latchWorkers*latchTasks {
		ck(false, fmt.Sprintf("tasks=%d want=%d", len(all), latchWorkers*latchTasks))
	}
	for w := 0; w < latchWorkers; w++ {
		for t := 0; t < latchTasks; t++ {
			if !seen[w][t] {
				ck(false, fmt.Sprintf("missing task w=%d t=%d", w, t))
			}
		}
	}

	// 已打开的 latch 必须广播给全部迟到等待者。
	late := newLatch(1)
	var lwg sync.WaitGroup
	var awoken int32
	for i := 0; i < latchWaiters; i++ {
		lwg.Add(1)
		go func() {
			defer lwg.Done()
			late.Wait()
			atomic.AddInt32(&awoken, 1)
		}()
	}
	gcStress()
	late.CountDown()
	lwg.Wait()
	if n := atomic.LoadInt32(&awoken); n != latchWaiters {
		ck(false, fmt.Sprintf("late waiters=%d want=%d", n, latchWaiters))
	}
	late.CountDown() // 打开后重复 CountDown 幂等
	late.Wait()

	gcStress()
	for _, task := range all {
		if task.val != task.want(seed) || task.tag != "task" {
			ck(false, fmt.Sprintf("task corrupted by final GC w=%d t=%d", task.w, task.t))
		}
	}

	fmt.Printf("workers=%d tasks=%d late_waiters=%d\n", latchWorkers, latchWorkers*latchTasks, latchWaiters)
	finish("l02_countdown_latch_gate")
}
