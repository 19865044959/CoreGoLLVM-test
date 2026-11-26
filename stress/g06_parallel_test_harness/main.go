// G06: 并行测试 harness——issue1 载体复刻（P0，covers L02/L17/L18）。
// 对准 issue/issue1.md §2：cel-go TestInterpreter 对大量子测试 t.Parallel()，
// 并发 goroutine 同时执行 program() → checker.Check() → ast.NewCheckedAST()，
// GC 在扫描窗口命中 NewCheckedAST 帧 → fatal error。复刻载体形状：
//   - 数百个「子测试」goroutine 并发跑同一深调用链：buildAST（AST 构造）→
//     checkRec（checker.Check 的 AST 深遍历）→ checkLike（NewCheckedAST 同构：
//     map 构造/迭代、非常量下标局部数组、返回前取址存结果）→ evalRec（再深遍历）；
//   - 每个 case 带独立小状态对象（caseState：3 张 map + issues + 自己的 seed/base）；
//   - 波栅栏 waveGate：整波 case 在浅栈点集合，gate driver 把一批 pending 合并成
//     一次 runtime.GC() 后放行全部——一次 GC 同时扫/缩 N 个 case 的深栈
//     （issue1 §2 的并发放大）；
//   - gcChurn goroutine 常驻高频 GC；STRESS_GMX=8,16 阶段切换 GOMAXPROCS
//     （高值阶段 GC worker 多）；
//   - 模拟 go test 生命周期：每波 WaitGroup + 超时控制（go test -timeout 同构）。
//
// 失败判定：进程被 runtime 崩掉（invalid pointer found on stack）即 FAIL。
// 官方 Go 栈图精确、恒 PASS。
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
	scenario           = "g06_parallel_test_harness"
	poisonIDBase       = int64(1) << 50
	fixedRoundsDefault = 10 // 波数
	depthDefault       = 384
	casesDefault       = 96
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

// ---------- AST 与 checker.Check 同构的深遍历 ----------

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
		failf("g06.checkRec: frame slot corrupted (seed=%d base=%d d=%d)", seed, base, d)
	}
	return r + n.id
}

//go:noinline
func evalRec(seed uint64, base int64, n *astNode, d int) int64 {
	if d == 0 || len(n.kids) == 0 {
		return n.id
	}
	r := evalRec(seed, base+1, n.kids[0], d-1)
	if n.tv == nil {
		failf("g06.evalRec: nil tv (seed=%d base=%d d=%d)", seed, base, d)
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
	local     *typeInfo
}

// caseState：每个子测试的独立小状态对象（t.Parallel 同构）。
type caseState struct {
	id     int64
	seed   uint64
	base   int64
	tm     map[int64]*typeInfo
	rm     map[int64]*refInfo
	etm    map[int64]*typeInfo
	expr   *astNode
	issues []*issueInfo
}

func newCaseState(id int64, seed uint64, base int64) *caseState {
	tm := make(map[int64]*typeInfo, 8)
	etm := make(map[int64]*typeInfo, 8)
	rm := make(map[int64]*refInfo, 4)
	for i := 1; i <= 8; i++ {
		tm[base+int64(i)] = newTypeInfo(seed, base+100+int64(i))
		etm[base+int64(i)] = newTypeInfo(seed, base+200+int64(i))
	}
	for i := 1; i <= 4; i++ {
		rm[base+int64(i)] = &refInfo{id: base + int64(i), node: nil}
	}
	issues := []*issueInfo{
		{id: base + 1, sev: 1, msg: "g06.issue-1", anchor: tm[base+1]},
		{id: base + 2, sev: 2, msg: "g06.issue-2", anchor: tm[base+2]},
	}
	return &caseState{id: id, seed: seed, base: base, tm: tm, rm: rm, etm: etm, issues: issues}
}

// checkLike：NewCheckedAST 同构——map 构造/迭代、非常量下标局部数组、返回前取址
// 存结果。gate 在浅栈点阻塞（波栅栏）：driver 的一次 GC 同时扫描/收缩整波 case
// 的深栈，copystack → adjustpointers 按栈图扫描本帧（issue1 崩溃路径）。
//
//go:noinline
func checkLike(s *caseState, gate func()) (ret *checkedAST, err error) {
	defer func() {
		if ret == nil {
			failf("g06.checkLike: nil result (case=%d seed=%d base=%d)", s.id, s.seed, s.base)
		}
	}()

	// —— NewCheckedAST 同构：map 构造 + 迭代 ——
	types := make(map[int64]*typeInfo, len(s.tm))
	exprTypes := make(map[int64]*typeInfo, len(s.etm))
	for id, e := range s.tm {
		types[id] = e
	}
	for id, e := range s.etm {
		exprTypes[id] = e
	}

	// —— 非常量下标局部数组 + 返回前取址存到结果 ——
	var arr [4]*typeInfo
	var earr [4]typeInfo
	m := int((s.seed + uint64(s.base)) % 4)
	for i := 0; i < 4; i++ {
		arr[i] = newTypeInfo(s.seed, s.base+1+int64(i))
		earr[i] = *arr[i]
	}
	picked := arr[m]

	ret = &checkedAST{
		expr:      s.expr,
		src:       &srcInfo{seed: s.seed, base: s.base, magic: magicFor(s.seed, s.base+999)},
		types:     types,
		refs:      s.rm,
		exprTypes: exprTypes,
		issues:    s.issues,
		local:     &earr[m],
	}

	gate()

	// 恢复后：跨 shrink 存活根逐项校验。
	checkTypeInfo(picked, s.seed, s.base+1+int64(m), "g06.checkLike/post-gate.picked")
	checkTypeInfo(ret.local, s.seed, s.base+1+int64(m), "g06.checkLike/post-gate.local")
	checkTypeInfo(ret.types[s.base+1], s.seed, s.base+101, "g06.checkLike/post-gate.types")
	checkTypeInfo(ret.exprTypes[s.base+2], s.seed, s.base+202, "g06.checkLike/post-gate.exprTypes")
	if len(ret.issues) != 2 {
		failf("g06.checkLike: issues len=%d want=2 (case=%d)", len(ret.issues), s.id)
	}
	if ret.src.magic != magicFor(s.seed, s.base+999) {
		failf("g06.checkLike: srcInfo corrupted (case=%d)", s.id)
	}
	runtime.KeepAlive(picked)
	runtime.KeepAlive(arr)
	runtime.KeepAlive(earr)
	return ret, nil
}

// programCase：TestInterpreter.func1 → program() 链——AST 构造 → checker.Check
// 深遍历 → NewCheckedAST 构造（浅栈点波栅栏）→ eval 深遍历 → 每子测试独立断言。
//
//go:noinline
func programCase(s *caseState, depth int, gate func()) error {
	s.expr = buildAST(s.seed, s.base, depth)
	atomic.AddInt64(&beats, 1)
	if s.expr == nil {
		return fmt.Errorf("g06.case=%d: nil ast", s.id)
	}

	if sum, want := checkRec(s.seed, s.base, s.expr, depth), checkSumWant(s.base, depth); sum != want {
		failf("g06.case=%d: checkRec checksum=%d want=%d", s.id, sum, want)
	}

	atomic.AddInt64(&beats, 1)
	ret, err := checkLike(s, gate)
	if err != nil {
		return err
	}
	atomic.AddInt64(&beats, 1)

	if sum, want := evalRec(s.seed, s.base, ret.expr, depth), checkSumWant(s.base, depth); sum != want {
		failf("g06.case=%d: eval checksum=%d want=%d", s.id, sum, want)
	}

	m := int((s.seed + uint64(s.base)) % 4)
	checkTypeInfo(ret.local, s.seed, s.base+1+int64(m), "g06.case/post-return.local")
	checkTypeInfo(ret.types[s.base+1], s.seed, s.base+101, "g06.case/post-return.types")

	// weak 强引用断言：抽样子测试（1/16）——结果仍被强引用时 GC 后必须非 nil。
	if s.id%16 == 0 {
		wp := weak.Make(ret.local)
		runtime.GC()
		v := wp.Value()
		if v == nil {
			failf("phase=weak case=%d seed=%d: strong ref held but object collected (栈图漏标/UAF)",
				s.id, s.seed)
		}
		checkTypeInfo(v, s.seed, s.base+1+int64(m), "g06.weak-strong-ref")
		atomic.AddInt64(&gWeakChecks, 1)
	}

	runtime.KeepAlive(ret)
	return nil
}

// ---------- 波栅栏（shrink 放大）与 go test 生命周期 ----------

// waveGate：整波 case 在浅栈点集合，driver 把一批 pending 合并成一次 GC 后
// 放行全部——一次 GC 同时扫/缩 N 个 case 的深栈（issue1 §2 并发放大）。
type waveGate struct {
	reqCh chan struct{}
	ackCh chan struct{}
}

func (g *waveGate) wait() {
	g.reqCh <- struct{}{}
	<-g.ackCh
}

func gateDriver(g *waveGate, stop <-chan struct{}, wg *sync.WaitGroup) {
	defer wg.Done()
	for {
		select {
		case <-stop:
			return
		case <-g.reqCh:
			n := 1
			for {
				select {
				case <-g.reqCh:
					n++
				default:
					runtime.GC()
					atomic.AddInt64(&gGateGC, 1)
					for i := 0; i < n; i++ {
						select {
						case g.ackCh <- struct{}{}:
						case <-stop:
							return
						}
					}
					goto nextBatch
				}
			}
		nextBatch:
		}
	}
}

// gcChurn：常驻高频 GC（issue1 §2 的并发 GC 放大）。不喂心跳。
func gcChurn(stop <-chan struct{}, wg *sync.WaitGroup) {
	defer wg.Done()
	seed := uint64(0x61C88647)
	var buf []*typeInfo
	for {
		select {
		case <-stop:
			return
		default:
		}
		for i := 0; i < 16; i++ {
			buf = append(buf, newTypeInfo(seed, int64(i)))
			if len(buf) > 512 {
				buf = buf[256:]
			}
		}
		runtime.GC()
		atomic.AddInt64(&gChurnGC, 1)
		// window profile（GOMAXPROCS=1）下无休眠的 GC 循环会饿死
		// case/watchdog；让出并限频，GC 压力由 gate driver 保证。
		if runtime.GOMAXPROCS(0) == 1 {
			runtime.Gosched()
			time.Sleep(time.Millisecond)
		}
	}
}

// runWave：go test 生命周期同构——整波子测试并发启动（t.Parallel），
// WaitGroup + 超时控制（go test -timeout）。
func runWave(wave int64, states []*caseState, depth int, gate *waveGate, caseTimeout time.Duration) {
	var wg sync.WaitGroup
	var wgErr int64
	for _, s := range states {
		wg.Add(1)
		go func(s *caseState) {
			defer wg.Done()
			if err := programCase(s, depth, gate.wait); err != nil {
				atomic.AddInt64(&wgErr, 1)
			}
		}(s)
	}
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(caseTimeout):
		failf("phase=wave wave=%d seed=%d: case timeout after %s (go test -timeout 同构)", wave, gSeed, caseTimeout)
	}
	if n := atomic.LoadInt64(&wgErr); n != 0 {
		failf("phase=wave wave=%d seed=%d: %d cases returned error", wave, gSeed, n)
	}
}

// ---------- 通用 harness ----------

var (
	beats       int64
	gBeats      int64
	gWeakChecks int64
	gGateGC     int64
	gChurnGC    int64
	gCasesDone  int64
	gSeed       uint64
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

// parseGMX 解析 STRESS_GMX（如 "8,16"）；空串/全非法返回 nil 表示不改变 GOMAXPROCS。
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
				failf("phase=watchdog: no wave progress for %s (beats=%d)", time.Since(lastChange), cur)
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
	cases := envInt("STRESS_WORKERS", casesDefault, 1, 512)
	hb := envInt("STRESS_HEARTBEAT", 5, 1, 60)
	seed := envSeed(1)
	fixed := int64(envInt("STRESS_FIXED_ROUNDS", fixedRoundsDefault, 1, 1000000))
	depth := envInt("STRESS_DEPTH", depthDefault, 64, 4096)
	caseTimeout := time.Duration(envInt("STRESS_CASE_TIMEOUT", 120, 10, 3600)) * time.Second
	gmx := parseGMX(os.Getenv("STRESS_GMX"))
	phases := len(gmx)
	if phases == 0 {
		phases = 1 // 未设置 STRESS_GMX：不调用 GOMAXPROCS，尊重 profile/环境
	}
	gSeed = seed

	fmt.Printf("scenario=%s phase=preflight go=%s goos=%s goarch=%s gomaxprocs=%d seed=%d seconds=%d cases_per_wave=%d fixed_waves=%d depth=%d heartbeat=%ds case_timeout=%s gmx=%v phases=%d\n",
		scenario, runtime.Version(), runtime.GOOS, runtime.GOARCH, runtime.GOMAXPROCS(0),
		seed, seconds, cases, fixed, depth, hb, caseTimeout, gmx, phases)

	runtime.GC()
	baseGoroutines := runtime.NumGoroutine()

	gate := &waveGate{reqCh: make(chan struct{}, 4096), ackCh: make(chan struct{}, 4096)}
	stop := make(chan struct{})
	var aux sync.WaitGroup
	aux.Add(1)
	go gateDriver(gate, stop, &aux)
	aux.Add(1)
	go gcChurn(stop, &aux)
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
			fmt.Printf("phase=%d/%d gomaxprocs=%d waves=[%d,%d)\n", p+1, phases, runtime.GOMAXPROCS(0), pStart, pEnd)
		}

		for wave := pStart; ; wave++ {
			if seconds > 0 {
				if time.Now().After(deadline) {
					break
				}
			} else if wave >= pEnd {
				break
			}
			states := make([]*caseState, cases)
			for i := range states {
				states[i] = newCaseState(
					int64(p)*1_000_000+wave*1000+int64(i),
					seed*1_000_003+uint64(wave+1)*7919+uint64(i)*104729,
					int64(p)*10_000_000_000_000+wave*1_000_000+int64(i)*50_000,
				)
			}
			runWave(wave, states, depth, gate, caseTimeout)
			atomic.AddInt64(&gCasesDone, int64(cases))
			atomic.AddInt64(&gBeats, 1)
		}
	}

	close(stop)
	aux.Wait()

	if atomic.LoadInt64(&gGateGC) == 0 {
		failf("phase=final: no gate GC happened")
	}
	if atomic.LoadInt64(&gBeats) == 0 {
		failf("phase=final: no waves completed")
	}

	// quiesce：GC 后 goroutine 回基线。
	runtime.GC()
	runtime.GC()
	ng := waitGoroutines(baseGoroutines, 10*time.Second)
	if ng > baseGoroutines+4 {
		failf("phase=final: goroutines baseline=%d after=%d: goroutine leak", baseGoroutines, ng)
	}

	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	fmt.Printf("metrics: waves=%d cases=%d gate_gc=%d churn_gc=%d weak_checks=%d num_gc=%d heap_alloc=%d heap_objects=%d depth=%d gomaxprocs_final=%d goroutines_start=%d goroutines_final=%d\n",
		atomic.LoadInt64(&gBeats), atomic.LoadInt64(&gCasesDone),
		atomic.LoadInt64(&gGateGC), atomic.LoadInt64(&gChurnGC), atomic.LoadInt64(&gWeakChecks),
		m.NumGC, m.HeapAlloc, m.HeapObjects, depth, runtime.GOMAXPROCS(0), baseGoroutines, ng)
	fmt.Printf("STRESS PASS %s\n", scenario)
}
