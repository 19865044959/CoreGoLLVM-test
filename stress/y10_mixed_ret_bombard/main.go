// Y10: 混合返回形态组合轰炸（P1，覆盖 L17/L18/L01）。
// 三形态 seed 派生轮换（splitmix64 确定性 PRNG，同 seed 可复现；每 worker 独立 seed）：
//
//	0 = y01 形状：匿名大值返回 func() Big（无命名结果），三个不同帧型调用点，
//	    返回后调用方立刻 GC；dirtyRec 同型下降铺垫（goret 槽落在脏字上）；
//	1 = y03 形状：泛型返回 genReturn[T]/genBox[T].get()，8 种实例化类型
//	    （指针密集/稀疏、位图跨 word、string/slice 混合），callee 内 GC；
//	2 = y05 形状：接口方法返回大值（itab 派发，value/pointer receiver 交替，
//	    大 receiver 拷贝，方法值/方法表达式调用——wrapper 可能尾调用）。
//
// 形态切换时强制 GC + 脏栈铺垫；每轮高频 GC（churn + 形态内 + 轮末 weak 探针）；
// 固定轮数模式断言三形态各 ≥ fixed/4 且与 PRNG 重算计数精确一致。
//
// 官方 Go 的结果槽/泛型/接口派发实现正确 → 恒 PASS；崩溃/漏标仅随 gollvm 出现。
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
	scenario = "y10_mixed_ret_bombard"

	carrierN = 16 // dirtyRec 帧的指针槽数
	genKinds = 8  // 泛型实例化类型数

	fixedRoundsDefault = 60
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

//go:noinline
func checkPayload(p *payload, seed uint64, want int64, where string) {
	if p == nil {
		failf("%s: nil payload (want id=%d): 结果槽漏标/UAF", where, want)
	}
	if p.id != want || p.magic != magicFor(seed, p.id) || p.aux != p.id^0x5A5A5A5A {
		failf("%s: canary mismatch id=%d want=%d magic=%#x aux=%#x: UAF/复用",
			where, p.id, want, p.magic, p.aux)
	}
	atomic.AddInt64(&canaryChecks, 1)
}

// ---------- 大值类型（>128B，超寄存器返回阈值 → goret 内存槽） ----------

// Big：多指针 + 非指针字段混合，位图跨多个 word；384B。
type Big struct {
	P  [6]*payload
	N0 [8]uint64
	Q  [4]*payload
	S  []*payload
	E  interface{}
	N1 [24]uint64
	R  *payload
}

// BigAlt：指针密集（32+16），448B。
type BigAlt struct {
	A [32]*payload
	B [8]uint64
	C [16]*payload
}

// MixPair：位图跨 word 的小型混合，136B。
type MixPair struct {
	P *payload
	N [12]uint64
	Q *payload
	S []*payload
}

// arrPtr：8 指针数组（64B，寄存器/内存返回边界）。
type arrPtr [8]*payload

// strMix：string/slice 混合，64B。
type strMix struct {
	K string
	S []*payload
	P *payload
	N [4]uint64
}

func makeBig(seed uint64, base int64) Big {
	b := Big{
		E: newPayload(seed, base+20),
		R: newPayload(seed, base+21),
	}
	for i := range b.P {
		b.P[i] = newPayload(seed, base+int64(i))
	}
	for i := range b.Q {
		b.Q[i] = newPayload(seed, base+6+int64(i))
	}
	b.S = []*payload{newPayload(seed, base+10), newPayload(seed, base+11), newPayload(seed, base+12)}
	for i := range b.N0 {
		b.N0[i] = uint64(base) + uint64(i)
	}
	for i := range b.N1 {
		b.N1[i] = uint64(base)*2 + uint64(i)
	}
	return b
}

//go:noinline
func checkBig(b Big, seed uint64, base int64, where string) int64 {
	for i := range b.P {
		checkPayload(b.P[i], seed, base+int64(i), where+".P")
	}
	for i := range b.N0 {
		if b.N0[i] != uint64(base)+uint64(i) {
			failf("%s: N0[%d]=%d want=%d（大值返回槽被改写）", where, i, b.N0[i], uint64(base)+uint64(i))
		}
	}
	for i := range b.Q {
		checkPayload(b.Q[i], seed, base+6+int64(i), where+".Q")
	}
	if len(b.S) != 3 {
		failf("%s: S len=%d want=3", where, len(b.S))
	}
	checkPayload(b.S[0], seed, base+10, where+".S0")
	checkPayload(b.S[1], seed, base+11, where+".S1")
	checkPayload(b.S[2], seed, base+12, where+".S2")
	en, ok := b.E.(*payload)
	if !ok {
		failf("%s: E 类型=%T want *payload", where, b.E)
	}
	checkPayload(en, seed, base+20, where+".E")
	for i := range b.N1 {
		if b.N1[i] != uint64(base)*2+uint64(i) {
			failf("%s: N1[%d] 被改写", where, i)
		}
	}
	checkPayload(b.R, seed, base+21, where+".R")
	atomic.AddInt64(&bigChecks, 1)
	runtime.KeepAlive(&b)
	return b.R.id + int64(b.N0[0])
}

func makeBigAlt(seed uint64, base int64) BigAlt {
	var a BigAlt
	for i := range a.A {
		a.A[i] = newPayload(seed, base+int64(i))
	}
	for i := range a.B {
		a.B[i] = uint64(base) + uint64(i)*3
	}
	for i := range a.C {
		a.C[i] = newPayload(seed, base+100+int64(i))
	}
	return a
}

//go:noinline
func checkBigAlt(a BigAlt, seed uint64, base int64, where string) int64 {
	for i := range a.A {
		checkPayload(a.A[i], seed, base+int64(i), where+".A")
	}
	for i := range a.B {
		if a.B[i] != uint64(base)+uint64(i)*3 {
			failf("%s: B[%d] 被改写", where, i)
		}
	}
	for i := range a.C {
		checkPayload(a.C[i], seed, base+100+int64(i), where+".C")
	}
	atomic.AddInt64(&bigChecks, 1)
	runtime.KeepAlive(&a)
	return a.A[0].id + a.C[15].id
}

func makeMixPair(seed uint64, base int64) MixPair {
	return MixPair{
		P: newPayload(seed, base),
		Q: newPayload(seed, base+1),
		S: []*payload{newPayload(seed, base+2), newPayload(seed, base+3)},
	}
}

//go:noinline
func checkMixPair(p MixPair, seed uint64, base int64, where string) int64 {
	checkPayload(p.P, seed, base, where+".P")
	for i := range p.N {
		if p.N[i] != 0 {
			failf("%s: N[%d]=%d want=0（返回槽未清零/被改写）", where, i, p.N[i])
		}
	}
	checkPayload(p.Q, seed, base+1, where+".Q")
	if len(p.S) != 2 {
		failf("%s: S len=%d want=2", where, len(p.S))
	}
	checkPayload(p.S[0], seed, base+2, where+".S0")
	checkPayload(p.S[1], seed, base+3, where+".S1")
	atomic.AddInt64(&bigChecks, 1)
	runtime.KeepAlive(&p)
	return p.P.id + p.Q.id
}

func makeArrPtr(seed uint64, base int64) arrPtr {
	var a arrPtr
	for i := range a {
		a[i] = newPayload(seed, base+int64(i))
	}
	return a
}

//go:noinline
func checkArrPtr(a arrPtr, seed uint64, base int64, where string) int64 {
	for i := range a {
		checkPayload(a[i], seed, base+int64(i), where+".a")
	}
	atomic.AddInt64(&bigChecks, 1)
	runtime.KeepAlive(&a)
	return a[0].id + a[7].id
}

func makeStrMix(seed uint64, base int64) strMix {
	return strMix{
		K: "k-" + strconv.FormatInt(base, 10),
		S: []*payload{newPayload(seed, base+1), newPayload(seed, base+2)},
		P: newPayload(seed, base),
	}
}

//go:noinline
func checkStrMix(s strMix, seed uint64, base int64, where string) int64 {
	if s.K != "k-"+strconv.FormatInt(base, 10) {
		failf("%s: K=%q 被改写", where, s.K)
	}
	if len(s.S) != 2 {
		failf("%s: S len=%d want=2", where, len(s.S))
	}
	checkPayload(s.S[0], seed, base+1, where+".S0")
	checkPayload(s.S[1], seed, base+2, where+".S1")
	checkPayload(s.P, seed, base, where+".P")
	for i := range s.N {
		if s.N[i] != 0 {
			failf("%s: N[%d] 应为 0", where, i)
		}
	}
	atomic.AddInt64(&bigChecks, 1)
	runtime.KeepAlive(&s)
	return s.P.id
}

// ---------- 脏栈制造（同 y01/g01 手法） ----------

//go:noinline
func fillDirty(c *[carrierN]*payload, seed uint64, base int64) {
	for i := range c {
		c[i] = newPayload(seed, base+int64(i))
	}
}

//go:noinline
func checksumDirty(c *[carrierN]*payload) int64 {
	var s int64
	for i := range c {
		s += c[i].id
	}
	return s
}

//go:noinline
func dirtyRec(seed uint64, base int64, d int, carrier [carrierN]*payload) int64 {
	c := carrier
	fillDirty(&c, seed, base+int64(d)*1000)
	sum := checksumDirty(&c)
	if d == 0 {
		runtime.GC()
		atomic.AddInt64(&gcCycles, 1)
		return sum
	}
	r := dirtyRec(seed, base, d-1, c)
	return sum + r
}

// ---------- 形态 0：y01 形状——匿名大值返回，多调用点 ----------

// retAnon：匿名（非命名）结果，>128B 大值经 goret 内存槽返回。
//
//go:noinline
func retAnon(seed uint64, base int64) Big {
	return makeBig(seed, base)
}

// anonSiteA/B/C：三个不同帧型的调用点（pad 尺寸不同 → goret 槽地址不同），
// 同型同深度下降使底部帧（= 旧 dirtyRec 帧）上的返回槽落在脏字上；返回后
// 调用方立刻 GC（x05 命中点形状）。同 site 跨轮复用同一槽地址。
//
//go:noinline
func anonSiteA(seed uint64, base int64, d int, carrier [carrierN]*payload, pad [4]uint64) int64 {
	c := carrier
	fillDirty(&c, seed, base+int64(d)*1000)
	sum := checksumDirty(&c)
	sp := pad[0] ^ pad[3]
	if d == 0 {
		b := retAnon(seed, base)
		runtime.GC() // 调用方立刻 GC
		atomic.AddInt64(&gcCycles, 1)
		return checkBig(b, seed, base, "y10.anon.A") + int64(sp)
	}
	r := anonSiteA(seed, base, d-1, c, pad)
	return sum + r + int64(sp)
}

//go:noinline
func anonSiteB(seed uint64, base int64, d int, carrier [carrierN]*payload, pad [8]uint64) int64 {
	c := carrier
	fillDirty(&c, seed, base+int64(d)*1000)
	sum := checksumDirty(&c)
	sp := pad[2] ^ pad[5]
	if d == 0 {
		b := retAnon(seed, base)
		runtime.GC()
		atomic.AddInt64(&gcCycles, 1)
		return checkBig(b, seed, base, "y10.anon.B") + int64(sp)
	}
	r := anonSiteB(seed, base, d-1, c, pad)
	return sum + r + int64(sp)
}

//go:noinline
func anonSiteC(seed uint64, base int64, d int, carrier [carrierN]*payload, pad [12]uint64) int64 {
	c := carrier
	fillDirty(&c, seed, base+int64(d)*1000)
	sum := checksumDirty(&c)
	sp := pad[7] ^ pad[11]
	if d == 0 {
		b := retAnon(seed, base)
		runtime.GC()
		atomic.AddInt64(&gcCycles, 1)
		return checkBig(b, seed, base, "y10.anon.C") + int64(sp)
	}
	r := anonSiteC(seed, base, d-1, c, pad)
	return sum + r + int64(sp)
}

//go:noinline
func anonGate(seed uint64, base int64, d int, site int64) int64 {
	dirtyRec(seed, base, d, [carrierN]*payload{})
	switch site % 3 {
	case 0:
		return anonSiteA(seed, base, d, [carrierN]*payload{}, [4]uint64{1, 2, 3, 4})
	case 1:
		return anonSiteB(seed, base, d, [carrierN]*payload{}, [8]uint64{5, 6, 7, 8, 9, 10, 11, 12})
	default:
		return anonSiteC(seed, base, d, [carrierN]*payload{}, [12]uint64{13, 14, 15, 16, 17, 18, 19, 20, 21, 22, 23, 24})
	}
}

// ---------- 形态 1：y03 形状——泛型返回（8 种实例化） ----------

// genReturn：泛型函数大值返回；结果写入后的 callee 内 GC（结果区被扫描窗口）。
//
//go:noinline
func genReturn[T any](seed uint64, base int64, mk func(uint64, int64) T) T {
	v := mk(seed, base)
	runtime.GC()
	atomic.AddInt64(&gcCycles, 1)
	return v
}

// genBox.get：泛型方法返回；GC 在返回前（结果槽写入窗口）。
type genBox[T any] struct {
	v T
}

//go:noinline
func (b genBox[T]) get() T {
	runtime.GC()
	atomic.AddInt64(&gcCycles, 1)
	return b.v
}

func mkBig(seed uint64, base int64) Big { return makeBig(seed, base) }
func mkBigAlt(seed uint64, base int64) BigAlt {
	return makeBigAlt(seed, base)
}
func mkMixPair(seed uint64, base int64) MixPair {
	return makeMixPair(seed, base)
}
func mkArrPtr(seed uint64, base int64) arrPtr {
	return makeArrPtr(seed, base)
}
func mkStrMix(seed uint64, base int64) strMix {
	return makeStrMix(seed, base)
}

//go:noinline
func genShape(seed uint64, base int64, kind int) int64 {
	switch kind {
	case 0:
		b := genReturn(seed, base, mkBig)
		runtime.GC()
		atomic.AddInt64(&gcCycles, 1)
		return checkBig(b, seed, base, "y10.gen.Big")
	case 1:
		b := genReturn(seed, base, mkBigAlt)
		runtime.GC()
		atomic.AddInt64(&gcCycles, 1)
		return checkBigAlt(b, seed, base, "y10.gen.BigAlt")
	case 2:
		b := genBox[Big]{v: makeBig(seed, base)}.get()
		runtime.GC()
		atomic.AddInt64(&gcCycles, 1)
		return checkBig(b, seed, base, "y10.gen.BoxBig")
	case 3:
		p := genReturn(seed, base, mkMixPair)
		runtime.GC()
		atomic.AddInt64(&gcCycles, 1)
		return checkMixPair(p, seed, base, "y10.gen.MixPair")
	case 4:
		a := genReturn(seed, base, mkArrPtr)
		runtime.GC()
		atomic.AddInt64(&gcCycles, 1)
		return checkArrPtr(a, seed, base, "y10.gen.ArrPtr")
	case 5:
		b := genBox[BigAlt]{v: makeBigAlt(seed, base)}.get()
		runtime.GC()
		atomic.AddInt64(&gcCycles, 1)
		return checkBigAlt(b, seed, base, "y10.gen.BoxBigAlt")
	case 6:
		s := genReturn(seed, base, mkStrMix)
		runtime.GC()
		atomic.AddInt64(&gcCycles, 1)
		return checkStrMix(s, seed, base, "y10.gen.StrMix")
	default:
		p := genBox[MixPair]{v: makeMixPair(seed, base)}.get()
		runtime.GC()
		atomic.AddInt64(&gcCycles, 1)
		return checkMixPair(p, seed, base, "y10.gen.BoxMixPair")
	}
}

// ---------- 形态 2：y05 形状——接口方法返回大值 ----------

type bigRetter interface {
	retBig(seed uint64, base int64) Big
}

type retValRecv struct{ pad [8]uint64 }

//go:noinline
func (retValRecv) retBig(seed uint64, base int64) Big {
	return makeBig(seed, base)
}

type retPtrRecv struct{ pad [8]uint64 }

//go:noinline
func (*retPtrRecv) retBig(seed uint64, base int64) Big {
	b := makeBig(seed, base)
	runtime.GC()
	atomic.AddInt64(&gcCycles, 1)
	return b
}

// bigCarrier：大 receiver 拷贝（receiver 本身含 384B Big + tag）。
type bigCarrier struct {
	b   Big
	tag int64
}

//go:noinline
func (c bigCarrier) ret(seed uint64, base int64) Big {
	runtime.GC()
	atomic.AddInt64(&gcCycles, 1)
	if c.tag != base {
		failf("y10.iface: receiver tag=%d want=%d（大 receiver 拷贝被改写）", c.tag, base)
	}
	return c.b
}

//go:noinline
func ifaceShape(seed uint64, base int64, variant int) int64 {
	var r bigRetter
	if variant%2 == 0 {
		r = retValRecv{}
	} else {
		r = &retPtrRecv{}
	}
	b := r.retBig(seed, base) // itab 派发（wrapper 可能尾调用）
	runtime.GC()
	atomic.AddInt64(&gcCycles, 1)
	sum := checkBig(b, seed, base, "y10.iface.dispatch")

	c := bigCarrier{b: makeBig(seed, base+1_000), tag: base + 1_000}
	var b2 Big
	switch variant % 3 {
	case 0:
		b2 = bigCarrier.ret(c, seed, base+1_000) // 方法表达式
	case 1:
		b2 = c.ret(seed, base+1_000) // 方法调用（大 receiver 拷贝）
	default:
		fv := c.ret // 绑定方法值（wrapper）
		b2 = fv(seed, base+1_000)
	}
	runtime.GC()
	atomic.AddInt64(&gcCycles, 1)
	return sum + checkBig(b2, seed, base+1_000, "y10.iface.bigrecv")
}

// ---------- 轮换 + worker ----------

// shapeNext：splitmix64 确定性轮换（seed 派生，同 seed 可复现）。
func shapeNext(seed uint64, r int64) int {
	x := seed ^ uint64(r)*0x9E3779B97F4A7C15
	x = (x ^ (x >> 30)) * 0xBF58476D1CE4E5B9
	x ^= x >> 27
	return int(x % 3)
}

type worker struct {
	id     int64
	seed   uint64
	base0  int64
	rounds int64

	lastShape int
	cAnon     int64
	cGen      int64
	cIface    int64

	depthSum int64
	depthMin int
	depthMax int
}

//go:noinline
func (w *worker) round(r int64) {
	base := w.base0 + r*100_000
	shape := shapeNext(w.seed, r)
	if w.lastShape >= 0 && shape != w.lastShape {
		// 形态切换：强制 GC + 脏栈铺垫。
		runtime.GC()
		atomic.AddInt64(&gcCycles, 1)
		dirtyRec(w.seed, base, 8+int(r%24), [carrierN]*payload{})
	}
	w.lastShape = shape

	var sum int64
	switch shape {
	case 0: // y01 形状：匿名大值返回（3 个调用点轮换）
		d := 16 + int(r%48)
		w.depthSum += int64(d)
		if w.depthMin == 0 || d < w.depthMin {
			w.depthMin = d
		}
		if d > w.depthMax {
			w.depthMax = d
		}
		sum = anonGate(w.seed, base, d, r)
		w.cAnon++
	case 1: // y03 形状：泛型返回（8 种实例化轮换）
		sum = genShape(w.seed, base, int(r%genKinds))
		w.cGen++
	default: // y05 形状：接口方法返回大值
		sum = ifaceShape(w.seed, base, int(r))
		w.cIface++
	}
	runtime.GC() // 高频：每轮 GC
	atomic.AddInt64(&gcCycles, 1)

	// weak 强引用探针：结果仍由本地强引用时 GC 后必须非 nil。
	anchor := newPayload(w.seed, base+50_000)
	wk := weak.Make(anchor)
	runtime.GC()
	atomic.AddInt64(&gcCycles, 1)
	if v := wk.Value(); v == nil {
		failf("y10.weak worker=%d round=%d shape=%d: strong ref held but object collected（漏标/UAF）",
			w.id, r, shape)
	} else {
		checkPayload(v, w.seed, base+50_000, "y10.weak-strong-ref")
	}
	atomic.AddInt64(&weakChecks, 1)
	runtime.KeepAlive(anchor)
	runtime.KeepAlive(wk)

	w.rounds++
	atomic.AddInt64(&beats, 1)
	if sum == 0 {
		failf("y10.round worker=%d round=%d: 校验和为 0", w.id, r)
	}
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

// ---------- 通用 harness ----------

var (
	beats        int64
	gcCycles     int64
	started      int64
	finished     int64
	gSeed        uint64
	canaryChecks int64
	bigChecks    int64
	weakChecks   int64
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
		// window（GOMAXPROCS=1+STW）下无休眠 GC 循环会饿死 worker/watchdog。
		if runtime.GOMAXPROCS(0) == 1 {
			runtime.Gosched()
			time.Sleep(time.Millisecond) // 节奏控制，非断言
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
				failf("phase=watchdog: 6 个心跳周期无进展 (%s, beats=%d)", time.Since(lastChange), cur)
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
	seconds := envInt("STRESS_SECONDS", 60, 0, 86400)
	workers := envInt("STRESS_WORKERS", 4, 1, 16)
	hb := envInt("STRESS_HEARTBEAT", 5, 1, 60)
	seed := envSeed(1)
	fixed := int64(envInt("STRESS_FIXED_ROUNDS", fixedRoundsDefault, 1, 1000000))
	gSeed = seed

	fmt.Printf("scenario=%s phase=preflight go=%s goos=%s goarch=%s gomaxprocs=%d seed=%d seconds=%d workers=%d fixed_rounds=%d heartbeat=%ds gen_kinds=%d\n",
		scenario, runtime.Version(), runtime.GOOS, runtime.GOARCH, runtime.GOMAXPROCS(0),
		seed, seconds, workers, fixed, hb, genKinds)

	runtime.GC()
	baseGoroutines := runtime.NumGoroutine()
	deadline := time.Now().Add(time.Duration(seconds) * time.Second)
	ws := make([]*worker, workers)
	for i := 0; i < workers; i++ {
		ws[i] = &worker{
			id:        int64(i),
			seed:      seed*1_000_003 + uint64(i+1)*7919,
			base0:     int64(i) * 1_000_000_000_000,
			lastShape: -1,
		}
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
			atomic.AddInt64(&started, 1)
			defer func() {
				atomic.AddInt64(&finished, 1)
				wg.Done()
			}()
			w.run(seconds, deadline, fixed)
		}(w)
	}
	wg.Wait()
	close(stop)
	aux.Wait()

	if s, f := atomic.LoadInt64(&started), atomic.LoadInt64(&finished); s != int64(workers) || f != s {
		failf("phase=final: started=%d finished=%d workers=%d", s, f, workers)
	}
	var rounds, aTot, gTot, iTot int64
	for _, w := range ws {
		rounds += w.rounds
		aTot += w.cAnon
		gTot += w.cGen
		iTot += w.cIface
		if seconds == 0 && w.rounds != fixed {
			failf("phase=final worker=%d: rounds=%d want=%d", w.id, w.rounds, fixed)
		}
		// 固定轮数：与 PRNG 重算计数精确一致（轮换确定性），三形态各 ≥ fixed/4。
		if seconds == 0 {
			var e [3]int64
			for r := int64(0); r < fixed; r++ {
				e[shapeNext(w.seed, r)]++
			}
			if w.cAnon != e[0] || w.cGen != e[1] || w.cIface != e[2] {
				failf("phase=final worker=%d: 形态计数 anon=%d gen=%d iface=%d want=%d/%d/%d（轮换被破坏）",
					w.id, w.cAnon, w.cGen, w.cIface, e[0], e[1], e[2])
			}
			// 小轮数时 shapeNext 分布可能不均：阈值 min(1, fixed/4)——
			// 固定轮数≥12 时才要求三形态各 ≥ fixed/4。
			need := fixed / 4
			if fixed < 12 {
				need = 0 // 小轮数 PRNG 分布不保证三形态；大轮数才做覆盖断言
			}
			if e[0] < need || e[1] < need || e[2] < need {
				failf("phase=final worker=%d: 三形态覆盖不足 anon=%d gen=%d iface=%d want 各≥%d",
					w.id, e[0], e[1], e[2], need)
			}
		}
	}
	if rounds == 0 {
		failf("phase=final: 没有完成任何轮次")
	}
	if aTot == 0 || gTot == 0 || iTot == 0 {
		failf("phase=final: 形态覆盖缺失 anon=%d gen=%d iface=%d", aTot, gTot, iTot)
	}
	if want := rounds; atomic.LoadInt64(&weakChecks) != want {
		failf("phase=final: weak 探针=%d want=%d", atomic.LoadInt64(&weakChecks), want)
	}
	if atomic.LoadInt64(&canaryChecks) == 0 || atomic.LoadInt64(&bigChecks) == 0 {
		failf("phase=final: 没有执行 canary/big 校验 canary=%d big=%d",
			atomic.LoadInt64(&canaryChecks), atomic.LoadInt64(&bigChecks))
	}

	ng := waitGoroutines(baseGoroutines, 5*time.Second)
	if ng > baseGoroutines+4 {
		failf("phase=final: goroutines baseline=%d after=%d: goroutine 泄漏", baseGoroutines, ng)
	}

	var dmin, dmax int
	var depthTotal int64
	for _, w := range ws {
		depthTotal += w.depthSum
		if dmin == 0 || w.depthMin < dmin {
			dmin = w.depthMin
		}
		if w.depthMax > dmax {
			dmax = w.depthMax
		}
	}
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	fmt.Printf("metrics: rounds=%d anon=%d generic=%d iface=%d canary_checks=%d big_checks=%d weak=%d depth_min=%d depth_max=%d depth_avg=%d gc_churn=%d num_gc=%d heap_alloc=%d heap_objects=%d goroutines=%d\n",
		rounds, aTot, gTot, iTot, atomic.LoadInt64(&canaryChecks), atomic.LoadInt64(&bigChecks),
		atomic.LoadInt64(&weakChecks), dmin, dmax, depthTotal/max(rounds, 1),
		atomic.LoadInt64(&gcCycles), m.NumGC, m.HeapAlloc, m.HeapObjects, ng)
	fmt.Printf("STRESS PASS %s\n", scenario)
}
