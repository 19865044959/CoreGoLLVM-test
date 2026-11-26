// X10: inline/musttail 尾调用 pcdata 边界（P0）。
// 目标：把 GC 注入点精确放到“函数末尾最后一条 call”与 deferreturn 边界上，
// 让 inline 树/musttail wrapper/method expression/interface 派发的 stackmap 区间现形。
//
// 形态：
//   - 小 wrapper 链 a→b→c→work：每层分配并持有一个局部指针，调用下一层后仍使用该指针；
//     inline 版不加指令（体量控制在可内联预算内），noinline 版逐层 //go:noinline 对照；
//   - work 首行 runtime.GC()（tail）：GC 恰在最后一条 call 之后、被调函数 stackmap 区间起点；
//   - work 中部 runtime.GC()（mid）：GC 后仍有 call 与指针使用，覆盖非尾部 call 的 pcdata；
//   - method expression / method value（-fm thunk，可能 musttail）/ interface 动态派发；
//   - deferreturn 边界：defer 闭包在 return 之后执行，闭包内 GC 后再使用捕获指针；
//   - 固定轮数下穷举 [inline|noinline] × [tail|mid] × [method|iface] × deferreturn。
//
// 所有指针带确定性 canary（nodeV），任何 GC 后读取失配立即 FAIL。
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

const scenario = "x10_inline_tailcall_pcdata"

const (
	idP = 11
	idQ = 12
	idA = 9101
	idB = 9102
	idC = 9103
)

const numCases = 10

var caseNames = [numCases]string{
	"chain_tail_inline",
	"chain_mid_inline",
	"chain_tail_noinline",
	"chain_mid_noinline",
	"method_expr_tail",
	"method_value_tail",
	"iface_tail",
	"iface_mid",
	"deferreturn",
	"deferreturn_work",
}

type node struct {
	id   int64
	v    int64
	next *node
	pad  [2]uint64
}

var (
	gSeed       uint64
	beats       int64
	started     int64
	finished    int64
	gcInject    int64
	canaryCheck int64
)

func nodeV(seed uint64, id int64) int64 {
	return int64(seed*0x9E3779B97F4A7C15^uint64(id)*0xBF58476D1CE4E5B9) | 1
}

func failf(format string, args ...interface{}) {
	fmt.Printf("FAIL: seed=%d gcInjections=%d "+format+"\n",
		append([]interface{}{gSeed, atomic.LoadInt64(&gcInject)}, args...)...)
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

// check 校验对象身份与 canary。//go:noinline 保证读取不被优化掉。
//
//go:noinline
func check(phase string, n *node, seed uint64, wantID int64) {
	atomic.AddInt64(&canaryCheck, 1)
	if n == nil {
		failf("phase=%s: nil *node (UAF)", phase)
	}
	if n.id != wantID {
		failf("phase=%s: id=%d want=%d (指针被改写)", phase, n.id, wantID)
	}
	if n.v != nodeV(seed, wantID) {
		failf("phase=%s id=%d: canary=%#x want=%#x (UAF/漏标)", phase, n.id, n.v, nodeV(seed, wantID))
	}
}

// checkSelf 自校验（id 与 v 自洽），用于 work 内不知道期望 id 的场景。
//
//go:noinline
func checkSelf(phase string, n *node, seed uint64) {
	atomic.AddInt64(&canaryCheck, 1)
	if n == nil {
		failf("phase=%s: nil *node (UAF)", phase)
	}
	if n.v != nodeV(seed, n.id) {
		failf("phase=%s id=%d: canary=%#x want=%#x (UAF/漏标)", phase, n.id, n.v, nodeV(seed, n.id))
	}
}

// injectGC 是全部 GC 注入的唯一入口：先计数再 GC，失败行可回放 gcCount。
//
//go:noinline
func injectGC(phase string) {
	atomic.AddInt64(&gcInject, 1)
	runtime.GC()
	runtime.KeepAlive(phase)
}

// workTail 的 GC 位于函数首行——调用者最后一条 call 刚返回的位置；
// 参数 p/a/b/c 在 GC 后仍被使用，调用者的局部指针则必须在调用点 stackmap 中存活。
//
//go:noinline
func workTail(seed uint64, p, a, b, c *node) int64 {
	injectGC("x10.work_tail")
	check("x10.work_tail.p", p, seed, idP)
	check("x10.work_tail.a", a, seed, idA)
	check("x10.work_tail.b", b, seed, idB)
	check("x10.work_tail.c", c, seed, idC)
	runtime.KeepAlive(p)
	runtime.KeepAlive(a)
	runtime.KeepAlive(b)
	runtime.KeepAlive(c)
	return p.v ^ a.v ^ b.v ^ c.v
}

// touchAfterGC 在 GC 之后制造新的 call 边界并再次读取指针。
//
//go:noinline
func touchAfterGC(seed uint64, x, y *node) int64 {
	checkSelf("x10.touch.x", x, seed)
	checkSelf("x10.touch.y", y, seed)
	runtime.KeepAlive(x)
	runtime.KeepAlive(y)
	return x.v ^ y.v
}

// workMid 的 GC 位于函数中部：GC 前后都有指针使用，GC 后还有一条 noinline call。
//
//go:noinline
func workMid(seed uint64, p, a, b, c *node) int64 {
	pre := p.v ^ a.v
	injectGC("x10.work_mid")
	post := touchAfterGC(seed, b, c)
	check("x10.work_mid.p", p, seed, idP)
	check("x10.work_mid.a", a, seed, idA)
	runtime.KeepAlive(p)
	runtime.KeepAlive(a)
	return pre ^ post
}

// ---------- inline 版 wrapper 链 a→b→c→work ----------
// 体量刻意压到内联预算内；noinline 对照在下方。

func chainTailInlineA(seed uint64, p *node, va, vb, vc int64) (a, b, c *node, r int64) {
	a = &node{id: idA, v: va}
	b, c, r = chainTailInlineB(seed, p, a, vb, vc)
	return
}

func chainTailInlineB(seed uint64, p, a *node, vb, vc int64) (b, c *node, r int64) {
	b = &node{id: idB, v: vb}
	c, r = chainTailInlineC(seed, p, a, b, vc)
	return
}

func chainTailInlineC(seed uint64, p, a, b *node, vc int64) (c *node, r int64) {
	c = &node{id: idC, v: vc}
	r = workTail(seed, p, a, b, c)
	return
}

func chainMidInlineA(seed uint64, p *node, va, vb, vc int64) (a, b, c *node, r int64) {
	a = &node{id: idA, v: va}
	b, c, r = chainMidInlineB(seed, p, a, vb, vc)
	return
}

func chainMidInlineB(seed uint64, p, a *node, vb, vc int64) (b, c *node, r int64) {
	b = &node{id: idB, v: vb}
	c, r = chainMidInlineC(seed, p, a, b, vc)
	return
}

func chainMidInlineC(seed uint64, p, a, b *node, vc int64) (c *node, r int64) {
	c = &node{id: idC, v: vc}
	r = workMid(seed, p, a, b, c)
	return
}

// ---------- noinline 对照 ----------

//go:noinline
func chainTailNoinlineA(seed uint64, p *node, va, vb, vc int64) (a, b, c *node, r int64) {
	a = &node{id: idA, v: va}
	b, c, r = chainTailNoinlineB(seed, p, a, vb, vc)
	return
}

//go:noinline
func chainTailNoinlineB(seed uint64, p, a *node, vb, vc int64) (b, c *node, r int64) {
	b = &node{id: idB, v: vb}
	c, r = chainTailNoinlineC(seed, p, a, b, vc)
	return
}

//go:noinline
func chainTailNoinlineC(seed uint64, p, a, b *node, vc int64) (c *node, r int64) {
	c = &node{id: idC, v: vc}
	r = workTail(seed, p, a, b, c)
	return
}

//go:noinline
func chainMidNoinlineA(seed uint64, p *node, va, vb, vc int64) (a, b, c *node, r int64) {
	a = &node{id: idA, v: va}
	b, c, r = chainMidNoinlineB(seed, p, a, vb, vc)
	return
}

//go:noinline
func chainMidNoinlineB(seed uint64, p, a *node, vb, vc int64) (b, c *node, r int64) {
	b = &node{id: idB, v: vb}
	c, r = chainMidNoinlineC(seed, p, a, b, vc)
	return
}

//go:noinline
func chainMidNoinlineC(seed uint64, p, a, b *node, vc int64) (c *node, r int64) {
	c = &node{id: idC, v: vc}
	r = workMid(seed, p, a, b, c)
	return
}

// ---------- method expression / method value / interface wrapper ----------

type svc struct {
	seed uint64
}

func (s *svc) callTail(p *node) int64 {
	a := &node{id: idA, v: nodeV(s.seed, idA)}
	b := &node{id: idB, v: nodeV(s.seed, idB)}
	c := &node{id: idC, v: nodeV(s.seed, idC)}
	r := workTail(s.seed, p, a, b, c)
	check("x10.method.a", a, s.seed, idA)
	check("x10.method.b", b, s.seed, idB)
	check("x10.method.c", c, s.seed, idC)
	return r
}

func (s *svc) callMid(p *node) int64 {
	a := &node{id: idA, v: nodeV(s.seed, idA)}
	b := &node{id: idB, v: nodeV(s.seed, idB)}
	c := &node{id: idC, v: nodeV(s.seed, idC)}
	r := workMid(s.seed, p, a, b, c)
	check("x10.method_mid.a", a, s.seed, idA)
	check("x10.method_mid.b", b, s.seed, idB)
	check("x10.method_mid.c", c, s.seed, idC)
	return r
}

type caller interface {
	call(p *node) int64
}

type implA struct {
	seed uint64
}

type implB struct {
	seed uint64
}

func (x *implA) call(p *node) int64 {
	a := &node{id: idA, v: nodeV(x.seed, idA)}
	b := &node{id: idB, v: nodeV(x.seed, idB)}
	c := &node{id: idC, v: nodeV(x.seed, idC)}
	r := workTail(x.seed, p, a, b, c)
	check("x10.ifaceA.a", a, x.seed, idA)
	check("x10.ifaceA.b", b, x.seed, idB)
	check("x10.ifaceA.c", c, x.seed, idC)
	return r
}

func (x *implB) call(p *node) int64 {
	a := &node{id: idA, v: nodeV(x.seed, idA)}
	b := &node{id: idB, v: nodeV(x.seed, idB)}
	c := &node{id: idC, v: nodeV(x.seed, idC)}
	r := workTail(x.seed, p, a, b, c)
	check("x10.ifaceB.a", a, x.seed, idA)
	check("x10.ifaceB.b", b, x.seed, idB)
	check("x10.ifaceB.c", c, x.seed, idC)
	return r
}

type callerMid interface {
	callMid(p *node) int64
}

type implMidA struct {
	seed uint64
}

type implMidB struct {
	seed uint64
}

func (x *implMidA) callMid(p *node) int64 {
	a := &node{id: idA, v: nodeV(x.seed, idA)}
	b := &node{id: idB, v: nodeV(x.seed, idB)}
	c := &node{id: idC, v: nodeV(x.seed, idC)}
	r := workMid(x.seed, p, a, b, c)
	check("x10.ifaceMidA.a", a, x.seed, idA)
	check("x10.ifaceMidA.b", b, x.seed, idB)
	check("x10.ifaceMidA.c", c, x.seed, idC)
	return r
}

func (x *implMidB) callMid(p *node) int64 {
	a := &node{id: idA, v: nodeV(x.seed, idA)}
	b := &node{id: idB, v: nodeV(x.seed, idB)}
	c := &node{id: idC, v: nodeV(x.seed, idC)}
	r := workMid(x.seed, p, a, b, c)
	check("x10.ifaceMidB.a", a, x.seed, idA)
	check("x10.ifaceMidB.b", b, x.seed, idB)
	check("x10.ifaceMidB.c", c, x.seed, idC)
	return r
}

// pick / pickMid 用 noinline + 动态种子选择实现，阻止编译器去虚化。
//
//go:noinline
func pick(sel, seed uint64) caller {
	if sel&1 == 0 {
		return &implA{seed: seed}
	}
	return &implB{seed: seed}
}

//go:noinline
func pickMid(sel, seed uint64) callerMid {
	if sel&1 == 0 {
		return &implMidA{seed: seed}
	}
	return &implMidB{seed: seed}
}

// ---------- deferreturn 边界 ----------

// deferBoundary 在 deferreturn 处执行 GC，再读取/替换捕获的命名返回值。
//
//go:noinline
func deferBoundary(seed uint64, p, q *node) (r *node) {
	r = p
	defer func() {
		injectGC("x10.deferreturn")
		check("x10.deferreturn.r", r, seed, idP)
		check("x10.deferreturn.q", q, seed, idQ)
		r = q
	}()
	return r
}

// deferCallsWork 在 deferreturn 的闭包里调用 workMid（GC 位于 deferreturn 链上的调用内）。
//
//go:noinline
func deferCallsWork(seed uint64, p *node) (r int64) {
	a := &node{id: idA, v: nodeV(seed, idA)}
	b := &node{id: idB, v: nodeV(seed, idB)}
	c := &node{id: idC, v: nodeV(seed, idC)}
	defer func() {
		r = workMid(seed, p, a, b, c)
		check("x10.deferwork.a", a, seed, idA)
		check("x10.deferwork.b", b, seed, idB)
		check("x10.deferwork.c", c, seed, idC)
	}()
	return 0
}

// ---------- worker / 用例 ----------

type worker struct {
	id        int64
	seed      uint64
	rounds    int64
	next      int64
	caseCount [numCases]int64
}

func (w *worker) verifyChain(phase string, p, a, b, c *node, r int64) {
	check(phase+".p", p, w.seed, idP)
	check(phase+".a", a, w.seed, idA)
	check(phase+".b", b, w.seed, idB)
	check(phase+".c", c, w.seed, idC)
	if want := p.v ^ a.v ^ b.v ^ c.v; r != want {
		failf("phase=%s: 返回值=%#x want=%#x (inline/musttail 数据流损坏)", phase, r, want)
	}
	runtime.KeepAlive(p)
	runtime.KeepAlive(a)
	runtime.KeepAlive(b)
	runtime.KeepAlive(c)
}

func (w *worker) runCase(c int, r int64) {
	seed := w.seed
	p := &node{id: idP, v: nodeV(seed, idP)}

	switch c {
	case 0:
		a, b, cc, got := chainTailInlineA(seed, p, nodeV(seed, idA), nodeV(seed, idB), nodeV(seed, idC))
		w.verifyChain("x10.chain_tail_inline", p, a, b, cc, got)
	case 1:
		a, b, cc, got := chainMidInlineA(seed, p, nodeV(seed, idA), nodeV(seed, idB), nodeV(seed, idC))
		w.verifyChain("x10.chain_mid_inline", p, a, b, cc, got)
	case 2:
		a, b, cc, got := chainTailNoinlineA(seed, p, nodeV(seed, idA), nodeV(seed, idB), nodeV(seed, idC))
		w.verifyChain("x10.chain_tail_noinline", p, a, b, cc, got)
	case 3:
		a, b, cc, got := chainMidNoinlineA(seed, p, nodeV(seed, idA), nodeV(seed, idB), nodeV(seed, idC))
		w.verifyChain("x10.chain_mid_noinline", p, a, b, cc, got)
	case 4:
		s := &svc{seed: seed}
		f := (*svc).callTail
		got := f(s, p)
		w.verifyMethod("x10.method_expr_tail", p, got, idA, idB, idC)
	case 5:
		s := &svc{seed: seed}
		f := s.callTail
		got := f(p)
		w.verifyMethod("x10.method_value_tail", p, got, idA, idB, idC)
	case 6:
		it := pick(seed^uint64(r), seed)
		got := it.call(p)
		w.verifyMethod("x10.iface_tail", p, got, idA, idB, idC)
	case 7:
		it := pickMid(seed^uint64(r), seed)
		got := it.callMid(p)
		w.verifyMethod("x10.iface_mid", p, got, idA, idB, idC)
	case 8:
		q := &node{id: idQ, v: nodeV(seed, idQ)}
		got := deferBoundary(seed, p, q)
		check("x10.deferreturn.ret", got, seed, idQ)
		if got.v != q.v {
			failf("phase=x10.deferreturn: 返回值指向错误对象")
		}
		runtime.KeepAlive(p)
		runtime.KeepAlive(q)
	case 9:
		got := deferCallsWork(seed, p)
		w.verifyMethod("x10.deferreturn_work", p, got, idA, idB, idC)
	default:
		failf("phase=preflight: case=%d 越界", c)
	}
	w.caseCount[c]++
	atomic.AddInt64(&beats, 1)
}

func (w *worker) verifyMethod(phase string, p *node, r int64, ids ...int64) {
	check(phase+".p", p, w.seed, idP)
	want := p.v
	for _, id := range ids {
		want ^= nodeV(w.seed, id)
	}
	if r != want {
		failf("phase=%s: 返回值=%#x want=%#x", phase, r, want)
	}
}

func (w *worker) round(r int64) {
	for c := 0; c < numCases; c++ {
		w.runCase(c, r)
	}
	w.rounds++
	w.next = r + 1
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

	fmt.Printf("scenario=%s phase=preflight go=%s goos=%s goarch=%s gomaxprocs=%d seed=%d seconds=%d workers=%d fixed_rounds=%d heartbeat=%ds cases=%d\n",
		scenario, runtime.Version(), runtime.GOOS, runtime.GOARCH, runtime.GOMAXPROCS(0),
		seed, seconds, workers, fixed, hb, numCases)

	baseGoroutines := runtime.NumGoroutine()
	deadline := time.Now().Add(time.Duration(seconds) * time.Second)
	ws := make([]*worker, workers)
	for i := range ws {
		ws[i] = &worker{
			id:   int64(i),
			seed: seed*1_000_003 + uint64(i+1)*7919,
		}
	}

	stop := make(chan struct{})
	var aux sync.WaitGroup
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

	var roundsTotal, checksTotal, gcTotal int64
	for _, w := range ws {
		roundsTotal += w.rounds
		for c := 0; c < numCases; c++ {
			if seconds == 0 && w.caseCount[c] != fixed {
				failf("phase=final worker=%d case=%s: runs=%d want=%d", w.id, caseNames[c], w.caseCount[c], fixed)
			}
			if w.caseCount[c] == 0 {
				failf("phase=final worker=%d case=%s: 未执行", w.id, caseNames[c])
			}
		}
		if seconds == 0 && w.next != fixed {
			failf("phase=final worker=%d: rounds=%d want=%d", w.id, w.next, fixed)
		}
	}
	if roundsTotal == 0 {
		failf("phase=final: no rounds completed")
	}
	checksTotal = atomic.LoadInt64(&canaryCheck)
	gcTotal = atomic.LoadInt64(&gcInject)
	// 每个 case 至少注入一次 GC（case 4/5/6/8 通过 workTail/deferreturn 注入）。
	if want := roundsTotal * numCases; gcTotal < want {
		failf("phase=final: gcInjections=%d want>=%d (穷举未完成)", gcTotal, want)
	}

	ng := waitGoroutines(baseGoroutines, 5*time.Second)
	if ng > baseGoroutines+4 {
		failf("phase=final: goroutines baseline=%d after=%d: goroutine leak", baseGoroutines, ng)
	}

	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	fmt.Printf("metrics: rounds=%d cases_per_round=%d canary_checks=%d gc_injections=%d num_gc=%d heap_alloc=%d heap_objects=%d goroutines=%d\n",
		roundsTotal, numCases, checksTotal, gcTotal, m.NumGC, m.HeapAlloc, m.HeapObjects, ng)
	fmt.Printf("case_counts:")
	for c := 0; c < numCases; c++ {
		var n int64
		for _, w := range ws {
			n += w.caseCount[c]
		}
		fmt.Printf(" %s=%d", caseNames[c], n)
	}
	fmt.Printf("\n")
	fmt.Printf("STRESS PASS %s\n", scenario)
}
