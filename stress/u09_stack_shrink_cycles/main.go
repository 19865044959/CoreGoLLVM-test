// U09: 栈收缩循环（P0）。
// 专打「收缩」路径（与 s03 互补：s03 打增长/展开，收缩只是顺带）：
//   - 每轮按锯齿梯子反复进入深递归（depth 递减至浅）再返回，栈随之反复长高；
//   - 返回后在浅栈处阻塞于 channel，由 shrink driver 执行 runtime.GC / debug.FreeOSMemory，
//     此时本 goroutine 栈使用远小于分配尺寸，GC 会 shrinkstack → copystack 搬移栈，
//     并重定位跨阻塞点存活的局部指针数组 / interface / slice 头；
//   - 恢复后立即逐槽校验 canary（漏重定位 → UAF/失配），再在收缩后的栈区域重建指针数组校验。
//
// 深/浅轮交替构成锯齿；每步固定「强制 GC」，最浅步用 FreeOSMemory。
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
	scenario    = "u09_stack_shrink_cycles"
	holdN       = 32 // 跨收缩存活的局部指针数组
	shrinkSlots = 16 // 阻塞点帧内的局部指针数组
	residualN   = 64 // 收缩后新建帧的指针数组
)

type node struct {
	id int64
	v  int64
}

var (
	beats int64

	started  int64
	finished int64

	canaryChecks int64
	shrinkCycles int64
	forcedGC     int64
	freeOS       int64

	sawDeep    int64
	sawShallow int64

	gSeed uint64
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

// descend: d+1 层递归，每层帧内 8 个指针槽；返回时逐层校验 canary。
// 每层固定贡献 2*local.v（base 也乘 2），checksum 公式见 descendExpected。
//
//go:noinline
func descend(d int, p *node, seed uint64, baseID int64) int64 {
	id := baseID + int64(d)
	local := &node{id: id, v: nodeMagic(seed, id)}
	var slots [8]*node
	slots[0] = local
	slots[len(slots)-1] = p
	if p == nil || p.v != nodeMagic(seed, p.id) {
		failf("phase=u09.descend d=%d: 入参指针 canary 失配", d)
	}
	if d == 0 {
		return 2 * local.v
	}
	r := descend(d-1, local, seed, baseID)
	if local.v != nodeMagic(seed, local.id) || slots[0] != local {
		failf("phase=u09.descend d=%d id=%d: 展开路径 canary 失配", d, local.id)
	}
	if slots[len(slots)-1] == nil || slots[len(slots)-1].v != nodeMagic(seed, slots[len(slots)-1].id) {
		failf("phase=u09.descend d=%d: 指针槽 canary 失配", d)
	}
	atomic.AddInt64(&canaryChecks, 2)
	return r + local.v + slots[0].v
}

// descendExpected 与 descend 的 checksum 公式一致：2 * sum(magic(baseID+k))。
func descendExpected(seed uint64, baseID int64, d int) int64 {
	var s int64
	for k := 0; k <= d; k++ {
		s += 2 * nodeMagic(seed, baseID+int64(k))
	}
	return s
}

type shrinkReq struct{ free bool }

type worker struct {
	id     int64
	seed   uint64
	base   int64
	depth  int
	reqCh  chan<- shrinkReq
	ackCh  <-chan struct{}
	rounds int64
}

// shrinkPoint: 在浅栈处阻塞，driver 执行 GC/FreeOSMemory。
// 本帧的 shrinkSlots 指针数组跨越阻塞点存活，栈被 shrink/copy 时这些槽必须被重定位。
//
//go:noinline
func (w *worker) shrinkPoint(free bool, seed uint64, baseID int64) {
	var local [shrinkSlots]*node
	for i := range local {
		id := baseID + int64(i)
		local[i] = &node{id: id, v: nodeMagic(seed, id)}
	}
	w.reqCh <- shrinkReq{free: free}
	<-w.ackCh
	// 收缩后：阻塞点帧里的指针槽必须仍有效（copystack 重定位正确）。
	for i := range local {
		if local[i] == nil || local[i].v != nodeMagic(seed, local[i].id) {
			failf("phase=u09.shrinkpoint worker=%d: local[%d] 收缩后 canary 失配（栈搬移漏重定位）", w.id, i)
		}
		atomic.AddInt64(&canaryChecks, 1)
	}
	atomic.AddInt64(&shrinkCycles, 1)
}

// residualProbe: 收缩后重新进入的帧里构造局部指针数组并逐槽校验
// （即「收缩后残留栈区域」的扫描覆盖）。
//
//go:noinline
func residualProbe(seed uint64, baseID int64) int {
	var slots [residualN]*node
	for i := range slots {
		id := baseID + int64(i)
		slots[i] = &node{id: id, v: nodeMagic(seed, id)}
	}
	var checked int
	for i := range slots {
		if slots[i] == nil || slots[i].v != nodeMagic(seed, slots[i].id) {
			failf("phase=u09.residual slot=%d: canary 失配", i)
		}
		atomic.AddInt64(&canaryChecks, 1)
		checked++
	}
	runtime.KeepAlive(slots)
	return checked
}

func (w *worker) round(r int64) {
	seed := w.seed
	base := w.base + r*10_000_000

	// 本帧的跨收缩存活数据：指针数组 + interface + slice 头。
	var hold [holdN]*node
	for i := range hold {
		id := base + int64(i)
		hold[i] = &node{id: id, v: nodeMagic(seed, id)}
	}
	holdSlice := make([]*node, 8)
	for i := range holdSlice {
		id := base + 1_000 + int64(i)
		holdSlice[i] = &node{id: id, v: nodeMagic(seed, id)}
	}
	holdIface := interface{}(hold[1])

	// 锯齿梯子：深轮逐级递减至浅；浅轮只走两步。
	var depths [4]int
	nsteps := 0
	if r%2 == 0 {
		d := w.depth
		depths = [4]int{d, d / 2, d / 4, 32}
		if depths[1] < 33 {
			depths[1] = 33
		}
		if depths[2] < 33 {
			depths[2] = 33
		}
		nsteps = 4
		atomic.AddInt64(&sawDeep, 1)
	} else {
		d := w.depth / 8
		if d < 33 {
			d = 33
		}
		depths = [4]int{d, 32, 0, 0}
		nsteps = 2
		atomic.AddInt64(&sawShallow, 1)
	}

	for step := 0; step < nsteps; step++ {
		d := depths[step]
		baseID := base + int64(step+1)*2_000_000
		if sum, want := descend(d, hold[0], seed, baseID), descendExpected(seed, baseID, d); sum != want {
			failf("phase=u09.descend worker=%d round=%d step=%d depth=%d: checksum=%d want=%d", w.id, r, step, d, sum, want)
		}

		// 收缩前校验跨调用的栈根。
		for i := range hold {
			if hold[i] == nil || hold[i].v != nodeMagic(seed, hold[i].id) {
				failf("phase=u09.pre-shrink worker=%d round=%d step=%d: hold[%d] canary 失配", w.id, r, step, i)
			}
			atomic.AddInt64(&canaryChecks, 1)
		}
		if n, ok := holdIface.(*node); !ok || n != hold[1] || n.v != nodeMagic(seed, n.id) {
			failf("phase=u09.pre-shrink worker=%d round=%d step=%d: interface 槽损坏", w.id, r, step)
		}
		for i := range holdSlice {
			if holdSlice[i] == nil || holdSlice[i].v != nodeMagic(seed, holdSlice[i].id) {
				failf("phase=u09.pre-shrink worker=%d round=%d step=%d: slice[%d] canary 失配", w.id, r, step, i)
			}
		}

		// 在浅栈处阻塞；driver 的 GC 会使本 goroutine 栈收缩（copystack）。
		free := step == nsteps-1
		w.shrinkPoint(free, seed, base+int64(step+1)*3_000_000)

		// 收缩后校验：这些槽在阻塞期间一直存活，必须被栈搬移正确重定位。
		for i := range hold {
			if hold[i] == nil || hold[i].v != nodeMagic(seed, hold[i].id) {
				failf("phase=u09.post-shrink worker=%d round=%d step=%d: hold[%d] canary 失配（栈搬移漏重定位）", w.id, r, step, i)
			}
			atomic.AddInt64(&canaryChecks, 1)
		}
		if n, ok := holdIface.(*node); !ok || n != hold[1] || n.v != nodeMagic(seed, n.id) {
			failf("phase=u09.post-shrink worker=%d round=%d step=%d: interface 槽损坏（栈搬移漏重定位）", w.id, r, step)
		}
		for i := range holdSlice {
			if holdSlice[i] == nil || holdSlice[i].v != nodeMagic(seed, holdSlice[i].id) {
				failf("phase=u09.post-shrink worker=%d round=%d step=%d: slice[%d] canary 失配（栈搬移漏重定位）", w.id, r, step, i)
			}
			atomic.AddInt64(&canaryChecks, 1)
		}

		if n := residualProbe(seed, base+int64(step+1)*4_000_000); n != residualN {
			failf("phase=u09.residual worker=%d round=%d step=%d: checked=%d want=%d", w.id, r, step, n, residualN)
		}
	}

	runtime.KeepAlive(hold)
	runtime.KeepAlive(holdIface)
	runtime.KeepAlive(holdSlice)
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

// shrinkDriver 是唯一的 GC/FreeOSMemory 发起者：worker 在浅栈阻塞时它执行 GC，
// 使 worker 栈满足 shrinkstack 条件（used < 1/4 alloc）。
func shrinkDriver(reqCh <-chan shrinkReq, ackCh chan<- struct{}, stop <-chan struct{}, wg *sync.WaitGroup) {
	defer wg.Done()
	for {
		select {
		case <-stop:
			return
		case req := <-reqCh:
			if req.free {
				debug.FreeOSMemory()
				atomic.AddInt64(&freeOS, 1)
			} else {
				runtime.GC()
				atomic.AddInt64(&forcedGC, 1)
			}
			select {
			case ackCh <- struct{}{}:
			case <-stop:
				return
			}
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
	fixed := int64(envInt("STRESS_FIXED_ROUNDS", 12, 1, 1000000))
	depth := envInt("STRESS_DEPTH", 1024, 64, 4096)
	gSeed = seed
	debug.SetMaxStack(64 << 20)

	fmt.Printf("scenario=%s phase=preflight go=%s goos=%s goarch=%s gomaxprocs=%d seed=%d seconds=%d workers=%d depth=%d fixed_rounds=%d heartbeat=%ds\n",
		scenario, runtime.Version(), runtime.GOOS, runtime.GOARCH, runtime.GOMAXPROCS(0),
		seed, seconds, workers, depth, fixed, hb)

	baseGoroutines := runtime.NumGoroutine()
	stop := make(chan struct{})
	reqCh := make(chan shrinkReq)
	ackCh := make(chan struct{})
	var aux sync.WaitGroup
	aux.Add(1)
	go shrinkDriver(reqCh, ackCh, stop, &aux)
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
			reqCh: reqCh,
			ackCh: ackCh,
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
	if s, f := atomic.LoadInt64(&started), atomic.LoadInt64(&finished); s != int64(workers) || f != s {
		failf("phase=final: workers started=%d finished=%d want=%d", s, f, workers)
	}
	if atomic.LoadInt64(&shrinkCycles) == 0 {
		failf("phase=final: 没有发生收缩点")
	}

	ng := waitBaseline(baseGoroutines, 6, 5*time.Second)
	if ng > baseGoroutines+4 {
		failf("phase=final: goroutines baseline=%d after=%d: goroutine 泄漏", baseGoroutines, ng)
	}

	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	fmt.Printf("metrics: rounds=%d shrink_cycles=%d forced_gc=%d free_os=%d canary_checks=%d saw_deep_rounds=%d saw_shallow_rounds=%d num_gc=%d heap_alloc=%d heap_objects=%d goroutines=%d\n",
		roundsTotal, atomic.LoadInt64(&shrinkCycles), atomic.LoadInt64(&forcedGC), atomic.LoadInt64(&freeOS),
		atomic.LoadInt64(&canaryChecks), atomic.LoadInt64(&sawDeep), atomic.LoadInt64(&sawShallow),
		m.NumGC, m.HeapAlloc, m.HeapObjects, ng)
	fmt.Printf("STRESS PASS %s\n", scenario)
}
