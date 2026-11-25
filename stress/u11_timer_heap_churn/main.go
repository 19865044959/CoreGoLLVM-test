// U11: timer 堆增删/sift 风暴（P0，covers L19/L04）。
//
// 每 worker 每轮并发创建 timersPerRound 个一次性 time.AfterFunc：
//   - 短周期（1µs..2ms）+ 少量短周期立即 Stop（制造 Stop 与到期竞态）；
//   - 中周期（500ms）在快照后 Stop（Stop 失败则回调补记）；
//   - 长周期（1h）在快照后 Stop，绝不允许到期。
//
// 回调只捕获自己的 timerRec（含确定性 canary 及其 aux canary），到期先校验 magic，
// 再从 registry 中自删除。每个 rec 先入 registry 再 arm timer，保证回调自删除时条目存在。
// 每轮校验 到期 + 停止 + 未到期 == created，随后等到 pending 归零、registry 清空、GC。
// 大量随机构建/删除让 runtime timer heap 持续 sift；到期回调在独立 goroutine 中读指针，
// 提前回收/漏标会当场 magic 失配。正文见 scenario.md。
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
	scenario       = "u11_timer_heap_churn"
	timersPerRound = 4096
	beatsPerCreate = 64
)

const (
	stPending int32 = iota
	stFired
	stStopped
)

const (
	kindShort int32 = iota
	kindMedium
	kindLong
)

var (
	beats            int64
	totalCreated     int64
	totalFired       int64
	totalStopped     int64
	totalStopTrue    int64
	totalStopFalse   int64
	totalLongCreated int64
	totalLongFired   int64
	gcRounds         int64
	gcChurnCycles    int64
	started          int64
	finished         int64
	gSeed            uint64
)

// timerCanary 的 magic 是确定性探针；next 指向 aux canary（只被本条 rec 引用）。
type timerCanary struct {
	id    int64
	gen   int64
	magic int64
	pad   uint64
	next  *timerCanary
}

type timerRec struct {
	worker *timerWorker
	round  int64
	id     int64
	gen    int64
	kind   int32
	state  int32 // stPending / stFired / stStopped
	canary *timerCanary
	timer  *time.Timer
}

type timerWorker struct {
	id     int64
	seed   uint64
	rounds int64
	next   int64
	done   int32

	mu        sync.Mutex
	reg       map[int64]*timerRec
	pending   int64
	fired     int64
	stopped   int64
	stopTrue  int64
	longFired int64
}

func timerMagic(seed uint64, gen, id int64) int64 {
	x := seed + 0x2545f4914f6cdd1d*uint64(gen+3) + 0x9e3779b97f4a7c15*uint64(id+5)
	x ^= x >> 33
	x *= 0xff51afd7ed558ccd
	x ^= x >> 33
	x *= 0xc4ceb9fe1a85ec53
	x ^= x >> 33
	return int64(x)
}

func (w *timerWorker) fail(rec *timerRec, phase, where, format string, args ...interface{}) {
	all := make([]interface{}, 0, 8+len(args))
	all = append(all, scenario, phase, w.id, rec.round, rec.id, rec.gen, atomic.LoadInt32(&rec.state), w.seed)
	all = append(all, args...)
	failf("scenario=%s phase=%s worker=%d round=%d id=%d gen=%d state=%d seed=%d where="+where+": "+format, all...)
}

// fire 是 AfterFunc 回调：校验 canary → 状态迁移 → registry 自删除。
func (w *timerWorker) fire(rec *timerRec) {
	c := rec.canary
	if c == nil {
		w.fail(rec, "callback", "canary", "nil canary (timer closure lost pointer)")
	}
	want := timerMagic(w.seed, rec.gen, rec.id)
	if c.id != rec.id || c.gen != rec.gen || c.magic != want {
		w.fail(rec, "callback", "canary",
			"canary mismatch magic=%#x want=%#x (timer callback pointer corrupted/UAF)", c.magic, want)
	}
	if c.next == nil {
		w.fail(rec, "callback", "aux", "nil aux canary")
	}
	if got, wantAux := c.next.magic, timerMagic(w.seed, c.next.gen, c.next.id); got != wantAux {
		w.fail(rec, "callback", "aux", "aux canary magic=%#x want=%#x (UAF)", got, wantAux)
	}
	if !atomic.CompareAndSwapInt32(&rec.state, stPending, stFired) {
		w.fail(rec, "callback", "state", "callback ran but state=%d (Stop returned true yet callback executed)", atomic.LoadInt32(&rec.state))
	}

	w.mu.Lock()
	got, ok := w.reg[rec.id]
	if !ok || got != rec {
		w.mu.Unlock()
		w.fail(rec, "callback", "registry", "self-delete failed: entry missing/mismatched (ok=%v)", ok)
	}
	delete(w.reg, rec.id)
	w.fired++
	w.pending--
	if rec.kind == kindLong {
		w.longFired++
	}
	w.mu.Unlock()
}

// stopRec 停止 medium/long（以及少量 short）；Stop=false 表示已到期，由回调补记。
func (w *timerWorker) stopRec(rec *timerRec) {
	ok := rec.timer.Stop()
	if !ok {
		atomic.AddInt64(&totalStopFalse, 1)
		if rec.kind == kindLong {
			w.fail(rec, "stop", "long", "1h timer Stop()=false (expired before stop)")
		}
		return
	}
	if !atomic.CompareAndSwapInt32(&rec.state, stPending, stStopped) {
		w.fail(rec, "stop", "state", "Stop()=true but state=%d (callback raced with successful Stop)", atomic.LoadInt32(&rec.state))
	}
	w.mu.Lock()
	got, found := w.reg[rec.id]
	if !found || got != rec {
		w.mu.Unlock()
		w.fail(rec, "stop", "registry", "registry entry missing/mismatched after Stop")
	}
	delete(w.reg, rec.id)
	w.stopped++
	w.stopTrue++
	w.pending--
	w.mu.Unlock()
	atomic.AddInt64(&totalStopTrue, 1)
}

func (w *timerWorker) round(r int64) {
	batch := timersPerRound
	longN := batch / 32
	mediumN := batch / 8

	// 独立 canary + aux：回调只通过自己的 rec 持有；不做跨 rec 链式保活。
	canaries := make([]*timerCanary, batch)
	for i := 0; i < batch; i++ {
		c := &timerCanary{id: int64(i), gen: r, magic: timerMagic(w.seed, r, int64(i))}
		c.next = &timerCanary{id: int64(batch + i), gen: r, magic: timerMagic(w.seed, r, int64(batch+i))}
		canaries[i] = c
	}

	w.mu.Lock()
	w.reg = make(map[int64]*timerRec, batch)
	w.pending = int64(batch)
	w.fired = 0
	w.stopped = 0
	w.stopTrue = 0
	w.longFired = 0
	w.mu.Unlock()

	stopList := make([]*timerRec, 0, longN+mediumN)
	for i := 0; i < batch; i++ {
		rec := &timerRec{worker: w, round: r, id: int64(i), gen: r, canary: canaries[i]}
		var d time.Duration
		switch {
		case i < longN:
			rec.kind = kindLong
			d = time.Hour
			atomic.AddInt64(&totalLongCreated, 1)
		case i < longN+mediumN:
			rec.kind = kindMedium
			d = 500 * time.Millisecond
		default:
			rec.kind = kindShort
			d = time.Duration(1+int((int64(i)*37+int64(w.seed)+r*13)%2000)) * time.Microsecond
		}
		// 先入 registry 再 arm：极短 timer 可能在 AfterFunc 返回前就触发回调。
		w.mu.Lock()
		w.reg[rec.id] = rec
		w.mu.Unlock()
		rec.timer = time.AfterFunc(d, func() { w.fire(rec) })
		if rec.kind == kindShort && i%32 == 0 {
			w.stopRec(rec) // 制造 Stop 与到期竞态
		} else if rec.kind != kindShort {
			stopList = append(stopList, rec)
		}
		if i%beatsPerCreate == 0 {
			atomic.AddInt64(&beats, 1)
		}
	}
	canaries = nil // 之后只允许 timer closure / registry 持有 canary

	// 快照：created == 到期 + 停止 + 未到期；long 尚未 Stop，必为 pending。
	w.mu.Lock()
	f, s, p, lf := w.fired, w.stopped, w.pending, w.longFired
	w.mu.Unlock()
	created := int64(batch)
	if f+s+p != created {
		w.failCallbacks(r, "snapshot", "fired=%d stopped=%d pending=%d created=%d (counts inconsistent)", f, s, p, created)
	}
	if p < int64(longN) {
		w.failCallbacks(r, "snapshot", "pending=%d want>=%d (long timers must be pending)", p, longN)
	}
	if lf != 0 {
		w.failCallbacks(r, "snapshot", "long_fired=%d before stop", lf)
	}
	atomic.AddInt64(&beats, 1)

	// 停止 medium + long；medium 若已到期则等待回调补记。
	for _, rec := range stopList {
		w.stopRec(rec)
	}

	// 等到所有 timer 都落到 fired 或 stopped。
	loop := 0
	for {
		w.mu.Lock()
		p = w.pending
		w.mu.Unlock()
		if p == 0 {
			break
		}
		loop++
		if loop%16 == 0 {
			atomic.AddInt64(&beats, 1)
		}
		runtime.Gosched()
		time.Sleep(50 * time.Microsecond)
	}

	w.mu.Lock()
	f, s, p = w.fired, w.stopped, w.pending
	regLen := len(w.reg)
	st, lf := w.stopTrue, w.longFired
	w.mu.Unlock()
	if p != 0 || f+s != created {
		w.failCallbacks(r, "round-final", "fired=%d stopped=%d pending=%d created=%d", f, s, p, created)
	}
	if regLen != 0 {
		w.failCallbacks(r, "round-final", "registry len=%d want=0 (callback self-delete leaked)", regLen)
	}
	if st != s {
		w.failCallbacks(r, "round-final", "stop_true=%d stopped=%d", st, s)
	}
	if lf != 0 {
		w.failCallbacks(r, "round-final", "long_fired=%d (1h timer fired/callback ran)", lf)
	}

	atomic.AddInt64(&totalFired, f)
	atomic.AddInt64(&totalStopped, s)
	atomic.AddInt64(&totalCreated, created)

	runtime.GC()
	atomic.AddInt64(&gcRounds, 1)
	w.rounds++
	w.next = r + 1
	atomic.AddInt64(&beats, 1)
}

func (w *timerWorker) failCallbacks(r int64, phase, format string, args ...interface{}) {
	all := make([]interface{}, 0, 5+len(args))
	all = append(all, scenario, phase, w.id, r, w.seed)
	all = append(all, args...)
	failf("scenario=%s phase=%s worker=%d round=%d seed=%d: "+format, all...)
}

func (w *timerWorker) run(seconds int, deadline time.Time, fixed int64) {
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
	workers := envInt("STRESS_WORKERS", 8, 1, 32)
	hb := envInt("STRESS_HEARTBEAT", 5, 1, 60)
	seed := envSeed(1)
	fixed := int64(envInt("STRESS_FIXED_ROUNDS", 16, 1, 1000000))
	gSeed = seed

	fmt.Printf("scenario=%s phase=preflight go=%s goos=%s goarch=%s gomaxprocs=%d seed=%d seconds=%d workers=%d fixed_rounds=%d heartbeat=%ds timers_per_round=%d long=%d medium=%d short=%d\n",
		scenario, runtime.Version(), runtime.GOOS, runtime.GOARCH, runtime.GOMAXPROCS(0), seed, seconds, workers, fixed, hb,
		timersPerRound, timersPerRound/32, timersPerRound/8, timersPerRound-timersPerRound/32-timersPerRound/8)

	baseGoroutines := runtime.NumGoroutine()
	deadline := time.Now().Add(time.Duration(seconds) * time.Second)

	ws := make([]*timerWorker, workers)
	for i := 0; i < workers; i++ {
		ws[i] = &timerWorker{id: int64(i), seed: seed*1_000_003 + uint64(i+1)*7919}
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
		go func(w *timerWorker) {
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
	aux.Wait()

	var roundsTotal, regTotal, pendingTotal int64
	for _, w := range ws {
		if seconds == 0 && w.next != fixed {
			failf("scenario=%s phase=final seed=%d worker=%d: rounds=%d want=%d", scenario, seed, w.id, w.next, fixed)
		}
		roundsTotal += w.rounds
		w.mu.Lock()
		regTotal += int64(len(w.reg))
		pendingTotal += w.pending
		w.mu.Unlock()
	}
	if s, f := atomic.LoadInt64(&started), atomic.LoadInt64(&finished); s != f || s != int64(workers) {
		failf("scenario=%s phase=final seed=%d: started=%d finished=%d workers=%d", scenario, seed, s, f, workers)
	}
	created := atomic.LoadInt64(&totalCreated)
	fired := atomic.LoadInt64(&totalFired)
	stopped := atomic.LoadInt64(&totalStopped)
	if created != fired+stopped {
		failf("scenario=%s phase=final seed=%d: created=%d fired=%d stopped=%d (unaccounted timers)", scenario, seed, created, fired, stopped)
	}
	if stopTrue := atomic.LoadInt64(&totalStopTrue); stopTrue != stopped {
		failf("scenario=%s phase=final seed=%d: stop_true=%d stopped=%d", scenario, seed, stopTrue, stopped)
	}
	if lf := atomic.LoadInt64(&totalLongFired); lf != 0 {
		failf("scenario=%s phase=final seed=%d: long_fired=%d (1h timers must never expire)", scenario, seed, lf)
	}
	if pendingTotal != 0 || regTotal != 0 {
		failf("scenario=%s phase=final seed=%d: pending=%d registry_entries=%d want=0", scenario, seed, pendingTotal, regTotal)
	}
	if want := atomic.LoadInt64(&totalLongCreated); want != created/32 || want != roundsTotal*int64(timersPerRound/32) {
		failf("scenario=%s phase=final seed=%d: long_created=%d inconsistent (rounds=%d workers=%d)", scenario, seed, want, roundsTotal, workers)
	}
	if roundsTotal == 0 || created == 0 {
		failf("scenario=%s phase=final seed=%d: no timers completed", scenario, seed)
	}

	ng := waitGoroutines(baseGoroutines, 5*time.Second)
	if ng > baseGoroutines+8 {
		failf("scenario=%s phase=final seed=%d: goroutine baseline=%d after=%d: goroutine leak", scenario, seed, baseGoroutines, ng)
	}

	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	fmt.Printf("metrics: rounds=%d timers_created=%d fired=%d stopped=%d stop_true=%d stop_false=%d pending=%d registry_entries=%d long_created=%d long_fired=%d gc_rounds=%d gc_churn=%d beats=%d num_gc=%d heap_alloc=%d heap_objects=%d goroutines_start=%d goroutines_final=%d\n",
		roundsTotal, created, fired, stopped, atomic.LoadInt64(&totalStopTrue), atomic.LoadInt64(&totalStopFalse),
		pendingTotal, regTotal, atomic.LoadInt64(&totalLongCreated), atomic.LoadInt64(&totalLongFired),
		atomic.LoadInt64(&gcRounds), atomic.LoadInt64(&gcChurnCycles), atomic.LoadInt64(&beats), m.NumGC,
		m.HeapAlloc, m.HeapObjects, baseGoroutines, ng)
	fmt.Printf("STRESS PASS %s\n", scenario)
}
