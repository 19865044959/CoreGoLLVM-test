// U12: 单 map 扩容搬迁长跑（P0，covers L13/L18）。
//
// 单个 map[int64]*mapNode 从 0 增长到 STRESS_MAP_MAX（默认 1<<20）：
//
//	grow    ：写者按递增 key 插入，多次触发内部扩容/搬迁；
//	delete  ：后半程逐 key 删除（再触发搬迁路径），读到即删；
//	reinsert：删除完成后按每 64 个 key 重插 1 个（新 gen=2 节点）。
//
// 写者每次只在写锁内做一小块（chunk），读者并发持有读锁做：
//   - 随机 key 存在性探测（按 phase/frontier 判定期望，present→校验 canary）；
//   - frontier 边界探测（未插入/已删除/未重插必须缺失）；
//   - range 遍历抽样校验（扩容期间迭代旧 bucket）。
//
// 每个访问都要求 key、gen、magic 与 mapMagic(seed,key,gen) 一致；节点被提前回收/复用会当场失配。
// 固定轮数模式：writer 轮数 = STRESS_FIXED_ROUNDS，按 6:3:1 分配 grow/delete/reinsert（fixed>=3 时
// 三阶段各至少 1 轮）；reader 持续遍历到 writer 完成全部阶段。时间模式按 elapsed 比例推进三阶段，
// 收尾时强制补齐，保证最终状态完整。正文见 scenario.md。
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
	scenario      = "u12_map_grow_stress"
	reinsertEvery = int64(64)
	growFrac      = 0.60
	deleteFrac    = 0.85
	probesPerRead = 64
	iterSamples   = 2048
)

const (
	phGrow int32 = iota
	phDelete
	phReinsert
	phDone
)

var (
	mu            sync.RWMutex
	m             map[int64]*mapNode
	maxKeys       int64
	chunk         int64
	reinsertTotal int64

	phase      int32 // 受 mu 保护
	inserted   int64
	deleted    int64
	reinserted int64

	beats      int64
	checks     int64
	fullChecks int64
	gcCycles   int64
	started    int64
	finished   int64
	gSeed      uint64
	writerDone chan struct{}
)

type mapNode struct {
	key   int64
	magic int64
	gen   int32
	pad   int32
}

func mapMagic(seed uint64, key, gen int64) int64 {
	x := seed + 0x9e3779b97f4a7c15*uint64(key+1) + 0x517cc1b727220a95*uint64(gen+2)
	x ^= x >> 30
	x *= 0xbf58476d1ce4e5b9
	x ^= x >> 27
	x *= 0x94d049bb133111eb
	x ^= x >> 31
	return int64(x)
}

func phaseName(p int32) string {
	switch p {
	case phGrow:
		return "grow"
	case phDelete:
		return "delete"
	case phReinsert:
		return "reinsert"
	case phDone:
		return "done"
	}
	return "unknown"
}

func failMap(worker, round, key int64, where, format string, args ...interface{}) {
	p := atomic.LoadInt32(&phase)
	all := make([]interface{}, 0, 6+len(args))
	all = append(all, scenario, phaseName(p), worker, round, key, gSeed)
	all = append(all, args...)
	failf("scenario=%s phase=%s worker=%d round=%d key=%d seed=%d where="+where+": "+format, all...)
}

// presentAt 是给定读者快照（phase/frontier）下的期望存在性。
func presentAt(k int64, ph int32, ins, del, rei int64) bool {
	switch ph {
	case phGrow:
		return k < ins
	case phDelete:
		return k >= del
	case phReinsert, phDone:
		if k%reinsertEvery != 0 {
			return false
		}
		return k/reinsertEvery < rei
	}
	return false
}

// checkNodeLocked 要求调用方持有 mu（读锁或写锁）。
func checkNodeLocked(worker, round, k int64, n *mapNode, where string) {
	if n == nil {
		failMap(worker, round, k, where, "nil *mapNode (UAF/entry 被提前回收)")
	}
	if n.key != k {
		failMap(worker, round, k, where, "node key=%d want=%d", n.key, k)
	}
	if n.gen != 1 && n.gen != 2 {
		failMap(worker, round, k, where, "node gen=%d want 1|2", n.gen)
	}
	if want := mapMagic(gSeed, k, int64(n.gen)); n.magic != want {
		failMap(worker, round, k, where, "canary mismatch gen=%d magic=%#x want=%#x (UAF/搬迁损坏)", n.gen, n.magic, want)
	}
	atomic.AddInt64(&checks, 1)
}

type worker struct {
	id     int64
	rnd    uint64
	rounds int64
	next   int64
	checks int64
	done   int32
}

func (w *worker) nextRand() uint64 {
	x := w.rnd
	x ^= x << 13
	x ^= x >> 7
	x ^= x << 17
	w.rnd = x
	return x
}

func (w *worker) probeLocked(ph int32, ins, del, rei, k int64, where string) {
	n, ok := m[k]
	want := presentAt(k, ph, ins, del, rei)
	if ok != want {
		failMap(w.id, w.next, k, where, "present=%v want=%v (phase=%s inserted=%d deleted=%d reinserted=%d)",
			ok, want, phaseName(ph), ins, del, rei)
	}
	if ok {
		checkNodeLocked(w.id, w.next, k, n, where)
		if ph >= phReinsert && n.gen != 2 {
			failMap(w.id, w.next, k, where, "reinserted node gen=%d want=2", n.gen)
		}
		if ph < phReinsert && n.gen != 1 {
			failMap(w.id, w.next, k, where, "grow/delete node gen=%d want=1", n.gen)
		}
	}
}

func (w *worker) frontierProbesLocked(ph int32, ins, del, rei int64) {
	switch ph {
	case phGrow:
		if ins > 0 {
			w.probeLocked(ph, ins, del, rei, ins-1, "grow.frontier.present")
		}
		if ins < maxKeys {
			w.probeLocked(ph, ins, del, rei, ins, "grow.frontier.absent")
		}
		w.probeLocked(ph, ins, del, rei, maxKeys-1, "grow.tail")
	case phDelete:
		if del > 0 {
			w.probeLocked(ph, ins, del, rei, del-1, "delete.frontier.absent")
		}
		if del < maxKeys {
			w.probeLocked(ph, ins, del, rei, del, "delete.frontier.present")
		}
		w.probeLocked(ph, ins, del, rei, maxKeys-1, "delete.tail")
	case phReinsert, phDone:
		if rei > 0 {
			w.probeLocked(ph, ins, del, rei, (rei-1)*reinsertEvery, "reinsert.frontier.present")
		}
		if rei < reinsertTotal {
			w.probeLocked(ph, ins, del, rei, rei*reinsertEvery, "reinsert.frontier.absent")
		}
		w.probeLocked(ph, ins, del, rei, reinsertEvery-1, "reinsert.nonmultiple.absent")
	}
}

// readRound 在读锁内做一轮抽样：随机探测 + 边界探测 + range 抽样校验。
func (w *worker) readRound() {
	mu.RLock()
	ph := phase
	ins, del, rei := inserted, deleted, reinserted

	for i := 0; i < probesPerRead; i++ {
		k := int64(w.nextRand() % uint64(maxKeys))
		w.probeLocked(ph, ins, del, rei, k, "random")
	}
	w.frontierProbesLocked(ph, ins, del, rei)

	visited := 0
	for k, n := range m {
		if !presentAt(k, ph, ins, del, rei) {
			failMap(w.id, w.next, k, "iter", "iterated unexpected key (phase=%s inserted=%d deleted=%d reinserted=%d)",
				phaseName(ph), ins, del, rei)
		}
		checkNodeLocked(w.id, w.next, k, n, "iter")
		if ph >= phReinsert && n.gen != 2 {
			failMap(w.id, w.next, k, "iter", "reinserted node gen=%d want=2", n.gen)
		}
		if ph < phReinsert && n.gen != 1 {
			failMap(w.id, w.next, k, "iter", "grow/delete node gen=%d want=1", n.gen)
		}
		visited++
		if visited >= iterSamples {
			break
		}
	}
	mu.RUnlock()
	w.checks += int64(probesPerRead + 3 + visited)
	atomic.AddInt64(&beats, 1)
	w.rounds++
	w.next++
}

type writer struct {
	id     int64
	rounds int64
	next   int64
	done   int32
}

func (wr *writer) beat() {
	atomic.AddInt64(&beats, 1)
}

func (wr *writer) growChunk(target int64) (done int64) {
	mu.Lock()
	if inserted >= target {
		mu.Unlock()
		return 0
	}
	from := inserted
	end := target
	if end > from+chunk {
		end = from + chunk
	}
	for k := from; k < end; k++ {
		n := &mapNode{key: k, gen: 1, magic: mapMagic(gSeed, k, 1)}
		m[k] = n
		if got := m[k]; got != n {
			mu.Unlock()
			failMap(wr.id, wr.next, k, "grow.insert", "map read-back pointer mismatch")
		}
		checkNodeLocked(wr.id, wr.next, k, n, "grow.insert")
		inserted++
	}
	mu.Unlock()
	wr.beat()
	return end - from
}

func (wr *writer) deleteChunk(target int64) (done int64) {
	mu.Lock()
	if deleted >= target {
		mu.Unlock()
		return 0
	}
	from := deleted
	end := target
	if end > from+chunk {
		end = from + chunk
	}
	for k := from; k < end; k++ {
		n, ok := m[k]
		if !ok {
			mu.Unlock()
			failMap(wr.id, wr.next, k, "delete", "expected key present before delete")
		}
		checkNodeLocked(wr.id, wr.next, k, n, "delete")
		if n.gen != 1 {
			mu.Unlock()
			failMap(wr.id, wr.next, k, "delete", "node gen=%d want=1", n.gen)
		}
		delete(m, k)
		deleted++
	}
	mu.Unlock()
	wr.beat()
	return end - from
}

func (wr *writer) reinsertChunk(target int64) (done int64) {
	step := chunk / reinsertEvery
	if step < 16 {
		step = 16
	}
	mu.Lock()
	if reinserted >= target {
		mu.Unlock()
		return 0
	}
	from := reinserted
	end := target
	if end > from+step {
		end = from + step
	}
	for j := from; j < end; j++ {
		k := j * reinsertEvery
		if k >= maxKeys {
			mu.Unlock()
			failMap(wr.id, wr.next, k, "reinsert", "slot j=%d out of key space", j)
		}
		n := &mapNode{key: k, gen: 2, magic: mapMagic(gSeed, k, 2)}
		if _, exists := m[k]; exists {
			mu.Unlock()
			failMap(wr.id, wr.next, k, "reinsert", "key already present before reinsert")
		}
		m[k] = n
		if got := m[k]; got != n {
			mu.Unlock()
			failMap(wr.id, wr.next, k, "reinsert", "map read-back pointer mismatch")
		}
		checkNodeLocked(wr.id, wr.next, k, n, "reinsert")
		reinserted++
	}
	mu.Unlock()
	wr.beat()
	return end - from
}

func (wr *writer) setPhase(next int32) {
	mu.Lock()
	switch next {
	case phDelete:
		if phase == phGrow && inserted == maxKeys {
			phase = phDelete
		}
	case phReinsert:
		if phase == phDelete && deleted == maxKeys {
			phase = phReinsert
		}
	case phDone:
		if phase == phReinsert && reinserted == reinsertTotal {
			phase = phDone
		}
	}
	mu.Unlock()
}

// verifyAllLocked 全量校验当前 phase 下的期望集合；调用时持有写锁。
func (wr *writer) verifyAll(expectLen int64, where string) {
	mu.Lock()
	defer mu.Unlock()
	if int64(len(m)) != expectLen {
		failMap(wr.id, wr.next, -1, where, "map len=%d want=%d", len(m), expectLen)
	}
	for k, n := range m {
		if !presentAt(k, phase, inserted, deleted, reinserted) {
			failMap(wr.id, wr.next, k, where, "unexpected key in phase=%s", phaseName(phase))
		}
		checkNodeLocked(wr.id, wr.next, k, n, where)
	}
	atomic.AddInt64(&fullChecks, 1)
}

// ensureGrowComplete：补齐 grow、全量校验、切到 delete。
func (wr *writer) ensureGrowComplete() int64 {
	if phase > phGrow {
		return 0
	}
	ops := int64(0)
	for {
		n := wr.growChunk(maxKeys)
		ops += n
		if n == 0 {
			break
		}
	}
	wr.verifyAll(maxKeys, "grow.complete")
	wr.setPhase(phDelete)
	return ops
}

// ensureDeleteComplete：补齐 delete、确认空 map、切到 reinsert。
func (wr *writer) ensureDeleteComplete() int64 {
	if phase > phDelete {
		return 0
	}
	ops := wr.ensureGrowComplete()
	for {
		n := wr.deleteChunk(maxKeys)
		ops += n
		if n == 0 {
			break
		}
	}
	wr.verifyAll(0, "delete.complete")
	wr.setPhase(phReinsert)
	return ops
}

// ensureReinsertComplete：补齐 reinsert、全量校验、切到 done。
func (wr *writer) ensureReinsertComplete() int64 {
	if phase == phDone {
		return 0
	}
	ops := wr.ensureDeleteComplete()
	for {
		n := wr.reinsertChunk(reinsertTotal)
		ops += n
		if n == 0 {
			break
		}
	}
	wr.verifyAll(reinsertTotal, "reinsert.complete")
	wr.setPhase(phDone)
	return ops
}

// roundFixed 执行固定轮数计划中的一个轮次；grow/delete/reinsert 轮数由 main 预分配。
func (wr *writer) roundFixed(r, growRounds, deleteRounds, reinsertRounds int64) int64 {
	ops := int64(0)
	switch {
	case r < growRounds:
		target := maxKeys * (r + 1) / growRounds
		for {
			n := wr.growChunk(target)
			ops += n
			if n == 0 {
				break
			}
		}
		if r == growRounds-1 {
			ops += wr.ensureGrowComplete() // 全量校验 + 切 delete
		}
	case r < growRounds+deleteRounds:
		ops += wr.ensureGrowComplete()
		wr.setPhase(phDelete)
		j := r - growRounds
		target := maxKeys * (j + 1) / deleteRounds
		for {
			n := wr.deleteChunk(target)
			ops += n
			if n == 0 {
				break
			}
		}
		if j == deleteRounds-1 {
			ops += wr.ensureDeleteComplete() // 空 map 校验 + 切 reinsert
		}
	default:
		ops += wr.ensureDeleteComplete()
		wr.setPhase(phReinsert)
		j := r - growRounds - deleteRounds
		target := reinsertTotal * (j + 1) / reinsertRounds
		for {
			n := wr.reinsertChunk(target)
			ops += n
			if n == 0 {
				break
			}
		}
		if j == reinsertRounds-1 {
			ops += wr.ensureReinsertComplete()
		}
	}
	return ops
}

// roundTimed 按 elapsed 比例推进三阶段。
func (wr *writer) roundTimed(start time.Time, total time.Duration) int64 {
	elapsed := time.Since(start)
	frac := 1.0
	if total > 0 {
		frac = float64(elapsed) / float64(total)
	}
	if frac > 1 {
		frac = 1
	}
	ops := int64(0)
	switch {
	case frac < growFrac:
		target := int64(float64(maxKeys) * frac / growFrac)
		for {
			n := wr.growChunk(target)
			ops += n
			if n == 0 {
				break
			}
		}
	case frac < deleteFrac:
		ops += wr.ensureGrowComplete()
		wr.setPhase(phDelete)
		target := int64(float64(maxKeys) * (frac - growFrac) / (deleteFrac - growFrac))
		for {
			n := wr.deleteChunk(target)
			ops += n
			if n == 0 {
				break
			}
		}
	default:
		ops += wr.ensureDeleteComplete()
		wr.setPhase(phReinsert)
		target := int64(float64(reinsertTotal) * (frac - deleteFrac) / (1 - deleteFrac))
		for {
			n := wr.reinsertChunk(target)
			ops += n
			if n == 0 {
				break
			}
		}
		if frac >= 0.999 {
			wr.ensureReinsertComplete()
			wr.setPhase(phDone)
		}
	}
	return ops
}

func (wr *writer) run(seconds int, deadline, start time.Time, fixed, growRounds, deleteRounds, reinsertRounds int64) {
	defer close(writerDone)
	r := int64(0)
	for {
		if seconds > 0 {
			if time.Now().After(deadline) {
				return
			}
		} else if r >= fixed {
			return
		}
		var ops int64
		if seconds > 0 {
			ops = wr.roundTimed(start, deadline.Sub(start))
		} else {
			ops = wr.roundFixed(r, growRounds, deleteRounds, reinsertRounds)
		}
		if ops > 0 {
			runtime.GC()
			atomic.AddInt64(&gcCycles, 1)
		} else if seconds > 0 {
			time.Sleep(time.Millisecond)
			atomic.AddInt64(&beats, 1)
		}
		wr.rounds++
		wr.next = r + 1
		r++
	}
}

func (w *worker) run(seconds int, deadline time.Time) {
	r := int64(0)
	for {
		if seconds > 0 {
			if time.Now().After(deadline) {
				return
			}
		} else {
			select {
			case <-writerDone:
				return
			default:
			}
		}
		w.readRound()
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
				failf("scenario=%s phase=watchdog seed=%d: no progress for %s (beats=%d)", scenario, gSeed, time.Since(lastChange), cur)
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
	workers := envInt("STRESS_WORKERS", 4, 1, 16)
	hb := envInt("STRESS_HEARTBEAT", 5, 1, 60)
	seed := envSeed(1)
	fixed := int64(envInt("STRESS_FIXED_ROUNDS", 16, 1, 1000000))
	maxKeys = int64(envInt("STRESS_MAP_MAX", 1<<20, 1024, 1<<22))
	gSeed = seed
	m = make(map[int64]*mapNode)
	reinsertTotal = (maxKeys + reinsertEvery - 1) / reinsertEvery
	chunk = maxKeys / 64
	if chunk < 256 {
		chunk = 256
	}
	if chunk > 4096 {
		chunk = 4096
	}
	writerDone = make(chan struct{})

	var growRounds, deleteRounds, reinsertRounds int64
	if seconds == 0 {
		switch {
		case fixed >= 3:
			growRounds = fixed * 3 / 5
			if growRounds < 1 {
				growRounds = 1
			}
			deleteRounds = fixed * 3 / 10
			if deleteRounds < 1 {
				deleteRounds = 1
			}
			reinsertRounds = fixed - growRounds - deleteRounds
			if reinsertRounds < 0 {
				reinsertRounds = 0
			}
		case fixed == 2:
			growRounds, deleteRounds, reinsertRounds = 1, 1, 0
		default:
			growRounds, deleteRounds, reinsertRounds = 1, 0, 0
		}
	} else {
		// 时间模式下的计划轮数只用于日志；实际按 elapsed 推进。
		growRounds, deleteRounds, reinsertRounds = 6, 3, 1
	}

	fmt.Printf("scenario=%s phase=preflight go=%s goos=%s goarch=%s gomaxprocs=%d seed=%d seconds=%d workers=%d fixed_rounds=%d heartbeat=%ds map_max=%d chunk=%d reinsert_every=%d reinsert_total=%d plan_grow=%d plan_delete=%d plan_reinsert=%d\n",
		scenario, runtime.Version(), runtime.GOOS, runtime.GOARCH, runtime.GOMAXPROCS(0), seed, seconds, workers, fixed, hb,
		maxKeys, chunk, reinsertEvery, reinsertTotal, growRounds, deleteRounds, reinsertRounds)

	baseGoroutines := runtime.NumGoroutine()
	start := time.Now()
	deadline := start.Add(time.Duration(seconds) * time.Second)

	wr := &writer{id: -1}
	ws := make([]*worker, workers)
	for i := 0; i < workers; i++ {
		rnd := seed*1_000_003 + uint64(i+1)*7919
		if rnd == 0 {
			rnd = 1
		}
		ws[i] = &worker{id: int64(i), rnd: rnd}
	}

	stop := make(chan struct{})
	var aux sync.WaitGroup
	aux.Add(1)
	go watchdog(time.Duration(hb)*time.Second, stop, &aux)

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		atomic.AddInt64(&started, 1)
		defer func() {
			atomic.StoreInt32(&wr.done, 1)
			atomic.AddInt64(&finished, 1)
		}()
		wr.run(seconds, deadline, start, fixed, growRounds, deleteRounds, reinsertRounds)
	}()
	for _, w := range ws {
		wg.Add(1)
		go func(w *worker) {
			defer wg.Done()
			atomic.AddInt64(&started, 1)
			defer func() {
				atomic.StoreInt32(&w.done, 1)
				atomic.AddInt64(&finished, 1)
			}()
			w.run(seconds, deadline)
		}(w)
	}

	wg.Wait()
	close(stop)
	aux.Wait()

	// 收尾补齐：时间模式可能因机器慢停在中间阶段；固定轮数在 fixed<3 时也可能缺阶段。
	wr.ensureReinsertComplete()
	wr.verifyAll(reinsertTotal, "final")

	if s, f := atomic.LoadInt64(&started), atomic.LoadInt64(&finished); s != f || s != int64(workers+1) {
		failf("scenario=%s phase=final seed=%d: started=%d finished=%d want=%d", scenario, seed, s, f, workers+1)
	}
	if seconds == 0 && wr.next != fixed {
		failf("scenario=%s phase=final seed=%d: writer rounds=%d want=%d", scenario, seed, wr.next, fixed)
	}
	if phase != phDone || inserted != maxKeys || deleted != maxKeys || reinserted != reinsertTotal {
		failf("scenario=%s phase=final seed=%d: phase=%s inserted=%d deleted=%d reinserted=%d (want done/%d/%d/%d)",
			scenario, seed, phaseName(phase), inserted, deleted, reinserted, maxKeys, maxKeys, reinsertTotal)
	}
	if int64(len(m)) != reinsertTotal {
		failf("scenario=%s phase=final seed=%d: map len=%d want=%d", scenario, seed, len(m), reinsertTotal)
	}
	var readerRounds int64
	for _, w := range ws {
		readerRounds += w.rounds
	}
	if readerRounds < 1 {
		failf("scenario=%s phase=final seed=%d: readers completed no traversal rounds", scenario, seed)
	}
	if atomic.LoadInt64(&checks) == 0 {
		failf("scenario=%s phase=final seed=%d: no canary checks executed", scenario, seed)
	}

	ng := waitGoroutines(baseGoroutines, 5*time.Second)
	if ng > baseGoroutines+4 {
		failf("scenario=%s phase=final seed=%d: goroutine baseline=%d after=%d: goroutine leak", scenario, seed, baseGoroutines, ng)
	}

	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	fmt.Printf("metrics: writer_rounds=%d reader_rounds=%d map_len=%d inserted=%d deleted=%d reinserted=%d canary_checks=%d full_checks=%d gc_rounds=%d beats=%d num_gc=%d heap_alloc=%d heap_objects=%d goroutines_start=%d goroutines_final=%d\n",
		wr.next, readerRounds, len(m), inserted, deleted, reinserted, atomic.LoadInt64(&checks),
		atomic.LoadInt64(&fullChecks), atomic.LoadInt64(&gcCycles), atomic.LoadInt64(&beats), ms.NumGC,
		ms.HeapAlloc, ms.HeapObjects, baseGoroutines, ng)
	fmt.Printf("STRESS PASS %s\n", scenario)
}
