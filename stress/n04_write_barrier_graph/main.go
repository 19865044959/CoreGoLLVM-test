// N04: 写屏障/老年代对象图长跑。
// 每个 worker 构造一棵大完全二叉树 + 链 + map[int64]*node + []*node slab，先晋升老年代；
// 随后持续做 old→young 改边、子树旋转、批量 []*node copy、interface 装箱替换、
// map 重插 young 指针；独立 GC churn 与每 2 轮显式 runtime.GC 并发。
// 校验存活节点内容、度、子节点集合、连通性与 checksum。
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
)

const (
	scenario = "n04"
	treeN    = 4095
	chainN   = 1024
	mapN     = 256
	slabN    = 4096
	youngN   = 64
	batchN   = 1024
)

type gnode struct {
	id    int64
	v     int64
	tag   string
	left  *gnode
	right *gnode
	next  *gnode // 链
	link  *gnode // old→young 观测边
	box   interface{}
}

type worker struct {
	id     int64
	seed   uint64
	next   int64
	rounds int64
	checks int64

	nodes     []*gnode
	root      *gnode
	chainHead *gnode
	m         map[int64]*gnode
	slab      []*gnode
	batchSrc  []*gnode
	batchDst  []*gnode

	lastYoung  *gnode
	lastAnchor *gnode
	lastOther  *gnode
	lastMapKey int64
}

var (
	beats    int64
	gcCycles int64
	started  int64
	finished int64
	gSeed    uint64
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

func newGN(id int64, tag string) *gnode {
	return &gnode{id: id, v: id*3 + 1, tag: tag}
}

// build 构造对象图；随后两轮 GC 让它晋升老年代。
func (w *worker) build() {
	w.nodes = make([]*gnode, treeN)
	for i := range w.nodes {
		w.nodes[i] = newGN(int64(i), "n04")
	}
	for i := 0; i < treeN; i++ {
		if l := 2*i + 1; l < treeN {
			w.nodes[i].left = w.nodes[l]
		}
		if r := 2*i + 2; r < treeN {
			w.nodes[i].right = w.nodes[r]
		}
	}
	w.root = w.nodes[0]

	var prev *gnode
	for i := 0; i < chainN; i++ {
		n := newGN(int64(treeN+i), "n04")
		if prev == nil {
			w.chainHead = n
		} else {
			prev.next = n
		}
		prev = n
	}

	w.m = make(map[int64]*gnode, mapN)
	for k := 0; k < mapN; k++ {
		w.m[int64(k)] = w.nodes[(k*37)%treeN]
	}
	w.slab = make([]*gnode, slabN)
	for i := range w.slab {
		w.slab[i] = w.nodes[(i*97)%treeN]
	}
	w.batchSrc = make([]*gnode, batchN)
	w.batchDst = make([]*gnode, batchN)
	for i := range w.batchSrc {
		w.batchSrc[i] = w.nodes[(i*13)%treeN]
	}
}

// verifyYoung 校验 old→young 边与年轻链在 GC 后仍然完整可达。
func (w *worker) verifyYoung(phase string, r int64) {
	y := w.lastYoung
	if y == nil {
		failf("phase=%s worker=%d round=%d seed=%d: young chain nil", phase, w.id, r, w.seed)
	}
	for i := 0; i < youngN; i++ {
		if y == nil || y.tag != "n04y" || y.v != y.id*3+1 {
			failf("phase=%s worker=%d round=%d seed=%d gomaxprocs=%d: young[%d] corrupted",
				phase, w.id, r, w.seed, runtime.GOMAXPROCS(0), i)
		}
		y = y.next
	}
	if y != nil {
		failf("phase=%s worker=%d round=%d seed=%d: young chain longer than %d", phase, w.id, r, w.seed, youngN)
	}
	if w.lastAnchor == nil || w.lastAnchor.link != w.lastYoung {
		failf("phase=%s worker=%d round=%d seed=%d: old->young link lost", phase, w.id, r, w.seed)
	}
	if got, ok := w.lastAnchor.box.(*gnode); !ok || got != w.lastYoung {
		failf("phase=%s worker=%d round=%d seed=%d: interface-boxed old->young pointer lost", phase, w.id, r, w.seed)
	}
	arr, ok := w.lastOther.box.([]*gnode)
	if !ok || len(arr) != 3 || arr[0] != w.lastYoung || arr[1] != w.lastYoung.next || arr[2] == nil ||
		arr[2].tag != "n04y" || arr[2].v != arr[2].id*3+1 {
		failf("phase=%s worker=%d round=%d seed=%d: interface-boxed []*node lost/corrupted", phase, w.id, r, w.seed)
	}
	if n, ok := w.m[w.lastMapKey]; !ok || n != w.lastYoung {
		failf("phase=%s worker=%d round=%d seed=%d key=%d: map old->young value lost", phase, w.id, r, w.seed, w.lastMapKey)
	}
	for i := range w.batchDst {
		n := w.batchDst[i]
		if n == nil || n.v != n.id*3+1 {
			failf("phase=%s worker=%d round=%d seed=%d: batch copy dst[%d] corrupted", phase, w.id, r, w.seed, i)
		}
	}
}

// verifyGraph 全量校验树（内容/度/子节点集合/连通性/checksum）、链、map、slab。
func (w *worker) verifyGraph(phase string) {
	visited := make([]bool, treeN)
	stack := make([]*gnode, 0, treeN)
	stack = append(stack, w.root)
	var cnt, sum int64
	for len(stack) > 0 {
		n := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if n == nil {
			failf("phase=%s worker=%d seed=%d: nil tree node", phase, w.id, w.seed)
		}
		if n.id < 0 || n.id >= treeN {
			failf("phase=%s worker=%d seed=%d: tree id=%d out of range", phase, w.id, w.seed, n.id)
		}
		if visited[n.id] {
			failf("phase=%s worker=%d seed=%d: duplicate tree node id=%d (cycle/multi-parent)", phase, w.id, w.seed, n.id)
		}
		visited[n.id] = true
		if n.tag != "n04" || n.v != n.id*3+1 {
			failf("phase=%s worker=%d seed=%d: tree node id=%d content corrupted", phase, w.id, w.seed, n.id)
		}
		deg := 0
		if n.left != nil {
			deg++
		}
		if n.right != nil {
			deg++
		}
		if n.id < treeN/2 {
			if deg != 2 {
				failf("phase=%s worker=%d seed=%d: internal node id=%d degree=%d want=2", phase, w.id, w.seed, n.id, deg)
			}
			wantL, wantR := n.id*2+1, n.id*2+2
			gl, gr := n.left.id, n.right.id
			if !(gl == wantL && gr == wantR) && !(gl == wantR && gr == wantL) {
				failf("phase=%s worker=%d seed=%d: node id=%d child set=(%d,%d) want={%d,%d}",
					phase, w.id, w.seed, n.id, gl, gr, wantL, wantR)
			}
			stack = append(stack, n.left, n.right)
		} else if deg != 0 {
			failf("phase=%s worker=%d seed=%d: leaf node id=%d degree=%d want=0", phase, w.id, w.seed, n.id, deg)
		}
		cnt++
		sum += n.id
	}
	if cnt != treeN {
		failf("phase=%s worker=%d seed=%d: reachable tree nodes=%d want=%d", phase, w.id, w.seed, cnt, treeN)
	}
	if want := int64(treeN) * (treeN - 1) / 2; sum != want {
		failf("phase=%s worker=%d seed=%d: tree id checksum=%d want=%d", phase, w.id, w.seed, sum, want)
	}

	c := w.chainHead
	for i := 0; i < chainN; i++ {
		if c == nil || c.id != int64(treeN+i) || c.tag != "n04" || c.v != c.id*3+1 {
			failf("phase=%s worker=%d seed=%d: chain[%d] corrupted", phase, w.id, w.seed, i)
		}
		c = c.next
	}
	if c != nil {
		failf("phase=%s worker=%d seed=%d: chain longer than %d", phase, w.id, w.seed, chainN)
	}

	if len(w.m) != mapN {
		failf("phase=%s worker=%d seed=%d: map len=%d want=%d", phase, w.id, w.seed, len(w.m), mapN)
	}
	for k := int64(0); k < mapN; k++ {
		n, ok := w.m[k]
		if !ok || n == nil {
			failf("phase=%s worker=%d seed=%d: map[%d] missing", phase, w.id, w.seed, k)
		}
		if (n.tag != "n04" && n.tag != "n04y") || n.v != n.id*3+1 {
			failf("phase=%s worker=%d seed=%d: map[%d] value corrupted id=%d", phase, w.id, w.seed, k, n.id)
		}
	}
	for i, n := range w.slab {
		if n == nil || n.tag != "n04" || n.v != n.id*3+1 {
			failf("phase=%s worker=%d seed=%d: slab[%d] corrupted", phase, w.id, w.seed, i)
		}
	}
	atomic.AddInt64(&w.checks, 1)
}

func (w *worker) round(r int64) {
	// 年轻对象链。
	baseID := int64(treeN) + r*youngN
	var yhead, ytail *gnode
	for i := int64(0); i < youngN; i++ {
		y := newGN(baseID+i, "n04y")
		if ytail == nil {
			yhead = y
		} else {
			ytail.next = y
		}
		ytail = y
	}

	// young→old 边。
	for i, y := 0, yhead; y != nil; i, y = i+1, y.next {
		y.link = w.nodes[(int(r)+i*31)%treeN]
	}

	// old→young：普通指针字段 + interface 装箱（*node 与 []*node）。
	anchor := w.nodes[(int(r)*7)%treeN]
	anchor.link = yhead
	anchor.box = yhead
	other := w.nodes[(int(r)*13+5)%treeN]
	other.box = []*gnode{yhead, yhead.next, ytail}

	// 子树旋转：只交换左右子节点，节点集合与度不变。
	rot := w.nodes[1+int(r%3)]
	rot.left, rot.right = rot.right, rot.left

	// map 中的 old→young 边与删除/重插。
	k := int64((int(r) * 11) % mapN)
	w.m[k] = yhead
	k2 := int64((int(r)*13 + 7) % mapN)
	delete(w.m, k2)
	w.m[k2] = w.nodes[(int(k2)*37)%treeN]

	// 批量 copy（bulk write barrier）：dst 预分配 slice，src 混合老对象与 young。
	shift := int(r) % batchN
	w.batchSrc[shift] = yhead
	copy(w.batchDst, w.batchSrc)
	cp := append([]*gnode(nil), w.batchSrc...)
	if len(cp) != batchN || cp[shift] != yhead {
		failf("phase=bulk-copy worker=%d round=%d seed=%d: append copy lost young pointer", w.id, r, w.seed)
	}
	for i := range w.batchDst {
		if w.batchDst[i] == nil || w.batchDst[i] != w.batchSrc[i] {
			failf("phase=bulk-copy worker=%d round=%d seed=%d: dst[%d] mismatch after copy", w.id, r, w.seed, i)
		}
		if w.batchDst[i].v != w.batchDst[i].id*3+1 {
			failf("phase=bulk-copy worker=%d round=%d seed=%d: dst[%d] content corrupted", w.id, r, w.seed, i)
		}
	}

	w.lastYoung, w.lastAnchor, w.lastOther, w.lastMapKey = yhead, anchor, other, k

	if r%2 == 0 {
		runtime.GC()
		w.verifyYoung("post-gc", r)
	}
	if r%8 == 0 {
		w.verifyGraph("periodic")
	}
	w.rounds++
	atomic.AddInt64(&beats, 1)
}

func (w *worker) run(seconds int, deadline time.Time, fixed int64) {
	atomic.AddInt64(&started, 1)
	defer atomic.AddInt64(&finished, 1)
	w.build()
	runtime.GC()
	runtime.GC() // 晋升到老年代

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
	runtime.GC()
	if w.next > 0 {
		w.verifyYoung("final", w.next-1)
	}
	w.verifyGraph("final")
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

func main() {
	setupCrashOutput()
	seconds := envInt("STRESS_SECONDS", 60, 0, 86400)
	workers := envInt("STRESS_WORKERS", 4, 1, 32)
	hb := envInt("STRESS_HEARTBEAT", 5, 1, 60)
	seed := envSeed(1)
	fixed := int64(envInt("STRESS_FIXED_ROUNDS", 32, 1, 1000000))
	gSeed = seed

	fmt.Printf("scenario=%s phase=preflight go=%s goos=%s goarch=%s gomaxprocs=%d seed=%d seconds=%d workers=%d fixed_rounds=%d heartbeat=%ds tree=%d chain=%d map=%d slab=%d young=%d batch=%d\n",
		scenario, runtime.Version(), runtime.GOOS, runtime.GOARCH, runtime.GOMAXPROCS(0), seed, seconds, workers, fixed, hb,
		treeN, chainN, mapN, slabN, youngN, batchN)

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

	var roundsTotal, checks int64
	for _, w := range ws {
		roundsTotal += w.rounds
		checks += atomic.LoadInt64(&w.checks)
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
	fmt.Printf("metrics: rounds=%d graph_checks=%d workers=%d tree=%d chain=%d map=%d slab=%d gc_churn=%d heap_alloc=%d heap_objects=%d num_gc=%d goroutines=%d\n",
		roundsTotal, checks, workers, treeN, chainN, mapN, slabN,
		atomic.LoadInt64(&gcCycles), m.HeapAlloc, m.HeapObjects, m.NumGC, ng)
	fmt.Printf("STRESS PASS %s\n", "n04_write_barrier_graph")
}
