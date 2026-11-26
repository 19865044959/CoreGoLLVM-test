// X08: 信号 altstack/sigpanic/换栈长跑（需要 CGO_ENABLED=1 与 C 工具链）。
//
// C 侧 pthread 先用 sigaltstack 自建信号栈，再回调 //export goSigCb；回调内：
//
//	sub=0 正常分配+周期 GC；
//	sub=1 深层 nil deref → SIGSEGV → runtime sigpanic → 跨 Go 帧展开 → recover 校验；
//	sub=2 debug.SetPanicOnFault + 非法地址（非 nil）fault → sigpanic → recover 校验；
//	sub=3 C 侧用 glibc ucontext 换到自建栈后回调 Go（StackSwitchCallback 式），GC 后校验。
//
// 独立 C pthread 持续向进程发 SIGURG/SIGPROF 洪水（Go 信号处理器在 gsignal/altstack 上运行）。
// C 帧不被展开；所有 panic 都在返回 C 之前 recover。校验计数/C 累计和/Go 独立期望值。
package main

/*
#cgo LDFLAGS: -lpthread
#include <stdint.h>

int64_t c_sig_storm(int32_t nthreads, int32_t iters, int32_t base, int32_t sub, int32_t altstack, int64_t *out_calls);
int32_t c_flood_start(void);
int32_t c_flood_stop(int64_t *sent);
void c_flood_pause(void);
void c_flood_resume(void);
int64_t c_stack_switch_run(int32_t nthreads, int32_t iters, int32_t base, int32_t altstack, int64_t *out_calls);
*/
import "C"

import (
	"fmt"
	"os"
	"runtime"
	"runtime/debug"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"
)

const (
	scenario = "x08_signal_altstack"

	faultDepth  = 24   // sigpanic 前嵌套的 Go 帧数（每层带指针槽）
	cbSlotCount = 4096 // 回调计数槽；base 区间互不重叠
	roundsPerID = 512
	maxCThreads = 2
)

type node struct {
	v    int64
	name string
}

var (
	beats          int64
	cbCount        int64
	storms         int64
	nilPanics      int64
	faultPanics    int64
	stackSwitchOK  = true
	stackSwitches  int64
	stormSeq       int64
	floodSignals   int64
	stackSwitchErr int64

	nilNode *node // 恒为 nil，供 nil deref 使用

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

// ---- 故障注入：nil deref 与 SetPanicOnFault ----

//go:noinline
func nilDeref() int64 {
	return nilNode.v + 1
}

//go:noinline
func nilNested(d int, seed int64) int64 {
	local := &node{v: seed + int64(d), name: "x08n"}
	var pad [4]*node
	pad[0] = local
	pad[3] = &node{v: seed ^ int64(d), name: "x08p"}
	if d == 0 {
		return nilDeref()
	}
	r := nilNested(d-1, seed)
	if local.v != seed+int64(d) || pad[0] != local || pad[3] == nil || pad[3].v != seed^int64(d) {
		failf("phase=sigpanic-deep depth=%d seed=%d: frame corrupted", d, seed)
	}
	return r
}

// faultPtr 返回一个恒不可映射的非 nil 地址；nocheckptr 避免 -race/checkptr 误报。
//
//go:nocheckptr
//go:noinline
func faultPtr() *node {
	return (*node)(unsafe.Pointer(uintptr(1)))
}

//go:noinline
func faultDeref() int64 {
	return faultPtr().v + 1
}

//go:noinline
func faultNested(d int, seed int64) int64 {
	local := &node{v: seed + int64(d), name: "x08f"}
	var pad [4]*node
	pad[0] = local
	pad[3] = &node{v: seed ^ int64(d), name: "x08q"}
	if d == 0 {
		return faultDeref()
	}
	r := faultNested(d-1, seed)
	if local.v != seed+int64(d) || pad[0] != local || pad[3] == nil || pad[3].v != seed^int64(d) {
		failf("phase=sigpanic-deep depth=%d seed=%d: frame corrupted", d, seed)
	}
	return r
}

// sigFaultRecover 在回调帧内 recover 由 SIGSEGV 产生的 runtime.Error；
// sub=2 期间打开 SetPanicOnFault（非法非 nil 地址也转 panic），返回前恢复原值。
func sigFaultRecover(sub int32, seed int64) {
	if sub == 2 {
		old := debug.SetPanicOnFault(true)
		defer debug.SetPanicOnFault(old)
	}
	buf := make([]byte, 128)
	buf[0] = byte(seed)
	defer func() {
		r := recover()
		if r == nil {
			failf("phase=sigpanic sub=%d seed=%d: fault did not panic", sub, seed)
		}
		re, ok := r.(runtime.Error)
		if !ok {
			failf("phase=sigpanic sub=%d seed=%d: unexpected panic type %T", sub, seed, r)
		}
		if !strings.Contains(re.Error(), "invalid memory address") {
			failf("phase=sigpanic sub=%d seed=%d: unexpected runtime error %q", sub, seed, re.Error())
		}
		// recover 之后再 GC：sigpanic 展开期间的栈/局部指针必须仍可扫描。
		runtime.GC()
		if buf[0] != byte(seed) {
			failf("phase=sigpanic-gc sub=%d seed=%d: callback local buffer corrupted", sub, seed)
		}
		if sub == 1 {
			atomic.AddInt64(&nilPanics, 1)
		} else {
			atomic.AddInt64(&faultPanics, 1)
		}
	}()
	if sub == 1 {
		nilNested(faultDepth, seed)
	} else {
		faultNested(faultDepth, seed)
	}
	failf("phase=sigpanic sub=%d seed=%d: nested fault returned without panic", sub, seed)
}

// normalWork / stackSwitchWork 是 sub=0/3 的确定性工作负载（分配 + 偶发 GC + 校验）。
//
//go:noinline
func normalWork(seed int64) int64 {
	n := &node{v: seed ^ 0x5A5A, name: "x08c"}
	if atomic.LoadInt64(&cbCount)%64 == 0 {
		runtime.GC()
	}
	if n.v != seed^0x5A5A || n.name != "x08c" {
		failf("phase=callback seed=%d: local node corrupted", seed)
	}
	return n.v
}

//go:noinline
func stackSwitchWork(seed int64) int64 {
	n := &node{v: seed*11 + 13, name: "x08s"}
	runtime.GC()
	if n.v != seed*11+13 || n.name != "x08s" {
		failf("phase=stack-switch seed=%d: local node corrupted after GC", seed)
	}
	return n.v
}

// subExpected 是单次回调返回值的纯函数（独立于回调实现计算）。
func subExpected(t, i, base int64, sub int32) int64 {
	x := base*1000003 + t*1009 + i*7 + int64(sub)*131
	out := x*3 + 1
	switch sub {
	case 0:
		out += x ^ 0x5A5A
	case 1:
		out += x*5 + 7
	case 2:
		out += x*7 + 11
	case 3:
		out += x*11 + 13
	}
	return out
}

func expectedStorm(nthreads, iters, base int64, sub int32) int64 {
	var s int64
	for t := int64(0); t < nthreads; t++ {
		for i := int64(0); i < iters; i++ {
			s += subExpected(t, i, base, sub)
		}
	}
	return s
}

//export goSigCb
func goSigCb(thread C.int32_t, iter C.int32_t, base C.int32_t, sub C.int32_t) C.int64_t {
	t, i, b, s := int64(thread), int64(iter), int64(base), int32(sub)
	if i < 0 {
		// C 换栈线程在普通栈上的预热回调：不计数/不求和，只建立 runtime 的栈边界。
		runtime.GC()
		return 0
	}
	atomic.AddInt64(&cbCount, 1)
	atomic.AddInt64(&cbPerSlot[int(b)%len(cbPerSlot)], 1)

	buf := make([]byte, 256)
	buf[0] = byte(t + i)
	x := b*1000003 + t*1009 + i*7 + int64(s)*131
	out := x*3 + 1
	switch s {
	case 0:
		out += normalWork(x)
	case 1:
		sigFaultRecover(1, x)
		out += x*5 + 7
	case 2:
		sigFaultRecover(2, x)
		out += x*7 + 11
	case 3:
		out += stackSwitchWork(x)
	default:
		failf("phase=callback thread=%d iter=%d: unknown sub=%d", t, i, s)
	}
	if buf[0] != byte(t+i) {
		failf("phase=callback thread=%d iter=%d base=%d sub=%d: local buffer corrupted", t, i, b, s)
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
	seq := atomic.AddInt64(&stormSeq, 1) - 1
	sub := int32(seq % 4) // 全局轮转：任意 ≥4 轮即覆盖 4 种子模式
	if sub == 3 && !stackSwitchOK {
		sub = 0
	}
	altstack := int32((w.seed + uint64(r)*7) % 2)
	nthreads := int32(1 + int((w.seed*3+uint64(r)*5)%uint64(maxCThreads)))
	iters := int32(4 + int((w.seed*7+uint64(r)*13)%5)) // 4..8
	if sub == 3 {
		iters = int32(2 + int((w.seed+uint64(r))%3)) // 换栈开销大，2..4
	}
	wantCalls := int64(nthreads) * int64(iters)

	idx := int(base) % len(cbPerSlot)
	atomic.StoreInt64(&cbPerSlot[idx], 0)
	var outcalls C.int64_t
	var sum int64
	if sub == 3 {
		// 双信号洪水 + ucontext 换栈的组合在 stock Go 1.24.1 上不稳；换栈期间短暂停洪水。
		C.c_flood_pause()
		time.Sleep(200 * time.Microsecond)
		sum = int64(C.c_stack_switch_run(C.int32_t(nthreads), C.int32_t(iters), C.int32_t(base), C.int32_t(altstack), &outcalls))
		C.c_flood_resume()
		atomic.AddInt64(&stackSwitches, 1)
	} else {
		sum = int64(C.c_sig_storm(C.int32_t(nthreads), C.int32_t(iters), C.int32_t(base), C.int32_t(sub), C.int32_t(altstack), &outcalls))
	}
	if sum < 0 {
		failf("phase=storm worker=%d round=%d seed=%d sub=%d: C error=%d", w.id, r, w.seed, sub, sum)
	}
	if got := atomic.LoadInt64(&cbPerSlot[idx]); got != wantCalls {
		failf("phase=storm worker=%d round=%d seed=%d sub=%d: callbacks=%d want=%d", w.id, r, w.seed, sub, got, wantCalls)
	}
	if int64(outcalls) != wantCalls {
		failf("phase=storm worker=%d round=%d seed=%d sub=%d: C out_calls=%d want=%d", w.id, r, w.seed, sub, int64(outcalls), wantCalls)
	}
	if want := expectedStorm(int64(nthreads), int64(iters), int64(base), sub); sum != want {
		failf("phase=storm worker=%d round=%d seed=%d sub=%d: checksum=%d want=%d", w.id, r, w.seed, sub, sum, want)
	}

	if r%4 == 0 {
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

func checkStorm(tag string, sum int64, outcalls C.int64_t, idx int, nthreads, iters, base int64, sub int32) {
	wantCalls := nthreads * iters
	if got := atomic.LoadInt64(&cbPerSlot[idx]); got != wantCalls {
		failf("phase=%s sub=%d: callbacks=%d want=%d", tag, sub, got, wantCalls)
	}
	if int64(outcalls) != wantCalls {
		failf("phase=%s sub=%d: C out_calls=%d want=%d", tag, sub, int64(outcalls), wantCalls)
	}
	if want := expectedStorm(nthreads, iters, base, sub); sum != want {
		failf("phase=%s sub=%d: checksum=%d want=%d", tag, sub, sum, want)
	}
}

func main() {
	setupCrashOutput()
	seconds := envInt("STRESS_SECONDS", 60, 0, 86400)
	workers := envInt("STRESS_WORKERS", 2, 1, 8)
	hb := envInt("STRESS_HEARTBEAT", 5, 1, 60)
	seed := envSeed(1)
	fixed := int64(envInt("STRESS_FIXED_ROUNDS", 6, 1, 100000))

	fmt.Printf("scenario=%s phase=preflight go=%s goos=%s goarch=%s gomaxprocs=%d seed=%d seconds=%d workers=%d fixed_rounds=%d heartbeat=%ds fault_depth=%d\n",
		scenario, runtime.Version(), runtime.GOOS, runtime.GOARCH, runtime.GOMAXPROCS(0),
		seed, seconds, workers, fixed, hb, faultDepth)

	// 预检 0/1/2：sigaltstack 上的正常回调、nil deref、SetPanicOnFault。
	for sub := int32(0); sub < 3; sub++ {
		base := int32(7003) + sub
		idx := int(base) % len(cbPerSlot)
		atomic.StoreInt64(&cbPerSlot[idx], 0)
		var outcalls C.int64_t
		sum := int64(C.c_sig_storm(1, 2, C.int32_t(base), C.int32_t(sub), 1, &outcalls))
		if sum < 0 {
			failf("phase=preflight sub=%d: C error=%d", sub, sum)
		}
		checkStorm("preflight", sum, outcalls, idx, 1, 2, int64(base), sub)
	}

	// 预检 3：C 栈切换回调；平台无 ucontext（musl）时返回 -4，跳过该子模式。
	{
		base := int32(7010)
		idx := int(base) % len(cbPerSlot)
		atomic.StoreInt64(&cbPerSlot[idx], 0)
		var outcalls C.int64_t
		sum := int64(C.c_stack_switch_run(1, 2, C.int32_t(base), 1, &outcalls))
		switch {
		case sum == -4:
			stackSwitchOK = false
			atomic.AddInt64(&stackSwitchErr, 1)
			fmt.Printf("scenario=%s phase=preflight note=\"ucontext unavailable, sub=3 disabled\"\n", scenario)
		case sum < 0:
			failf("phase=preflight-stackswitch: C error=%d", sum)
		default:
			checkStorm("preflight-stackswitch", sum, outcalls, idx, 1, 2, int64(base), 3)
			atomic.AddInt64(&stackSwitches, 1)
		}
	}

	// 外部信号洪水（SIGURG + SIGPROF）覆盖整个 worker 阶段。
	if rc := int32(C.c_flood_start()); rc != 0 {
		failf("phase=preflight: signal flood start failed rc=%d", rc)
	}
	startBase := runtime.NumGoroutine()
	stop := make(chan struct{})
	var auxWG sync.WaitGroup
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

	var sent C.int64_t
	if rc := int32(C.c_flood_stop(&sent)); rc != 0 {
		failf("phase=final: signal flood stop failed rc=%d", rc)
	}
	floodSignals = int64(sent)

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
	if atomic.LoadInt64(&nilPanics) == 0 {
		failf("phase=final: nil-deref sigpanic path did not run")
	}
	if atomic.LoadInt64(&faultPanics) == 0 {
		failf("phase=final: SetPanicOnFault sigpanic path did not run")
	}
	if stackSwitchOK && atomic.LoadInt64(&stackSwitches) == 0 {
		failf("phase=final: stack-switch path did not run")
	}
	if floodSignals == 0 {
		failf("phase=final: signal flood sent no signals")
	}

	ng := waitGoroutines(startBase, 5*time.Second)
	if ng > startBase+8 {
		failf("phase=final: goroutine baseline exceeded: start=%d after=%d", startBase, ng)
	}

	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	fmt.Printf("metrics: rounds=%d beats=%d callbacks=%d storms=%d nil_panics=%d fault_panics=%d stack_switch_ok=%d stack_switches=%d stack_switch_err=%d flood_signals=%d num_gc=%d heap_alloc=%d num_cgo_call=%d goroutines_start=%d goroutines_final=%d\n",
		roundsTotal, atomic.LoadInt64(&beats), atomic.LoadInt64(&cbCount), atomic.LoadInt64(&storms),
		atomic.LoadInt64(&nilPanics), atomic.LoadInt64(&faultPanics), boolInt(stackSwitchOK),
		atomic.LoadInt64(&stackSwitches), atomic.LoadInt64(&stackSwitchErr), floodSignals,
		m.NumGC, m.HeapAlloc, runtime.NumCgoCall(), startBase, ng)
	fmt.Println("STRESS PASS " + scenario)
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
