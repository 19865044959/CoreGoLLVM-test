// X03: 命名返回值 + defer 改写 + RVO（P1）。
// 对准 D19/P0#2：`func f() (r *Big)` + defer 改写命名返回、大 struct 按值返回（拷贝路径）、
// deferreturn 处 GC（defer 闭包内 runtime.GC()）、panic/recover 后命名返回校验、
// 多返回路径（if/else 返回不同对象），并交替 //go:noinline 与可内联版本。
// Big 含 5 个指针来源（A/B/slice 两元素/interface 装箱）；每次 GC 后逐字段校验 canary。
// 探针：canary + weak.Make 强引用断言（命名返回槽仍持有强引用时 Value 必须非 nil）。
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
	scenario           = "x03_namedret_defer_rvo"
	poisonIDBase       = int64(1) << 50
	fixedRoundsDefault = 8
)

type payload struct {
	magic uint64
	id    int64
	aux   int64
	pad   [5]uint64
}

func magicFor(seed uint64, id int64) uint64 {
	x := seed*0x9E3779B97F4A7C15 + uint64(id)*0x2545F4914F6CDD1D + 0x5EED5EED
	x ^= x >> 31
	x *= 0xBF58476D1CE4E5B9
	x ^= x >> 27
	return x
}

func newPayload(seed uint64, id int64) *payload {
	return &payload{magic: magicFor(seed, id), id: id, aux: id ^ 0x5A5A5A5A}
}

//go:noinline
func checkPayload(p *payload, seed uint64, want int64, where string) {
	if p == nil {
		failf("%s: nil payload (want id=%d): UAF/漏标", where, want)
	}
	if p.id != want || p.magic != magicFor(seed, p.id) || p.aux != p.id^0x5A5A5A5A {
		failf("%s: canary mismatch id=%d want=%d magic=%#x want_magic=%#x aux=%#x: UAF/复用",
			where, p.id, want, p.magic, magicFor(seed, p.id), p.aux)
	}
	runtime.KeepAlive(p)
}

var poisonSeq int64

//go:noinline
func poisonAlloc(seed uint64, n int) []*payload {
	seq := atomic.AddInt64(&poisonSeq, 1)
	out := make([]*payload, n)
	for i := range out {
		out[i] = newPayload(seed, poisonIDBase+seq*1_000_000+int64(i))
	}
	return out
}

// Big 含多个指针字段：A/B 两个直接指针、S 两个指针、Iface 装箱指针。
type Big struct {
	A     *payload
	B     *payload
	S     []*payload
	Iface any
	N     int64
	Pad   [10]uint64
}

func newBig(seed uint64, base int64) *Big {
	return &Big{
		A:     newPayload(seed, base),
		B:     newPayload(seed, base+1000),
		S:     []*payload{newPayload(seed, base+2000), newPayload(seed, base+2001)},
		Iface: newPayload(seed, base+3000),
		N:     base,
		Pad:   [10]uint64{uint64(base), 1, 2, 3, 4, 5, 6, 7, 8, 9},
	}
}

func newBigValue(seed uint64, base int64) Big {
	return *newBig(seed, base)
}

//go:noinline
func checkBigWithA(seed uint64, base int64, aWant int64, b *Big, where string) {
	if b == nil {
		failf("%s: nil *Big", where)
	}
	checkPayload(b.A, seed, aWant, where+".A")
	checkPayload(b.B, seed, base+1000, where+".B")
	if len(b.S) != 2 {
		failf("%s: S len=%d want=2", where, len(b.S))
	}
	checkPayload(b.S[0], seed, base+2000, where+".S[0]")
	checkPayload(b.S[1], seed, base+2001, where+".S[1]")
	v, ok := b.Iface.(*payload)
	if !ok {
		failf("%s: Iface=%T want *payload", where, b.Iface)
	}
	checkPayload(v, seed, base+3000, where+".Iface")
	if b.N != base {
		failf("%s: N=%d want=%d", where, b.N, base)
	}
	if b.Pad[0] != uint64(base) || b.Pad[9] != 9 {
		failf("%s: Pad corrupted: %v", where, b.Pad)
	}
	runtime.KeepAlive(b)
}

//go:noinline
func checkBig(seed uint64, base int64, b *Big, where string) {
	checkBigWithA(seed, base, base, b, where)
}

//go:noinline
func checkBigValue(seed uint64, base int64, b Big, where string) {
	checkBigWithA(seed, base, base, &b, where)
}

// ---------- 命名返回 + defer 改写 + deferreturn GC ----------

//go:noinline
func namedRetNI(seed uint64, base int64) (r *Big) {
	defer func() {
		if r == nil || r.A == nil {
			failf("namedRetNI: named result cleared before deferreturn (seed=%d base=%d)", seed, base)
		}
		wp := weak.Make(r.A)
		runtime.GC()
		if wp.Value() == nil {
			failf("namedRetNI: strong ref through named result lost at deferreturn (seed=%d base=%d)", seed, base)
		}
		checkBig(seed, base+1, r, "namedRetNI/deferreturn-post-gc")
	}()
	defer func() {
		checkBig(seed, base, r, "namedRetNI/defer-pre-gc")
		r = newBig(seed, base+1)
		runtime.GC() // deferreturn 处 GC：命名返回槽必须仍被扫描
		runtime.KeepAlive(r)
	}()
	return newBig(seed, base)
}

// 可内联版本（defer 允许内联时，命名返回槽进入 caller 帧）。
func namedRetInline(seed uint64, base int64) (r *Big) {
	defer func() {
		if r == nil || r.A == nil {
			failf("namedRetInline: named result cleared before deferreturn (seed=%d base=%d)", seed, base)
		}
		wp := weak.Make(r.A)
		runtime.GC()
		if wp.Value() == nil {
			failf("namedRetInline: strong ref through named result lost at deferreturn (seed=%d base=%d)", seed, base)
		}
		checkBig(seed, base+1, r, "namedRetInline/deferreturn-post-gc")
	}()
	defer func() {
		checkBig(seed, base, r, "namedRetInline/defer-pre-gc")
		r = newBig(seed, base+1)
		runtime.GC()
		runtime.KeepAlive(r)
	}()
	return newBig(seed, base)
}

// ---------- 命名大 struct 返回 + defer 字段改写（RVO/拷贝路径） ----------

//go:noinline
func namedStructRetNI(seed uint64, base int64) (r Big) {
	defer func() {
		checkBigValue(seed, base, r, "namedStructRetNI/defer-pre-gc")
		r.A = newPayload(seed, base+7)
		wp := weak.Make(r.A)
		runtime.GC()
		if wp.Value() == nil {
			failf("namedStructRetNI: strong ref through named struct result lost at deferreturn (seed=%d base=%d)", seed, base)
		}
		checkBigWithA(seed, base, base+7, &r, "namedStructRetNI/deferreturn-post-gc")
	}()
	r = newBigValue(seed, base)
	return
}

func namedStructRetInline(seed uint64, base int64) (r Big) {
	defer func() {
		checkBigValue(seed, base, r, "namedStructRetInline/defer-pre-gc")
		r.A = newPayload(seed, base+7)
		wp := weak.Make(r.A)
		runtime.GC()
		if wp.Value() == nil {
			failf("namedStructRetInline: strong ref through named struct result lost at deferreturn (seed=%d base=%d)", seed, base)
		}
		checkBigWithA(seed, base, base+7, &r, "namedStructRetInline/deferreturn-post-gc")
	}()
	r = newBigValue(seed, base)
	return
}

// ---------- 大 struct 按值返回 + 按值传入（拷贝路径） ----------

//go:noinline
func retBigValueNI(seed uint64, base int64) Big {
	return newBigValue(seed, base)
}

func retBigValueInline(seed uint64, base int64) Big {
	return newBigValue(seed, base)
}

//go:noinline
func consumeBigValue(seed uint64, base int64, b Big) Big {
	checkBigValue(seed, base, b, "bigByValue/callee-pre-gc")
	wp := weak.Make(b.A)
	runtime.GC()
	poison := poisonAlloc(seed, 64)
	if wp.Value() == nil {
		failf("bigByValue: strong ref through byval copy lost at GC (seed=%d base=%d)", seed, base)
	}
	checkBigValue(seed, base, b, "bigByValue/callee-post-gc")
	runtime.KeepAlive(poison)
	return b
}

// ---------- 多返回路径（if/else 返回不同对象） ----------

//go:noinline
func multiReturnNI(seed uint64, base int64, pick int64) (r *Big) {
	want := base + 1 + (pick & 1)
	defer func() {
		if r == nil || r.A == nil {
			failf("multiReturnNI: named result nil on return path (seed=%d base=%d pick=%d)", seed, base, pick)
		}
		checkBig(seed, want, r, "multiReturnNI/defer-pre-gc")
		wp := weak.Make(r.A)
		runtime.GC()
		if wp.Value() == nil {
			failf("multiReturnNI: strong ref through named result lost at deferreturn (seed=%d base=%d)", seed, base)
		}
		checkBig(seed, want, r, "multiReturnNI/deferreturn-post-gc")
	}()
	if pick&1 == 0 {
		return newBig(seed, base+1)
	}
	return newBig(seed, base+2)
}

func multiReturnInline(seed uint64, base int64, pick int64) (r *Big) {
	want := base + 1 + (pick & 1)
	defer func() {
		if r == nil || r.A == nil {
			failf("multiReturnInline: named result nil on return path (seed=%d base=%d pick=%d)", seed, base, pick)
		}
		checkBig(seed, want, r, "multiReturnInline/defer-pre-gc")
		wp := weak.Make(r.A)
		runtime.GC()
		if wp.Value() == nil {
			failf("multiReturnInline: strong ref through named result lost at deferreturn (seed=%d base=%d)", seed, base)
		}
		checkBig(seed, want, r, "multiReturnInline/deferreturn-post-gc")
	}()
	if pick&1 == 0 {
		return newBig(seed, base+1)
	}
	return newBig(seed, base+2)
}

// ---------- panic/recover 后命名返回校验 ----------

type panicVal struct{ p *payload }

//go:noinline
func panicNamedNI(seed uint64, base int64) (r *Big) {
	defer func() {
		x := recover()
		if x == nil {
			failf("panicNamedNI: recover returned nil (seed=%d base=%d)", seed, base)
		}
		pv, ok := x.(panicVal)
		if !ok {
			failf("panicNamedNI: panic value=%T want panicVal", x)
		}
		if r == nil || r.A == nil {
			failf("panicNamedNI: named result cleared during unwind (seed=%d base=%d)", seed, base)
		}
		checkBig(seed, base+4, r, "panicNamedNI/defer-pre-gc")
		checkPayload(pv.p, seed, base+9, "panicNamedNI/panic-value-pre-gc")
		wp := weak.Make(r.A)
		runtime.GC() // recover 后的 deferreturn 处 GC
		if wp.Value() == nil {
			failf("panicNamedNI: strong ref through named result lost after recover (seed=%d base=%d)", seed, base)
		}
		checkBig(seed, base+4, r, "panicNamedNI/deferreturn-post-gc")
		checkPayload(pv.p, seed, base+9, "panicNamedNI/panic-value-post-gc")
		runtime.KeepAlive(x)
	}()
	r = newBig(seed, base+4)
	panic(panicVal{p: newPayload(seed, base+9)})
}

func panicNamedInline(seed uint64, base int64) (r *Big) {
	defer func() {
		x := recover()
		if x == nil {
			failf("panicNamedInline: recover returned nil (seed=%d base=%d)", seed, base)
		}
		pv, ok := x.(panicVal)
		if !ok {
			failf("panicNamedInline: panic value=%T want panicVal", x)
		}
		if r == nil || r.A == nil {
			failf("panicNamedInline: named result cleared during unwind (seed=%d base=%d)", seed, base)
		}
		checkBig(seed, base+4, r, "panicNamedInline/defer-pre-gc")
		checkPayload(pv.p, seed, base+9, "panicNamedInline/panic-value-pre-gc")
		wp := weak.Make(r.A)
		runtime.GC()
		if wp.Value() == nil {
			failf("panicNamedInline: strong ref through named result lost after recover (seed=%d base=%d)", seed, base)
		}
		checkBig(seed, base+4, r, "panicNamedInline/deferreturn-post-gc")
		checkPayload(pv.p, seed, base+9, "panicNamedInline/panic-value-post-gc")
		runtime.KeepAlive(x)
	}()
	r = newBig(seed, base+4)
	panic(panicVal{p: newPayload(seed, base+9)})
}

// ---------- defer 链依次改写命名返回 ----------

//go:noinline
func deferChainNI(seed uint64, base int64) (r *Big) {
	defer func() {
		if r == nil || r.A == nil {
			failf("deferChainNI: named result nil after defer chain (seed=%d base=%d)", seed, base)
		}
		wp := weak.Make(r.A)
		runtime.GC()
		if wp.Value() == nil {
			failf("deferChainNI: strong ref through named result lost at deferreturn (seed=%d base=%d)", seed, base)
		}
		checkBig(seed, base+2, r, "deferChainNI/deferreturn-post-gc")
	}()
	defer func() {
		checkBig(seed, base+1, r, "deferChain/defer-pre-gc")
		r = newBig(seed, base+2)
		runtime.GC()
		runtime.KeepAlive(r)
	}()
	defer func() {
		r = newBig(seed, base+1)
	}()
	return newBig(seed, base)
}

func deferChainInline(seed uint64, base int64) (r *Big) {
	defer func() {
		if r == nil || r.A == nil {
			failf("deferChainInline: named result nil after defer chain (seed=%d base=%d)", seed, base)
		}
		wp := weak.Make(r.A)
		runtime.GC()
		if wp.Value() == nil {
			failf("deferChainInline: strong ref through named result lost at deferreturn (seed=%d base=%d)", seed, base)
		}
		checkBig(seed, base+2, r, "deferChainInline/deferreturn-post-gc")
	}()
	defer func() {
		checkBig(seed, base+1, r, "deferChain/defer-pre-gc")
		r = newBig(seed, base+2)
		runtime.GC()
		runtime.KeepAlive(r)
	}()
	defer func() {
		r = newBig(seed, base+1)
	}()
	return newBig(seed, base)
}

// ---------- worker ----------

type worker struct {
	id    int64
	seed  uint64
	base0 int64

	next   int64
	rounds int64
	checks int64
	gcN    int64

	lastWeak []weak.Pointer[payload]
}

//go:noinline
func (w *worker) round(r int64) {
	base := w.base0 + r*50000
	pick := int64(w.seed>>3) + r

	// 每个 op 的 id 空间以 4000 为间距（op 内最大偏移 +3000）。
	rN := namedRetNI(w.seed, base+0)
	checkBig(w.seed, base+1, rN, "namedRetNI/caller-post-return")

	rNI := namedRetInline(w.seed, base+4000)
	checkBig(w.seed, base+4001, rNI, "namedRetInline/caller-post-return")

	rS := namedStructRetNI(w.seed, base+8000)
	checkBigWithA(w.seed, base+8000, base+8007, &rS, "namedStructRetNI/caller-post-return")

	rSI := namedStructRetInline(w.seed, base+12000)
	checkBigWithA(w.seed, base+12000, base+12007, &rSI, "namedStructRetInline/caller-post-return")

	bv := retBigValueNI(w.seed, base+16000)
	bv2 := consumeBigValue(w.seed, base+16000, bv)
	checkBigValue(w.seed, base+16000, bv, "bigByValue/caller-original-post-return")
	checkBigValue(w.seed, base+16000, bv2, "bigByValue/caller-returned-post-return")
	bv2.A = newPayload(w.seed, base+16007)
	checkPayload(bv.A, w.seed, base+16000, "bigByValue/copy-independence")

	bvI := retBigValueInline(w.seed, base+20000)
	bvI = consumeBigValue(w.seed, base+20000, bvI)
	checkBigValue(w.seed, base+20000, bvI, "bigByValueInline/caller-post-return")

	mr := multiReturnNI(w.seed, base+24000, pick)
	checkBig(w.seed, base+24001+(pick&1), mr, "multiReturnNI/caller-post-return")

	mrI := multiReturnInline(w.seed, base+28000, pick)
	checkBig(w.seed, base+28001+(pick&1), mrI, "multiReturnInline/caller-post-return")

	pr := panicNamedNI(w.seed, base+32000)
	checkBig(w.seed, base+32004, pr, "panicNamedNI/caller-post-return")

	prI := panicNamedInline(w.seed, base+36000)
	checkBig(w.seed, base+36004, prI, "panicNamedInline/caller-post-return")

	ch := deferChainNI(w.seed, base+40000)
	checkBig(w.seed, base+40002, ch, "deferChainNI/caller-post-return")

	chI := deferChainInline(w.seed, base+44000)
	checkBig(w.seed, base+44002, chI, "deferChainInline/caller-post-return")

	// caller 级强引用断言：命名返回结果跨额外 GC 仍存活。
	wps := []weak.Pointer[payload]{
		weak.Make(rN.A),
		weak.Make(rSI.A),
		weak.Make(bv2.A),
		weak.Make(chI.A),
	}
	wids := []int64{base + 1, base + 12007, base + 16007, base + 44002}
	runtime.GC()
	w.gcN++
	for i, wp := range wps {
		v := wp.Value()
		if v == nil {
			failf("phase=weak worker=%d round=%d slot=%d id=%d seed=%d: strong ref held but object collected (命名返回漏标/UAF)",
				w.id, r, i, wids[i], w.seed)
		}
		checkPayload(v, w.seed, wids[i], "weak-strong-ref")
	}
	atomic.AddInt64(&gWeakChecks, int64(len(wps)))

	runtime.KeepAlive(rN)
	runtime.KeepAlive(rNI)
	runtime.KeepAlive(rS)
	runtime.KeepAlive(rSI)
	runtime.KeepAlive(bv)
	runtime.KeepAlive(bv2)
	runtime.KeepAlive(bvI)
	runtime.KeepAlive(mr)
	runtime.KeepAlive(mrI)
	runtime.KeepAlive(pr)
	runtime.KeepAlive(prI)
	runtime.KeepAlive(ch)
	runtime.KeepAlive(chI)

	w.checks += 100
	w.gcN += 15
	w.lastWeak = wps
	w.rounds++
	w.next = r + 1
	atomic.AddInt64(&gBeats, 1)
}

// ---------- 通用 harness ----------

var (
	gBeats      int64
	gWeakChecks int64
	gStarted    int64
	gFinished   int64
	gSeed       uint64
)

func failf(format string, args ...interface{}) {
	fmt.Printf("FAIL: "+format+"\n", args...)
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
				failf("phase=watchdog seed=%d: no worker progress for %s (beats=%d)", gSeed, time.Since(lastChange), cur)
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
	fixed := int64(envInt("STRESS_FIXED_ROUNDS", fixedRoundsDefault, 1, 1000000))
	gSeed = seed

	if sz := reflect.TypeOf(Big{}).Size(); sz <= 128 {
		failf("phase=preflight: Big size=%d want >128", sz)
	}

	fmt.Printf("scenario=%s phase=preflight go=%s goos=%s goarch=%s gomaxprocs=%d seed=%d seconds=%d workers=%d fixed_rounds=%d heartbeat=%ds ops=12 big_size=%d\n",
		scenario, runtime.Version(), runtime.GOOS, runtime.GOARCH, runtime.GOMAXPROCS(0), seed, seconds, workers, fixed, hb,
		reflect.TypeOf(Big{}).Size())

	runtime.GC()
	baseGoroutines := runtime.NumGoroutine()

	deadline := time.Now().Add(time.Duration(seconds) * time.Second)
	ws := make([]*worker, workers)
	for i := 0; i < workers; i++ {
		ws[i] = &worker{
			id:    int64(i),
			seed:  seed*1_000_003 + uint64(i+1)*7919,
			base0: int64(i) * 1_000_000_000_000,
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

	runtime.GC()
	runtime.GC()
	aliveAfterDrop := 0
	for _, w := range ws {
		for _, wp := range w.lastWeak {
			if wp.Value() != nil {
				aliveAfterDrop++
			}
		}
	}

	if s, f := atomic.LoadInt64(&gStarted), atomic.LoadInt64(&gFinished); s != int64(workers) || f != s {
		failf("phase=final seed=%d: started=%d finished=%d workers=%d", seed, s, f, workers)
	}
	var roundsTotal, checksTotal, gcTotal int64
	for _, w := range ws {
		roundsTotal += w.rounds
		checksTotal += w.checks
		gcTotal += w.gcN
		if seconds == 0 && w.next != fixed {
			failf("phase=final seed=%d worker=%d: rounds=%d want=%d", seed, w.id, w.next, fixed)
		}
	}
	if roundsTotal == 0 {
		failf("phase=final seed=%d: no rounds completed", seed)
	}

	ng := waitGoroutines(baseGoroutines, 5*time.Second)
	if ng > baseGoroutines+4 {
		failf("phase=final seed=%d: goroutines baseline=%d after=%d: goroutine leak", seed, baseGoroutines, ng)
	}

	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	fmt.Printf("metrics: rounds=%d ops=%d canary_checks=%d worker_gc=%d weak_checks=%d weak_alive_after_drop=%d num_gc=%d heap_alloc=%d heap_objects=%d goroutines_start=%d goroutines_final=%d\n",
		roundsTotal, roundsTotal*12, checksTotal, gcTotal, atomic.LoadInt64(&gWeakChecks),
		aliveAfterDrop, m.NumGC, m.HeapAlloc, m.HeapObjects, baseGoroutines, ng)
	fmt.Printf("STRESS PASS %s\n", scenario)
}
