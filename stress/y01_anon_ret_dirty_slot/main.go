// Y01: 匿名按值返回槽脏字（P0，covers L17/L18）。
// x05 命中形状（大对象 + 匿名按值返回槽未清零，GC 扫到旧已释放数据）的
// 最小纯化 + 压力拉满，去掉 panic/Goexit 载体，专打槽本身：
//   - makeBig(...) Big：Big 528B（16 指针 + words/pad 混合，指针位跨两个
//     位图 word），无命名结果——匿名返回槽（超寄存器返回阈值，槽在调用方帧）；
//   - dirtyRec：深度 d=3+i%96 递归把各层帧刷满指向短命堆对象的指针，depth 0
//     处 gcGate() 清扫成悬挂（issue2 dirtyRec 手法）；victimRec 同深度下降
//     （同签名同 carrier → 帧尺寸一致），使 makeBig 的返回槽落在脏字上；
//   - 调用方在拿到返回值前先 gcGate()（noinline helper 承载 GC——槽仍脏的
//     扫描窗口），makeBig 返回与首次读之间再 gcGate()（槽已写入的扫描窗口），
//     之后分字段消费校验 canary；
//   - 变体：分字段部分写入（每步后 GC 穷举写入窗口）、同一调用点 4 轮复用
//     （同栈位反复）、返回值直接送 chan / 直接装箱 interface、weak 强引用探针。
//
// 官方 Go 恒 PASS；崩溃（found bad pointer in Go heap / pointer to free
// object / marked free object）= 编译器 bug（返回槽未入口清零/栈图漏标）；
// FAIL canary = 确定性 UAF（对象被提前回收/复用）。
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
	scenario = "y01_anon_ret_dirty_slot"

	bigPtrs  = 16
	bigWords = 8
	bigPad   = 40 // Ptrs(16)+Words(8)+Pad(40)=64 words → Tail 落在位图 word1

	hotCalls    = 4
	hotStep     = 1000
	depthJitter = 96 // d = 3 + i%96
	roundStride = 50_000

	offFull  = 0
	offStep  = 10_000
	offChan  = 20_000
	offIface = 30_000
	offHot   = 40_000
)

// Big：>128B，17 个指针字段跨两个位图 word，words/pad 非指针混合。
// 布局（byte 偏移）：Ptrs 0..128（位图 word0）、Words 128..192（非指针）、
// Pad 192..512（非指针）、Tail 512（位图 word1 bit0）、Seq 520。
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

// nodeMagic 与 u01/x05 一致：canary 由 (seed,id) 决定，任何回收/复用都会失配。
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

// gcGate：noinline helper 承载 GC——调用点的 caller 帧 liveness/栈图是
// 被测对象（扫描窗口精确落在 helper 调用点）。
//
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
	if b.Pad[0] != uint64(base)*0x9E37+1 || b.Pad[bigPad-1] != uint64(base)*0x9E37+uint64(bigPad) {
		failf("phase=%s: Pad corrupted [%#x %#x]", phase, b.Pad[0], b.Pad[bigPad-1])
	}
	if b.Seq != base {
		failf("phase=%s: Seq=%d want=%d", phase, b.Seq, base)
	}
	runtime.KeepAlive(&b)
}

// makeBig：匿名按值返回大对象（无命名结果）。
//
//go:noinline
func makeBig(seed uint64, base int64) Big {
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

// makeBigStepwise：分字段部分写入，每步后 GC——穷举写入窗口
// （指针槽未写/半写状态下调用方帧被扫描）。
//
//go:noinline
func makeBigStepwise(seed uint64, base int64, dirty bool) Big {
	var b Big
	for i := range b.Words {
		b.Words[i] = wordPattern(seed, base, i)
	}
	for i := range b.Pad {
		b.Pad[i] = uint64(base)*0x9E37 + uint64(i) + 1
	}
	b.Seq = base
	if !dirty {
		gcGate() // 窗口 0：只有非指针字段已写，全部指针槽未写
	}
	for i := 0; i < bigPtrs; i++ {
		b.Ptrs[i] = newNode(seed, base+int64(i))
		if !dirty {
			gcGate() // 窗口 1..16：每写一个指针字段后扫描一次
		}
	}
	b.Tail = newNode(seed, base+bigPtrs)
	if !dirty {
		gcGate() // 窗口 17：全部写入完成
	}
	return b
}

// fillDirty 把 Big 的全部指针字段刷成指向短命堆对象的指针（帧刷脏）。
func fillDirty(b *Big, seed uint64, base int64) {
	for i := range b.Ptrs {
		b.Ptrs[i] = newNode(seed, base+int64(i))
	}
	b.Tail = newNode(seed, base+bigPtrs)
	b.Seq = base
}

// checksum 读取全部指针字段：刷脏写的最后使用点先于递归调用，递归调用的
// safepoint 处这些槽已死，depth 0 的 gcGate 把短命对象清扫成悬挂。
//
//go:noinline
func checksum(b *Big) int64 {
	var s int64
	for i := range b.Ptrs {
		s += b.Ptrs[i].v
	}
	s += b.Tail.v
	return s
}

type leafFn func(w *worker, seed uint64, base int64, dirty bool) Big

// dirtyRec：深度 d 递归把各层帧刷满指向短命堆对象的指针，depth 0 处经叶子
// 刷入返回槽后 gcGate 清扫成悬挂。与 victimRec 同签名 → 帧尺寸一致。
// base 全程不变（叶子与 caller 校验用同一 base）；层内刷脏 id 随 d 推进。
//
//go:noinline
func dirtyRec(w *worker, seed uint64, base int64, d int, c Big, leaf leafFn) Big {
	cc := c
	fillDirty(&cc, seed, base+int64(d)*1000)
	s := checksum(&cc)
	if d == 0 {
		_ = leaf(w, seed, base, true)
		gcGate() // 清扫：本帧 carrier + 叶子返回槽 → 悬挂
		return cc
	}
	r := dirtyRec(w, seed, base, d-1, cc, leaf)
	_ = s
	return r
}

// victimRec：同深度下降（同签名同 carrier → 各层帧落在旧 dirtyRec 帧的脏字上），
// depth 0 处调叶子（makeBig 的返回槽落在脏字上）。
//
//go:noinline
func victimRec(w *worker, seed uint64, base int64, d int, c Big, leaf leafFn) Big {
	cc := c
	fillDirty(&cc, seed, base+int64(d)*1000)
	s := checksum(&cc)
	if d == 0 {
		bv := leaf(w, seed, base, false)
		_ = s
		return bv
	}
	bv := victimRec(w, seed, base, d-1, cc, leaf)
	_ = s
	return bv
}

// victimGate：一轮完整复刻——先脏栈，再同深度下降。
//
//go:noinline
func victimGate(w *worker, seed uint64, base int64, d int, leaf leafFn) Big {
	dirtyRec(w, seed, base, d, Big{}, leaf)
	return victimRec(w, seed, base, d, Big{}, leaf)
}

// leafFull：完整写入 + 前/后双 GC 窗口。
// 前窗口：拿到返回值前——槽仍脏（悬挂指针）；后窗口：返回与首次读之间——槽已写入。
//
//go:noinline
func leafFull(w *worker, seed uint64, base int64, dirty bool) Big {
	if !dirty {
		gcGate() // 拿到返回值前：槽仍脏 → 栈图不得把脏字当活指针
	}
	b := makeBig(seed, base)
	if !dirty {
		gcGate() // 返回后、首次读前：槽已写入 → 栈图必须标活全部指针
		checkBig(b, seed, base, "y01.full/post-gate")
	}
	return b
}

// leafStep：分字段部分写入（makeBigStepwise 内每步后 GC）。
//
//go:noinline
func leafStep(w *worker, seed uint64, base int64, dirty bool) Big {
	b := makeBigStepwise(seed, base, dirty)
	if !dirty {
		checkBig(b, seed, base, "y01.step/post-writes")
	}
	return b
}

// leafChan：返回值直接送 chan（不经本地读）。
//
//go:noinline
func leafChan(w *worker, seed uint64, base int64, dirty bool) Big {
	if !dirty {
		gcGate()
	}
	b := makeBig(seed, base)
	if !dirty {
		w.ch <- b
		gcGate()
	}
	return b
}

// leafIface：返回值直接装箱进 interface（不经本地读）。
//
//go:noinline
func leafIface(w *worker, seed uint64, base int64, dirty bool) Big {
	if !dirty {
		gcGate()
	}
	b := makeBig(seed, base)
	if !dirty {
		v := any(b)
		gcGate() // 装箱后 GC：interface 数据指针必须被扫描
		bv := v.(Big)
		checkBig(bv, seed, base, "y01.iface/unbox")
	}
	return b
}

// leafHot：同一调用点 hotCalls 轮复用（同栈位反复）：每轮调用前 GC——
// 扫描上一轮写入后已死的旧值（悬挂）或首轮脏槽；跨轮次复用同一栈位。
//
//go:noinline
func leafHot(w *worker, seed uint64, base int64, dirty bool) Big {
	var last Big
	for i := 0; i < hotCalls; i++ {
		if !dirty {
			gcGate() // 同栈位反复：扫描上一轮的旧值（已死→悬挂）
		}
		last = makeBig(seed, base+int64(i)*hotStep)
	}
	return last
}

type worker struct {
	id     int64
	seed   uint64
	base0  int64
	rounds int64
	ch     chan Big

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

	// A: 完整写入 + 前/后 GC 窗口。
	bv := victimGate(w, w.seed, base+offFull, d, leafFull)
	checkBig(bv, w.seed, base+offFull, "y01.full/caller")

	// B: 分字段部分写入（每步后 GC，穷举写入窗口）。
	bv = victimGate(w, w.seed, base+offStep, d, leafStep)
	checkBig(bv, w.seed, base+offStep, "y01.step/caller")

	// C: 返回值直接送 chan。
	bv = victimGate(w, w.seed, base+offChan, d, leafChan)
	checkBig(bv, w.seed, base+offChan, "y01.chan/returned")

	// D: 返回值直接装箱 interface（叶子内已校验）。
	bv = victimGate(w, w.seed, base+offIface, d, leafIface)
	checkBig(bv, w.seed, base+offIface, "y01.iface/returned")

	// E: 同一调用点 4 轮复用（同栈位反复）。
	hotBase := base + offHot + (hotCalls-1)*hotStep
	bv = victimGate(w, w.seed, base+offHot, d, leafHot)
	wp := weak.Make(bv.Ptrs[0])
	gcGate()
	v := wp.Value()
	if v == nil {
		failf("phase=y01.weak worker=%d round=%d: 结果仍被强引用却已回收（返回槽漏标）", w.id, r)
	}
	checkNode(v, w.seed, hotBase, "y01.weak")
	atomic.AddInt64(&gWeak, 1)
	checkBig(bv, w.seed, hotBase, "y01.hot/caller")

	// chan 回收端：值必须跨过期间全部 GC 存活。
	bv = <-w.ch
	gcGate()
	checkBig(bv, w.seed, base+offChan, "y01.chan/drain")

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
	if est := int64(depthJitter+3) * 4096; est > 32<<20 {
		failf("phase=preflight: depth=%d 估算栈用量 %d 超过安全线", depthJitter+3, est)
	}

	fmt.Printf("scenario=%s phase=preflight go=%s goos=%s goarch=%s gomaxprocs=%d seed=%d seconds=%d workers=%d fixed_rounds=%d heartbeat=%ds depth_jitter=%d big_size=%d\n",
		scenario, runtime.Version(), runtime.GOOS, runtime.GOARCH, runtime.GOMAXPROCS(0),
		seed, seconds, workers, fixed, hb, depthJitter, reflect.TypeOf(Big{}).Size())
	fmt.Printf("scenario=%s phase=preflight note=anon_byval_return_slot; variants=full_gates,stepwise_writes,chan,iface,hot_site,weak_probe\n", scenario)

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
			id:    int64(i),
			seed:  seed*1_000_003 + uint64(i+1)*7919,
			base0: int64(i)*1_000_000_000 + 1_000_000,
			ch:    make(chan Big, 8),
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
