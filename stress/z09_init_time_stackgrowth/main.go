// Z09: init 期栈增长（go#41816）。
//
// Go 1.15 回归：init 函数里 mapassign 触发栈复制时，init 帧的指针映射错误，
// 运行时报 “invalid pointer found on stack”。本场景把 init 期最容易出错的三种组合
// 拆到同一包的多个 init（init_a.go / init_b.go）：
//
//	init_1：大局部帧（[4096]*node 数组 + map/slice），函数中部调用深递归强制 morestack
//	        （栈复制带走含 4096 个指针的 init 帧），增长后逐项校验 canary；
//	init_2：map[int]*node 循环写入 1000 项（mapassign 触发扩容），期间反复插入
//	        noinline 深递归（栈增长与 mapassign 交错），结束后 runtime.GC() 全量校验；
//	init_3：包级变量初始化链 gA→gB→gC（依赖顺序）+ GC + 跨 init 顺序依赖校验。
//
// init 只运行一次；main 先校验三个 init 的成果（计数/内容/顺序），再启动 worker
// 针对 init 创建的对象做多轮“写新对象→GC→读回”压力，确认 init 期对象长期有效。
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
	scenario = "z09_init_time_stackgrowth"

	init1IDBase = int64(1_000_000)
	init2IDBase = int64(2_000_000)
	gAID        = int64(9_001_001)
	gBID        = int64(9_001_002)
	gCID        = int64(9_001_003)
)

type initNode struct {
	id   int64
	v    int64
	tag  string
	next *initNode
	pad  [2]uint64
}

var (
	beats    int64
	checks   int64
	gcCycles int64
	started  int64
	finished int64

	init1Runs  int
	init2Runs  int
	init3Runs  int
	init3Stage int
	gSeed      uint64
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

func initMagic(id int64) int64 {
	return int64(uint64(id)*0x9E3779B97F4A7C15^0xBF58476D1CE4E5B9) | 1
}

// deepExpected 是 deepRec 的闭式期望：叶子为两个 magic 之和，非叶层每层 +magic(seed+d)。
func deepExpected(d int, seed int64) int64 {
	s := initMagic(seed) + initMagic(seed*3)
	for k := 1; k <= d; k++ {
		s += initMagic(seed + int64(k))
	}
	return s
}

// deepRec 是 noinline 深递归：每层指针槽，每 64 层一次 GC。
// 在 init 帧（尤其 init_1 的 [4096]*node 局部数组）仍存活时强制 morestack，
// 让栈复制必须正确搬运 init 帧的指针图。
//
//go:noinline
func deepRec(d int, seed int64) int64 {
	local := &initNode{id: seed + int64(d), v: initMagic(seed + int64(d)), tag: "deep"}
	var pad [4]*initNode
	pad[0] = local
	pad[3] = &initNode{id: seed*3 + int64(d), v: initMagic(seed*3 + int64(d)), tag: "deep-p"}

	if d%64 == 0 {
		runtime.GC()
		if local.v != initMagic(seed+int64(d)) || pad[0] != local || pad[3] == nil || pad[3].v != initMagic(seed*3+int64(d)) {
			failf("phase=init-deep-gc depth=%d seed=%d: frame pointers corrupted after GC", d, seed)
		}
	}
	if d == 0 {
		if pad[0] != local || pad[3].id != seed*3 {
			failf("phase=init-deep depth=0 seed=%d: leaf frame corrupted", seed)
		}
		return local.v + pad[3].v
	}
	r := deepRec(d-1, seed)
	if local.v != initMagic(seed+int64(d)) || pad[0] != local || pad[3] == nil || pad[3].v != initMagic(seed*3+int64(d)) {
		failf("phase=init-deep depth=%d seed=%d: frame corrupted on unwind", d, seed)
	}
	return r + local.v
}

func checkInitNode(n *initNode, wantID int64, tag string, phase string) {
	if n == nil {
		failf("phase=%s id=%d: nil *initNode (init 期对象被回收/UAF)", phase, wantID)
	}
	if n.id != wantID || n.v != initMagic(wantID) || n.tag != tag {
		failf("phase=%s id=%d: content corrupted got id=%d v=%#x tag=%q want v=%#x tag=%q",
			phase, wantID, n.id, n.v, n.tag, initMagic(wantID), tag)
	}
}

// validateInitAll 全量校验三个 init 的成果。main 启动时与收尾各一次。
func validateInitAll(phase string) {
	if init1Runs != 1 || init2Runs != 1 || init3Runs != 1 {
		failf("phase=%s: init run counts = (%d,%d,%d) want (1,1,1)", phase, init1Runs, init2Runs, init3Runs)
	}
	if init1Stage != 1 || init2Stage != 2 || init3Stage != 1 {
		failf("phase=%s: init stage flags = (%d,%d,%d) want (1,2,1)", phase, init1Stage, init2Stage, init3Stage)
	}
	if len(initOrder) != 3 || initOrder[0] != 1 || initOrder[1] != 2 || initOrder[2] != 3 {
		failf("phase=%s: init order = %v want [1 2 3]", phase, initOrder)
	}

	if len(init1Nodes) != init1N {
		failf("phase=%s: init_1 nodes=%d want=%d", phase, len(init1Nodes), init1N)
	}
	var sum int64
	for i := 0; i < init1N; i++ {
		n := init1Nodes[i]
		id := init1IDBase + int64(i)
		checkInitNode(n, id, "init1", phase)
		sum += n.v
	}
	if sum != init1Checksum {
		failf("phase=%s: init_1 checksum=%d want=%d", phase, sum, init1Checksum)
	}
	if init1MapLen != init1N/64 || init1SliceLen != init1N/64 {
		failf("phase=%s: init_1 map=%d slice=%d want=%d", phase, init1MapLen, init1SliceLen, init1N/64)
	}

	if len(init2Map) != init2N {
		failf("phase=%s: init_2 map len=%d want=%d", phase, len(init2Map), init2N)
	}
	var sum2 int64
	for i := 0; i < init2N; i++ {
		n, ok := init2Map[i]
		if !ok {
			failf("phase=%s: init_2 map[%d] missing", phase, i)
		}
		checkInitNode(n, init2IDBase+int64(i), "init2", phase)
		sum2 += n.v
	}
	if sum2 != init2MapChecksum {
		failf("phase=%s: init_2 checksum=%d want=%d", phase, sum2, init2MapChecksum)
	}

	checkGlobalChain(phase)
}

// validateInitSample 是 worker 每轮的轻量校验（全量在启动/收尾）。
func validateInitSample(w *worker, r int64, phase string) {
	for i := int64(0); i < 64; i++ {
		idx := (r*17 + w.id*131 + i*7) % init1N
		checkInitNode(init1Nodes[idx], init1IDBase+idx, "init1", phase)
	}
	for i := int64(0); i < 8; i++ {
		idx := (r*31 + w.id*17 + i) % init2N
		n, ok := init2Map[int(idx)]
		if !ok {
			failf("phase=%s worker=%d round=%d: init_2 map[%d] missing", phase, w.id, r, idx)
		}
		checkInitNode(n, init2IDBase+idx, "init2", phase)
	}
	checkGlobalChain(phase)
}

type worker struct {
	id     int64
	seed   uint64
	rounds int64
	next   int64
	checks int64
}

// round 对 init 期创建的对象做“写新对象 → GC → 读回”的长期有效性压力。
func (w *worker) round(r int64) {
	// 每个 worker 独占 init_1 的一个槽位做写操作（避免并发写同一 init 期对象）。
	slot := init1Nodes[w.id]
	checkInitNode(slot, init1IDBase+w.id, "init1", "write-init")

	hid := w.id*1_000_000_000 + r
	holder := &initNode{id: hid, v: initMagic(hid), tag: "round"}
	slot.next = holder // init 期老对象 → 本轮新对象

	refs := make([]*initNode, 0, 64)
	for i := int64(0); i < 64; i++ {
		idx := (uint64(r)*17 + uint64(w.id)*131 + uint64(i)*7) % init1N
		n := init1Nodes[idx]
		checkInitNode(n, init1IDBase+int64(idx), "init1", "alloc-ref")
		refs = append(refs, n)
	}
	holder.next = refs[0]

	runtime.GC()
	atomic.AddInt64(&gcCycles, 1)

	if slot.next != holder {
		failf("phase=post-gc worker=%d round=%d: init_1[%d].next lost after GC (写屏障/init 对象寿命)", w.id, r, w.id)
	}
	checkInitNode(slot, init1IDBase+w.id, "init1", "post-gc")
	checkInitNode(holder, hid, "round", "post-gc")
	for i, n := range refs {
		checkInitNode(n, n.id, "init1", fmt.Sprintf("post-gc.refs[%d]", i))
		if n.v != initMagic(n.id) {
			failf("phase=post-gc worker=%d round=%d refs[%d]: corrupted", w.id, r, i)
		}
	}
	validateInitSample(w, r, "post-gc")
	atomic.AddInt64(&checks, 1)

	w.rounds++
	w.next = r + 1
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
			sink = append(sink, b)
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
				failf("phase=watchdog: no worker progress for %s (beats=%d)", time.Since(lastChange), cur)
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
	workers := envInt("STRESS_WORKERS", 4, 1, 8)
	hb := envInt("STRESS_HEARTBEAT", 5, 1, 60)
	seed := envSeed(1)
	fixed := int64(envInt("STRESS_FIXED_ROUNDS", 16, 1, 1000000))
	gSeed = seed

	fmt.Printf("scenario=%s phase=preflight go=%s goos=%s goarch=%s gomaxprocs=%d seed=%d seconds=%d workers=%d fixed_rounds=%d heartbeat=%ds init1_array=%d init1_depth=%d init2_map=%d init2_depth=%d\n",
		scenario, runtime.Version(), runtime.GOOS, runtime.GOARCH, runtime.GOMAXPROCS(0), seed, seconds, workers, fixed, hb,
		init1N, init1Depth, init2N, init2Depth)

	// init 已经跑完：启动即全量校验（任何 init 期栈复制/指针映射错误在此现形）。
	validateInitAll("startup")

	// 预热 GC/finalizer 内部 goroutine，再取 goroutine 基线。
	for i := 0; i < 3; i++ {
		runtime.GC()
		time.Sleep(time.Millisecond)
	}
	baseGoroutines := runtime.NumGoroutine()

	deadline := time.Now().Add(time.Duration(seconds) * time.Second)
	ws := make([]*worker, workers)
	for i := 0; i < workers; i++ {
		ws[i] = &worker{id: int64(i), seed: seed*1_000_003 + uint64(i+1)*7919}
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

	var roundsTotal int64
	for _, w := range ws {
		roundsTotal += w.rounds
		if seconds == 0 && w.next != fixed {
			failf("phase=final worker=%d: rounds=%d want=%d", w.id, w.next, fixed)
		}
	}
	if roundsTotal == 0 {
		failf("phase=final: no rounds completed")
	}

	// 收尾：init 期对象在长跑 + 多轮 GC 后仍全部有效。
	validateInitAll("final")

	ng := waitGoroutines(baseGoroutines, 5*time.Second)
	if ng > baseGoroutines+4 {
		failf("phase=final: goroutines baseline=%d after=%d: goroutine leak", baseGoroutines, ng)
	}

	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	fmt.Printf("metrics: rounds=%d round_checks=%d init1_nodes=%d init2_entries=%d init_order=%v gc_churn=%d num_gc=%d heap_alloc=%d heap_objects=%d goroutines=%d\n",
		roundsTotal, atomic.LoadInt64(&checks), len(init1Nodes), len(init2Map), initOrder,
		atomic.LoadInt64(&gcCycles), m.NumGC, m.HeapAlloc, m.HeapObjects, ng)
	fmt.Printf("STRESS PASS %s\n", scenario)
}
