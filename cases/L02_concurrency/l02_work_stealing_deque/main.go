// L02: work-stealing 双端队列（Go runtime runqsteal / etcd FIFO 调度场景）：
// 每个 worker 私有 deque，所有者从队尾取（LIFO），窃取者从队头取（FIFO）；
// 任务不均衡分配，强制发生窃取，校验每个任务恰好执行一次。
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
	wsWorkers   = 4
	wsBaseTasks = 96
)

type wsTask struct {
	id  int
	val int64
	tag string
}

type deque struct {
	mu    sync.Mutex
	items []*wsTask
}

func (d *deque) push(t *wsTask) {
	d.mu.Lock()
	d.items = append(d.items, t)
	d.mu.Unlock()
}

func (d *deque) pop() *wsTask {
	d.mu.Lock()
	defer d.mu.Unlock()
	n := len(d.items)
	if n == 0 {
		return nil
	}
	t := d.items[n-1]
	d.items[n-1] = nil
	d.items = d.items[:n-1]
	return t
}

func (d *deque) steal() *wsTask {
	d.mu.Lock()
	defer d.mu.Unlock()
	n := len(d.items)
	if n == 0 {
		return nil
	}
	t := d.items[0]
	copy(d.items, d.items[1:])
	d.items[n-1] = nil
	d.items = d.items[:n-1]
	return t
}

func (d *deque) size() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(d.items)
}

type wsScheduler struct {
	total    int
	seed     int64
	queues   []*deque
	all      []*wsTask
	seen     []int32
	executed int32
	steals   int32
	checksum atomic.Int64
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

func tortureRounds() int {
	if s := os.Getenv("TORTURE_ROUNDS"); s != "" {
		if n, err := strconv.Atoi(s); err == nil && n > 1 {
			if n > 8 {
				n = 8
			}
			return n
		}
	}
	return 1
}

func (s *wsScheduler) seedWork() {
	// worker 0 拿到约 3/4 的任务，其余 worker 均分剩余任务，保证每个 deque >=2。
	counts := make([]int, wsWorkers)
	hot := s.total*5/8 + s.total/8
	if hot > s.total-wsWorkers+1 {
		hot = s.total - wsWorkers + 1
	}
	counts[0] = hot
	rem := s.total - hot
	for i := 1; i < wsWorkers; i++ {
		counts[i] = rem / (wsWorkers - 1)
	}
	for i := 1; i < wsWorkers; i++ {
		// 把余数补到最后一个 worker，保持总和精确。
		if i == wsWorkers-1 {
			counts[i] = rem
		}
		rem -= counts[i]
	}

	id := 0
	for w := 0; w < wsWorkers; w++ {
		for k := 0; k < counts[w]; k++ {
			t := &wsTask{id: id, val: s.seed + int64(id)*13, tag: fmt.Sprintf("t%d", id)}
			s.all = append(s.all, t)
			s.queues[w].push(t)
			id++
		}
	}
}

func (s *wsScheduler) process(t *wsTask, worker int) {
	if t.id < 0 || t.id >= s.total {
		ck(false, fmt.Sprintf("worker %d got out-of-range task id=%d", worker, t.id))
		return
	}
	if t.val != s.seed+int64(t.id)*13 || t.tag != fmt.Sprintf("t%d", t.id) {
		ck(false, fmt.Sprintf("task payload corrupted id=%d", t.id))
	}
	if n := atomic.AddInt32(&s.seen[t.id], 1); n != 1 {
		ck(false, fmt.Sprintf("task %d executed %d times", t.id, n))
	}
	s.checksum.Add(t.val)
	atomic.AddInt32(&s.executed, 1)
	if t.id%19 == 0 {
		gcStress()
	}
	if t.val != s.seed+int64(t.id)*13 || t.tag != fmt.Sprintf("t%d", t.id) {
		ck(false, fmt.Sprintf("task payload corrupted after GC id=%d", t.id))
	}
}

func (s *wsScheduler) worker(id int, start <-chan struct{}, wg *sync.WaitGroup) {
	defer wg.Done()
	<-start
	n := len(s.queues)

	// 先窃取一个任务：所有 deque 都不为空，保证每个 worker 至少成功一次。
	victim := (id + 1) % n
	if st := s.queues[victim].steal(); st != nil {
		atomic.AddInt32(&s.steals, 1)
		s.process(st, id)
	}

	var t *wsTask
	for {
		t = s.queues[id].pop()
		if t == nil {
			for off := 1; off < n; off++ {
				v := (id + off) % n
				if st := s.queues[v].steal(); st != nil {
					atomic.AddInt32(&s.steals, 1)
					t = st
					break
				}
			}
		}
		if t == nil {
			if int(atomic.LoadInt32(&s.executed)) == s.total {
				return
			}
			runtime.Gosched()
			continue
		}
		s.process(t, id)
		t = nil
	}
}

func checkDequeSemantics(seed int64) {
	d := &deque{}
	for i := 1; i <= 3; i++ {
		d.push(&wsTask{id: i, val: seed + int64(i)*13, tag: fmt.Sprintf("t%d", i)})
	}
	if t := d.pop(); t == nil || t.id != 3 {
		ck(false, "owner pop must return newest task (LIFO)")
	}
	if t := d.steal(); t == nil || t.id != 1 {
		ck(false, "thief steal must return oldest task (FIFO)")
	}
	if t := d.pop(); t == nil || t.id != 2 {
		ck(false, "owner pop after steal returned wrong task")
	}
	if t := d.steal(); t != nil {
		ck(false, "empty deque steal must return nil")
	}
}

func main() {
	seed := seedVal()
	scale := tortureRounds()
	total := wsBaseTasks * scale
	checkDequeSemantics(seed)

	s := &wsScheduler{
		total:  total,
		seed:   seed,
		queues: make([]*deque, wsWorkers),
		seen:   make([]int32, total),
		all:    make([]*wsTask, 0, total),
	}
	for i := range s.queues {
		s.queues[i] = &deque{}
	}
	s.seedWork()
	if len(s.all) != total {
		ck(false, fmt.Sprintf("seeded tasks=%d want=%d", len(s.all), total))
	}
	for i, q := range s.queues {
		if q.size() < 2 {
			ck(false, fmt.Sprintf("worker %d deque too small: %d", i, q.size()))
		}
	}

	start := make(chan struct{})
	var wg sync.WaitGroup
	for w := 0; w < wsWorkers; w++ {
		wg.Add(1)
		go s.worker(w, start, &wg)
	}
	gcStress()
	close(start)
	wg.Wait()

	if n := atomic.LoadInt32(&s.executed); int(n) != total {
		ck(false, fmt.Sprintf("executed=%d want=%d", n, total))
	}
	if n := atomic.LoadInt32(&s.steals); int(n) < wsWorkers {
		ck(false, fmt.Sprintf("steals=%d want>=%d", n, wsWorkers))
	}
	if got, want := s.checksum.Load(), int64(total)*seed+int64(total*(total-1)/2)*13; got != want {
		ck(false, fmt.Sprintf("checksum=%d want=%d", got, want))
	}
	for i := 0; i < total; i++ {
		if n := atomic.LoadInt32(&s.seen[i]); n != 1 {
			ck(false, fmt.Sprintf("task %d seen=%d want=1", i, n))
		}
	}
	for i, q := range s.queues {
		if n := q.size(); n != 0 {
			ck(false, fmt.Sprintf("worker %d deque not drained: %d", i, n))
		}
	}

	gcStress()
	for _, t := range s.all {
		if t.val != seed+int64(t.id)*13 || t.tag != fmt.Sprintf("t%d", t.id) {
			ck(false, fmt.Sprintf("task payload corrupted by final GC id=%d", t.id))
		}
	}

	fmt.Printf("workers=%d tasks=%d scale=%d\n", wsWorkers, total, scale)
	finish("l02_work_stealing_deque")
}
