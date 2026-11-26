// X04: LockOSThread + 线程退出 + STW 风暴（P1，覆盖 L04/L17）。
// 目标：把栈增长/收缩与 LockOSThread、locked goroutine 退出（oldm 线程退出路径）、
// ReadMemStats 引发的 STW、并发 GC churn 叠加在一起：
//   - worker 每轮 LockOSThread → 深递归（持指针数组，沿途多次显式 GC）→ 浅递归 →
//     GC/weak 探针校验 → UnlockOSThread；
//   - 另有 LockOSThread 后直接 runtime.Goexit 的子 goroutine（不 Unlock）——goroutine
//     退出即线程退出，模拟 oldm 路径；defer 链在退出过程中再 GC 并校验 canary；
//   - 观察者 goroutine 周期 runtime.ReadMemStats（每次短暂 STW）；
//   - 独立 GC churn goroutine 持续 runtime.GC。
//
// debug.SetMaxStack 限制最大栈，深度远低于上限，防真溢出。
// 判据只有确定性 canary / weak 存活探针 / 计数一致性；不做墙钟吞吐断言。
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
	"weak"
)

const (
	scenario = "x04_lockosthread_stw"

	deepSlots    = 16 // 每层递归帧的指针槽
	shallowDepth = 6
	shallowIters = 16
	exitSlots    = 4
	gcEveryDepth = 64 // 递归路径上每隔多少层显式 GC（含栈移动/STW）

	anchorOff     = 7
	deepBaseOff   = 1_000
	shallowBase   = 500_000
	exitBaseOff   = 900_000
	exitIDStride  = 16
	roundStride   = 10_000_000
	maxDepthLimit = 4096
)

type node struct {
	id  int64
	v   int64
	pad [2]uint64
}

var (
	beats int64

	roundsTotal int64

	lockAcquired int64
	lockReleased int64

	exitSpawned  int64
	exitFinished int64
	exitGCs      int64

	canaryChecks int64
	weakChecks   int64
	gcCycles     int64
	stwReads     int64

	gSeed uint64
)

// nodeMagic 与 u01 完全一致：确定性 canary。
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

func newNode(seed uint64, id int64) *node {
	return &node{id: id, v: nodeMagic(seed, id)}
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
		failf("phase=%s: nil *node (UAF/栈扫描漏标)", phase)
	}
	if n.v != nodeMagic(seed, n.id) {
		failf("phase=%s id=%d: canary v=%#x want=%#x (UAF/栈扫描漏标)",
			phase, n.id, n.v, nodeMagic(seed, n.id))
	}
	atomic.AddInt64(&canaryChecks, 1)
}

func expectedSum(seed uint64, baseID int64, n int) int64 {
	var s int64
	for i := 0; i < n; i++ {
		s += nodeMagic(seed, baseID+int64(i))
	}
	return s
}

// deepRecurse: d+1 层递归帧，每帧 16 个指针槽；返回途中与栈底都显式 GC。
// 栈增长/搬移后每帧重新校验本帧指针与 canary。
//
//go:noinline
func deepRecurse(d int, parent *node, seed uint64, baseID int64) int64 {
	id := baseID + int64(d)
	local := newNode(seed, id)
	var slots [deepSlots]*node
	slots[0] = local
	slots[deepSlots/2] = parent
	for i := 1; i < deepSlots; i++ {
		if i != deepSlots/2 {
			slots[i] = local
		}
	}
	if d%gcEveryDepth == 0 {
		// 递归深处 GC：正在增长的栈被扫描/搬移，STW 与 morestack 交错。
		runtime.GC()
		atomic.AddInt64(&gcCycles, 1)
		checkNode(slots[0], seed, "x04.deep.post-gc.slot0")
		checkNode(slots[deepSlots/2], seed, "x04.deep.post-gc.parent")
	}
	if d == 0 {
		checkNode(local, seed, "x04.deep.bottom.local")
		return local.v
	}
	r := deepRecurse(d-1, local, seed, baseID)
	// 子层返回后重新读取本帧：栈可能已经增长/收缩过。
	checkNode(slots[0], seed, "x04.deep.unwind.slot0")
	checkNode(slots[deepSlots-1], seed, "x04.deep.unwind.slotLast")
	if slots[0] != local || slots[deepSlots/2] != parent {
		failf("phase=x04.deep d=%d: 帧内指针被改写", d)
	}
	return r + local.v
}

// shallowRecurse: 浅递归，用于在 Unlock 前再压一次小帧栈增长。
//
//go:noinline
func shallowRecurse(d int, parent *node, seed uint64, baseID int64) int64 {
	local := newNode(seed, baseID+int64(d))
	var slots [4]*node
	slots[0] = local
	slots[3] = parent
	if d == 0 {
		checkNode(local, seed, "x04.shallow.bottom")
		return local.v
	}
	r := shallowRecurse(d-1, local, seed, baseID)
	checkNode(slots[0], seed, "x04.shallow.unwind.slot0")
	checkNode(slots[3], seed, "x04.shallow.unwind.parent")
	return r + local.v
}

type exitResult struct {
	base int64
	sum  int64
}

// exitLocked: LockOSThread 后工作，最后 runtime.Goexit 且不 Unlock——
// 按 runtime 语义该 goroutine 退出时 OS 线程也随之退出（oldm 路径）。
// defer 链在退出过程中再强制 GC 并校验本帧全量 canary，然后交回校验和。
//
//go:noinline
func exitLocked(seed uint64, baseID int64, done chan<- exitResult) {
	runtime.LockOSThread()
	atomic.AddInt64(&exitSpawned, 1)

	var arr [exitSlots]*node
	for i := 0; i < exitSlots; i++ {
		arr[i] = newNode(seed, baseID+int64(i))
	}
	local := newNode(seed, baseID+exitSlots)
	checkNode(local, seed, "x04.exit.body.local")
	for i := range arr {
		checkNode(arr[i], seed, "x04.exit.body.arr")
	}

	// 退出前 GC：locked 线程上的活跃栈被扫描。
	runtime.GC()
	atomic.AddInt64(&gcCycles, 1)

	defer func() {
		// Goexit 展开时执行：线程即将销毁，栈帧仍在，GC 再扫一遍。
		runtime.GC()
		atomic.AddInt64(&gcCycles, 1)
		atomic.AddInt64(&exitGCs, 1)
		sum := int64(0)
		for i := range arr {
			checkNode(arr[i], seed, "x04.exit.defer.arr")
			sum += arr[i].v
		}
		checkNode(local, seed, "x04.exit.defer.local")
		sum += local.v
		atomic.AddInt64(&exitFinished, 1)
		done <- exitResult{base: baseID, sum: sum}
	}()

	runtime.Goexit()
	failf("phase=x04.exit: runtime.Goexit 返回了")
}

type worker struct {
	id     int64
	seed   uint64
	base   int64
	depth  int
	exits  int
	rounds int64
	checks int64
	exitCh chan exitResult
}

func (w *worker) round(r int64) {
	base := w.base + r*roundStride

	// 1) LockOSThread 内做深/浅递归 + GC。
	runtime.LockOSThread()
	atomic.AddInt64(&lockAcquired, 1)

	anchor := newNode(w.seed, base+anchorOff)
	wk := weak.Make(anchor)
	if wk.Value() != anchor {
		failf("phase=x04.weak.pre worker=%d round=%d: 持强引用时 weak.Value()=nil", w.id, r)
	}

	sum := deepRecurse(w.depth, anchor, w.seed, base+deepBaseOff)
	if want := expectedSum(w.seed, base+deepBaseOff, w.depth+1); sum != want {
		failf("phase=x04.deep worker=%d round=%d: sum=%#x want=%#x", w.id, r, sum, want)
	}

	for i := 0; i < shallowIters; i++ {
		sbase := base + shallowBase + int64(i)*100
		got := shallowRecurse(shallowDepth, anchor, w.seed, sbase)
		if want := expectedSum(w.seed, sbase, shallowDepth+1); got != want {
			failf("phase=x04.shallow worker=%d round=%d i=%d: sum=%#x want=%#x", w.id, r, i, got, want)
		}
	}

	// 切到 GC 点：anchor 仍被本帧强引用，weak 探针必须看到对象存活。
	runtime.GC()
	atomic.AddInt64(&gcCycles, 1)
	if wk.Value() != anchor {
		failf("phase=x04.weak.post worker=%d round=%d: GC 后强引用对象 weak.Value()=nil", w.id, r)
	}
	atomic.AddInt64(&weakChecks, 1)
	runtime.KeepAlive(anchor)
	runtime.KeepAlive(wk)

	runtime.UnlockOSThread()
	atomic.AddInt64(&lockReleased, 1)

	// 2) LockOSThread 后直接 Goexit 的子 goroutine（不 Unlock）。
	for i := 0; i < w.exits; i++ {
		ebase := base + exitBaseOff + int64(i)*exitIDStride
		go exitLocked(w.seed, ebase, w.exitCh)
	}
	seen := make(map[int64]bool, w.exits)
	for i := 0; i < w.exits; i++ { // 并发退出，到达顺序不定：按 base 匹配
		got := <-w.exitCh
		valid := false
		for j := 0; j < w.exits; j++ {
			if got.base == base+exitBaseOff+int64(j)*exitIDStride {
				valid = true
				break
			}
		}
		if !valid || seen[got.base] {
			failf("phase=x04.exit worker=%d round=%d: 非法/重复 base=%d", w.id, r, got.base)
		}
		seen[got.base] = true
		want := expectedSum(w.seed, got.base, exitSlots) + nodeMagic(w.seed, got.base+exitSlots)
		if got.sum != want {
			failf("phase=x04.exit worker=%d round=%d base=%d: sum=%#x want=%#x", w.id, r, got.base, got.sum, want)
		}
	}

	w.rounds++
	atomic.AddInt64(&roundsTotal, 1)
	w.checks += int64(w.depth+1)*2 + shallowIters*(shallowDepth+1)
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

// stwObserver: 周期 ReadMemStats，制造短 STW；只记录次数，不做时延断言。
func stwObserver(stop <-chan struct{}, wg *sync.WaitGroup) {
	defer wg.Done()
	var m runtime.MemStats
	for {
		select {
		case <-stop:
			return
		default:
		}
		runtime.ReadMemStats(&m)
		atomic.AddInt64(&stwReads, 1)
		time.Sleep(2 * time.Millisecond) // 节奏控制，非断言
	}
}

func gcChurn(stop <-chan struct{}, wg *sync.WaitGroup) {
	defer wg.Done()
	for {
		select {
		case <-stop:
			return
		default:
		}
		runtime.GC()
		atomic.AddInt64(&gcCycles, 1)
		runtime.Gosched()
		time.Sleep(time.Millisecond) // 节奏控制，非断言
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
	workers := envInt("STRESS_WORKERS", 4, 1, 32)
	hb := envInt("STRESS_HEARTBEAT", 5, 1, 60)
	seed := envSeed(1)
	fixed := int64(envInt("STRESS_FIXED_ROUNDS", 8, 1, 1000000))
	depth := envInt("STRESS_DEPTH", 384, 16, maxDepthLimit)
	exits := envInt("STRESS_EXITS", 2, 1, 16)
	gSeed = seed

	// 深递归每帧 ~200B（16 指针 + 开销），远小于 64MiB 上限。
	if est := int64(depth+1) * 512; est > 32<<20 {
		failf("phase=preflight: depth=%d 估算栈用量 %d 超过安全线", depth, est)
	}
	debug.SetMaxStack(64 << 20)

	fmt.Printf("scenario=%s phase=preflight go=%s goos=%s goarch=%s gomaxprocs=%d seed=%d seconds=%d workers=%d depth=%d exits_per_round=%d fixed_rounds=%d heartbeat=%ds gc_every_depth=%d\n",
		scenario, runtime.Version(), runtime.GOOS, runtime.GOARCH, runtime.GOMAXPROCS(0),
		seed, seconds, workers, depth, exits, fixed, hb, gcEveryDepth)

	baseGoroutines := runtime.NumGoroutine()
	stop := make(chan struct{})
	var aux sync.WaitGroup
	aux.Add(1)
	go stwObserver(stop, &aux)
	aux.Add(1)
	go gcChurn(stop, &aux)
	aux.Add(1)
	go watchdog(time.Duration(hb)*time.Second, stop, &aux)

	deadline := time.Now().Add(time.Duration(seconds) * time.Second)
	ws := make([]*worker, workers)
	for i := 0; i < workers; i++ {
		ws[i] = &worker{
			id:     int64(i),
			seed:   seed*1_000_003 + uint64(i+1)*7919,
			base:   int64(i)*1_000_000_000 + 1_000_000,
			depth:  depth,
			exits:  exits,
			exitCh: make(chan exitResult, exits),
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

	var rounds, checks int64
	for _, w := range ws {
		rounds += w.rounds
		checks += w.checks
		if seconds == 0 && w.rounds != fixed {
			failf("phase=final worker=%d: rounds=%d want=%d", w.id, w.rounds, fixed)
		}
	}
	if rounds == 0 {
		failf("phase=final: 没有完成任何轮次")
	}

	if s, f := atomic.LoadInt64(&exitSpawned), atomic.LoadInt64(&exitFinished); s != f || s != rounds*int64(exits) {
		failf("phase=final: exit goroutines spawned=%d finished=%d want=%d", s, f, rounds*int64(exits))
	}
	if a, r := atomic.LoadInt64(&lockAcquired), atomic.LoadInt64(&lockReleased); a != r || a != rounds {
		failf("phase=final: LockOSThread=%d UnlockOSThread=%d rounds=%d", a, r, rounds)
	}
	if wc := atomic.LoadInt64(&weakChecks); wc != rounds {
		failf("phase=final: weak 探针=%d want=%d", wc, rounds)
	}
	if atomic.LoadInt64(&canaryChecks) == 0 {
		failf("phase=final: 没有执行 canary 校验")
	}

	ng := waitBaseline(baseGoroutines, 6, 5*time.Second)
	if ng > baseGoroutines+4 {
		failf("phase=final: goroutines baseline=%d after=%d: goroutine 泄漏", baseGoroutines, ng)
	}

	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	fmt.Printf("metrics: rounds=%d frame_checks=%d canary_checks=%d weak_probes=%d lock=%d unlock=%d exit_spawned=%d exit_finished=%d exit_gcs=%d stw_reads=%d gc_churn=%d num_gc=%d heap_alloc=%d heap_objects=%d goroutines=%d\n",
		rounds, checks, atomic.LoadInt64(&canaryChecks), atomic.LoadInt64(&weakChecks),
		atomic.LoadInt64(&lockAcquired), atomic.LoadInt64(&lockReleased),
		atomic.LoadInt64(&exitSpawned), atomic.LoadInt64(&exitFinished), atomic.LoadInt64(&exitGCs),
		atomic.LoadInt64(&stwReads), atomic.LoadInt64(&gcCycles),
		m.NumGC, m.HeapAlloc, m.HeapObjects, ng)
	fmt.Printf("STRESS PASS %s\n", scenario)
}
