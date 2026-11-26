// X07: cgo 回调 panic/深栈长跑（需要 CGO_ENABLED=1 与 C 工具链）。
//
// C 侧 pthread 反复回调 //export goCbPanic；回调内做数千层深递归（每层指针槽），
// 递归叶执行 runtime.GC，然后按模式 panic/recover（含 panic 展开期间再 GC 的变体），
// 校验后返回 C；C 线程在返回后继续下一次回调。GOMEMLIMIT 由 Go 侧动态贴近 live heap，
// 强制 mutator 在回调递归中进入 GC assist。校验回调计数/C 侧校验和/Go 侧独立期望值。
//
// 回调内 runtime.Goexit 不作实现：Goexit 会终止回调 goroutine 而无法把返回值交还 C
// pthread，C 侧会永久挂起；按调研条目约定改用“深层 panic + recover”及其展开期 GC 变体
// （进程不崩溃，recover 后校验）。BUILD_MODE=module，C 定义在 x07_cgo.c。
package main

/*
#cgo LDFLAGS: -lpthread
#include <stdint.h>

int64_t c_panic_storm(int32_t nthreads, int32_t iters, int32_t base, int32_t mode, int64_t *out_calls);
*/
import "C"

import (
	"fmt"
	"os"
	"runtime"
	"runtime/debug"
	"runtime/metrics"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

const (
	scenario = "x07_cgo_panic_deepstack"

	deepDepth    = 2048 // 回调内递归层数（每层至少 1 个指针槽）
	cbSlotCount  = 4096 // 回调计数槽数；worker 的 base 区间互不重叠
	roundsPerID  = 512  // 每 worker 一轮使用的槽数（4096/8 个 worker）
	maxCThreads  = 2    // 每次风暴的 C pthread 数上限
	unwindGCMod  = 256  // mode=2 展开期每 N 层触发一次 GC
	minMemLimit  = int64(8) << 20
	govInterval  = 250 * time.Millisecond
	assistMetric = "/cpu/classes/gc/mark/assist:cpu-seconds"
)

type node struct {
	v    int64
	name string
}

type deepPayload struct {
	sum       int64
	seed      int64
	depth     int
	local     *node
	localWant int64
}

var (
	beats           int64
	cbCount         int64
	storms          int64
	panicsRecovered int64
	unwindGCs       int64
	nestedPanics    int64
	limitSets       int64
	limitPhases     int64

	cbPerSlot [cbSlotCount]int64
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

// deepExpected 是 deepRun 的闭式期望值：叶子 2*seed，非叶层每层 +seed+d。
func deepExpected(d int, seed int64) int64 {
	n := int64(d)
	return 2*seed + n*seed + n*(n+1)/2
}

// deepRun 在 C→Go 回调帧内递归 d 层；每层有 local 指针槽与 [4]*node pad。
// mode=0 正常返回；mode=1/2 在叶 GC 后 panic（payload 持指针，展开期由 recover 校验）；
// mode=2 还在每 unwindGCMod 层的 defer 中触发 GC 并复查本帧指针槽。
//
//go:noinline
func deepRun(d int, seed int64, mode int32) int64 {
	local := &node{v: seed + int64(d), name: "x07cb"}
	var pad [4]*node
	pad[0] = local
	pad[3] = &node{v: seed ^ int64(d), name: "x07p"}

	if mode == 2 && d%unwindGCMod == 0 {
		defer func() {
			if local.v != seed+int64(d) || pad[0] != local || pad[3] == nil || pad[3].v != seed^int64(d) {
				failf("phase=unwind depth=%d seed=%d: frame pointers corrupted", d, seed)
			}
			runtime.GC()
			if local.v != seed+int64(d) || pad[0] != local || pad[3] == nil || pad[3].v != seed^int64(d) {
				failf("phase=unwind-gc depth=%d seed=%d: frame pointers corrupted after GC", d, seed)
			}
			atomic.AddInt64(&unwindGCs, 1)
		}()
	}

	if d == 0 {
		if local.v != seed || pad[0] != local || pad[3] == nil || pad[3].v != seed {
			failf("phase=callback-deep depth=0 seed=%d: leaf frame corrupted", seed)
		}
		runtime.GC()
		if local.v != seed || pad[0] != local || pad[3] == nil || pad[3].v != seed {
			failf("phase=callback-deep depth=0 seed=%d: leaf frame corrupted after GC", seed)
		}
		if mode != 0 {
			panic(&deepPayload{sum: deepExpected(deepDepth, seed), seed: seed, depth: 0, local: local, localWant: seed})
		}
		return local.v + pad[3].v
	}

	r := deepRun(d-1, seed, mode)
	if local.v != seed+int64(d) || pad[0] != local || pad[3] == nil || pad[3].v != seed^int64(d) {
		failf("phase=callback-deep depth=%d seed=%d: frame corrupted", d, seed)
	}
	return r + local.v
}

// recoverDeep 调用 deepRun(mode=1/2)，panic 在回调帧内被 recover；随后再 GC 并重新
// 校验 payload 指向的对象（panic payload 必须跨展开/GC 存活）。
func recoverDeep(seed int64, mode int32) (sum int64) {
	defer func() {
		r := recover()
		if r == nil {
			failf("phase=callback seed=%d mode=%d: expected panic was not raised", seed, mode)
		}
		p, ok := r.(*deepPayload)
		if !ok || p == nil {
			failf("phase=callback seed=%d mode=%d: unexpected panic value %T", seed, mode, r)
		}
		if p.seed != seed || p.sum != deepExpected(deepDepth, seed) || p.depth != 0 {
			failf("phase=callback seed=%d mode=%d: panic payload numbers corrupted", seed, mode)
		}
		if p.local == nil || p.local.v != p.localWant || p.local.name != "x07cb" {
			failf("phase=panic-payload seed=%d mode=%d: panicked frame pointer corrupted", seed, mode)
		}
		runtime.GC()
		if p.local == nil || p.local.v != p.localWant || p.local.name != "x07cb" {
			failf("phase=panic-recover-gc seed=%d mode=%d: payload pointer corrupted after GC", seed, mode)
		}
		sum = p.sum
		atomic.AddInt64(&panicsRecovered, 1)
	}()
	deepRun(deepDepth, seed, mode)
	failf("phase=callback seed=%d mode=%d: deepRun returned without panic", seed, mode)
	return 0
}

// nestedDeep：连续两轮 panic/recover（中间各做一次 GC），两段都必须在返回 C 前 recover。
func nestedDeep(seed int64) (sum int64) {
	want := deepExpected(deepDepth, seed)

	func() {
		defer func() {
			r := recover()
			if r == nil {
				failf("phase=nested seed=%d: first panic was not raised", seed)
			}
			p, ok := r.(*deepPayload)
			if !ok || p == nil || p.sum != want || p.local == nil || p.local.v != p.localWant {
				failf("phase=nested seed=%d: first panic payload corrupted", seed)
			}
			runtime.GC()
			if p.local == nil || p.local.v != p.localWant {
				failf("phase=nested seed=%d: first payload pointer corrupted after GC", seed)
			}
			sum += p.sum
			atomic.AddInt64(&nestedPanics, 1)
		}()
		deepRun(deepDepth, seed, 1)
		failf("phase=nested seed=%d: first deepRun returned without panic", seed)
	}()

	func() {
		defer func() {
			r := recover()
			if r == nil {
				failf("phase=nested seed=%d: second panic was not raised", seed)
			}
			p, ok := r.(*deepPayload)
			if !ok || p == nil || p.sum != want || p.local == nil || p.local.v != p.localWant {
				failf("phase=nested seed=%d: second panic payload corrupted", seed)
			}
			runtime.GC()
			if p.local == nil || p.local.v != p.localWant {
				failf("phase=nested seed=%d: second payload pointer corrupted after GC", seed)
			}
			sum += p.sum
			atomic.AddInt64(&nestedPanics, 1)
		}()
		panic(&deepPayload{sum: want, seed: seed, depth: 0, local: &node{v: seed, name: "x07cb"}, localWant: seed})
	}()

	return sum
}

// cbExpected 是单次回调返回值的纯函数（Go 侧独立计算，用于与 C 侧累计和比对）。
func cbExpected(t, i, base int64, mode int32) int64 {
	x := base*1000003 + t*1009 + i*7
	out := x*3 + 1
	switch mode {
	case 0, 1, 2:
		out += deepExpected(deepDepth, x)
	case 3:
		out += 2 * deepExpected(deepDepth, x)
	}
	return out
}

func expectedStorm(nthreads, iters, base int64, mode int32) int64 {
	var s int64
	for t := int64(0); t < nthreads; t++ {
		for i := int64(0); i < iters; i++ {
			s += cbExpected(t, i, base, mode)
		}
	}
	return s
}

//export goCbPanic
func goCbPanic(thread C.int32_t, iter C.int32_t, base C.int32_t, mode C.int32_t) C.int64_t {
	t, i, b, m := int64(thread), int64(iter), int64(base), int32(mode)
	atomic.AddInt64(&cbCount, 1)
	atomic.AddInt64(&cbPerSlot[int(b)%len(cbPerSlot)], 1)

	// 回调内分配：GC 时回调栈/寄存器与 C 帧之上的 Go 帧必须可扫描。
	buf := make([]byte, 256)
	buf[0] = byte(t + i)
	x := b*1000003 + t*1009 + i*7
	out := x*3 + 1
	switch m {
	case 0:
		out += deepRun(deepDepth, x, 0)
	case 1, 2:
		out += recoverDeep(x, m)
	case 3:
		out += nestedDeep(x)
	default:
		failf("phase=callback thread=%d iter=%d: unknown mode=%d", t, i, m)
	}
	if buf[0] != byte(t+i) {
		failf("phase=callback thread=%d iter=%d base=%d mode=%d: local buffer corrupted", t, i, b, m)
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
	slot := w.id*roundsPerID + r%roundsPerID
	base := int32(1000) + int32(slot)
	mode := int32((w.seed + uint64(r)) % 4)
	nthreads := int32(1 + int((w.seed*3+uint64(r)*5)%uint64(maxCThreads)))
	iters := int32(3 + int((w.seed*7+uint64(r)*13)%4)) // 3..6
	wantCalls := int64(nthreads) * int64(iters)

	idx := int(base) % len(cbPerSlot)
	atomic.StoreInt64(&cbPerSlot[idx], 0)
	var outcalls C.int64_t
	sum := int64(C.c_panic_storm(C.int32_t(nthreads), C.int32_t(iters), C.int32_t(base), C.int32_t(mode), &outcalls))
	got := atomic.LoadInt64(&cbPerSlot[idx])
	if got != wantCalls {
		failf("phase=storm worker=%d round=%d seed=%d mode=%d: callbacks=%d want=%d", w.id, r, w.seed, mode, got, wantCalls)
	}
	if int64(outcalls) != wantCalls {
		failf("phase=storm worker=%d round=%d seed=%d mode=%d: C out_calls=%d want=%d", w.id, r, w.seed, mode, int64(outcalls), wantCalls)
	}
	want := expectedStorm(int64(nthreads), int64(iters), int64(base), mode)
	if sum != want {
		failf("phase=storm worker=%d round=%d seed=%d mode=%d: checksum=%d want=%d", w.id, r, w.seed, mode, sum, want)
	}

	// 回调之外的 GC：与回调内 GC 交替，覆盖“GC 落在 C→Go 切换边界”的窗口。
	if mode != 0 {
		runtime.GC()
	}
	w.rounds = r
	atomic.AddInt64(&storms, 1)
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

// ---- GOMEMLIMIT 动态贴近 live heap（强制回调递归中的 GC assist） ----

func withLiveLimit(factor float64) int64 {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	live := int64(m.HeapAlloc)
	if live < 1<<20 {
		live = 1 << 20
	}
	lim := int64(float64(live) * factor)
	if lim < minMemLimit {
		lim = minMemLimit
	}
	debug.SetMemoryLimit(lim)
	atomic.AddInt64(&limitSets, 1)
	return lim
}

func governor(stop <-chan struct{}, wg *sync.WaitGroup) {
	defer wg.Done()
	factors := [...]float64{1.1, 1.25, 1.5}
	i := 0
	t := time.NewTicker(govInterval)
	defer t.Stop()
	for {
		select {
		case <-stop:
			return
		case <-t.C:
			withLiveLimit(factors[i%len(factors)])
			i++
			atomic.AddInt64(&limitPhases, 1)
		}
	}
}

func gcAssistSeconds() float64 {
	samples := []metrics.Sample{{Name: assistMetric}}
	metrics.Read(samples)
	if samples[0].Value.Kind() == metrics.KindFloat64 {
		return samples[0].Value.Float64()
	}
	return 0
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

func main() {
	setupCrashOutput()
	seconds := envInt("STRESS_SECONDS", 60, 0, 86400)
	workers := envInt("STRESS_WORKERS", 2, 1, 8)
	hb := envInt("STRESS_HEARTBEAT", 5, 1, 60)
	seed := envSeed(1)
	fixed := int64(envInt("STRESS_FIXED_ROUNDS", 8, 1, 100000))

	// 记录并原样恢复 GOMEMLIMIT。
	origLimit := debug.SetMemoryLimit(-1)

	fmt.Printf("scenario=%s phase=preflight go=%s goos=%s goarch=%s gomaxprocs=%d seed=%d seconds=%d workers=%d fixed_rounds=%d heartbeat=%ds deep=%d\n",
		scenario, runtime.Version(), runtime.GOOS, runtime.GOARCH, runtime.GOMAXPROCS(0),
		seed, seconds, workers, fixed, hb, deepDepth)

	// 预检：四种回调模式各跑一次单线程风暴，确认 //export 链接、panic/recover 路径可用。
	for mode := int32(0); mode < 4; mode++ {
		iters := int32(2)
		wantCalls := int64(iters)
		idx := int(7003+int(mode)) % len(cbPerSlot)
		atomic.StoreInt64(&cbPerSlot[idx], 0)
		var outcalls C.int64_t
		sum := int64(C.c_panic_storm(1, C.int32_t(iters), C.int32_t(7003+int(mode)), C.int32_t(mode), &outcalls))
		if got := atomic.LoadInt64(&cbPerSlot[idx]); got != wantCalls {
			failf("phase=preflight mode=%d: callbacks=%d want=%d", mode, got, wantCalls)
		}
		if want := expectedStorm(1, int64(iters), int64(7003+int(mode)), mode); sum != want {
			failf("phase=preflight mode=%d: checksum=%d want=%d", mode, sum, want)
		}
		if int64(outcalls) != wantCalls {
			failf("phase=preflight mode=%d: C out_calls=%d want=%d", mode, int64(outcalls), wantCalls)
		}
	}

	// 开局即把内存上限压到 live heap 附近。
	withLiveLimit(1.1)
	atomic.AddInt64(&limitPhases, 1)

	startBase := runtime.NumGoroutine()
	stop := make(chan struct{})
	var auxWG sync.WaitGroup
	auxWG.Add(1)
	go governor(stop, &auxWG)
	auxWG.Add(1)
	go watchdog(time.Duration(hb)*time.Second, stop, &auxWG)

	deadline := time.Now().Add(time.Duration(seconds) * time.Second)
	ws := make([]*worker, workers)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		w := &worker{id: int64(i), seed: seed*1000 + uint64(i) + 1}
		ws[i] = w
		wg.Add(1)
		go func(w *worker) {
			defer wg.Done()
			w.run(deadline, seconds, fixed)
		}(w)
	}
	wg.Wait()
	close(stop)
	auxWG.Wait()

	// quiesce：恢复环境原值并做收尾 GC。
	debug.SetMemoryLimit(origLimit)
	runtime.GC()

	var roundsTotal int64
	for _, w := range ws {
		roundsTotal += w.rounds + 1
		if seconds == 0 && w.rounds+1 != fixed {
			failf("phase=final worker=%d: rounds=%d want=%d", w.id, w.rounds+1, fixed)
		}
	}
	if roundsTotal == 0 {
		failf("phase=final: no rounds completed")
	}
	if n := atomic.LoadInt64(&panicsRecovered); n == 0 {
		failf("phase=final: no panic/recover cycle ran (panics=%d)", n)
	}
	if n := atomic.LoadInt64(&nestedPanics); n == 0 {
		failf("phase=final: no nested panic cycle ran (nested=%d)", n)
	}

	ng := waitGoroutines(startBase, 5*time.Second)
	if ng > startBase+8 {
		failf("phase=final: goroutine baseline exceeded: start=%d after=%d", startBase, ng)
	}

	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	fmt.Printf("metrics: rounds=%d beats=%d callbacks=%d storms=%d panics_recovered=%d nested_panics=%d unwind_gcs=%d limit_sets=%d limit_phases=%d assist_cpu_seconds=%.6f num_gc=%d heap_alloc=%d num_cgo_call=%d goroutines_start=%d goroutines_final=%d\n",
		roundsTotal, atomic.LoadInt64(&beats), atomic.LoadInt64(&cbCount), atomic.LoadInt64(&storms),
		atomic.LoadInt64(&panicsRecovered), atomic.LoadInt64(&nestedPanics), atomic.LoadInt64(&unwindGCs),
		atomic.LoadInt64(&limitSets), atomic.LoadInt64(&limitPhases), gcAssistSeconds(), m.NumGC,
		m.HeapAlloc, runtime.NumCgoCall(), startBase, ng)
	fmt.Println("STRESS PASS " + scenario)
}
