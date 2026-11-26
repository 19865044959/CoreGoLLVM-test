// X13: safepoint 矩阵（P0，方法论核心场景）。
// 目标：把“偶现”变成“对每个 step 边界必测一次”——12 种 IR 形态 × 操作脚本 × 穷举注入。
//
// 方法：
//   - 12 个 shape 各代表一种 IR 形态：FCA 传参 / 局部数组取址 / 闭包捕获 / 深递归 /
//     defer 链 / 接口派发 / 泛型实例 / chan 交接 / unsafe 内部指针 / 大 byval /
//     命名返回+RVO / 尾调用 wrapper；
//   - 每个 shape 的操作序列拆成 5 步，每步是一个 noinline closure；6 个边界（step 前/后）
//     先单点全扫（i=0..5），再扫有界双点（i<j）——每点独立重建 state 后运行；
//   - 每个注入点调 runtime.GC()；GOMAXPROCS 强制 1，并对每个注入点加全局串行锁，
//     保证任意时刻只有一个 shape 在跑，GC 点严格等于枚举点；
//   - 每步后校验全部 anchor 的 canary 与 weak.Pointer（持强引用被回收 = FAIL）；
//   - 失败行打印 (shape, step_i, step_j, seed, gcCount)；metrics 打印
//     injection_points_covered / injection_points_total，未扫完整矩阵不得 PASS；
//   - 固定轮数 = 完整扫一轮矩阵（1 轮 = 12 shape × 21 点 = 252 点，注入 GC 432 次），
//     远低于 5000 点上限。
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
	"weak"
)

const scenario = "x13_safepoint_matrix"

const (
	numShapes    = 12
	stepsPerShap = 5
	boundaries   = stepsPerShap + 1
	doubles      = boundaries * (boundaries - 1) / 2
	pointsShape  = boundaries + doubles
	totalPoints  = numShapes * pointsShape
)

const (
	shFCA = iota
	shLocalArray
	shClosure
	shRecursion
	shDefer
	shIface
	shGeneric
	shChan
	shUnsafe
	shByval
	shRVO
	shTail
)

var shapeNames = [numShapes]string{
	"fca_args",
	"local_array_addr",
	"closure_capture",
	"deep_recursion",
	"defer_chain",
	"iface_dispatch",
	"generic_inst",
	"chan_handoff",
	"unsafe_interior",
	"large_byval",
	"namedret_rvo",
	"tailcall_wrapper",
}

type mnode struct {
	id   int64
	v    int64
	next *mnode
	alt  *mnode
	pad  [2]uint64
}

func mnodeV(seed uint64, id int64) int64 {
	return int64(seed*0x9E3779B97F4A7C15^uint64(id)*0xBF58476D1CE4E5B9) | 1
}

var (
	gSeed   uint64
	curName string
	curI    int
	curJ    int
	curRnd  int64
	gcCount int64
	beats   int64
	started int64
	done    int64
)

func failf(format string, args ...interface{}) {
	fmt.Printf("FAIL: shape=%s step_i=%d step_j=%d seed=%d gcCount=%d round=%d: "+format+"\n",
		append([]interface{}{curName, curI, curJ, gSeed, atomic.LoadInt64(&gcCount), curRnd}, args...)...)
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

// checkNode 只读取 canary，不依赖字段地址；//go:noinline 防止读取被优化。
//
//go:noinline
func checkNode(seed uint64, n *mnode, where string) {
	if n == nil {
		failf("%s: nil *mnode", where)
	}
	if n.v != mnodeV(seed, n.id) {
		failf("%s id=%d: canary=%#x want=%#x (UAF/漏标)", where, n.id, n.v, mnodeV(seed, n.id))
	}
}

//go:noinline
func injectPoint(shape, i, j int) {
	atomic.AddInt64(&gcCount, 1)
	runtime.GC()
	runtime.KeepAlive(shape)
	runtime.KeepAlive(i)
	runtime.KeepAlive(j)
}

// ---------- state ----------

type padHolder struct {
	pad [4]*mnode
	obj mnode
	n   int64
}

type bigAgg struct {
	P   [4]*mnode
	Q   [3]*mnode
	S   string
	I   any
	N   int64
	Pad [8]uint64
}

type rvoBig struct {
	P   [3]*mnode
	S   string
	N   int64
	Pad [4]uint64
}

type shapeState struct {
	shape int
	seed  uint64
	round int64

	nodes []*mnode
	weaks []weak.Pointer[mnode]
	acc   int64

	base [3]*mnode

	// shape 专用
	arr    [5]*mnode
	arr22  [2][2]*mnode
	agg    bigAgg
	fca    fcaAgg
	rvo    rvoBig
	ch     chan *mnode
	holder *padHolder
	ip     unsafe.Pointer
	ipp    *mnode
	cl     func() int64
	slot   *mnode
	iface  shapeIface
	anyv   any
	last   *mnode
	fn     func(*mnode) int64
}

func (st *shapeState) track(ns ...*mnode) {
	for _, n := range ns {
		if n == nil {
			failf("phase=track: nil node")
		}
		st.nodes = append(st.nodes, n)
		st.weaks = append(st.weaks, weak.Make(n))
	}
}

func (st *shapeState) mk(k int) *mnode {
	id := int64(1000 + st.shape*100 + k)
	n := &mnode{id: id, v: mnodeV(st.seed, id)}
	st.track(n)
	return n
}

func newShapeState(shape int, seed uint64, round int64) *shapeState {
	st := &shapeState{shape: shape, seed: seed, round: round}
	st.base[0] = st.mk(0)
	st.base[1] = st.mk(1)
	st.base[2] = st.mk(2)
	switch shape {
	case shChan:
		st.ch = make(chan *mnode, 4)
	case shUnsafe:
		st.holder = &padHolder{pad: [4]*mnode{st.base[0], st.base[1], st.base[2], st.mk(3)}}
		st.holder.obj = mnode{id: 7901, v: mnodeV(seed, 7901)}
		st.ip = unsafe.Pointer(&st.holder.obj)
	}
	return st
}

func (st *shapeState) verify(step int) {
	for i, n := range st.nodes {
		if n == nil {
			failf("after step=%d node=%d: nil anchor", step, i)
		}
		if n.v != mnodeV(st.seed, n.id) {
			failf("after step=%d node=%d id=%d: canary=%#x want=%#x (UAF/漏标)",
				step, i, n.id, n.v, mnodeV(st.seed, n.id))
		}
		if st.weaks[i].Value() == nil {
			failf("after step=%d node=%d id=%d: weak.Value()==nil while strongly anchored (活对象被回收)", step, i, n.id)
		}
	}
	if st.shape == shUnsafe {
		if st.ip != nil {
			checkNode(st.seed, (*mnode)(st.ip), "unsafe.ip")
		}
		if st.ipp != nil {
			checkNode(st.seed, st.ipp, "unsafe.ipp")
		}
	}
	switch st.shape {
	case shChan:
		if len(st.ch) > cap(st.ch) {
			failf("after step=%d: chan len=%d cap=%d", step, len(st.ch), cap(st.ch))
		}
	}
}

// ---------- 每步一个 noinline closure ----------

func (st *shapeState) steps() []func() {
	return []func(){
		func() { st.step(0) },
		func() { st.step(1) },
		func() { st.step(2) },
		func() { st.step(3) },
		func() { st.step(4) },
	}
}

//go:noinline
func (st *shapeState) step(i int) {
	switch st.shape {
	case shFCA:
		st.fcaStep(i)
	case shLocalArray:
		st.localArrayStep(i)
	case shClosure:
		st.closureStep(i)
	case shRecursion:
		st.recursionStep(i)
	case shDefer:
		st.deferStep(i)
	case shIface:
		st.ifaceStep(i)
	case shGeneric:
		st.genericStep(i)
	case shChan:
		st.chanStep(i)
	case shUnsafe:
		st.unsafeStep(i)
	case shByval:
		st.byvalStep(i)
	case shRVO:
		st.rvoStep(i)
	case shTail:
		st.tailStep(i)
	default:
		failf("unknown shape=%d", st.shape)
	}
}

// ===== shape 0: FCA 传参 =====

type fcaAgg struct {
	A [2]*mnode
	B [2][2]*mnode
	N int64
	S string
	I any
}

//go:noinline
func fcaTake2(seed uint64, a [2]*mnode) int64 {
	var r int64
	for _, n := range a {
		checkNode(seed, n, "fca.take2")
		r ^= n.v
	}
	return r
}

//go:noinline
func fcaTakeAgg(seed uint64, g fcaAgg) int64 {
	var r int64
	for _, n := range g.A {
		checkNode(seed, n, "fca.takeAgg.A")
		r ^= n.v
	}
	for _, row := range g.B {
		for _, n := range row {
			checkNode(seed, n, "fca.takeAgg.B")
			r ^= n.v
		}
	}
	runtime.KeepAlive(g)
	return r ^ g.N ^ int64(len(g.S))
}

//go:noinline
func fcaRet(seed uint64, base int64) ([2][2]*mnode, int64) {
	var m [2][2]*mnode
	var r int64
	for i := 0; i < 2; i++ {
		for j := 0; j < 2; j++ {
			id := base + int64(i*2+j)
			m[i][j] = &mnode{id: id, v: mnodeV(seed, id)}
			r ^= m[i][j].v
		}
	}
	return m, r
}

func (st *shapeState) fcaStep(i int) {
	switch i {
	case 0:
		a := [2]*mnode{st.base[0], st.base[1]}
		st.acc ^= fcaTake2(st.seed, a)
	case 1:
		g := fcaAgg{
			A: [2]*mnode{st.base[1], st.base[2]},
			B: [2][2]*mnode{{st.base[0], st.base[1]}, {st.base[2], st.mk(3)}},
		}
		st.fca = g
		st.acc ^= fcaTakeAgg(st.seed, g)
	case 2:
		g := st.fca
		g.N = 7
		g.S = "fca"
		g.I = st.base[0]
		st.fca = g
		st.acc ^= fcaTakeAgg(st.seed, g)
	case 3:
		m, r := fcaRet(st.seed, 3000)
		for _, row := range m {
			for _, n := range row {
				st.track(n)
			}
		}
		st.arr22 = m
		st.acc ^= r
	case 4:
		g := fcaAgg{
			A: [2]*mnode{st.arr22[1][1], st.arr22[0][0]},
			B: st.arr22,
			N: 9,
		}
		st.acc ^= fcaTakeAgg(st.seed, g)
	}
}

// ===== shape 1: 局部数组取址 =====

//go:noinline
func localTake(p *mnode, seed uint64) int64 {
	checkNode(seed, p, "local.take")
	return p.v
}

//go:noinline
func localArrSum(a *[5]*mnode, seed uint64) int64 {
	var r int64
	for _, n := range a {
		checkNode(seed, n, "local.arr")
		r ^= n.v
	}
	runtime.KeepAlive(a)
	return r
}

func (st *shapeState) localArrayStep(i int) {
	switch i {
	case 0:
		for k := 0; k < len(st.arr); k++ {
			st.arr[k] = st.mk(10 + k)
		}
	case 1:
		idx := int(st.seed % uint64(len(st.arr)))
		p := &st.arr[idx]
		st.acc ^= localTake(*p, st.seed)
		if *p != st.arr[idx] {
			failf("local array: 取址后元素被改写")
		}
		runtime.KeepAlive(p)
	case 2:
		p0, p4 := &st.arr[0], &st.arr[4]
		*p0, *p4 = *p4, *p0
		checkNode(st.seed, st.arr[0], "local.swap0")
		checkNode(st.seed, st.arr[4], "local.swap4")
		runtime.KeepAlive(p0)
		runtime.KeepAlive(p4)
	case 3:
		st.acc ^= localArrSum(&st.arr, st.seed)
	case 4:
		var r int64
		for k := 0; k < len(st.arr); k++ {
			idx := int(st.seed+uint64(k)) % len(st.arr)
			checkNode(st.seed, st.arr[idx], "local.sum")
			r ^= st.arr[idx].v
		}
		st.acc ^= r
	}
}

// ===== shape 2: 闭包捕获 =====

func (st *shapeState) closureStep(i int) {
	switch i {
	case 0:
		c0, c1, c2 := st.base[0], st.mk(20), st.mk(21)
		st.cl = func() int64 { return c0.v ^ c1.v ^ c2.v }
		st.acc ^= st.cl()
	case 1:
		st.acc ^= st.cl()
	case 2:
		p, q := st.mk(22), st.mk(23)
		st.cl = func() int64 {
			p.next = q
			return p.v ^ q.v
		}
		st.acc ^= st.cl()
		if p.next != q {
			failf("closure: 捕获指针写入丢失")
		}
	case 3:
		target := st.base[0]
		f := func() { target = st.base[1] }
		f()
		st.slot = target
		if target != st.base[1] {
			failf("closure: 捕获变量更新丢失")
		}
	case 4:
		f := func() int64 { return st.cl() }
		st.acc ^= f()
	}
}

// ===== shape 3: 深递归 =====

//go:noinline
func recWalk(seed uint64, depth int, p *mnode) int64 {
	id := int64(60000 + depth)
	local := &mnode{id: id, v: mnodeV(seed, id)}
	var slots [4]*mnode
	slots[0] = local
	slots[3] = p
	if depth == 0 {
		return local.v ^ p.v ^ slots[0].v ^ slots[3].v
	}
	r := recWalk(seed, depth-1, local)
	return local.v ^ r ^ p.v
}

//go:noinline
func recReturn(seed uint64, depth int, base int64) *mnode {
	id := base + int64(depth)
	if depth == 0 {
		return &mnode{id: id, v: mnodeV(seed, id)}
	}
	return recReturn(seed, depth-1, base)
}

func (st *shapeState) recursionStep(i int) {
	switch i {
	case 0:
		depth := 16 + int(st.seed%8)
		st.acc ^= recWalk(st.seed, depth, st.base[0])
	case 1:
		n := recReturn(st.seed, 12, 61000)
		st.track(n)
		checkNode(st.seed, n, "recursion.ret")
		st.last = n
	case 2:
		st.acc ^= recWalk(st.seed, 3, st.base[1])
	case 3:
		n := recReturn(st.seed, 5, 62000)
		st.track(n)
		checkNode(st.seed, n, "recursion.ret2")
		st.last = n
	case 4:
		if st.last == nil {
			failf("recursion: last 未设置")
		}
		st.acc ^= st.last.v ^ st.base[0].v
	}
}

// ===== shape 4: defer 链 =====

//go:noinline
func deferChain(seed uint64, ns []*mnode) (r int64) {
	defer func() {
		checkNode(seed, ns[0], "defer.chain.0")
		r ^= ns[0].v
	}()
	defer func() {
		checkNode(seed, ns[1], "defer.chain.1")
		r ^= ns[1].v
	}()
	defer func() {
		checkNode(seed, ns[2], "defer.chain.2")
		r ^= ns[2].v
	}()
	return ns[0].v ^ ns[1].v
}

//go:noinline
func deferNamed(seed uint64, p *mnode) (r *mnode) {
	r = p
	defer func() {
		checkNode(seed, r, "defer.named")
		r = r.next
	}()
	return
}

//go:noinline
func deferPanic(seed uint64, p *mnode) (ok bool) {
	defer func() {
		if e := recover(); e != nil {
			checkNode(seed, p, "defer.panic")
			ok = true
		}
	}()
	panic(p)
}

func (st *shapeState) deferStep(i int) {
	switch i {
	case 0:
		st.acc ^= deferChain(st.seed, []*mnode{st.base[0], st.base[1], st.base[2]})
	case 1:
		st.base[0].next = st.base[1]
		r := deferNamed(st.seed, st.base[0])
		if r != st.base[1] {
			failf("defer: 命名返回值未被 defer 改写")
		}
	case 2:
		if !deferPanic(st.seed, st.base[2]) {
			failf("defer: panic 未被 recover")
		}
	case 3:
		st.acc ^= deferChain(st.seed, []*mnode{st.mk(40), st.base[1], st.base[2]})
	case 4:
		st.acc ^= deferChain(st.seed, []*mnode{st.base[2], st.base[0], st.base[1]})
	}
}

// ===== shape 5: 接口派发 =====

type shapeIface interface {
	touch(p *mnode) int64
}

type shapeImplA struct {
	seed uint64
	n    *mnode
}

type shapeImplB struct {
	seed uint64
	n    *mnode
}

func (x *shapeImplA) touch(p *mnode) int64 {
	checkNode(x.seed, x.n, "iface.A.n")
	checkNode(x.seed, p, "iface.A.p")
	return x.n.v ^ p.v
}

func (x *shapeImplB) touch(p *mnode) int64 {
	checkNode(x.seed, x.n, "iface.B.n")
	checkNode(x.seed, p, "iface.B.p")
	return x.n.v ^ p.v
}

//go:noinline
func pickShape(sel uint64, a, b shapeIface) shapeIface {
	if sel&1 == 0 {
		return a
	}
	return b
}

func (st *shapeState) ifaceStep(i int) {
	switch i {
	case 0:
		ia := &shapeImplA{seed: st.seed, n: st.base[0]}
		ib := &shapeImplB{seed: st.seed, n: st.mk(50)}
		st.iface = pickShape(st.seed, ia, ib)
		st.acc ^= st.iface.touch(st.base[1])
	case 1:
		switch x := st.iface.(type) {
		case *shapeImplA:
			checkNode(st.seed, x.n, "iface.switch.A")
		case *shapeImplB:
			checkNode(st.seed, x.n, "iface.switch.B")
		default:
			failf("iface: unknown dynamic type %T", st.iface)
		}
	case 2:
		f := st.iface.touch
		st.acc ^= f(st.base[2])
	case 3:
		st.anyv = st.iface
		x, ok := st.anyv.(shapeIface)
		if !ok {
			failf("iface: any 断言失败")
		}
		st.acc ^= x.touch(st.base[0])
	case 4:
		list := []shapeIface{st.iface, &shapeImplA{seed: st.seed, n: st.base[1]}}
		st.acc ^= list[0].touch(st.base[2]) ^ list[1].touch(st.base[0])
	}
}

// ===== shape 6: 泛型实例 =====

//go:noinline
func genTake[T any](seed uint64, v T, p *mnode) int64 {
	checkNode(seed, p, "generic.take.p")
	runtime.KeepAlive(v)
	return p.v
}

//go:noinline
func genRet[T any](v T) T {
	return v
}

type genAgg struct {
	A [2]*mnode
	N int64
}

type genPtr interface {
	~*mnode
}

//go:noinline
func genPtrTake[T genPtr](seed uint64, v T) int64 {
	p := (*mnode)(v)
	checkNode(seed, p, "generic.ptr")
	return p.v
}

func (st *shapeState) genericStep(i int) {
	switch i {
	case 0:
		a := [2]*mnode{st.base[0], st.base[1]}
		st.acc ^= genTake(st.seed, a, st.base[2])
	case 1:
		g := genAgg{A: [2]*mnode{st.base[1], st.mk(60)}, N: 5}
		st.acc ^= genTake(st.seed, g, st.base[0])
	case 2:
		a := genRet[[2]*mnode]([2]*mnode{st.base[0], st.base[2]})
		checkNode(st.seed, a[0], "generic.ret.0")
		checkNode(st.seed, a[1], "generic.ret.1")
	case 3:
		st.anyv = st.base[1]
		st.acc ^= genTake(st.seed, st.anyv, st.base[2])
	case 4:
		st.acc ^= genPtrTake(st.seed, st.base[0]) ^ genPtrTake(st.seed, st.base[1])
	}
}

// ===== shape 7: chan 交接 =====

func (st *shapeState) chanStep(i int) {
	switch i {
	case 0:
		st.ch <- st.base[0]
	case 1:
		n := <-st.ch
		checkNode(st.seed, n, "chan.recv0")
		if n != st.base[0] {
			failf("chan: 收到的对象身份错误")
		}
		st.last = n
	case 2:
		st.ch <- st.base[1]
		st.ch <- st.base[2]
	case 3:
		n1 := <-st.ch
		n2 := <-st.ch
		checkNode(st.seed, n1, "chan.recv1")
		checkNode(st.seed, n2, "chan.recv2")
		if n1 != st.base[1] || n2 != st.base[2] {
			failf("chan: FIFO 顺序错误")
		}
	case 4:
		select {
		case st.ch <- st.base[0]:
		default:
			failf("chan: 缓冲满")
		}
		n := <-st.ch
		checkNode(st.seed, n, "chan.recv3")
		if n != st.base[0] {
			failf("chan: 末次交接身份错误")
		}
	}
}

// ===== shape 8: unsafe 内部指针 =====

func (st *shapeState) unsafeStep(i int) {
	switch i {
	case 0:
		st.ip = unsafe.Pointer(&st.holder.obj)
		p := (*mnode)(st.ip)
		checkNode(st.seed, p, "unsafe.ip.set")
		runtime.KeepAlive(p)
	case 1:
		p := (*mnode)(st.ip)
		checkNode(st.seed, p, "unsafe.ip.read")
		st.acc ^= p.v
		runtime.KeepAlive(p)
	case 2:
		h2 := &padHolder{pad: [4]*mnode{st.mk(70), st.mk(71), st.mk(72), st.mk(73)}}
		h2.obj = mnode{id: 7900, v: mnodeV(st.seed, 7900)}
		st.ipp = &h2.obj // 仅保留内部指针，holder 本身不再有强引用
		st.track(st.ipp)
		h2 = nil
	case 3:
		checkNode(st.seed, st.ipp, "unsafe.ipp.read")
		checkNode(st.seed, (*mnode)(st.ip), "unsafe.ip.read2")
		st.acc ^= st.ipp.v
	case 4:
		if (*mnode)(st.ip) != &st.holder.obj {
			failf("unsafe: 内部指针身份被改写")
		}
	}
}

// ===== shape 9: 大 byval =====

//go:noinline
func bigTake(seed uint64, g bigAgg) int64 {
	var r int64
	for _, n := range g.P {
		checkNode(seed, n, "byval.P")
		r ^= n.v
	}
	for _, n := range g.Q {
		checkNode(seed, n, "byval.Q")
		r ^= n.v
	}
	runtime.KeepAlive(g)
	return r ^ g.N ^ int64(len(g.S))
}

//go:noinline
func bigTakeMixed(seed uint64, g bigAgg, p *mnode) int64 {
	checkNode(seed, p, "byval.mixed.p")
	return bigTake(seed, g) ^ p.v
}

//go:noinline
func bigRet(seed uint64, base int64) bigAgg {
	var g bigAgg
	for k := 0; k < 4; k++ {
		id := base + int64(k)
		g.P[k] = &mnode{id: id, v: mnodeV(seed, id)}
	}
	for k := 0; k < 3; k++ {
		id := base + 10 + int64(k)
		g.Q[k] = &mnode{id: id, v: mnodeV(seed, id)}
	}
	g.S = "big"
	g.N = 11
	return g
}

func (st *shapeState) byvalStep(i int) {
	switch i {
	case 0:
		g := bigAgg{
			P: [4]*mnode{st.base[0], st.base[1], st.base[2], st.mk(80)},
			Q: [3]*mnode{st.base[0], st.base[1], st.base[2]},
			S: "big",
			I: st.base[0],
			N: 3,
		}
		st.agg = g
		st.acc ^= bigTake(st.seed, g)
	case 1:
		g := bigRet(st.seed, 7000)
		for _, n := range g.P {
			st.track(n)
		}
		for _, n := range g.Q {
			st.track(n)
		}
		st.agg = g
		st.acc ^= bigTake(st.seed, g)
	case 2:
		st.acc ^= bigTakeMixed(st.seed, st.agg, st.base[0])
	case 3:
		st.agg.I = st.agg.P[0]
		st.acc ^= bigTake(st.seed, st.agg)
	case 4:
		if st.agg.N != 11 {
			failf("byval: N=%d want=11", st.agg.N)
		}
		st.acc ^= bigTake(st.seed, st.agg)
	}
}

// ===== shape 10: 命名返回 + RVO =====

//go:noinline
func rvoMake(seed uint64, a, b, c *mnode) (r rvoBig) {
	r.P = [3]*mnode{a, b, c}
	r.S = "rvo"
	r.N = 1
	defer func() {
		r.N = 7
		r.P[2] = c
	}()
	return
}

//go:noinline
func rvoMutate(seed uint64, a, b *mnode) (r rvoBig) {
	r.P[0] = a
	defer func() {
		r.P[1] = b
		r.N = 9
	}()
	return
}

//go:noinline
func rvoMulti(seed uint64, a *mnode) (rvoBig, error) {
	r := rvoBig{P: [3]*mnode{a, a, a}, S: "multi", N: 13}
	return r, nil
}

func (st *shapeState) rvoStep(i int) {
	switch i {
	case 0:
		r := rvoMake(st.seed, st.base[0], st.base[1], st.base[2])
		for _, n := range r.P {
			checkNode(st.seed, n, "rvo.make")
		}
		st.rvo = r
	case 1:
		r := rvoMutate(st.seed, st.base[0], st.base[1])
		for _, n := range r.P {
			if n != nil {
				checkNode(st.seed, n, "rvo.mut")
			}
		}
		if r.N != 9 {
			failf("rvo: deferred 修改丢失 N=%d", r.N)
		}
		st.rvo = r
	case 2:
		if st.rvo.N != 9 && st.rvo.N != 7 {
			failf("rvo: N=%d", st.rvo.N)
		}
		st.acc ^= st.rvo.P[0].v
	case 3:
		r, err := rvoMulti(st.seed, st.base[2])
		if err != nil {
			failf("rvo: multi err=%v", err)
		}
		checkNode(st.seed, r.P[1], "rvo.multi")
		st.rvo = r
	case 4:
		st.acc ^= st.rvo.N
	}
}

// ===== shape 11: 尾调用 wrapper =====

func tailA(seed uint64, p, a *mnode) int64 {
	return tailB(seed, p, a)
}

func tailB(seed uint64, p, a *mnode) int64 {
	id := int64(8001)
	b := &mnode{id: id, v: mnodeV(seed, id)}
	return tailWork(seed, p, a, b)
}

//go:noinline
func tailWork(seed uint64, p, a, b *mnode) int64 {
	checkNode(seed, p, "tail.p")
	checkNode(seed, a, "tail.a")
	checkNode(seed, b, "tail.b")
	r := p.v ^ a.v ^ b.v
	runtime.KeepAlive(p)
	runtime.KeepAlive(a)
	runtime.KeepAlive(b)
	return r
}

//go:noinline
func tailDefer(seed uint64, p *mnode) (r int64) {
	defer func() {
		checkNode(seed, p, "tail.defer")
		r = p.v
	}()
	return 0
}

func (st *shapeState) tailStep(i int) {
	switch i {
	case 0:
		st.acc ^= tailA(st.seed, st.base[0], st.base[1])
	case 1:
		if st.iface == nil {
			st.iface = &shapeImplA{seed: st.seed, n: st.base[0]}
		}
		f := st.iface.touch
		st.acc ^= f(st.base[2])
	case 2:
		if st.iface == nil {
			st.iface = &shapeImplB{seed: st.seed, n: st.base[1]}
		}
		st.acc ^= st.iface.touch(st.base[0])
	case 3:
		st.acc ^= tailDefer(st.seed, st.base[1])
	case 4:
		st.acc ^= tailA(st.seed, st.base[1], st.base[2])
	}
}

// ---------- 注入点执行 ----------

var matrixMu sync.Mutex

func runPoint(shape, i, j int, round int64, seed uint64) {
	matrixMu.Lock()
	defer matrixMu.Unlock()

	curName, curI, curJ, curRnd = shapeNames[shape], i, j, round
	st := newShapeState(shape, seed, round)
	steps := st.steps()
	for k := 0; k < stepsPerShap; k++ {
		if k == i || k == j {
			injectPoint(shape, i, j)
			st.verify(k)
		}
		steps[k]()
		st.verify(k)
	}
	if i == stepsPerShap || j == stepsPerShap {
		injectPoint(shape, i, j)
		st.verify(stepsPerShap)
	}
	runtime.KeepAlive(st)
	atomic.AddInt64(&beats, 1)
}

// runMatrix 完整扫一轮：每 shape 先 6 个单点，再 15 个双点（i<j），共 21 点。
func runMatrix(w *worker, round int64) {
	for _, shape := range w.shapes {
		for i := 0; i <= stepsPerShap; i++ {
			runPoint(shape, i, -1, round, w.seed)
			w.cov++
		}
		for i := 0; i <= stepsPerShap; i++ {
			for j := i + 1; j <= stepsPerShap; j++ {
				runPoint(shape, i, j, round, w.seed)
				w.cov++
			}
		}
	}
}

type worker struct {
	id      int64
	seed    uint64
	shapes  []int
	rounds  int64
	plan    int64
	planGCs int64
	cov     int64
	next    int64
}

func (w *worker) run(seconds int, deadline time.Time, fixed int64) {
	atomic.AddInt64(&started, 1)
	defer atomic.AddInt64(&done, 1)
	r := int64(0)
	for {
		if seconds > 0 && time.Now().After(deadline) {
			break
		}
		if seconds == 0 && r >= fixed {
			break
		}
		runMatrix(w, r)
		w.rounds++
		w.next = r + 1
		r++
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
				failf("phase=watchdog: no injection progress for %s (beats=%d)", time.Since(lastChange), cur)
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
	workers := envInt("STRESS_WORKERS", 1, 1, numShapes)
	hb := envInt("STRESS_HEARTBEAT", 5, 1, 60)
	seed := envSeed(1)
	fixed := int64(envInt("STRESS_FIXED_ROUNDS", 2, 1, 1000000))
	gSeed = seed

	origProcs := runtime.GOMAXPROCS(1)
	fmt.Printf("scenario=%s phase=preflight go=%s goos=%s goarch=%s gomaxprocs=%d(origs=%d) seed=%d seconds=%d workers=%d fixed_rounds=%d heartbeat=%ds shapes=%d steps=%d points_per_shape=%d points_per_matrix=%d\n",
		scenario, runtime.Version(), runtime.GOOS, runtime.GOARCH, runtime.GOMAXPROCS(0), origProcs,
		seed, seconds, workers, fixed, hb, numShapes, stepsPerShap, pointsShape, totalPoints)

	baseGoroutines := runtime.NumGoroutine()
	deadline := time.Now().Add(time.Duration(seconds) * time.Second)

	ws := make([]*worker, workers)
	for i := 0; i < workers; i++ {
		w := &worker{
			id:   int64(i),
			seed: seed*1_000_003 + uint64(i+1)*7919,
		}
		for s := i; s < numShapes; s += workers {
			w.shapes = append(w.shapes, s)
		}
		w.plan = int64(len(w.shapes) * pointsShape)
		w.planGCs = int64(len(w.shapes)) * int64(boundaries+2*doubles)
		ws[i] = w
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
	runtime.GOMAXPROCS(origProcs)

	if s, f := atomic.LoadInt64(&started), atomic.LoadInt64(&done); s != int64(workers) || f != s {
		failf("phase=final: started=%d finished=%d workers=%d", s, f, workers)
	}

	var roundsTotal, covTotal, planTotal, gcTotal, gcWant int64
	for _, w := range ws {
		roundsTotal += w.rounds
		covTotal += w.cov
		planTotal += w.plan * w.rounds
		gcWant += w.planGCs * w.rounds
		if w.rounds == 0 {
			failf("phase=final worker=%d: 未完成任何完整矩阵（seconds=%d）", w.id, seconds)
		}
		if w.next != w.rounds {
			failf("phase=final worker=%d: rounds=%d next=%d", w.id, w.rounds, w.next)
		}
		if seconds == 0 && w.rounds != fixed {
			failf("phase=final worker=%d: matrices=%d want=%d", w.id, w.rounds, fixed)
		}
	}
	// 覆盖完整性：每个完成的矩阵必须 21 点/shape 全部执行。任何未扫完的点都不得 PASS。
	if covTotal != planTotal {
		failf("phase=final: injection_points_covered=%d total=%d (矩阵未扫完，禁止 PASS)", covTotal, planTotal)
	}
	if planTotal == 0 || covTotal < totalPoints {
		failf("phase=final: covered=%d < 单矩阵点数=%d", covTotal, totalPoints)
	}
	gcTotal = atomic.LoadInt64(&gcCount)
	// 单点每 shape 6 次、双点每 shape 15×2 次，共 36 次/ shape / 矩阵；只允许恰好相等。
	if gcTotal != gcWant {
		failf("phase=final: gc_injections=%d want=%d (注入点未按枚举执行)", gcTotal, gcWant)
	}

	ng := waitGoroutines(baseGoroutines, 5*time.Second)
	if ng > baseGoroutines+4 {
		failf("phase=final: goroutines baseline=%d after=%d: goroutine leak", baseGoroutines, ng)
	}

	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	fmt.Printf("metrics: full_matrices=%d injection_points_covered=%d injection_points_total=%d gc_injections=%d gc_want=%d shapes=%d steps=%d num_gc=%d heap_alloc=%d heap_objects=%d goroutines=%d\n",
		roundsTotal, covTotal, planTotal, gcTotal, gcWant, numShapes, stepsPerShap,
		m.NumGC, m.HeapAlloc, m.HeapObjects, ng)
	fmt.Printf("STRESS PASS %s\n", scenario)
}
