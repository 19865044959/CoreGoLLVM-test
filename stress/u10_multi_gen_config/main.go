// U10: 三代配置并存热替换（P1，covers L02/L18）。
//
// 目标：把“原子 pointer 换代 + 旧代在途使用 + 引用计数归零后回收”这条路径长跑压测。
// 每代配置包含 map[int64]*canaryNode、[]*canaryNode、interface handler（handler 反向持有
// 配置与 canary），每个节点带确定性 magic。worker 每个请求：
//
//	原子 load 当前代 → refs++ → （每 swapEveryOps 次尝试换代）→ 校验所持代的
//	map/slice/interface/canary 内容 → refs--。
//
// 换代由持旧代引用的请求执行：完整构造新代 → StorePointer → 发布 publishedGen →
// 释放“当前槽位”引用 → 旧代进入 retired 列表。retire goroutine 等所有 worker 都完成过一次
// 换代后的请求（grace，保证不会再有人 load 到旧代）且旧代 refs==0 后丢弃引用并周期性 GC。
// 因此旧代可能在多个请求仍在使用时被换代，这些请求继续校验旧代内容直到 drain——任何
// 提前回收/漏标都会让 magic 校验当场失配。正文见 scenario.md。
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
)

const (
	scenario    = "u10_multi_gen_config"
	cfgEntries  = 96 // 每代 map/slice 条目数
	opsPerRound = 8  // 每个 worker 每轮的请求数
	sampleN     = 8  // 抽样校验节点数
	fullEvery   = 8  // 每 N 个请求做一次全量校验
)

var (
	beats          int64
	requests       int64
	refsAcquired   int64
	swaps          int64
	oldChecks      int64
	fullChecks     int64
	sampleChecks   int64
	probeFinalized int64
	gcChurnCycles  int64
	retireEnqueued int64
	retiredFreed   int64
	retireBatches  int64
	maxAliveGens   int64
	inflight       int64
	started        int64
	finished       int64
	opSeq          int64
	publishedGen   int64
	swapEveryOps   = int64(64)
	gSeed          uint64

	curCfg   unsafe.Pointer // *genConfig
	curProbe unsafe.Pointer // *genProbe（当前代 probe，防误终结）
	swapMu   sync.Mutex

	retiredMu   sync.Mutex
	retiredList []*genConfig
	retireWake  chan struct{}
)

// canaryNode 的 magic 是确定性探针：任何一次读取都要求 magic == canaryMagic(seed,gen,id)。
type canaryNode struct {
	id    int64
	gen   int64
	magic int64
	pad   uint64
	next  *canaryNode
}

// genProbe 只被当前代配置持有、且不反向引用配置，避免 finalizer 对象落入引用环。
type genProbe struct {
	gen   int64
	magic int64
	pad   [2]uint64
}

type handler interface {
	Gen() int64
	Compute(x int64) int64
	Check() error
	Name() string
}

// genHandler 反向持有配置与 canary，让 interface 装箱/拆箱路径也带指针。
type genHandler struct {
	cfg   *genConfig
	self  *canaryNode
	host  *canaryNode
	mul   int64
	label string
}

func (h *genHandler) Gen() int64 { return h.cfg.gen }
func (h *genHandler) Compute(x int64) int64 {
	return h.cfg.offset + x*h.mul
}
func (h *genHandler) Name() string { return h.label }
func (h *genHandler) Check() error {
	if h == nil || h.cfg == nil {
		return fmt.Errorf("nil handler or cfg")
	}
	if h.self == nil || h.host == nil {
		return fmt.Errorf("nil canary self/host")
	}
	if h.self.gen != h.cfg.gen || h.self.magic != canaryMagic(h.cfg.seed, h.cfg.gen, h.self.id) {
		return fmt.Errorf("self canary corrupted id=%d gen=%d magic=%#x", h.self.id, h.self.gen, h.self.magic)
	}
	if h.host.gen != h.cfg.gen || h.host.magic != canaryMagic(h.cfg.seed, h.cfg.gen, h.host.id) {
		return fmt.Errorf("host canary corrupted id=%d gen=%d magic=%#x", h.host.id, h.host.gen, h.host.magic)
	}
	return nil
}

// genConfig 发布后完全不可变（refs 除外）。
type genConfig struct {
	gen     int64
	seed    uint64
	name    string
	offset  int64
	factor  int64
	entries map[int64]*canaryNode
	order   []*canaryNode
	handler handler
	anchor  *canaryNode
	probe   *genProbe
	refs    int64
	pad     [2]uint64
}

func canaryMagic(seed uint64, gen, id int64) int64 {
	x := seed + 0x9e3779b97f4a7c15*uint64(gen+1) + 0xbf58476d1ce4e5b9*uint64(id+2)
	x ^= x >> 30
	x *= 0xbf58476d1ce4e5b9
	x ^= x >> 27
	x *= 0x94d049bb133111eb
	x ^= x >> 31
	return int64(x)
}

func buildConfig(seed uint64, gen int64) *genConfig {
	c := &genConfig{
		gen:    gen,
		seed:   seed,
		offset: gen*1_000_003 + 17,
		factor: 3 + gen%7,
	}
	c.name = fmt.Sprintf("u10-gen-%d", gen)
	c.entries = make(map[int64]*canaryNode, cfgEntries)
	c.order = make([]*canaryNode, cfgEntries)
	for i := 0; i < cfgEntries; i++ {
		n := &canaryNode{id: int64(i), gen: gen, magic: canaryMagic(seed, gen, int64(i))}
		c.entries[n.id] = n
		c.order[i] = n
	}
	for i := 0; i < cfgEntries; i++ {
		c.order[i].next = c.order[(i+1)%cfgEntries]
	}
	h := &genHandler{
		cfg:   c,
		self:  c.order[int((gen*7)%cfgEntries)],
		host:  c.order[int((gen*13+5)%cfgEntries)],
		mul:   c.factor,
		label: c.name,
	}
	c.handler = h
	c.anchor = c.order[int((gen*11+3)%cfgEntries)]
	c.probe = &genProbe{gen: gen, magic: canaryMagic(seed, gen, -7)}
	runtime.SetFinalizer(c.probe, probeFinalizerCB)
	c.refs = 1 // “当前代槽位”引用；换代时释放
	return c
}

// probeFinalizerCB 只能看到旧代 probe：若“当前代”的 probe 被终结，说明当前配置
// 在仍被原子 pointer 发布时被回收（可达性/发布语义错误）。probe 不反向引用 config，
// 因而不会形成带 finalizer 的引用环。
func probeFinalizerCB(p *genProbe) {
	atomic.AddInt64(&probeFinalized, 1)
	if unsafe.Pointer(p) == atomic.LoadPointer(&curProbe) {
		failf("scenario=%s phase=finalizer seed=%d: current probe gen=%d was finalized while still published (atomic pointer reachability broken)",
			scenario, gSeed, p.gen)
	}
}

type worker struct {
	id int64

	seed     uint64
	rounds   int64
	next     int64
	seenGen  int64
	done     int32
	checks   int64
	requests int64
}

func (w *worker) failCfg(c *genConfig, seq int64, phase, where, format string, args ...interface{}) {
	cur := (*genConfig)(atomic.LoadPointer(&curCfg))
	curGen := int64(0)
	if cur != nil {
		curGen = cur.gen
	}
	all := make([]interface{}, 0, 9+len(args))
	all = append(all, scenario, phase, w.id, seq, where, c.gen, curGen, c.gen < curGen, w.seed)
	all = append(all, args...)
	failf("scenario=%s phase=%s worker=%d seq=%d where=%s gen=%d current_gen=%d retired=%v seed=%d: "+format, all...)
}

func (w *worker) checkNode(c *genConfig, n *canaryNode, idx int, seq int64, phase, where string) {
	if n == nil {
		w.failCfg(c, seq, phase, where, "nil *canaryNode (UAF/旧代被提前回收)")
	}
	want := canaryMagic(c.seed, c.gen, int64(idx))
	if n.id != int64(idx) || n.gen != c.gen || n.magic != want {
		w.failCfg(c, seq, phase, where,
			"canary mismatch id=%d want_id=%d gen=%d want_gen=%d magic=%#x want_magic=%#x (UAF/旧代被提前回收)",
			n.id, idx, n.gen, c.gen, n.magic, want)
	}
	if n.next != c.order[(idx+1)%cfgEntries] {
		w.failCfg(c, seq, phase, where, "next pointer broken id=%d", n.id)
	}
	if n.next == nil || n.next.magic != canaryMagic(c.seed, c.gen, n.next.id) {
		w.failCfg(c, seq, phase, where, "next canary corrupted id=%d", n.id)
	}
	w.checks++
}

// verifyConfig 校验 map/slice/interface/anchor 的完整性与 canary。
func (w *worker) verifyConfig(c *genConfig, seq int64, phase string, full bool) {
	if c == nil {
		failf("scenario=%s phase=%s worker=%d seq=%d seed=%d: nil *genConfig", scenario, phase, w.id, seq, w.seed)
	}
	if c.gen <= 0 {
		w.failCfg(c, seq, phase, "config", "invalid gen=%d", c.gen)
	}
	if len(c.order) != cfgEntries || len(c.entries) != cfgEntries {
		w.failCfg(c, seq, phase, "config", "len mismatch order=%d entries=%d want=%d", len(c.order), len(c.entries), cfgEntries)
	}
	if want := fmt.Sprintf("u10-gen-%d", c.gen); c.name != want {
		w.failCfg(c, seq, phase, "name", "expected=%q actual=%q", want, c.name)
	}
	if full {
		for i := 0; i < cfgEntries; i++ {
			if c.order[i] == nil {
				w.failCfg(c, seq, phase, "order", "order[%d] is nil", i)
			}
			if got := c.entries[int64(i)]; got != c.order[i] {
				w.failCfg(c, seq, phase, "entries", "entries[%d] pointer mismatch (map/slice incoherent)", i)
			}
			w.checkNode(c, c.order[i], i, seq, phase, "order")
		}
		atomic.AddInt64(&fullChecks, 1)
	} else {
		for i := 0; i < sampleN; i++ {
			idx := int((seq*131 + int64(i)*17 + w.id) % cfgEntries)
			if got := c.entries[int64(idx)]; got != c.order[idx] {
				w.failCfg(c, seq, phase, "entries", "sample entries[%d] pointer mismatch", idx)
			}
			w.checkNode(c, c.order[idx], idx, seq, phase, "sample")
		}
		atomic.AddInt64(&sampleChecks, 1)
	}
	h, ok := c.handler.(*genHandler)
	if !ok || h == nil {
		w.failCfg(c, seq, phase, "handler", "interface handler type=%T want=*genHandler", c.handler)
	}
	if h.cfg != c {
		w.failCfg(c, seq, phase, "handler", "handler.cfg points to wrong config")
	}
	if h.Gen() != c.gen || h.Name() != c.name {
		w.failCfg(c, seq, phase, "handler", "Gen=%d Name=%q want gen=%d name=%q", h.Gen(), h.Name(), c.gen, c.name)
	}
	x := seq*7 + w.id
	if got, want := c.handler.Compute(x), c.offset+x*c.factor; got != want {
		w.failCfg(c, seq, phase, "handler", "Compute(%d)=%d want=%d", x, got, want)
	}
	if err := c.handler.Check(); err != nil {
		w.failCfg(c, seq, phase, "handler", "handler.Check: %v", err)
	}
	if c.anchor == nil || c.anchor.gen != c.gen || c.anchor.magic != canaryMagic(c.seed, c.gen, c.anchor.id) {
		w.failCfg(c, seq, phase, "anchor", "anchor canary corrupted")
	}
	if c.probe == nil || c.probe.gen != c.gen || c.probe.magic != canaryMagic(c.seed, c.gen, -7) {
		w.failCfg(c, seq, phase, "probe", "probe corrupted")
	}
	w.checks++
}

// acquire 原子取当前代并加引用。publishedGen 先读、cur 后读：若已看到新代发布，
// 再 load 到更旧的代即原子发布回退。
func (w *worker) acquire(seq int64) *genConfig {
	before := atomic.LoadInt64(&publishedGen)
	p := atomic.LoadPointer(&curCfg)
	if p == nil {
		failf("scenario=%s phase=acquire worker=%d seq=%d seed=%d: nil current config pointer", scenario, w.id, seq, w.seed)
	}
	c := (*genConfig)(p)
	if c.gen < before {
		failf("scenario=%s phase=acquire worker=%d seq=%d seed=%d: stale config gen=%d published_gen=%d (atomic swap regression)",
			scenario, w.id, seq, w.seed, c.gen, before)
	}
	atomic.AddInt64(&c.refs, 1)
	atomic.AddInt64(&refsAcquired, 1)
	return c
}

func (w *worker) release(c *genConfig) {
	atomic.AddInt64(&c.refs, -1)
	atomic.StoreInt64(&w.seenGen, c.gen)
	atomic.AddInt64(&inflight, -1)
}

// maybeSwap 由持有旧代引用的请求调用：构造新代 → 原子发布 → 释放槽位引用 → 退役旧代。
func (w *worker) maybeSwap(c *genConfig, seq int64) {
	if atomic.LoadInt64(&publishedGen) != c.gen {
		return
	}
	swapMu.Lock()
	defer swapMu.Unlock()
	if atomic.LoadInt64(&publishedGen) != c.gen {
		return
	}
	nc := buildConfig(gSeed, c.gen+1)
	atomic.StorePointer(&curCfg, unsafe.Pointer(nc))
	atomic.StorePointer(&curProbe, unsafe.Pointer(nc.probe))
	atomic.StoreInt64(&publishedGen, nc.gen)
	if got := (*genConfig)(atomic.LoadPointer(&curCfg)); got != nc {
		w.failCfg(c, seq, "swap", "publish", "current pointer not new gen after StorePointer (got gen=%d want=%d)", got.gen, nc.gen)
	}
	atomic.AddInt64(&swaps, 1)
	atomic.AddInt64(&c.refs, -1) // 当前槽位引用
	retireConfig(c)
	atomic.AddInt64(&beats, 1)
}

func retireConfig(c *genConfig) {
	retiredMu.Lock()
	retiredList = append(retiredList, c)
	n := int64(len(retiredList)) + 1 // + 当前代
	retiredMu.Unlock()
	atomic.AddInt64(&retireEnqueued, 1)
	for {
		old := atomic.LoadInt64(&maxAliveGens)
		if n <= old || atomic.CompareAndSwapInt64(&maxAliveGens, old, n) {
			break
		}
	}
	select {
	case retireWake <- struct{}{}:
	default:
	}
}

// graceReady 表示所有 worker 都已结束（不会再 acquire）或已发布 seenGen >= gen+1，
// 即不会再有新的旧代引用产生。
func graceReady(ws []*worker, gen int64) bool {
	for _, w := range ws {
		if atomic.LoadInt32(&w.done) == 1 {
			continue
		}
		if atomic.LoadInt64(&w.seenGen) >= gen+1 {
			continue
		}
		return false
	}
	return true
}

// drainRetired 回收 grace 已满足且 refs==0 的旧代；final 时要求所有旧代都已 drain。
func drainRetired(ws []*worker, final bool) bool {
	var freed []*genConfig
	retiredMu.Lock()
	if len(retiredList) != 0 {
		rem := make([]*genConfig, 0, len(retiredList))
		for _, c := range retiredList {
			ready := final || graceReady(ws, c.gen)
			if !ready {
				rem = append(rem, c)
				continue
			}
			refs := atomic.LoadInt64(&c.refs)
			if refs != 0 {
				if final {
					retiredMu.Unlock()
					failf("scenario=%s phase=retire seed=%d: retired gen=%d still has refs=%d after all workers finished",
						scenario, gSeed, c.gen, refs)
				}
				rem = append(rem, c)
				continue
			}
			freed = append(freed, c)
			atomic.AddInt64(&retiredFreed, 1)
		}
		retiredList = rem
	}
	retiredMu.Unlock()
	if len(freed) == 0 {
		return false
	}
	// 丢弃引用即可；旧代由独立 gcChurn 的周期性 GC 回收（避免 retire 路径被 GC 阻塞）。
	freed = nil
	atomic.AddInt64(&retireBatches, 1)
	return true
}

func retireLoop(stop <-chan struct{}, wg *sync.WaitGroup, ws []*worker) {
	defer wg.Done()
	t := time.NewTicker(time.Millisecond)
	defer t.Stop()
	for {
		select {
		case <-stop:
			drainRetired(ws, true)
			return
		case <-retireWake:
		case <-t.C:
		}
		drainRetired(ws, false)
	}
}

func (w *worker) request(seq int64) {
	c := w.acquire(seq)
	atomic.AddInt64(&inflight, 1)
	if seq%swapEveryOps == 0 {
		w.maybeSwap(c, seq)
	}
	pub := atomic.LoadInt64(&publishedGen)
	old := c.gen < pub
	if old {
		atomic.AddInt64(&oldChecks, 1)
	}
	// 换代后仍在途的旧代：强制全量校验，确认内容未被提前回收。
	w.verifyConfig(c, seq, "request", old || seq%fullEvery == 0)
	got := c.handler.Compute(seq*7 + w.id)
	want := c.offset + (seq*7+w.id)*c.factor
	if got != want {
		w.failCfg(c, seq, "request", "handler", "Compute mismatch got=%d want=%d", got, want)
	}
	w.requests++
	atomic.AddInt64(&requests, 1)
	w.release(c)
	atomic.AddInt64(&beats, 1)
}

func (w *worker) round(r int64) {
	for i := 0; i < opsPerRound; i++ {
		seq := atomic.AddInt64(&opSeq, 1)
		w.request(seq)
	}
	w.rounds++
	w.next = r + 1
	atomic.AddInt64(&beats, 1)
}

func (w *worker) run(seconds int, deadline time.Time, fixed int64) {
	r := int64(0)
	for {
		if seconds > 0 {
			if time.Now().After(deadline) {
				return
			}
		} else if r >= fixed {
			return
		}
		w.round(r)
		r++
	}
}

func gcChurn(stop <-chan struct{}, wg *sync.WaitGroup) {
	defer wg.Done()
	t := time.NewTicker(2 * time.Millisecond)
	defer t.Stop()
	var sink [][]byte
	for {
		select {
		case <-stop:
			sink = nil
			return
		case <-t.C:
			for i := 0; i < 8; i++ {
				b := make([]byte, 4096)
				b[0] = byte(i)
				sink = append(sink, b)
			}
			for i := range sink {
				sink[i] = nil
			}
			sink = sink[:0]
			runtime.GC()
			atomic.AddInt64(&gcChurnCycles, 1)
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
				failf("scenario=%s phase=watchdog seed=%d: no worker progress for %s (beats=%d)",
					scenario, gSeed, time.Since(lastChange), cur)
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

func failf(format string, args ...interface{}) {
	fmt.Printf("FAIL: "+format+"\n", args...)
	buf := make([]byte, 4<<20)
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

func main() {
	setupCrashOutput()
	seconds := envInt("STRESS_SECONDS", 60, 0, 86400)
	workers := envInt("STRESS_WORKERS", 4, 1, 32)
	hb := envInt("STRESS_HEARTBEAT", 5, 1, 60)
	seed := envSeed(1)
	fixed := int64(envInt("STRESS_FIXED_ROUNDS", 12, 1, 1000000))
	gSeed = seed
	retireWake = make(chan struct{}, 1)

	if seconds == 0 {
		// 小规模固定轮数也要产生若干次换代。
		totalOps := int64(workers) * fixed * opsPerRound
		if totalOps/4 < swapEveryOps {
			swapEveryOps = totalOps / 4
		}
		if swapEveryOps < 1 {
			swapEveryOps = 1
		}
	}

	// preflight：构造第 1 代并做一次全量校验。
	pw := &worker{id: -1, seed: seed}
	pc := buildConfig(seed, 1)
	pw.verifyConfig(pc, 0, "preflight", true)
	if got := atomic.LoadInt64(&pc.refs); got != 1 {
		failf("scenario=%s phase=preflight seed=%d: initial refs=%d want=1", scenario, seed, got)
	}
	atomic.StorePointer(&curCfg, unsafe.Pointer(pc))
	atomic.StorePointer(&curProbe, unsafe.Pointer(pc.probe))
	atomic.StoreInt64(&publishedGen, 1)

	fmt.Printf("scenario=%s phase=preflight go=%s goos=%s goarch=%s gomaxprocs=%d seed=%d seconds=%d workers=%d fixed_rounds=%d heartbeat=%ds entries=%d ops_per_round=%d swap_every_ops=%d\n",
		scenario, runtime.Version(), runtime.GOOS, runtime.GOARCH, runtime.GOMAXPROCS(0), seed, seconds, workers, fixed, hb,
		cfgEntries, opsPerRound, swapEveryOps)

	runtime.GC()
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
	aux.Add(1)
	go retireLoop(stop, &aux, ws)

	var wg sync.WaitGroup
	for _, w := range ws {
		wg.Add(1)
		go func(w *worker) {
			defer wg.Done()
			atomic.AddInt64(&started, 1)
			defer func() {
				atomic.StoreInt32(&w.done, 1)
				atomic.AddInt64(&finished, 1)
			}()
			w.run(seconds, deadline, fixed)
		}(w)
	}

	wg.Wait()
	close(stop)
	aux.Wait() // retireLoop 在这里 drain 全部旧代

	// 收尾：全量校验当前代 + 引用/计数一致性。
	cur := (*genConfig)(atomic.LoadPointer(&curCfg))
	pw.verifyConfig(cur, atomic.LoadInt64(&opSeq), "final", true)
	if got, want := atomic.LoadInt64(&publishedGen), cur.gen; got != want {
		failf("scenario=%s phase=final seed=%d: published_gen=%d current_gen=%d", scenario, seed, got, want)
	}
	if got := atomic.LoadInt64(&cur.refs); got != 1 {
		failf("scenario=%s phase=final seed=%d: current gen=%d refs=%d want=1 (slot ref only)", scenario, seed, cur.gen, got)
	}
	if got := atomic.LoadInt64(&inflight); got != 0 {
		failf("scenario=%s phase=final seed=%d: inflight=%d want=0", scenario, seed, got)
	}
	if got, want := atomic.LoadInt64(&requests), atomic.LoadInt64(&refsAcquired); got != want {
		failf("scenario=%s phase=final seed=%d: requests=%d refs_acquired=%d", scenario, seed, got, want)
	}
	if got, want := atomic.LoadInt64(&retiredFreed), atomic.LoadInt64(&retireEnqueued); got != want {
		failf("scenario=%s phase=final seed=%d: retired_freed=%d retire_enqueued=%d (old generation not drained)",
			scenario, seed, got, want)
	}
	if got, want := atomic.LoadInt64(&swaps), atomic.LoadInt64(&retireEnqueued); got != want {
		failf("scenario=%s phase=final seed=%d: swaps=%d retire_enqueued=%d", scenario, seed, got, want)
	}
	if s, f := atomic.LoadInt64(&started), atomic.LoadInt64(&finished); s != f || s != int64(workers) {
		failf("scenario=%s phase=final seed=%d: started=%d finished=%d workers=%d", scenario, seed, s, f, workers)
	}
	if seconds == 0 {
		var reqs int64
		for _, w := range ws {
			if w.next != fixed {
				failf("scenario=%s phase=final seed=%d worker=%d: rounds=%d want=%d", scenario, seed, w.id, w.next, fixed)
			}
			reqs += w.requests
		}
		if want := int64(workers) * fixed * opsPerRound; atomic.LoadInt64(&opSeq) != want || reqs != want {
			failf("scenario=%s phase=final seed=%d: ops=%d requests=%d want=%d", scenario, seed, atomic.LoadInt64(&opSeq), reqs, want)
		}
	}
	if sw := atomic.LoadInt64(&swaps); sw > 0 && atomic.LoadInt64(&oldChecks) < sw {
		failf("scenario=%s phase=final seed=%d: old_gen_checks=%d < swaps=%d (in-flight old gen not exercised)",
			scenario, seed, atomic.LoadInt64(&oldChecks), sw)
	}
	if atomic.LoadInt64(&beats) == 0 {
		failf("scenario=%s phase=final seed=%d: no progress completed", scenario, seed)
	}

	ng := waitGoroutines(baseGoroutines, 5*time.Second)
	if ng > baseGoroutines+4 {
		failf("scenario=%s phase=final seed=%d: goroutine baseline=%d after=%d: goroutine leak", scenario, seed, baseGoroutines, ng)
	}

	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	fmt.Printf("metrics: rounds_ops=%d requests=%d refs_acquired=%d swaps=%d old_gen_checks=%d full_checks=%d sample_checks=%d retire_enqueued=%d retired_freed=%d retire_batches=%d max_alive_gens=%d probe_finalized=%d gc_churn=%d beats=%d num_gc=%d heap_alloc=%d heap_objects=%d goroutines_start=%d goroutines_final=%d\n",
		atomic.LoadInt64(&opSeq), atomic.LoadInt64(&requests), atomic.LoadInt64(&refsAcquired), atomic.LoadInt64(&swaps),
		atomic.LoadInt64(&oldChecks), atomic.LoadInt64(&fullChecks), atomic.LoadInt64(&sampleChecks),
		atomic.LoadInt64(&retireEnqueued), atomic.LoadInt64(&retiredFreed), atomic.LoadInt64(&retireBatches),
		atomic.LoadInt64(&maxAliveGens), atomic.LoadInt64(&probeFinalized), atomic.LoadInt64(&gcChurnCycles),
		atomic.LoadInt64(&beats), m.NumGC, m.HeapAlloc, m.HeapObjects, baseGoroutines, ng)
	fmt.Printf("STRESS PASS %s\n", scenario)
}
