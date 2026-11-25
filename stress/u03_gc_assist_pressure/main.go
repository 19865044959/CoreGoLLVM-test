// U03: GC assist 压力长跑。
// 预分配大 anchor 对象图（完全二叉树 + 链 + map + finalizer 探针）常驻老年代；
// 分配风暴 worker 每轮混合分配小 node / 中 slice / 大 []byte(1-4MB)，立即校验后交给
// 独立 verifier goroutine 短暂持有再释放，制造“分配-校验-失活”窗口；
// 内存 governor 在 live heap 的 1.0x/1.1x/1.2x 间轮换 GOMEMLIMIT，把 mutator 持续
// 推入 GC assist；独立写屏障 worker 并发做 old→young 改边（指针/interface/map）。
// assist 活跃度只记录不断言：读 runtime/metrics 的 /gc/pauses 与 NumGC/HeapAlloc 曲线。
package main

import (
	"fmt"
	"math"
	"os"
	"runtime"
	"runtime/debug"
	"runtime/metrics"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

const (
	scenario         = "u03_gc_assist_pressure"
	treeN            = 1 << 19 // anchor 完全二叉树节点数
	chainN           = 1 << 17 // anchor 链节点数
	mapN             = 1 << 15 // anchor map 稳定键数
	dynKeys          = 32      // 写屏障 worker 轮换的 old→young map 键数
	dynSlots         = 8       // old→young 指针/interface 槽位数
	youngN           = 32      // 每次写屏障改边挂接的 young 链长度
	holdDepth        = 2       // verifier 持有批次上限（释放窗口）
	tempChanCap      = 8       // 分配→verifier 通道容量
	anchorCheckEvery = 16      // 每隔多少轮做一次 anchor 全量校验（GC 前后各一次）
	writerGCEvery    = 8       // 写屏障 worker 每 N 轮显式 GC 并验证 old→young 存活
)

const phaseEvery = 250 * time.Millisecond // GOMEMLIMIT 阶段轮换间隔

const (
	minLimit = int64(8) << 20 // GOMEMLIMIT 下限，避免异常小的估算
)

type stormNode struct {
	id    int64
	magic uint64
	tag   string
	next  *stormNode
}

type tempBatch struct {
	worker int64
	round  int64
	magic  uint64
	node   *stormNode
	med    []uint64
	big    []byte
	nbytes int64
}

type anchorNode struct {
	id     int64
	v      int64
	canary uint64
	tag    string
	left   *anchorNode
	right  *anchorNode
	next   *anchorNode

	// 以下两个字段只由写屏障 worker 在 g.mu 写锁内更新，校验方在读锁内读取。
	link *stormNode  // old→young 普通指针边
	box  interface{} // old→young interface 装箱（*stormNode / []*stormNode）
}

type anchorGraph struct {
	mu    sync.RWMutex
	nodes []*anchorNode
	root  *anchorNode
	chain *anchorNode
	m     map[int64]interface{} // 稳定键→*anchorNode；动态键→*stormNode（old→young）
	slots []*anchorNode
}

var (
	beats           int64
	started         int64
	finished        int64
	gSeed           uint64
	stormID         int64
	tempAllocs      int64
	bigAllocs       int64
	tempBytes       int64
	bigBytes        int64
	releasedBatches int64
	releasedBytes   int64
	heldFinal       int64
	anchorChecks    int64
	writerRounds    int64
	youngVerified   int64
	limitPhases     int64
	limitSets       int64
	heapPeak        int64
	anchorFinCount  int64

	tempCh      chan *tempBatch
	anchorFinCh chan int64
	anchor      *anchorGraph
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

func magicOf(id int64) uint64 {
	return uint64(id)*0x9E3779B97F4A7C15 + 0x51ED270B
}

func canaryOf(id int64) uint64 {
	return magicOf(id) ^ 0xC0FFEE5EEDBEEF
}

func nextStormID() int64 {
	return atomic.AddInt64(&stormID, 1)
}

func newStorm(tag string) *stormNode {
	id := nextStormID()
	return &stormNode{id: id, magic: magicOf(id), tag: tag}
}

func stormOK(n *stormNode, tag string) bool {
	return n != nil && n.id > 0 && n.tag == tag && n.magic == magicOf(n.id)
}

func noteHeap(v uint64) {
	v64 := int64(v)
	for {
		old := atomic.LoadInt64(&heapPeak)
		if v64 <= old {
			return
		}
		if atomic.CompareAndSwapInt64(&heapPeak, old, v64) {
			return
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

// finAnchor 是可达 anchor 被终结的探针：正常永远不应执行。
func finAnchor(n *anchorNode) {
	atomic.AddInt64(&anchorFinCount, 1)
	select {
	case anchorFinCh <- n.id:
	default:
	}
}

func buildAnchorGraph() *anchorGraph {
	g := &anchorGraph{nodes: make([]*anchorNode, treeN+chainN)}
	for i := range g.nodes {
		id := int64(i)
		g.nodes[i] = &anchorNode{id: id, v: id*3 + 1, canary: canaryOf(id), tag: "u03a"}
	}
	for i := 0; i < treeN; i++ {
		if l := 2*i + 1; l < treeN {
			g.nodes[i].left = g.nodes[l]
		}
		if r := 2*i + 2; r < treeN {
			g.nodes[i].right = g.nodes[r]
		}
	}
	g.root = g.nodes[0]

	prev := g.nodes[treeN]
	g.chain = prev
	for i := 1; i < chainN; i++ {
		prev.next = g.nodes[treeN+i]
		prev = prev.next
	}

	g.m = make(map[int64]interface{}, mapN+dynKeys)
	for k := 0; k < mapN; k++ {
		g.m[int64(k)] = g.nodes[(k*37)%treeN]
	}

	g.slots = make([]*anchorNode, dynSlots)
	for i := range g.slots {
		g.slots[i] = g.nodes[1+(i*7919)%(treeN-1)]
	}

	runtime.SetFinalizer(g.root, finAnchor)
	runtime.SetFinalizer(g.chain, finAnchor)
	runtime.SetFinalizer(g.nodes[treeN/2], finAnchor)
	return g
}

// verify 全量 canary 校验：树（内容/结构/子边身份）、链、稳定 map、动态 old→young 槽位。
// 树/链不可变，可在锁外遍历；link/box/map 在读锁内取一致快照。
func (g *anchorGraph) verify(phase string) {
	for i := 0; i < treeN; i++ {
		n := g.nodes[i]
		if n == nil || n.id != int64(i) || n.v != int64(i)*3+1 || n.canary != canaryOf(int64(i)) || n.tag != "u03a" {
			failf("phase=%s seed=%d context=anchor-tree: node[%d] canary/content corrupted", phase, gSeed, i)
		}
		var wantL, wantR *anchorNode
		if l := 2*i + 1; l < treeN {
			wantL = g.nodes[l]
		}
		if r := 2*i + 2; r < treeN {
			wantR = g.nodes[r]
		}
		if n.left != wantL || n.right != wantR {
			failf("phase=%s seed=%d context=anchor-tree: node[%d] child edges corrupted", phase, gSeed, i)
		}
		if n.next != nil {
			failf("phase=%s seed=%d context=anchor-tree: node[%d] stray chain edge", phase, gSeed, i)
		}
	}

	for i := 0; i < chainN; i++ {
		n := g.nodes[treeN+i]
		if n == nil || n.id != int64(treeN+i) || n.v != int64(treeN+i)*3+1 ||
			n.canary != canaryOf(int64(treeN+i)) || n.tag != "u03a" {
			failf("phase=%s seed=%d context=anchor-chain: node[%d] canary/content corrupted", phase, gSeed, i)
		}
		if i+1 < chainN {
			if n.next != g.nodes[treeN+i+1] {
				failf("phase=%s seed=%d context=anchor-chain: node[%d] next edge corrupted", phase, gSeed, i)
			}
		} else if n.next != nil {
			failf("phase=%s seed=%d context=anchor-chain: tail next not nil", phase, gSeed)
		}
	}

	g.mu.RLock()
	defer g.mu.RUnlock()
	for k := 0; k < mapN; k++ {
		v, ok := g.m[int64(k)]
		if !ok {
			failf("phase=%s seed=%d context=anchor-map: key %d missing", phase, gSeed, k)
		}
		an, ok := v.(*anchorNode)
		if !ok || an != g.nodes[(k*37)%treeN] {
			failf("phase=%s seed=%d context=anchor-map: key %d value corrupted", phase, gSeed, k)
		}
	}
	for i := 0; i < dynKeys; i++ {
		v, ok := g.m[int64(mapN+i)]
		if !ok {
			continue // 写屏障 worker 尚未覆盖到
		}
		sn, ok := v.(*stormNode)
		if !ok || !stormOK(sn, "u03y") {
			failf("phase=%s seed=%d context=anchor-map: dynamic key %d old->young value corrupted", phase, gSeed, mapN+i)
		}
	}
	for i, slot := range g.slots {
		if slot.link != nil {
			cnt := 0
			for n := slot.link; n != nil; n = n.next {
				if !stormOK(n, "u03y") {
					failf("phase=%s seed=%d context=anchor-slot[%d]: young chain corrupted at %d", phase, gSeed, i, cnt)
				}
				cnt++
				if cnt > youngN {
					failf("phase=%s seed=%d context=anchor-slot[%d]: young chain too long", phase, gSeed, i)
				}
			}
			if cnt != youngN {
				failf("phase=%s seed=%d context=anchor-slot[%d]: young chain len=%d want=%d", phase, gSeed, i, cnt, youngN)
			}
		}
		switch b := slot.box.(type) {
		case nil:
		case *stormNode:
			if b != slot.link || !stormOK(b, "u03y") {
				failf("phase=%s seed=%d context=anchor-slot[%d]: boxed old->young pointer lost", phase, gSeed, i)
			}
		case []*stormNode:
			if len(b) != 3 || b[0] != slot.link || !stormOK(b[0], "u03y") ||
				!stormOK(b[1], "u03y") || !stormOK(b[2], "u03y") {
				failf("phase=%s seed=%d context=anchor-slot[%d]: boxed []*stormNode lost", phase, gSeed, i)
			}
		default:
			failf("phase=%s seed=%d context=anchor-slot[%d]: unexpected box type %T", phase, gSeed, i, slot.box)
		}
	}
	atomic.AddInt64(&anchorChecks, 1)
}

// ---- 写屏障 worker：并发 old→young 改边 ----

type wbWriter struct {
	seed    uint64
	slot    int64
	mapKey  int64
	rounds  int64
	slotIdx int64
}

// writerAllocYoung 构造一条 young 链并把它挂到老年代 anchor 图上。
// 刻意 noinline 且只返回非指针值，使 GC 时唯一引用路径是 anchor 图本身。
//
//go:noinline
func writerAllocYoung(g *anchorGraph, w *wbWriter, r int64) {
	head := newStorm("u03y")
	cur := head
	for i := 1; i < youngN; i++ {
		n := newStorm("u03y")
		cur.next = n
		cur = n
	}
	slotIdx := int(r) % len(g.slots)
	k := int64(mapN) + r%int64(dynKeys)
	g.mu.Lock()
	g.slots[slotIdx].link = head
	g.slots[slotIdx].box = []*stormNode{head, head.next, cur}
	g.m[int64(k)] = head
	// 稳定键删除/重插：走 map 写路径。
	k2 := (r * 7) % mapN
	v := g.m[k2]
	delete(g.m, k2)
	g.m[k2] = v
	g.mu.Unlock()
	w.slotIdx = int64(slotIdx)
	w.mapKey = k
}

// wbGCVerify 在本地不再持有 young 指针后触发 GC，再从 anchor 图读回并校验存活。
//
//go:noinline
func wbGCVerify(g *anchorGraph, slotIdx int64, mapKey int64) {
	runtime.GC()

	g.mu.RLock()
	head := g.slots[int(slotIdx)].link
	box := g.slots[int(slotIdx)].box
	mv := g.m[mapKey]
	g.mu.RUnlock()

	if !stormOK(head, "u03y") {
		failf("phase=wb-post-gc seed=%d context=old->young: head lost/corrupted after GC", gSeed)
	}
	cnt := 0
	for n := head; n != nil; n = n.next {
		if !stormOK(n, "u03y") {
			failf("phase=wb-post-gc seed=%d context=old->young: young node %d corrupted after GC", gSeed, cnt)
		}
		cnt++
		if cnt > youngN {
			failf("phase=wb-post-gc seed=%d context=old->young: young chain too long after GC", gSeed)
		}
	}
	if cnt != youngN {
		failf("phase=wb-post-gc seed=%d context=old->young: young chain len=%d want=%d after GC", gSeed, cnt, youngN)
	}
	sn, ok := mv.(*stormNode)
	if !ok || sn != head {
		failf("phase=wb-post-gc seed=%d context=old->young: map entry lost after GC", gSeed)
	}
	switch b := box.(type) {
	case *stormNode:
		if b != head {
			failf("phase=wb-post-gc seed=%d context=old->young: boxed pointer mismatch after GC", gSeed)
		}
	case []*stormNode:
		if len(b) != 3 || b[0] != head || b[1] == nil || b[2] == nil ||
			!stormOK(b[1], "u03y") || !stormOK(b[2], "u03y") {
			failf("phase=wb-post-gc seed=%d context=old->young: boxed slice lost after GC", gSeed)
		}
	default:
		failf("phase=wb-post-gc seed=%d context=old->young: unexpected box type %T after GC", gSeed, box)
	}
	atomic.AddInt64(&youngVerified, 1)
}

func (w *wbWriter) round(g *anchorGraph, r int64) {
	writerAllocYoung(g, w, r)
	if r%writerGCEvery == 0 {
		wbGCVerify(g, w.slotIdx, w.mapKey)
	}
	w.rounds++
	atomic.AddInt64(&writerRounds, 1)
	atomic.AddInt64(&beats, 1)
}

func (w *wbWriter) run(g *anchorGraph, stop <-chan struct{}, seconds int, deadline time.Time, fixed int64) {
	r := int64(0)
	for {
		select {
		case <-stop:
			return
		default:
		}
		if seconds > 0 {
			if time.Now().After(deadline) {
				return
			}
		} else if r >= fixed {
			return
		}
		w.round(g, r)
		r++
	}
}

// ---- 分配风暴 worker 与独立 verifier ----

type tempWorker struct {
	id   int64
	seed uint64
	next int64
}

func (w *tempWorker) makeBatch(r int64) *tempBatch {
	n := newStorm("u03t")
	magic := n.magic

	medLen := 256 + int(magic%1793)
	med := make([]uint64, medLen)
	for i := range med {
		med[i] = magic ^ (uint64(i)*0x9E3779B1 + 1)
	}

	bigLen := (1 << 20) + int(magic%(3<<20))
	big := make([]byte, bigLen)
	for i := 0; i < len(big); i += 4096 {
		big[i] = byte(magic>>uint((i/4096)%56)) + byte(i/4096)
	}
	big[len(big)-1] = byte(magic ^ 0xA5)

	return &tempBatch{
		worker: w.id,
		round:  r,
		magic:  magic,
		node:   n,
		med:    med,
		big:    big,
		nbytes: int64(medLen*8 + bigLen),
	}
}

func verifyBatch(phase string, b *tempBatch) {
	if b == nil || !stormOK(b.node, "u03t") || b.node.magic != b.magic {
		failf("phase=%s seed=%d worker=%d round=%d context=temp-node: canary corrupted", phase, gSeed, b.worker, b.round)
	}
	if len(b.med) < 256 {
		failf("phase=%s seed=%d worker=%d round=%d context=temp-med: len=%d too small", phase, gSeed, b.worker, b.round, len(b.med))
	}
	for _, i := range []int{0, len(b.med) / 2, len(b.med) - 1} {
		want := b.magic ^ (uint64(i)*0x9E3779B1 + 1)
		if b.med[i] != want {
			failf("phase=%s seed=%d worker=%d round=%d context=temp-med: canary at %d corrupted", phase, gSeed, b.worker, b.round, i)
		}
	}
	bl := len(b.big)
	if bl < 1<<20 || bl > 4<<20 {
		failf("phase=%s seed=%d worker=%d round=%d context=temp-big: len=%d out of range", phase, gSeed, b.worker, b.round, bl)
	}
	for _, i := range []int{0, (bl / 3) &^ 4095, (2 * bl / 3) &^ 4095} {
		want := byte(b.magic>>uint((i/4096)%56)) + byte(i/4096)
		if b.big[i] != want {
			failf("phase=%s seed=%d worker=%d round=%d context=temp-big: canary at %d corrupted", phase, gSeed, b.worker, b.round, i)
		}
	}
	if b.big[bl-1] != byte(b.magic^0xA5) {
		failf("phase=%s seed=%d worker=%d round=%d context=temp-big: tail canary corrupted", phase, gSeed, b.worker, b.round)
	}
}

func (w *tempWorker) round(r int64) {
	b := w.makeBatch(r)
	verifyBatch("immediate", b)
	atomic.AddInt64(&tempAllocs, 1)
	atomic.AddInt64(&bigAllocs, 1)
	atomic.AddInt64(&tempBytes, b.nbytes)
	atomic.AddInt64(&bigBytes, int64(len(b.big)))
	tempCh <- b // 交给独立 verifier 短暂持有后释放
	b = nil

	// 周期全量 anchor 校验：GC 前一次、GC 后一次。
	if r%anchorCheckEvery == 0 {
		anchor.verify("pre-gc")
		runtime.GC()
		anchor.verify("post-gc")
	}
	atomic.AddInt64(&beats, 1)
}

func (w *tempWorker) run(seconds int, deadline time.Time, fixed int64) {
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
		w.next = r + 1
		r++
	}
}

func releaseBatch(b *tempBatch) {
	atomic.AddInt64(&releasedBatches, 1)
	atomic.AddInt64(&releasedBytes, b.nbytes)
	b.node = nil
	b.med = nil
	b.big = nil
}

func verifier(wg *sync.WaitGroup) {
	defer wg.Done()
	held := make([]*tempBatch, 0, holdDepth)
	for b := range tempCh {
		verifyBatch("post-handoff", b)
		held = append(held, b)
		if len(held) > holdDepth {
			old := held[0]
			held = held[1:]
			verifyBatch("held", old)
			releaseBatch(old)
		}
		atomic.AddInt64(&beats, 1)
	}
	for _, b := range held {
		verifyBatch("held-drain", b)
		releaseBatch(b)
	}
	atomic.StoreInt64(&heldFinal, int64(len(held)))
}

// ---- GOMEMLIMIT 动态贴近 / 监控 ----

func governor(stop <-chan struct{}, wg *sync.WaitGroup) {
	defer wg.Done()
	factors := [...]float64{1.0, 1.1, 1.2}
	i := 0
	t := time.NewTicker(phaseEvery)
	defer t.Stop()
	for {
		select {
		case <-stop:
			return
		case <-t.C:
			var m runtime.MemStats
			runtime.ReadMemStats(&m)
			noteHeap(m.HeapAlloc)
			live := int64(m.HeapAlloc)
			if live < 1<<20 {
				live = 1 << 20
			}
			lim := int64(float64(live) * factors[i%len(factors)])
			i++
			if lim < minLimit {
				lim = minLimit
			}
			debug.SetMemoryLimit(lim)
			atomic.AddInt64(&limitPhases, 1)
			atomic.AddInt64(&limitSets, 1)
		}
	}
}

func watchAnchorFinalizers(stop <-chan struct{}, wg *sync.WaitGroup) {
	defer wg.Done()
	for {
		select {
		case id := <-anchorFinCh:
			failf("phase=finalizer seed=%d context=anchor: reachable anchor node finalized id=%d (anchor_finalized=%d)",
				gSeed, id, atomic.LoadInt64(&anchorFinCount))
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
				failf("phase=watchdog seed=%d context=progress: no heartbeat for %s (beats=%d limits=%d)",
					gSeed, time.Since(lastChange), cur, atomic.LoadInt64(&limitSets))
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

// gcPauses 读 /gc/pauses:seconds 直方图，只用于记录 assist/GC 活跃度，不断言。
func gcPauses() (count uint64, seconds float64) {
	samples := []metrics.Sample{{Name: "/gc/pauses:seconds"}}
	metrics.Read(samples)
	if samples[0].Value.Kind() != metrics.KindFloat64Histogram {
		return 0, 0
	}
	h := samples[0].Value.Float64Histogram()
	for i, c := range h.Counts {
		if c == 0 || i+1 >= len(h.Buckets) {
			continue
		}
		lo, hi := h.Buckets[i], h.Buckets[i+1]
		var mid float64
		switch {
		case math.IsInf(lo, -1):
			mid = hi / 2
		case math.IsInf(hi, 1):
			mid = lo * 2
		default:
			mid = (lo + hi) / 2
		}
		seconds += mid * float64(c)
		count += c
	}
	return count, seconds
}

func main() {
	setupCrashOutput()
	seconds := envInt("STRESS_SECONDS", 60, 0, 86400)
	workers := envInt("STRESS_WORKERS", 4, 1, 32)
	hb := envInt("STRESS_HEARTBEAT", 5, 1, 60)
	seed := envSeed(1)
	fixed := int64(envInt("STRESS_FIXED_ROUNDS", 24, 1, 1000000))
	gSeed = seed

	tempCh = make(chan *tempBatch, tempChanCap)
	anchorFinCh = make(chan int64, 1024)

	// 记录并原样恢复 GCPercent / MemoryLimit。
	origGOGC := debug.SetGCPercent(-1)
	debug.SetGCPercent(origGOGC)
	origLimit := debug.SetMemoryLimit(-1)

	baseGoroutines := runtime.NumGoroutine()

	anchor = buildAnchorGraph()
	runtime.GC()
	runtime.GC()
	var m0 runtime.MemStats
	runtime.ReadMemStats(&m0)
	noteHeap(m0.HeapAlloc)
	live := int64(m0.HeapAlloc)
	if live <= 0 {
		failf("phase=preflight seed=%d context=memory: live heap estimate=%d", seed, live)
	}
	initLimit := int64(float64(live) * 1.0)
	if initLimit < minLimit {
		initLimit = minLimit
	}
	debug.SetMemoryLimit(initLimit) // 开局即贴近 live heap，立刻进入 assist 压力
	atomic.AddInt64(&limitPhases, 1)
	atomic.AddInt64(&limitSets, 1)

	fmt.Printf("scenario=%s phase=preflight go=%s goos=%s goarch=%s gomaxprocs=%d seed=%d seconds=%d workers=%d fixed_rounds=%d heartbeat=%ds tree=%d chain=%d map=%d live=%d init_limit=%d orig_limit=%d orig_gogc=%d\n",
		scenario, runtime.Version(), runtime.GOOS, runtime.GOARCH, runtime.GOMAXPROCS(0), seed, seconds, workers, fixed, hb,
		treeN, chainN, mapN, live, initLimit, origLimit, origGOGC)

	stopAux := make(chan struct{})
	finStop := make(chan struct{})

	var finWG sync.WaitGroup
	finWG.Add(1)
	go watchAnchorFinalizers(finStop, &finWG)

	var verWG sync.WaitGroup
	verWG.Add(1)
	go verifier(&verWG)

	var auxWG sync.WaitGroup
	auxWG.Add(1)
	go governor(stopAux, &auxWG)
	auxWG.Add(1)
	go watchdog(time.Duration(hb)*time.Second, stopAux, &auxWG)

	writer := &wbWriter{seed: seed}
	auxWG.Add(1)
	go func() {
		defer auxWG.Done()
		writer.run(anchor, stopAux, seconds, time.Now().Add(time.Duration(seconds)*time.Second), fixed)
	}()

	deadline := time.Now().Add(time.Duration(seconds) * time.Second)
	ws := make([]*tempWorker, workers)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		ws[i] = &tempWorker{id: int64(i), seed: seed*1000 + uint64(i) + 1}
		wg.Add(1)
		go func(w *tempWorker) {
			defer wg.Done()
			w.run(seconds, deadline, fixed)
		}(ws[i])
	}

	wg.Wait()
	close(stopAux)
	auxWG.Wait()
	close(tempCh)
	verWG.Wait()

	// quiesce：恢复运行环境原值。
	debug.SetMemoryLimit(origLimit)
	debug.SetGCPercent(origGOGC)

	// 收尾全量校验：GC 前后各一次（含写屏障 worker 留下的最后一条 young 链）。
	anchor.verify("pre-final-gc")
	runtime.GC()
	anchor.verify("post-final-gc")

	// 给可达 anchor 的 finalizer 探针一个宽限窗口；正常情况下计数必须为 0。
	for i := 0; i < 3; i++ {
		runtime.GC()
		time.Sleep(10 * time.Millisecond)
		if atomic.LoadInt64(&anchorFinCount) != 0 {
			break
		}
	}
	if n := atomic.LoadInt64(&anchorFinCount); n != 0 {
		failf("phase=final seed=%d context=finalizer: %d reachable anchor finalizer(s) fired", seed, n)
	}
	close(finStop)
	finWG.Wait()

	if s, f := atomic.LoadInt64(&started), atomic.LoadInt64(&finished); s != int64(workers) || f != s {
		failf("phase=final seed=%d context=workers: started=%d finished=%d want=%d", seed, s, f, workers)
	}

	var roundsTotal int64
	for _, w := range ws {
		roundsTotal += w.next
		if seconds == 0 && w.next != fixed {
			failf("phase=final seed=%d worker=%d context=rounds: rounds=%d want=%d", seed, w.id, w.next, fixed)
		}
	}
	if roundsTotal == 0 {
		failf("phase=final seed=%d context=rounds: no rounds completed", seed)
	}
	if atomic.LoadInt64(&tempAllocs) == 0 || atomic.LoadInt64(&bigAllocs) == 0 {
		failf("phase=final seed=%d context=alloc: no temporary allocations recorded", seed)
	}

	ng := waitGoroutines(baseGoroutines, 5*time.Second)
	if ng > baseGoroutines+4 {
		failf("phase=final seed=%d context=goroutines: baseline=%d after=%d leaked", seed, baseGoroutines, ng)
	}

	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	noteHeap(m.HeapAlloc)
	pauseCount, pauseSeconds := gcPauses()
	fmt.Printf("metrics: rounds=%d workers=%d writer_rounds=%d young_verified=%d anchor_checks=%d anchor_finalized=%d temp_allocs=%d temp_bytes=%d big_allocs=%d big_bytes=%d released_batches=%d released_bytes=%d held_final=%d assist_alloc_bytes=%d num_gc=%d heap_peak=%d heap_alloc=%d heap_objects=%d limit_phases=%d limit_sets=%d gc_pause_count=%d gc_pause_seconds=%.6f goroutines=%d\n",
		roundsTotal, workers, atomic.LoadInt64(&writerRounds), atomic.LoadInt64(&youngVerified),
		atomic.LoadInt64(&anchorChecks), atomic.LoadInt64(&anchorFinCount),
		atomic.LoadInt64(&tempAllocs), atomic.LoadInt64(&tempBytes),
		atomic.LoadInt64(&bigAllocs), atomic.LoadInt64(&bigBytes),
		atomic.LoadInt64(&releasedBatches), atomic.LoadInt64(&releasedBytes),
		atomic.LoadInt64(&heldFinal), atomic.LoadInt64(&tempBytes),
		m.NumGC, atomic.LoadInt64(&heapPeak), m.HeapAlloc, m.HeapObjects,
		atomic.LoadInt64(&limitPhases), atomic.LoadInt64(&limitSets),
		pauseCount, pauseSeconds, ng)
	fmt.Printf("STRESS PASS %s\n", scenario)
}
