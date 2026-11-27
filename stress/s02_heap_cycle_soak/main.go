// S02: 堆循环长跑。
// 持续分配指针密集 slab → 环形保留 → 丢弃/FreeOSMemory 收缩 → 再增长；
// 独立 GC churn；所有存活对象内容、fresh 分配全零、接口/slice/map 内容持续校验。
package main

import (
	"fmt"
	"os"
	"runtime"
	"runtime/debug"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

type node struct {
	v    int64
	name string
	next *node
}

type kv struct {
	k, s string
	p    *node
}

type slab struct {
	id    int64
	base  int64
	nodes []*node
	kvs   []kv
	iface interface{}
	chain *node
	sum   int64
}

type worker struct {
	id     int64
	seed   uint64
	ring   []*slab
	rounds int64
}

var (
	beats    int64
	gcCycles int64
)

func failf(format string, args ...interface{}) {
	fmt.Printf("FAIL: "+format+"\n", args...)
	buf := make([]byte, 1<<20)
	n := runtime.Stack(buf, true)
	fmt.Fprintf(os.Stderr, "%s", buf[:n])
	os.Exit(1)
}

func envInt(key string, def, min, max int) int {
	s := os.Getenv(key)
	if s == "" {
		return def
	}
	v, err := strconv.Atoi(s)
	if err != nil || v < min {
		return def
	}
	if v > max {
		return max
	}
	return v
}

func envSeed(def uint64) uint64 {
	s := os.Getenv("UAF_SEED")
	if s == "" {
		return def
	}
	v, err := strconv.ParseUint(s, 10, 64)
	if err != nil || v == 0 {
		return def
	}
	return v
}

func (w *worker) makeSlab(round int64) *slab {
	n := 512 + int((w.seed+uint64(round)*7)%1536)
	nodes := make([]*node, n)
	for i := range nodes {
		if nodes[i] != nil {
			failf("phase=alloc worker=%d round=%d seed=%d: fresh []*node not zeroed at %d", w.id, round, w.seed, i)
		}
	}
	base := w.id*1_000_000 + round*4096
	for i := range nodes {
		nodes[i] = &node{v: base + int64(i), name: "s02"}
	}

	kvs := make([]kv, 128)
	for i := range kvs {
		if kvs[i].p != nil || kvs[i].k != "" || kvs[i].s != "" {
			failf("phase=alloc worker=%d round=%d: fresh []kv not zeroed at %d", w.id, round, i)
		}
	}
	for i := range kvs {
		kvs[i] = kv{k: "k", s: "v", p: nodes[i%n]}
	}

	chain := &node{v: base + 900000, name: "s02c"}
	head := chain
	for i := 0; i < 7; i++ {
		chain.next = &node{v: base + 900001 + int64(i), name: "s02c"}
		chain = chain.next
	}

	sum := int64(n)*base + int64(n*(n-1)/2)
	return &slab{
		id:    w.id*1_000_000 + round,
		base:  base,
		nodes: nodes,
		kvs:   kvs,
		iface: []*node{nodes[0], nodes[n/2], nodes[n-1]},
		chain: head,
		sum:   sum,
	}
}

func (w *worker) verifySlab(s *slab, phase string, round int64) {
	n := len(s.nodes)
	for _, i := range []int{0, n / 2, n - 1} {
		q := s.nodes[i]
		if q == nil || q.v != s.base+int64(i) || q.name != "s02" {
			failf("phase=%s worker=%d round=%d seed=%d: node[%d] corrupted", phase, w.id, round, w.seed, i)
		}
	}
	if len(s.kvs) != 128 {
		failf("phase=%s worker=%d round=%d: kvs len=%d", phase, w.id, round, len(s.kvs))
	}
	for _, j := range []int{0, 64, 127} {
		q := s.kvs[j].p
		if q == nil || q.v != s.base+int64(j%n) || s.kvs[j].k != "k" || s.kvs[j].s != "v" {
			failf("phase=%s worker=%d round=%d seed=%d: kv[%d] corrupted", phase, w.id, round, w.seed, j)
		}
	}
	arr, ok := s.iface.([]*node)
	if !ok || len(arr) != 3 || arr[2] == nil || arr[2].v != s.base+int64(n-1) {
		failf("phase=%s worker=%d round=%d seed=%d: interface slice corrupted", phase, w.id, round, w.seed)
	}
	c := s.chain
	for i := 0; i < 8; i++ {
		if c == nil || c.v != s.base+900000+int64(i) || c.name != "s02c" {
			failf("phase=%s worker=%d round=%d seed=%d: chain[%d] corrupted", phase, w.id, round, w.seed, i)
		}
		c = c.next
	}
	if c != nil {
		failf("phase=%s worker=%d round=%d seed=%d: chain longer than 8", phase, w.id, round, w.seed)
	}
	if s.sum != int64(n)*s.base+int64(n*(n-1)/2) {
		failf("phase=%s worker=%d round=%d seed=%d: sum corrupted", phase, w.id, round, w.seed)
	}
}

func (w *worker) round(r int64) {
	s := w.makeSlab(r)
	w.ring = append(w.ring, s)
	if len(w.ring) > 3 {
		w.ring[0] = nil
		w.ring = w.ring[1:]
	}
	for _, q := range w.ring {
		w.verifySlab(q, "steady", r)
	}
	if r%8 == 0 {
		runtime.GC()
	}
	if r%24 == 0 {
		// 收缩：只保留最新一个 slab，FreeOSMemory 后再增长。
		for i := 0; i < len(w.ring)-1; i++ {
			w.ring[i] = nil
		}
		w.ring = w.ring[len(w.ring)-1:]
		debug.FreeOSMemory()
		for _, q := range w.ring {
			w.verifySlab(q, "shrink", r)
		}
	}
	atomic.AddInt64(&beats, 1)
}

func (w *worker) run(deadline time.Time, seconds int, fixed int64) {
	r := int64(0)
	for {
		if seconds > 0 {
			if time.Now().After(deadline) {
				break
			}
		} else if r >= fixed {
			break
		}
		w.round(r)
		r++
	}
	w.rounds = r
	for _, s := range w.ring {
		w.verifySlab(s, "final", r)
	}
}

func gcChurn(stop <-chan struct{}, wg *sync.WaitGroup) {
	defer wg.Done()
	var sink [][]byte
	for {
		select {
		case <-stop:
			return
		default:
		}
		for i := 0; i < 16; i++ {
			b := make([]byte, 4096)
			b[0] = byte(i)
			sink = append(sink[:0], b)
		}
		for i := range sink {
			sink[i] = nil
		}
		sink = sink[:0]
		runtime.GC()
		atomic.AddInt64(&gcCycles, 1)
		// window（GOMAXPROCS=1+STW）下无休眠 GC 循环会饿死 worker/watchdog。
		if runtime.GOMAXPROCS(0) == 1 {
			runtime.Gosched()
			time.Sleep(time.Millisecond)
		}
	}
}

func watchdog(interval time.Duration, stop <-chan struct{}) {
	last := atomic.LoadInt64(&beats)
	lastChange := time.Now()
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-stop:
			return
		case <-t.C:
			cur := atomic.LoadInt64(&beats)
			if cur != last {
				last = cur
				lastChange = time.Now()
				continue
			}
			if time.Since(lastChange) > 6*interval {
				failf("watchdog stalled: no worker progress for %s (beats=%d)", time.Since(lastChange), cur)
			}
		}
	}
}

// 把 fatal/panic 的 crash 文本写到 runner 指定的文件（core 不可用的环境用）。
func setupCrashOutput() {
	p := os.Getenv("STRESS_CRASH_FILE")
	if p == "" {
		return
	}
	f, err := os.OpenFile(p, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return
	}
	_ = debug.SetCrashOutput(f, debug.CrashOptions{})
}

func main() {
	setupCrashOutput()
	seconds := envInt("STRESS_SECONDS", 60, 0, 86400)
	workers := envInt("STRESS_WORKERS", 4, 1, 32)
	hb := envInt("STRESS_HEARTBEAT", 5, 1, 60)
	seed := envSeed(1)
	fixed := int64(envInt("STRESS_FIXED_ROUNDS", 64, 1, 100000))

	fmt.Printf("scenario=s02 phase=preflight go=%s goos=%s goarch=%s gomaxprocs=%d seed=%d seconds=%d workers=%d heartbeat=%ds\n",
		runtime.Version(), runtime.GOOS, runtime.GOARCH, runtime.GOMAXPROCS(0), seed, seconds, workers, hb)

	deadline := time.Now().Add(time.Duration(seconds) * time.Second)
	ws := make([]*worker, workers)
	var wg sync.WaitGroup
	stop := make(chan struct{})
	for i := 0; i < workers; i++ {
		w := &worker{id: int64(i), seed: seed*1000 + uint64(i) + 1}
		ws[i] = w
		wg.Add(1)
		go func() {
			defer wg.Done()
			w.run(deadline, seconds, fixed)
		}()
	}
	var cwg sync.WaitGroup
	cwg.Add(1)
	go gcChurn(stop, &cwg)
	go watchdog(time.Duration(hb)*time.Second, stop)

	wg.Wait()
	close(stop)
	cwg.Wait()

	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	fmt.Printf("metrics: rounds=%d beats=%d gc_churn=%d heap_alloc=%d heap_objects=%d num_gc=%d goroutines=%d\n",
		ws[0].rounds, atomic.LoadInt64(&beats), atomic.LoadInt64(&gcCycles),
		m.HeapAlloc, m.HeapObjects, m.NumGC, runtime.NumGoroutine())
	fmt.Println("STRESS PASS s02_heap_cycle_soak")
}
