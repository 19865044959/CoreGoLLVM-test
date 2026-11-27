// Y03: 泛型返回槽（P0，covers L01/L17）。
// Box[T].Get() T 大值返回 × 8 个实参类型（不同尺寸/指针位图，含纯指针、
// 稀疏指针、string 混合），字典（interface itab 派发）与单态化（直接调用）
// 双路径 + 泛型函数返回局部聚合（buildLocal，RVO/拷贝交互）：
//   - 每个叶子三路调用：Box[T].Get() 直接调用、经 getterIface[T] 接口派发
//     （itab/字典路径）、buildLocal[T]（局部聚合返回）——三路返回槽并置在
//     同一叶子帧；返回前 gcGate（槽仍脏）+ 返回后 gcGate 再分字段消费；
//   - 脏栈铺垫：dirtyRec（d=3+i%96 各层帧刷满短命指针 + depth 0 gcGate 清扫）
//     + victimRec 同深度下降（同签名同 carrier T8 → 帧尺寸一致，返回槽落在
//     脏字上）；
//   - 8 类型 × 每轮下降 + weak 强引用探针。
//
// 官方 Go 恒 PASS；崩溃 = 编译器 bug（实例化返回槽未入口清零/字典路径栈图漏标）；
// FAIL canary = 确定性 UAF。
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
	scenario = "y03_generic_ret_slot"

	depthJitter = 96 // d = 3 + i%96
	roundStride = 50_000

	offT1 = 0
	offT2 = 6_000
	offT3 = 12_000
	offT4 = 18_000
	offT5 = 24_000
	offT6 = 30_000
	offT7 = 36_000
	offT8 = 42_000
	offW  = 48_000
)

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

// ---------------------------------------------------------------- 8 个实参类型

// T1：稀疏 3 指针 + 标量/pad 混合（136B）。
type T1 struct {
	A, B *node
	N0   int64
	C    *node
	N1   int64
	W    [2]uint64
	Pad  [10]uint64
}

// T2：纯指针密集（14 指针，112B）。
type T2 struct {
	P [14]*node
}

// T3：16 指针 + words（200B）。
type T3 struct {
	P   [16]*node
	W   [8]uint64
	Seq int64
}

// T4：两端指针 + 大非指针块（272B，稀疏位图）。
type T4 struct {
	A   *node
	Blk [31]uint64
	B   *node
	Seq int64
}

// T5：8 指针 + 4 string（136B，string 位图两 word/项）。
type T5 struct {
	P [8]*node
	S [4]string
	N int64
}

// T6：前后指针组 + 大 buf（288B）。
type T6 struct {
	P   [4]*node
	Buf [28]uint64
	Q   [4]*node
	Seq int64
}

// T7：24 指针 + words（240B）。
type T7 struct {
	P [24]*node
	W [6]uint64
}

// T8：纯指针密集（32 指针，256B）——同时作为下降 carrier。
type T8 struct {
	P [32]*node
}

var (
	gBeats    int64
	gChecks   int64
	gGates    int64
	gGcChurn  int64
	gWeak     int64
	gLeafCalls int64
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

// ---------------------------------------------------------------- fill/check 方法

// fillable[T]：F-bounded 约束——fill 构造并返回 T，check 分字段校验。
type fillable[T any] interface {
	fill(seed uint64, base int64) T
	check(seed uint64, base int64, phase string)
}

func (t T1) fill(seed uint64, base int64) T1 {
	return T1{
		A: newNode(seed, base), B: newNode(seed, base+1),
		N0: base + 10, C: newNode(seed, base+3), N1: base + 11,
		W: [2]uint64{wordPattern(seed, base, 0), wordPattern(seed, base, 1)},
		Pad: padArr(seed, base, 10),
	}
}

func (t T1) check(seed uint64, base int64, phase string) {
	checkNode(t.A, seed, base, phase+".A")
	checkNode(t.B, seed, base+1, phase+".B")
	checkNode(t.C, seed, base+3, phase+".C")
	if t.N0 != base+10 || t.N1 != base+11 {
		failf("phase=%s: N0/N1=%d/%d want=%d/%d", phase, t.N0, t.N1, base+10, base+11)
	}
	if t.W[0] != wordPattern(seed, base, 0) || t.W[1] != wordPattern(seed, base, 1) {
		failf("phase=%s: W 哨兵损坏", phase)
	}
	checkPad(t.Pad[:], seed, base, phase)
	runtime.KeepAlive(&t)
}

func (t T2) fill(seed uint64, base int64) T2 {
	var v T2
	for i := range v.P {
		v.P[i] = newNode(seed, base+int64(i))
	}
	return v
}

func (t T2) check(seed uint64, base int64, phase string) {
	for i := range t.P {
		checkNode(t.P[i], seed, base+int64(i), phase+".P")
	}
	runtime.KeepAlive(&t)
}

func (t T3) fill(seed uint64, base int64) T3 {
	var v T3
	for i := range v.P {
		v.P[i] = newNode(seed, base+int64(i))
	}
	for i := range v.W {
		v.W[i] = wordPattern(seed, base, i)
	}
	v.Seq = base
	return v
}

func (t T3) check(seed uint64, base int64, phase string) {
	for i := range t.P {
		checkNode(t.P[i], seed, base+int64(i), phase+".P")
	}
	for i := range t.W {
		if t.W[i] != wordPattern(seed, base, i) {
			failf("phase=%s: W[%d]=%#x want=%#x", phase, i, t.W[i], wordPattern(seed, base, i))
		}
	}
	if t.Seq != base {
		failf("phase=%s: Seq=%d want=%d", phase, t.Seq, base)
	}
	runtime.KeepAlive(&t)
}

func (t T4) fill(seed uint64, base int64) T4 {
	var v T4
	v.A = newNode(seed, base)
	v.B = newNode(seed, base+1)
	for i := range v.Blk {
		v.Blk[i] = uint64(base)*0x9E37 + uint64(i) + 1
	}
	v.Seq = base
	return v
}

func (t T4) check(seed uint64, base int64, phase string) {
	checkNode(t.A, seed, base, phase+".A")
	checkNode(t.B, seed, base+1, phase+".B")
	if t.Blk[0] != uint64(base)*0x9E37+1 || t.Blk[30] != uint64(base)*0x9E37+31 {
		failf("phase=%s: Blk 哨兵损坏", phase)
	}
	if t.Seq != base {
		failf("phase=%s: Seq=%d want=%d", phase, t.Seq, base)
	}
	runtime.KeepAlive(&t)
}

func (t T5) fill(seed uint64, base int64) T5 {
	var v T5
	for i := range v.P {
		v.P[i] = newNode(seed, base+int64(i))
	}
	for i := range v.S {
		v.S[i] = fmt.Sprintf("y03s%d", base+int64(i))
	}
	v.N = base + 20
	return v
}

func (t T5) check(seed uint64, base int64, phase string) {
	for i := range t.P {
		checkNode(t.P[i], seed, base+int64(i), phase+".P")
	}
	for i := range t.S {
		if t.S[i] != fmt.Sprintf("y03s%d", base+int64(i)) {
			failf("phase=%s: S[%d]=%q want=%q", phase, i, t.S[i], fmt.Sprintf("y03s%d", base+int64(i)))
		}
	}
	if t.N != base+20 {
		failf("phase=%s: N=%d want=%d", phase, t.N, base+20)
	}
	runtime.KeepAlive(&t)
}

func (t T6) fill(seed uint64, base int64) T6 {
	var v T6
	for i := range v.P {
		v.P[i] = newNode(seed, base+int64(i))
	}
	for i := range v.Q {
		v.Q[i] = newNode(seed, base+4+int64(i))
	}
	for i := range v.Buf {
		v.Buf[i] = uint64(base)*0x9E37 + uint64(i) + 1
	}
	v.Seq = base
	return v
}

func (t T6) check(seed uint64, base int64, phase string) {
	for i := range t.P {
		checkNode(t.P[i], seed, base+int64(i), phase+".P")
	}
	for i := range t.Q {
		checkNode(t.Q[i], seed, base+4+int64(i), phase+".Q")
	}
	if t.Buf[0] != uint64(base)*0x9E37+1 || t.Buf[27] != uint64(base)*0x9E37+28 {
		failf("phase=%s: Buf 哨兵损坏", phase)
	}
	if t.Seq != base {
		failf("phase=%s: Seq=%d want=%d", phase, t.Seq, base)
	}
	runtime.KeepAlive(&t)
}

func (t T7) fill(seed uint64, base int64) T7 {
	var v T7
	for i := range v.P {
		v.P[i] = newNode(seed, base+int64(i))
	}
	for i := range v.W {
		v.W[i] = wordPattern(seed, base, i)
	}
	return v
}

func (t T7) check(seed uint64, base int64, phase string) {
	for i := range t.P {
		checkNode(t.P[i], seed, base+int64(i), phase+".P")
	}
	for i := range t.W {
		if t.W[i] != wordPattern(seed, base, i) {
			failf("phase=%s: W[%d]=%#x want=%#x", phase, i, t.W[i], wordPattern(seed, base, i))
		}
	}
	runtime.KeepAlive(&t)
}

func (t T8) fill(seed uint64, base int64) T8 {
	var v T8
	for i := range v.P {
		v.P[i] = newNode(seed, base+int64(i))
	}
	return v
}

func (t T8) check(seed uint64, base int64, phase string) {
	for i := range t.P {
		checkNode(t.P[i], seed, base+int64(i), phase+".P")
	}
	runtime.KeepAlive(&t)
}

func padArr(seed uint64, base int64, n int) [10]uint64 {
	var a [10]uint64
	for i := range a {
		a[i] = uint64(base)*0x9E37 + uint64(i) + 1
	}
	return a
}

func checkPad(p []uint64, seed uint64, base int64, phase string) {
	if p[0] != uint64(base)*0x9E37+1 || p[len(p)-1] != uint64(base)*0x9E37+uint64(len(p)) {
		failf("phase=%s: Pad 哨兵损坏", phase)
	}
}

// ---------------------------------------------------------------- 泛型容器/构造

// Box：泛型容器；Get 按值返回 T（大值返回槽）。
type Box[T any] struct {
	v T
}

//go:noinline
func (b Box[T]) Get() T { return b.v }

// getterIface：实例化的泛型接口——经 itab 派发（字典路径）。
type getterIface[T any] interface {
	Get() T
}

// buildLocal：泛型函数返回局部聚合（RVO/拷贝交互）。
//
//go:noinline
func buildLocal[T fillable[T]](seed uint64, base int64) T {
	var v T
	v = v.fill(seed, base)
	return v
}

// ---------------------------------------------------------------- 脏栈下降

// fillDirtyT8：把 carrier（T8）全部指针刷成短命堆对象指针（帧刷脏）。
func fillDirtyT8(c *T8, seed uint64, base int64) {
	for i := range c.P {
		c.P[i] = newNode(seed, base+int64(i))
	}
}

//go:noinline
func checksumT8(c *T8) int64 {
	var s int64
	for i := range c.P {
		s += c.P[i].v
	}
	return s
}

type leafFn func(seed uint64, base int64, dirty bool) int64

// dirtyRec / victimRec：同签名同 carrier 同深度下降 → 帧尺寸一致，
// 泛型返回槽落在旧 dirtyRec 帧的脏字上。base 全程不变，层内刷脏 id 随 d 推进。
//
//go:noinline
func dirtyRec(seed uint64, base int64, d int, c T8, leaf leafFn) int64 {
	cc := c
	fillDirtyT8(&cc, seed, base+int64(d)*1000)
	s := checksumT8(&cc)
	if d == 0 {
		_ = leaf(seed, base, true)
		gcGate() // 清扫：本帧 carrier + 叶子返回槽 → 悬挂
		return s
	}
	r := dirtyRec(seed, base, d-1, cc, leaf)
	_ = s
	return r
}

//go:noinline
func victimRec(seed uint64, base int64, d int, c T8, leaf leafFn) int64 {
	cc := c
	fillDirtyT8(&cc, seed, base+int64(d)*1000)
	s := checksumT8(&cc)
	if d == 0 {
		return leaf(seed, base, false)
	}
	r := victimRec(seed, base, d-1, cc, leaf)
	_ = s
	return r
}

//go:noinline
func victimGate(seed uint64, base int64, d int, leaf leafFn) int64 {
	dirtyRec(seed, base, d, T8{}, leaf)
	return victimRec(seed, base, d, T8{}, leaf)
}

// ---------------------------------------------------------------- 每类型叶子

// leafT1..leafT8：三路调用——直接（单态化）、接口（itab 派发）、泛型局部聚合；
// 返回前 gcGate（槽仍脏）+ 返回后 gcGate 再分字段消费。
//
//go:noinline
func leafT1(seed uint64, base int64, dirty bool) int64 {
	if !dirty {
		gcGate() // 返回前：槽仍脏
	}
	bv := Box[T1]{v: T1{}.fill(seed, base)}.Get()
	var g getterIface[T1] = Box[T1]{v: T1{}.fill(seed, base)}
	bv2 := g.Get()
	bv3 := buildLocal[T1](seed, base)
	if dirty {
		return 0
	}
	gcGate() // 返回后 GC 再消费
	bv.check(seed, base, "y03.t1.direct")
	bv2.check(seed, base, "y03.t1.iface")
	bv3.check(seed, base, "y03.t1.build")
	return 3
}

//go:noinline
func leafT2(seed uint64, base int64, dirty bool) int64 {
	if !dirty {
		gcGate()
	}
	bv := Box[T2]{v: T2{}.fill(seed, base)}.Get()
	var g getterIface[T2] = Box[T2]{v: T2{}.fill(seed, base)}
	bv2 := g.Get()
	bv3 := buildLocal[T2](seed, base)
	if dirty {
		return 0
	}
	gcGate()
	bv.check(seed, base, "y03.t2.direct")
	bv2.check(seed, base, "y03.t2.iface")
	bv3.check(seed, base, "y03.t2.build")
	return 3
}

//go:noinline
func leafT3(seed uint64, base int64, dirty bool) int64 {
	if !dirty {
		gcGate()
	}
	bv := Box[T3]{v: T3{}.fill(seed, base)}.Get()
	var g getterIface[T3] = Box[T3]{v: T3{}.fill(seed, base)}
	bv2 := g.Get()
	bv3 := buildLocal[T3](seed, base)
	if dirty {
		return 0
	}
	gcGate()
	bv.check(seed, base, "y03.t3.direct")
	bv2.check(seed, base, "y03.t3.iface")
	bv3.check(seed, base, "y03.t3.build")
	return 3
}

//go:noinline
func leafT4(seed uint64, base int64, dirty bool) int64 {
	if !dirty {
		gcGate()
	}
	bv := Box[T4]{v: T4{}.fill(seed, base)}.Get()
	var g getterIface[T4] = Box[T4]{v: T4{}.fill(seed, base)}
	bv2 := g.Get()
	bv3 := buildLocal[T4](seed, base)
	if dirty {
		return 0
	}
	gcGate()
	bv.check(seed, base, "y03.t4.direct")
	bv2.check(seed, base, "y03.t4.iface")
	bv3.check(seed, base, "y03.t4.build")
	return 3
}

//go:noinline
func leafT5(seed uint64, base int64, dirty bool) int64 {
	if !dirty {
		gcGate()
	}
	bv := Box[T5]{v: T5{}.fill(seed, base)}.Get()
	var g getterIface[T5] = Box[T5]{v: T5{}.fill(seed, base)}
	bv2 := g.Get()
	bv3 := buildLocal[T5](seed, base)
	if dirty {
		return 0
	}
	gcGate()
	bv.check(seed, base, "y03.t5.direct")
	bv2.check(seed, base, "y03.t5.iface")
	bv3.check(seed, base, "y03.t5.build")
	return 3
}

//go:noinline
func leafT6(seed uint64, base int64, dirty bool) int64 {
	if !dirty {
		gcGate()
	}
	bv := Box[T6]{v: T6{}.fill(seed, base)}.Get()
	var g getterIface[T6] = Box[T6]{v: T6{}.fill(seed, base)}
	bv2 := g.Get()
	bv3 := buildLocal[T6](seed, base)
	if dirty {
		return 0
	}
	gcGate()
	bv.check(seed, base, "y03.t6.direct")
	bv2.check(seed, base, "y03.t6.iface")
	bv3.check(seed, base, "y03.t6.build")
	return 3
}

//go:noinline
func leafT7(seed uint64, base int64, dirty bool) int64 {
	if !dirty {
		gcGate()
	}
	bv := Box[T7]{v: T7{}.fill(seed, base)}.Get()
	var g getterIface[T7] = Box[T7]{v: T7{}.fill(seed, base)}
	bv2 := g.Get()
	bv3 := buildLocal[T7](seed, base)
	if dirty {
		return 0
	}
	gcGate()
	bv.check(seed, base, "y03.t7.direct")
	bv2.check(seed, base, "y03.t7.iface")
	bv3.check(seed, base, "y03.t7.build")
	return 3
}

//go:noinline
func leafT8(seed uint64, base int64, dirty bool) int64 {
	if !dirty {
		gcGate()
	}
	bv := Box[T8]{v: T8{}.fill(seed, base)}.Get()
	var g getterIface[T8] = Box[T8]{v: T8{}.fill(seed, base)}
	bv2 := g.Get()
	bv3 := buildLocal[T8](seed, base)
	if dirty {
		return 0
	}
	gcGate()
	bv.check(seed, base, "y03.t8.direct")
	bv2.check(seed, base, "y03.t8.iface")
	bv3.check(seed, base, "y03.t8.build")
	return 3
}

type worker struct {
	id     int64
	seed   uint64
	base0  int64
	rounds int64

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

	leaves := 0
	leaves += int(victimGate(w.seed, base+offT1, d, leafT1))
	leaves += int(victimGate(w.seed, base+offT2, d, leafT2))
	leaves += int(victimGate(w.seed, base+offT3, d, leafT3))
	leaves += int(victimGate(w.seed, base+offT4, d, leafT4))
	leaves += int(victimGate(w.seed, base+offT5, d, leafT5))
	leaves += int(victimGate(w.seed, base+offT6, d, leafT6))
	leaves += int(victimGate(w.seed, base+offT7, d, leafT7))
	leaves += int(victimGate(w.seed, base+offT8, d, leafT8))
	if leaves != 8*3 {
		failf("phase=y03.leaf worker=%d round=%d: leaf 调用=%d want=%d", w.id, r, leaves, 8*3)
	}
	atomic.AddInt64(&gLeafCalls, int64(leaves))

	// weak 强引用断言：结果仍被强引用时对象不得被回收。
	probe := T8{}.fill(w.seed, base+offW)
	wp := weak.Make(probe.P[0])
	gcGate()
	v := wp.Value()
	if v == nil {
		failf("phase=y03.weak worker=%d round=%d: 结果仍被强引用却已回收（泛型返回槽漏标）", w.id, r)
	}
	checkNode(v, w.seed, base+offW, "y03.weak")
	atomic.AddInt64(&gWeak, 1)
	probe.check(w.seed, base+offW, "y03.weak.full")

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
	fixed := int64(envInt("STRESS_FIXED_ROUNDS", 30, 1, 1000000))
	gSeed = seed

	fmt.Printf("scenario=%s phase=preflight go=%s goos=%s goarch=%s gomaxprocs=%d seed=%d seconds=%d workers=%d fixed_rounds=%d heartbeat=%ds depth_jitter=%d sizes_t1..t8=%d,%d,%d,%d,%d,%d,%d,%d\n",
		scenario, runtime.Version(), runtime.GOOS, runtime.GOARCH, runtime.GOMAXPROCS(0),
		seed, seconds, workers, fixed, hb, depthJitter,
		reflect.TypeOf(T1{}).Size(), reflect.TypeOf(T2{}).Size(), reflect.TypeOf(T3{}).Size(),
		reflect.TypeOf(T4{}).Size(), reflect.TypeOf(T5{}).Size(), reflect.TypeOf(T6{}).Size(),
		reflect.TypeOf(T7{}).Size(), reflect.TypeOf(T8{}).Size())
	fmt.Printf("scenario=%s phase=preflight note=generic_return_slots; paths=direct,itab_iface,buildLocal; types=8\n", scenario)

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
	if want := rounds * 24; atomic.LoadInt64(&gLeafCalls) != want {
		failf("phase=final: leaf 调用=%d want=%d", atomic.LoadInt64(&gLeafCalls), want)
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
	fmt.Printf("metrics: rounds=%d canary_checks=%d gates=%d gc_churn=%d weak=%d leaf_calls=%d depth_min=%d depth_max=%d depth_avg=%d num_gc=%d heap_alloc=%d heap_objects=%d goroutines_start=%d goroutines_final=%d\n",
		rounds, atomic.LoadInt64(&gChecks), atomic.LoadInt64(&gGates),
		atomic.LoadInt64(&gGcChurn), atomic.LoadInt64(&gWeak), atomic.LoadInt64(&gLeafCalls),
		dmin, dmax, depthTotal/max(rounds, 1),
		m.NumGC, m.HeapAlloc, m.HeapObjects, baseGoroutines, ng)
	fmt.Printf("STRESS PASS %s\n", scenario)
}
