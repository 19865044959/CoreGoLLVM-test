// Z01: KeepAlive 作为被测目标（P0，covers L18/L17/L05）。
// 历史场景都把 runtime.KeepAlive 当工具；本场景反过来验证 KeepAlive/liveness 本身
// （go#30476 可重物化栈地址、go#22458 spill 槽、go#16249 结果参数 liveness 类）。
//
// 形态 A：栈上 box（含 *node 字段，轻到编译器可重物化 &b）→ 只经 b 访问 p →
//
//	runtime.GC() → runtime.KeepAlive(&b) → 读 b.p 并校验 canary。
//
// 形态 B：p.child 的最后一次直接使用在 callGC 前；GC 后只经从 p 指向对象取出的
//
//	派生指针 q（child.back 指回 p）使用，p/q 均须存活——测试“派生值跨 GC
//	仍保活其引用链”的 liveness 不变量。
//	注：Go 规范允许“最后一次使用为纯值读取”的对象在随后 GC 中被回收，
//	因此这里用 back-pointer 链（q→child→p）构造一个必然保活的合法程序，
//	对纯 int 派生值断言“p 不得终结”会在官方 Go 上误报（已本地实测）。
//
// 形态 C：[8]box 数组，每字段指向不同堆对象，KeepAlive(&arr) 覆盖全部；多次 GC
//
//	后逐字段校验 + 逐对象 finalizer/weak 断言。
//
// 形态 D：defer runtime.KeepAlive(&b)（返回时才执行，是函数最后执行的语句），
//
//	中间夹多轮 GC；字段只在 GC 前读取，GC 后只经 finalizer/weak 探针验证。
//
// 每个堆对象：finalizer（在“仍被引用”期间触发 = 提前终结 = FAIL）+ weak.Make
// （持有期 Value()==nil = 对象被提前回收 = FAIL）。finalizer 只做原子计数 +
// 非阻塞 channel 通知，检查点同步非阻塞读，不阻塞运行时终结器 goroutine。
// 每轮以 3+round%64 的递归帧抖动改变 GC 时的栈布局；window profile（GOGC=off，
// 显式 GC 驱动回收）必跑。
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
	scenario = "z01_keepalive_liveness"
	nodeTag  = int64(0x7A30315F)
)

// node 的 v 是确定性 canary：任何读取都必须等于 nodeV(seed,id)。
type node struct {
	id    int64
	v     int64
	tag   int64
	child *node
	back  *node
	pad   [8]int64
}

// box 保持“轻”：只有 p 与 x，使 &b 成为编译器可重物化的栈地址。
type box struct {
	p *node
	x int64
}

type probeRec struct {
	id        int64
	released  int32
	premature int32
	finals    int32
	done      chan struct{} // cap 1；finalizer 非阻塞投递
	wk        weak.Pointer[node]
}

var (
	gSeed     uint64
	beats     int64
	gcCycles  int64
	started   int64
	finished  int64
	probes    int64
	weakCheck int64
	finalized int64
)

func nodeV(seed uint64, id int64) int64 {
	return int64(seed*0x9E3779B97F4A7C15^uint64(id)*0xBF58476D1CE4E5B9) | 1
}

func failf(format string, args ...interface{}) {
	fmt.Printf("FAIL: seed=%d gcCycles=%d "+format+"\n",
		append([]interface{}{gSeed, atomic.LoadInt64(&gcCycles)}, args...)...)
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

func newNode(seed uint64, id int64) *node {
	return &node{id: id, v: nodeV(seed, id), tag: nodeTag}
}

// trackNode 注册 finalizer + weak 探针。finalizer 必须短且不阻塞。
func trackNode(p *node) *probeRec {
	pr := &probeRec{id: p.id, done: make(chan struct{}, 1), wk: weak.Make(p)}
	runtime.SetFinalizer(p, func(*node) {
		if atomic.LoadInt32(&pr.released) == 0 {
			atomic.AddInt32(&pr.premature, 1)
		}
		atomic.AddInt32(&pr.finals, 1)
		atomic.AddInt64(&finalized, 1)
		select {
		case pr.done <- struct{}{}:
		default:
		}
	})
	atomic.AddInt64(&probes, 1)
	return pr
}

func releaseProbe(pr *probeRec) { atomic.StoreInt32(&pr.released, 1) }

// verifyProbe 是“仍被合法引用”阶段的同步检查点：finalizer 计数/通知非阻塞读 +
// weak.Value() 直接判据（weak 在对象不可达时、finalizer 执行前即转 nil）。
//
//go:noinline
func verifyProbe(pr *probeRec, form string, round int64) {
	atomic.AddInt64(&weakCheck, 1)
	if n := atomic.LoadInt32(&pr.premature); n != 0 {
		failf("phase=%s/premature form=%s round=%d id=%d: finalizer ran while object still legally referenced (early collection)", form, form, round, pr.id)
	}
	select {
	case <-pr.done:
		failf("phase=%s/notify form=%s round=%d id=%d: finalizer notification observed while object still legally referenced", form, round, pr.id)
	default:
	}
	if pr.wk.Value() == nil {
		failf("phase=%s/weak form=%s round=%d id=%d: weak.Make(...).Value()==nil while object still legally referenced (premature reclamation)", form, round, pr.id)
	}
}

// formA：栈上 box；只经 b 访问 p；GC 后 KeepAlive(&b)，再读 b.p。
//
//go:noinline
func formA(seed uint64, round int64) {
	id := int64(0xA0000) + round%256
	var b box
	b.p = newNode(seed, id)
	b.x = round
	pr := trackNode(b.p)
	if b.p.id != id || b.p.v != nodeV(seed, id) || b.p.tag != nodeTag {
		failf("phase=A/pre form=A round=%d id=%d: content corrupted before GC", round, id)
	}
	runtime.GC()
	atomic.AddInt64(&gcCycles, 1)
	runtime.KeepAlive(&b)
	verifyProbe(pr, "A", round)
	if b.p.id != id || b.p.v != nodeV(seed, id) || b.p.tag != nodeTag {
		failf("phase=A/post form=A round=%d id=%d: content corrupted after GC (got id=%d v=%#x tag=%#x)",
			round, id, b.p.id, b.p.v, b.p.tag)
	}
	releaseProbe(pr)
}

// formB：派生指针 q 跨 GC 保活 q→child→p 链；p 的最后直接使用在 GC 前。
//
//go:noinline
func formB(seed uint64, round int64) {
	pid := int64(0xB0000) + round%256
	cid := pid + 0x1000
	p := newNode(seed, pid)
	q := newNode(seed, cid)
	ppr := trackNode(p)
	qpr := trackNode(q)
	p.child = q
	q.back = p
	qq := p.child // p 的最后一次直接使用
	runtime.GC()
	atomic.AddInt64(&gcCycles, 1)
	verifyProbe(ppr, "B.parent", round)
	verifyProbe(qpr, "B.child", round)
	if qq == nil || qq.id != cid || qq.v != nodeV(seed, cid) || qq.tag != nodeTag {
		failf("phase=B/child form=B round=%d id=%d: derived child corrupted after GC", round, cid)
	}
	bk := qq.back
	if bk == nil || bk.id != pid || bk.v != nodeV(seed, pid) || bk.tag != nodeTag {
		failf("phase=B/back form=B round=%d id=%d: back-pointer chain p lost after GC", round, pid)
	}
	releaseProbe(ppr)
	releaseProbe(qpr)
}

// formC：[8]box 数组，KeepAlive(&arr) 一次覆盖全部字段；多轮 GC 后逐字段校验。
//
//go:noinline
func formC(seed uint64, round int64) {
	var arr [8]box
	var ids [8]int64
	var prs [8]*probeRec
	for i := 0; i < 8; i++ {
		ids[i] = int64(0xC0000+i*0x100) + round%128
		arr[i].p = newNode(seed, ids[i])
		arr[i].x = int64(i)
		prs[i] = trackNode(arr[i].p)
	}
	for i := 0; i < 8; i++ {
		if arr[i].p == nil || arr[i].p.id != ids[i] || arr[i].p.v != nodeV(seed, ids[i]) {
			failf("phase=C/pre form=C round=%d slot=%d id=%d: content corrupted before GC", round, i, ids[i])
		}
	}
	runtime.GC()
	runtime.GC()
	atomic.AddInt64(&gcCycles, 2)
	runtime.KeepAlive(&arr)
	for i := 0; i < 8; i++ {
		verifyProbe(prs[i], "C", round)
		if arr[i].p == nil || arr[i].p.id != ids[i] || arr[i].p.v != nodeV(seed, ids[i]) || arr[i].p.tag != nodeTag {
			failf("phase=C/post form=C round=%d slot=%d id=%d: content corrupted after GC", round, i, ids[i])
		}
		if arr[i].x != int64(i) {
			failf("phase=C/post form=C round=%d slot=%d: box field x=%d corrupted", round, i, arr[i].x)
		}
	}
	for i := 0; i < 8; i++ {
		releaseProbe(prs[i])
	}
}

// formD：defer runtime.KeepAlive(&b)（函数返回时才执行），中间夹 3 轮 GC；
// 字段只在 GC 前读取，GC 后只经 finalizer/weak 探针验证。
//
//go:noinline
func formD(seed uint64, round int64) {
	id := int64(0xD0000) + round%256
	var b box
	b.p = newNode(seed, id)
	b.x = round
	pr := trackNode(b.p)
	if b.p.id != id || b.p.v != nodeV(seed, id) || b.p.tag != nodeTag {
		failf("phase=D/pre form=D round=%d id=%d: content corrupted before GC", round, id)
	}
	defer runtime.KeepAlive(&b) // 最后执行的语句：GC 循环结束后才在 return 时执行
	for i := 0; i < 3; i++ {
		runtime.GC()
		atomic.AddInt64(&gcCycles, 1)
		verifyProbe(pr, "D", round)
	}
	releaseProbe(pr)
}

// jitter 用递归帧抖动改变 GC 时的栈布局，并在每帧保留一个活指针槽，顺带压栈扫描。
//
//go:noinline
func jitter(depth int, anchor *node, seed uint64, round int64) {
	var slots [16]*node
	slots[depth&15] = anchor
	if depth > 0 {
		jitter(depth-1, anchor, seed, round)
	} else {
		formA(seed, round)
		formB(seed, round)
		formC(seed, round)
		formD(seed, round)
	}
	if slots[depth&15] != anchor {
		failf("phase=jitter round=%d depth=%d: live stack slot corrupted", round, depth)
	}
	slots[depth&15] = nil
}

type worker struct {
	id     int64
	seed   uint64
	anchor *node
	rounds int64
}

func roundSeed(seed uint64, r int64) uint64 {
	s := seed*1_000_003 ^ uint64(r)*0x9E3779B97F4A7C15
	if s == 0 {
		s = 1
	}
	return s
}

func (w *worker) round(r int64) {
	s := roundSeed(w.seed, r)
	jitter(3+int(r%64), w.anchor, s, r)
	w.rounds++
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
	runtime.GC()
}

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
			sink = append(sink[:0], b)
		}
		for i := range sink {
			sink[i] = nil
		}
		sink = sink[:0]
		runtime.GC()
		atomic.AddInt64(&gcCycles, 1)
		// window（GOMAXPROCS=1 + STW）下无休眠 GC 循环会饿死 worker/watchdog。
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
				failf("phase=watchdog: 无 worker 进展 %s (beats=%d)", time.Since(lastChange), cur)
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

// makeDroppedProbe 构造“立即失活”的探针：对象只由 weak/finalizer 关联。
//
//go:noinline
func makeDroppedProbe(seed uint64, id int64) *probeRec {
	p := newNode(seed, id)
	pr := trackNode(p)
	releaseProbe(pr)
	runtime.KeepAlive(p)
	return pr
}

// finalizerSanity 证明终结器机制在本进程内实际运行（正向健全性检查，
// 不是时延断言；15s 只是失败上限）。负向断言已由每轮的 verifyProbe 覆盖。
func finalizerSanity(seed uint64) {
	const n = 4
	prs := make([]*probeRec, n)
	for i := range prs {
		prs[i] = makeDroppedProbe(seed, int64(0xF0000+i))
	}
	deadline := time.Now().Add(15 * time.Second)
	for {
		done := 0
		for _, pr := range prs {
			select {
			case <-pr.done:
				done++
			default:
			}
		}
		if done == n {
			return
		}
		if time.Now().After(deadline) {
			failf("phase=final: 已失活 probe 的 finalizer 未在 15s 内执行（终结器机制失效/饥饿）")
		}
		runtime.GC()
		atomic.AddInt64(&gcCycles, 1)
		runtime.Gosched()
		time.Sleep(time.Millisecond)
	}
}

func main() {
	setupCrashOutput()
	seconds := envInt("STRESS_SECONDS", 60, 0, 86400)
	workers := envInt("STRESS_WORKERS", 2, 1, 8)
	hb := envInt("STRESS_HEARTBEAT", 5, 1, 60)
	seed := envSeed(1)
	fixed := int64(envInt("STRESS_FIXED_ROUNDS", 8, 1, 1000000))
	gSeed = seed

	fmt.Printf("scenario=%s phase=preflight go=%s goos=%s goarch=%s gomaxprocs=%d seed=%d seconds=%d workers=%d fixed_rounds=%d heartbeat=%ds forms=A,B,C,D\n",
		scenario, runtime.Version(), runtime.GOOS, runtime.GOARCH, runtime.GOMAXPROCS(0),
		seed, seconds, workers, fixed, hb)

	ws := make([]*worker, workers)
	for i := range ws {
		ws[i] = &worker{
			id:     int64(i),
			seed:   seed*1_000_003 + uint64(i+1)*7919,
			anchor: newNode(seed, -100-int64(i)),
		}
	}

	// 预热 finalizer/GC 内部 goroutine，再取 goroutine 基线。
	warm := makeDroppedProbe(seed, -999)
	runtime.KeepAlive(warm)
	for i := 0; i < 3; i++ {
		runtime.GC()
		time.Sleep(time.Millisecond)
	}
	baseGoroutines := runtime.NumGoroutine()

	deadline := time.Now().Add(time.Duration(seconds) * time.Second)
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
	var rounds int64
	for _, w := range ws {
		rounds += w.rounds
		if w.rounds == 0 {
			failf("phase=final worker=%d: no rounds completed", w.id)
		}
		if seconds == 0 && w.rounds != fixed {
			failf("phase=final worker=%d: rounds=%d want=%d", w.id, w.rounds, fixed)
		}
	}

	finalizerSanity(seed)

	ng := waitGoroutines(baseGoroutines, 5*time.Second)
	if ng > baseGoroutines+6 {
		failf("phase=final: goroutines baseline=%d after=%d: goroutine leak", baseGoroutines, ng)
	}

	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	fmt.Printf("metrics: rounds=%d forms=A,B,C,D probes=%d weak_checks=%d premature_finalizers=0 finalized=%d gc_cycles=%d num_gc=%d heap_alloc=%d heap_objects=%d goroutines=%d\n",
		rounds, atomic.LoadInt64(&probes), atomic.LoadInt64(&weakCheck), atomic.LoadInt64(&finalized),
		atomic.LoadInt64(&gcCycles), m.NumGC, m.HeapAlloc, m.HeapObjects, ng)
	fmt.Printf("STRESS PASS %s\n", scenario)
}
