// Z05: open-coded defer 记录生命周期（P0）。
// 目标：go#37688 / go#37782 / go#44623——panic 路径把 defer 记录挂到 g0 链表、
// freedefer 不清零导致 GC 扫到坏指针；递归 panic/recover 后陈旧 open-coded defer
// 条目残留；defer 参数指针/闭包槽的 liveness 错误。
//
// 递归函数 rec3/rec5/rec8 的函数体只含 3/5/8 个“简单 defer”（无循环、无闭包、
// 无动态分支），每个 defer 都是 //go:noinline 的指针参数调用 checkNode(p, tag, want)。
// 每轮回合：
//  1. chain(0) 递归到 chain(2)，每层跑一条深递归 rec 链（深度 200），
//     最底层 panic(大 struct + 指针 payload)，由该层的 defer recover；
//     内层 recover 后返回，父层再跑一条 rec 链、再 panic —— 3 层嵌套 recover 链；
//  2. 每个 checkNode 校验 defer 参数指针 p 的 id/canary，并按 (depth,deferIdx)
//     矩阵计数；每轮按 seed 选一个 (tag, depth) 在 defer 内触发 runtime.GC()，
//     让 GC 发生在 panic 展开/deferreturn 中间；
//  3. 回合末校验矩阵总计：每条链的每一帧每个 defer 恰好执行一次（共 3 条链），
//     3 次 recover、payload 的 24 个指针槽与 words/字符串完好，节点 canary 全对。
//
// 并发多 worker 同时展开（go#37688 panic 路径）；每轮少量 GC + 独立 gcChurn。
// 不使用 runtime.Goexit（x05 覆盖）。
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
	scenario    = "z05_opendefer_record_lifecycle"
	maxDefers   = 8
	maxDepth    = 1024 // runs 矩阵上界
	maxRecDepth = 512  // STRESS_DEPTH 上限（< maxDepth，留出余量）
	payloadPtr  = 24
	nodeTag     = "z05"

	overflowA uint64 = 6364136223846793005
)

type dnode struct {
	id  int64
	v   int64
	tag string
	_   int64
}

// panicPayload 是大 struct + 指针 payload：展开期间必须被 _panic 记录保活。
type panicPayload struct {
	level int
	nodes [payloadPtr]*dnode
	str   string
	words [8]uint64
}

type worker struct {
	id    int64
	seed  uint64
	depth int

	nodes      []*dnode
	payloadStr string
	runs       [maxDepth][maxDefers]int64

	variant    int
	gcTag      int
	gcAtDepth  int
	recChains  int64
	recoveries int64
	defersDone int64
	rounds     int64
	gcCalls    int64
	checks     int64
}

func mix64(x uint64) uint64 {
	x += 0x9E3779B97F4A7C15
	x = (x ^ (x >> 30)) * 0xBF58476D1CE4E5B9
	x = (x ^ (x >> 27)) * 0x94D049BB133111EB
	x ^= x >> 31
	return x
}

func nodeWant(seed uint64, id int64) int64 {
	v := int64(mix64(seed^uint64(id)*0xD1B54A32D192ED03^0x5A5A5A5A) & 0x7FFFFFFFFFFFFFFF)
	if v == 0 {
		v = 0x2545F4914F6CDD1D
	}
	return v
}

func (w *worker) init() {
	w.nodes = make([]*dnode, w.depth+16)
	for i := range w.nodes {
		w.nodes[i] = &dnode{id: int64(i), v: nodeWant(w.seed, int64(i)), tag: nodeTag}
	}
	w.payloadStr = fmt.Sprintf("z05-payload-%d-%d", w.id, w.seed)
}

func (w *worker) wantNode(depth int) int64 {
	return nodeWant(w.seed, int64(depth))
}

func (w *worker) makePayload(level, depth int) panicPayload {
	var pp panicPayload
	pp.level = level
	pp.str = w.payloadStr
	pp.words[0] = w.seed ^ 0x9E3779B97F4A7C15
	for i := 1; i < len(pp.words); i++ {
		pp.words[i] = pp.words[i-1]*overflowA + 1
	}
	for i := 0; i < payloadPtr; i++ {
		pp.nodes[i] = w.nodes[(depth+i*7+level)%len(w.nodes)]
	}
	return pp
}

func (w *worker) validatePayload(pp *panicPayload, level, depth int) {
	if pp.level != level {
		failf("worker=%d seed=%d: payload.level=%d want=%d", w.id, w.seed, pp.level, level)
	}
	if pp.str != w.payloadStr {
		failf("worker=%d seed=%d: payload.str 被改写: %q", w.id, w.seed, pp.str)
	}
	want := w.seed ^ 0x9E3779B97F4A7C15
	if pp.words[0] != want {
		failf("worker=%d seed=%d: payload.words[0]=%#x want=%#x", w.id, w.seed, pp.words[0], want)
	}
	for i := 1; i < len(pp.words); i++ {
		want = want*overflowA + 1
		if pp.words[i] != want {
			failf("worker=%d seed=%d: payload.words[%d]=%#x want=%#x", w.id, w.seed, i, pp.words[i], want)
		}
	}
	for i := 0; i < payloadPtr; i++ {
		idx := (depth + i*7 + level) % len(w.nodes)
		n := pp.nodes[i]
		if n == nil || n != w.nodes[idx] {
			failf("worker=%d seed=%d: payload.nodes[%d] 丢失/指向错误 (got=%p want=%p)", w.id, w.seed, i, n, w.nodes[idx])
		}
		if n.id != int64(idx) || n.v != nodeWant(w.seed, int64(idx)) || n.tag != nodeTag {
			failf("worker=%d seed=%d: payload.nodes[%d] canary 损坏 (id=%d v=%d)", w.id, w.seed, i, n.id, n.v)
		}
	}
}

// checkNode 是 open-coded defer 的被调函数：noinline、参数为指针。
// 它按 (depth, tag) 计数，并校验 defer 参数指针 p 的身份与 canary。
//
//go:noinline
func (w *worker) checkNode(p *dnode, tag, want int64, depth int) {
	if p == nil {
		failf("worker=%d seed=%d: defer 参数指针为 nil (depth=%d tag=%d)", w.id, w.seed, depth, tag)
	}
	if p.id != int64(depth) || p.tag != nodeTag {
		failf("worker=%d seed=%d: defer 参数指针指向错误对象 (depth=%d tag=%d gotID=%d gotTag=%q)",
			w.id, w.seed, depth, tag, p.id, p.tag)
	}
	if p.v != want {
		failf("worker=%d seed=%d: defer 参数 canary 错误 (depth=%d tag=%d got=%d want=%d)",
			w.id, w.seed, depth, tag, p.v, want)
	}
	w.runs[depth][tag]++
	w.defersDone++
	w.checks++
	// 变体：让 GC 发生在 panic 展开 / deferreturn 中间（每链只在指定帧触发一次）。
	if int(tag) == w.gcTag && depth == w.gcAtDepth {
		runtime.GC()
		w.gcCalls++
		gcYield()
	}
}

// rec3 / rec5 / rec8：3、5、8 个简单 defer（无循环/闭包），编译器生成 open-coded defer。
//
//go:noinline
func rec3(w *worker, depth, level int, p *dnode) {
	want := w.wantNode(depth)
	defer w.checkNode(p, 0, want, depth)
	defer w.checkNode(p, 1, want, depth)
	defer w.checkNode(p, 2, want, depth)
	if depth == 0 {
		panic(w.makePayload(level, depth))
	}
	rec3(w, depth-1, level, w.nodes[depth-1])
}

//go:noinline
func rec5(w *worker, depth, level int, p *dnode) {
	want := w.wantNode(depth)
	defer w.checkNode(p, 0, want, depth)
	defer w.checkNode(p, 1, want, depth)
	defer w.checkNode(p, 2, want, depth)
	defer w.checkNode(p, 3, want, depth)
	defer w.checkNode(p, 4, want, depth)
	if depth == 0 {
		panic(w.makePayload(level, depth))
	}
	rec5(w, depth-1, level, w.nodes[depth-1])
}

//go:noinline
func rec8(w *worker, depth, level int, p *dnode) {
	want := w.wantNode(depth)
	defer w.checkNode(p, 0, want, depth)
	defer w.checkNode(p, 1, want, depth)
	defer w.checkNode(p, 2, want, depth)
	defer w.checkNode(p, 3, want, depth)
	defer w.checkNode(p, 4, want, depth)
	defer w.checkNode(p, 5, want, depth)
	defer w.checkNode(p, 6, want, depth)
	defer w.checkNode(p, 7, want, depth)
	if depth == 0 {
		panic(w.makePayload(level, depth))
	}
	rec8(w, depth-1, level, w.nodes[depth-1])
}

func (w *worker) defersPerVariant() int {
	switch w.variant {
	case 0:
		return 3
	case 1:
		return 5
	default:
		return 8
	}
}

func (w *worker) runRec(level int) {
	w.recChains++
	depth := w.depth
	switch w.variant {
	case 0:
		rec3(w, depth, level, w.nodes[depth])
	case 1:
		rec5(w, depth, level, w.nodes[depth])
	default:
		rec8(w, depth, level, w.nodes[depth])
	}
}

// chain 构造 3 层嵌套 recover 链：先内层完整跑一条 rec 链并 recover，
// 返回后父层再跑一条 rec 链、再 panic，由父层自己的 defer recover。
func (w *worker) chain(level int) {
	defer func() {
		r := recover()
		if r == nil {
			failf("worker=%d seed=%d: level=%d rec 链未触发 panic", w.id, w.seed, level)
		}
		pp, ok := r.(panicPayload)
		if !ok {
			failf("worker=%d seed=%d: level=%d panic payload 类型=%T", w.id, w.seed, level, r)
		}
		w.validatePayload(&pp, level, 0)
		w.recoveries++
	}()
	if level < 2 {
		w.chain(level + 1)
	}
	w.runRec(level)
}

func (w *worker) resetRuns() {
	for d := 0; d <= w.depth; d++ {
		for j := 0; j < maxDefers; j++ {
			w.runs[d][j] = 0
		}
	}
}

func (w *worker) round(r int64) {
	w.resetRuns()
	w.variant = int(r % 3)
	// defer 内 GC 的位置随 seed/轮次变化：tag 0..4，depth 1..8。
	w.gcTag = int((w.seed + uint64(r)*3) % 5)
	w.gcAtDepth = 1 + int((w.seed+uint64(r)*7)%8)
	w.recChains = 0
	w.recoveries = 0

	w.chain(0)

	if w.recChains != 3 {
		failf("worker=%d seed=%d round=%d: rec 链数=%d want=3", w.id, w.seed, r, w.recChains)
	}
	if w.recoveries != 3 {
		failf("worker=%d seed=%d round=%d: recover 次数=%d want=3", w.id, w.seed, r, w.recoveries)
	}

	// defer 执行矩阵：3 条链 × (depth+1) 帧 × K 个 defer，每个条目恰好 3 次。
	K := w.defersPerVariant()
	for d := 0; d <= w.depth; d++ {
		for j := 0; j < maxDefers; j++ {
			want := int64(0)
			if j < K {
				want = 3
			}
			if got := w.runs[d][j]; got != want {
				failf("worker=%d seed=%d round=%d variant=%d depth=%d tag=%d: defer 执行次数=%d want=%d",
					w.id, w.seed, r, w.variant, d, j, got, want)
			}
		}
	}

	// 每轮少量显式 GC；GC 后校验节点 canary。
	runtime.GC()
	w.gcCalls++
	gcYield()
	for i := 0; i < len(w.nodes); i++ {
		if w.nodes[i].id != int64(i) || w.nodes[i].v != nodeWant(w.seed, int64(i)) || w.nodes[i].tag != nodeTag {
			failf("worker=%d seed=%d round=%d: node[%d] corrupted after panic/recover storm", w.id, w.seed, r, i)
		}
	}
	// defer 链结束后再入一次浅链，覆盖“recover 后复用 defer 记录”的形态。
	w.resetRuns()
	w.variant = int((r + 1) % 3)
	w.recChains = 0
	w.recoveries = 0
	if r%3 == 0 {
		w.gcAtDepth = 0 // depth=0 的 defer 内 GC：最底层展开路径
	}
	w.chain(0)
	K = w.defersPerVariant()
	for d := 0; d <= w.depth; d++ {
		for j := 0; j < K; j++ {
			if got := w.runs[d][j]; got != 3 {
				failf("worker=%d seed=%d round=%d (re-entry) variant=%d depth=%d tag=%d: defer 执行次数=%d want=3",
					w.id, w.seed, r, w.variant, d, j, got)
			}
		}
	}
	if w.recoveries != 3 {
		failf("worker=%d seed=%d round=%d (re-entry): recover 次数=%d want=3", w.id, w.seed, r, w.recoveries)
	}

	w.rounds++
	atomic.AddInt64(&beats, 1)
}

func (w *worker) run(seconds int, deadline time.Time, fixed int64) {
	atomic.AddInt64(&started, 1)
	defer atomic.AddInt64(&finished, 1)
	w.init()
	runtime.GC()
	gcYield()
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
	runtime.GC()
	gcYield()
	for i := 0; i < len(w.nodes); i++ {
		if w.nodes[i].v != nodeWant(w.seed, int64(i)) {
			failf("worker=%d seed=%d: final node[%d] corrupted", w.id, w.seed, i)
		}
	}
	if w.rounds == 0 {
		failf("worker=%d seed=%d: no rounds completed", w.id, w.seed)
	}
}

var (
	beats    int64
	gcCycles int64
	started  int64
	finished int64
	gSeed    uint64
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

// gcYield: GOMAXPROCS=1 时在显式 GC 之后让出 P。多个 worker 在同一 P 上
// 紧邻调用 runtime.GC 会互相排队饥饿（一个完成立刻再次进入 GC），实测会让
// 其余 worker 的首次 GC 卡住数秒；worker 与 gcChurn 都必须让出。
func gcYield() {
	if runtime.GOMAXPROCS(0) == 1 {
		runtime.Gosched()
		time.Sleep(time.Millisecond)
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
	workers := envInt("STRESS_WORKERS", 4, 1, 16)
	hb := envInt("STRESS_HEARTBEAT", 5, 1, 60)
	seed := envSeed(1)
	fixed := int64(envInt("STRESS_FIXED_ROUNDS", 16, 1, 1000000))
	depth := envInt("STRESS_DEPTH", 200, 8, maxRecDepth)
	gSeed = seed

	fmt.Printf("scenario=%s phase=preflight go=%s goos=%s goarch=%s gomaxprocs=%d seed=%d seconds=%d workers=%d fixed_rounds=%d heartbeat=%ds depth=%d defer_variants=3,5,8 chains=3\n",
		scenario, runtime.Version(), runtime.GOOS, runtime.GOARCH, runtime.GOMAXPROCS(0), seed, seconds, workers, fixed, hb, depth)

	baseGoroutines := runtime.NumGoroutine()
	deadline := time.Now().Add(time.Duration(seconds) * time.Second)
	ws := make([]*worker, workers)
	for i := 0; i < workers; i++ {
		ws[i] = &worker{id: int64(i), seed: seed*1000 + uint64(i) + 1, depth: depth}
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

	var rounds, checks, defers, gcs int64
	for _, w := range ws {
		rounds += w.rounds
		checks += w.checks
		defers += w.defersDone
		gcs += w.gcCalls
		if seconds == 0 && w.rounds != fixed {
			failf("phase=final seed=%d worker=%d: rounds=%d want=%d", seed, w.id, w.rounds, fixed)
		}
	}
	if rounds == 0 || defers == 0 {
		failf("phase=final seed=%d: no rounds/defers completed (rounds=%d defers=%d)", seed, rounds, defers)
	}

	ng := waitGoroutines(baseGoroutines, 5*time.Second)
	if ng > baseGoroutines+4 {
		failf("phase=final seed=%d: goroutines baseline=%d after=%d: goroutine leak", seed, baseGoroutines, ng)
	}

	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	fmt.Printf("metrics: rounds=%d defer_exec=%d checks=%d explicit_gc=%d gc_churn=%d workers=%d depth=%d chains_per_round=6 payload_ptrs=%d heap_alloc=%d heap_objects=%d num_gc=%d goroutines=%d\n",
		rounds, defers, checks, gcs, atomic.LoadInt64(&gcCycles), workers, depth, payloadPtr,
		m.HeapAlloc, m.HeapObjects, m.NumGC, ng)
	fmt.Printf("STRESS PASS %s\n", scenario)
}
