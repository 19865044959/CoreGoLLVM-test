// U02: 抢占 × 栈扫描窗口风暴长跑（P0）。
// 三种持针形态的 worker 并行，在无调用/极少调用窗口里让指针活跃于寄存器/未 spill 栈槽：
//
//	a) interface 值在 3 个实现间轮转并调用方法（receiver 常驻寄存器）；
//	b) 绑定 receiver 的 method value + 捕获 []*node 的闭包链；
//	c) struct{p *node; inner [4]*node} 聚合逐字段轮转访问。
//
// 独立 gcStorm goroutine 高频 runtime.GC()，放大“GC 扫所有栈 → 异步抢占”窗口；
// 阶段切换 GOMAXPROCS 1↔2（显式 STRESS_GMX 覆盖；进程初始 GOMAXPROCS=1 时不切，
// 保持 soak-single 语义）；worker 每轮在 Gosched / 无缓冲 channel 往返 / 微秒 Sleep
// 三种挂起点持针，恢复后立即校验 canary；GC 前后各做一轮全量校验；
// 循环外可达 anchor 注册 finalizer 探针，可达被终结即 FAIL。
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
)

const (
	scenarioName   = "u02_preempt_scan_window"
	burstN         = 4096 // 每个 chunk 的无调用窗口迭代数
	chunksPerRound = 4    // 每轮 chunk 数
	ifaceImpls     = 3    // interface 实现数（2-3 个间热切换）
	closureNodes   = 8    // 闭包捕获的指针数组长度（2 的幂）
	mixNodes       = 5    // 聚合持有的指针数（p + inner[4]）
	anchorCount    = 4    // finalizer 探针数
)

const (
	kindIface = iota
	kindClosure
	kindMix
)

var (
	beats         int64 // 每完成一轮 +1，watchdog 用
	gcStormCount  int64 // 独立 gcStorm 的 GC 次数
	workerGC      int64 // worker 轮内显式 GC 次数
	chunkChecks   int64 // chunk 窗口边界全量校验次数
	preemptChecks int64 // 挂起点/GC 后（潜在被抢占后）校验次数
	suspendChecks int64 // 主动挂起后校验次数
	canaryChecks  int64 // canary 节点校验次数
	totalSum      int64
	started       int64
	finished      int64
	gSeed         uint64

	anchorStore     []*anchorObj
	anchorFinalized int64
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

// parseGMX 解析 STRESS_GMX（如 "1,2,4"）；空串/全非法返回 nil。
func parseGMX(s string) []int {
	if s == "" {
		return nil
	}
	var out []int
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		v, err := strconv.Atoi(part)
		if err != nil || v < 1 || v > 256 {
			continue
		}
		out = append(out, v)
	}
	return out
}

// magicFor 是确定性 canary：同一 seed/id 恒得同一 magic，零值被替换。
func magicFor(seed uint64, id int64) int64 {
	x := seed*0x9E3779B97F4A7C15 + uint64(id)*0x2545F4914F6CDD1D + 0x5EED5EED
	x ^= x >> 31
	x *= 0xBF58476D1CE4E5B9
	x ^= x >> 27
	if x == 0 {
		x = 1
	}
	return int64(x)
}

type node struct {
	magic int64
	id    int64
	pad0  int64
	pad1  int64
}

func newNode(seed uint64, id int64) *node {
	return &node{magic: magicFor(seed, id), id: id, pad0: id*7 + 3, pad1: id*11 + 5}
}

// checkNode 做全字段校验：magic 由 seed/id 重算，任何 UAF/覆盖当场失败。
func checkNode(where string, seed uint64, wid, r, c int64, p *node) {
	if p == nil {
		failf("phase=canary where=%s worker=%d round=%d chunk=%d seed=%d gomaxprocs=%d: nil node",
			where, wid, r, c, seed, runtime.GOMAXPROCS(0))
	}
	if p.magic != magicFor(seed, p.id) || p.pad0 != p.id*7+3 || p.pad1 != p.id*11+5 {
		failf("phase=canary where=%s worker=%d round=%d chunk=%d seed=%d gomaxprocs=%d: node corrupted id=%d magic=%d pad=(%d,%d)",
			where, wid, r, c, seed, runtime.GOMAXPROCS(0), p.id, p.magic, p.pad0, p.pad1)
	}
}

// spell 是热切换的接口方法集；raw 用于校验 receiver 与登记节点的恒等性。
type spell interface {
	cast(r, c int64) int64
	raw() *node
}

type implA struct {
	n    *node
	tag  int64
	seed uint64
	wid  int64
}

func (a *implA) cast(r, c int64) int64 {
	if a == nil {
		failf("phase=iface-A worker=unknown: nil receiver")
	}
	if a.n == nil || a.tag != a.n.id*7+1 {
		failf("phase=iface-A worker=%d round=%d chunk=%d seed=%d gomaxprocs=%d: receiver tag corrupted",
			a.wid, r, c, a.seed, runtime.GOMAXPROCS(0))
	}
	checkNode("iface-A", a.seed, a.wid, r, c, a.n)
	return a.n.magic
}

func (a *implA) raw() *node { return a.n }

type implB struct {
	pad  int64
	n    *node
	tag  int64
	seed uint64
	wid  int64
}

func (b *implB) cast(r, c int64) int64 {
	if b == nil {
		failf("phase=iface-B worker=unknown: nil receiver")
	}
	if b.n == nil || b.tag != b.n.id*11+3 {
		failf("phase=iface-B worker=%d round=%d chunk=%d seed=%d gomaxprocs=%d: receiver tag corrupted",
			b.wid, r, c, b.seed, runtime.GOMAXPROCS(0))
	}
	checkNode("iface-B", b.seed, b.wid, r, c, b.n)
	return b.n.magic
}

func (b *implB) raw() *node { return b.n }

type implC struct {
	tag  int64
	n    *node
	pad  [2]int64
	seed uint64
	wid  int64
}

func (c *implC) cast(r, ch int64) int64 {
	if c == nil {
		failf("phase=iface-C worker=unknown: nil receiver")
	}
	if c.n == nil || c.tag != c.n.id*13+5 {
		failf("phase=iface-C worker=%d round=%d chunk=%d seed=%d gomaxprocs=%d: receiver tag corrupted",
			c.wid, r, ch, c.seed, runtime.GOMAXPROCS(0))
	}
	checkNode("iface-C", c.seed, c.wid, r, ch, c.n)
	return c.n.magic
}

func (c *implC) raw() *node { return c.n }

type fixture struct {
	n    *node
	tag  int64
	seed uint64
	wid  int64
}

// bump 是绑定到 fixture 的 method value 的目标，receiver 指针随 func value 存活。
//
//go:noinline
func (f *fixture) bump(r int64) int64 {
	if f == nil {
		failf("phase=methodvalue worker=unknown: nil receiver")
	}
	if f.n == nil || f.tag != f.n.id*17+7 {
		failf("phase=methodvalue worker=%d round=%d seed=%d gomaxprocs=%d: receiver tag corrupted",
			f.wid, r, f.seed, runtime.GOMAXPROCS(0))
	}
	checkNode("methodvalue", f.seed, f.wid, r, -1, f.n)
	return f.n.magic + r
}

type entry struct {
	n     *node
	magic int64
	id    int64
}

// mixAgg 是混合聚合持针形态：一个直接指针 + 4 个数组内指针。
type mixAgg struct {
	p     *node
	inner [4]*node
	tag   int64
}

type worker struct {
	kind     int
	id       int64
	seed     uint64
	phase    int
	next     int64
	localSum int64
	accSum   int64 // 只由本 worker goroutine 原子累加，收尾对照

	// a) interface 热切换
	ifaceEntries [ifaceImpls]entry
	ifaces       [ifaceImpls]spell

	// b) method value + 闭包链
	fixture      *fixture
	fixtureMagic int64
	mv           func(int64) int64
	ptrs         []*node
	ptrMagic     []int64
	ptrIDs       []int64
	ptrSum       int64
	readAll      func(r, c int64) int64
	outer        func(r, c int64) int64

	// c) 混合聚合
	mixEntries [mixNodes]entry
	mixTag     int64

	ch chan *node // 无缓冲往返通道（每 worker 一个 relay goroutine）
}

func newWorker(kind int, id int64, seed uint64) *worker {
	w := &worker{kind: kind, id: id, seed: seed, ch: make(chan *node)}
	base := (id + 1) * 1000

	for i := 0; i < ifaceImpls; i++ {
		n := newNode(seed, base+int64(i))
		w.ifaceEntries[i] = entry{n: n, magic: n.magic, id: n.id}
		switch i {
		case 0:
			w.ifaces[i] = &implA{n: n, tag: n.id*7 + 1, seed: seed, wid: id}
		case 1:
			w.ifaces[i] = &implB{pad: 0x11, n: n, tag: n.id*11 + 3, seed: seed, wid: id}
		default:
			w.ifaces[i] = &implC{tag: n.id*13 + 5, n: n, pad: [2]int64{0x21, 0x22}, seed: seed, wid: id}
		}
	}

	fn := newNode(seed, base+100)
	w.fixture = &fixture{n: fn, tag: fn.id*17 + 7, seed: seed, wid: id}
	w.fixtureMagic = fn.magic
	w.mv = w.fixture.bump

	w.ptrs = make([]*node, closureNodes)
	w.ptrMagic = make([]int64, closureNodes)
	w.ptrIDs = make([]int64, closureNodes)
	for i := 0; i < closureNodes; i++ {
		n := newNode(seed, base+200+int64(i))
		w.ptrs[i] = n
		w.ptrMagic[i] = n.magic
		w.ptrIDs[i] = n.id
		w.ptrSum += n.magic
	}
	// 闭包捕获指针数组：循环读；outer 再包一层形成闭包链。
	w.readAll = func(r, c int64) int64 {
		var s int64
		for i, p := range w.ptrs {
			if p == nil || p.magic != w.ptrMagic[i] || p.id != w.ptrIDs[i] {
				failf("phase=closure-capture worker=%d round=%d chunk=%d seed=%d gomaxprocs=%d: ptr[%d] corrupted",
					w.id, r, c, w.seed, runtime.GOMAXPROCS(0), i)
			}
			s += p.magic
		}
		return s
	}
	w.outer = func(r, c int64) int64 { return w.readAll(r, c) + r }

	for i := 0; i < mixNodes; i++ {
		n := newNode(seed, base+300+int64(i))
		w.mixEntries[i] = entry{n: n, magic: n.magic, id: n.id}
	}
	w.mixTag = int64(seed%997)*77 + id + 9
	return w
}

func (w *worker) failCtx(phase string, r, c int64, format string, args ...interface{}) {
	failf("phase=%s worker=%d round=%d chunk=%d kind=%d seed=%d gomaxprocs=%d: "+format,
		append([]interface{}{phase, w.id, r, c, w.kind, w.seed, runtime.GOMAXPROCS(0)}, args...)...)
}

// suspend 在三种挂起点之一挂起；调用方持有的指针形态必须在返回后仍被使用。
func (w *worker) suspend(r int64, p *node) {
	switch int(r % 3) {
	case 0:
		runtime.Gosched()
	case 1:
		w.ch <- p // 与 relay 的无缓冲 rendezvous：先阻塞发送
		got := <-w.ch
		if got != p {
			w.failCtx("chan", r, -1, "pointer identity lost across channel roundtrip")
		}
	default:
		d := time.Duration(50+(w.id*37+r*13)%250) * time.Microsecond
		time.Sleep(d)
	}
}

// ifaceRound: interface 值在 3 个实现间轮转；每个 chunk 先跑无调用窗口，再调 cast 校验。
func (w *worker) ifaceRound(r int64) {
	ents := w.ifaceEntries[:]
	var chunkSum int64
	for c := int64(0); c < chunksPerRound; c++ {
		rot := (int(r) + int(c)) % ifaceImpls
		iface := w.ifaces[rot]
		want := ents[rot].magic

		var s int64
		for j := 0; j < burstN; j++ {
			idx := (j + rot) % ifaceImpls
			q := ents[idx].n
			if q == nil || q.magic != ents[idx].magic || q.id != ents[idx].id {
				w.failCtx("iface-burst", r, c, "window slot corrupted j=%d idx=%d", j, idx)
			}
			s += q.magic + int64(j&7)
		}
		chunkSum += s

		// 窗口后调用接口方法：receiver 指针在无调用窗口内一直存活。
		if got := iface.cast(r, c); got != want {
			w.failCtx("iface-cast", r, c, "cast magic=%d want=%d", got, want)
		}
		atomic.AddInt64(&canaryChecks, burstN+1)
		atomic.AddInt64(&chunkChecks, 1)
	}

	// 主动挂起：iface 与 receiver 节点跨越挂起点存活，恢复后立即校验。
	rot := int(r) % ifaceImpls
	iface := w.ifaces[rot]
	p := ents[rot].n
	w.suspend(r, p)
	if p.magic != ents[rot].magic || p.id != ents[rot].id {
		w.failCtx("iface-post-suspend", r, -1, "receiver node corrupted after suspension")
	}
	if got := iface.cast(r, -1); got != ents[rot].magic {
		w.failCtx("iface-post-suspend", r, -1, "cast magic=%d want=%d", got, ents[rot].magic)
	}
	atomic.AddInt64(&suspendChecks, 1)
	atomic.AddInt64(&preemptChecks, 1)

	w.verifyIface("pre-gc", r)
	runtime.GC()
	atomic.AddInt64(&workerGC, 1)
	w.verifyIface("post-gc", r)
	atomic.AddInt64(&preemptChecks, 1)

	w.localSum += chunkSum
	atomic.AddInt64(&w.accSum, chunkSum)
	atomic.AddInt64(&totalSum, chunkSum)
}

func (w *worker) verifyIface(where string, r int64) {
	for i := 0; i < ifaceImpls; i++ {
		e := &w.ifaceEntries[i]
		got := w.ifaces[i].cast(r, -1)
		if got != e.magic {
			w.failCtx("verify-"+where, r, -1, "iface[%d] magic=%d want=%d", i, got, e.magic)
		}
		if w.ifaces[i].raw() != e.n {
			w.failCtx("verify-"+where, r, -1, "iface[%d] receiver identity lost", i)
		}
		checkNode(where, w.seed, w.id, r, -1, e.n)
		if e.n.id != e.id {
			w.failCtx("verify-"+where, r, -1, "iface[%d] id=%d want=%d", i, e.n.id, e.id)
		}
	}
	atomic.AddInt64(&canaryChecks, ifaceImpls)
}

// closureRound: 绑定 receiver 的 method value 循环调用 + 闭包捕获指针数组循环读。
func (w *worker) closureRound(r int64) {
	ptrs := w.ptrs
	pMagic := w.ptrMagic
	pIDs := w.ptrIDs
	mv := w.mv
	var chunkSum int64

	for c := int64(0); c < chunksPerRound; c++ {
		rot := int((r + c) & (closureNodes - 1))
		var s int64
		for j := 0; j < burstN; j++ {
			idx := (j + rot) & (closureNodes - 1)
			q := ptrs[idx]
			if q == nil || q.magic != pMagic[idx] || q.id != pIDs[idx] {
				w.failCtx("closure-burst", r, c, "ptr[%d] corrupted j=%d", idx, j)
			}
			s += q.magic + int64(j&3)
		}
		chunkSum += s

		if got := mv(int64(c)); got != w.fixtureMagic+int64(c) {
			w.failCtx("methodvalue", r, c, "bump=%d want=%d", got, w.fixtureMagic+int64(c))
		}
		if got := w.readAll(r, c); got != w.ptrSum {
			w.failCtx("closure-capture", r, c, "sum=%d want=%d", got, w.ptrSum)
		}
		if got := w.outer(r, c); got != w.ptrSum+r {
			w.failCtx("closure-chain", r, c, "sum=%d want=%d", got, w.ptrSum+r)
		}
		atomic.AddInt64(&canaryChecks, burstN+closureNodes+2)
		atomic.AddInt64(&chunkChecks, 1)
	}

	// 挂起：method value（含 receiver 指针）与闭包捕获的指针数组跨越挂起点。
	rot := int(r & (closureNodes - 1))
	p := ptrs[rot]
	w.suspend(r, p)
	if p.magic != pMagic[rot] || p.id != pIDs[rot] {
		w.failCtx("closure-post-suspend", r, -1, "ptr[%d] corrupted after suspension", rot)
	}
	if got := mv(0); got != w.fixtureMagic {
		w.failCtx("methodvalue-post-suspend", r, -1, "bump=%d want=%d", got, w.fixtureMagic)
	}
	if got := w.readAll(r, -1); got != w.ptrSum {
		w.failCtx("closure-post-suspend", r, -1, "sum=%d want=%d", got, w.ptrSum)
	}
	atomic.AddInt64(&suspendChecks, 1)
	atomic.AddInt64(&preemptChecks, 1)

	w.verifyClosure("pre-gc", r)
	runtime.GC()
	atomic.AddInt64(&workerGC, 1)
	w.verifyClosure("post-gc", r)
	atomic.AddInt64(&preemptChecks, 1)

	w.localSum += chunkSum
	atomic.AddInt64(&w.accSum, chunkSum)
	atomic.AddInt64(&totalSum, chunkSum)
}

func (w *worker) verifyClosure(where string, r int64) {
	if got := w.mv(0); got != w.fixtureMagic {
		w.failCtx("verify-"+where, r, -1, "bump=%d want=%d", got, w.fixtureMagic)
	}
	checkNode(where, w.seed, w.id, r, -1, w.fixture.n)
	if got := w.readAll(r, -1); got != w.ptrSum {
		w.failCtx("verify-"+where, r, -1, "closure sum=%d want=%d", got, w.ptrSum)
	}
	for i := 0; i < closureNodes; i++ {
		checkNode(where, w.seed, w.id, r, -1, w.ptrs[i])
		if w.ptrs[i].id != w.ptrIDs[i] {
			w.failCtx("verify-"+where, r, -1, "ptr[%d] id=%d want=%d", i, w.ptrs[i].id, w.ptrIDs[i])
		}
	}
	atomic.AddInt64(&canaryChecks, closureNodes+1)
}

// mixRound: struct{p *node; inner [4]*node} 在循环中逐字段轮转访问。
func (w *worker) mixRound(r int64) {
	rot := int(r % mixNodes)
	var m mixAgg
	var want [mixNodes]int64
	var wantID [mixNodes]int64

	m.p = w.mixEntries[rot].n
	want[0] = w.mixEntries[rot].magic
	wantID[0] = w.mixEntries[rot].id
	for i := 0; i < 4; i++ {
		e := &w.mixEntries[(rot+i+1)%mixNodes]
		m.inner[i] = e.n
		want[i+1] = e.magic
		wantID[i+1] = e.id
	}
	m.tag = w.mixTag

	var chunkSum int64
	for c := int64(0); c < chunksPerRound; c++ {
		var s int64
		for j := 0; j < burstN; j++ {
			f := (j + rot + int(c)) % mixNodes
			var q *node
			switch f {
			case 0:
				q = m.p
			case 1:
				q = m.inner[0]
			case 2:
				q = m.inner[1]
			case 3:
				q = m.inner[2]
			default:
				q = m.inner[3]
			}
			if q == nil || q.magic != want[f] || q.id != wantID[f] {
				w.failCtx("mix-burst", r, c, "field=%d corrupted j=%d", f, j)
			}
			s += q.magic + int64(j&7)
		}
		chunkSum += s

		// 逐字段轮转全量校验。
		if m.p == nil || m.p.magic != want[0] || m.p.id != wantID[0] || m.tag != w.mixTag {
			w.failCtx("mix-fields", r, c, "p/tag corrupted")
		}
		for i := 0; i < 4; i++ {
			q := m.inner[i]
			if q == nil || q.magic != want[i+1] || q.id != wantID[i+1] {
				w.failCtx("mix-fields", r, c, "inner[%d] corrupted", i)
			}
		}
		atomic.AddInt64(&canaryChecks, burstN+mixNodes)
		atomic.AddInt64(&chunkChecks, 1)
	}

	// 挂起：整个局部聚合的 5 个指针跨越挂起点存活。
	p := m.p
	w.suspend(r, p)
	w.verifyMix("post-suspend", r, m, &want, &wantID)
	atomic.AddInt64(&suspendChecks, 1)
	atomic.AddInt64(&preemptChecks, 1)

	w.verifyMix("pre-gc", r, m, &want, &wantID)
	runtime.GC()
	atomic.AddInt64(&workerGC, 1)
	w.verifyMix("post-gc", r, m, &want, &wantID)
	atomic.AddInt64(&preemptChecks, 1)

	runtime.KeepAlive(m)
	w.localSum += chunkSum
	atomic.AddInt64(&w.accSum, chunkSum)
	atomic.AddInt64(&totalSum, chunkSum)
}

func (w *worker) verifyMix(where string, r int64, m mixAgg, want, wantID *[mixNodes]int64) {
	if m.p == nil || m.p.magic != want[0] || m.p.id != wantID[0] {
		w.failCtx("verify-"+where, r, -1, "p corrupted")
	}
	for i := 0; i < 4; i++ {
		q := m.inner[i]
		if q == nil || q.magic != want[i+1] || q.id != wantID[i+1] {
			w.failCtx("verify-"+where, r, -1, "inner[%d] corrupted", i)
		}
	}
	if m.tag != w.mixTag {
		w.failCtx("verify-"+where, r, -1, "tag=%d want=%d", m.tag, w.mixTag)
	}
	atomic.AddInt64(&canaryChecks, mixNodes)
}

func (w *worker) run(deadline time.Time, timed bool, stopRound int64) {
	atomic.AddInt64(&started, 1)
	defer atomic.AddInt64(&finished, 1)
	for {
		if timed {
			if !time.Now().Before(deadline) {
				return
			}
		} else if w.next >= stopRound {
			return
		}
		r := w.next
		switch w.kind {
		case kindIface:
			w.ifaceRound(r)
		case kindClosure:
			w.closureRound(r)
		default:
			w.mixRound(r)
		}
		w.next = r + 1
		atomic.AddInt64(&beats, 1)
	}
}

// anchorObj 是循环外仍可达的 finalizer 探针；被终结即 FAIL。
type anchorObj struct {
	id    int64
	magic int64
	pad   [2]int64
}

func anchorFinalizer(a *anchorObj) {
	atomic.AddInt64(&anchorFinalized, 1)
}

func setupAnchors(seed uint64) {
	anchorStore = make([]*anchorObj, anchorCount)
	for i := 0; i < anchorCount; i++ {
		id := int64(9000 + i)
		a := &anchorObj{id: id, magic: magicFor(seed, id), pad: [2]int64{int64(i) + 1, int64(i) + 2}}
		runtime.SetFinalizer(a, anchorFinalizer)
		anchorStore[i] = a
	}
}

func verifyAnchors(where string) {
	if len(anchorStore) != anchorCount {
		failf("phase=anchor where=%s: anchor store len=%d want=%d", where, len(anchorStore), anchorCount)
	}
	for i, a := range anchorStore {
		if a == nil || a.magic != magicFor(gSeed, a.id) || a.pad != [2]int64{int64(i) + 1, int64(i) + 2} {
			failf("phase=anchor where=%s anchor=%d seed=%d: anchor corrupted", where, i, gSeed)
		}
	}
	if n := atomic.LoadInt64(&anchorFinalized); n != 0 {
		failf("phase=anchor where=%s seed=%d: 可达 anchor 被 finalizer 终结 count=%d", where, gSeed, n)
	}
}

// gcStorm 与所有 worker 并行，高频显式 GC：GC 需要扫所有栈 → 触发对运行中 goroutine 的抢占。
func gcStorm(stop <-chan struct{}, wg *sync.WaitGroup) {
	defer wg.Done()
	var sink [][]byte
	for {
		select {
		case <-stop:
			return
		default:
		}
		for i := 0; i < 8; i++ {
			b := make([]byte, 1024)
			b[i] = byte(i)
			sink = append(sink, b)
		}
		for i := range sink {
			sink[i] = nil
		}
		sink = sink[:0]
		runtime.GC()
		atomic.AddInt64(&gcStormCount, 1)
		// window（GOMAXPROCS=1+STW）下无休眠 GC 循环会饿死 worker/watchdog。
		if runtime.GOMAXPROCS(0) == 1 {
			runtime.Gosched()
			time.Sleep(time.Millisecond)
		}
	}
}

// relayLoop 与单个 worker 做无缓冲指针往返：接收后阻塞回送，制造真实阻塞收发。
func relayLoop(ch chan *node, stop <-chan struct{}, wg *sync.WaitGroup) {
	defer wg.Done()
	for {
		select {
		case p := <-ch:
			select {
			case ch <- p:
			case <-stop:
				return
			}
		case <-stop:
			return
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
				failf("watchdog stalled: no worker progress for %s (beats=%d seed=%d)", time.Since(lastChange), cur, gSeed)
			}
		}
	}
}

// 把 fatal/panic 的 crash 文本写到 runner 指定的文件（core 不可用的环境用）。
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
	workers := envInt("STRESS_WORKERS", 6, 1, 32)
	hb := envInt("STRESS_HEARTBEAT", 5, 1, 60)
	seed := envSeed(1)
	fixed := int64(envInt("STRESS_FIXED_ROUNDS", 32, 1, 1000000))
	gSeed = seed

	// GOMAXPROCS 阶段：显式 STRESS_GMX 优先；否则初始 >=2 时自动 1↔2；
	// 初始=1（soak-single）时不切，保持 profile 语义。
	gmx := parseGMX(os.Getenv("STRESS_GMX"))
	auto := false
	if len(gmx) == 0 {
		if runtime.GOMAXPROCS(0) >= 2 {
			gmx = []int{1, 2}
			auto = true
		}
	}
	phases := len(gmx)
	if phases == 0 {
		phases = 1
	}

	setupAnchors(seed)
	verifyAnchors("preflight")

	// 预热一次 GC，再取 goroutine 基线（避免 runtime 后台 goroutine 抖动）。
	runtime.GC()
	startBase := runtime.NumGoroutine()

	fmt.Printf("scenario=u02 phase=preflight go=%s goos=%s goarch=%s gomaxprocs=%d seed=%d seconds=%d workers=%d heartbeat=%ds gmx=%v auto=%v phases=%d fixed_rounds=%d burst=%d chunks=%d anchors=%d\n",
		runtime.Version(), runtime.GOOS, runtime.GOARCH, runtime.GOMAXPROCS(0), seed, seconds, workers, hb, gmx, auto, phases, fixed, burstN, chunksPerRound, anchorCount)

	ws := make([]*worker, workers)
	for i := 0; i < workers; i++ {
		ws[i] = newWorker(i%3, int64(i), seed*1000+uint64(i)+1)
	}

	stop := make(chan struct{})
	var aux sync.WaitGroup
	aux.Add(1)
	go gcStorm(stop, &aux)
	aux.Add(1)
	go watchdog(time.Duration(hb)*time.Second, stop, &aux)
	for _, w := range ws {
		aux.Add(1)
		go relayLoop(w.ch, stop, &aux)
	}

	start := time.Now()
	var total time.Duration
	if seconds > 0 {
		total = time.Duration(seconds) * time.Second
	}

	for p := 0; p < phases; p++ {
		// 阶段边界才改 GOMAXPROCS；初始=1 且未显式设置时 phases=1，不触碰。
		if len(gmx) > 0 {
			runtime.GOMAXPROCS(gmx[p])
		}
		pStart := fixed * int64(p) / int64(phases)
		pEnd := fixed * int64(p+1) / int64(phases)
		var deadline time.Time
		if seconds > 0 {
			if p == phases-1 {
				deadline = start.Add(total)
			} else {
				deadline = start.Add(total * time.Duration(p+1) / time.Duration(phases))
			}
			fmt.Printf("phase=%d/%d gomaxprocs=%d timed=true\n", p+1, phases, runtime.GOMAXPROCS(0))
		} else {
			fmt.Printf("phase=%d/%d gomaxprocs=%d rounds=[%d,%d)\n", p+1, phases, runtime.GOMAXPROCS(0), pStart, pEnd)
		}

		var wg sync.WaitGroup
		for _, w := range ws {
			w.phase = p
			wg.Add(1)
			go func(w *worker) {
				defer wg.Done()
				w.run(deadline, seconds > 0, pEnd)
			}(w)
		}
		wg.Wait()
		verifyAnchors("phase-boundary")
	}

	close(stop)
	aux.Wait()

	// 收尾：多轮 GC 后再验 anchor（可达对象不得被终结），并做 goroutine 基线。
	runtime.GC()
	runtime.GC()
	verifyAnchors("final")
	ng := waitGoroutines(startBase, 5*time.Second)

	var roundsTotal, locals int64
	for _, w := range ws {
		roundsTotal += w.next
		if seconds == 0 && w.next != fixed {
			failf("phase=final worker=%d seed=%d: rounds=%d want=%d", w.id, w.seed, w.next, fixed)
		}
		if got := atomic.LoadInt64(&w.accSum); got != w.localSum {
			failf("phase=final worker=%d seed=%d: atomic sum=%d local sum=%d", w.id, w.seed, got, w.localSum)
		}
		locals += w.localSum
	}
	if got := atomic.LoadInt64(&totalSum); got != locals {
		failf("phase=final: global atomic sum=%d sum of workers=%d", got, locals)
	}
	if s, f := atomic.LoadInt64(&started), atomic.LoadInt64(&finished); s != f {
		failf("phase=final: started=%d finished=%d", s, f)
	}
	if atomic.LoadInt64(&beats) == 0 {
		failf("phase=final: no rounds completed (seed=%d)", seed)
	}
	if ng > startBase+8 {
		failf("phase=final seed=%d: goroutine baseline exceeded: final=%d start=%d", seed, ng, startBase)
	}

	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	fmt.Printf("metrics: rounds=%d beats=%d chunk_checks=%d preempt_checks=%d suspend_checks=%d canary_checks=%d gc_storm=%d worker_gc=%d num_gc=%d total_sum=%d anchors=%d finalized=%d goroutines_start=%d goroutines_final=%d heap_alloc=%d heap_objects=%d gomaxprocs=%d\n",
		roundsTotal, atomic.LoadInt64(&beats), atomic.LoadInt64(&chunkChecks), atomic.LoadInt64(&preemptChecks),
		atomic.LoadInt64(&suspendChecks), atomic.LoadInt64(&canaryChecks), atomic.LoadInt64(&gcStormCount),
		atomic.LoadInt64(&workerGC), m.NumGC, atomic.LoadInt64(&totalSum), anchorCount,
		atomic.LoadInt64(&anchorFinalized), startBase, ng, m.HeapAlloc, m.HeapObjects, runtime.GOMAXPROCS(0))
	fmt.Printf("STRESS PASS %s\n", scenarioName)
}
