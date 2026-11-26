// X06: iter.Pull 协程切换（P0，覆盖 L01/L16/L17）。
// 目标：coro（m 栈上的 coroswitch）与 GC 扫描两个 goroutine 栈的交界面：
//   - 生成器逐次产出带指针 payload（canary），消费者每次 pull 之间显式 runtime.GC()：
//     此时 consumer 栈（上一 payload/anchors）与挂起的 coro 栈（生成器局部 nd/held）上
//     都有活跃指针，GC 必须同时正确扫描；
//   - 嵌套 Pull：生成器内部再 iter.Pull 另一个生成器（两层 coro，一条 m 上多次切换）；
//   - 生成器内 panic 传播到消费者 recover（iter.Pull 的 panicValue 路径），panic 后
//     next 返回零值 false、stop 幂等；
//   - stop 提前停止：next 之后恒为 false，生成器 stopped 计数、goroutine 回基线；
//   - LockOSThread 变体：coro 继承线程锁状态，切换必须校验 m/lock 一致；
//   - 高速切换阶段用固定轮数控制总量（不做墙钟吞吐断言），并行 GC churn。
//
// 仅标准库；iter.Pull 需要 go1.23+（官方 1.24 基线）。
package main

import (
	"fmt"
	"iter"
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
	scenario = "x06_coro_iterpull"

	gcPulls     = 24    // 每次 pull 之间 GC 的段（窗口最窄）
	fastPulls   = 16384 // 高速切换段总量（不逐次 GC，固定轮数控制总量）
	nestedOuter = 48    // 嵌套 Pull 外层产量
	nestedInner = 64    // 内层产量（须大于外层，保证内层不先耗尽）
	nestedGC    = 8     // 嵌套段每 N 次 pull 一次 GC
	lockedPulls = 8     // LockOSThread 段提前 stop 前 pull 数
	panicIDBase = 1_000_000

	roundStride = 100_000_000
	gcBaseOff   = 1_000
	fastBaseOff = 10_000_000
	outerBase   = 20_000_000
	innerBase   = 30_000_000
	panicBase   = 40_000_000
	lockBase    = 50_000_000
	anchorOff   = 7
)

type node struct {
	id  int64
	v   int64
	pad [2]uint64
}

type payload struct {
	id   int64
	v    int64
	n    *node
	box  any
	list []*node
}

type genPanicPayload struct {
	seed uint64
	id   int64
	n    *node
}

type genCounters struct {
	completed int64
	stopped   int64
}

var (
	beats int64

	workerStarted  int64
	workerFinished int64

	roundsTotal int64

	gcPullTotal   int64
	fastPullTotal int64
	nestedPullTot int64

	genCompleted int64
	genStopped   int64

	panicPropagated int64
	stopEarly       int64

	canaryChecks int64
	weakChecks   int64
	gcCycles     int64

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
		failf("phase=%s: nil *node (coro 栈扫描漏标/UAF)", phase)
	}
	if n.v != nodeMagic(seed, n.id) {
		failf("phase=%s id=%d: canary v=%#x want=%#x (coro 栈扫描漏标/UAF)",
			phase, n.id, n.v, nodeMagic(seed, n.id))
	}
	atomic.AddInt64(&canaryChecks, 1)
}

func checkPayload(p payload, seed uint64, phase string) {
	if p.id != p.n.id || p.v != p.n.v {
		failf("phase=%s: payload id=%d n.id=%d v=%#x n.v=%#x", phase, p.id, p.n.id, p.v, p.n.v)
	}
	checkNode(p.n, seed, phase+".n")
	if got, ok := p.box.(*node); !ok || got != p.n {
		failf("phase=%s: box=%T/%v 不是同一 *node", phase, p.box, p.box)
	}
	if len(p.list) != 2 || p.list[0] != p.n {
		failf("phase=%s: list len=%d 首项不符", phase, len(p.list))
	}
	checkNode(p.list[1], seed, phase+".list1")
}

// checkNestedPayload: 嵌套段的外层 payload 的 box 指向内层 payload 的对象。
func checkNestedPayload(p payload, seed uint64, phase string) {
	if p.id != p.n.id || p.v != p.n.v {
		failf("phase=%s: payload id=%d n.id=%d v=%#x n.v=%#x", phase, p.id, p.n.id, p.v, p.n.v)
	}
	checkNode(p.n, seed, phase+".outer")
	in, ok := p.box.(*node)
	if !ok {
		failf("phase=%s: box=%T 不是 *node", phase, p.box)
	}
	checkNode(in, seed, phase+".inner")
	if len(p.list) != 2 || p.list[0] != p.n || p.list[1] != in {
		failf("phase=%s: list len=%d 内容不符", phase, len(p.list))
	}
}

// genSeq: 每次 yield 带指针 payload；yield 挂起期间 coro 栈上的局部指针
// （nd/held/sib）必须被 GC 正确扫描；恢复后立即校验。
func genSeq(seed uint64, baseID int64, n int, c *genCounters) iter.Seq[payload] {
	return func(yield func(payload) bool) {
		var held [4]*node
		for i := 0; i < n; i++ {
			id := baseID + int64(i)
			nd := newNode(seed, id)
			sib := newNode(seed, id+panicIDBase)
			held[0] = nd
			held[1] = nd
			held[2] = sib
			held[3] = sib
			p := payload{id: id, v: nodeMagic(seed, id), n: nd, box: nd, list: []*node{nd, sib}}
			if !yield(p) {
				c.stopped++
				return
			}
			// 从 coroswitch 恢复：消费者可能刚跑过 GC。
			checkNode(nd, seed, "x06.gen.resume.nd")
			if held[0] != nd || held[2] != sib {
				failf("phase=x06.gen.resume: held 指针被改写")
			}
			runtime.KeepAlive(held)
			runtime.KeepAlive(sib)
		}
		c.completed++
	}
}

// genNested: 生成器内部再 iter.Pull 另一生成器（coro 套 coro）。
func genNested(seed uint64, outBase, inBase int64, outN, inN int, c *genCounters) iter.Seq[payload] {
	return func(yield func(payload) bool) {
		innerC := &genCounters{}
		next, stop := iter.Pull(genSeq(seed, inBase, inN, innerC))
		defer stop()
		for i := 0; i < outN; i++ {
			iv, ok := next()
			if !ok {
				break
			}
			checkPayload(iv, seed, "x06.nested.inner")
			id := outBase + int64(i)
			on := newNode(seed, id)
			p := payload{id: id, v: nodeMagic(seed, id), n: on, box: iv.n, list: []*node{on, iv.n}}
			if !yield(p) {
				c.stopped++
				return
			}
			checkNode(on, seed, "x06.nested.resume.on")
			checkNode(iv.n, seed, "x06.nested.resume.inner")
		}
		c.completed++
	}
}

// panicSeq: 产出首个值后在 coro 内 panic，iter.Pull 在消费者 next 处重新 panic。
func panicSeq(seed uint64, baseID int64, c *genCounters) iter.Seq[payload] {
	return func(yield func(payload) bool) {
		nd := newNode(seed, baseID)
		sib := newNode(seed, baseID+1)
		p := payload{id: baseID, v: nodeMagic(seed, baseID), n: nd, box: nd, list: []*node{nd, sib}}
		if !yield(p) {
			c.stopped++
			return
		}
		checkNode(nd, seed, "x06.panic.before")
		panic(&genPanicPayload{seed: seed, id: baseID, n: nd})
	}
}

type worker struct {
	id     int64
	seed   uint64
	base   int64
	rounds int64
}

// phaseGCWindow: 每次 pull 之间显式 GC——consumer 栈与挂起的 coro 栈同时持针。
func (w *worker) phaseGCWindow(base int64) {
	c := &genCounters{}
	next, stop := iter.Pull(genSeq(w.seed, base, gcPulls, c))
	anchor := newNode(w.seed, base+anchorOff)
	wk := weak.Make(anchor)
	got := 0
	var last payload
	for {
		p, ok := next()
		if !ok {
			break
		}
		wantID := base + int64(got)
		if p.id != wantID || p.v != nodeMagic(w.seed, wantID) {
			failf("phase=x06.gc worker=%d: id=%d want=%d", w.id, p.id, wantID)
		}
		checkPayload(p, w.seed, "x06.gc.pull")
		last = p
		got++
		runtime.GC()
		atomic.AddInt64(&gcCycles, 1)
		checkPayload(last, w.seed, "x06.gc.post")
		if wk.Value() != anchor {
			failf("phase=x06.gc worker=%d: GC 后 strong-reachable weak.Value()=nil", w.id)
		}
		atomic.AddInt64(&weakChecks, 1)
		runtime.KeepAlive(anchor)
		runtime.KeepAlive(last)
	}
	stop()
	if got != gcPulls {
		failf("phase=x06.gc worker=%d: pulls=%d want=%d", w.id, got, gcPulls)
	}
	if c.completed != 1 || c.stopped != 0 {
		failf("phase=x06.gc worker=%d: gen completed=%d stopped=%d want=1/0", w.id, c.completed, c.stopped)
	}
	if z, ok := next(); ok || z.n != nil || z.id != 0 {
		failf("phase=x06.gc worker=%d: 结束后 next 仍返回", w.id)
	}
	stop()
	atomic.AddInt64(&genCompleted, 1)
	atomic.AddInt64(&gcPullTotal, int64(got))
}

// phaseFast: 高速切换段，用固定总量控制；指针校验只做轻量 canary。
func (w *worker) phaseFast(base int64) {
	c := &genCounters{}
	next, stop := iter.Pull(genSeq(w.seed, base, fastPulls, c))
	got := 0
	var sum, want int64
	var last payload
	for {
		p, ok := next()
		if !ok {
			break
		}
		wantID := base + int64(got)
		if p.id != wantID || p.v != nodeMagic(w.seed, wantID) || p.n == nil || p.n.v != p.v {
			failf("phase=x06.fast worker=%d: id=%d want=%d canary 失配", w.id, p.id, wantID)
		}
		last = p
		sum += p.v
		want += nodeMagic(w.seed, wantID)
		got++
	}
	stop()
	if got != fastPulls {
		failf("phase=x06.fast worker=%d: pulls=%d want=%d", w.id, got, fastPulls)
	}
	if sum != want {
		failf("phase=x06.fast worker=%d: checksum=%#x want=%#x", w.id, sum, want)
	}
	checkPayload(last, w.seed, "x06.fast.last")
	if c.completed != 1 || c.stopped != 0 {
		failf("phase=x06.fast worker=%d: gen completed=%d stopped=%d want=1/0", w.id, c.completed, c.stopped)
	}
	atomic.AddInt64(&genCompleted, 1)
	atomic.AddInt64(&fastPullTotal, int64(got))
}

// phaseNested: 生成器内再 Pull 另一个生成器；每 N 次 pull 一次 GC。
func (w *worker) phaseNested(base int64) {
	c := &genCounters{}
	next, stop := iter.Pull(genNested(w.seed, base, base+100_000, nestedOuter, nestedInner, c))
	got := 0
	var last payload
	for {
		p, ok := next()
		if !ok {
			break
		}
		wantID := base + int64(got)
		if p.id != wantID {
			failf("phase=x06.nested worker=%d: id=%d want=%d", w.id, p.id, wantID)
		}
		checkNestedPayload(p, w.seed, "x06.nested.pull")
		last = p
		got++
		if got%nestedGC == 0 {
			runtime.GC()
			atomic.AddInt64(&gcCycles, 1)
			checkNestedPayload(last, w.seed, "x06.nested.post")
		}
	}
	stop()
	if got != nestedOuter {
		failf("phase=x06.nested worker=%d: pulls=%d want=%d", w.id, got, nestedOuter)
	}
	if c.completed != 1 || c.stopped != 0 {
		failf("phase=x06.nested worker=%d: gen completed=%d stopped=%d want=1/0", w.id, c.completed, c.stopped)
	}
	if z, ok := next(); ok || z.n != nil {
		failf("phase=x06.nested worker=%d: 结束后 next 仍返回", w.id)
	}
	stop()
	atomic.AddInt64(&genCompleted, 1)
	atomic.AddInt64(&nestedPullTot, int64(got))
}

// phasePanic: 生成器 panic → 消费者 next 处 re-panic → recover → 帧/指针校验。
func (w *worker) phasePanic(base int64) {
	c := &genCounters{}
	next, stop := iter.Pull(panicSeq(w.seed, base, c))
	v, ok := next()
	if !ok {
		failf("phase=x06.panic worker=%d: 首个值未返回", w.id)
	}
	if v.id != base || v.v != nodeMagic(w.seed, base) {
		failf("phase=x06.panic worker=%d: 首个值 id=%d", w.id, v.id)
	}
	checkPayload(v, w.seed, "x06.panic.first")

	func() {
		defer func() {
			e := recover()
			if e == nil {
				failf("phase=x06.panic worker=%d: next 未传播 panic", w.id)
			}
			pl, is := e.(*genPanicPayload)
			if !is {
				failf("phase=x06.panic worker=%d: panic payload 类型=%T", w.id, e)
			}
			runtime.GC()
			atomic.AddInt64(&gcCycles, 1)
			checkNode(pl.n, w.seed, "x06.panic.recover")
			atomic.AddInt64(&panicPropagated, 1)
		}()
		next() // 第二次 pull 触发 coro 内 panic
		failf("phase=x06.panic worker=%d: next 未 panic", w.id)
	}()

	if z, ok := next(); ok || z.n != nil || z.id != 0 {
		failf("phase=x06.panic worker=%d: panic 后 next 仍返回值", w.id)
	}
	stop() // done=true，幂等
	stop()
	if c.stopped != 0 || c.completed != 0 {
		failf("phase=x06.panic worker=%d: gen completed=%d stopped=%d want=0/0", w.id, c.completed, c.stopped)
	}
}

// phaseLocked: LockOSThread 后创建 Pull（coro 继承线程锁状态），提前 stop，
// 校验 stop 后 next 恒 false、stopped 计数与线程锁状态一致。
func (w *worker) phaseLocked(base int64) {
	runtime.LockOSThread()
	c := &genCounters{}
	next, stop := iter.Pull(genSeq(w.seed, base, lockedPulls*4, c))
	got := 0
	for i := 0; i < lockedPulls; i++ {
		p, ok := next()
		if !ok {
			failf("phase=x06.locked worker=%d: 第 %d 次 pull 提前结束", w.id, i)
		}
		wantID := base + int64(i)
		if p.id != wantID || p.v != nodeMagic(w.seed, wantID) {
			failf("phase=x06.locked worker=%d: id=%d want=%d", w.id, p.id, wantID)
		}
		checkNode(p.n, w.seed, "x06.locked.pull")
		got++
		if i%2 == 0 {
			runtime.Gosched()
		}
	}
	stop() // 提前停止
	if z, ok := next(); ok || z.n != nil || z.id != 0 {
		failf("phase=x06.locked worker=%d: stop 后 next 仍返回 (got=%d)", w.id, got)
	}
	stop()
	if c.stopped != 1 {
		failf("phase=x06.locked worker=%d: gen stopped=%d want=1 (stop 未传播)", w.id, c.stopped)
	}
	if c.completed != 0 {
		failf("phase=x06.locked worker=%d: gen completed=%d want=0", w.id, c.completed)
	}
	runtime.UnlockOSThread()
	atomic.AddInt64(&genStopped, 1)
	atomic.AddInt64(&stopEarly, 1)
}

func (w *worker) round(r int64) {
	base := w.base + r*roundStride
	w.phaseGCWindow(base + gcBaseOff)
	w.phaseFast(base + fastBaseOff)
	w.phaseNested(base + outerBase)
	w.phasePanic(base + panicBase)
	w.phaseLocked(base + lockBase)
	w.rounds++
	atomic.AddInt64(&roundsTotal, 1)
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
	workers := envInt("STRESS_WORKERS", 4, 1, 16)
	hb := envInt("STRESS_HEARTBEAT", 5, 1, 60)
	seed := envSeed(1)
	fixed := int64(envInt("STRESS_FIXED_ROUNDS", 8, 1, 1000000))
	gSeed = seed
	debug.SetMaxStack(64 << 20)

	fmt.Printf("scenario=%s phase=preflight go=%s goos=%s goarch=%s gomaxprocs=%d seed=%d seconds=%d workers=%d fixed_rounds=%d heartbeat=%ds gc_pulls=%d fast_pulls=%d nested=%dx%d locked_pulls=%d\n",
		scenario, runtime.Version(), runtime.GOOS, runtime.GOARCH, runtime.GOMAXPROCS(0),
		seed, seconds, workers, fixed, hb, gcPulls, fastPulls, nestedOuter, nestedInner, lockedPulls)

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
		}
	}

	var wg sync.WaitGroup
	for _, w := range ws {
		wg.Add(1)
		go func(w *worker) {
			atomic.AddInt64(&workerStarted, 1)
			defer func() {
				atomic.AddInt64(&workerFinished, 1)
				wg.Done()
			}()
			w.run(seconds, deadline, fixed)
		}(w)
	}
	wg.Wait()
	close(stop)
	aux.Wait()

	var rounds int64
	for _, w := range ws {
		rounds += w.rounds
		if seconds == 0 && w.rounds != fixed {
			failf("phase=final worker=%d: rounds=%d want=%d", w.id, w.rounds, fixed)
		}
	}
	if rounds == 0 {
		failf("phase=final: 没有完成任何轮次")
	}
	if s, f := atomic.LoadInt64(&workerStarted), atomic.LoadInt64(&workerFinished); s != int64(workers) || f != s {
		failf("phase=final: workers started=%d finished=%d want=%d", s, f, workers)
	}
	if want := rounds * 3; atomic.LoadInt64(&genCompleted) != want {
		failf("phase=final: generator completed=%d want=%d", atomic.LoadInt64(&genCompleted), want)
	}
	if want := rounds; atomic.LoadInt64(&genStopped) != want {
		failf("phase=final: generator stopped=%d want=%d", atomic.LoadInt64(&genStopped), want)
	}
	if want := rounds * gcPulls; atomic.LoadInt64(&gcPullTotal) != want {
		failf("phase=final: gc 段 pulls=%d want=%d", atomic.LoadInt64(&gcPullTotal), want)
	}
	if want := rounds * fastPulls; atomic.LoadInt64(&fastPullTotal) != want {
		failf("phase=final: 高速段 pulls=%d want=%d", atomic.LoadInt64(&fastPullTotal), want)
	}
	if want := rounds * nestedOuter; atomic.LoadInt64(&nestedPullTot) != want {
		failf("phase=final: 嵌套段 pulls=%d want=%d", atomic.LoadInt64(&nestedPullTot), want)
	}
	if atomic.LoadInt64(&panicPropagated) != rounds {
		failf("phase=final: panic 传播=%d want=%d", atomic.LoadInt64(&panicPropagated), rounds)
	}
	if atomic.LoadInt64(&stopEarly) != rounds {
		failf("phase=final: 提前 stop=%d want=%d", atomic.LoadInt64(&stopEarly), rounds)
	}
	if want := rounds * gcPulls; atomic.LoadInt64(&weakChecks) != want {
		failf("phase=final: weak 探针=%d want=%d", atomic.LoadInt64(&weakChecks), want)
	}
	if atomic.LoadInt64(&canaryChecks) == 0 {
		failf("phase=final: 没有执行 canary 校验")
	}

	ng := waitBaseline(baseGoroutines, 6, 5*time.Second)
	if ng > baseGoroutines+4 {
		failf("phase=final: goroutines baseline=%d after=%d: goroutine 泄漏 (coro 未退出)", baseGoroutines, ng)
	}

	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	fmt.Printf("metrics: rounds=%d pulls_gc=%d pulls_fast=%d pulls_nested=%d gen_completed=%d gen_stopped=%d panic_propagated=%d stop_early=%d canary_checks=%d weak_probes=%d gc_churn=%d num_gc=%d heap_alloc=%d heap_objects=%d goroutines=%d\n",
		rounds, atomic.LoadInt64(&gcPullTotal), atomic.LoadInt64(&fastPullTotal),
		atomic.LoadInt64(&nestedPullTot), atomic.LoadInt64(&genCompleted), atomic.LoadInt64(&genStopped),
		atomic.LoadInt64(&panicPropagated), atomic.LoadInt64(&stopEarly),
		atomic.LoadInt64(&canaryChecks), atomic.LoadInt64(&weakChecks), atomic.LoadInt64(&gcCycles),
		m.NumGC, m.HeapAlloc, m.HeapObjects, ng)
	fmt.Printf("STRESS PASS %s\n", scenario)
}
