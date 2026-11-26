// X09: ABI0 汇编帧 args/results stackmap 长跑（linux/amd64，BUILD_MODE=module）。
//
// 手写 .s 中的函数均为 NOSPLIT + 严格 ABI0：
//   - asmPtrCall：*node 参数/返回值；asm 内 CALL ·helperCanary，指针参数只由汇编帧的
//     args pointer map 保活（driver 内 p 在调用后即死）。
//   - asmPtrReturn：Go helper 返回 *node，asm 先写入 result 槽并 GO_RESULTS_INITIALIZED，
//     再 CALL ·helperSink（GC+批量分配）——结果指针只由汇编帧 results bitmap 保活。
//   - asmLoop：计数循环内每轮 CALL ·helperCanary，p 活跨全部调用。
//   - asmChain0..4：NOSPLIT 自递归会被链接器判为 infinite cycle，故用 5 个互不相同的
//     NOSPLIT 汇编函数组成有界递归链，每层调用 Go helper 并把 helper 返回值透传。
//
// 对照 Go 版本（goChain）校验结果一致。helper 内 runtime.GC 后批量分配同 size class
// 对象，任何未被正确标记的汇编帧指针都会被 clobberfree/复用暴露。
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

const (
	scenario = "x09_asm_abi0_stackmap"

	helperBatch = 64 // helper 每次分配的对象数（与 node 同 size class）
	loopN       = 8  // asmLoop 计数循环轮数
	chainN      = 4  // asmChain0..3 各调一次 helper，asmChain4 为基例
)

type node struct {
	v    int64
	name string
}

var (
	beats       int64
	rounds      int64
	ptrCalls    int64
	ptrReturns  int64
	loopCalls   int64
	chainCalls  int64
	goChainRuns int64

	asmSink  atomic.Pointer[[]*node] // helperCanary/helperSink 的存活 batch
	asmSink2 atomic.Pointer[[]*node] // GC 后分配的垃圾 batch（迫使复用被误回收的槽）
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

// ---- Go helper：被汇编帧 CALL；内部 GC + 批量分配 ----

func canaryExpected() int64 {
	var s int64
	for i := 0; i < helperBatch; i++ {
		s += int64(i) ^ 0x5A5A
	}
	return s ^ 0xA5A5
}

// helperCanary 分配一批 node、GC、再分配一批同 size class 垃圾，最后读回第一批求和。
// 返回值是由 batch 内容决定的常数；GC 后读回保证对象存活语义正确。
//
//go:noinline
func helperCanary() int64 {
	pre := make([]*node, helperBatch)
	for i := range pre {
		pre[i] = &node{v: int64(i) ^ 0x5A5A, name: "x09h"}
	}
	asmSink.Store(&pre)
	runtime.GC()
	garbage := make([]*node, helperBatch)
	for i := range garbage {
		garbage[i] = &node{v: -int64(i) - 1, name: "x09g"}
	}
	asmSink2.Store(&garbage)
	var s int64
	for _, n := range pre {
		if n == nil || n.name != "x09h" {
			failf("phase=helper context=canary: batch node corrupted")
		}
		s += n.v
	}
	return s ^ 0xA5A5
}

// helperAlloc 返回一个 GC 后仍有效的 *node（供 asm result 槽保活测试）。
//
//go:noinline
func helperAlloc() *node {
	q := &node{v: 0x9E3779B9, name: "x09alloc"}
	runtime.GC()
	return q
}

// helperSink 在 asm 结果指针已写入 result 槽后触发 GC，并分配同 size class 垃圾。
//
//go:noinline
func helperSink() int64 {
	garbage := make([]*node, helperBatch)
	for i := range garbage {
		garbage[i] = &node{v: -int64(i) - 1, name: "x09s"}
	}
	asmSink2.Store(&garbage)
	runtime.GC()
	return 0
}

// goChain 是 asmChain0..4 的纯 Go 对照实现（相同 helper 调用次数与累加顺序）。
//
//go:noinline
func goChainImpl(p *node, n int64) int64 {
	if n == 0 {
		return p.v
	}
	v := p.v
	c := helperCanary()
	r := goChainImpl(p, n-1)
	if p.v != v || p.name != "x09go" {
		failf("phase=go-chain: p corrupted n=%d v=%d want=%d", n, p.v, v)
	}
	return r + c + p.v
}

//go:noinline
func goChainValue(x, n int64) int64 {
	p := &node{v: x, name: "x09go"}
	return goChainImpl(p, n)
}

// ---- 汇编函数声明（实现见 asm_amd64.s） ----

func asmPtrCall(p *node, x int64) (ret *node, canary int64)
func asmPtrReturn(p *node, x int64) (ret *node, chk int64)
func asmLoop(p *node, n int64) int64
func asmChain0(p *node, n int64) int64
func asmChain1(p *node, n int64) int64
func asmChain2(p *node, n int64) int64
func asmChain3(p *node, n int64) int64
func asmChain4(p *node, n int64) int64

// ---- driver：p 在调用汇编后不再使用，确保汇编帧的 args pointer map 是唯一根 ----

//go:noinline
func drivePtrCall(x int64) (*node, int64) {
	p := &node{v: x, name: "x09p"}
	return asmPtrCall(p, x)
}

//go:noinline
func drivePtrReturn(x int64) (*node, int64) {
	p := &node{v: x, name: "x09ret"}
	q, chk := asmPtrReturn(p, x)
	return q, chk
}

//go:noinline
func driveLoop(x, n int64) int64 {
	p := &node{v: x, name: "x09loop"}
	return asmLoop(p, n)
}

//go:noinline
func driveChain(x, n int64) int64 {
	p := &node{v: x, name: "x09chain"}
	return asmChain0(p, n)
}

func checkRound(tag string, x int64, hc int64) {
	q, c := drivePtrCall(x)
	if q == nil || q.v != x || q.name != "x09p" {
		failf("phase=asm-ptr-call tag=%s x=%d: arg pointer corrupted q=%v", tag, x, q)
	}
	if c != hc+x {
		failf("phase=asm-ptr-call tag=%s x=%d: helper result/pointer read corrupted c=%d want=%d", tag, x, c, hc+x)
	}
	atomic.AddInt64(&ptrCalls, 1)

	pq, chk := drivePtrReturn(x)
	if pq == nil || pq.v != 0x9E3779B9 || pq.name != "x09alloc" {
		failf("phase=asm-ptr-return tag=%s x=%d: result pointer corrupted q=%v", tag, x, pq)
	}
	if chk != x {
		failf("phase=asm-ptr-return tag=%s x=%d: arg pointer across helperSink corrupted chk=%d", tag, chk)
	}
	atomic.AddInt64(&ptrReturns, 1)

	if got, want := driveLoop(x, loopN), x+int64(loopN)*hc; got != want {
		failf("phase=asm-loop tag=%s x=%d: checksum=%d want=%d", tag, x, got, want)
	}
	atomic.AddInt64(&loopCalls, 1)

	if got, want := driveChain(x, chainN), goChainValue(x, chainN); got != want {
		failf("phase=asm-chain tag=%s x=%d: asm=%d go=%d", tag, x, got, want)
	}
	atomic.AddInt64(&chainCalls, 1)
	atomic.AddInt64(&goChainRuns, 1)

	// 调用者再次持 q/pq 跨 GC：验证返回值本身在 Go 侧仍有效。
	runtime.GC()
	if q.v != x || q.name != "x09p" {
		failf("phase=post-gc tag=%s x=%d: returned arg pointer corrupted", tag, x)
	}
	if pq.v != 0x9E3779B9 || pq.name != "x09alloc" {
		failf("phase=post-gc tag=%s x=%d: returned result pointer corrupted", tag, x)
	}
}

type worker struct {
	id     int64
	seed   uint64
	rounds int64
}

func (w *worker) round(r int64) {
	x := int64(w.id)*100003 + int64(w.seed%(1<<20))*7 + r*131 + 1
	checkRound("worker", x, canaryExpected())
	w.rounds = r
	atomic.AddInt64(&rounds, 1)
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

func main() {
	setupCrashOutput()
	seconds := envInt("STRESS_SECONDS", 60, 0, 86400)
	workers := envInt("STRESS_WORKERS", 4, 1, 32)
	hb := envInt("STRESS_HEARTBEAT", 5, 1, 60)
	seed := envSeed(1)
	fixed := int64(envInt("STRESS_FIXED_ROUNDS", 64, 1, 1000000))

	hc := canaryExpected()
	fmt.Printf("scenario=%s phase=preflight go=%s goos=%s goarch=%s gomaxprocs=%d seed=%d seconds=%d workers=%d fixed_rounds=%d heartbeat=%ds helper_batch=%d loop_n=%d chain_n=%d canary=%d\n",
		scenario, runtime.Version(), runtime.GOOS, runtime.GOARCH, runtime.GOMAXPROCS(0),
		seed, seconds, workers, fixed, hb, helperBatch, loopN, chainN, hc)

	if got := helperCanary(); got != hc {
		failf("phase=preflight: helper canary=%d want=%d", got, hc)
	}
	// 预检覆盖每条汇编形状（含 GC 后指针读回）。
	checkRound("preflight", 123456789, hc)
	checkRound("preflight", -987654321, hc)

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
	if atomic.LoadInt64(&ptrCalls) == 0 || atomic.LoadInt64(&ptrReturns) == 0 ||
		atomic.LoadInt64(&loopCalls) == 0 || atomic.LoadInt64(&chainCalls) == 0 {
		failf("phase=final: some asm shape did not run: ptr=%d ret=%d loop=%d chain=%d",
			atomic.LoadInt64(&ptrCalls), atomic.LoadInt64(&ptrReturns),
			atomic.LoadInt64(&loopCalls), atomic.LoadInt64(&chainCalls))
	}

	ng := waitGoroutines(startBase, 5*time.Second)
	if ng > startBase+4 {
		failf("phase=final: goroutine baseline exceeded: start=%d after=%d", startBase, ng)
	}

	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	fmt.Printf("metrics: rounds=%d beats=%d ptr_calls=%d ptr_returns=%d loop_calls=%d chain_calls=%d go_chain=%d num_gc=%d heap_alloc=%d goroutines_start=%d goroutines_final=%d\n",
		roundsTotal, atomic.LoadInt64(&beats), atomic.LoadInt64(&ptrCalls), atomic.LoadInt64(&ptrReturns),
		atomic.LoadInt64(&loopCalls), atomic.LoadInt64(&chainCalls), atomic.LoadInt64(&goChainRuns),
		m.NumGC, m.HeapAlloc, startBase, ng)
	fmt.Println("STRESS PASS " + scenario)
}
