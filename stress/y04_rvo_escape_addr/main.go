// Y04: RVO/内联消拷贝 × 字段地址导出逃逸（P0，covers L11/L17）。
// func f() Big { var b Big; ...; return b } 的内联版（期望消拷贝直接构造在调用方槽）
// 与非内联版对照；调用方取返回值字段地址导出（&ret.p 存入 interface 逃逸 → 栈对象
// 位图被测）→ GC → 经导出指针校验；返回后立刻 GC 再消费；defer 修改返回值变体
// （返回值在 defer 前求值，defer 内改写局部聚合 + deferreturn 窗口 GC——RVO 若把
// 局部聚合提升为结果槽，改写泄漏进结果即值语义破坏）：
//   - fNoinline：匿名返回大对象（fresh 短命节点，noinline）；
//   - fInline：同形状但字段写全来自预置池（低成本 → 可内联 + RVO 消拷贝）；
//   - fDefer：defer 内改写局部聚合 Ptrs[0] + gcGate（deferreturn 窗口：调用方槽
//     此时尚未写入），返回值必须保持 defer 前快照；
//   - 消费模式：字段地址导出逃逸（&ret.P[0] → interface）→ GC → 经导出指针校验；
//     chan 直接送走 → GC → drain 校验；weak 强引用探针；
//   - 脏栈铺垫：dirtyRec（d=3+i%96 各层帧刷满短命指针 + depth 0 gcGate 清扫）
//     + victimRec 同深度下降（同签名同 carrier → 帧尺寸一致，返回槽落在脏字上）。
//
// 官方 Go 恒 PASS；崩溃 = 编译器 bug（返回槽未入口清零/栈对象位图漏标）；
// FAIL canary/指针同一性/快照 = 确定性 UAF 或 RVO/拷贝语义破坏。
package main

import (
	"fmt"
	"os"
	"reflect"
	"runtime"
	"runtime/debug"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
	"weak"
)

const (
	scenario = "y04_rvo_escape_addr"

	bigPtrs  = 16
	bigWords = 8
	bigPad   = 40 // Ptrs(16)+Words(8)+Pad(40)=64 words → Tail 落在位图 word1
	maxW     = 16
	poolRow  = bigPtrs + 1 // 每 worker 一行池：16 + Tail

	depthJitter = 96 // d = 3 + i%96
	roundStride = 50_000

	offNI = 0
	offIN = 6_000
	offDF = 12_000
	offCH = 18_000
	offW  = 24_000
)

type Big struct {
	Ptrs  [bigPtrs]*node
	Words [bigWords]uint64
	Pad   [bigPad]uint64
	Tail  *node
	Seq   int64
}

type node struct {
	id  int64
	v   int64
	pad [2]uint64
}

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

func wordPattern(seed uint64, base int64, i int) uint64 {
	return seed*0x9E3779B97F4A7C15 + uint64(base)*uint64(i+1)*0x2545F4914F6CDD1D + uint64(i)*0x5EED5EED
}

// gPool：内联版数据源（每 worker 一行）。条目内容每轮刷新，字段写 = 池指针赋值。
var gPool [maxW * poolRow]node

func refreshPool(seed uint64, base int64, off int) {
	for i := 0; i < poolRow; i++ {
		gPool[off+i].id = base + int64(i)
		gPool[off+i].v = nodeMagic(seed, base+int64(i))
	}
}

var (
	gBeats    int64
	gChecks   int64
	gGates    int64
	gGcChurn  int64
	gWeak     int64
	gStarted  int64
	gFinished int64
	gSeed     uint64
)

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

//go:noinline
func gcGate() {
	runtime.GC()
	atomic.AddInt64(&gGates, 1)
}

func gcChurn(stop <-chan struct{}, wg *sync.WaitGroup) {
	defer wg.Done()
	oneP := runtime.GOMAXPROCS(0) == 1
	for {
		select {
		case <-stop:
			return
		default:
		}
		runtime.GC()
		atomic.AddInt64(&gGcChurn, 1)
		runtime.Gosched()
		if oneP {
			// GOMAXPROCS=1（window profile）：必须 Gosched+1ms sleep，
			// 否则 churn 抢占 P 饿死 worker/watchdog（上批血的教训）。
			time.Sleep(time.Millisecond)
		}
	}
}

func watchdog(interval time.Duration, stop <-chan struct{}, wg *sync.WaitGroup) {
	defer wg.Done()
	last := atomic.LoadInt64(&gBeats)
	lastChange := time.Now()
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-stop:
			return
		case <-t.C:
			cur := atomic.LoadInt64(&gBeats)
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

//go:noinline
func checkNode(n *node, seed uint64, wantID int64, phase string) {
	if n == nil {
		failf("phase=%s: nil *node want id=%d (UAF/栈图漏标)", phase, wantID)
	}
	if n.id != wantID || n.v != nodeMagic(seed, n.id) {
		failf("phase=%s: canary mismatch id=%d want=%d v=%#x want=%#x (UAF/复用)",
			phase, n.id, wantID, n.v, nodeMagic(seed, wantID))
	}
	atomic.AddInt64(&gChecks, 1)
	runtime.KeepAlive(n)
}

//go:noinline
func checkBig(b Big, seed uint64, base int64, phase string) {
	for i := range b.Ptrs {
		checkNode(b.Ptrs[i], seed, base+int64(i), phase+".P")
	}
	checkNode(b.Tail, seed, base+bigPtrs, phase+".Tail")
	for i := range b.Words {
		if b.Words[i] != wordPattern(seed, base, i) {
			failf("phase=%s: Words[%d]=%#x want=%#x", phase, i, b.Words[i], wordPattern(seed, base, i))
		}
	}
	if b.Seq != base {
		failf("phase=%s: Seq=%d want=%d", phase, b.Seq, base)
	}
	runtime.KeepAlive(&b)
}

// checkInlineBig：内联版校验——指针必须与预置池**同一**（拷贝/RVO 语义破坏即现形）。
//
//go:noinline
func checkInlineBig(b Big, seed uint64, base int64, poolOff int, phase string) {
	for i := range b.Ptrs {
		n := b.Ptrs[i]
		if n != &gPool[poolOff+i] {
			failf("phase=%s: Ptrs[%d]=%p want=&pool[%d] (拷贝/RVO 语义破坏)", phase, i, n, poolOff+i)
		}
		checkNode(n, seed, base+int64(i), phase+".P")
	}
	if b.Tail != &gPool[poolOff+bigPtrs] {
		failf("phase=%s: Tail=%p want=&pool[%d] (拷贝/RVO 语义破坏)", phase, b.Tail, poolOff+bigPtrs)
	}
	checkNode(b.Tail, seed, base+bigPtrs, phase+".Tail")
	for i := range b.Words {
		if b.Words[i] != wordPattern(seed, base, i) {
			failf("phase=%s: Words[%d]=%#x want=%#x", phase, i, b.Words[i], wordPattern(seed, base, i))
		}
	}
	if b.Seq != base {
		failf("phase=%s: Seq=%d want=%d", phase, b.Seq, base)
	}
	runtime.KeepAlive(&b)
}

// makeBigFresh：fresh 短命节点的完整构造。
//
//go:noinline
func makeBigFresh(seed uint64, base int64) Big {
	var b Big
	for i := range b.Ptrs {
		b.Ptrs[i] = newNode(seed, base+int64(i))
	}
	b.Tail = newNode(seed, base+bigPtrs)
	for i := range b.Words {
		b.Words[i] = wordPattern(seed, base, i)
	}
	for i := range b.Pad {
		b.Pad[i] = uint64(base)*0x9E37 + uint64(i) + 1
	}
	b.Seq = base
	return b
}

// fNoinline：非内联对照版。
//
//go:noinline
func fNoinline(seed uint64, base int64) Big {
	var b Big
	b = makeBigFresh(seed, base)
	return b
}

// fInline：内联版——字段写全来自预置池（低成本 → 可内联 + RVO 消拷贝）。
// 池指针赋值不产生分配；返回局部聚合 b。
func fInline(seed uint64, base int64, poolOff int) Big {
	var b Big
	for i := 0; i < bigPtrs; i++ {
		b.Ptrs[i] = &gPool[poolOff+i]
	}
	b.Tail = &gPool[poolOff+bigPtrs]
	for i := range b.Words {
		b.Words[i] = wordPattern(seed, base, i)
	}
	b.Seq = base
	return b
}

// fDirtyInline：fInline 的脏槽孪生（同签名）——写 fresh 短命节点，
// 供 dirty 下降把同一调用点的槽刷成可清扫形态。
//
//go:noinline
func fDirtyInline(seed uint64, base int64, poolOff int) Big {
	return makeBigFresh(seed, base)
}

// fDefer：defer 修改返回值变体。返回值在 defer 前求值（快照语义）；
// defer 内改写局部聚合 Ptrs[0] 并 gcGate（deferreturn 窗口：调用方槽此时尚未写入）。
// RVO 若把局部聚合提升为结果槽，改写会泄漏进结果 → 快照校验即现形。
//
//go:noinline
func fDefer(seed uint64, base int64) Big {
	var b Big
	b = makeBigFresh(seed, base)
	defer func() {
		b.Ptrs[0] = newNode(seed, base+777) // 改写局部聚合（不得影响已求值的返回值）
		gcGate()                            // deferreturn 窗口 GC
	}()
	return b
}

// ---------------------------------------------------------------- 脏栈下降

func fillDirty(b *Big, seed uint64, base int64) {
	for i := range b.Ptrs {
		b.Ptrs[i] = newNode(seed, base+int64(i))
	}
	b.Tail = newNode(seed, base+bigPtrs)
	b.Seq = base
}

//go:noinline
func checksum(b *Big) int64 {
	var s int64
	for i := range b.Ptrs {
		s += b.Ptrs[i].v
	}
	s += b.Tail.v
	return s
}

type leafFn func(w *worker, seed uint64, base int64, dirty bool) int64

// dirtyRec / victimRec：同签名同 carrier 同深度下降 → 帧尺寸一致，
// 返回槽落在旧 dirtyRec 帧的脏字上。base 全程不变，层内刷脏 id 随 d 推进。
//
//go:noinline
func dirtyRec(w *worker, seed uint64, base int64, d int, c Big, leaf leafFn) int64 {
	cc := c
	fillDirty(&cc, seed, base+int64(d)*1000)
	s := checksum(&cc)
	if d == 0 {
		_ = leaf(w, seed, base, true)
		gcGate() // 清扫：本帧 carrier + 叶子返回槽 → 悬挂
		return s
	}
	r := dirtyRec(w, seed, base, d-1, cc, leaf)
	_ = s
	return r
}

//go:noinline
func victimRec(w *worker, seed uint64, base int64, d int, c Big, leaf leafFn) int64 {
	cc := c
	fillDirty(&cc, seed, base+int64(d)*1000)
	s := checksum(&cc)
	if d == 0 {
		return leaf(w, seed, base, false)
	}
	r := victimRec(w, seed, base, d-1, cc, leaf)
	_ = s
	return r
}

//go:noinline
func victimGate(w *worker, seed uint64, base int64, d int, leaf leafFn) int64 {
	dirtyRec(w, seed, base, d, Big{}, leaf)
	return victimRec(w, seed, base, d, Big{}, leaf)
}

// ---------------------------------------------------------------- 消费叶子

// leafNoinline：非内联版 + 字段地址导出逃逸 + GC + 经导出指针校验。
//
//go:noinline
func leafNoinline(w *worker, seed uint64, base int64, dirty bool) int64 {
	if !dirty {
		gcGate() // 返回前：槽仍脏
	}
	ret := fNoinline(seed, base)
	if dirty {
		return 0
	}
	pp := &ret.Ptrs[0] // 取返回值字段地址
	w.escape = pp   // 存入 interface → 逃逸（栈对象位图被测）
	gcGate()        // 返回后立刻 GC 再消费
	checkNode(*pp, seed, base, "y04.noinline.escaped") // 经导出指针校验
	checkBig(ret, seed, base, "y04.noinline.full")
	w.escape = nil
	return 1
}

// leafInline：内联版 + 同样的字段地址导出逃逸模式；脏槽孪生 fDirtyInline。
//
//go:noinline
func leafInline(w *worker, seed uint64, base int64, dirty bool) int64 {
	if !dirty {
		gcGate() // 返回前：槽仍脏
	}
	var ret Big
	if dirty {
		ret = fDirtyInline(seed, base, w.poolOff)
	} else {
		ret = fInline(seed, base, w.poolOff)
	}
	if dirty {
		return 0
	}
	pp := &ret.Ptrs[0]
	w.escape = pp
	gcGate() // 返回后立刻 GC 再消费
	checkNode(*pp, seed, base, "y04.inline.escaped")
	checkInlineBig(ret, seed, base, w.poolOff, "y04.inline.full")
	w.escape = nil
	return 1
}

// leafDefer：defer 修改返回值变体——返回值必须保持 defer 前快照。
//
//go:noinline
func leafDefer(w *worker, seed uint64, base int64, dirty bool) int64 {
	if !dirty {
		gcGate() // 返回前：槽仍脏
	}
	ret := fDefer(seed, base)
	if dirty {
		return 0
	}
	gcGate() // 返回后立刻 GC 再消费
	checkNode(ret.Ptrs[0], seed, base, "y04.defer.snapshot") // defer 前快照（不得是 base+777）
	checkBig(ret, seed, base, "y04.defer.full")
	return 1
}

// leafChan：返回值直接送 chan。
//
//go:noinline
func leafChan(w *worker, seed uint64, base int64, dirty bool) int64 {
	if !dirty {
		gcGate()
	}
	ret := fNoinline(seed, base)
	if !dirty {
		w.ch <- ret
		gcGate()
	}
	return 0
}

type worker struct {
	id      int64
	seed    uint64
	base0   int64
	rounds  int64
	ch      chan Big
	poolOff int
	escape  any

	depthSum int64
	depthMin int
	depthMax int
}

func (w *worker) round(r int64) {
	base := w.base0 + r*roundStride
	d := 3 + int(r%depthJitter)
	w.depthSum += int64(d)
	if w.depthMin == 0 || d < w.depthMin {
		w.depthMin = d
	}
	if d > w.depthMax {
		w.depthMax = d
	}

	refreshPool(w.seed, base+offIN, w.poolOff) // 内联版预置池

	_ = victimGate(w, w.seed, base+offNI, d, leafNoinline)
	_ = victimGate(w, w.seed, base+offIN, d, leafInline)
	_ = victimGate(w, w.seed, base+offDF, d, leafDefer)
	_ = victimGate(w, w.seed, base+offCH, d, leafChan)

	// chan 回收端：值必须跨过期间全部 GC 存活。
	bv := <-w.ch
	gcGate()
	checkBig(bv, w.seed, base+offCH, "y04.chan/drain")

	// weak 强引用断言：结果仍被强引用时对象不得被回收。
	ret := fNoinline(w.seed, base+offW)
	wp := weak.Make(ret.Ptrs[0])
	gcGate()
	v := wp.Value()
	if v == nil {
		failf("phase=y04.weak worker=%d round=%d: 结果仍被强引用却已回收（返回槽漏标）", w.id, r)
	}
	checkNode(v, w.seed, base+offW, "y04.weak")
	atomic.AddInt64(&gWeak, 1)
	checkBig(ret, w.seed, base+offW, "y04.weak.full")

	w.rounds++
	atomic.AddInt64(&gBeats, 1)
}

func (w *worker) run(seconds int, deadline time.Time, fixed int64) {
	atomic.AddInt64(&gStarted, 1)
	defer atomic.AddInt64(&gFinished, 1)
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

func main() {
	setupCrashOutput()
	seconds := envInt("STRESS_SECONDS", 60, 0, 86400)
	workers := envInt("STRESS_WORKERS", 4, 1, 16)
	hb := envInt("STRESS_HEARTBEAT", 5, 1, 60)
	seed := envSeed(1)
	fixed := int64(envInt("STRESS_FIXED_ROUNDS", 60, 1, 1000000))
	gSeed = seed

	if sz := reflect.TypeOf(Big{}).Size(); sz < 512 {
		failf("phase=preflight: Big size=%d want >=512 (Tail 必须落在位图 word1)", sz)
	}

	fmt.Printf("scenario=%s phase=preflight go=%s goos=%s goarch=%s gomaxprocs=%d seed=%d seconds=%d workers=%d fixed_rounds=%d heartbeat=%ds depth_jitter=%d big_size=%d\n",
		scenario, runtime.Version(), runtime.GOOS, runtime.GOARCH, runtime.GOMAXPROCS(0),
		seed, seconds, workers, fixed, hb, depthJitter, reflect.TypeOf(Big{}).Size())
	fmt.Printf("scenario=%s phase=preflight note=rvo_inline_escape; variants=noinline_escape,inline_pool_escape,defer_snapshot,chan\n", scenario)

	runtime.GC()
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
			id:      int64(i),
			seed:    seed*1_000_003 + uint64(i+1)*7919,
			base0:   int64(i)*1_000_000_000 + 1_000_000,
			ch:      make(chan Big, 4),
			poolOff: i * poolRow,
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

	var rounds, depthTotal int64
	for _, w := range ws {
		rounds += w.rounds
		depthTotal += w.depthSum
		if seconds == 0 && w.rounds != fixed {
			failf("phase=final worker=%d: rounds=%d want=%d", w.id, w.rounds, fixed)
		}
		if n := len(w.ch); n != 0 {
			failf("phase=final worker=%d: chan 残留=%d want=0（每轮 drain 一次）", w.id, n)
		}
		if w.escape != nil {
			failf("phase=final worker=%d: escape 接口未清空", w.id)
		}
	}
	if rounds == 0 {
		failf("phase=final: 没有完成任何轮次")
	}
	if s, f := atomic.LoadInt64(&gStarted), atomic.LoadInt64(&gFinished); s != int64(workers) || f != s {
		failf("phase=final: workers started=%d finished=%d want=%d", s, f, workers)
	}
	if atomic.LoadInt64(&gChecks) == 0 {
		failf("phase=final: 没有执行 canary 校验")
	}

	runtime.GC()
	runtime.GC()
	ng := waitBaseline(baseGoroutines, 6, 5*time.Second)
	if ng > baseGoroutines+4 {
		failf("phase=final: goroutines baseline=%d after=%d: goroutine 泄漏", baseGoroutines, ng)
	}

	var dmin, dmax int
	for _, w := range ws {
		if dmin == 0 || w.depthMin < dmin {
			dmin = w.depthMin
		}
		if w.depthMax > dmax {
			dmax = w.depthMax
		}
	}
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	fmt.Printf("metrics: rounds=%d canary_checks=%d gates=%d gc_churn=%d weak=%d depth_min=%d depth_max=%d depth_avg=%d num_gc=%d heap_alloc=%d heap_objects=%d goroutines_start=%d goroutines_final=%d\n",
		rounds, atomic.LoadInt64(&gChecks), atomic.LoadInt64(&gGates),
		atomic.LoadInt64(&gGcChurn), atomic.LoadInt64(&gWeak),
		dmin, dmax, depthTotal/max(rounds, 1),
		m.NumGC, m.HeapAlloc, m.HeapObjects, baseGoroutines, ng)
	fmt.Printf("STRESS PASS %s\n", scenario)
}
