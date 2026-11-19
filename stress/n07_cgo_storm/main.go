// N07: cgo 风暴长跑（需要 CGO_ENABLED=1 与 C 工具链）。
// C 侧多 pthread 线程反复回调 //export goCb；回调内分配、深递归、周期性 runtime.GC()；
// Go 侧再调用 C 阻塞函数（nanosleep）。校验 C 侧回调计数/校验和与 Go 独立计算的期望值；
// runtime.NumCgoCall 只记录不断言。BUILD_MODE=module，C 定义在 storm.c。
package main

/*
#cgo LDFLAGS: -lpthread
#include <stdint.h>

int64_t c_storm_run(int32_t nthreads, int32_t iters, int32_t base, int64_t *out_calls);
int32_t c_block_ms(int32_t ms);
*/
import "C"

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

const cbDepth = 24

type node struct {
	v    int64
	name string
}

var (
	beats      int64
	cbCount    int64
	storms     int64
	blockCalls int64
	blockEINTR int64

	// 按 base 分槽的回调计数：不同 worker 的 base 区间互不重叠，
	// 用于在并发风暴中精确校验单次风暴的 Go 侧回调次数。
	cbPerBase [2048]int64
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

// deepCallback 在 C→Go 回调帧内做深递归 + 每层指针分配，GC 后校验帧内指针。
//
//go:noinline
func deepCallback(d int, seed int64) int64 {
	local := &node{v: seed + int64(d), name: "cb"}
	var pad [4]*node
	pad[0] = local
	pad[3] = &node{v: seed, name: "cbp"}
	if d == 0 {
		if pad[0] != local || pad[3].v != seed {
			failf("phase=callback-deep depth=0 seed=%d: frame corrupted", seed)
		}
		return local.v + pad[3].v
	}
	r := deepCallback(d-1, seed)
	if local.v != seed+int64(d) || pad[0] != local || pad[3].v != seed {
		failf("phase=callback-deep depth=%d seed=%d: frame corrupted", d, seed)
	}
	return r + local.v
}

func deepCallbackExpected(d int, seed int64) int64 {
	// 非叶层 d..1 各加 seed+k；叶子（d=0）返回 local+pad[3] = 2*seed。
	s := int64(2) * seed
	for k := 1; k <= d; k++ {
		s += seed + int64(k)
	}
	return s
}

// cbValue 是回调返回值的纯函数，供 Go 侧独立计算期望校验和。
func cbValue(t, i, base int64) int64 {
	x := base*1000003 + t*1009 + i*7
	v := x*3 + 1
	if (t*31+i*17)%23 == 0 {
		v += deepCallbackExpected(cbDepth, x)
	}
	return v
}

func expectedStorm(nthreads, iters, base int64) int64 {
	var s int64
	for t := int64(0); t < nthreads; t++ {
		for i := int64(0); i < iters; i++ {
			s += cbValue(t, i, base)
		}
	}
	return s
}

//export goCb
func goCb(thread C.int32_t, iter C.int32_t, base C.int32_t) C.int64_t {
	t, i, b := int64(thread), int64(iter), int64(base)
	n := atomic.AddInt64(&cbCount, 1)
	atomic.AddInt64(&cbPerBase[int(base)%len(cbPerBase)], 1)
	// 回调内分配：GC 时回调栈/寄存器必须可扫描。
	buf := make([]byte, 256)
	buf[0] = byte(t + i)
	x := b*1000003 + t*1009 + i*7
	out := x*3 + 1
	if (t*31+i*17)%23 == 0 {
		out += deepCallback(cbDepth, x)
	}
	if n%64 == 0 {
		runtime.GC()
	}
	if buf[0] != byte(t+i) {
		failf("phase=callback thread=%d iter=%d base=%d: local buffer corrupted", t, i, b)
	}
	runtime.KeepAlive(buf)
	return C.int64_t(out)
}

type worker struct {
	id     int64
	seed   uint64
	rounds int64
}

func (w *worker) round(r int64) {
	nthreads := int32(2 + int((w.seed+uint64(r))%3))
	iters := int32(16 + int((w.seed*7+uint64(r)*13)%17))
	base := int32(1000 + w.id*100 + r%97)
	wantCalls := int64(nthreads) * int64(iters)

	before := atomic.LoadInt64(&cbCount)
	idx := int(base) % len(cbPerBase)
	atomic.StoreInt64(&cbPerBase[idx], 0)
	var outcalls C.int64_t
	sum := int64(C.c_storm_run(C.int32_t(nthreads), C.int32_t(iters), C.int32_t(base), &outcalls))
	got := atomic.LoadInt64(&cbPerBase[idx])
	if got != wantCalls {
		failf("phase=storm worker=%d round=%d seed=%d: callbacks=%d want=%d", w.id, r, w.seed, got, wantCalls)
	}
	if after := atomic.LoadInt64(&cbCount); after-before < wantCalls {
		failf("phase=storm worker=%d round=%d seed=%d: global callback counter did not advance", w.id, r, w.seed)
	}
	if int64(outcalls) != wantCalls {
		failf("phase=storm worker=%d round=%d seed=%d: C out_calls=%d want=%d", w.id, r, w.seed, int64(outcalls), wantCalls)
	}
	want := expectedStorm(int64(nthreads), int64(iters), int64(base))
	if sum != want {
		failf("phase=storm worker=%d round=%d seed=%d: checksum=%d want=%d", w.id, r, w.seed, sum, want)
	}

	// Go → C 阻塞调用（cgo 切换期间其他 worker 必须继续推进）。
	rc := int32(C.c_block_ms(C.int32_t(1 + (r+w.id)%3)))
	atomic.AddInt64(&blockCalls, 1)
	if rc != 0 {
		// nanosleep 被信号（如 async preempt 的 SIGURG）打断属正常，只记录。
		atomic.AddInt64(&blockEINTR, 1)
	}
	if r%4 == 0 {
		runtime.GC()
	}
	atomic.AddInt64(&storms, 1)
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

func watchdog(interval time.Duration, stop <-chan struct{}, wg *sync.WaitGroup) {
	defer wg.Done()
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
	workers := envInt("STRESS_WORKERS", 3, 1, 8)
	hb := envInt("STRESS_HEARTBEAT", 5, 1, 60)
	seed := envSeed(1)
	fixed := int64(envInt("STRESS_FIXED_ROUNDS", 8, 1, 100000))

	fmt.Printf("scenario=n07 phase=preflight go=%s goos=%s goarch=%s gomaxprocs=%d seed=%d seconds=%d workers=%d heartbeat=%ds\n",
		runtime.Version(), runtime.GOOS, runtime.GOARCH, runtime.GOMAXPROCS(0), seed, seconds, workers, hb)

	// 预检：一次单线程风暴，确认 C 侧可回调且校验和一致。
	pre := expectedStorm(1, 4, 7)
	var preCalls C.int64_t
	preGot := int64(C.c_storm_run(1, 4, 7, &preCalls))
	if preGot != pre || int64(preCalls) != 4 {
		failf("phase=preflight: storm checksum=%d want=%d calls=%d", preGot, pre, int64(preCalls))
	}
	atomic.StoreInt64(&cbCount, 0)

	startBase := runtime.NumGoroutine()
	deadline := time.Now().Add(time.Duration(seconds) * time.Second)
	stop := make(chan struct{})

	ws := make([]*worker, workers)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		w := &worker{id: int64(i), seed: seed*1000 + uint64(i) + 1}
		ws[i] = w
		wg.Add(1)
		go func() {
			defer wg.Done()
			w.run(deadline, seconds, fixed)
		}()
	}
	var helpers sync.WaitGroup
	helpers.Add(1)
	go watchdog(time.Duration(hb)*time.Second, stop, &helpers)

	wg.Wait()
	close(stop)
	helpers.Wait()

	runtime.GC()
	final := runtime.NumGoroutine()
	if final > startBase+8 {
		failf("phase=quiesce: goroutine baseline exceeded: final=%d start=%d", final, startBase)
	}

	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	fmt.Printf("metrics: rounds=%d beats=%d callbacks=%d storms=%d block_calls=%d block_eintr=%d num_cgo_call=%d goroutines_start=%d goroutines_final=%d heap_alloc=%d num_gc=%d\n",
		ws[0].rounds, atomic.LoadInt64(&beats), atomic.LoadInt64(&cbCount), atomic.LoadInt64(&storms),
		atomic.LoadInt64(&blockCalls), atomic.LoadInt64(&blockEINTR), runtime.NumCgoCall(),
		startBase, final, m.HeapAlloc, m.NumGC)
	fmt.Println("STRESS PASS n07_cgo_storm")
}
