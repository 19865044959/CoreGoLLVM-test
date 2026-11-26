// X01: 聚合 statepoint（P0）。
// 对准报告 A4/A5：FCA 聚合抽取只支持部分形态，嵌套数组在 release 下可能静默 miscompile。
// 每个 worker 每轮以确定性 canary payload 构造聚合，经 //go:noinline 函数：
//   - 返回聚合：[2]*payload、[2][2]*payload、struct{A [2]*payload; B [2][2]*payload}、
//     [1]struct{P *payload}；
//   - 按值传入聚合：同样的 shape + >128B 混合 int/string/slice/iface 大结构；
//   - goret + byval + 普通参数混排；
//   - 8 种泛型实例化 Box[T] 返回聚合。
//
// callee 内 runtime.GC() 后按同 size class 批量分配（迫使复用）再解引用每个指针字段校验
// canary；返回后 caller 再校验一次。每轮末尾用一个 GC 校验 weak.Make 强引用断言：
// 聚合仍被强引用时 weak.Value() 必须非 nil（对象被提前回收 = 漏标/UAF）。
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
	scenario = "x01_aggregate_statepoint"

	// poison 对象的 id 前缀，保证与正常 payload 的 id 空间不相交。
	poisonIDBase = int64(1) << 50

	fixedRoundsDefault = 8
)

// payload 是探针对象：magic 由 (seed,id) 决定，任何回收/复用都会失配。
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

// checkPayload 是探针校验函数：noinline + KeepAlive，避免被优化掉。
//
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

// poisonAlloc 在 GC 之后分配同 size class 对象，迫使已释放槽位被复用。
//
//go:noinline
func poisonAlloc(seed uint64, n int) []*payload {
	seq := atomic.AddInt64(&poisonSeq, 1)
	out := make([]*payload, n)
	for i := range out {
		out[i] = newPayload(seed, poisonIDBase+seq*1_000_000+int64(i))
	}
	return out
}

// ---------- 公共校验（全部 noinline） ----------

//go:noinline
func checkPtr2(seed uint64, id0 int64, p0, p1 *payload, where string) {
	checkPayload(p0, seed, id0, where+".0")
	checkPayload(p1, seed, id0+1, where+".1")
}

//go:noinline
func checkPtr22(seed uint64, id0 int64, b [2][2]*payload, where string) {
	checkPtr2(seed, id0, b[0][0], b[0][1], where+".r0")
	checkPtr2(seed, id0+2, b[1][0], b[1][1], where+".r1")
}

// ---------- 基础聚合 shape ----------

//go:noinline
func retA2(seed uint64, base int64) [2]*payload {
	return [2]*payload{newPayload(seed, base), newPayload(seed, base+1)}
}

//go:noinline
func takeA2(seed uint64, base int64, a [2]*payload) [2]*payload {
	runtime.GC()
	poison := poisonAlloc(seed, 48)
	checkPtr2(seed, base, a[0], a[1], "A2.callee-post-gc")
	runtime.KeepAlive(poison)
	return a
}

//go:noinline
func retB22(seed uint64, base int64) (out [2][2]*payload) {
	for i := 0; i < 2; i++ {
		for j := 0; j < 2; j++ {
			out[i][j] = newPayload(seed, base+int64(i*2+j))
		}
	}
	return out
}

//go:noinline
func takeB22(seed uint64, base int64, b [2][2]*payload) [2][2]*payload {
	runtime.GC()
	poison := poisonAlloc(seed, 48)
	checkPtr22(seed, base, b, "B22.callee-post-gc")
	runtime.KeepAlive(poison)
	return b
}

type agg struct {
	A [2]*payload
	B [2][2]*payload
}

//go:noinline
func checkAgg(seed uint64, base int64, a agg, where string) {
	checkPtr2(seed, base, a.A[0], a.A[1], where+".A")
	checkPtr22(seed, base+10, a.B, where+".B")
	runtime.KeepAlive(&a)
}

//go:noinline
func retAgg(seed uint64, base int64) agg {
	return agg{
		A: [2]*payload{newPayload(seed, base), newPayload(seed, base+1)},
		B: [2][2]*payload{
			{newPayload(seed, base+10), newPayload(seed, base+11)},
			{newPayload(seed, base+12), newPayload(seed, base+13)},
		},
	}
}

//go:noinline
func takeAgg(seed uint64, base int64, a agg) agg {
	runtime.GC()
	poison := poisonAlloc(seed, 48)
	checkAgg(seed, base, a, "agg.callee-post-gc")
	runtime.KeepAlive(poison)
	return a
}

type one struct{ P *payload }

//go:noinline
func checkOne(seed uint64, base int64, a [1]one, where string) {
	checkPayload(a[0].P, seed, base, where+".P")
}

//go:noinline
func retOne(seed uint64, base int64) [1]one {
	return [1]one{{P: newPayload(seed, base)}}
}

//go:noinline
func takeOne(seed uint64, base int64, a [1]one) [1]one {
	runtime.GC()
	poison := poisonAlloc(seed, 48)
	checkOne(seed, base, a, "one.callee-post-gc")
	runtime.KeepAlive(poison)
	return a
}

// ---------- >128B 混合大结构（int/string/slice/iface 混合） ----------

type mixed struct {
	N     int64
	S     string
	B     []*payload
	Iface any
	P     *payload
	A     [2]*payload
	Pad   [8]uint64
}

func mixedString(seed uint64, base int64) string {
	return fmt.Sprintf("mix:%d:%d", seed, base)
}

func newMixed(seed uint64, base int64) mixed {
	m := mixed{
		N:   base,
		S:   mixedString(seed, base),
		B:   []*payload{newPayload(seed, base+20), newPayload(seed, base+21)},
		P:   newPayload(seed, base+24),
		A:   [2]*payload{newPayload(seed, base+25), newPayload(seed, base+26)},
		Pad: [8]uint64{uint64(base), 1, 2, 3, 4, 5, 6, 7},
	}
	if base&1 == 0 {
		m.Iface = [2]*payload{newPayload(seed, base+22), newPayload(seed, base+23)}
	} else {
		m.Iface = newPayload(seed, base+27)
	}
	return m
}

//go:noinline
func checkMixed(seed uint64, base int64, m mixed, where string) {
	if m.N != base {
		failf("%s: mixed.N=%d want=%d", where, m.N, base)
	}
	if m.S != mixedString(seed, base) {
		failf("%s: mixed.S=%q want=%q", where, m.S, mixedString(seed, base))
	}
	if len(m.B) != 2 {
		failf("%s: mixed.B len=%d want=2", where, len(m.B))
	}
	checkPtr2(seed, base+20, m.B[0], m.B[1], where+".B")
	if base&1 == 0 {
		v, ok := m.Iface.([2]*payload)
		if !ok {
			failf("%s: mixed.Iface=%T want [2]*payload", where, m.Iface)
		}
		checkPtr2(seed, base+22, v[0], v[1], where+".iface")
	} else {
		v, ok := m.Iface.(*payload)
		if !ok {
			failf("%s: mixed.Iface=%T want *payload", where, m.Iface)
		}
		checkPayload(v, seed, base+27, where+".iface")
	}
	checkPayload(m.P, seed, base+24, where+".P")
	checkPtr2(seed, base+25, m.A[0], m.A[1], where+".A")
	if m.Pad[0] != uint64(base) || m.Pad[7] != 7 {
		failf("%s: mixed.Pad corrupted: %v", where, m.Pad)
	}
	runtime.KeepAlive(&m)
}

//go:noinline
func takeMixed(seed uint64, base int64, m mixed) mixed {
	runtime.GC()
	poison := poisonAlloc(seed, 48)
	checkMixed(seed, base, m, "mixed.callee-post-gc")
	runtime.KeepAlive(poison)
	return m
}

// ---------- goret + byval + 普通参数混排 ----------

//go:noinline
func mixCall(seed uint64, base int64, tag int64, a [2]*payload, m mixed, s string) (int64, [2]*payload, mixed) {
	runtime.GC()
	poison := poisonAlloc(seed, 48)
	checkPtr2(seed, base, a[0], a[1], "mixCall.callee-post-gc.a")
	checkMixed(seed, base+10, m, "mixCall.callee-post-gc.m")
	if s != mixedString(seed, base+10) {
		failf("mixCall.callee-post-gc: s=%q want=%q", s, mixedString(seed, base+10))
	}
	runtime.KeepAlive(poison)
	return tag + int64(len(s)), a, m
}

// ---------- 泛型 Box[T]：8 种实例化 ----------

type Box[T any] struct {
	V   T
	Tag int64
	Pad [4]uint64
}

//go:noinline
func boxNew[T any](v T, tag int64) Box[T] {
	return Box[T]{V: v, Tag: tag, Pad: [4]uint64{uint64(tag), 1, 2, 3}}
}

//go:noinline
func boxTake[T any](seed uint64, tag int64, b Box[T], verify func(uint64, int64, T, string)) Box[T] {
	runtime.GC()
	poison := poisonAlloc(seed, 32)
	if b.Tag != tag {
		failf("box[%T].callee-post-gc: Tag=%d want=%d", b.V, b.Tag, tag)
	}
	if b.Pad != [4]uint64{uint64(tag), 1, 2, 3} {
		failf("box[%T].callee-post-gc: Pad corrupted: %v", b.V, b.Pad)
	}
	verify(seed, tag, b.V, "box.callee-post-gc")
	runtime.KeepAlive(poison)
	return b
}

func verifyBoxA2(seed uint64, tag int64, v [2]*payload, where string) {
	checkPtr2(seed, tag, v[0], v[1], where)
}

func verifyBoxB22(seed uint64, tag int64, v [2][2]*payload, where string) {
	checkPtr22(seed, tag, v, where)
}

func verifyBoxAgg(seed uint64, tag int64, v agg, where string) {
	checkAgg(seed, tag, v, where)
}

func verifyBoxOne(seed uint64, tag int64, v [1]one, where string) {
	checkOne(seed, tag, v, where)
}

func verifyBoxMixed(seed uint64, tag int64, v mixed, where string) {
	checkMixed(seed, tag, v, where)
}

func verifyBoxPtr(seed uint64, tag int64, v *payload, where string) {
	checkPayload(v, seed, tag, where)
}

func verifyBoxSlice(seed uint64, tag int64, v []*payload, where string) {
	if len(v) != 2 {
		failf("%s: slice len=%d want=2", where, len(v))
	}
	checkPtr2(seed, tag, v[0], v[1], where)
}

func verifyBoxAny(seed uint64, tag int64, v any, where string) {
	p, ok := v.([2]*payload)
	if !ok {
		failf("%s: Box[any].V=%T want [2]*payload", where, v)
	}
	checkPtr2(seed, tag, p[0], p[1], where)
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

	lastWeak   []weak.Pointer[payload]
	lastWeakID []int64
}

func (w *worker) addWeak(wps *[]weak.Pointer[payload], ids *[]int64, p *payload, id int64) {
	*wps = append(*wps, weak.Make(p))
	*ids = append(*ids, id)
}

// round 执行一轮全部 14 种聚合 shape。
//
//go:noinline
func (w *worker) round(r int64) {
	base := w.base0 + r*2000

	var wps []weak.Pointer[payload]
	var wids []int64

	// 结果变量在 round 作用域声明，weak 强引用断言前由 KeepAlive 保活。
	var (
		ra2 [2]*payload
		rm2 mixed
		bx0 Box[[2]*payload]
		bx1 Box[[2][2]*payload]
		bx2 Box[agg]
		bx3 Box[[1]one]
		bx4 Box[mixed]
		bx5 Box[*payload]
		bx6 Box[[]*payload]
		bx7 Box[any]
	)

	// shape 0: [2]*payload 返回 → 按值传入（callee 内 GC）。
	a2 := retA2(w.seed, base+0)
	checkPtr2(w.seed, base+0, a2[0], a2[1], "A2/caller-pre-gc")
	a2 = takeA2(w.seed, base+0, a2)
	checkPtr2(w.seed, base+0, a2[0], a2[1], "A2.caller-post-return")
	w.addWeak(&wps, &wids, a2[0], base+0)
	w.checks += 8
	w.gcN++

	// shape 1: [2][2]*payload。
	b22 := retB22(w.seed, base+100)
	checkPtr22(w.seed, base+100, b22, "B22/caller-pre-gc")
	b22 = takeB22(w.seed, base+100, b22)
	checkPtr22(w.seed, base+100, b22, "B22.caller-post-return")
	w.addWeak(&wps, &wids, b22[0][0], base+100)
	w.checks += 12
	w.gcN++

	// shape 2: struct{A [2]*payload; B [2][2]*payload}。
	ag := retAgg(w.seed, base+200)
	checkAgg(w.seed, base+200, ag, "agg/caller-pre-gc")
	ag = takeAgg(w.seed, base+200, ag)
	checkAgg(w.seed, base+200, ag, "agg.caller-post-return")
	w.addWeak(&wps, &wids, ag.A[0], base+200)
	w.checks += 16
	w.gcN++

	// shape 3: [1]struct{P *payload}。
	on := retOne(w.seed, base+300)
	checkOne(w.seed, base+300, on, "one/caller-pre-gc")
	on = takeOne(w.seed, base+300, on)
	checkOne(w.seed, base+300, on, "one.caller-post-return")
	w.addWeak(&wps, &wids, on[0].P, base+300)
	w.checks += 4
	w.gcN++

	// shape 4: >128B 混合大结构，按值返回 + 按值传入。
	mx := newMixed(w.seed, base+400)
	checkMixed(w.seed, base+400, mx, "mixed/caller-pre-gc")
	mx = takeMixed(w.seed, base+400, mx)
	checkMixed(w.seed, base+400, mx, "mixed.caller-post-return")
	{
		wp := mx.P
		if base&1 == 0 {
			wp = mx.B[0]
		}
		w.addWeak(&wps, &wids, wp, base+400+boolID(base&1 == 0, 20, 24))
	}
	w.checks += 20
	w.gcN++

	// shape 5: goret + byval + 普通参数混排。
	{
		sb := base + 500
		ra := retA2(w.seed, sb)
		rm := newMixed(w.seed, sb+10)
		s := mixedString(w.seed, sb+10)
		checkPtr2(w.seed, sb, ra[0], ra[1], "mixCall/caller-pre-gc.a")
		checkMixed(w.seed, sb+10, rm, "mixCall/caller-pre-gc.m")
		var n int64
		n, ra2, rm2 = mixCall(w.seed, sb, 7, ra, rm, s)
		if n != 7+int64(len(s)) {
			failf("mixCall.caller-post-return: n=%d want=%d", n, 7+len(s))
		}
		checkPtr2(w.seed, sb, ra2[0], ra2[1], "mixCall.caller-post-return.a")
		checkMixed(w.seed, sb+10, rm2, "mixCall.caller-post-return.m")
		w.addWeak(&wps, &wids, ra2[0], sb)
		w.checks += 18
		w.gcN++
	}

	// shape 6..13: 8 种 Box[T] 泛型实例化。
	{
		sb := base + 600
		bx0 = boxNew([2]*payload{newPayload(w.seed, sb), newPayload(w.seed, sb+1)}, sb)
		verifyBoxA2(w.seed, sb, bx0.V, "box1/caller-pre-gc")
		bx0 = boxTake(w.seed, sb, bx0, verifyBoxA2)
		checkPtr2(w.seed, sb, bx0.V[0], bx0.V[1], "box1.caller-post-return")
		w.addWeak(&wps, &wids, bx0.V[0], sb)
		w.checks += 4
		w.gcN++
	}
	{
		sb := base + 700
		bx1 = boxNew(retB22(w.seed, sb), sb)
		verifyBoxB22(w.seed, sb, bx1.V, "box2/caller-pre-gc")
		bx1 = boxTake(w.seed, sb, bx1, verifyBoxB22)
		checkPtr22(w.seed, sb, bx1.V, "box2.caller-post-return")
		w.addWeak(&wps, &wids, bx1.V[0][0], sb)
		w.checks += 6
		w.gcN++
	}
	{
		sb := base + 800
		bx2 = boxNew(retAgg(w.seed, sb), sb)
		verifyBoxAgg(w.seed, sb, bx2.V, "box3/caller-pre-gc")
		bx2 = boxTake(w.seed, sb, bx2, verifyBoxAgg)
		checkAgg(w.seed, sb, bx2.V, "box3.caller-post-return")
		w.addWeak(&wps, &wids, bx2.V.A[0], sb)
		w.checks += 8
		w.gcN++
	}
	{
		sb := base + 900
		bx3 = boxNew(retOne(w.seed, sb), sb)
		verifyBoxOne(w.seed, sb, bx3.V, "box4/caller-pre-gc")
		bx3 = boxTake(w.seed, sb, bx3, verifyBoxOne)
		checkOne(w.seed, sb, bx3.V, "box4.caller-post-return")
		w.addWeak(&wps, &wids, bx3.V[0].P, sb)
		w.checks += 2
		w.gcN++
	}
	{
		sb := base + 1000
		bx4 = boxNew(newMixed(w.seed, sb), sb)
		verifyBoxMixed(w.seed, sb, bx4.V, "box5/caller-pre-gc")
		bx4 = boxTake(w.seed, sb, bx4, verifyBoxMixed)
		checkMixed(w.seed, sb, bx4.V, "box5.caller-post-return")
		w.addWeak(&wps, &wids, bx4.V.P, sb+24)
		w.checks += 10
		w.gcN++
	}
	{
		sb := base + 1100
		bx5 = boxNew(newPayload(w.seed, sb), sb)
		verifyBoxPtr(w.seed, sb, bx5.V, "box6/caller-pre-gc")
		bx5 = boxTake(w.seed, sb, bx5, verifyBoxPtr)
		checkPayload(bx5.V, w.seed, sb, "box6.caller-post-return")
		w.addWeak(&wps, &wids, bx5.V, sb)
		w.checks += 2
		w.gcN++
	}
	{
		sb := base + 1200
		bx6 = boxNew([]*payload{newPayload(w.seed, sb), newPayload(w.seed, sb+1)}, sb)
		verifyBoxSlice(w.seed, sb, bx6.V, "box7/caller-pre-gc")
		bx6 = boxTake(w.seed, sb, bx6, verifyBoxSlice)
		verifyBoxSlice(w.seed, sb, bx6.V, "box7.caller-post-return")
		w.addWeak(&wps, &wids, bx6.V[0], sb)
		w.checks += 4
		w.gcN++
	}
	{
		sb := base + 1300
		bx7 = boxNew(any([2]*payload{newPayload(w.seed, sb), newPayload(w.seed, sb+1)}), sb)
		verifyBoxAny(w.seed, sb, bx7.V, "box8/caller-pre-gc")
		bx7 = boxTake(w.seed, sb, bx7, verifyBoxAny)
		verifyBoxAny(w.seed, sb, bx7.V, "box8.caller-post-return")
		p := bx7.V.([2]*payload)
		w.addWeak(&wps, &wids, p[0], sb)
		w.checks += 4
		w.gcN++
	}

	// 强引用断言：上面全部结果仍由本地变量强引用，GC 后 weak.Value() 必须非 nil。
	runtime.GC()
	w.gcN++
	for i, wp := range wps {
		v := wp.Value()
		if v == nil {
			failf("phase=weak worker=%d round=%d slot=%d id=%d seed=%d: strong ref held but object collected (聚合漏标/UAF)",
				w.id, r, i, wids[i], w.seed)
		}
		checkPayload(v, w.seed, wids[i], "weak-strong-ref")
	}
	atomic.AddInt64(&gWeakChecks, int64(len(wps)))

	// 让编译器把全部聚合活到 weak 校验之后（GC 在 KeepAlive 之前）。
	runtime.KeepAlive(a2)
	runtime.KeepAlive(b22)
	runtime.KeepAlive(ag)
	runtime.KeepAlive(on)
	runtime.KeepAlive(mx)
	runtime.KeepAlive(ra2)
	runtime.KeepAlive(rm2)
	runtime.KeepAlive(bx0)
	runtime.KeepAlive(bx1)
	runtime.KeepAlive(bx2)
	runtime.KeepAlive(bx3)
	runtime.KeepAlive(bx4)
	runtime.KeepAlive(bx5)
	runtime.KeepAlive(bx6)
	runtime.KeepAlive(bx7)

	w.lastWeak = wps
	w.lastWeakID = wids
	w.rounds++
	w.next = r + 1
	atomic.AddInt64(&gBeats, 1)
}

func boolID(c bool, a, b int64) int64 {
	if c {
		return a
	}
	return b
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

	if sz := reflect.TypeOf(mixed{}).Size(); sz <= 128 {
		failf("phase=preflight: mixed size=%d want >128", sz)
	}

	fmt.Printf("scenario=%s phase=preflight go=%s goos=%s goarch=%s gomaxprocs=%d seed=%d seconds=%d workers=%d fixed_rounds=%d heartbeat=%ds shapes=14 generic_instances=8 mixed_size=%d\n",
		scenario, runtime.Version(), runtime.GOOS, runtime.GOARCH, runtime.GOMAXPROCS(0), seed, seconds, workers, fixed, hb,
		reflect.TypeOf(mixed{}).Size())

	// 预热 GC 后取 goroutine 基线。
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

	// quiesce：全量 GC 后再验一次最后一次 weak（此时应已失活，仅记录）。
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
	fmt.Printf("metrics: rounds=%d shape_ops=%d canary_checks=%d worker_gc=%d weak_checks=%d weak_alive_after_drop=%d num_gc=%d heap_alloc=%d heap_objects=%d goroutines_start=%d goroutines_final=%d\n",
		roundsTotal, roundsTotal*14, checksTotal, gcTotal, atomic.LoadInt64(&gWeakChecks),
		aliveAfterDrop, m.NumGC, m.HeapAlloc, m.HeapObjects, baseGoroutines, ng)
	fmt.Printf("STRESS PASS %s\n", scenario)
}
