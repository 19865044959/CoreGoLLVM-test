// Y02: 多返回混合槽（P0，covers L17）。
// 多个大匿名结果并置（(Big,error)、(int,Big,*node)、(Big,Big2)、(Big,Big2,error)），
// 槽布局与 x05/g01 的单结果不同；每个组合带“错误路径提前 return err”变体——
// 提前 return 时其余结果槽在机器层面未初始化，正是潜在 UAF 窗口：
//   - 成功路径：分步写各结果（r1 写 → gcGate → r2 写 → gcGate），穷举
//     “部分结果已写/未写”窗口；
//   - 错误路径：提前 return 前 gcGate（其余结果槽未初始化被扫描窗口），
//     return 后校验各槽零值——槽未清零即现形；
//   - 消费交错：第 1 个结果立刻校验、第 2 个结果先 GC 再校验、第 3 个
//     经 chan 送走后 GC 再收回校验；
//   - 脏栈铺垫：dirtyRec（d=3+i%96 各层帧刷满短命指针 + depth 0 gcGate 清扫）
//     + victimRec 同深度下降（同签名同 carrier → 帧尺寸一致，结果槽落在脏字上）。
//
// 官方 Go 恒 PASS；崩溃 = 编译器 bug（结果槽未入口清零/栈图漏标）；
// FAIL canary/零值 = 确定性 UAF（脏槽被当活指针扫描或槽未清零泄漏脏值）。
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
	scenario = "y02_multiret_slots"

	bigPtrs  = 16
	bigWords = 8
	bigPad   = 40 // Ptrs(16)+Words(8)+Pad(40)=64 words → Tail 落在位图 word1

	big2Ptrs  = 8
	big2Words = 4
	big2Pad   = 12

	depthJitter = 96 // d = 3 + i%96
	roundStride = 50_000

	off1  = 0
	off1t = 6_000
	off2  = 12_000
	off2t = 18_000
	off3  = 24_000
	off3t = 30_000
	off4  = 36_000
	off4t = 42_000
	offW  = 48_000
)

// Big：>128B，17 个指针字段跨两个位图 word（与 y01 同构）。
type Big struct {
	Ptrs  [bigPtrs]*node
	Words [bigWords]uint64
	Pad   [bigPad]uint64
	Tail  *node
	Seq   int64
}

// Big2：不同尺寸/位图形状（8 指针前置 + 尾指针），与 Big 并置时槽布局不同。
type Big2 struct {
	Ptrs  [big2Ptrs]*node
	N0    int64
	N1    int64
	Words [big2Words]uint64
	Pad   [big2Pad]uint64
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

// yErr：错误结果 canary（经 chan 往返时校验 code/seed）。
type yErr struct {
	code int64
	seed uint64
}

func (e yErr) Error() string { return fmt.Sprintf("y02 err code=%d", e.code) }

func mkErr(seed uint64, base int64) error {
	return yErr{code: base + 9999, seed: seed}
}

var (
	gBeats    int64
	gChecks   int64
	gGates    int64
	gGcChurn  int64
	gWeak     int64
	gTrig     int64
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

//go:noinline
func checkBig2(b Big2, seed uint64, base int64, phase string) {
	for i := range b.Ptrs {
		checkNode(b.Ptrs[i], seed, base+int64(i), phase+".P")
	}
	checkNode(b.Tail, seed, base+big2Ptrs, phase+".Tail")
	if b.N0 != base+50 || b.N1 != base+51 {
		failf("phase=%s: N0/N1=%d/%d want=%d/%d", phase, b.N0, b.N1, base+50, base+51)
	}
	for i := range b.Words {
		if b.Words[i] != wordPattern(seed, base, 100+i) {
			failf("phase=%s: Words[%d]=%#x want=%#x", phase, i, b.Words[i], wordPattern(seed, base, 100+i))
		}
	}
	if b.Seq != base {
		failf("phase=%s: Seq=%d want=%d", phase, b.Seq, base)
	}
	runtime.KeepAlive(&b)
}

// checkZeroBig：错误路径返回后其余结果槽必须全零——槽未清零（脏值泄漏）即现形。
//
//go:noinline
func checkZeroBig(b Big, phase string) {
	for i := range b.Ptrs {
		if b.Ptrs[i] != nil {
			failf("phase=%s: Ptrs[%d]=%p want=nil (错误路径结果槽未清零)", phase, i, b.Ptrs[i])
		}
	}
	if b.Tail != nil {
		failf("phase=%s: Tail=%p want=nil (错误路径结果槽未清零)", phase, b.Tail)
	}
	for i := range b.Words {
		if b.Words[i] != 0 {
			failf("phase=%s: Words[%d]=%#x want=0 (错误路径结果槽未清零)", phase, i, b.Words[i])
		}
	}
	if b.Seq != 0 {
		failf("phase=%s: Seq=%d want=0 (错误路径结果槽未清零)", phase, b.Seq)
	}
	runtime.KeepAlive(&b)
}

//go:noinline
func checkZeroBig2(b Big2, phase string) {
	for i := range b.Ptrs {
		if b.Ptrs[i] != nil {
			failf("phase=%s: Big2 Ptrs[%d]=%p want=nil (错误路径结果槽未清零)", phase, i, b.Ptrs[i])
		}
	}
	if b.Tail != nil || b.N0 != 0 || b.N1 != 0 || b.Seq != 0 {
		failf("phase=%s: Big2 非零字段 (错误路径结果槽未清零)", phase)
	}
	for i := range b.Words {
		if b.Words[i] != 0 {
			failf("phase=%s: Big2 Words[%d]=%#x want=0 (错误路径结果槽未清零)", phase, i, b.Words[i])
		}
	}
	runtime.KeepAlive(&b)
}

//go:noinline
func checkErr(e error, seed uint64, base int64, phase string) {
	ye, ok := e.(yErr)
	if !ok {
		failf("phase=%s: err 类型=%T want=yErr", phase, e)
	}
	if ye.code != base+9999 || ye.seed != seed {
		failf("phase=%s: err canary code=%d seed=%d want=%d/%d", phase, ye.code, ye.seed, base+9999, seed)
	}
}

//go:noinline
func checkErrNil(e error, phase string) {
	if e != nil {
		failf("phase=%s: want nil err got %T %v", phase, e, e)
	}
}

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

//go:noinline
func makeBig2(seed uint64, base int64) Big2 {
	var b Big2
	for i := range b.Ptrs {
		b.Ptrs[i] = newNode(seed, base+int64(i))
	}
	b.Tail = newNode(seed, base+big2Ptrs)
	b.N0 = base + 50
	b.N1 = base + 51
	for i := range b.Words {
		b.Words[i] = wordPattern(seed, base, 100+i)
	}
	for i := range b.Pad {
		b.Pad[i] = uint64(base)*0x9E37 + uint64(i) + 1
	}
	b.Seq = base
	return b
}

// ---------------------------------------------------------------- 多返回组合

// c1: (Big, error)。成功路径 r1 写 → GC → return；错误路径提前 return 前 GC。
//
//go:noinline
func c1(seed uint64, base int64, trig bool) (Big, error) {
	if trig {
		gcGate() // 错误路径窗口：Big/error 结果槽未初始化被扫描
		return Big{}, mkErr(seed, base)
	}
	b := makeBig(seed, base)
	gcGate() // r1 已写、error 槽未写窗口
	return b, nil
}

// c2: (int, Big, *node)。标量先写；错误路径提前 return 时其余槽未初始化。
//
//go:noinline
func c2(seed uint64, base int64, trig bool) (int64, Big, *node) {
	n := base + 7
	if trig {
		gcGate() // 错误路径窗口：Big/*node 结果槽未初始化被扫描
		return 0, Big{}, nil
	}
	b := makeBig(seed, base)
	gcGate() // n/Big 已写、*node 槽未写窗口
	p := newNode(seed, base+100)
	return n, b, p
}

// c3: (Big, Big2)。两个大匿名结果并置，分步写 + 中间窗口。
//
//go:noinline
func c3(seed uint64, base int64, trig bool) (Big, Big2) {
	if trig {
		gcGate() // 错误路径窗口：两个大槽未初始化被扫描
		return Big{}, Big2{}
	}
	b := makeBig(seed, base)
	gcGate() // r1 已写、r2 未写窗口
	b2 := makeBig2(seed, base)
	return b, b2
}

// c4: (Big, Big2, error)。三个结果，分步写 + 两个中间窗口。
//
//go:noinline
func c4(seed uint64, base int64, trig bool) (Big, Big2, error) {
	if trig {
		gcGate() // 错误路径窗口：全部结果槽未初始化被扫描
		return Big{}, Big2{}, mkErr(seed, base)
	}
	b := makeBig(seed, base)
	gcGate() // r1 已写、r2/r3 未写窗口
	b2 := makeBig2(seed, base)
	gcGate() // r1/r2 已写、error 槽未写窗口
	return b, b2, nil
}

// fillDirty 把 Big 的全部指针字段刷成指向短命堆对象的指针（帧刷脏）。
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
// 结果槽落在旧 dirtyRec 帧的脏字上。base 全程不变，层内刷脏 id 随 d 推进。
//
//go:noinline
func dirtyRec(w *worker, seed uint64, base int64, d int, c Big, leaf leafFn) int64 {
	cc := c
	fillDirty(&cc, seed, base+int64(d)*1000)
	s := checksum(&cc)
	if d == 0 {
		_ = leaf(w, seed, base, true)
		gcGate() // 清扫：本帧 carrier + 叶子结果槽 → 悬挂
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

// ---------------------------------------------------------------- 消费交错叶子

// leaf1: (Big, error)。r1 立刻校验；r2 经 chan 送走后 GC 再收回校验。
//
//go:noinline
func leaf1(w *worker, seed uint64, base int64, dirty bool) int64 {
	b, e := c1(seed, base, false)
	if dirty {
		return 0
	}
	checkBig(b, seed, base, "y02.c1.early") // r1 立刻校验
	checkErrNil(e, "y02.c1")
	w.che <- e // r2 经 chan 送走
	gcGate()   // 送走后 GC
	e2 := <-w.che
	checkErrNil(e2, "y02.c1.late") // 收回校验
	return 1
}

// leaf1Trig: 错误路径——提前 return err，其余结果槽必须零值。
//
//go:noinline
func leaf1Trig(w *worker, seed uint64, base int64, dirty bool) int64 {
	b, e := c1(seed, base, !dirty) // dirty → 全写刷槽；clean → 错误路径
	if dirty {
		return 0
	}
	checkZeroBig(b, "y02.c1.trig") // 槽未清零即现形
	w.che <- e
	gcGate()
	e2 := <-w.che
	checkErr(e2, seed, base, "y02.c1.trig.err")
	atomic.AddInt64(&gTrig, 1)
	return 1
}

// leaf2: (int, Big, *node)。n 立刻校验；Big 先 GC 再校验；*node 经 chan 送走后 GC 校验。
//
//go:noinline
func leaf2(w *worker, seed uint64, base int64, dirty bool) int64 {
	n, b, p := c2(seed, base, false)
	if dirty {
		return 0
	}
	if n != base+7 {
		failf("phase=y02.c2: n=%d want=%d", n, base+7)
	}
	gcGate() // r2 先 GC 再校验
	checkBig(b, seed, base, "y02.c2.late")
	w.chn <- p // r3 经 chan 送走
	gcGate()
	q := <-w.chn
	checkNode(q, seed, base+100, "y02.c2.chan")
	return 1
}

// leaf2Trig: 错误路径——提前 return，n/Big/*node 槽零值。
//
//go:noinline
func leaf2Trig(w *worker, seed uint64, base int64, dirty bool) int64 {
	n, b, p := c2(seed, base, !dirty)
	if dirty {
		return 0
	}
	if n != 0 || p != nil {
		failf("phase=y02.c2.trig: n=%d p=%p want=0/nil (错误路径结果槽未清零)", n, p)
	}
	checkZeroBig(b, "y02.c2.trig")
	atomic.AddInt64(&gTrig, 1)
	return 1
}

// leaf3: (Big, Big2)。r1 立刻校验；r2 先 GC 再校验。
//
//go:noinline
func leaf3(w *worker, seed uint64, base int64, dirty bool) int64 {
	b, b2 := c3(seed, base, false)
	if dirty {
		return 0
	}
	checkBig(b, seed, base, "y02.c3.early")
	gcGate() // r2 先 GC 再校验
	checkBig2(b2, seed, base, "y02.c3.late")
	return 1
}

// leaf3Trig: 错误路径——两个大槽零值。
//
//go:noinline
func leaf3Trig(w *worker, seed uint64, base int64, dirty bool) int64 {
	b, b2 := c3(seed, base, !dirty)
	if dirty {
		return 0
	}
	checkZeroBig(b, "y02.c3.trig")
	checkZeroBig2(b2, "y02.c3.trig")
	atomic.AddInt64(&gTrig, 1)
	return 1
}

// leaf4: (Big, Big2, error)。r1 立刻；r2 先 GC 再校验；r3 经 chan 送走后 GC 校验。
//
//go:noinline
func leaf4(w *worker, seed uint64, base int64, dirty bool) int64 {
	b, b2, e := c4(seed, base, false)
	if dirty {
		return 0
	}
	checkBig(b, seed, base, "y02.c4.early")
	gcGate()
	checkBig2(b2, seed, base, "y02.c4.late")
	checkErrNil(e, "y02.c4")
	return 1
}

// leaf4Trig: 错误路径——其余槽零值，error 经 chan 往返。
//
//go:noinline
func leaf4Trig(w *worker, seed uint64, base int64, dirty bool) int64 {
	b, b2, e := c4(seed, base, !dirty)
	if dirty {
		return 0
	}
	checkZeroBig(b, "y02.c4.trig")
	checkZeroBig2(b2, "y02.c4.trig")
	w.che <- e
	gcGate()
	e2 := <-w.che
	checkErr(e2, seed, base, "y02.c4.trig.err")
	atomic.AddInt64(&gTrig, 1)
	return 1
}

type worker struct {
	id     int64
	seed   uint64
	base0  int64
	rounds int64
	che    chan error
	chn    chan *node

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

	_ = victimGate(w, w.seed, base+off1, d, leaf1)
	_ = victimGate(w, w.seed, base+off1t, d, leaf1Trig)
	_ = victimGate(w, w.seed, base+off2, d, leaf2)
	_ = victimGate(w, w.seed, base+off2t, d, leaf2Trig)
	_ = victimGate(w, w.seed, base+off3, d, leaf3)
	_ = victimGate(w, w.seed, base+off3t, d, leaf3Trig)
	_ = victimGate(w, w.seed, base+off4, d, leaf4)
	_ = victimGate(w, w.seed, base+off4t, d, leaf4Trig)

	// weak 强引用断言：结果仍被强引用时对象不得被回收。
	probe := makeBig(w.seed, base+offW)
	wp := weak.Make(probe.Ptrs[0])
	gcGate()
	v := wp.Value()
	if v == nil {
		failf("phase=y02.weak worker=%d round=%d: 结果仍被强引用却已回收（结果槽漏标）", w.id, r)
	}
	checkNode(v, w.seed, base+offW, "y02.weak")
	atomic.AddInt64(&gWeak, 1)
	checkBig(probe, w.seed, base+offW, "y02.weak.full")

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
	fixed := int64(envInt("STRESS_FIXED_ROUNDS", 40, 1, 1000000))
	gSeed = seed

	if sz := reflect.TypeOf(Big{}).Size(); sz < 512 {
		failf("phase=preflight: Big size=%d want >=512 (Tail 必须落在位图 word1)", sz)
	}

	fmt.Printf("scenario=%s phase=preflight go=%s goos=%s goarch=%s gomaxprocs=%d seed=%d seconds=%d workers=%d fixed_rounds=%d heartbeat=%ds depth_jitter=%d big_size=%d big2_size=%d\n",
		scenario, runtime.Version(), runtime.GOOS, runtime.GOARCH, runtime.GOMAXPROCS(0),
		seed, seconds, workers, fixed, hb, depthJitter, reflect.TypeOf(Big{}).Size(), reflect.TypeOf(Big2{}).Size())
	fmt.Printf("scenario=%s phase=preflight note=multiret_slots; combos=(Big,err),(int,Big,*node),(Big,Big2),(Big,Big2,err); variants=early_late_chan,trig_early_return\n", scenario)

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
			che:   make(chan error, 4),
			chn:   make(chan *node, 4),
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
		if n := len(w.che); n != 0 {
			failf("phase=final worker=%d: che 残留=%d want=0（叶子内即时收回）", w.id, n)
		}
		if n := len(w.chn); n != 0 {
			failf("phase=final worker=%d: chn 残留=%d want=0（叶子内即时收回）", w.id, n)
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
	if want := rounds * 4; atomic.LoadInt64(&gTrig) != want {
		failf("phase=final: trig 路径=%d want=%d", atomic.LoadInt64(&gTrig), want)
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
	fmt.Printf("metrics: rounds=%d canary_checks=%d gates=%d gc_churn=%d weak=%d trig_paths=%d depth_min=%d depth_max=%d depth_avg=%d num_gc=%d heap_alloc=%d heap_objects=%d goroutines_start=%d goroutines_final=%d\n",
		rounds, atomic.LoadInt64(&gChecks), atomic.LoadInt64(&gGates),
		atomic.LoadInt64(&gGcChurn), atomic.LoadInt64(&gWeak), atomic.LoadInt64(&gTrig),
		dmin, dmax, depthTotal/max(rounds, 1),
		m.NumGC, m.HeapAlloc, m.HeapObjects, baseGoroutines, ng)
	fmt.Printf("STRESS PASS %s\n", scenario)
}
