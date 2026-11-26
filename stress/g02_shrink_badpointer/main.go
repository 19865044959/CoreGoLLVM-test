// G02: 栈收缩扫坏指针——issue1 核心复刻（P0，covers L04/L17/L18）。
// 对准 issue/issue1.md：cel-go 在 ARM64 偶现 `fatal error: invalid pointer found
// on stack`，崩溃在 GC 后台标记线程 shrinkstack 的 adjustframe/adjustpointers，
// 被扫帧 = common/ast.NewCheckedAST。复刻其触发形状：
//   - 并发 goroutine 各执行「深调用 → 浅返回」锯齿（checker.Check 的 AST 深遍历
//     返回后在浅栈执行 NewCheckedAST 同构构造——与 tRunner → TestInterpreter.func1
//     → program → checker.Check → NewCheckedAST 的栈形一致）；
//   - checkLike（NewCheckedAST 同构）：中等复杂度、指针局部（3 张 map 构造 +
//     迭代）、多返回值、defer、非常量下标局部数组（栈驻留取值 + 返回前取址
//     &earr[m] 存进结果对象——模拟 checker.Check → ast.NewCheckedAST 链）；
//   - 浅栈点经 gate 阻塞，shrink driver / GC churn 高频 runtime.GC()：
//     scanstack → shrinkstack → copystack → adjustpointers 反复扫描本帧；
//   - STRESS_GMX=2,8,16 阶段切换 GOMAXPROCS（高值阶段 GC worker 多，issue1 §2
//     的 GOMAXPROCS=32 放大器同思路）。
//
// 失败判定：进程被 runtime 崩掉（invalid pointer found on stack）即 FAIL；
// window/soak 双跑 + soak-single。官方 Go 栈图精确、恒 PASS。
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
	"weak"
)

const (
	scenario           = "g02_shrink_badpointer"
	poisonIDBase       = int64(1) << 50
	fixedRoundsDefault = 20
	depthDefault       = 512
)

type typeInfo struct {
	id    int64
	magic uint64
	aux   int64
}

func magicFor(seed uint64, id int64) uint64 {
	x := seed*0x9E3779B97F4A7C15 + uint64(id)*0x2545F4914F6CDD1D + 0x5EED5EED
	x ^= x >> 31
	x *= 0xBF58476D1CE4E5B9
	x ^= x >> 27
	return x
}

func newTypeInfo(seed uint64, id int64) *typeInfo {
	return &typeInfo{id: id, magic: magicFor(seed, id), aux: id ^ 0x7A7A7A7A}
}

//go:noinline
func checkTypeInfo(t *typeInfo, seed uint64, want int64, where string) {
	if t == nil {
		failf("%s: nil *typeInfo (want id=%d): UAF/漏标", where, want)
	}
	if t.id != want || t.magic != magicFor(seed, t.id) || t.aux != t.id^0x7A7A7A7A {
		failf("%s: typeInfo canary mismatch id=%d want=%d magic=%#x aux=%#x: UAF/复用",
			where, t.id, want, t.magic, t.aux)
	}
	runtime.KeepAlive(t)
}

var poisonSeq int64

//go:noinline
func poisonTypeAlloc(seed uint64, n int) []*typeInfo {
	seq := atomic.AddInt64(&poisonSeq, 1)
	out := make([]*typeInfo, n)
	for i := range out {
		out[i] = newTypeInfo(seed, poisonIDBase+seq*1_000_000+int64(i))
	}
	return out
}

// ---------- AST 与 checker.Check 同构的深遍历（深调用 → 浅返回锯齿） ----------

type astNode struct {
	id   int64
	tv   *typeInfo
	kids []*astNode
}

func buildAST(seed uint64, base int64, depth int) *astNode {
	root := &astNode{id: base, tv: newTypeInfo(seed, base+10_000)}
	cur := root
	for k := 0; k < depth; k++ {
		next := &astNode{id: base + 1 + int64(k), tv: newTypeInfo(seed, base+10_001+int64(k))}
		cur.kids = []*astNode{next}
		cur = next
	}
	return root
}

// checkRec：checker.Check 的 AST 深遍历同构——每帧持有 4 个 int64 槽（展开路径
// 校验，迫使 spill），递归到 depth 0。链上 id 之和 = checkSumWant。
//
//go:noinline
func checkRec(seed uint64, base int64, n *astNode, d int) int64 {
	var slot [4]int64
	slot[0] = n.id
	slot[1] = int64(d)
	slot[2] = int64(len(n.kids))
	slot[3] = n.id ^ 0x55
	if d == 0 || len(n.kids) == 0 {
		return slot[0]
	}
	r := checkRec(seed, base+1, n.kids[0], d-1)
	if slot[0] != n.id || slot[1] != int64(d) {
		failf("g02.checkRec: frame slot corrupted (seed=%d base=%d d=%d)", seed, base, d)
	}
	return r + n.id
}

// evalRec：program() 使用 CheckedAST 的再一轮深遍历（锯齿后半段：收缩后重新长高）。
//
//go:noinline
func evalRec(seed uint64, base int64, n *astNode, d int) int64 {
	if d == 0 || len(n.kids) == 0 {
		return n.id
	}
	r := evalRec(seed, base+1, n.kids[0], d-1)
	if n.tv == nil {
		failf("g02.evalRec: nil tv (seed=%d base=%d d=%d)", seed, base, d)
	}
	return r + n.id
}

func checkSumWant(base int64, depth int) int64 {
	return int64(depth+1)*base + int64(depth)*int64(depth+1)/2
}

// ---------- NewCheckedAST 同构（issue1 的崩溃帧） ----------

type srcInfo struct {
	seed  uint64
	base  int64
	magic uint64
}

type refInfo struct {
	id   int64
	node *astNode
}

type issueInfo struct {
	id     int64
	sev    int64
	msg    string
	anchor *typeInfo
}

type checkedAST struct {
	expr      *astNode
	src       *srcInfo
	types     map[int64]*typeInfo
	refs      map[int64]*refInfo
	exprTypes map[int64]*typeInfo
	issues    []*issueInfo
	local     *typeInfo // 返回前取址存到结果：&earr[m]
}

// checkLike：NewCheckedAST 同构——中等复杂度、指针局部、多返回值、defer、
// 非常量下标局部数组。gate 在浅栈点阻塞：shrink driver 的 GC 使本 goroutine
// 栈满足 shrinkstack 条件（深遍历刚返回、栈大部分空闲），copystack →
// adjustpointers 按栈图扫描本帧——栈图把非指针槽（循环变量/下标/spill 垃圾）
// 当指针即 `invalid pointer found on stack`（issue1 现场）。
//
//go:noinline
func checkLike(seed uint64, base int64, expr *astNode, tm map[int64]*typeInfo, rm map[int64]*refInfo, etm map[int64]*typeInfo, issues []*issueInfo, depth int, gate func()) (ret *checkedAST, err error) {
	defer func() {
		if ret == nil {
			failf("g02.checkLike: nil result (seed=%d base=%d)", seed, base)
		}
	}()

	// —— checker.Check 同构：AST 深遍历（深调用 → 浅返回锯齿的“深→浅”）——
	sum := checkRec(seed, base, expr, depth)
	if want := checkSumWant(base, depth); sum != want {
		failf("g02.checkLike: checkRec checksum=%d want=%d (seed=%d base=%d depth=%d)", sum, want, seed, base, depth)
	}

	// —— NewCheckedAST 同构：map 构造 + 迭代 + 非常量下标局部数组 ——
	if tm == nil {
		tm = map[int64]*typeInfo{}
	}
	types := make(map[int64]*typeInfo, len(tm))
	exprTypes := make(map[int64]*typeInfo, len(etm))
	for id, e := range tm {
		types[id] = e
	}
	for id, e := range etm {
		exprTypes[id] = e
	}

	var arr [4]*typeInfo // 栈驻留：元素经非常量下标取值（值拷贝带出）
	var earr [4]typeInfo // 逃逸路径：&earr[m] 取址存进结果对象
	m := int((seed + uint64(base)) % 4)
	for i := 0; i < 4; i++ {
		arr[i] = newTypeInfo(seed, base+1+int64(i))
		earr[i] = *arr[i]
	}
	picked := arr[m]

	ret = &checkedAST{
		expr:      expr,
		src:       &srcInfo{seed: seed, base: base, magic: magicFor(seed, base+999)},
		types:     types,
		refs:      rm,
		exprTypes: exprTypes,
		issues:    issues,
		local:     &earr[m],
	}

	gate() // 浅栈阻塞点：driver GC → shrinkstack → copystack → adjustpointers 扫本帧

	// 恢复后：跨 shrink 存活根逐项校验（栈搬移漏重定位 = 立即 FAIL）。
	checkTypeInfo(picked, seed, base+1+int64(m), "g02.checkLike/post-gate.picked")
	checkTypeInfo(ret.local, seed, base+1+int64(m), "g02.checkLike/post-gate.local")
	checkTypeInfo(ret.types[base+1], seed, base+101, "g02.checkLike/post-gate.types")
	checkTypeInfo(ret.types[base+4], seed, base+104, "g02.checkLike/post-gate.types2")
	checkTypeInfo(ret.exprTypes[base+2], seed, base+202, "g02.checkLike/post-gate.exprTypes")
	if len(ret.issues) != 2 {
		failf("g02.checkLike: issues len=%d want=2 (seed=%d base=%d)", len(ret.issues), seed, base)
	}
	if ret.src.magic != magicFor(seed, base+999) {
		failf("g02.checkLike: srcInfo corrupted (seed=%d base=%d)", seed, base)
	}
	runtime.KeepAlive(picked)
	runtime.KeepAlive(arr)
	runtime.KeepAlive(earr)
	return ret, nil
}

// ---------- worker 与 shrink driver ----------

type shrinkReq struct{ round int64 }

type worker struct {
	id    int64
	seed  uint64
	base0 int64
	depth int

	reqCh chan<- shrinkReq
	ackCh <-chan struct{}

	next   int64
	rounds int64
	checks int64

	lastWeak []weak.Pointer[typeInfo]
}

//go:noinline
func (w *worker) round(r int64) {
	base := w.base0 + r*50_000
	depth := w.depth

	expr := buildAST(w.seed, base, depth)
	tm := make(map[int64]*typeInfo, 8)
	etm := make(map[int64]*typeInfo, 8)
	rm := make(map[int64]*refInfo, 4)
	for i := 1; i <= 8; i++ {
		tm[base+int64(i)] = newTypeInfo(w.seed, base+100+int64(i))
		etm[base+int64(i)] = newTypeInfo(w.seed, base+200+int64(i))
	}
	for i := 1; i <= 4; i++ {
		rm[base+int64(i)] = &refInfo{id: base + int64(i), node: expr}
	}
	issues := []*issueInfo{
		{id: base + 1, sev: 1, msg: "g02.issue-1", anchor: tm[base+1]},
		{id: base + 2, sev: 2, msg: "g02.issue-2", anchor: tm[base+2]},
	}

	ret, err := checkLike(w.seed, base, expr, tm, rm, etm, issues, depth, func() {
		w.reqCh <- shrinkReq{round: r}
		<-w.ackCh
	})
	if err != nil {
		failf("phase=round worker=%d round=%d seed=%d: checkLike err=%v", w.id, r, w.seed, err)
	}

	// caller 侧校验（program() 使用 CheckedAST）：再一轮深遍历 + 全字段复核。
	if sum, want := evalRec(w.seed, base, ret.expr, depth), checkSumWant(base, depth); sum != want {
		failf("phase=round worker=%d round=%d: eval checksum=%d want=%d", w.id, r, sum, want)
	}
	m := int((w.seed + uint64(base)) % 4)
	checkTypeInfo(ret.local, w.seed, base+1+int64(m), "g02.caller/post-return.local")
	checkTypeInfo(ret.types[base+1], w.seed, base+101, "g02.caller/post-return.types")
	if ret.issues[0].anchor == nil || ret.issues[1].anchor == nil {
		failf("phase=round worker=%d round=%d: issue anchor nil", w.id, r)
	}

	// weak 强引用断言：结果仍被强引用，GC 后必须非 nil。
	wp := weak.Make(ret.local)
	runtime.GC()
	if v := wp.Value(); v == nil {
		failf("phase=weak worker=%d round=%d seed=%d: strong ref held but object collected (栈图漏标/UAF)",
			w.id, r, w.seed)
	} else {
		checkTypeInfo(v, w.seed, base+1+int64(m), "g02.weak-strong-ref")
	}
	atomic.AddInt64(&gWeakChecks, 1)

	runtime.KeepAlive(ret)
	w.lastWeak = append(w.lastWeak, wp)
	w.rounds++
	w.next = r + 1
	w.checks += 12
	atomic.AddInt64(&gBeats, 1)
}

// shrinkDriver：唯一的 shrink 发起者——worker 在浅栈点阻塞时执行 runtime.GC()，
// 使 worker 栈满足 shrinkstack 条件（used < 1/4 alloc），触发
// scanstack → shrinkstack → copystack → adjustpointers 路径。
func shrinkDriver(reqCh <-chan shrinkReq, ackCh chan<- struct{}, stop <-chan struct{}, wg *sync.WaitGroup) {
	defer wg.Done()
	for {
		select {
		case <-stop:
			return
		case req := <-reqCh:
			atomic.AddInt64(&gShrinkReq, 1)
			_ = req
			runtime.GC()
			atomic.AddInt64(&gShrinkGC, 1)
			select {
			case ackCh <- struct{}{}:
			case <-stop:
				return
			}
		}
	}
}

// gcChurn：独立高频 GC churn（issue1 §2 的并发 GC 放大）。所有显式 runtime.GC()
// 都经 shrinkDriver 串行发起——gcstoptheworld=2,gccheckmark=1 下多个 goroutine
// 并发调 runtime.GC() 会使 GC 握手互等卡死（window profile 实测），串行化后
// churn 只贡献分配压力 + 由 driver 统一执行 GC。不喂心跳——心跳只反映 worker
// 进展，churn 卡死不掩蔽 worker 停滞。
func gcChurn(reqCh chan<- shrinkReq, ackCh <-chan struct{}, stop <-chan struct{}, wg *sync.WaitGroup) {
	defer wg.Done()
	seed := uint64(0x61C88647)
	var buf []*typeInfo
	batch := 0
	for {
		for i := 0; i < 16; i++ {
			buf = append(buf, newTypeInfo(seed, int64(i)))
			if len(buf) > 512 {
				buf = buf[256:]
			}
		}
		batch++
		runtime.Gosched()
		if batch%64 == 0 {
			select {
			case <-stop:
				return
			case reqCh <- shrinkReq{round: -1}: // 经 driver 串行 GC
			}
			select {
			case <-ackCh:
			case <-stop:
				return
			}
		}
	}
}

// ---------- 通用 harness ----------

var (
	gBeats      int64
	gWeakChecks int64
	gShrinkReq  int64
	gShrinkGC   int64
	gChurnGC    int64
	gStarted    int64
	gFinished   int64
	gSeed       uint64

	lastWeakMu sync.Mutex
	gLastWeak  []weak.Pointer[typeInfo]
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

// parseGMX 解析 STRESS_GMX（如 "2,8,16"）；空串/全非法返回 nil 表示不改变 GOMAXPROCS。
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

func (w *worker) run(seconds int, deadline time.Time, fixed int64, start, end int64) {
	atomic.AddInt64(&gStarted, 1)
	defer atomic.AddInt64(&gFinished, 1)
	r := start
	for {
		if seconds > 0 {
			if time.Now().After(deadline) {
				break
			}
		} else if r >= end {
			break
		}
		w.round(r)
		r++
	}
}

func main() {
	setupCrashOutput()
	seconds := envInt("STRESS_SECONDS", 60, 0, 86400)
	workers := envInt("STRESS_WORKERS", 4, 1, 8)
	hb := envInt("STRESS_HEARTBEAT", 5, 1, 60)
	seed := envSeed(1)
	fixed := int64(envInt("STRESS_FIXED_ROUNDS", fixedRoundsDefault, 1, 1000000))
	depth := envInt("STRESS_DEPTH", depthDefault, 64, 4096)
	gmx := parseGMX(os.Getenv("STRESS_GMX"))
	phases := len(gmx)
	if phases == 0 {
		phases = 1 // 未设置 STRESS_GMX：不调用 GOMAXPROCS，尊重 profile/环境
	}
	gSeed = seed

	fmt.Printf("scenario=%s phase=preflight go=%s goos=%s goarch=%s gomaxprocs=%d seed=%d seconds=%d workers=%d fixed_rounds=%d depth=%d heartbeat=%ds gmx=%v phases=%d\n",
		scenario, runtime.Version(), runtime.GOOS, runtime.GOARCH, runtime.GOMAXPROCS(0),
		seed, seconds, workers, fixed, depth, hb, gmx, phases)

	runtime.GC()
	baseGoroutines := runtime.NumGoroutine()

	reqCh := make(chan shrinkReq)
	ackCh := make(chan struct{})
	stop := make(chan struct{})
	var aux sync.WaitGroup
	aux.Add(1)
	go shrinkDriver(reqCh, ackCh, stop, &aux)
	aux.Add(1)
	go gcChurn(reqCh, ackCh, stop, &aux)
	aux.Add(1)
	go watchdog(time.Duration(hb)*time.Second, stop, &aux)

	start := time.Now()
	var total time.Duration
	if seconds > 0 {
		total = time.Duration(seconds) * time.Second
	}

	for p := 0; p < phases; p++ {
		// 阶段边界：只有显式给出 STRESS_GMX 才由主 goroutine 改 GOMAXPROCS。
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

		ws := make([]*worker, workers)
		for i := 0; i < workers; i++ {
			ws[i] = &worker{
				id:    int64(i),
				seed:  seed*1_000_003 + uint64(i+1)*7919,
				base0: int64(i)*1_000_000_000_000 + int64(p)*100_000_000_000,
				depth: depth,
				reqCh: reqCh,
				ackCh: ackCh,
			}
		}
		var wg sync.WaitGroup
		for _, w := range ws {
			wg.Add(1)
			go func(w *worker) {
				defer wg.Done()
				w.run(seconds, deadline, fixed, pStart, pEnd)
			}(w)
		}
		wg.Wait()
		for _, w := range ws {
			lastWeakMu.Lock()
			gLastWeak = append(gLastWeak, w.lastWeak...)
			lastWeakMu.Unlock()
		}
	}

	close(stop)
	aux.Wait()

	// quiesce：全量 GC 后再验最后一次 weak（此时应已失活，仅记录）。
	runtime.GC()
	runtime.GC()
	aliveAfterDrop := 0
	lastWeakMu.Lock()
	for _, wp := range gLastWeak {
		if wp.Value() != nil {
			aliveAfterDrop++
		}
	}
	lastWeakMu.Unlock()

	if s, f := atomic.LoadInt64(&gStarted), atomic.LoadInt64(&gFinished); s != int64(workers*phases) || f != s {
		failf("phase=final: started=%d finished=%d want=%d", s, f, workers*phases)
	}
	if atomic.LoadInt64(&gShrinkGC) == 0 {
		failf("phase=final: no shrink GC happened")
	}
	if atomic.LoadInt64(&gBeats) == 0 {
		failf("phase=final: no rounds completed")
	}

	ng := waitGoroutines(baseGoroutines, 5*time.Second)
	if ng > baseGoroutines+4 {
		failf("phase=final: goroutines baseline=%d after=%d: goroutine leak", baseGoroutines, ng)
	}

	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	fmt.Printf("metrics: beats=%d shrink_gc=%d shrink_req=%d churn_gc=%d weak_checks=%d weak_alive_after_drop=%d num_gc=%d heap_alloc=%d heap_objects=%d depth=%d gomaxprocs_final=%d goroutines_start=%d goroutines_final=%d\n",
		atomic.LoadInt64(&gBeats), atomic.LoadInt64(&gShrinkGC), atomic.LoadInt64(&gShrinkReq),
		atomic.LoadInt64(&gChurnGC), atomic.LoadInt64(&gWeakChecks), aliveAfterDrop,
		m.NumGC, m.HeapAlloc, m.HeapObjects, depth, runtime.GOMAXPROCS(0), baseGoroutines, ng)
	fmt.Printf("STRESS PASS %s\n", scenario)
}
