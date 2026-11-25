// U05: sync.Pool 身份复用长跑（P0）。
//
// 池中存放含大量指针字段的大对象（40KB data + 16 直接指针 + 8 slice 指针 +
// 8 项 map + interface 装箱 + 24 个标量）。每个 worker 每轮：
//
//	a) Get -> 复用对象先校验上一代 canary（跨 worker 也可由 owner/gen 重算） ->
//	   全字段重新填充 -> 校验 -> Put -> 再 Get，覆盖对象身份复用、重新填充完整、
//	   map/slice/interface 无旧指针残留；
//	b) 阶段 1 在 Put 前后插入显式 GC，覆盖“取出后持有跨 GC”与“Put 后池被
//	   victim/清空再 Get”两条路径；
//	c) 外部引用后 Put：对象归还池后外部仍持有，GC 后外部读校验内容完好；
//	d) 两轮 GC 清空 worker 私有池的 victim 两级缓存，Get 必须走 New 路径。
//
// 独立 gcChurn 持续分配 + GC；任何指针字段/内容漂移立即 FAIL。
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
	scenarioName = "u05_pool_identity_reuse"
	phases       = 2

	pointeeN  = 16        // 直接指针字段数
	subN      = 8         // slice 内指针数
	mapN      = 8         // map 项数
	dataBytes = 40 * 1024 // 大对象 data 区（超过大对象阈值）

	extEvery    = 5 // 每 N 轮做一次外部引用后 Put 子测试
	victimEvery = 8 // 每 N 轮做一次 victim 两级缓存子测试

	fnvOff   = uint32(2166136261)
	fnvPrime = uint32(16777619)
)

type pointee struct {
	id    int64
	magic int64
	pad   int64
}

type poolObj struct {
	owner int64
	gen   int64
	data  []byte
	ptrs  [pointeeN]*pointee
	sub   []*pointee
	m     map[string]*pointee
	boxed interface{}
	raw   [16]int64
}

var (
	beats         int64
	started       int64
	finished      int64
	gcCycles      int64
	workerGC      int64
	fillSeq       int64
	gets          int64
	puts          int64
	newAllocs     int64
	reuseHits     int64
	objChecks     int64
	extChecks     int64
	victimNewPath int64
	totalSum      int64
	gSeed         uint64
	mapKeys       [mapN]string
)

var globalPool = sync.Pool{New: func() interface{} {
	atomic.AddInt64(&newAllocs, 1)
	return newPoolObj()
}}

func init() {
	for i := range mapKeys {
		mapKeys[i] = fmt.Sprintf("k%d", i)
	}
}

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

func newPoolObj() *poolObj {
	return &poolObj{
		data: make([]byte, dataBytes),
		m:    make(map[string]*pointee, mapN),
	}
}

func dataPat(seed uint64, owner, gen int64, i int) byte {
	x := seed ^ uint64(owner)*0x9E3779B97F4A7C15 ^ uint64(gen)*0xD6E8FEB86659FD93 ^ uint64(i)*0x165667B19E3779F9
	x ^= x >> 30
	x *= 0xBF58476D1CE4E5B9
	x ^= x >> 27
	return byte(x)
}

func pointeeMagic(seed uint64, owner, gen int64, idx int) int64 {
	x := seed*0x9E3779B97F4A7C15 + uint64(owner)*0x2545F4914F6CDD1D + uint64(gen)*0x9E3779B1 + uint64(idx)*0x85EBCA77C2B2AE63
	x ^= x >> 33
	x *= 0xff51afd7ed558ccd
	x ^= x >> 33
	if x == 0 {
		x = 1
	}
	return int64(x)
}

func sumBytes(b []byte) uint32 {
	h := fnvOff
	for _, c := range b {
		h ^= uint32(c)
		h *= fnvPrime
	}
	return h
}

func wantDataSum(seed uint64, owner, gen int64, n int) uint32 {
	h := fnvOff
	for i := 0; i < n; i++ {
		h ^= uint32(dataPat(seed, owner, gen, i))
		h *= fnvPrime
	}
	return h
}

func fillObj(o *poolObj, seed uint64, owner, gen int64) {
	o.owner = owner
	o.gen = gen
	for i := range o.data {
		o.data[i] = dataPat(seed, owner, gen, i)
	}
	for i := 0; i < pointeeN; i++ {
		o.ptrs[i] = &pointee{id: int64(i), magic: pointeeMagic(seed, owner, gen, i), pad: gen*7 + int64(i)}
	}
	if cap(o.sub) != subN {
		o.sub = make([]*pointee, subN)
	} else {
		o.sub = o.sub[:subN]
	}
	for i := 0; i < subN; i++ {
		o.sub[i] = &pointee{id: int64(100 + i), magic: pointeeMagic(seed, owner, gen, 100+i), pad: gen*11 + int64(i)}
	}
	if o.m == nil {
		o.m = make(map[string]*pointee, mapN)
	} else {
		clear(o.m)
	}
	for i := 0; i < mapN; i++ {
		o.m[mapKeys[i]] = &pointee{id: int64(200 + i), magic: pointeeMagic(seed, owner, gen, 200+i), pad: gen*13 + int64(i)}
	}
	if gen&1 == 0 {
		o.boxed = o.ptrs[0]
	} else {
		o.boxed = o.sub
	}
	for i := range o.raw {
		o.raw[i] = gen*131 + owner*17 + int64(i)
	}
}

type worker struct {
	id   int64
	seed uint64
	next int64

	localSum int64

	extPool    sync.Pool
	victimPool sync.Pool
	victimNews int64

	seen    [16]uintptr
	seenN   int
	seenPos int
}

func newWorker(id int64, seed uint64) *worker {
	w := &worker{id: id, seed: seed}
	w.extPool.New = func() interface{} {
		atomic.AddInt64(&newAllocs, 1)
		return newPoolObj()
	}
	w.victimPool.New = func() interface{} {
		w.victimNews++
		atomic.AddInt64(&newAllocs, 1)
		return newPoolObj()
	}
	return w
}

func (w *worker) failCtx(phase string, r int64, where, format string, args ...interface{}) {
	prefix := fmt.Sprintf("phase=%s worker=%d round=%d seed=%d gomaxprocs=%d where=%s: ",
		phase, w.id, r, w.seed, runtime.GOMAXPROCS(0), where)
	failf(prefix+format, args...)
}

func (w *worker) noteSeen(p uintptr) bool {
	hit := false
	for _, q := range w.seen[:w.seenN] {
		if q == p {
			hit = true
			break
		}
	}
	if w.seenN < len(w.seen) {
		w.seen[w.seenN] = p
		w.seenN++
	} else {
		w.seen[w.seenPos] = p
		w.seenPos = (w.seenPos + 1) % len(w.seen)
	}
	return hit
}

// verifyFresh 校验 New 路径的新对象：全零/全空，保证没有旧内容泄漏进来。
func (w *worker) verifyFresh(phase string, r int64, where string, o *poolObj) {
	if o.gen != 0 || o.owner != 0 {
		w.failCtx(phase, r, where, "fresh object has gen=%d owner=%d want 0/0", o.gen, o.owner)
	}
	for i := 0; i < pointeeN; i++ {
		if o.ptrs[i] != nil {
			w.failCtx(phase, r, where, "fresh ptrs[%d] != nil", i)
		}
	}
	if len(o.sub) != 0 {
		w.failCtx(phase, r, where, "fresh sub len=%d want=0", len(o.sub))
	}
	if len(o.m) != 0 {
		w.failCtx(phase, r, where, "fresh map len=%d want=0", len(o.m))
	}
	if o.boxed != nil {
		w.failCtx(phase, r, where, "fresh boxed != nil (%T)", o.boxed)
	}
	for i := range o.raw {
		if o.raw[i] != 0 {
			w.failCtx(phase, r, where, "fresh raw[%d]=%d want=0", i, o.raw[i])
		}
	}
	if len(o.data) != dataBytes {
		w.failCtx(phase, r, where, "fresh data len=%d want=%d", len(o.data), dataBytes)
	}
	for i := 0; i < dataBytes; i += dataBytes / 32 {
		if o.data[i] != 0 {
			w.failCtx(phase, r, where, "fresh data[%d]=%#x want=0", i, o.data[i])
		}
	}
	atomic.AddInt64(&objChecks, 1)
}

// verifyObj 按对象自带的 owner/gen 重算全部 canary：跨 worker 复用同样成立。
func (w *worker) verifyObj(phase string, r int64, where string, o *poolObj) {
	if o == nil {
		w.failCtx(phase, r, where, "nil pool object")
	}
	if o.gen == 0 {
		w.verifyFresh(phase, r, where, o)
		return
	}
	owner, gen := o.owner, o.gen
	for i := 0; i < pointeeN; i++ {
		p := o.ptrs[i]
		if p == nil {
			w.failCtx(phase, r, where, "ptrs[%d] nil (旧指针残留/被清空)", i)
		}
		if p.id != int64(i) || p.magic != pointeeMagic(gSeed, owner, gen, i) || p.pad != gen*7+int64(i) {
			w.failCtx(phase, r, where, "ptrs[%d] corrupted id=%d magic=%#x pad=%d (UAF/旧指针残留)", i, p.id, p.magic, p.pad)
		}
	}
	if len(o.sub) != subN {
		w.failCtx(phase, r, where, "sub len=%d want=%d", len(o.sub), subN)
	}
	for i := 0; i < subN; i++ {
		p := o.sub[i]
		if p == nil {
			w.failCtx(phase, r, where, "sub[%d] nil", i)
		}
		if p.id != int64(100+i) || p.magic != pointeeMagic(gSeed, owner, gen, 100+i) || p.pad != gen*11+int64(i) {
			w.failCtx(phase, r, where, "sub[%d] corrupted id=%d magic=%#x pad=%d", i, p.id, p.magic, p.pad)
		}
	}
	if len(o.m) != mapN {
		w.failCtx(phase, r, where, "map len=%d want=%d (旧 key 残留)", len(o.m), mapN)
	}
	for i := 0; i < mapN; i++ {
		p, ok := o.m[mapKeys[i]]
		if !ok || p == nil {
			w.failCtx(phase, r, where, "map[%s] missing", mapKeys[i])
		}
		if p.id != int64(200+i) || p.magic != pointeeMagic(gSeed, owner, gen, 200+i) || p.pad != gen*13+int64(i) {
			w.failCtx(phase, r, where, "map[%s] corrupted id=%d magic=%#x pad=%d", mapKeys[i], p.id, p.magic, p.pad)
		}
	}
	switch v := o.boxed.(type) {
	case *pointee:
		if gen&1 != 0 {
			w.failCtx(phase, r, where, "boxed type *pointee for odd gen=%d (旧 interface 残留)", gen)
		}
		if v != o.ptrs[0] {
			w.failCtx(phase, r, where, "boxed identity != ptrs[0] (旧指针残留)")
		}
	case []*pointee:
		if gen&1 == 0 {
			w.failCtx(phase, r, where, "boxed type []*pointee for even gen=%d (旧 interface 残留)", gen)
		}
		if len(v) != subN {
			w.failCtx(phase, r, where, "boxed slice len=%d want=%d", len(v), subN)
		}
		for i := range v {
			if v[i] != o.sub[i] {
				w.failCtx(phase, r, where, "boxed slice[%d] identity != sub[%d] (旧指针残留)", i, i)
			}
		}
	default:
		w.failCtx(phase, r, where, "boxed type=%T (被清空/旧值残留)", o.boxed)
	}
	for i := range o.raw {
		if o.raw[i] != gen*131+owner*17+int64(i) {
			w.failCtx(phase, r, where, "raw[%d]=%d want=%d", i, o.raw[i], gen*131+owner*17+int64(i))
		}
	}
	if len(o.data) != dataBytes {
		w.failCtx(phase, r, where, "data len=%d want=%d", len(o.data), dataBytes)
	}
	for i := 0; i < dataBytes; i += dataBytes / 32 {
		if got, want := o.data[i], dataPat(gSeed, owner, gen, i); got != want {
			w.failCtx(phase, r, where, "data[%d]=%#x want=%#x (UAF/内容残留)", i, got, want)
		}
	}
	if got, want := sumBytes(o.data), wantDataSum(gSeed, owner, gen, dataBytes); got != want {
		w.failCtx(phase, r, where, "data checksum=%#x want=%#x (UAF/内容残留)", got, want)
	}
	atomic.AddInt64(&objChecks, 1)
}

// externalRef：对象 Put 回池后外部引用仍持有，GC 后外部读必须完好。
func (w *worker) externalRef(phase string, r int64) {
	o := w.extPool.Get().(*poolObj)
	w.verifyObj(phase, r, "ext-pre", o)
	gen := atomic.AddInt64(&fillSeq, 1)
	fillObj(o, gSeed, w.id, gen)
	w.verifyObj(phase, r, "ext-fill", o)

	w.extPool.Put(o)
	runtime.GC()
	atomic.AddInt64(&workerGC, 1)

	w.verifyObj(phase, r, "ext-after-put-gc", o) // 外部仍持有，内容必须完好

	o2 := w.extPool.Get().(*poolObj)
	w.verifyObj(phase, r, "ext-regrab", o2)
	gen2 := atomic.AddInt64(&fillSeq, 1)
	fillObj(o2, gSeed, w.id, gen2)
	w.verifyObj(phase, r, "ext-refill", o2)
	w.extPool.Put(o2)
	atomic.AddInt64(&extChecks, 1)
}

// victimTest：两轮 GC 清空私有池的两级 victim 缓存，Get 必须走 New 路径。
func (w *worker) victimTest(phase string, r int64) {
	a := w.victimPool.Get().(*poolObj)
	b := w.victimPool.Get().(*poolObj)
	fillObj(a, gSeed, w.id, atomic.AddInt64(&fillSeq, 1))
	fillObj(b, gSeed, w.id, atomic.AddInt64(&fillSeq, 1))
	w.verifyObj(phase, r, "victim-pre-put", a)
	w.verifyObj(phase, r, "victim-pre-put", b)
	w.victimPool.Put(a)
	w.victimPool.Put(b)

	runtime.GC()
	runtime.GC()
	atomic.AddInt64(&workerGC, 2)

	before := w.victimNews
	x := w.victimPool.Get().(*poolObj)
	if w.victimNews != before+1 {
		w.failCtx(phase, r, "victim-new-path", "New calls delta=%d want=1 (victim 缓存两轮 GC 后仍存活)", w.victimNews-before)
	}
	if x.gen != 0 {
		w.failCtx(phase, r, "victim-new-path", "reused object survived two GCs gen=%d (victim 未按预期老化)", x.gen)
	}
	w.verifyFresh(phase, r, "victim-fresh", x)
	fillObj(x, gSeed, w.id, atomic.AddInt64(&fillSeq, 1))
	w.verifyObj(phase, r, "victim-refill", x)
	w.victimPool.Put(x)
	atomic.AddInt64(&victimNewPath, 1)
}

func (w *worker) cycle(phaseName string, r int64, phase int) {
	o := globalPool.Get().(*poolObj)
	atomic.AddInt64(&gets, 1)
	w.verifyObj(phaseName, r, "get-pre", o) // 复用对象先校验上一代 canary

	gen := atomic.AddInt64(&fillSeq, 1)
	fillObj(o, gSeed, w.id, gen)
	w.verifyObj(phaseName, r, "get-fill", o)
	if w.noteSeen(uintptr(unsafe.Pointer(o))) {
		atomic.AddInt64(&reuseHits, 1)
	}

	if phase > 0 {
		runtime.GC()
		atomic.AddInt64(&workerGC, 1)
		w.verifyObj(phaseName, r, "held-across-gc", o)
	}

	globalPool.Put(o)
	atomic.AddInt64(&puts, 1)

	if phase > 0 {
		// Put 之后 GC：对象可能进入 victim 或被清掉，再 Get 走复用/New 两条路径。
		runtime.GC()
		atomic.AddInt64(&workerGC, 1)
	}

	o2 := globalPool.Get().(*poolObj)
	atomic.AddInt64(&gets, 1)
	w.verifyObj(phaseName, r, "re-get", o2)
	gen2 := atomic.AddInt64(&fillSeq, 1)
	fillObj(o2, gSeed, w.id, gen2)
	w.verifyObj(phaseName, r, "re-fill", o2)
	if w.noteSeen(uintptr(unsafe.Pointer(o2))) {
		atomic.AddInt64(&reuseHits, 1)
	}

	if phase > 0 {
		runtime.GC()
		atomic.AddInt64(&workerGC, 1)
		w.verifyObj(phaseName, r, "re-fill-gc", o2)
	}
	globalPool.Put(o2)
	atomic.AddInt64(&puts, 1)

	if r%extEvery == 0 {
		w.externalRef(phaseName, r)
	}
	if r%victimEvery == 0 {
		w.victimTest(phaseName, r)
	}

	w.localSum += gen + gen2
	atomic.AddInt64(&totalSum, gen+gen2)
	atomic.AddInt64(&beats, 1)
}

func (w *worker) run(phaseName string, phase int, deadline time.Time, timed bool, stopRound int64) {
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
		w.cycle(phaseName, r, phase)
		w.next = r + 1
	}
}

// gcChurn 持续分配 + GC，与池的 Get/Put 并发，扩大对象回收/复用窗口。
func gcChurn(stop <-chan struct{}, wg *sync.WaitGroup) {
	defer wg.Done()
	var sink [][]byte
	for {
		select {
		case <-stop:
			return
		default:
		}
		for i := 0; i < 12; i++ {
			b := make([]byte, 4096)
			b[i%len(b)] = byte(i)
			sink = append(sink, b)
		}
		for i := range sink {
			sink[i] = nil
		}
		sink = sink[:0]
		runtime.GC()
		atomic.AddInt64(&gcCycles, 1)
		time.Sleep(time.Millisecond)
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
				failf("phase=watchdog seed=%d: no worker progress for %s (beats=%d)", gSeed, time.Since(lastChange), cur)
			}
		}
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
	fixed := int64(envInt("STRESS_FIXED_ROUNDS", 16, 1, 1000000))
	gSeed = seed

	fmt.Printf("scenario=%s phase=preflight go=%s goos=%s goarch=%s gomaxprocs=%d seed=%d seconds=%d workers=%d heartbeat=%ds phases=%d fixed_rounds=%d obj_bytes=%d ptrs=%d sub=%d map=%d\n",
		scenarioName, runtime.Version(), runtime.GOOS, runtime.GOARCH, runtime.GOMAXPROCS(0), seed, seconds, workers, hb, phases, fixed,
		dataBytes, pointeeN, subN, mapN)

	runtime.GC()
	startBase := runtime.NumGoroutine()

	stop := make(chan struct{})
	var aux sync.WaitGroup
	aux.Add(1)
	go gcChurn(stop, &aux)
	aux.Add(1)
	go watchdog(time.Duration(hb)*time.Second, stop, &aux)

	ws := make([]*worker, workers)
	for i := 0; i < workers; i++ {
		ws[i] = newWorker(int64(i), seed*1_000_003+uint64(i+1)*7919)
	}

	start := time.Now()
	var total time.Duration
	if seconds > 0 {
		total = time.Duration(seconds) * time.Second
	}

	for p := 0; p < phases; p++ {
		pStart := fixed * int64(p) / int64(phases)
		pEnd := fixed * int64(p+1) / int64(phases)
		var deadline time.Time
		phaseName := fmt.Sprintf("p%d", p)
		if seconds > 0 {
			if p == phases-1 {
				deadline = start.Add(total)
			} else {
				deadline = start.Add(total * time.Duration(p+1) / time.Duration(phases))
			}
			fmt.Printf("phase=%s gc_between_put_get=%v timed=true\n", phaseName, p > 0)
		} else {
			fmt.Printf("phase=%s gc_between_put_get=%v rounds=[%d,%d)\n", phaseName, p > 0, pStart, pEnd)
		}

		var wg sync.WaitGroup
		for _, w := range ws {
			wg.Add(1)
			go func(w *worker, phase int, name string) {
				defer wg.Done()
				w.run(name, phase, deadline, seconds > 0, pEnd)
			}(w, p, phaseName)
		}
		wg.Wait()
	}

	close(stop)
	aux.Wait()

	runtime.GC()
	runtime.GC()

	// 收尾从全局池取一个对象再校验一次（含 New 路径）。
	for _, w := range ws {
		o := globalPool.Get().(*poolObj)
		w.verifyObj("final", -1, "final-get", o)
		globalPool.Put(o)
	}

	if s, f := atomic.LoadInt64(&started), atomic.LoadInt64(&finished); s != f || s != int64(workers*phases) {
		failf("phase=final seed=%d: started=%d finished=%d want=%d", seed, s, f, workers*phases)
	}

	var roundsTotal, locals int64
	for _, w := range ws {
		roundsTotal += w.next
		if seconds == 0 && w.next != fixed {
			failf("phase=final worker=%d seed=%d: rounds=%d want=%d", w.id, w.seed, w.next, fixed)
		}
		locals += w.localSum
	}
	if got := atomic.LoadInt64(&totalSum); got != locals {
		failf("phase=final seed=%d: total sum=%d sum of workers=%d", seed, got, locals)
	}
	if roundsTotal == 0 {
		failf("phase=final seed=%d: no rounds completed", seed)
	}
	if atomic.LoadInt64(&gets) == 0 || atomic.LoadInt64(&puts) == 0 || atomic.LoadInt64(&objChecks) == 0 {
		failf("phase=final seed=%d: zero coverage gets=%d puts=%d checks=%d", seed,
			atomic.LoadInt64(&gets), atomic.LoadInt64(&puts), atomic.LoadInt64(&objChecks))
	}
	if atomic.LoadInt64(&extChecks) == 0 {
		failf("phase=final seed=%d: external-ref subtest never ran", seed)
	}
	if atomic.LoadInt64(&victimNewPath) == 0 {
		failf("phase=final seed=%d: victim subtest never ran", seed)
	}

	ng := waitGoroutines(startBase, 5*time.Second)
	if ng > startBase+8 {
		failf("phase=final seed=%d: goroutines baseline=%d after=%d: goroutine leak", seed, startBase, ng)
	}

	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	fmt.Printf("metrics: rounds=%d gets=%d puts=%d new_allocs=%d reuse_hits=%d obj_checks=%d ext_checks=%d victim_new_path=%d gc_churn=%d worker_gc=%d num_gc=%d total_sum=%d goroutines_start=%d goroutines_final=%d heap_alloc=%d heap_objects=%d gomaxprocs=%d\n",
		roundsTotal, atomic.LoadInt64(&gets), atomic.LoadInt64(&puts), atomic.LoadInt64(&newAllocs),
		atomic.LoadInt64(&reuseHits), atomic.LoadInt64(&objChecks), atomic.LoadInt64(&extChecks),
		atomic.LoadInt64(&victimNewPath), atomic.LoadInt64(&gcCycles), atomic.LoadInt64(&workerGC), m.NumGC,
		atomic.LoadInt64(&totalSum), startBase, ng, m.HeapAlloc, m.HeapObjects, runtime.GOMAXPROCS(0))
	fmt.Printf("STRESS PASS %s\n", scenarioName)
}
