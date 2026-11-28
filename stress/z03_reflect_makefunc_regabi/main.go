// Z03: reflect.MakeFunc + register ABI RegArgs 假栈对象（P0，covers L17/L06/L11，
// go#75778；requires: reflect）。
//
// reflect.MakeFunc 生成的函数经 makeFuncStub 进入回调：指针参数先落在寄存器 /
// RegArgs 假栈对象里。若 GC 在该帧内发生而 RegArgs 未被扫描，参数指针会失效
// （marked free object in span / canary 错 / 崩溃）。本场景在回调内第一件事执行
// runtime.GC()，然后逐个参数校验 canary + 槽位号；调用侧用 register ABI 传递
// 8/12 个指针参数，另起 goroutine 高频 GC 与调用重叠。
//
// 变体：
//
//	fn8   func(*rn x6, string, *rn) *rn —— 7 指针 + string，恰好打满 amd64 整数寄存器；
//	      typed 调用（参数只存在于寄存器/调用帧）与 Value.Call 各跑一遍；
//	fn12  12 指针（>9 个整数寄存器，强制 spill 到栈参数区）；
//	fnVar variadic MakeFunc 类型（回调收到打包后的 []*rn Value）；
//	fnBig 回调返回含指针字段的大 struct（多字返回值路径）；
//	nested 回调内再 Value.Call 另一个 MakeFunc（双层 makeFuncStub 帧）。
//
// 回调返回的参数派生对象带 link/src，调用侧据此校验寄存器/参数映射无错位。
// window profile（GOGC=off + 全 STW + gccheckmark）必跑。
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
)

const (
	scenario = "z03_reflect_makefunc_regabi"
	rnTag    = int64(0x7A30335F)
)

// rn 约 1KB；canary 覆盖 id/v/tag/src 与尾部署名的 pad 槽。
type rn struct {
	id   int64
	v    int64
	tag  int64
	src  int64
	link *rn
	pad  [112]int64
}

type bigRet struct {
	a *rn
	b *rn
	c [3]*rn
	d int64
	e *rn
}

type fn8 func(*rn, *rn, *rn, *rn, *rn, *rn, string, *rn) *rn
type fn12 func(*rn, *rn, *rn, *rn, *rn, *rn, *rn, *rn, *rn, *rn, *rn, *rn) *rn
type fnVar func(*rn, ...*rn) *rn
type fnBig func(*rn, *rn, *rn, *rn) bigRet
type fn4 func(*rn, *rn, *rn, *rn) *rn

var (
	typ8   = reflect.TypeOf((fn8)(nil))
	typ12  = reflect.TypeOf((fn12)(nil))
	typVar = reflect.TypeOf((fnVar)(nil))
	typBig = reflect.TypeOf((fnBig)(nil))
	typ4   = reflect.TypeOf((fn4)(nil))
)

var (
	gSeed     uint64
	beats     int64
	gcCycles  int64
	started   int64
	finished  int64
	idSeq     int64
	outSeq    int64
	callGc    int64
	call8T    int64
	call8V    int64
	call12    int64
	callVar   int64
	callBig   int64
	callNest  int64
	nodesMade int64
)

func rnV(seed uint64, id int64) int64 {
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

func newRN(seed uint64, id int64, src int64) *rn {
	p := new(rn)
	p.id, p.v, p.tag, p.src = id, rnV(seed, id), rnTag, src
	p.pad[3] = id*7 + 3
	p.pad[100] = id*11 + 7
	atomic.AddInt64(&nodesMade, 1)
	return p
}

func newNodeID() int64 { return atomic.AddInt64(&idSeq, 16) }

// checkArg 在回调内 GC 之后校验一个参数：类型、canary、尾部 pad、槽位号。
//
//go:noinline
func checkArg(v reflect.Value, slot int, variant string) *rn {
	if !v.IsValid() || v.Kind() != reflect.Ptr {
		failf("phase=%s/slot=%d: reflect.Value not a pointer (kind=%v)", variant, slot, v.Kind())
	}
	p, ok := v.Interface().(*rn)
	if !ok || p == nil {
		failf("phase=%s/slot=%d: nil/typed-mismatch argument after GC", variant, slot)
	}
	if p.tag != rnTag || p.v != rnV(gSeed, p.id) {
		failf("phase=%s/slot=%d id=%d: argument corrupted after GC (tag=%#x v=%#x want=%#x)",
			variant, slot, p.id, p.tag, p.v, rnV(gSeed, p.id))
	}
	if p.pad[3] != p.id*7+3 || p.pad[100] != p.id*11+7 {
		failf("phase=%s/slot=%d id=%d: argument tail pad corrupted after GC", variant, slot, p.id)
	}
	if p.src != int64(slot) {
		failf("phase=%s/slot=%d id=%d: argument slot mismatch src=%d (register/args mapping broken)",
			variant, slot, p.id, p.src)
	}
	return p
}

func newOut(link *rn, srcID int64, slot int64) *rn {
	id := newNodeID()
	p := newRN(gSeed, id, slot)
	p.link = link
	p.src = srcID
	return p
}

func checkOut(out *rn, wantLinkID int64, wantSrc int64, variant string) {
	if out == nil || out.tag != rnTag || out.v != rnV(gSeed, out.id) {
		failf("phase=%s/out: nil or corrupted result", variant)
	}
	if out.pad[3] != out.id*7+3 || out.pad[100] != out.id*11+7 {
		failf("phase=%s/out id=%d: result tail pad corrupted", variant, out.id)
	}
	if out.link == nil || out.link.id != wantLinkID || out.link.v != rnV(gSeed, out.link.id) ||
		out.link.pad[3] != out.link.id*7+3 {
		failf("phase=%s/out: derived link object lost/corrupted (want id=%d)", variant, wantLinkID)
	}
	if out.src != wantSrc {
		failf("phase=%s/out: src=%d want=%d (argument slot correlation broken)", variant, out.src, wantSrc)
	}
}

type worker struct {
	id     int64
	seed   uint64
	m8     reflect.Value
	t8     fn8
	m12    reflect.Value
	t12    fn12
	mv     reflect.Value
	tv     fnVar
	mb     reflect.Value
	m4     reflect.Value
	t4     fn4
	rounds int64
}

func (w *worker) cb8(in []reflect.Value) []reflect.Value {
	runtime.GC()
	atomic.AddInt64(&callGc, 1)
	if len(in) != 8 {
		failf("phase=fn8: callback arg count=%d want=8", len(in))
	}
	ps := make([]*rn, 8)
	for i := 0; i < 6; i++ {
		ps[i] = checkArg(in[i], i, "fn8")
	}
	if in[6].Kind() != reflect.String || in[6].String() != "s8" {
		failf("phase=fn8: string argument corrupted: %v", in[6])
	}
	ps[7] = checkArg(in[7], 7, "fn8")
	return []reflect.Value{reflect.ValueOf(newOut(ps[0], ps[3].id, -1))}
}

func (w *worker) cb12(in []reflect.Value) []reflect.Value {
	runtime.GC()
	atomic.AddInt64(&callGc, 1)
	if len(in) != 12 {
		failf("phase=fn12: callback arg count=%d want=12", len(in))
	}
	ps := make([]*rn, 12)
	for i := range ps {
		ps[i] = checkArg(in[i], i, "fn12")
	}
	return []reflect.Value{reflect.ValueOf(newOut(ps[0], ps[7].id, -1))}
}

func (w *worker) cbVar(in []reflect.Value) []reflect.Value {
	runtime.GC()
	atomic.AddInt64(&callGc, 1)
	if len(in) != 2 {
		failf("phase=variadic: callback arg count=%d want=2 (first + packed slice)", len(in))
	}
	p0 := checkArg(in[0], 0, "variadic")
	rest, ok := in[1].Interface().([]*rn)
	if !ok {
		failf("phase=variadic: packed variadic argument is not []*rn: %v", in[1].Type())
	}
	if len(rest) != 6 {
		failf("phase=variadic: variadic len=%d want=6", len(rest))
	}
	for i, p := range rest {
		if p == nil || p.tag != rnTag || p.v != rnV(gSeed, p.id) || p.pad[3] != p.id*7+3 {
			failf("phase=variadic/slot=%d: variadic element corrupted after GC", i+1)
		}
		if p.src != int64(i+1) {
			failf("phase=variadic/slot=%d id=%d: variadic element slot mismatch src=%d", i+1, p.id, p.src)
		}
	}
	return []reflect.Value{reflect.ValueOf(newOut(rest[2], p0.id, -1))}
}

func (w *worker) cbBig(in []reflect.Value) []reflect.Value {
	runtime.GC()
	atomic.AddInt64(&callGc, 1)
	if len(in) != 4 {
		failf("phase=bigret: callback arg count=%d want=4", len(in))
	}
	ps := make([]*rn, 4)
	for i := range ps {
		ps[i] = checkArg(in[i], i, "bigret")
	}
	e := newOut(ps[3], ps[1].id, -1)
	return []reflect.Value{reflect.ValueOf(bigRet{
		a: ps[0], b: ps[1], c: [3]*rn{ps[2], ps[3], e}, d: int64(4242), e: e,
	})}
}

func (w *worker) cbNested(in []reflect.Value) []reflect.Value {
	runtime.GC()
	atomic.AddInt64(&callGc, 1)
	if len(in) != 2 {
		failf("phase=nested.outer: callback arg count=%d want=2", len(in))
	}
	a := checkArg(in[0], 0, "nested.outer")
	checkArg(in[1], 1, "nested.outer")
	base := newNodeID()
	res := w.m4.Call([]reflect.Value{
		reflect.ValueOf(newRN(gSeed, base, 0)),
		reflect.ValueOf(newRN(gSeed, base+1, 1)),
		reflect.ValueOf(newRN(gSeed, base+2, 2)),
		reflect.ValueOf(newRN(gSeed, base+3, 3)),
	})
	if len(res) != 1 {
		failf("phase=nested.inner: result count=%d want=1", len(res))
	}
	inner := res[0].Interface().(*rn)
	checkOut(inner, base, -1, "nested.inner")
	return []reflect.Value{reflect.ValueOf(newOut(a, inner.id, -1))}
}

func (w *worker) setup() {
	m8 := reflect.MakeFunc(typ8, w.cb8)
	w.m8, w.t8 = m8, m8.Interface().(fn8)
	m12 := reflect.MakeFunc(typ12, w.cb12)
	w.m12, w.t12 = m12, m12.Interface().(fn12)
	mv := reflect.MakeFunc(typVar, w.cbVar)
	w.mv, w.tv = mv, mv.Interface().(fnVar)
	w.mb = reflect.MakeFunc(typBig, w.cbBig)
	m4 := reflect.MakeFunc(typ4, func(in []reflect.Value) []reflect.Value {
		runtime.GC()
		atomic.AddInt64(&callGc, 1)
		if len(in) != 4 {
			failf("phase=nested.inner: callback arg count=%d want=4", len(in))
		}
		ps := make([]*rn, 4)
		for i := range ps {
			ps[i] = checkArg(in[i], i, "nested.inner")
		}
		return []reflect.Value{reflect.ValueOf(newOut(ps[0], ps[2].id, -1))}
	})
	w.m4, w.t4 = m4, m4.Interface().(fn4)
}

// callTyped8：参数在实参位置现场分配，调用返回后不再持有参数指针——GC 落在
// makeFuncStub 帧时，参数的唯一根就是寄存器/RegArgs。
//
//go:noinline
func (w *worker) callTyped8() {
	base := newNodeID()
	out := w.t8(
		newRN(gSeed, base, 0), newRN(gSeed, base+1, 1), newRN(gSeed, base+2, 2),
		newRN(gSeed, base+3, 3), newRN(gSeed, base+4, 4), newRN(gSeed, base+5, 5),
		"s8", newRN(gSeed, base+7, 7))
	checkOut(out, base, base+3, "fn8-typed")
	atomic.AddInt64(&call8T, 1)
}

func (w *worker) callValue8() {
	base := newNodeID()
	vals := []reflect.Value{
		reflect.ValueOf(newRN(gSeed, base, 0)),
		reflect.ValueOf(newRN(gSeed, base+1, 1)),
		reflect.ValueOf(newRN(gSeed, base+2, 2)),
		reflect.ValueOf(newRN(gSeed, base+3, 3)),
		reflect.ValueOf(newRN(gSeed, base+4, 4)),
		reflect.ValueOf(newRN(gSeed, base+5, 5)),
		reflect.ValueOf("s8"),
		reflect.ValueOf(newRN(gSeed, base+7, 7)),
	}
	res := w.m8.Call(vals)
	if len(res) != 1 {
		failf("phase=fn8-call: result count=%d want=1", len(res))
	}
	checkOut(res[0].Interface().(*rn), base, base+3, "fn8-call")
	atomic.AddInt64(&call8V, 1)
}

//go:noinline
func (w *worker) callTyped12() {
	base := newNodeID()
	out := w.t12(
		newRN(gSeed, base, 0), newRN(gSeed, base+1, 1), newRN(gSeed, base+2, 2),
		newRN(gSeed, base+3, 3), newRN(gSeed, base+4, 4), newRN(gSeed, base+5, 5),
		newRN(gSeed, base+6, 6), newRN(gSeed, base+7, 7), newRN(gSeed, base+8, 8),
		newRN(gSeed, base+9, 9), newRN(gSeed, base+10, 10), newRN(gSeed, base+11, 11))
	checkOut(out, base, base+7, "fn12-typed")
	atomic.AddInt64(&call12, 1)
}

//go:noinline
func (w *worker) callVariadic() {
	base := newNodeID()
	out := w.tv(
		newRN(gSeed, base, 0), newRN(gSeed, base+1, 1), newRN(gSeed, base+2, 2),
		newRN(gSeed, base+3, 3), newRN(gSeed, base+4, 4), newRN(gSeed, base+5, 5),
		newRN(gSeed, base+6, 6))
	// 回调以 rest[2] 为 link，p0.id 为 src。
	checkOut(out, base+3, base, "variadic-typed")
	atomic.AddInt64(&callVar, 1)
}

func (w *worker) callBig() {
	base := newNodeID()
	res := w.mb.Call([]reflect.Value{
		reflect.ValueOf(newRN(gSeed, base, 0)),
		reflect.ValueOf(newRN(gSeed, base+1, 1)),
		reflect.ValueOf(newRN(gSeed, base+2, 2)),
		reflect.ValueOf(newRN(gSeed, base+3, 3)),
	})
	if len(res) != 1 {
		failf("phase=bigret: result count=%d want=1", len(res))
	}
	br, ok := res[0].Interface().(bigRet)
	if !ok {
		failf("phase=bigret: result type=%v want bigRet", res[0].Type())
	}
	if br.a == nil || br.a.id != base || br.a.v != rnV(gSeed, base) || br.a.pad[3] != br.a.id*7+3 {
		failf("phase=bigret: field a corrupted after return")
	}
	if br.b == nil || br.b.id != base+1 || br.b.pad[100] != br.b.id*11+7 {
		failf("phase=bigret: field b corrupted after return")
	}
	if br.c[0] == nil || br.c[0].id != base+2 || br.c[1] == nil || br.c[1].id != base+3 ||
		br.c[2] == nil || br.c[2].id != br.e.id || br.e.tag != rnTag {
		failf("phase=bigret: pointer array / e field corrupted after return")
	}
	if br.d != 4242 {
		failf("phase=bigret: scalar field d=%d want=4242", br.d)
	}
	atomic.AddInt64(&callBig, 1)
}

//go:noinline
func (w *worker) callNested() {
	base := newNodeID()
	out := w.t8(newRN(gSeed, base, 0), newRN(gSeed, base+1, 1), newRN(gSeed, base+2, 2),
		newRN(gSeed, base+3, 3), newRN(gSeed, base+4, 4), newRN(gSeed, base+5, 5),
		"s8", newRN(gSeed, base+7, 7))
	if out == nil || out.link == nil || out.tag != rnTag || out.v != rnV(gSeed, out.id) {
		failf("phase=nested.outer: result corrupted after double stub frames")
	}
	if out.link.tag != rnTag || out.link.v != rnV(gSeed, out.link.id) || out.link.pad[3] != out.link.id*7+3 {
		failf("phase=nested.outer: inner-derived link corrupted")
	}
	atomic.AddInt64(&callNest, 1)
}

func (w *worker) round(r int64) {
	w.callTyped8()
	w.callValue8()
	w.callTyped12()
	w.callVariadic()
	w.callBig()
	w.callNested()
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

func main() {
	setupCrashOutput()
	seconds := envInt("STRESS_SECONDS", 60, 0, 86400)
	workers := envInt("STRESS_WORKERS", 2, 1, 8)
	hb := envInt("STRESS_HEARTBEAT", 5, 1, 60)
	seed := envSeed(1)
	fixed := int64(envInt("STRESS_FIXED_ROUNDS", 8, 1, 1000000))
	gSeed = seed

	fmt.Printf("scenario=%s phase=preflight go=%s goos=%s goarch=%s gomaxprocs=%d seed=%d seconds=%d workers=%d fixed_rounds=%d heartbeat=%ds variants=fn8-typed,fn8-call,fn12,variadic,bigret,nested\n",
		scenario, runtime.Version(), runtime.GOOS, runtime.GOARCH, runtime.GOMAXPROCS(0),
		seed, seconds, workers, fixed, hb)

	ws := make([]*worker, workers)
	for i := range ws {
		ws[i] = &worker{id: int64(i), seed: seed*1_000_003 + uint64(i+1)*7919}
		ws[i].setup()
	}

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
	// 每轮每变体恰好一次；nested 回调内额外 Value.Call 一次。
	for _, v := range []struct {
		name string
		p    *int64
	}{
		{"fn8-typed", &call8T}, {"fn8-call", &call8V}, {"fn12", &call12},
		{"variadic", &callVar}, {"bigret", &callBig}, {"nested", &callNest},
	} {
		if got := atomic.LoadInt64(v.p); got != rounds {
			failf("phase=final variant=%s: calls=%d want=%d", v.name, got, rounds)
		}
	}

	ng := waitGoroutines(baseGoroutines, 5*time.Second)
	if ng > baseGoroutines+6 {
		failf("phase=final: goroutines baseline=%d after=%d: goroutine leak", baseGoroutines, ng)
	}

	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	fmt.Printf("metrics: rounds=%d nodes=%d callback_gcs=%d fn8_typed=%d fn8_call=%d fn12=%d variadic=%d bigret=%d nested=%d calls_total=%d gc_cycles=%d num_gc=%d heap_alloc=%d heap_objects=%d goroutines=%d\n",
		rounds, atomic.LoadInt64(&nodesMade), atomic.LoadInt64(&callGc),
		atomic.LoadInt64(&call8T), atomic.LoadInt64(&call8V), atomic.LoadInt64(&call12),
		atomic.LoadInt64(&callVar), atomic.LoadInt64(&callBig), atomic.LoadInt64(&callNest),
		atomic.LoadInt64(&call8T)+atomic.LoadInt64(&call8V)+atomic.LoadInt64(&call12)+
			atomic.LoadInt64(&callVar)+atomic.LoadInt64(&callBig)+atomic.LoadInt64(&callNest),
		atomic.LoadInt64(&gcCycles), m.NumGC, m.HeapAlloc, m.HeapObjects, ng)
	fmt.Printf("STRESS PASS %s\n", scenario)
}
