// Z06: 新 C 线程 + 栈增长（需要 CGO_ENABLED=1 与 C 工具链）。
//
// 对齐 gccgo gcstack 类缺陷（stress_history_bugs.md #14）：新建 C 线程回调 Go 时，
// 运行时为回调创建 extra G；若该 G 复用了仍指向上一轮已 unmapped 栈段的 gcstack，
// GC 扫描就会触碰已释放内存。
//
// 每 worker 每轮调用 C c_spawn_one(cb)：C 侧**新建一个 pthread**（线程生命周期仅本次
// 回调，返回后立即 join），线程内以函数指针调用 //export goCb。goCb 内做
// 800–2000 层深递归（每层指针槽 + 每 256 层一次 runtime.GC），强制回调所属 G 栈增长；
// 递归展开时逐层校验 canary。返回值与纯 Go 函数期望值比对，NumCgoCall 只记录。
// 独立 goroutine 提供 GC churn。BUILD_MODE=module，C 定义在 storm.c。
package main

/*
#cgo LDFLAGS: -lpthread
#include <stdint.h>

int64_t c_spawn_one(void *cb, int32_t iter, int64_t seed);
void *c_go_cb(void);
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

const (
	scenario = "z06_cgo_newthread_stackgrowth"

	minDepth   = 800
	depthRange = 1201 // depth ∈ [800, 2000]
	gcEveryN   = 256  // 递归中每 N 层触发一次 GC
)

type node struct {
	v    int64
	name string
}

var (
	beats      int64
	cbCount    int64
	deepLayers int64
	gcCycles   int64
	spawns     int64
	started    int64
	finished   int64
	gSeed      uint64
)

func failf(format string, args ...interface{}) {
	fmt.Printf("FAIL: seed=%d callbacks=%d "+format+"\n",
		append([]interface{}{gSeed, atomic.LoadInt64(&cbCount)}, args...)...)
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

// depthFor 由 seed/iter 派生本轮递归深度（800–2000），Go 侧与回调侧共用，
// 保证“新线程栈增长深度”每轮不同且可独立复算。
func depthFor(seed, iter int64) int {
	h := uint64(seed)*0x9E3779B97F4A7C15 ^ uint64(iter)*0xBF58476D1CE4E5B9
	return minDepth + int((h>>17)%depthRange)
}

// deepExpected 是 deepFrame 的闭式期望：叶子 4*seed，非叶层每层 +seed+d。
func deepExpected(d int, seed int64) int64 {
	n := int64(d)
	return 4*seed + n*seed + n*(n+1)/2
}

// extra 是回调返回值的附加项（参数往返校验）。
func extra(seed, iter int64) int64 {
	return seed*7 + iter*3 + 1
}

// expectedCb 是回调返回值的纯函数期望，由父侧独立计算。
func expectedCb(seed, iter int64) int64 {
	return deepExpected(depthFor(seed, iter), seed) + extra(seed, iter)
}

// deepFrame 在 C→Go 回调帧内递归 d 层；每层有指针槽，每 gcEveryN 层 GC 一次。
// 新 C 线程对应的 extra G 在递归中反复 morestack，展开时逐层校验帧内指针。
//
//go:noinline
func deepFrame(d int, seed int64) int64 {
	local := &node{v: seed + int64(d), name: "z06cb"}
	var pad [4]*node
	pad[0] = local
	pad[3] = &node{v: seed*3 + int64(d), name: "z06p"}

	if d%gcEveryN == 0 {
		runtime.GC()
		if local.v != seed+int64(d) || pad[0] != local || pad[3] == nil || pad[3].v != seed*3+int64(d) {
			failf("phase=callback-gc depth=%d seed=%d: frame pointers corrupted after GC", d, seed)
		}
	}
	if d == 0 {
		if pad[0] != local || pad[3] == nil {
			failf("phase=callback-deep depth=0 seed=%d: leaf frame corrupted", seed)
		}
		return local.v + pad[3].v
	}
	r := deepFrame(d-1, seed)
	if local.v != seed+int64(d) || pad[0] != local || pad[3] == nil || pad[3].v != seed*3+int64(d) {
		failf("phase=callback-deep depth=%d seed=%d: frame corrupted on unwind", d, seed)
	}
	return r + local.v
}

//export goCb
func goCb(iter C.int32_t, seed C.int64_t) C.int64_t {
	i, s := int64(iter), int64(seed)
	d := depthFor(s, i)
	if d < minDepth || d >= minDepth+depthRange {
		failf("phase=callback iter=%d: derived depth=%d out of range", i, d)
	}
	atomic.AddInt64(&cbCount, 1)

	got := deepFrame(d, s)
	want := deepExpected(d, s)
	if got != want {
		failf("phase=callback-deep depth=%d seed=%d iter=%d: recursion sum=%d want=%d", d, s, i, got, want)
	}
	atomic.AddInt64(&deepLayers, int64(d))
	return C.int64_t(got + extra(s, i))
}

type worker struct {
	id     int64
	seed   uint64
	rounds int64
	next   int64
	layers int64
}

func (w *worker) round(r int64) {
	seed := int64(uint64(w.seed) ^ (uint64(r) * 0x9E3779B97F4A7C15))
	if seed == 0 {
		seed = 1
	}
	want := expectedCb(seed, r)

	got := int64(C.c_spawn_one(C.c_go_cb(), C.int32_t(r), C.int64_t(seed)))
	atomic.AddInt64(&spawns, 1)
	if got == -1 {
		failf("phase=spawn worker=%d round=%d seed=%d: pthread_create failed (thread resource exhausted?)", w.id, r, seed)
	}
	if got == -2 {
		failf("phase=spawn worker=%d round=%d seed=%d: pthread_join failed", w.id, r, seed)
	}
	if got != want {
		failf("phase=spawn worker=%d round=%d seed=%d depth=%d: C returned %d want=%d",
			w.id, r, seed, depthFor(seed, r), got, want)
	}
	w.layers += int64(depthFor(seed, r))
	w.rounds++
	w.next = r + 1
	atomic.AddInt64(&beats, 1)
}

func (w *worker) run(seconds int, deadline time.Time, fixed int64) {
	atomic.AddInt64(&started, 1)
	defer atomic.AddInt64(&finished, 1)
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

// gcChurn 是独立 GC churn：真实新线程回调期间持续分配 + 显式 GC，
// 让回调栈/帧扫描窗口与 GC 重叠。
func gcChurn(stop <-chan struct{}, wg *sync.WaitGroup) {
	defer wg.Done()
	var sink [][]byte
	for {
		select {
		case <-stop:
			return
		default:
		}
		for i := 0; i < 8; i++ {
			b := make([]byte, 2048)
			b[0] = byte(i)
			sink = append(sink, b)
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
				failf("phase=watchdog: no worker progress for %s (beats=%d)", time.Since(lastChange), cur)
			}
		}
	}
}

func waitGoroutines(base int, grace time.Duration) int {
	giveUp := time.Now().Add(grace)
	for {
		n := runtime.NumGoroutine()
		if n <= base || time.Now().After(giveUp) {
			return n
		}
		time.Sleep(10 * time.Millisecond)
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
	workers := envInt("STRESS_WORKERS", 2, 1, 8)
	hb := envInt("STRESS_HEARTBEAT", 5, 1, 60)
	seed := envSeed(1)
	fixed := int64(envInt("STRESS_FIXED_ROUNDS", 8, 1, 100000))
	gSeed = seed

	fmt.Printf("scenario=%s phase=preflight go=%s goos=%s goarch=%s gomaxprocs=%d seed=%d seconds=%d workers=%d fixed_rounds=%d heartbeat=%ds depth_range=%d-%d\n",
		scenario, runtime.Version(), runtime.GOOS, runtime.GOARCH, runtime.GOMAXPROCS(0), seed, seconds, workers, fixed, hb,
		minDepth, minDepth+depthRange-1)

	// 预检：真正新建一条 C 线程并回调一次，确认 //export 符号、函数指针与
	// Go/C 返回值往返一致，且深递归校验和正确。
	cgoBefore := runtime.NumCgoCall()
	preSeed := int64(7)
	preGot := int64(C.c_spawn_one(C.c_go_cb(), C.int32_t(0), C.int64_t(preSeed)))
	if preWant := expectedCb(preSeed, 0); preGot != preWant {
		failf("phase=preflight: callback returned %d want=%d (export signature/function pointer/ABI)", preGot, preWant)
	}
	if runtime.NumCgoCall() <= cgoBefore {
		failf("phase=preflight: NumCgoCall did not advance (%d)", runtime.NumCgoCall())
	}
	// 预检的计数不进入本轮统计。
	atomic.StoreInt64(&cbCount, 0)
	atomic.StoreInt64(&deepLayers, 0)

	baseGoroutines := runtime.NumGoroutine()
	deadline := time.Now().Add(time.Duration(seconds) * time.Second)
	ws := make([]*worker, workers)
	for i := 0; i < workers; i++ {
		ws[i] = &worker{id: int64(i), seed: seed*1_000_003 + uint64(i+1)*7919}
	}

	stop := make(chan struct{})
	var aux sync.WaitGroup
	aux.Add(1)
	go gcChurn(stop, &aux)
	aux.Add(1)
	go watchdog(time.Duration(hb)*time.Second, stop, &aux)

	var wg sync.WaitGroup
	for _, w := range ws {
		wg.Add(1)
		go func(w *worker) {
			defer wg.Done()
			w.run(seconds, deadline, fixed)
		}(w)
	}
	wg.Wait()
	close(stop)
	aux.Wait()

	if s, f := atomic.LoadInt64(&started), atomic.LoadInt64(&finished); s != int64(workers) || f != s {
		failf("phase=final: started=%d finished=%d workers=%d", s, f, workers)
	}

	var roundsTotal, layers int64
	for _, w := range ws {
		roundsTotal += w.rounds
		layers += w.layers
		if seconds == 0 && w.next != fixed {
			failf("phase=final worker=%d: rounds=%d want=%d", w.id, w.next, fixed)
		}
	}
	if roundsTotal == 0 {
		failf("phase=final: no rounds completed")
	}
	if got := atomic.LoadInt64(&spawns); got != int64(roundsTotal) {
		failf("phase=final: thread spawns=%d want=rounds=%d", got, roundsTotal)
	}
	if got := atomic.LoadInt64(&deepLayers); got != layers {
		failf("phase=final: recursion layers=%d want=%d", got, layers)
	}

	ng := waitGoroutines(baseGoroutines, 5*time.Second)
	if ng > baseGoroutines+4 {
		failf("phase=final: goroutines baseline=%d after=%d: goroutine leak", baseGoroutines, ng)
	}

	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	fmt.Printf("metrics: rounds=%d thread_spawns=%d callbacks=%d recursion_layers=%d gc_churn=%d num_cgo_call=%d num_gc=%d heap_alloc=%d heap_objects=%d goroutines=%d\n",
		roundsTotal, atomic.LoadInt64(&spawns), atomic.LoadInt64(&cbCount), layers,
		atomic.LoadInt64(&gcCycles), runtime.NumCgoCall(), m.NumGC, m.HeapAlloc, m.HeapObjects, ng)
	fmt.Printf("STRESS PASS %s\n", scenario)
}
