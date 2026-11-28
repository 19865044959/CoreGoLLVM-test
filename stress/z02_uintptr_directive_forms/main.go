// Z02: //go:uintptrescapes 的 5 种调用形态（P0，covers L17/L18，go#34474/#34684）。
//
// 指令语义：函数体的 uintptr 参数可能由 unsafe.Pointer 转换而来，调用方必须把
// 源对象保活到调用返回。编译器/逃逸分析只在“静态已知被调函数”的调用形态注入
// keepalive；本场景把 5 种形态拆开，任一形态漏注入即 UAF：
//
//	a) direct           consumeDirect(pr, f, r, uintptr(up))
//	b) method_value     m := vt.consume; m(pr, f, r, uintptr(up))
//	c) method_expr      (*upTarget).consume(vt, pr, f, r, uintptr(up))
//	d) func_value       经 func 变量/参数调用（fn 未知）
//	e) iface_method     经接口方法动态分派（接口方法上不能写该指令）
//
// 被调函数体内第一件事是 runtime.GC()（关键：GC 发生在调用内），随后把 uintptr
// 转回对象校验 canary。对象为 4KB/64KB 大对象（避开 tiny 分配器），注册 finalizer
// 探针（调用期间被终结 = FAIL）+ weak.Make 断言（调用期间 Value()==nil = FAIL）。
//
// 官方 Go 1.24.1 本地实测（见 scenario.md）：direct/method_expr 保活成功；
// method_value/func_value/iface_method 在调用内即被回收。因此后三类降级为
// “强引用跨调用 + 只校验内容”，前两类保持严格 finalizer/weak 断言。
// 每个形态每轮各跑 4KB 与 64KB 两个对象；独立 GC churn 并发施压。
package main

import (
	"fmt"
	"os"
	"runtime"
	"runtime/debug"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"
	"weak"
)

const (
	scenario = "z02_uintptr_directive_forms"
	upTag    = int64(0x7A30325F)

	kindBig  = int64(0) // ~4KB
	kindHuge = int64(1) // ~64KB
)

const (
	formDirect = int64(iota)
	formMethodValue
	formMethodExpr
	formFuncValue
	formIface
	formCount
)

var formNames = [int(formCount)]string{
	"direct", "method_value", "method_expr", "func_value", "iface_method",
}

// formStrict：官方 Go 上该形态由 uintptrescapes 注入 keepalive 的实测结果。
var formStrict = [int(formCount)]bool{
	formDirect:      true,
	formMethodValue: false,
	formMethodExpr:  true,
	formFuncValue:   false,
	formIface:       false,
}

// upBig 约 4KB；upHuge 约 64KB。两者前 3 个字段布局一致（分别按类型校验）。
type upBig struct {
	id  int64
	v   int64
	tag int64
	pad [500]int64
}

type upHuge struct {
	id  int64
	v   int64
	tag int64
	pad [8180]int64
}

type upProbe struct {
	id        int64
	wantV     int64
	kind      int64
	released  int32
	premature int32
	finals    int32
	done      chan struct{}
	w4        weak.Pointer[upBig]
	w64       weak.Pointer[upHuge]
}

type upTarget struct{ x int64 }

var (
	gSeed        uint64
	beats        int64
	gcCycles     int64
	started      int64
	finished     int64
	probesMade   int64
	strictChecks int64
	finalized    int64
	idSeq        int64
	callCount    [int(formCount)]int64
	consumeFn    = consumeDirect
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

func (pr *upProbe) finish() {
	if atomic.LoadInt32(&pr.released) == 0 {
		atomic.AddInt32(&pr.premature, 1)
	}
	atomic.AddInt32(&pr.finals, 1)
	atomic.AddInt64(&finalized, 1)
	select {
	case pr.done <- struct{}{}:
	default:
	}
}

func (pr *upProbe) weakNil() bool {
	if pr.kind == kindBig {
		return pr.w4.Value() == nil
	}
	return pr.w64.Value() == nil
}

// newProbe 分配大对象并注册 finalizer + weak 探针。返回 unsafe.Pointer 以便
// 各调用形态在实参位置写 `uintptr(up)`（指令识别 unsafe.Pointer→uintptr 转换）。
func newProbe(seed uint64, id int64, kind int64) (unsafe.Pointer, *upProbe) {
	pr := &upProbe{id: id, wantV: nodeV(seed, id), kind: kind, done: make(chan struct{}, 1)}
	if kind == kindBig {
		p := new(upBig)
		p.id, p.v, p.tag = id, pr.wantV, upTag
		pr.w4 = weak.Make(p)
		runtime.SetFinalizer(p, func(*upBig) { pr.finish() })
		return unsafe.Pointer(p), pr
	}
	p := new(upHuge)
	p.id, p.v, p.tag = id, pr.wantV, upTag
	pr.w64 = weak.Make(p)
	runtime.SetFinalizer(p, func(*upHuge) { pr.finish() })
	return unsafe.Pointer(p), pr
}

func releaseProbe(pr *upProbe) { atomic.StoreInt32(&pr.released, 1) }

// afterGC 在调用内先 GC，再按形态做严格/降级检查，最后校验 canary。
// 这里故意做 uintptr→指针往返（被测语义），-race 默认开启的 checkptr 无法
// 追踪跨函数 uintptr 的分配基址，会误报 "pointer arithmetic result points to
// invalid allocation"，故对该函数关闭 checkptr 插桩。
//
//go:nocheckptr
//go:noinline
func afterGC(pr *upProbe, u uintptr, form int64, round int64) {
	runtime.GC()
	runtime.GC() // 两次完整周期：确保不可达对象在 weak 表中已被清除
	atomic.AddInt64(&gcCycles, 2)
	atomic.AddInt64(&callCount[form], 1)
	name := formNames[form]
	if formStrict[form] {
		atomic.AddInt64(&strictChecks, 1)
		if atomic.LoadInt32(&pr.premature) != 0 {
			failf("phase=in-call form=%s round=%d id=%d kind=%d: finalizer ran during call (uintptrescapes keepalive missing)", name, round, pr.id, pr.kind)
		}
		select {
		case <-pr.done:
			failf("phase=in-call/notify form=%s round=%d id=%d kind=%d: finalizer notification during call", name, round, pr.id, pr.kind)
		default:
		}
		if pr.weakNil() {
			failf("phase=in-call/weak form=%s round=%d id=%d kind=%d: weak.Make(...).Value()==nil during call (object reclaimed; directive not honored for this form)", name, round, pr.id, pr.kind)
		}
	}
	want := pr.wantV
	if pr.kind == kindBig {
		p := (*upBig)(unsafe.Pointer(u))
		if p.id != pr.id || p.v != want || p.tag != upTag {
			failf("phase=in-call/content form=%s round=%d id=%d kind=big: corrupted (got id=%d v=%#x tag=%#x want v=%#x)",
				name, round, pr.id, p.id, p.v, p.tag, want)
		}
		return
	}
	p := (*upHuge)(unsafe.Pointer(u))
	if p.id != pr.id || p.v != want || p.tag != upTag {
		failf("phase=in-call/content form=%s round=%d id=%d kind=huge: corrupted (got id=%d v=%#x tag=%#x want v=%#x)",
			name, round, pr.id, p.id, p.v, p.tag, want)
	}
}

//go:uintptrescapes
//go:noinline
func consumeDirect(pr *upProbe, form int64, round int64, u uintptr) {
	afterGC(pr, u, form, round)
}

//go:uintptrescapes
//go:noinline
func (t *upTarget) consume(pr *upProbe, form int64, round int64, u uintptr) {
	afterGC(pr, u, form, round)
}

type upIface interface {
	Consume(pr *upProbe, form int64, round int64, u uintptr)
}

//go:uintptrescapes
//go:noinline
func (t *upTarget) Consume(pr *upProbe, form int64, round int64, u uintptr) {
	afterGC(pr, u, form, round)
}

// 以下三个 helper 保证调用点看不到静态被调函数（方法值/函数值/接口分派）。

//go:noinline
func callMethodValue(v *upTarget, pr *upProbe, form, round int64, up unsafe.Pointer) {
	m := v.consume
	m(pr, form, round, uintptr(up))
}

//go:noinline
func callFuncValue(fn func(*upProbe, int64, int64, uintptr), pr *upProbe, form, round int64, up unsafe.Pointer) {
	fn(pr, form, round, uintptr(up))
}

//go:noinline
func callIface(i upIface, pr *upProbe, form, round int64, up unsafe.Pointer) {
	i.Consume(pr, form, round, uintptr(up))
}

// runOne 执行一次指定形态+对象大小的调用。降级形态额外 KeepAlive(up)，
// 把对象强持有到调用返回之后，使内容校验有意义。
//
//go:noinline
func runOne(form int64, kind int64, seed uint64, round int64, vt *upTarget) {
	id := atomic.AddInt64(&idSeq, 2)
	up, pr := newProbe(seed, id, kind)
	atomic.AddInt64(&probesMade, 1)
	switch form {
	case formDirect:
		consumeDirect(pr, form, round, uintptr(up))
	case formMethodValue:
		callMethodValue(vt, pr, form, round, up)
		runtime.KeepAlive(up)
	case formMethodExpr:
		(*upTarget).consume(vt, pr, form, round, uintptr(up))
	case formFuncValue:
		callFuncValue(consumeFn, pr, form, round, up)
		runtime.KeepAlive(up)
	case formIface:
		callIface(vt, pr, form, round, up)
		runtime.KeepAlive(up)
	}
	releaseProbe(pr)
}

type worker struct {
	id     int64
	seed   uint64
	vt     *upTarget
	rounds int64
}

func roundSeed(seed uint64, r int64) uint64 {
	s := seed + uint64(r+1)*0x9E3779B97F4A7C15
	if s == 0 {
		s = 1
	}
	return s
}

func (w *worker) round(r int64) {
	s := roundSeed(w.seed, r)
	for f := int64(0); f < formCount; f++ {
		for k := int64(0); k < 2; k++ {
			runOne(f, k, s, r, w.vt)
		}
	}
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

//go:noinline
func makeDroppedProbe(seed uint64, id int64, kind int64) *upProbe {
	_, pr := newProbe(seed, id, kind)
	releaseProbe(pr)
	return pr
}

// finalizerSanity：证明终结器机制实际运行（正向健全性检查，15s 仅为失败上限）。
func finalizerSanity(seed uint64) {
	const n = 4
	prs := make([]*upProbe, n)
	for i := range prs {
		prs[i] = makeDroppedProbe(seed, int64(0xF0000+i), int64(i&1))
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

	var modes []string
	for f := 0; f < int(formCount); f++ {
		m := "strict"
		if !formStrict[f] {
			m = "content-only"
		}
		modes = append(modes, fmt.Sprintf("%s=%s", formNames[f], m))
	}
	fmt.Printf("scenario=%s phase=preflight go=%s goos=%s goarch=%s gomaxprocs=%d seed=%d seconds=%d workers=%d fixed_rounds=%d heartbeat=%ds forms=%s\n",
		scenario, runtime.Version(), runtime.GOOS, runtime.GOARCH, runtime.GOMAXPROCS(0),
		seed, seconds, workers, fixed, hb, strings.Join(modes, ","))

	ws := make([]*worker, workers)
	for i := range ws {
		ws[i] = &worker{
			id:   int64(i),
			seed: seed*1_000_003 + uint64(i+1)*7919,
			vt:   &upTarget{x: int64(i)},
		}
	}

	warm := makeDroppedProbe(seed, -999, kindBig)
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
	// 每轮每形态各跑 4KB/64KB 两个对象，即每形态每轮 2 次调用。
	want := rounds * 2
	for f := 0; f < int(formCount); f++ {
		if got := atomic.LoadInt64(&callCount[f]); got != want {
			failf("phase=final form=%s: calls=%d want=%d", formNames[f], got, want)
		}
	}

	finalizerSanity(seed)

	ng := waitGoroutines(baseGoroutines, 5*time.Second)
	if ng > baseGoroutines+6 {
		failf("phase=final: goroutines baseline=%d after=%d: goroutine leak", baseGoroutines, ng)
	}

	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	fmt.Printf("metrics: rounds=%d calls=%d probes=%d strict_checks=%d premature_finalizers=0 finalized=%d form_direct=%d form_method_value=%d form_method_expr=%d form_func_value=%d form_iface=%d gc_cycles=%d num_gc=%d heap_alloc=%d heap_objects=%d goroutines=%d\n",
		rounds, atomic.LoadInt64(&probesMade), atomic.LoadInt64(&probesMade), atomic.LoadInt64(&strictChecks),
		atomic.LoadInt64(&finalized),
		atomic.LoadInt64(&callCount[formDirect]), atomic.LoadInt64(&callCount[formMethodValue]),
		atomic.LoadInt64(&callCount[formMethodExpr]), atomic.LoadInt64(&callCount[formFuncValue]),
		atomic.LoadInt64(&callCount[formIface]), atomic.LoadInt64(&gcCycles), m.NumGC,
		m.HeapAlloc, m.HeapObjects, ng)
	fmt.Printf("STRESS PASS %s\n", scenario)
}
