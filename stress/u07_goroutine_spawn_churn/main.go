// U07: goroutine 生成/退出翻腾（P0）。
// 目标：goroutine 退出窗口 + 栈缓存（stackpool）复用时的栈扫描。
// 每 worker 每轮 spawn perRound 个短 goroutine，各持 canary 对象（nodeMagic，与 u01 同一探针）：
//
//	mode=0: defer 闭包持指针，退出路径上校验并把指针交回父 goroutine；
//	mode=1: panic 携指针，recover 后校验并交回；
//	mode=2: Goexit 前把指针交回 channel，defer 在退出路径上继续持指针校验。
//
// 主循环在 spawn 中途与轮末强制 GC，另有 GC churn goroutine 持续 GC，
// 让「正在退出的 goroutine 栈」与栈缓存复用反复被扫描；父 goroutine 在子退出后再验同一批对象。
// 收尾 quiesce 后多次采样 NumGoroutine 必须回基线。
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
	scenario  = "u07_goroutine_spawn_churn"
	perRound  = 192 // 每轮每 worker 的短 goroutine 数（数百）
	bodySlots = 8
)

type node struct {
	id    int64
	v     int64
	mode  int32
	round int64
}

var (
	beats int64

	started  int64 // 已启动的短 goroutine
	finished int64 // 已退出的短 goroutine

	canaryChecks int64
	gcCycles     int64

	spawnedDefer  int64
	spawnedPanic  int64
	spawnedGoexit int64

	gSeed uint64
)

// nodeMagic 与 u01 完全一致：确定性的 canary，任何漏扫描/复用都会失配。
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

func checkNode(n *node, seed uint64, phase string, wantMode int32) {
	if n == nil {
		failf("phase=%s: nil *node (UAF/栈扫描漏标)", phase)
	}
	if n.v != nodeMagic(seed, n.id) {
		failf("phase=%s id=%d mode=%d round=%d: canary v=%#x want=%#x (UAF/栈扫描漏标)",
			phase, n.id, n.mode, n.round, n.v, nodeMagic(seed, n.id))
	}
	if n.mode != wantMode {
		failf("phase=%s id=%d round=%d: mode=%d want=%d", phase, n.id, n.round, n.mode, wantMode)
	}
	atomic.AddInt64(&canaryChecks, 1)
}

// spawnDefer: defer 闭包持指针，退出路径（函数返回）上校验并交回父 goroutine。
//
//go:noinline
func spawnDefer(seed uint64, id int64, round int64, out chan<- *node) {
	const mode = int32(0)
	n := &node{id: id, v: nodeMagic(seed, id), mode: mode, round: round}
	var slots [bodySlots]*node
	slots[0] = n
	for i := 1; i < bodySlots; i++ {
		sid := id*31 + int64(i)
		slots[i] = &node{id: sid, v: nodeMagic(seed, sid), mode: mode, round: round}
	}
	defer func() {
		// 退出窗口：闭包持有的指针仍必须可读且 canary 正确。
		for i := range slots {
			checkNode(slots[i], seed, "u07.defer.exit", mode)
		}
		out <- n
	}()
	for i := range slots {
		checkNode(slots[i], seed, "u07.defer.body", mode)
	}
	runtime.Gosched()
}

// spawnPanic: panic 携带指针，recover 后校验，随后退出。
//
//go:noinline
func spawnPanic(seed uint64, id int64, round int64, out chan<- *node) {
	const mode = int32(1)
	n := &node{id: id, v: nodeMagic(seed, id), mode: mode, round: round}
	var slots [4]*node
	slots[0] = n
	for i := 1; i < len(slots); i++ {
		sid := id*17 + int64(i)
		slots[i] = &node{id: sid, v: nodeMagic(seed, sid), mode: mode, round: round}
	}
	defer func() {
		e := recover()
		if e == nil {
			failf("phase=u07.panic id=%d round=%d: panic 未传播", id, round)
		}
		p, ok := e.(*node)
		if !ok || p != n {
			failf("phase=u07.panic id=%d round=%d: payload=%T 不是同一 *node", id, round, e)
		}
		for i := range slots {
			checkNode(slots[i], seed, "u07.panic.recover", mode)
		}
		out <- n
	}()
	panic(n)
}

// spawnGoexit: Goexit 前把指针交回 channel，defer 在退出路径上继续校验。
//
//go:noinline
func spawnGoexit(seed uint64, id int64, round int64, out chan<- *node) {
	const mode = int32(2)
	n := &node{id: id, v: nodeMagic(seed, id), mode: mode, round: round}
	var slots [4]*node
	slots[0] = n
	for i := 1; i < len(slots); i++ {
		sid := id*13 + int64(i)
		slots[i] = &node{id: sid, v: nodeMagic(seed, sid), mode: mode, round: round}
	}
	defer func() {
		// Goexit 会执行整条调用链上的 defer；此时栈正在退出。
		for i := range slots {
			checkNode(slots[i], seed, "u07.goexit.defer", mode)
		}
	}()
	for i := range slots {
		checkNode(slots[i], seed, "u07.goexit.body", mode)
	}
	out <- n // Goexit 前把指针交回父 goroutine
	runtime.Goexit()
}

type worker struct {
	id     int64
	seed   uint64
	base   int64
	out    chan *node
	rounds int64
}

func (w *worker) round(r int64) {
	idBase := w.base + r*perRound
	var wg sync.WaitGroup
	for i := 0; i < perRound; i++ {
		id := idBase + int64(i)
		k := i % 3
		switch k {
		case 0:
			atomic.AddInt64(&spawnedDefer, 1)
		case 1:
			atomic.AddInt64(&spawnedPanic, 1)
		default:
			atomic.AddInt64(&spawnedGoexit, 1)
		}
		wg.Add(1)
		go func(k int, id int64) {
			atomic.AddInt64(&started, 1)
			defer func() {
				atomic.AddInt64(&finished, 1)
				wg.Done()
			}()
			switch k {
			case 0:
				spawnDefer(w.seed, id, r, w.out)
			case 1:
				spawnPanic(w.seed, id, r, w.out)
			default:
				spawnGoexit(w.seed, id, r, w.out)
			}
		}(k, id)
		if i == perRound/2 {
			// 一半子 goroutine 已启动：让 GC 与它们的退出窗口重叠。
			runtime.GC()
			atomic.AddInt64(&gcCycles, 1)
		}
	}

	// 收齐本轮的指针：每个短 goroutine 恰好交回一个，id 必须唯一且落在本轮区间。
	seen := make([]bool, perRound)
	for i := 0; i < perRound; i++ {
		n := <-w.out
		j := n.id - idBase
		if j < 0 || j >= perRound {
			failf("phase=u07.parent worker=%d round=%d: id=%d 越界", w.id, r, n.id)
		}
		if seen[j] {
			failf("phase=u07.parent worker=%d round=%d: 重复 id=%d", w.id, r, n.id)
		}
		seen[j] = true
		// 子 goroutine 已走完退出路径（Goexit 的收尾可能与本次校验重叠），再验一遍。
		checkNode(n, w.seed, "u07.parent.after-exit", int32(j%3))
	}
	wg.Wait()
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

func gcChurn(stop <-chan struct{}, wg *sync.WaitGroup) {
	defer wg.Done()
	t := time.NewTicker(5 * time.Millisecond)
	defer t.Stop()
	sink := make([]byte, 2048)
	for {
		select {
		case <-stop:
			return
		case <-t.C:
			sink[0]++
			runtime.GC()
			atomic.AddInt64(&gcCycles, 1)
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
				failf("phase=watchdog: 6 个心跳周期无进展 (%s, beats=%d)", time.Since(lastChange), cur)
			}
		}
	}
}

// waitBaseline 连续多次采样确认 goroutine 数回到基线；超时后由调用方判失败。
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
	workers := envInt("STRESS_WORKERS", 4, 1, 16)
	hb := envInt("STRESS_HEARTBEAT", 5, 1, 60)
	seed := envSeed(1)
	fixed := int64(envInt("STRESS_FIXED_ROUNDS", 16, 1, 1000000))
	gSeed = seed
	debug.SetMaxStack(64 << 20)

	fmt.Printf("scenario=%s phase=preflight go=%s goos=%s goarch=%s gomaxprocs=%d seed=%d seconds=%d workers=%d fixed_rounds=%d heartbeat=%ds spawn_per_round=%d\n",
		scenario, runtime.Version(), runtime.GOOS, runtime.GOARCH, runtime.GOMAXPROCS(0),
		seed, seconds, workers, fixed, hb, perRound)

	baseGoroutines := runtime.NumGoroutine()
	stop := make(chan struct{})
	var aux sync.WaitGroup
	aux.Add(1)
	go gcChurn(stop, &aux)
	aux.Add(1)
	go watchdog(time.Duration(hb)*time.Second, stop, &aux)

	deadline := time.Now().Add(time.Duration(seconds) * time.Second)
	ws := make([]*worker, workers)
	for i := 0; i < workers; i++ {
		ws[i] = &worker{
			id:   int64(i),
			seed: seed*1_000_003 + uint64(i+1)*7919,
			base: int64(i)*1_000_000_000 + 1_000_000,
			out:  make(chan *node, perRound),
		}
	}

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

	var roundsTotal int64
	for _, w := range ws {
		roundsTotal += w.rounds
		if seconds == 0 && w.rounds != fixed {
			failf("phase=final worker=%d: rounds=%d want=%d", w.id, w.rounds, fixed)
		}
	}
	if roundsTotal == 0 {
		failf("phase=final: 没有完成任何轮次")
	}
	wantSpawns := roundsTotal * perRound
	if s, f := atomic.LoadInt64(&started), atomic.LoadInt64(&finished); s != wantSpawns || f != s {
		failf("phase=final: goroutines started=%d finished=%d want=%d (started!=finished 表示有 goroutine 未退出)",
			s, f, wantSpawns)
	}

	ng := waitBaseline(baseGoroutines, 6, 5*time.Second)
	if ng > baseGoroutines+4 {
		failf("phase=final: goroutines baseline=%d after=%d: goroutine 泄漏", baseGoroutines, ng)
	}

	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	fmt.Printf("metrics: rounds=%d spawned=%d defer=%d panic=%d goexit=%d canary_checks=%d gc_churn=%d num_gc=%d heap_alloc=%d heap_objects=%d goroutines=%d\n",
		roundsTotal, wantSpawns, atomic.LoadInt64(&spawnedDefer), atomic.LoadInt64(&spawnedPanic),
		atomic.LoadInt64(&spawnedGoexit), atomic.LoadInt64(&canaryChecks), atomic.LoadInt64(&gcCycles),
		m.NumGC, m.HeapAlloc, m.HeapObjects, ng)
	fmt.Printf("STRESS PASS %s\n", scenario)
}
