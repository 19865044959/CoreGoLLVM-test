// Y06: stackObjectRecord 正常流复用（P0，covers L17/L04）。
// 目标：A9 stackObjectRecord——addrtaken 大局部（含指针）经 defer 闭包取址，
// GC 必须按 stackObjectRecord 精确扫描栈中对象（此前从未覆盖；x05 的 dead object
// 是 panic 保活形态，这里是正常流中的 stack object 复用）：
//   - deepFrames：每层帧内 3 个 stack object（Big 264B + [8]*node + 混合 struct，
//     不同 offset），全被同一 defer 闭包取址捕获；depth 0 处 GC——各层只有经本层
//     栈对象可达的节点，GC 后逐层 defer 校验 canary + weak 探针（栈扫描漏
//     stackObjectRecord → 节点被回收 → 立即现形）；
//   - morestack 交错：深递归（含 defer 局部）多次增长/搬移栈，pending defer 的
//     捕获指针与 stack object 位置被 copystack adjust；
//   - 收缩：浅返回后在 channel 上阻塞（浅帧内仍有 defer 取址局部），driver 执行
//     GC/FreeOSMemory → shrinkstack/copystack 搬移栈 → 恢复后 defer 校验（指针被
//     adjust 正确）；随后再进深调用复用同栈区域（复用后新 stack object 校验）；
//   - 逃逸变体：&local 存入逃逸接口（逃逸分析堆分配——控制组），跨 goroutine 校验。
// debug.SetMaxStack 限深防真溢出。
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
	scenario = "y06_stackobj_reuse"

	bigSlots   = 8
	bigWords   = 16
	obj2N      = 8
	obj3N      = 4
	depthJitter = 96
)

type node struct {
	id  int64
	v   int64
	pad [2]uint64
}

// Big 大局部聚合（264B：16 指针 + 16 words + seq）——stack object 1。
type Big struct {
	a     [bigSlots]*node
	b     [bigSlots]*node
	words [bigWords]uint64
	seq   int64
}

// obj3T 混合 stack object 3（4 指针 + 4 words，不同 offset）。
type obj3T struct {
	s [obj3N]*node
	w [4]uint64
}

// ---------- 通用 canary ----------

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

func checkNode(n *node, wantID int64, seed uint64, phase string) {
	if n == nil {
		failf("phase=%s: nil *node want id=%d (UAF/栈对象漏扫描)", phase, wantID)
	}
	if n.id != wantID {
		failf("phase=%s: id=%d want=%d (栈对象被复用/改写)", phase, n.id, wantID)
	}
	if n.v != nodeMagic(seed, n.id) {
		failf("phase=%s id=%d: canary v=%#x want=%#x (UAF/栈对象漏扫描)", phase, n.id, n.v, nodeMagic(seed, n.id))
	}
	atomic.AddInt64(&canaryChecks, 1)
}

func fillLocal(l *Big, seed uint64, base int64) {
	l.seq = base
	for i := 0; i < bigSlots; i++ {
		l.a[i] = newNode(seed, base+int64(i))
		l.b[i] = newNode(seed, base+int64(bigSlots+i))
	}
	for i := range l.words {
		l.words[i] = uint64(i+1)*0x9E3779B97F4A7C15
	}
}

//go:noinline
func checkLocal(l *Big, seed uint64, base int64, phase string) {
	for i := 0; i < bigSlots; i++ {
		checkNode(l.a[i], base+int64(i), seed, phase+".a")
		checkNode(l.b[i], base+int64(bigSlots+i), seed, phase+".b")
	}
	for i := range l.words {
		if l.words[i] != uint64(i+1)*0x9E3779B97F4A7C15 {
			failf("phase=%s: words[%d]=%#x (局部聚合被改写)", phase, i, l.words[i])
		}
	}
	if l.seq != base {
		failf("phase=%s: seq=%d want=%d (局部聚合被改写)", phase, l.seq, base)
	}
	runtime.KeepAlive(l)
}

// useLocal：addrtaken 的另一形态（noinline 非保留调用）；读取全部指针字段
// 使刷入成为活代码。
//
//go:noinline
func useLocal(l *Big, seed uint64, base int64) int64 {
	var s int64
	for i := 0; i < bigSlots; i++ {
		checkNode(l.a[i], base+int64(i), seed, "y06.useLocal.a")
		checkNode(l.b[i], base+int64(bigSlots+i), seed, "y06.useLocal.b")
		s += l.a[i].v + l.b[i].v
	}
	return s
}

//go:noinline
func useObj2(o *[obj2N]*node, seed uint64, base int64) int64 {
	var s int64
	for i := range o {
		checkNode(o[i], base+100+int64(i), seed, "y06.useObj2")
		s += o[i].v
	}
	return s
}

//go:noinline
func useObj3(o *obj3T, seed uint64, base int64) int64 {
	var s int64
	for i := range o.s {
		checkNode(o.s[i], base+200+int64(i), seed, "y06.useObj3")
		s += o.s[i].v
	}
	for i := range o.w {
		s += int64(o.w[i])
	}
	return s
}

// ---------- report ----------

// report 是每轮堆逃逸的计数载体：defer 闭包把校验结果值写回（指针不逃逸，
// 局部保持栈对象形态）。
type report struct {
	sum          int64
	frames       int64
	shrinkFrames int64
}

// ---------- 深递归 × 同帧多 stack object ----------

// deepFrames：每层帧内 3 个 addrtaken 局部（Big + [8]*node + obj3T），被同一
// defer 闭包取址捕获；depth 0 处 GC 扫描所有层——各层节点只有经本层栈对象可达，
// 漏 stackObjectRecord 即被回收，unwind 时逐层 defer 校验 + weak 探针现形。
//
//go:noinline
func deepFrames(seed uint64, base int64, d int, rep *report) int64 {
	var local Big          // stack object 1
	var obj2 [obj2N]*node  // stack object 2（不同 offset）
	var obj3 obj3T         // stack object 3（不同 offset）
	fillLocal(&local, seed, base)
	for i := range obj2 {
		obj2[i] = newNode(seed, base+100+int64(i))
	}
	for i := range obj3.s {
		obj3.s[i] = newNode(seed, base+200+int64(i))
	}
	for i := range obj3.w {
		obj3.w[i] = uint64(base+300) + uint64(i)
	}

	wkA := weak.Make(local.a[0])
	wkB := weak.Make(obj2[0])

	defer func() {
		// 展开路径上的 defer：抽样层先 GC 再读旧帧 canary（栈对象须仍被扫描）。
		if d%17 == 0 || d < 2 {
			runtime.GC()
			atomic.AddInt64(&gcCycles, 1)
		}
		checkLocal(&local, seed, base, "y06.deep.defer")
		for i := range obj2 {
			checkNode(obj2[i], base+100+int64(i), seed, "y06.deep.obj2")
		}
		for i := range obj3.s {
			checkNode(obj3.s[i], base+200+int64(i), seed, "y06.deep.obj3")
		}
		for i := range obj3.w {
			if obj3.w[i] != uint64(base+300)+uint64(i) {
				failf("phase=y06.deep.defer d=%d: obj3.w[%d]=%#x (栈对象被改写)", d, i, obj3.w[i])
			}
		}
		if wkA.Value() != local.a[0] || wkB.Value() != obj2[0] {
			failf("phase=y06.deep.defer d=%d: GC 后 weak 探针失效（stack object 漏扫描）", d)
		}
		rep.sum += useLocal(&local, seed, base)
		rep.frames++
	}()

	sum := useLocal(&local, seed, base)
	_ = useObj2(&obj2, seed, base)
	_ = useObj3(&obj3, seed, base)

	if d == 0 {
		runtime.GC() // 栈扫描须按 stackObjectRecord 找到所有层的 local/obj2/obj3
		atomic.AddInt64(&gcCycles, 1)
		return sum
	}
	r := deepFrames(seed, base+1000, d-1, rep)
	// 子帧 depth0 GC 之后（unwind 前）再校验本帧——本帧对象必须存活。
	sum += useLocal(&local, seed, base)
	return sum + r
}

// ---------- 收缩（shrinkstack/copystack） ----------

type shrinkReq struct{ free bool }

// shrinkFrame：浅栈处阻塞于 channel（浅帧内仍有 defer 取址局部），driver 执行
// GC/FreeOSMemory → shrinkstack/copystack 搬移栈；恢复后及 defer 中校验
// captured &local/&obj2 被 adjust 正确。
//
//go:noinline
func shrinkFrame(seed uint64, base int64, free bool, reqCh chan<- shrinkReq, ackCh <-chan struct{}, rep *report) int64 {
	var local Big
	var obj2 [obj2N]*node
	fillLocal(&local, seed, base)
	for i := range obj2 {
		obj2[i] = newNode(seed, base+100+int64(i))
	}
	wk := weak.Make(local.a[0])

	defer func() {
		// 收缩/搬移之后 defer 执行：captured &local/&obj2 必须被 copystack 正确 adjust。
		runtime.GC()
		atomic.AddInt64(&gcCycles, 1)
		checkLocal(&local, seed, base, "y06.shrink.defer")
		for i := range obj2 {
			checkNode(obj2[i], base+100+int64(i), seed, "y06.shrink.obj2")
		}
		if wk.Value() != local.a[0] {
			failf("phase=y06.shrink.defer: 收缩+GC 后 weak 探针失效（栈搬移漏重定位/漏扫描）")
		}
		atomic.AddInt64(&weakChecks, 1)
		rep.shrinkFrames++
	}()

	reqCh <- shrinkReq{free: free} // 浅栈处阻塞：driver GC → shrinkstack
	<-ackCh
	atomic.AddInt64(&shrinkCycles, 1)

	// 恢复后立即校验：栈已搬移，本帧指针槽必须仍有效。
	checkLocal(&local, seed, base, "y06.shrink.post-copy")
	for i := range obj2 {
		checkNode(obj2[i], base+100+int64(i), seed, "y06.shrink.post-copy")
	}
	return useLocal(&local, seed, base)
}

// shrinkDriver 是唯一的收缩 GC 发起者：worker 在浅栈阻塞时它执行 GC /
// FreeOSMemory，使 worker 栈满足 shrinkstack 条件（used < alloc/4）。
func shrinkDriver(reqCh <-chan shrinkReq, ackCh chan<- struct{}, stop <-chan struct{}, wg *sync.WaitGroup) {
	defer wg.Done()
	for {
		select {
		case <-stop:
			return
		case req := <-reqCh:
			runtime.GC()
			atomic.AddInt64(&forcedGC, 1)
			if req.free {
				debug.FreeOSMemory()
				atomic.AddInt64(&freeOS, 1)
			}
			ackCh <- struct{}{}
		}
	}
}

// ---------- 逃逸控制变体（跨 goroutine 校验） ----------

// escBox：&local 的逃逸接口落点。escape 分析把 escapedFrame 的 local 移到堆
// （控制组：走堆分配路径，指针跨 goroutine 校验合法）。
type escBox struct {
	iface interface{}
	done  chan struct{}
	res   chan int64
}

//go:noinline
func escapedFrame(seed uint64, base int64, box *escBox) int64 {
	var local Big
	fillLocal(&local, seed, base)
	defer func() {
		box.iface = &local // 逃逸接口：local 被 escape 分析移到堆
		close(box.done)    // store 完成后 checker 才读
	}()
	sum := useLocal(&local, seed, base)
	runtime.GC()
	atomic.AddInt64(&gcCycles, 1)
	return sum
}

func checkEscaped(box *escBox, seed uint64, base int64) int64 {
	<-box.done
	p, ok := box.iface.(*Big)
	if !ok {
		failf("phase=y06.escaped: iface 不是 *Big: %T", box.iface)
	}
	checkLocal(p, seed, base, "y06.escaped.cross-goroutine") // 跨 goroutine 校验
	atomic.AddInt64(&escapedChecks, 1)
	var s int64
	for i := 0; i < bigSlots; i++ {
		s += p.a[i].v + p.b[i].v
	}
	return s
}

// ---------- 每轮 weak 探针（紧凑 A9 探测） ----------

//go:noinline
func weakProbeFrame(seed uint64, base int64) int64 {
	var local Big
	fillLocal(&local, seed, base)
	wk := weak.Make(local.a[0])
	defer func() {
		runtime.GC()
		atomic.AddInt64(&gcCycles, 1)
		if wk.Value() != local.a[0] {
			failf("phase=y06.weakprobe: strong ref held but collected（stack object 漏扫描）")
		}
		atomic.AddInt64(&weakChecks, 1)
		checkLocal(&local, seed, base, "y06.weakprobe")
	}()
	return useLocal(&local, seed, base)
}

// ---------- worker ----------

type worker struct {
	id    int64
	seed  uint64
	base0 int64
	reqCh chan<- shrinkReq
	ackCh <-chan struct{}

	next   int64
	rounds int64
	checks int64

	depthSum int64
	depthMin int
	depthMax int
}

//go:noinline
func (w *worker) round(r int64) {
	base := w.base0 + r*50_000
	d := 3 + int(r%depthJitter) // 深度抖动：任意栈布局下对齐
	w.depthSum += int64(d)
	if w.depthMin == 0 || d < w.depthMin {
		w.depthMin = d
	}
	if d > w.depthMax {
		w.depthMax = d
	}

	// 阶段 1：morestack 交错——深递归（含 defer 局部），depth0 GC 后逐层 defer 校验。
	rep := &report{}
	_ = deepFrames(w.seed, base, d, rep)
	if rep.frames != int64(d)+1 {
		failf("phase=y06.deep worker=%d round=%d: defer frames=%d want=%d", w.id, r, rep.frames, d+1)
	}

	// 阶段 2：收缩——浅返回后 park（浅帧仍有 defer 取址局部），driver GC/FreeOSMemory。
	for i := 0; i < 2; i++ {
		_ = shrinkFrame(w.seed, base+int64(i)*3000+1000, i&1 == 1, w.reqCh, w.ackCh, rep)
	}
	if rep.shrinkFrames != 2 {
		failf("phase=y06.shrink worker=%d round=%d: shrink frames=%d want=2", w.id, r, rep.shrinkFrames)
	}

	// 阶段 3：同栈区域复用——收缩后再进深调用（复用收缩后的栈区域）。
	rep2 := &report{}
	_ = deepFrames(w.seed, base+2000, d, rep2)
	if rep2.frames != int64(d)+1 {
		failf("phase=y06.reuse worker=%d round=%d: defer frames=%d want=%d", w.id, r, rep2.frames, d+1)
	}

	// 阶段 4：逃逸控制变体——跨 goroutine 校验。
	box := &escBox{done: make(chan struct{}), res: make(chan int64, 1)}
	go func() { box.res <- checkEscaped(box, w.seed, base+9000) }()
	_ = escapedFrame(w.seed, base+9000, box)
	_ = <-box.res

	// 阶段 5：紧凑 weak 探针。
	_ = weakProbeFrame(w.seed, base+9001)

	w.rounds++
	w.next = r + 1
	w.checks += 16*8 + 8
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
}

// ---------- 通用 harness ----------

var (
	beats int64

	started  int64
	finished int64

	canaryChecks  int64
	weakChecks    int64
	escapedChecks int64

	gcCycles      int64
	gcChurnCycles int64
	forcedGC      int64
	freeOS        int64
	shrinkCycles  int64

	gSeed uint64
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

// gcChurn：分配-释放-GC 循环。GOMAXPROCS=1（window profile）下必须先 Gosched
// 让出 P、再 1ms sleep，否则 worker 被饿死（心跳 watchdog 触发误报）。
func gcChurn(stop <-chan struct{}, wg *sync.WaitGroup) {
	defer wg.Done()
	var sink [][]byte
	for {
		select {
		case <-stop:
			return
		default:
		}
		for i := 0; i < 16; i++ {
			b := make([]byte, 4096)
			b[0] = byte(i)
			sink = append(sink, b)
		}
		for i := range sink {
			sink[i] = nil
		}
		sink = sink[:0]
		runtime.Gosched() // GOMAXPROCS=1 时先让出 P，防 window 饿死
		runtime.GC()
		atomic.AddInt64(&gcChurnCycles, 1)
		time.Sleep(time.Millisecond) // 1ms sleep：单 P 下 gcChurn 不能独占
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

func main() {
	setupCrashOutput()
	debug.SetMaxStack(64 << 20) // 限深防真溢出（深度受 STRESS_DEPTH 约束，远低于上限）
	seconds := envInt("STRESS_SECONDS", 60, 0, 86400)
	workers := envInt("STRESS_WORKERS", 4, 1, 32)
	hb := envInt("STRESS_HEARTBEAT", 5, 1, 60)
	seed := envSeed(1)
	fixed := int64(envInt("STRESS_FIXED_ROUNDS", 8, 1, 1000000))
	gSeed = seed

	if s := reflect.TypeOf(Big{}).Size(); s != 264 {
		failf("phase=preflight: Big size=%d want=264 (栈对象形态假设不成立)", s)
	}

	fmt.Printf("scenario=%s phase=preflight go=%s goos=%s goarch=%s gomaxprocs=%d seed=%d seconds=%d workers=%d fixed_rounds=%d heartbeat=%ds big_size=%d depth_jitter=%d\n",
		scenario, runtime.Version(), runtime.GOOS, runtime.GOARCH, runtime.GOMAXPROCS(0), seed, seconds, workers, fixed, hb,
		reflect.TypeOf(Big{}).Size(), depthJitter)

	baseGoroutines := runtime.NumGoroutine()
	deadline := time.Now().Add(time.Duration(seconds) * time.Second)
	reqCh := make(chan shrinkReq)
	ackCh := make(chan struct{})
	ws := make([]*worker, workers)
	for i := 0; i < workers; i++ {
		ws[i] = &worker{
			id:    int64(i),
			seed:  seed*1_000_003 + uint64(i+1)*7919,
			base0: int64(i)*10_000_000_000 + 1_000_000,
			reqCh: reqCh,
			ackCh: ackCh,
		}
	}

	stop := make(chan struct{})
	var aux sync.WaitGroup
	aux.Add(1)
	go gcChurn(stop, &aux)
	aux.Add(1)
	go shrinkDriver(reqCh, ackCh, stop, &aux)
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
	var roundsTotal, checksTotal int64
	for _, w := range ws {
		roundsTotal += w.rounds
		checksTotal += w.checks
		if seconds == 0 && w.next != fixed {
			failf("phase=final worker=%d: rounds=%d want=%d", w.id, w.next, fixed)
		}
	}
	if roundsTotal == 0 {
		failf("phase=final: 没有完成任何轮次")
	}
	if sc := atomic.LoadInt64(&shrinkCycles); sc != roundsTotal*2 {
		failf("phase=final: shrink_cycles=%d want=%d", sc, roundsTotal*2)
	}
	if ec := atomic.LoadInt64(&escapedChecks); ec != roundsTotal {
		failf("phase=final: escaped_checks=%d want=%d", ec, roundsTotal)
	}
	if wc := atomic.LoadInt64(&weakChecks); wc != roundsTotal*3 {
		failf("phase=final: weak_checks=%d want=%d", wc, roundsTotal*3)
	}

	ng := waitGoroutines(baseGoroutines, 5*time.Second)
	if ng > baseGoroutines+4 {
		failf("phase=final: goroutines baseline=%d after=%d: goroutine leak", baseGoroutines, ng)
	}

	var dmin, dmax, dsum int64
	for _, w := range ws {
		dsum += w.depthSum
		if dmin == 0 || int64(w.depthMin) < dmin {
			dmin = int64(w.depthMin)
		}
		if int64(w.depthMax) > dmax {
			dmax = int64(w.depthMax)
		}
	}
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	fmt.Printf("metrics: rounds=%d canary_checks=%d shrink_cycles=%d forced_gc=%d free_os=%d escaped_checks=%d weak_checks=%d worker_gc=%d gc_churn=%d depth_min=%d depth_max=%d depth_avg=%d num_gc=%d heap_alloc=%d heap_objects=%d goroutines=%d\n",
		roundsTotal, atomic.LoadInt64(&canaryChecks),
		atomic.LoadInt64(&shrinkCycles), atomic.LoadInt64(&forcedGC), atomic.LoadInt64(&freeOS),
		atomic.LoadInt64(&escapedChecks), atomic.LoadInt64(&weakChecks),
		atomic.LoadInt64(&gcCycles), atomic.LoadInt64(&gcChurnCycles),
		dmin, dmax, dsum/max(roundsTotal, 1), m.NumGC, m.HeapAlloc, m.HeapObjects, ng)
	fmt.Printf("STRESS PASS %s\n", scenario)
}
