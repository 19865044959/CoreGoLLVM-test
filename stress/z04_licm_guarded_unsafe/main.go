// Z04: 守卫内 unsafe.Pointer 算术被 LICM 提升（P0）。
// 目标：go#82865 / go#83420（Go 1.27 新回归）——循环不变量提升把守卫内的
// unsafe.Pointer 算术移出守卫，使无效指针在安全点上“存活”，GC 报
// "invalid pointer found on stack"。
//
// 每个 worker：一个 64KB 堆 buf（按 32B nodeView 视图填充 canary）、一组堆 node 对象、
// 一个 []pairView（uintptr 中间量）和一个 8KB 字符串。
// 每轮按轮换顺序执行 5 种循环形态：
//
//	a) 边界守卫：指针基址循环不变量，unsafe.Add 算术在守卫内（最后 16 次守卫为假）；
//	b) nil 守卫：循环外 base 在 nil/有效之间按轮交替，算术为循环不变量，最易被 LICM 提升；
//	c) 类型守卫：interface 数组混合 *node 与 string，仅在 ok 分支内做 unsafe 视图；
//	d) uintptr 中间量：守卫内 uintptr(unsafe.Pointer(&arr[0])) + off 再转回指针；
//	e) 字符串切片守卫：守卫内 unsafe.StringData(str) + offset 读字节。
//
// 所有解引用都在守卫成立、界内且对象存活的范围内（合法程序）；指针计算只在守卫内发生。
// 每次计算后、校验前调用可能触发 runtime.GC() 的 maybeGC，让指针跨安全点存活。
// 独立 gcChurn 并发；任何 canary 不符或进程崩溃即失败（崩溃文本由 runner 记录）。
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
	"unsafe"
)

const (
	scenario  = "z04_licm_guarded_unsafe"
	bufSize   = 64 << 10
	viewSize  = 32 // unsafe.Sizeof(nodeView{})
	viewN     = bufSize / viewSize
	formIters = 512
	strLen    = 8192

	viewTag int64 = 0x5A04
	nodeTag int64 = 0x7A04
)

// nodeView 是 buf 内 32B 一格的自描述视图。
type nodeView struct {
	id   int64
	v    int64
	tag  int64
	next int64
}

// liveNode 是堆上独立分配、同时被 unsafe 视图访问的 node 对象。
type liveNode struct {
	id  int64
	v   int64
	tag int64
	_   int64
}

// pairView 用于 uintptr 中间量形态。
type pairView struct {
	key uint64
	val uint64
}

func mix64(x uint64) uint64 {
	x += 0x9E3779B97F4A7C15
	x = (x ^ (x >> 30)) * 0xBF58476D1CE4E5B9
	x = (x ^ (x >> 27)) * 0x94D049BB133111EB
	x ^= x >> 31
	return x
}

// cellWant 是 buf 视图与 uintptr 形态的确定性 canary。
func cellWant(seed uint64, id int64) int64 {
	v := int64(mix64(seed^uint64(id)*0x9E3779B97F4A7C15) & 0x7FFFFFFFFFFFFFFF)
	if v == 0 {
		v = 0x5DEECE66D
	}
	return v
}

// nodeWant 是堆 node 的确定性 canary。
func nodeWant(seed uint64, id int64) int64 {
	v := int64(mix64(seed^uint64(id)*0xD1B54A32D192ED03^0xA5A5A5A5) & 0x7FFFFFFFFFFFFFFF)
	if v == 0 {
		v = 0x2545F4914F6CDD1D
	}
	return v
}

func byteWant(seed uint64, off int) byte {
	return byte(mix64(seed^uint64(off)*0x27D4EB2F165667C5) >> 17)
}

type worker struct {
	id   int64
	seed uint64

	buf     []byte
	nodes   []liveNode // nil 守卫用（&nodes[0] 为基址）
	ptrs    []*liveNode
	arr     []pairView
	str     string
	ifaces  []interface{}
	gcEvery int64
	gcCalls int64
	checks  int64
	rounds  int64
	sink    int64
}

func (w *worker) init() {
	w.buf = make([]byte, bufSize)
	bp := unsafe.Pointer(&w.buf[0])
	for i := 0; i < viewN; i++ {
		v := (*nodeView)(unsafe.Add(bp, i*viewSize))
		v.id = int64(i)
		v.v = cellWant(w.seed, int64(i))
		v.tag = viewTag
		v.next = int64(i + 1)
	}

	w.nodes = make([]liveNode, formIters)
	for i := range w.nodes {
		w.nodes[i] = liveNode{id: int64(i), v: nodeWant(w.seed, int64(i)), tag: nodeTag}
	}
	w.ptrs = make([]*liveNode, formIters)
	for i := range w.ptrs {
		w.ptrs[i] = &liveNode{id: int64(i), v: nodeWant(w.seed, int64(i)), tag: nodeTag}
	}
	w.arr = make([]pairView, formIters)
	for i := range w.arr {
		w.arr[i] = pairView{key: uint64(i), val: uint64(cellWant(w.seed, int64(i)))}
	}
	sb := make([]byte, strLen)
	for i := range sb {
		sb[i] = byteWant(w.seed, i)
	}
	w.str = string(sb)

	w.ifaces = make([]interface{}, 64)
	for i := range w.ifaces {
		if i%4 == 3 {
			w.ifaces[i] = fmt.Sprintf("z04-bad-%d", i)
		} else {
			w.ifaces[i] = w.ptrs[(i*7)%len(w.ptrs)]
		}
	}
	// 每 worker 的 GC 间隔由 seed 派生（128..383 次迭代一次）。
	w.gcEvery = 128 + int64((w.seed>>8)%256)
}

func (w *worker) maybeGC(i int64) {
	if w.gcEvery > 0 && i%w.gcEvery == 0 {
		runtime.GC()
		w.gcCalls++
		gcYield()
	}
}

func (w *worker) checkView(p *nodeView, i int64, form string, r int64) {
	if p == nil {
		failf("form=%s worker=%d round=%d seed=%d: view[%d] nil", form, w.id, r, w.seed, i)
	}
	if p.id != i || p.v != cellWant(w.seed, i) || p.tag != viewTag || p.next != i+1 {
		failf("form=%s worker=%d round=%d seed=%d: view[%d] corrupted (id=%d v=%d tag=%d next=%d wantV=%d)",
			form, w.id, r, w.seed, i, p.id, p.v, p.tag, p.next, cellWant(w.seed, i))
	}
	w.sink += p.v
	w.checks++
}

func (w *worker) checkNodeAt(p *liveNode, i int64, form string, r int64) {
	if p == nil || p.id != i || p.v != nodeWant(w.seed, i) || p.tag != nodeTag {
		var gotID, gotV, gotTag int64 = -1, -1, -1
		if p != nil {
			gotID, gotV, gotTag = p.id, p.v, p.tag
		}
		failf("form=%s worker=%d round=%d seed=%d: node[%d] corrupted (id=%d v=%d tag=%d wantV=%d)",
			form, w.id, r, w.seed, i, gotID, gotV, gotTag, nodeWant(w.seed, i))
	}
	w.sink += p.v
	w.checks++
}

// formBounds: a) 边界守卫。指针基址是循环不变量，unsafe.Add 在守卫内。
// 守卫最后 16 次为假（i*viewSize >= len(s)），源码不计算越界指针；
// 若 LICM 把算术提升出守卫，越界指针会在 maybeGC 的安全点上“存活”。
func (w *worker) formBounds(r int64) {
	s := w.buf
	n := viewN + 16
	for i := 0; i < n; i++ {
		if i*viewSize < len(s) {
			p := (*nodeView)(unsafe.Add(unsafe.Pointer(&s[0]), i*viewSize))
			w.maybeGC(int64(i))
			w.checkView(p, int64(i), "bounds", r)
		} else {
			w.maybeGC(int64(i))
		}
	}
}

// formNil: b) nil 守卫。base 在循环外按轮交替为 nil/有效；算术（循环不变 off）
// 在 if base != nil 内。nil 轮若算术被提升，nil+off 会出现在安全点上。
func (w *worker) formNil(r int64) {
	var base unsafe.Pointer
	if r%2 == 0 {
		base = unsafe.Pointer(&w.nodes[0])
	}
	off := int64(r%4) * int64(unsafe.Sizeof(liveNode{}))
	for i := 0; i < formIters; i++ {
		if base != nil {
			p := (*liveNode)(unsafe.Add(base, off))
			w.maybeGC(int64(i))
			w.checkNodeAt(p, off/int64(unsafe.Sizeof(liveNode{})), "nil-guard", r)
		} else {
			w.maybeGC(int64(i))
		}
	}
}

// formType: c) 类型守卫。interface 数组混合 *node 与 string，只有 ok 分支做 unsafe 视图。
func (w *worker) formType(r int64) {
	for i := 0; i < formIters; i++ {
		slot := (i + int(r)) % len(w.ifaces)
		iface := w.ifaces[slot]
		if v, ok := iface.(*liveNode); ok {
			p := (*liveNode)(unsafe.Add(unsafe.Pointer(v), 0))
			// ifaces[slot] 初始化时指向 ptrs[(slot*7)%N]，按此校验身份。
			exp := int64((slot * 7) % len(w.ptrs))
			w.maybeGC(int64(i))
			w.checkNodeAt(p, exp, "type-guard", r)
		} else if s, ok := iface.(string); !ok || len(s) == 0 {
			failf("form=type-guard worker=%d round=%d seed=%d: bad branch type=%T", w.id, r, w.seed, iface)
		} else {
			w.maybeGC(int64(i))
		}
	}
}

// formUintptr: d) uintptr 中间量，全部在守卫内：uintptr(&arr[0]) + off 再转回指针。
// checkptr（-race 默认开启）只允许“同一表达式内”的 uintptr 回转，不支持先存
// 中间变量；本形态正是两段式，故显式关闭本函数的 checkptr 插桩。off 由
// `j < len(arr)` 守卫，仍保证解引用在界内；其余 4 种形态保留 checkptr 检查。
//
//go:nocheckptr
func (w *worker) formUintptr(r int64) {
	n := len(w.arr) + 16
	rot := int64(0)
	if r%3 == 0 {
		rot = 1
	}
	sz := unsafe.Sizeof(pairView{})
	for i := int64(0); i < int64(n); i++ {
		j := i + rot
		if j < int64(len(w.arr)) {
			off := uintptr(j) * sz
			u := uintptr(unsafe.Pointer(&w.arr[0])) + off
			p := (*pairView)(unsafe.Pointer(u))
			w.maybeGC(i)
			if p.key != uint64(j) || p.val != uint64(cellWant(w.seed, j)) {
				failf("form=uintptr worker=%d round=%d seed=%d: arr[%d] corrupted (key=%d val=%d want=%d)",
					w.id, r, w.seed, j, p.key, p.val, uint64(cellWant(w.seed, j)))
			}
			w.sink += int64(p.val)
			w.checks++
		} else {
			w.maybeGC(i)
		}
	}
}

// formString: e) 字符串切片守卫。unsafe.StringData 在守卫内，跨守卫的字节读取前
// 再做 j < len(str) 检查；最后 16 次守卫为假。
func (w *worker) formString(r int64) {
	s := w.str
	n := len(s) + 16
	for j := 0; j < n; j++ {
		if j <= len(s) {
			p := unsafe.StringData(s)
			if j < len(s) {
				b := *(*byte)(unsafe.Add(unsafe.Pointer(p), j))
				w.maybeGC(int64(j))
				if b != byteWant(w.seed, j) {
					failf("form=string worker=%d round=%d seed=%d: str[%d]=%#x want=%#x",
						w.id, r, w.seed, j, b, byteWant(w.seed, j))
				}
				w.sink += int64(b)
				w.checks++
			} else {
				w.maybeGC(int64(j))
			}
		} else {
			w.maybeGC(int64(j))
		}
	}
}

// verifySample: 一轮结束后抽查 buf 视图与 node canary，确认所有显式 GC 之后内容完好。
func (w *worker) verifySample(r int64) {
	bp := unsafe.Pointer(&w.buf[0])
	for k := 0; k < 32; k++ {
		i := int64(k) * (viewN / 32)
		p := (*nodeView)(unsafe.Add(bp, int(i)*viewSize))
		w.checkView(p, i, "post-round", r)
	}
	for i := 0; i < len(w.ptrs); i += 37 {
		w.checkNodeAt(w.ptrs[i], int64(i), "post-round", r)
	}
}

func (w *worker) round(r int64) {
	order := int((uint64(r) + uint64(w.id)) % 5)
	for k := 0; k < 5; k++ {
		switch (order + k) % 5 {
		case 0:
			w.formBounds(r)
		case 1:
			w.formNil(r)
		case 2:
			w.formType(r)
		case 3:
			w.formUintptr(r)
		case 4:
			w.formString(r)
		}
	}
	if r%4 == 0 {
		runtime.GC()
		w.gcCalls++
		gcYield()
	}
	w.verifySample(r)
	w.rounds++
	atomic.AddInt64(&beats, 1)
}

func (w *worker) run(seconds int, deadline time.Time, fixed int64) {
	atomic.AddInt64(&started, 1)
	defer atomic.AddInt64(&finished, 1)
	w.init()
	runtime.GC()
	gcYield()
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
	gcYield()
	w.verifySample(r)
	if w.checks == 0 {
		failf("worker=%d seed=%d: no checks executed", w.id, w.seed)
	}
}

var (
	beats    int64
	gcCycles int64
	started  int64
	finished int64
	gSeed    uint64
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

// gcYield: GOMAXPROCS=1 时在显式 GC 之后让出 P，避免多 worker 在单 P 上
// 紧邻调用 runtime.GC 造成排队饥饿（与 gcChurn 的让出同理）。
func gcYield() {
	if runtime.GOMAXPROCS(0) == 1 {
		runtime.Gosched()
		time.Sleep(time.Millisecond)
	}
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
				failf("phase=watchdog seed=%d: 无 worker 进展 %s (beats=%d)", gSeed, time.Since(lastChange), cur)
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
	workers := envInt("STRESS_WORKERS", 4, 1, 32)
	hb := envInt("STRESS_HEARTBEAT", 5, 1, 60)
	seed := envSeed(1)
	fixed := int64(envInt("STRESS_FIXED_ROUNDS", 16, 1, 1000000))
	gSeed = seed

	fmt.Printf("scenario=%s phase=preflight go=%s goos=%s goarch=%s gomaxprocs=%d seed=%d seconds=%d workers=%d fixed_rounds=%d heartbeat=%ds buf=%d views=%d str=%d forms=5\n",
		scenario, runtime.Version(), runtime.GOOS, runtime.GOARCH, runtime.GOMAXPROCS(0), seed, seconds, workers, fixed, hb,
		bufSize, viewN, strLen)

	baseGoroutines := runtime.NumGoroutine()
	deadline := time.Now().Add(time.Duration(seconds) * time.Second)
	ws := make([]*worker, workers)
	for i := 0; i < workers; i++ {
		ws[i] = &worker{id: int64(i), seed: seed*1000 + uint64(i) + 1}
	}

	var wg sync.WaitGroup
	for _, w := range ws {
		wg.Add(1)
		go func(w *worker) {
			defer wg.Done()
			w.run(seconds, deadline, fixed)
		}(w)
	}
	stop := make(chan struct{})
	var aux sync.WaitGroup
	aux.Add(1)
	go gcChurn(stop, &aux)
	aux.Add(1)
	go watchdog(time.Duration(hb)*time.Second, stop, &aux)

	wg.Wait()
	close(stop)
	aux.Wait()

	if s, f := atomic.LoadInt64(&started), atomic.LoadInt64(&finished); s != int64(workers) || f != s {
		failf("phase=final seed=%d: started=%d finished=%d workers=%d", seed, s, f, workers)
	}

	var rounds, checks, gcs, sink int64
	for _, w := range ws {
		rounds += w.rounds
		checks += w.checks
		gcs += w.gcCalls
		sink += w.sink
		if seconds == 0 && w.rounds != fixed {
			failf("phase=final seed=%d worker=%d: rounds=%d want=%d", seed, w.id, w.rounds, fixed)
		}
	}
	if rounds == 0 || checks == 0 {
		failf("phase=final seed=%d: no rounds/checks completed (rounds=%d checks=%d)", seed, rounds, checks)
	}

	ng := waitGoroutines(baseGoroutines, 5*time.Second)
	if ng > baseGoroutines+4 {
		failf("phase=final seed=%d: goroutines baseline=%d after=%d: goroutine leak", seed, baseGoroutines, ng)
	}

	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	fmt.Printf("metrics: rounds=%d checks=%d explicit_gc=%d gc_churn=%d workers=%d buf=%d views=%d forms=5 sink=%d heap_alloc=%d heap_objects=%d num_gc=%d goroutines=%d\n",
		rounds, checks, gcs, atomic.LoadInt64(&gcCycles), workers, bufSize, viewN, sink,
		m.HeapAlloc, m.HeapObjects, m.NumGC, ng)
	fmt.Printf("STRESS PASS %s\n", scenario)
}
