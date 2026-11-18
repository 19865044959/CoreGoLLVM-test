// S03: 栈增长/展开长跑。
// 深递归 + 帧内指针槽 + GC 交织；panic/recover、递归闭包、64KB 大帧轮换；
// 每层在展开时校验本帧指针，GC 后校验 payload。
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
}

var (
	beats    int64
	gcCycles int64
	depth    int
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

// 每个调用方自己的 sink，避免并发写全局。
func gcStress() {
	var sink [][]byte
	for i := 0; i < 4; i++ {
		b := make([]byte, 4096)
		b[0] = byte(i)
		sink = append(sink[:0], b)
	}
	for i := range sink {
		sink[i] = nil
	}
	runtime.KeepAlive(sink)
	runtime.GC()
	atomic.AddInt64(&gcCycles, 1)
}

//go:noinline
func deep(d int, p *node) int64 {
	var buf [32]*node
	buf[0] = &node{v: p.v, name: "d"}
	buf[31] = &node{v: p.v + 1, name: "d"}
	local := &node{v: p.v + 1, name: "d"}
	if d == 0 {
		gcStress()
		return buf[0].v + buf[31].v + local.v
	}
	r := deep(d-1, local)
	if d%257 == 0 {
		gcStress()
	}
	if buf[0].v != p.v || buf[31].v != p.v+1 || local.v != p.v+1 || p.name != "d" {
		failf("phase=deep depth=%d payload corrupted", d)
	}
	return r + buf[0].v + local.v
}

func expectedDeep(seed int64, max int) int64 {
	exp := 3*(seed+int64(max)) + 2
	for d := 1; d <= max; d++ {
		exp += 2*(seed+int64(max-d)) + 1
	}
	return exp
}

//go:noinline
func deepPanic(d int, p *node) int64 {
	local := &node{v: p.v + 1, name: "p"}
	if d == 0 {
		gcStress()
		panic(local)
	}
	r := deepPanic(d-1, local)
	return r + local.v
}

//go:noinline
func recoverDeep(seed int64, panDepth int) (sum int64, ok bool) {
	defer func() {
		if e := recover(); e != nil {
			gcStress()
			if q, is := e.(*node); is && q.name == "p" {
				sum = q.v
				ok = true
			}
		}
	}()
	return deepPanic(panDepth, &node{v: seed, name: "p"}), false
}

//go:noinline
func bigFrame(seed int64) int64 {
	var big [8192]*node
	for i := 0; i < len(big); i += 64 {
		big[i] = &node{v: seed + int64(i), name: "b"}
	}
	gcStress()
	var s int64
	for i := 0; i < len(big); i += 64 {
		if big[i] == nil || big[i].v != seed+int64(i) || big[i].name != "b" {
			failf("phase=bigframe index=%d corrupted", i)
		}
		s += big[i].v
	}
	return s
}

func expectedBig(seed int64) int64 {
	var s int64
	for i := 0; i < 8192; i += 64 {
		s += seed + int64(i)
	}
	return s
}

//go:noinline
func closureDeep(seed int64, max int) int64 {
	var rec func(int, *node) int64
	rec = func(d int, p *node) int64 {
		local := &node{v: p.v + 1, name: "c"}
		if d == 0 {
			gcStress()
			return local.v
		}
		r := rec(d-1, local)
		if local.v != p.v+1 || p.name != "c" {
			failf("phase=closure depth=%d corrupted", d)
		}
		return r + local.v
	}
	return rec(max, &node{v: seed, name: "c"})
}

func expectedClosure(seed int64, max int) int64 {
	sum := seed + int64(max) + 1
	for d := 1; d <= max; d++ {
		sum += seed + int64(max) + 1 - int64(d)
	}
	return sum
}

type worker struct {
	id     int64
	seed   uint64
	rounds int64
}

func (w *worker) round(r int64) {
	seed := int64(w.seed) + r*100000
	if deep(depth, &node{v: seed, name: "d"}) != expectedDeep(seed, depth) {
		failf("phase=deep worker=%d round=%d seed=%d: checksum mismatch", w.id, r, w.seed)
	}
	if r%4 == 0 {
		sum, ok := recoverDeep(seed, 512)
		if !ok || sum != seed+513 {
			failf("phase=panic worker=%d round=%d seed=%d: payload=%d want=%d ok=%v", w.id, r, w.seed, sum, seed+513, ok)
		}
	}
	if r%4 == 0 {
		if closureDeep(seed, 256) != expectedClosure(seed, 256) {
			failf("phase=closure worker=%d round=%d seed=%d: checksum mismatch", w.id, r, w.seed)
		}
	}
	if r%8 == 0 {
		if bigFrame(seed) != expectedBig(seed) {
			failf("phase=bigframe worker=%d round=%d seed=%d: checksum mismatch", w.id, r, w.seed)
		}
		debug.FreeOSMemory()
		if deep(64, &node{v: seed, name: "d"}) != expectedDeep(seed, 64) {
			failf("phase=shrink worker=%d round=%d seed=%d: post-shrink mismatch", w.id, r, w.seed)
		}
	}
	w.rounds = r
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
	workers := envInt("STRESS_WORKERS", 4, 1, 16)
	hb := envInt("STRESS_HEARTBEAT", 5, 1, 60)
	seed := envSeed(1)
	fixed := int64(envInt("STRESS_FIXED_ROUNDS", 16, 1, 100000))
	depth = envInt("STRESS_DEPTH", 1500, 64, 4000)
	debug.SetMaxStack(64 << 20)

	fmt.Printf("scenario=s03 phase=preflight go=%s goos=%s goarch=%s gomaxprocs=%d seed=%d seconds=%d workers=%d depth=%d heartbeat=%ds\n",
		runtime.Version(), runtime.GOOS, runtime.GOARCH, runtime.GOMAXPROCS(0), seed, seconds, workers, depth, hb)

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
	go watchdog(time.Duration(hb)*time.Second, stop)

	wg.Wait()
	close(stop)

	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	fmt.Printf("metrics: rounds=%d beats=%d gc_churn=%d heap_alloc=%d heap_objects=%d num_gc=%d goroutines=%d\n",
		ws[0].rounds, atomic.LoadInt64(&beats), atomic.LoadInt64(&gcCycles),
		m.HeapAlloc, m.HeapObjects, m.NumGC, runtime.NumGoroutine())
	fmt.Println("STRESS PASS s03_stack_churn_soak")
}
