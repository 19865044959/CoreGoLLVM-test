// X05: Goexit / panic 帧与 dead stack object（P0，覆盖 L17/L18）。
// 目标：go#82394 / go#82917 的形态——panic 记录在展开过程中保活死栈对象：
//   - 大 struct 按值传参给 noinline 函数（callee 内显式 GC）；
//   - 闭包取地址局部指针数组，构造 address-taken 栈对象/死栈对象形态；
//   - 丢弃本地强引用 → runtime.GC() → runtime.Goexit()，defer 链在退出过程中再 GC
//     并校验从旧帧带出的 canary（保留指针变体）或只确认无崩溃（全丢弃变体）；
//   - panic(value payload) 在外层 defer recover：展开后、GC 后再校验 payload 内指针；
//   - panic(pointer payload) + weak.Make 探针：weak 在展开+GC 后必须仍能看到对象；
//   - panic → defer 内 runtime.Callers 走查栈 → recover → 再 panic，共三层；
//   - panic + defer + WaitGroup + Gosched 交错变体（多 goroutine 同时展开）。
//
// 说明：不在本进程直接读取已回收的栈内存（那是未定义行为，stock 也可能崩）；
// “旧帧区域”的判据一律走合法引用（defer 捕获指针 / recover 的 panic payload / weak 探针）。
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
	scenario = "x05_goexit_panicframe"

	arrN       = 8
	bigSlots   = 8
	bigWords   = 16
	exitSlots  = 4
	chainSlots = 8
	payloadSlo = 8
	wgFanout   = 4

	roundStride  = 10_000_000
	deepBaseOff  = 1_000
	chainBaseOff = 2_000_000
	exitBaseOff  = 4_000_000
	valueBaseOff = 6_000_000
	ptrBaseOff   = 7_000_000
	wgBaseOff    = 8_000_000
	anchorOff    = 9
)

type node struct {
	id  int64
	v   int64
	pad [2]uint64
}

// big 按值传参（>128B），内部大量指针；用于 args/locals 栈映射形态。
type big struct {
	a     [bigSlots]*node
	b     [bigSlots]*node
	words [bigWords]uint64
	seq   int64
}

// panicPayload 以大值形式 panic（非指针）：展开过程中由 _panic 记录保活。
type panicPayload struct {
	seed  uint64
	id    int64
	sum   int64
	n     *node
	slots [payloadSlo]*node
	words [bigWords]uint64
}

type counters struct {
	chain  int64
	checks int64
}

var (
	beats int64

	workerStarted  int64
	workerFinished int64

	roundsTotal int64

	sentinelPaths int64
	exitSpawned   int64
	exitFinished  int64

	chainRecoveries   int64
	callersFrames     int64
	panicWGRecoveries int64
	goscheds          int64

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
		failf("phase=%s: nil *node (UAF/帧扫描漏标)", phase)
	}
	if n.v != nodeMagic(seed, n.id) {
		failf("phase=%s id=%d: canary v=%#x want=%#x (UAF/帧扫描漏标)",
			phase, n.id, n.v, nodeMagic(seed, n.id))
	}
	atomic.AddInt64(&canaryChecks, 1)
}

func checkPayload(pl panicPayload, seed uint64, phase string) {
	checkNode(pl.n, seed, phase+".n")
	for i := range pl.slots {
		checkNode(pl.slots[i], seed, phase+".slot")
	}
	for i := range pl.words {
		if pl.words[i] != uint64(i)*0x9E3779B97F4A7C15 {
			failf("phase=%s: words[%d]=%#x: panic payload 被改写", phase, i, pl.words[i])
		}
	}
}

func makePayload(seed uint64, baseID int64, seq int64) panicPayload {
	p := panicPayload{seed: seed, id: baseID, sum: seq}
	p.n = newNode(seed, baseID)
	p.slots[0] = p.n
	for i := 1; i < len(p.slots); i++ {
		p.slots[i] = newNode(seed, baseID+int64(i))
	}
	for i := range p.words {
		p.words[i] = uint64(i) * 0x9E3779B97F4A7C15
	}
	return p
}

func makeBig(seed uint64, baseID int64) big {
	var bv big
	bv.seq = baseID
	for i := 0; i < bigSlots; i++ {
		bv.a[i] = newNode(seed, baseID+int64(i))
		bv.b[i] = newNode(seed, baseID+int64(bigSlots+i))
	}
	for i := range bv.words {
		bv.words[i] = uint64(i+1) * 0x9E3779B97F4A7C15
	}
	return bv
}

// consumeBig 大 struct 按值传参 + 指向局部数组的指针；callee 内 GC 前后各校验一次。
//
//go:noinline
func consumeBig(bv big, arrp *[arrN]*node, seed uint64, phase string) int64 {
	sum := bv.seq
	for i := 0; i < bigSlots; i++ {
		checkNode(bv.a[i], seed, phase+".a-pre")
		checkNode(bv.b[i], seed, phase+".b-pre")
	}
	for i := 0; i < arrN; i++ {
		checkNode(arrp[i], seed, phase+".arr-pre")
	}
	runtime.GC()
	atomic.AddInt64(&gcCycles, 1)
	for i := 0; i < bigSlots; i++ {
		checkNode(bv.a[i], seed, phase+".a-post")
		checkNode(bv.b[i], seed, phase+".b-post")
		sum += bv.a[i].v + bv.b[i].v
	}
	for i := 0; i < arrN; i++ {
		checkNode(arrp[i], seed, phase+".arr-post")
		sum += arrp[i].v
	}
	return sum
}

// ---------------------------------------------------------------- Goexit 形态

// goexitRetain: 闭包取址局部数组，保留一个从旧帧带出的指针，Goexit 的 defer
// 链里再 GC 后校验；weak 探针确认展开+GC 期间对象仍存活。
//
//go:noinline
func goexitRetain(seed uint64, baseID int64, out chan<- int64) {
	atomic.AddInt64(&exitSpawned, 1)
	arr := [arrN]*node{}
	for i := range arr {
		arr[i] = newNode(seed, baseID+int64(i))
	}
	bv := makeBig(seed, baseID+100)
	sum := consumeBig(bv, &arr, seed, "x05.exit_retain.consume")

	pick := func(i int) *node { return arr[i] } // 闭包取址局部数组
	tail := pick(arrN - 1)
	wk := weak.Make(tail)

	bv = big{} // 丢弃本地强引用（按值副本在 consumeBig 返回后已消失）
	for i := range arr {
		arr[i] = nil
	}
	runtime.GC()
	atomic.AddInt64(&gcCycles, 1)

	defer func() {
		// Goexit 展开路径上的 defer：再 GC，然后读旧帧带出的 canary。
		runtime.GC()
		atomic.AddInt64(&gcCycles, 1)
		checkNode(tail, seed, "x05.exit_retain.defer.tail")
		if wk.Value() != tail {
			failf("phase=x05.exit_retain: 展开+GC 后 weak.Value()=nil (对象被提前回收)")
		}
		atomic.AddInt64(&weakChecks, 1)
		atomic.AddInt64(&exitFinished, 1)
		out <- sum + tail.v
	}()

	runtime.Goexit()
	failf("phase=x05.exit_retain: runtime.Goexit 返回了")
}

// goexitDead: 同样的按值大 struct + 局部数组 + 闭包取址形态，但丢弃全部强引用后
// Goexit；defer 链里只做 GC 与求和。用于“确认无崩溃”的死栈对象形态。
//
//go:noinline
func goexitDead(seed uint64, baseID int64, out chan<- int64) {
	atomic.AddInt64(&exitSpawned, 1)
	arr := [arrN]*node{}
	for i := range arr {
		arr[i] = newNode(seed, baseID+int64(i))
	}
	bv := makeBig(seed, baseID+100)
	sum := consumeBig(bv, &arr, seed, "x05.exit_dead.consume")

	view := func() int64 {
		var s int64
		for i := range arr {
			s += arr[i].v
		}
		return s
	}
	deadSum := view()

	bv = big{}
	for i := range arr {
		arr[i] = nil
	}
	runtime.GC()
	atomic.AddInt64(&gcCycles, 1)

	defer func() {
		runtime.GC()
		atomic.AddInt64(&gcCycles, 1)
		atomic.AddInt64(&exitFinished, 1)
		out <- sum + deadSum
	}()

	runtime.Goexit()
	failf("phase=x05.exit_dead: runtime.Goexit 返回了")
}

// ---------------------------------------------------------------- panic 形态

// panicValueShape: 以“大值”panic；外层 defer recover 后先 GC 再校验 payload。
// 展开期间内层帧已死，_panic 记录必须保活 payload 内的指针。
//
//go:noinline
func panicValueShape(seed uint64, baseID int64) {
	arr := [arrN]*node{}
	for i := range arr {
		arr[i] = newNode(seed, baseID+int64(i))
	}
	bv := makeBig(seed, baseID+100)
	sum := consumeBig(bv, &arr, seed, "x05.value.consume")

	pl := makePayload(seed, baseID+500, sum)
	for i := range pl.slots {
		pl.slots[i] = arr[i%arrN]
	}

	bv = big{}
	for i := range arr {
		arr[i] = nil
	}
	runtime.GC()
	atomic.AddInt64(&gcCycles, 1)

	defer func() {
		e := recover()
		if e == nil {
			failf("phase=x05.value: panic 未被 recover")
		}
		got, ok := e.(panicPayload)
		if !ok {
			failf("phase=x05.value: payload 类型=%T want=panicPayload", e)
		}
		// 先 GC 再校验：展开后的 payload 必须仍然完整。
		runtime.GC()
		atomic.AddInt64(&gcCycles, 1)
		checkPayload(got, seed, "x05.value.recover")
		if got.sum != sum {
			failf("phase=x05.value: payload sum=%#x want=%#x", got.sum, sum)
		}
		atomic.AddInt64(&sentinelPaths, 1)
	}()

	panic(pl)
}

// panicPointerShape: 指针 payload + weak 探针；recover 后 GC 再校验，
// weak.Value() 必须仍指向被 panic 记录保活的对象。
//
//go:noinline
func panicPointerShape(seed uint64, baseID int64) {
	arr := [arrN]*node{}
	for i := range arr {
		arr[i] = newNode(seed, baseID+int64(i))
	}
	bv := makeBig(seed, baseID+100)
	sum := consumeBig(bv, &arr, seed, "x05.ptr.consume")

	plv := makePayload(seed, baseID+500, sum)
	for i := range plv.slots {
		plv.slots[i] = arr[i%arrN]
	}
	wk := weak.Make(plv.n)

	bv = big{}
	for i := range arr {
		arr[i] = nil
	}
	runtime.GC()
	atomic.AddInt64(&gcCycles, 1)

	defer func() {
		e := recover()
		if e == nil {
			failf("phase=x05.ptr: panic 未被 recover")
		}
		got, ok := e.(*panicPayload)
		if !ok {
			failf("phase=x05.ptr: payload 类型=%T want=*panicPayload", e)
		}
		runtime.GC()
		atomic.AddInt64(&gcCycles, 1)
		checkPayload(*got, seed, "x05.ptr.recover")
		if wk.Value() != got.n {
			failf("phase=x05.ptr: 展开+GC 后 weak.Value()=nil (panic 记录未保活 dead stack object)")
		}
		atomic.AddInt64(&weakChecks, 1)
		atomic.AddInt64(&sentinelPaths, 1)
	}()

	panic(&plv)
}

// checkCallers 在 defer/展开路径上走查栈，确认帧信息可用（只做基本完整性检查）。
func checkCallers(phase string) {
	var pcs [64]uintptr
	n := runtime.Callers(2, pcs[:])
	if n == 0 {
		failf("phase=%s: runtime.Callers 返回 0 帧", phase)
	}
	frames := runtime.CallersFrames(pcs[:n])
	count := 0
	for {
		f, more := frames.Next()
		if f.Function == "" {
			failf("phase=%s: CallersFrames 返回空函数名", phase)
		}
		count++
		if !more {
			break
		}
	}
	if count < 2 {
		failf("phase=%s: 帧数=%d want>=2", phase, count)
	}
	atomic.AddInt64(&callersFrames, int64(count))
}

// chainBottom: d+1 层持指针帧，栈底以“大值 payload”panic。
//
//go:noinline
func chainBottom(d int, parent *node, seed uint64, baseID int64) {
	local := newNode(seed, baseID+int64(d))
	var slots [chainSlots]*node
	slots[0] = local
	slots[chainSlots-1] = parent
	for i := 1; i < chainSlots-1; i++ {
		slots[i] = local
	}
	checkNode(slots[0], seed, "x05.chain.bottom.local")
	checkNode(slots[chainSlots-1], seed, "x05.chain.bottom.parent")
	if d == 0 {
		runtime.GC()
		atomic.AddInt64(&gcCycles, 1)
		pl := makePayload(seed, baseID+777, local.v)
		for i := range pl.slots {
			pl.slots[i] = local
		}
		panic(pl)
	}
	chainBottom(d-1, local, seed, baseID)
	checkNode(local, seed, "x05.chain.bottom.unwind")
}

// chainL3/L2/L1: panic → defer 内 Callers 走查栈 + GC → recover → 再 panic；
// L1 是最后一层 recover。每层的 big 参数按值传递，制造 args/locals 栈映射形态。
//
//go:noinline
func chainL3(depth int, bv big, seed uint64, baseID int64, c *counters) (ok bool) {
	defer func() {
		e := recover()
		if e == nil {
			return
		}
		checkCallers("x05.chain.l3")
		runtime.GC()
		atomic.AddInt64(&gcCycles, 1)
		pl, is := e.(panicPayload)
		if !is {
			failf("phase=x05.chain.l3: payload 类型=%T", e)
		}
		checkPayload(pl, seed, "x05.chain.l3")
		c.chain++
		atomic.AddInt64(&chainRecoveries, 1)
		np := makePayload(seed, pl.id+1_000, pl.sum+pl.n.v)
		for i := range np.slots {
			np.slots[i] = bv.a[i]
		}
		panic(np)
	}()
	checkNode(bv.a[0], seed, "x05.chain.l3.arg")
	chainBottom(depth, bv.a[0], seed, baseID)
	return true
}

//
//go:noinline
func chainL2(depth int, bv big, seed uint64, baseID int64, c *counters) (ok bool) {
	defer func() {
		e := recover()
		if e == nil {
			return
		}
		checkCallers("x05.chain.l2")
		runtime.GC()
		atomic.AddInt64(&gcCycles, 1)
		pl, is := e.(panicPayload)
		if !is {
			failf("phase=x05.chain.l2: payload 类型=%T", e)
		}
		checkPayload(pl, seed, "x05.chain.l2")
		c.chain++
		atomic.AddInt64(&chainRecoveries, 1)
		np := makePayload(seed, pl.id+1_000, pl.sum+pl.n.v)
		for i := range np.slots {
			np.slots[i] = bv.b[i]
		}
		panic(np)
	}()
	checkNode(bv.b[0], seed, "x05.chain.l2.arg")
	chainL3(depth, bv, seed, baseID+10, c)
	return true
}

//
//go:noinline
func chainL1(depth int, bv big, seed uint64, baseID int64, c *counters) (ok bool) {
	defer func() {
		e := recover()
		if e == nil {
			return
		}
		checkCallers("x05.chain.l1")
		runtime.GC()
		atomic.AddInt64(&gcCycles, 1)
		pl, is := e.(panicPayload)
		if !is {
			failf("phase=x05.chain.l1: payload 类型=%T", e)
		}
		checkPayload(pl, seed, "x05.chain.l1")
		c.chain++
		atomic.AddInt64(&chainRecoveries, 1)
		ok = true
	}()
	checkNode(bv.a[1], seed, "x05.chain.l1.arg")
	chainL2(depth, bv, seed, baseID+20, c)
	return false
}

// panicWG: panic + defer + WaitGroup + Gosched 交错变体。
func (w *worker) panicWG(baseID int64) {
	var wg sync.WaitGroup
	for i := 0; i < wgFanout; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			pl := makePayload(w.seed, baseID+int64(i)*100, int64(i))
			var slots [4]*node
			for j := range slots {
				slots[j] = newNode(w.seed, baseID+int64(i)*100+int64(10+j))
			}
			defer func() {
				e := recover()
				if e == nil {
					failf("phase=x05.wg i=%d: panic 未被 recover", i)
				}
				got, ok := e.(panicPayload)
				if !ok {
					failf("phase=x05.wg i=%d: payload 类型=%T", i, e)
				}
				runtime.Gosched()
				atomic.AddInt64(&goscheds, 1)
				runtime.GC()
				atomic.AddInt64(&gcCycles, 1)
				checkPayload(got, w.seed, "x05.wg.recover")
				for j := range slots {
					checkNode(slots[j], w.seed, "x05.wg.slot")
				}
				atomic.AddInt64(&panicWGRecoveries, 1)
			}()
			runtime.Gosched()
			atomic.AddInt64(&goscheds, 1)
			panic(pl)
		}(i)
		runtime.Gosched()
		atomic.AddInt64(&goscheds, 1)
	}
	wg.Wait()
}

type worker struct {
	id     int64
	seed   uint64
	base   int64
	depth  int
	exits  int
	rounds int64
	c      counters
	exitCh chan int64
}

func (w *worker) round(r int64) {
	base := w.base + r*roundStride

	// 1) panic(value payload) → 外层 recover → GC → 校验。
	panicValueShape(w.seed, base+valueBaseOff)
	// 2) panic(pointer payload) + weak 探针。
	panicPointerShape(w.seed, base+ptrBaseOff)

	// 3) 三层 panic / Callers / re-panic 链，big 按值穿过每一层。
	before := w.c.chain
	chainL1(w.depth, makeBig(w.seed, base+chainBaseOff), w.seed, base+chainBaseOff, &w.c)
	if got := w.c.chain - before; got != 3 {
		failf("phase=x05.chain worker=%d round=%d: recoveries=%d want=3", w.id, r, got)
	}

	// 4) Goexit 路径：保留指针变体与全丢弃变体交替。
	for i := 0; i < w.exits; i++ {
		ebase := base + exitBaseOff + int64(i)*1_000
		if i%2 == 0 {
			go goexitRetain(w.seed, ebase, w.exitCh)
		} else {
			go goexitDead(w.seed, ebase, w.exitCh)
		}
	}
	var retainSums int64
	for i := 0; i < w.exits; i++ {
		retainSums += <-w.exitCh
	}
	if retainSums == 0 {
		failf("phase=x05.exit worker=%d round=%d: 没有收到 Goexit 校验和", w.id, r)
	}

	// 5) panic + WaitGroup + Gosched 交错。
	w.panicWG(base + wgBaseOff)

	// 6) 跨整轮的 weak 存活探针。
	anchor := newNode(w.seed, base+anchorOff)
	wk := weak.Make(anchor)
	runtime.GC()
	atomic.AddInt64(&gcCycles, 1)
	if wk.Value() != anchor {
		failf("phase=x05.weak worker=%d round=%d: GC 后 strong-reachable weak.Value()=nil", w.id, r)
	}
	atomic.AddInt64(&weakChecks, 1)
	runtime.KeepAlive(anchor)
	runtime.KeepAlive(wk)

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
	depth := envInt("STRESS_DEPTH", 192, 16, 2048)
	exits := envInt("STRESS_EXITS", 2, 1, 16)
	gSeed = seed

	if est := int64(depth+1) * 1024; est > 32<<20 {
		failf("phase=preflight: depth=%d 估算栈用量 %d 超过安全线", depth, est)
	}
	debug.SetMaxStack(64 << 20)

	fmt.Printf("scenario=%s phase=preflight go=%s goos=%s goarch=%s gomaxprocs=%d seed=%d seconds=%d workers=%d depth=%d exits_per_round=%d fixed_rounds=%d heartbeat=%ds\n",
		scenario, runtime.Version(), runtime.GOOS, runtime.GOARCH, runtime.GOMAXPROCS(0),
		seed, seconds, workers, depth, exits, fixed, hb)
	fmt.Printf("scenario=%s phase=preflight note=goexit_uses_runtime.Goexit; variants=value_payload,ptr_weak,3layer_callers,wg_gosched,exit_retain,exit_dead\n", scenario)

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
			id:     int64(i),
			seed:   seed*1_000_003 + uint64(i+1)*7919,
			base:   int64(i)*1_000_000_000 + 1_000_000,
			depth:  depth,
			exits:  exits,
			exitCh: make(chan int64, exits),
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

	var rounds, chain int64
	for _, w := range ws {
		rounds += w.rounds
		chain += w.c.chain
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
	if want := rounds * 3; chain != want {
		failf("phase=final: chain recoveries=%d want=%d", chain, want)
	}
	if s, f := atomic.LoadInt64(&exitSpawned), atomic.LoadInt64(&exitFinished); s != f || s != rounds*int64(exits) {
		failf("phase=final: exit goroutines spawned=%d finished=%d want=%d", s, f, rounds*int64(exits))
	}
	if want := rounds * 2; atomic.LoadInt64(&sentinelPaths) != want {
		failf("phase=final: sentinel panic 路径=%d want=%d", atomic.LoadInt64(&sentinelPaths), want)
	}
	if want := rounds * wgFanout; atomic.LoadInt64(&panicWGRecoveries) != want {
		failf("phase=final: panicWG recoveries=%d want=%d", atomic.LoadInt64(&panicWGRecoveries), want)
	}
	retainPerRound := int64((exits + 1) / 2) // ptr payload + retain exit + anchor
	if want := rounds * (2 + retainPerRound); atomic.LoadInt64(&weakChecks) != want {
		failf("phase=final: weak 探针=%d want=%d", atomic.LoadInt64(&weakChecks), want)
	}
	if atomic.LoadInt64(&canaryChecks) == 0 {
		failf("phase=final: 没有执行 canary 校验")
	}
	if atomic.LoadInt64(&callersFrames) == 0 {
		failf("phase=final: 没有执行 Callers 走查")
	}

	ng := waitBaseline(baseGoroutines, 6, 5*time.Second)
	if ng > baseGoroutines+4 {
		failf("phase=final: goroutines baseline=%d after=%d: goroutine 泄漏", baseGoroutines, ng)
	}

	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	fmt.Printf("metrics: rounds=%d chain_recoveries=%d sentinel_paths=%d exit_spawned=%d exit_finished=%d panic_wg=%d callers_frames=%d callers_checks=%d gosched=%d canary_checks=%d weak_probes=%d gc_churn=%d num_gc=%d heap_alloc=%d heap_objects=%d goroutines=%d\n",
		rounds, chain, atomic.LoadInt64(&sentinelPaths), atomic.LoadInt64(&exitSpawned),
		atomic.LoadInt64(&exitFinished), atomic.LoadInt64(&panicWGRecoveries),
		atomic.LoadInt64(&callersFrames), atomic.LoadInt64(&callersFrames),
		atomic.LoadInt64(&goscheds), atomic.LoadInt64(&canaryChecks), atomic.LoadInt64(&weakChecks),
		atomic.LoadInt64(&gcCycles), m.NumGC, m.HeapAlloc, m.HeapObjects, ng)
	fmt.Printf("STRESS PASS %s\n", scenario)
}
