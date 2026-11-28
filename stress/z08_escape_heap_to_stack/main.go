// Z08: 逃逸分析失败面（go#10353 / go#79592）。
//
// 每个 worker 每轮轮换 5 种“本该逃逸到堆”的形态；若编译器漏判逃逸，
// 堆（全局 chan/map/interface）就会持有指向栈帧的指针，栈帧回收后读取即 UAF：
//
//	a) 闭包捕获局部变量（标量按引用捕获）后逃逸到全局 chan + interface 槽；
//	b) var a [16]*node; s := a[:8]; s = append(s, ...)（cap 足够，就地写）
//	   把 s 存入全局 map；
//	c) 函数返回 interface{} 装箱指向局部 node 的指针；
//	d) 值接收者方法值（捕获 receiver 副本，副本内含 *node）存全局 chan；
//	e) 泛型 returnLocal[T](v T) *T，多类型实例化后存全局 chan。
//
// 探针：每个逃逸 node 注册 finalizer（仍被全局引用而终结 = FAIL）+ weak.Make
// （强持有期间 Value()!=nil）；node 内容是确定性 canary，任何读取不匹配立即 FAIL。
// 每轮在“逃逸存储后、读取前”插入 runtime.GC()（并做栈复用压力），独立 goroutine
// 提供 GC churn。
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
	scenario     = "z08_escape_heap_to_stack"
	escArrN      = 16 // 局部数组 [16]*node，s := a[:8] 后 append 8 个仍在 cap 内
	escHalf      = escArrN / 2
	maxWorkers   = 16
	stackChurnD  = 64
	chanSlotSize = 8
)

type node struct {
	id   int64
	v    int64
	tag  string
	next *node
	pad  [2]uint64
}

type recv struct {
	id  int64
	v   int64
	p   *node
	pad [2]uint64
}

type genVal struct {
	id  int64
	v   int64
	p   *node
	pad [2]int64
}

var (
	// 全局逃逸存储（按 worker 分槽，避免跨 worker 竞争）。
	gClosureCh []chan func() int64
	gIfaceCh   []chan interface{}
	gMethodCh  []chan func() int64
	gGenericCh []chan interface{}
	gSliceMap  []map[string][]*node
	gIfaceSlot []interface{}

	beats    int64
	gcCycles int64
	started  int64
	finished int64
	gSeed    uint64
)

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

func nodeMagic(seed uint64, id int64) int64 {
	return int64(seed*0x9E3779B97F4A7C15^uint64(id)*0xBF58476D1CE4E5B9) | 1
}

func mkNode(seed uint64, id int64, tag string) *node {
	return &node{id: id, v: nodeMagic(seed, id), tag: tag}
}

// ---------------------------------------------------------------- 逃逸形态

// mkClosure 返回的闭包按引用捕获局部 x 与 n；两个局部都必须随闭包逃逸到堆。
//
//go:noinline
func mkClosure(seed uint64, id int64) (func() int64, *node) {
	n := mkNode(seed, id, "esc-closure")
	x := int64(seed%977) + id*3
	cl := func() int64 {
		x++
		return n.v + x
	}
	return cl, n
}

// mkAppended 在局部数组上构造 slice 并就地 append（cap 足够，不重分配）；
// 返回的 slice 头引用局部数组 a 的底层存储。
//
//go:noinline
func mkAppended(seed uint64, base int64) []*node {
	var a [escArrN]*node
	for i := 0; i < escHalf; i++ {
		a[i] = mkNode(seed, base+int64(i), "esc-append")
	}
	s := a[:escHalf]
	for i := escHalf; i < escArrN; i++ {
		s = append(s, mkNode(seed, base+int64(i), "esc-append"))
	}
	if len(s) != escArrN || cap(s) != escArrN || &s[0] != &a[0] {
		failf("phase=append-build base=%d: append reallocated or len/cap changed (len=%d cap=%d)", base, len(s), cap(s))
	}
	return s
}

// mkIface 返回装箱了“局部 node 指针”的 interface{}。
//
//go:noinline
func mkIface(seed uint64, id int64) (interface{}, *node) {
	n := mkNode(seed, id, "esc-iface")
	return interface{}(n), n
}

//go:noinline
func (r recv) score() int64 { return r.v + r.p.v }

// mkMethod 返回值接收者 r 的方法值：方法值捕获 receiver 副本（含 *node）。
//
//go:noinline
func mkMethod(seed uint64, id int64) (func() int64, *node, int64) {
	n := mkNode(seed, id, "esc-method")
	r := recv{id: id, v: int64(seed%1_000_003) + id*11, p: n, pad: [2]uint64{uint64(id), seed}}
	m := r.score
	return m, n, r.v + n.v
}

// returnLocal 取参数地址返回：v 必须逃逸到堆，否则返回指向本栈帧的指针。
//
//go:noinline
func returnLocal[T any](v T) *T { return &v }

// stackChurn 制造栈复用压力：深递归 + 指针槽，覆盖刚返回的逃逸源帧。
//
//go:noinline
func stackChurn(d int, seed int64) int64 {
	var slot [4]*node
	slot[0] = &node{v: seed + int64(d), tag: "churn"}
	slot[3] = slot[0]
	var buf [8]uint64
	for i := range buf {
		buf[i] = uint64(seed) + uint64(d) + uint64(i)
	}
	if d == 0 {
		return slot[0].v + slot[3].v
	}
	r := stackChurn(d-1, seed)
	if slot[0] != slot[3] || slot[0].v != seed+int64(d) || buf[0] != uint64(seed)+uint64(d) {
		failf("phase=stack-churn depth=%d seed=%d: frame corrupted", d, seed)
	}
	return r + slot[0].v
}

// ---------------------------------------------------------------- worker

type worker struct {
	id     int64
	seed   uint64
	rounds int64
	next   int64
	checks int64

	probes     []*node
	weaks      []weak.Pointer[node]
	weakChecks int64
	fins       int64
}

func (w *worker) probe(n *node, phase string) {
	if n == nil {
		failf("phase=%s worker=%d: nil probe node", phase, w.id)
	}
	runtime.SetFinalizer(n, func(*node) {
		atomic.AddInt64(&w.fins, 1)
	})
	w.probes = append(w.probes, n)
	w.weaks = append(w.weaks, weak.Make(n))
}

// retire 先解绑上一轮探针的 finalizer，再丢强引用：正常终结不应被计入本轮。
func (w *worker) retire() {
	for _, p := range w.probes {
		runtime.SetFinalizer(p, nil)
	}
	w.probes = w.probes[:0]
	w.weaks = w.weaks[:0]
}

func (w *worker) checkNode(n *node, seed uint64, wantID int64, phase, where string) {
	if n == nil {
		failf("phase=%s worker=%d round=%d seed=%d %s: nil *node (堆持有栈指针/UAF)", phase, w.id, w.next, w.seed, where)
	}
	if n.id != wantID || n.v != nodeMagic(seed, n.id) {
		failf("phase=%s worker=%d round=%d seed=%d %s: canary mismatch id=%d v=%#x want=%#x (逃逸分析失败/UAF)",
			phase, w.id, w.next, w.seed, where, n.id, n.v, nodeMagic(seed, n.id))
	}
	w.checks++
}

func (w *worker) round(r int64) {
	seed := w.seed ^ (uint64(r+1) * 0x9E3779B97F4A7C15)
	base := w.id*1_000_000_000 + r*1000 + 1
	w.retire()

	// a) 闭包捕获局部变量 → 全局 chan + 全局 interface 槽。
	cl, clNode := mkClosure(seed, base+1)
	wantClo := clNode.v + int64(seed%977) + (base+1)*3 + 1
	gClosureCh[w.id] <- cl
	gIfaceSlot[w.id] = cl
	w.probe(clNode, "closure")

	// b) 局部数组 + 就地 append → 全局 map。
	s := mkAppended(seed, base+100)
	gSliceMap[w.id]["esc"] = s
	w.probe(s[0], "append-build")
	w.probe(s[escArrN-1], "append-build")

	// c) interface{} 装箱局部指针。
	iface, ifNode := mkIface(seed, base+200)
	gIfaceCh[w.id] <- iface
	w.probe(ifNode, "iface")

	// d) 值接收者方法值。
	meth, mNode, wantMeth := mkMethod(seed, base+300)
	gMethodCh[w.id] <- meth
	w.probe(mNode, "method")

	// e) 泛型取参数地址返回，多类型实例化。
	i64v := int64(seed) + base + 400
	genNode := mkNode(seed, base+401, "esc-generic")
	gv := genVal{id: base + 401, v: nodeMagic(seed, base+401) ^ 0x5A5A, p: genNode, pad: [2]int64{base, int64(seed)}}
	pInt := returnLocal[int64](i64v)
	pGen := returnLocal[genVal](gv)
	pArr := returnLocal[[2]int64]([2]int64{base + 402, int64(seed)})
	gGenericCh[w.id] <- pInt
	gGenericCh[w.id] <- pGen
	gGenericCh[w.id] <- pArr
	w.probe(genNode, "generic")

	// 让逃逸源函数的栈帧被复用，再在“逃逸存储后、读取前”执行 GC 窗口。
	stackChurn(stackChurnD, int64(seed)^base)
	runtime.GC()
	atomic.AddInt64(&gcCycles, 1)

	// 读回全局存储：任何 canary 失配都说明堆里存过栈指针。
	cl2 := <-gClosureCh[w.id]
	if got := cl2(); got != wantClo {
		failf("phase=post-gc worker=%d round=%d seed=%d closure: captured value=%d want=%d (栈上捕获变量被复用/UAF)",
			w.id, r, seed, got, wantClo)
	}
	cl3, ok := gIfaceSlot[w.id].(func() int64)
	if !ok {
		failf("phase=post-gc worker=%d round=%d seed=%d closure-iface: lost func value %T", w.id, r, seed, gIfaceSlot[w.id])
	}
	if got := cl3(); got != wantClo+1 {
		failf("phase=post-gc worker=%d round=%d seed=%d closure-iface: captured value=%d want=%d", w.id, r, seed, got, wantClo+1)
	}
	gIfaceSlot[w.id] = nil

	s2 := gSliceMap[w.id]["esc"]
	if len(s2) != escArrN || cap(s2) != escArrN {
		failf("phase=post-gc worker=%d round=%d seed=%d append: len=%d cap=%d want=%d", w.id, r, seed, len(s2), cap(s2), escArrN)
	}
	for i := 0; i < escArrN; i++ {
		w.checkNode(s2[i], seed, base+100+int64(i), "post-gc", fmt.Sprintf("append[%d]", i))
	}

	n2, ok := (<-gIfaceCh[w.id]).(*node)
	if !ok || n2 == nil {
		failf("phase=post-gc worker=%d round=%d seed=%d iface: type assertion to *node failed", w.id, r, seed)
	}
	w.checkNode(n2, seed, base+200, "post-gc", "iface")

	meth2 := <-gMethodCh[w.id]
	if got := meth2(); got != wantMeth {
		failf("phase=post-gc worker=%d round=%d seed=%d method: receiver copy result=%d want=%d (方法值捕获栈副本)",
			w.id, r, seed, got, wantMeth)
	}

	pInt2, ok := (<-gGenericCh[w.id]).(*int64)
	if !ok || pInt2 == nil || *pInt2 != i64v {
		failf("phase=post-gc worker=%d round=%d seed=%d generic.int64: value mismatch", w.id, r, seed)
	}
	pGen2, ok := (<-gGenericCh[w.id]).(*genVal)
	if !ok || pGen2 == nil {
		failf("phase=post-gc worker=%d round=%d seed=%d generic.genVal: type assertion failed", w.id, r, seed)
	}
	if pGen2.id != base+401 || pGen2.v != nodeMagic(seed, base+401)^0x5A5A || pGen2.pad != [2]int64{base, int64(seed)} {
		failf("phase=post-gc worker=%d round=%d seed=%d generic.genVal: content corrupted id=%d v=%#x", w.id, r, seed, pGen2.id, pGen2.v)
	}
	w.checkNode(pGen2.p, seed, base+401, "post-gc", "generic.p")
	pArr2, ok := (<-gGenericCh[w.id]).(*[2]int64)
	if !ok || pArr2 == nil || pArr2[0] != base+402 || pArr2[1] != int64(seed) {
		failf("phase=post-gc worker=%d round=%d seed=%d generic.[2]int64: value mismatch", w.id, r, seed)
	}

	// weak：强持有期间 Value() 必须非 nil；可达对象不得被终结。
	for i, wp := range w.weaks {
		if wp.Value() == nil {
			failf("phase=post-gc worker=%d round=%d seed=%d weak[%d]: Value()==nil while strongly referenced (提前回收/UAF)",
				w.id, r, seed, i)
		}
		w.weakChecks++
	}
	if n := atomic.LoadInt64(&w.fins); n != 0 {
		failf("phase=post-gc worker=%d round=%d seed=%d: reachable escaped object was finalized (count=%d)", w.id, r, seed, n)
	}

	// 第二轮 GC 后复查（局部 cl2/s2/n2/meth2/泛型指针仍存活）。
	runtime.GC()
	atomic.AddInt64(&gcCycles, 1)
	w.checkNode(clNode, seed, base+1, "post-gc2", "closure.node")
	w.checkNode(s2[escArrN-1], seed, base+100+escArrN-1, "post-gc2", "append.last")
	w.checkNode(genNode, seed, base+401, "post-gc2", "generic.p")
	if wp := w.weaks[0]; wp.Value() == nil {
		failf("phase=post-gc2 worker=%d round=%d seed=%d: weak[0] nil while referenced", w.id, r, seed)
	}
	w.weakChecks++
	if n := atomic.LoadInt64(&w.fins); n != 0 {
		failf("phase=post-gc2 worker=%d round=%d seed=%d: reachable escaped object was finalized (count=%d)", w.id, r, seed, n)
	}

	w.rounds++
	w.next = r + 1
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
	if n := atomic.LoadInt64(&w.fins); n != 0 {
		failf("phase=final worker=%d: reachable escaped object was finalized (count=%d)", w.id, n)
	}
	w.retire()
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
			sink = append(sink, b)
		}
		for i := range sink {
			sink[i] = nil
		}
		sink = sink[:0]
		runtime.GC()
		atomic.AddInt64(&gcCycles, 1)
		// window（GOMAXPROCS=1+STW）下无休眠 GC 循环会饿死 worker/watchdog。
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
	workers := envInt("STRESS_WORKERS", 4, 1, maxWorkers)
	hb := envInt("STRESS_HEARTBEAT", 5, 1, 60)
	seed := envSeed(1)
	fixed := int64(envInt("STRESS_FIXED_ROUNDS", 16, 1, 1000000))
	gSeed = seed

	fmt.Printf("scenario=%s phase=preflight go=%s goos=%s goarch=%s gomaxprocs=%d seed=%d seconds=%d workers=%d fixed_rounds=%d heartbeat=%ds array=%d forms=closure/append/iface/method/generic\n",
		scenario, runtime.Version(), runtime.GOOS, runtime.GOARCH, runtime.GOMAXPROCS(0), seed, seconds, workers, fixed, hb, escArrN)

	gClosureCh = make([]chan func() int64, workers)
	gIfaceCh = make([]chan interface{}, workers)
	gMethodCh = make([]chan func() int64, workers)
	gGenericCh = make([]chan interface{}, workers)
	gSliceMap = make([]map[string][]*node, workers)
	gIfaceSlot = make([]interface{}, workers)
	for i := 0; i < workers; i++ {
		gClosureCh[i] = make(chan func() int64, chanSlotSize)
		gIfaceCh[i] = make(chan interface{}, chanSlotSize)
		gMethodCh[i] = make(chan func() int64, chanSlotSize)
		gGenericCh[i] = make(chan interface{}, chanSlotSize)
		gSliceMap[i] = make(map[string][]*node)
	}

	// 预热 finalizer/weak 运行时 goroutine，再取 goroutine 基线。
	dummy := &node{id: -1, v: 1 << 40, tag: "warm"}
	runtime.SetFinalizer(dummy, func(*node) {})
	for i := 0; i < 3; i++ {
		runtime.GC()
		time.Sleep(time.Millisecond)
	}
	runtime.SetFinalizer(dummy, nil)
	runtime.KeepAlive(dummy)
	baseGoroutines := runtime.NumGoroutine()

	deadline := time.Now().Add(time.Duration(seconds) * time.Second)
	ws := make([]*worker, workers)
	for i := 0; i < workers; i++ {
		ws[i] = &worker{id: int64(i), seed: seed*1_000_003 + uint64(i+1)*7919}
	}

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

	var roundsTotal, checksTotal, weakChecks, fins int64
	for _, w := range ws {
		roundsTotal += w.rounds
		checksTotal += w.checks
		weakChecks += w.weakChecks
		fins += atomic.LoadInt64(&w.fins)
		if seconds == 0 && w.next != fixed {
			failf("phase=final worker=%d: rounds=%d want=%d", w.id, w.next, fixed)
		}
	}
	if roundsTotal == 0 {
		failf("phase=final: no rounds completed")
	}
	if fins != 0 {
		failf("phase=final: reachable escaped objects were finalized (count=%d)", fins)
	}

	ng := waitGoroutines(baseGoroutines, 5*time.Second)
	if ng > baseGoroutines+4 {
		failf("phase=final: goroutines baseline=%d after=%d: goroutine leak", baseGoroutines, ng)
	}

	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	fmt.Printf("metrics: rounds=%d canary_checks=%d weak_checks=%d objects_finalized=%d gc_churn=%d num_gc=%d heap_alloc=%d heap_objects=%d goroutines=%d\n",
		roundsTotal, checksTotal, weakChecks, fins,
		atomic.LoadInt64(&gcCycles), m.NumGC, m.HeapAlloc, m.HeapObjects, ng)
	fmt.Printf("STRESS PASS %s\n", scenario)
}
