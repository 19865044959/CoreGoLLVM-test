// U08: panic/栈展开交互（P1）。
// 目标：深递归（数百层持指针帧）中 panic → recover → 再 panic 链（3 层嵌套 recover）、
// 跨展开持指针的 defer 闭包、以及深栈中反复触发的 runtime.Error（nil map 写/越界/除零）。
// 每个递归帧注册 defer 闭包，在展开路径上校验本帧指针；每层 recover 都重新校验 payload 指针。
// 每轮 GC（深栈底强制 GC + 收尾 GC），让展开中的帧与 panic payload 一起被扫描。
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

const scenario = "u08_panic_stack_interact"

type node struct {
	id int64
	v  int64
}

type payload struct {
	seed uint64
	n    *node
	id   int64
}

// counters 是每 worker 独占的计数（worker 单 goroutine 内使用，无需原子）；
// 不能用全局原子计数做「本次调用增量」断言，否则并发 worker 会互相污染增量。
type counters struct {
	deferChecks       int64
	chainRecoveries   int64
	runtimeRecoveries int64
}

var (
	beats int64

	started  int64
	finished int64

	gcCycles int64

	gSeed   uint64
	zeroDiv int64
)

// nodeMagic 与 u01 完全一致。
func nodeMagic(seed uint64, id int64) int64 {
	x := seed + uint64(id)*0x9E3779B97F4A7C15
	x ^= x >> 33
	x *= 0xff51afd7ed558ccd
	x ^= x >> 33
	x *= 0xc4ceb9fe1a85ec53
	x ^= x >> 33
	if x == 0 {
		x = 0x5DEECE66D
	}
	return int64(x)
}

func failf(format string, args ...interface{}) {
	fmt.Printf("FAIL: seed=%d "+format+"\n", append([]interface{}{gSeed}, args...)...)
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

func checkNode(n *node, seed uint64, phase string) {
	if n == nil {
		failf("phase=%s: nil *node (UAF/展开路径漏标)", phase)
	}
	if n.v != nodeMagic(seed, n.id) {
		failf("phase=%s id=%d: canary v=%#x want=%#x (UAF/展开路径漏标)",
			phase, n.id, n.v, nodeMagic(seed, n.id))
	}
}

// gcStress 在深栈底/恢复路径上强制 GC（沿用 s03 的形态）。
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

// deepPanicChain: d+1 层持指针帧，每帧 defer 闭包在展开路径上校验 local/p/slots。
// 栈底 panic 一个携带指针的 payload。
//
//go:noinline
func deepPanicChain(d int, p *node, seed uint64, baseID int64, c *counters) int64 {
	local := &node{id: baseID + int64(d), v: nodeMagic(seed, baseID+int64(d))}
	var slots [4]*node
	slots[0] = local
	slots[3] = p
	defer func() {
		// 展开路径：每帧的闭包都持有指针，GC 可能在 unwind 期间发生。
		checkNode(local, seed, "u08.chain.frame.local")
		checkNode(slots[0], seed, "u08.chain.frame.slot0")
		checkNode(p, seed, "u08.chain.frame.p")
		c.deferChecks++
	}()
	if d == 0 {
		gcStress()
		panic(&payload{seed: seed, n: local, id: local.id})
	}
	r := deepPanicChain(d-1, local, seed, baseID, c)
	if local.v != nodeMagic(seed, local.id) {
		failf("phase=u08.chain d=%d id=%d: 正常返回路径 canary 失配", d, local.id)
	}
	return r + local.v + slots[0].v
}

//go:noinline
func chainLayer3(d int, p *node, seed uint64, baseID int64, c *counters) (ok bool) {
	defer func() {
		e := recover()
		if e == nil {
			return
		}
		pl, is := e.(*payload)
		if !is {
			failf("phase=u08.chain.l3: panic payload %T", e)
		}
		if pl.seed != seed {
			failf("phase=u08.chain.l3: payload seed=%d want=%d", pl.seed, seed)
		}
		checkNode(pl.n, seed, "u08.chain.l3.payload")
		gcStress()
		c.chainRecoveries++
		np := &node{id: pl.id + 1_000_000, v: nodeMagic(seed, pl.id+1_000_000)}
		panic(&payload{seed: seed, n: np, id: np.id})
	}()
	deepPanicChain(d, p, seed, baseID, c)
	return true
}

//go:noinline
func chainLayer2(d int, p *node, seed uint64, baseID int64, c *counters) (ok bool) {
	defer func() {
		e := recover()
		if e == nil {
			return
		}
		pl, is := e.(*payload)
		if !is {
			failf("phase=u08.chain.l2: panic payload %T", e)
		}
		checkNode(pl.n, seed, "u08.chain.l2.payload")
		gcStress()
		c.chainRecoveries++
		np := &node{id: pl.id + 2_000_000, v: nodeMagic(seed, pl.id+2_000_000)}
		panic(&payload{seed: seed, n: np, id: np.id})
	}()
	chainLayer3(d, p, seed, baseID, c)
	return true
}

//go:noinline
func chainLayer1(d int, p *node, seed uint64, baseID int64, c *counters) (ok bool) {
	defer func() {
		e := recover()
		if e == nil {
			return
		}
		pl, is := e.(*payload)
		if !is {
			failf("phase=u08.chain.l1: panic payload %T", e)
		}
		checkNode(pl.n, seed, "u08.chain.l1.payload")
		gcStress()
		c.chainRecoveries++
		ok = true
	}()
	chainLayer2(d, p, seed, baseID, c)
	return false
}

// runPanicChain 要求恰好 3 层 recover：L3 收到原始 payload、L2 收到再 panic、L1 收到最终 payload。
func runPanicChain(seed uint64, depth int, baseID int64, c *counters) {
	before := c.chainRecoveries
	p := &node{id: baseID + int64(depth) + 1, v: nodeMagic(seed, baseID+int64(depth)+1)}
	ok := chainLayer1(depth, p, seed, baseID, c)
	got := c.chainRecoveries - before
	if !ok || got != 3 {
		failf("phase=u08.chain depth=%d: ok=%v recoveries=%d want=3", depth, ok, got)
	}
}

// triggerRuntimeError 在深栈底触发真实的 runtime.Error。
//
//go:noinline
func triggerRuntimeError(kind int, a, b *node) {
	switch kind {
	case 0:
		var m map[string]*node
		m["boom"] = a // nil map 写
	case 1:
		s := make([]*node, 4)
		i := len(s)
		_ = s[i] // 越界
	default:
		_ = a.id / zeroDiv // 除零（zeroDiv 为包级变量，防常量折叠）
	}
	runtime.KeepAlive(b)
	panic("u08: runtime error did not trigger")
}

// deepRuntimeError: 每帧 defer 在校验指针后放行 panic，直到顶层 recover。
//
//go:noinline
func deepRuntimeError(d int, p *node, seed uint64, baseID int64, kind int, c *counters) int64 {
	local := &node{id: baseID + int64(d), v: nodeMagic(seed, baseID+int64(d))}
	var slots [4]*node
	slots[0] = local
	slots[3] = p
	defer func() {
		checkNode(local, seed, "u08.runtime.frame.local")
		checkNode(slots[0], seed, "u08.runtime.frame.slot0")
		checkNode(p, seed, "u08.runtime.frame.p")
		c.deferChecks++
	}()
	if d == 0 {
		gcStress()
		triggerRuntimeError(kind, local, p)
	}
	r := deepRuntimeError(d-1, local, seed, baseID, kind, c)
	return r + local.v
}

// recoverRuntimeError 在顶层 recover，校验 error 类型为 runtime.Error 且跨展开的指针仍有效。
//
//go:noinline
func recoverRuntimeError(seed uint64, depth int, baseID int64, kind int, c *counters) (ok bool, msg string) {
	top := &node{id: baseID, v: nodeMagic(seed, baseID)}
	defer func() {
		e := recover()
		if e == nil {
			return
		}
		re, is := e.(runtime.Error)
		if !is {
			failf("phase=u08.runtime kind=%d: panic %T 不是 runtime.Error: %v", kind, e, e)
		}
		checkNode(top, seed, "u08.runtime.top")
		gcStress()
		c.runtimeRecoveries++
		msg = re.Error()
		ok = true
	}()
	deepRuntimeError(depth, top, seed, baseID+1, kind, c)
	return false, ""
}

type worker struct {
	id     int64
	seed   uint64
	base   int64
	depth  int
	rounds int64
	c      counters
}

func (w *worker) round(r int64) {
	base := w.base + r*4_000_000
	wantDefers := int64(w.depth) + 1

	// 1) 深栈 panic → 3 层 recover/再 panic 链。
	beforeDefers := w.c.deferChecks
	runPanicChain(w.seed, w.depth, base+1_000_000, &w.c)
	if got := w.c.deferChecks - beforeDefers; got != wantDefers {
		failf("phase=u08.chain worker=%d round=%d: 展开帧 defer 校验=%d want=%d", w.id, r, got, wantDefers)
	}

	// 2) 深栈中的 runtime.Error 反复触发 + recover。
	for kind := 0; kind < 3; kind++ {
		beforeDefers = w.c.deferChecks
		baseID := base + int64(kind+2)*1_000_000
		ok, msg := recoverRuntimeError(w.seed, w.depth, baseID, kind, &w.c)
		if !ok || msg == "" {
			failf("phase=u08.runtime worker=%d round=%d kind=%d: 未成功 recover ok=%v msg=%q", w.id, r, kind, ok, msg)
		}
		if got := w.c.deferChecks - beforeDefers; got != wantDefers {
			failf("phase=u08.runtime worker=%d round=%d kind=%d: 展开帧 defer 校验=%d want=%d", w.id, r, kind, got, wantDefers)
		}
	}

	runtime.GC()
	atomic.AddInt64(&gcCycles, 1)
	w.rounds++
	atomic.AddInt64(&beats, 1)
}

func (w *worker) run(seconds int, deadline time.Time, fixed int64) {
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
				failf("phase=watchdog: 6 个心跳周期无进展 (%s, beats=%d)", time.Since(lastChange), cur)
			}
		}
	}
}

func waitBaseline(base, need int, grace time.Duration) int {
	giveUp := time.Now().Add(grace)
	stable := 0
	for {
		n := runtime.NumGoroutine()
		if n <= base+4 {
			stable++
			if stable >= need {
				return n
			}
		} else {
			stable = 0
		}
		if time.Now().After(giveUp) {
			return runtime.NumGoroutine()
		}
		time.Sleep(10 * time.Millisecond)
	}
}

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
	workers := envInt("STRESS_WORKERS", 4, 1, 8)
	hb := envInt("STRESS_HEARTBEAT", 5, 1, 60)
	seed := envSeed(1)
	fixed := int64(envInt("STRESS_FIXED_ROUNDS", 16, 1, 1000000))
	depth := envInt("STRESS_DEPTH", 320, 16, 2048)
	gSeed = seed
	debug.SetMaxStack(64 << 20)

	fmt.Printf("scenario=%s phase=preflight go=%s goos=%s goarch=%s gomaxprocs=%d seed=%d seconds=%d workers=%d depth=%d fixed_rounds=%d heartbeat=%ds\n",
		scenario, runtime.Version(), runtime.GOOS, runtime.GOARCH, runtime.GOMAXPROCS(0),
		seed, seconds, workers, depth, fixed, hb)

	baseGoroutines := runtime.NumGoroutine()
	stop := make(chan struct{})
	var aux sync.WaitGroup
	aux.Add(1)
	go watchdog(time.Duration(hb)*time.Second, stop, &aux)

	deadline := time.Now().Add(time.Duration(seconds) * time.Second)
	ws := make([]*worker, workers)
	for i := 0; i < workers; i++ {
		ws[i] = &worker{
			id:    int64(i),
			seed:  seed*1_000_003 + uint64(i+1)*7919,
			base:  int64(i)*1_000_000_000 + 1_000_000,
			depth: depth,
		}
	}

	var wg sync.WaitGroup
	for _, w := range ws {
		wg.Add(1)
		go func(w *worker) {
			atomic.AddInt64(&started, 1)
			defer func() {
				atomic.AddInt64(&finished, 1)
				wg.Done()
			}()
			w.run(seconds, deadline, fixed)
		}(w)
	}
	wg.Wait()
	close(stop)
	aux.Wait()

	var roundsTotal, chainRecoveries, runtimeRecoveries, deferChecks int64
	for _, w := range ws {
		roundsTotal += w.rounds
		chainRecoveries += w.c.chainRecoveries
		runtimeRecoveries += w.c.runtimeRecoveries
		deferChecks += w.c.deferChecks
		if seconds == 0 && w.rounds != fixed {
			failf("phase=final worker=%d: rounds=%d want=%d", w.id, w.rounds, fixed)
		}
	}
	if roundsTotal == 0 {
		failf("phase=final: 没有完成任何轮次")
	}
	wantRecoveries := roundsTotal * 3
	if chainRecoveries != wantRecoveries {
		failf("phase=final: chain recoveries=%d want=%d", chainRecoveries, wantRecoveries)
	}
	if want := roundsTotal * 3; runtimeRecoveries != want {
		failf("phase=final: runtime recoveries=%d want=%d", runtimeRecoveries, want)
	}
	wantDefers := roundsTotal * 4 * (int64(depth) + 1)
	if deferChecks != wantDefers {
		failf("phase=final: frame defer 校验=%d want=%d", deferChecks, wantDefers)
	}
	if s, f := atomic.LoadInt64(&started), atomic.LoadInt64(&finished); s != int64(workers) || f != s {
		failf("phase=final: workers started=%d finished=%d want=%d", s, f, workers)
	}

	ng := waitBaseline(baseGoroutines, 6, 5*time.Second)
	if ng > baseGoroutines+4 {
		failf("phase=final: goroutines baseline=%d after=%d: goroutine 泄漏", baseGoroutines, ng)
	}

	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	fmt.Printf("metrics: rounds=%d chain_recoveries=%d runtime_recoveries=%d frame_defers=%d gc_churn=%d num_gc=%d heap_alloc=%d heap_objects=%d goroutines=%d\n",
		roundsTotal, chainRecoveries, runtimeRecoveries, deferChecks, atomic.LoadInt64(&gcCycles),
		m.NumGC, m.HeapAlloc, m.HeapObjects, ng)
	fmt.Printf("STRESS PASS %s\n", scenario)
}
