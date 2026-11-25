// U04: unsafe 零拷贝缓冲长跑（P0）。
//
// 每个 worker 每轮在四种持有形态下反复跨 GC 校验确定性 canary：
//
//	a) []byte -> string 零拷贝视图（单表达式 unsafe.Pointer 转换）；丢弃 []byte 后
//	   仅由 string 视图保活，GC 后逐字节校验；
//	b) slice header 拆装：提取/重装 data/len/cap，派生子视图、reslice 到 cap，
//	   丢弃 base slice 后仅由 header 值保活；
//	c) 内部指针 &buf[i] 作为唯一引用跨 GC（unsafe.Slice 重建尾部视图）；
//	d) 同一缓冲的 string / 子 slice / header 多视图别名一致性（含 GC 后）。
//
// 独立 gcChurn 持续分配 + GC；两个阶段，第二阶段放大缓冲。worker 另持有一组
// 长生命周期视图，每轮与收尾都校验，任何内容漂移（UAF/漏扫/header 拆装错误）
// 立即 FAIL 并带出上下文。
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
	scenarioName = "u04_unsafe_zerocopy_buffer"
	phases       = 2

	fnvOff   = uint32(2166136261)
	fnvPrime = uint32(16777619)

	liveBytes = 20 * 1024 // 长生命周期缓冲基准大小
)

// sliceHeader 用于 header 拆装：与 slice 前三个字一一对应。
type sliceHeader struct {
	data unsafe.Pointer
	len  int
	cap  int
}

var (
	beats          int64
	started        int64
	finished       int64
	gcCycles       int64
	workerGC       int64
	aliasViews     int64
	headerOps      int64
	interiorProbes int64
	viewChecks     int64
	canaryChecks   int64
	totalSum       int64

	gSeed uint64
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

// pat 是确定性 canary 字节：同一 (seed, worker, round, offset) 恒得同一字节。
func pat(seed uint64, wid, round int64, i int) byte {
	x := seed ^ uint64(wid)*0x9E3779B97F4A7C15 ^ uint64(round)*0xC2B2AE3D27D4EB4F ^ uint64(i)*0x165667B19E3779F9
	x ^= x >> 29
	x *= 0xBF58476D1CE4E5B9
	x ^= x >> 32
	return byte(x)
}

func fill(b []byte, seed uint64, wid, round int64, off int) {
	for i := range b {
		b[i] = pat(seed, wid, round, off+i)
	}
}

func sumBytes(b []byte) uint32 {
	h := fnvOff
	for _, c := range b {
		h ^= uint32(c)
		h *= fnvPrime
	}
	return h
}

func sumString(s string) uint32 {
	h := fnvOff
	for i := 0; i < len(s); i++ {
		h ^= uint32(s[i])
		h *= fnvPrime
	}
	return h
}

func wantSum(seed uint64, wid, round int64, off, n int) uint32 {
	h := fnvOff
	for i := 0; i < n; i++ {
		h ^= uint32(pat(seed, wid, round, off+i))
		h *= fnvPrime
	}
	return h
}

// bytesToString 是单表达式 unsafe.Pointer 转换的零拷贝 []byte->string 视图。
//
//go:noinline
func bytesToString(b []byte) string {
	return *(*string)(unsafe.Pointer(&b))
}

//go:noinline
func headerOf(b []byte) sliceHeader {
	return *(*sliceHeader)(unsafe.Pointer(&b))
}

//go:noinline
func sliceFromHeader(h sliceHeader) []byte {
	return *(*[]byte)(unsafe.Pointer(&h))
}

// aliasProbe 只把 string 视图交给调用方：返回后 base []byte 不可达，
// 跨 GC 的存活性完全由 string 的 data 指针承担。
//
//go:noinline
func aliasProbe(seed uint64, wid, round int64, n int) (string, uint32) {
	b := make([]byte, n)
	fill(b, seed, wid, round, 0)
	return bytesToString(b), sumBytes(b)
}

// headerProbe 只返回 header 值；base slice 在返回后不可达。
//
//go:noinline
func headerProbe(seed uint64, wid, round int64, n int) (sliceHeader, uint32) {
	b := make([]byte, n)
	fill(b, seed, wid, round, 0)
	h := headerOf(b)
	return h, sumBytes(b)
}

// interiorProbe 只返回内部指针与元数据；base slice 在返回后不可达，
// 对象的存活性只由 &b[off] 这个内部指针承担。
//
//go:noinline
func interiorProbe(seed uint64, wid, round int64, n int) (p *byte, off, tail int, want uint32) {
	b := make([]byte, n)
	fill(b, seed, wid, round, 0)
	off = n/3 + 1
	return &b[off], off, n - off, sumBytes(b[off:])
}

type worker struct {
	id   int64
	seed uint64
	next int64

	localSum int64

	// 长生命周期视图：setup 后仅这些视图引用原缓冲。
	liveStr  string
	liveLen  int
	livePtr  *byte
	liveOff  int
	liveTail int
	liveSum  uint32
}

func newWorker(id int64, seed uint64) *worker {
	return &worker{id: id, seed: seed}
}

func (w *worker) failCtx(phase string, r int64, where, format string, args ...interface{}) {
	prefix := fmt.Sprintf("phase=%s worker=%d round=%d seed=%d gomaxprocs=%d where=%s: ",
		phase, w.id, r, w.seed, runtime.GOMAXPROCS(0), where)
	failf(prefix+format, args...)
}

func (w *worker) setup() {
	n := liveBytes + int(w.id%4)*2048
	b := make([]byte, n)
	fill(b, w.seed, w.id, -7, 0)
	w.liveLen = n
	w.liveSum = sumBytes(b)
	w.liveStr = bytesToString(b)
	w.liveOff = n/3 + 1
	w.liveTail = n - w.liveOff
	w.livePtr = &b[w.liveOff]
	b = nil
	runtime.KeepAlive(w.liveStr)
	runtime.KeepAlive(w.livePtr)
	atomic.AddInt64(&beats, 1)
}

// checkView 校验 b 的全部内容与 pat 序列一致（校验和 + 首/中/尾抽样）。
func (w *worker) checkView(phase string, r int64, where string, b []byte, off, n int, round int64) {
	if len(b) != n {
		w.failCtx(phase, r, where, "view len=%d want=%d", len(b), n)
	}
	if n > 0 {
		if got, want := b[0], pat(w.seed, w.id, round, off); got != want {
			w.failCtx(phase, r, where, "view first byte=%#x want=%#x (UAF/视图损坏)", got, want)
		}
		if got, want := b[n/2], pat(w.seed, w.id, round, off+n/2); got != want {
			w.failCtx(phase, r, where, "view mid byte=%#x want=%#x (UAF/视图损坏)", got, want)
		}
		if got, want := b[n-1], pat(w.seed, w.id, round, off+n-1); got != want {
			w.failCtx(phase, r, where, "view last byte=%#x want=%#x (UAF/视图损坏)", got, want)
		}
	}
	if got, want := sumBytes(b), wantSum(w.seed, w.id, round, off, n); got != want {
		w.failCtx(phase, r, where, "view checksum=%#x want=%#x (UAF/视图损坏)", got, want)
	}
	atomic.AddInt64(&canaryChecks, 1)
}

func (w *worker) verifyLive(where string) {
	if w.liveLen <= 0 || w.liveStr == "" || w.livePtr == nil || w.liveTail <= 0 {
		failf("phase=%s worker=%d seed=%d where=%s: live view not initialized", where, w.id, w.seed, where)
	}
	if len(w.liveStr) != w.liveLen {
		w.failCtx(where, -1, "live-string", "len=%d want=%d", len(w.liveStr), w.liveLen)
	}
	if unsafe.StringData(w.liveStr) == nil {
		w.failCtx(where, -1, "live-string", "nil data pointer")
	}
	if got, want := sumString(w.liveStr), w.liveSum; got != want {
		w.failCtx(where, -1, "live-string", "checksum=%#x want=%#x (视图未保活/UAF)", got, want)
	}
	tail := unsafe.Slice(w.livePtr, w.liveTail)
	if got, want := sumBytes(tail), wantSum(w.seed, w.id, -7, w.liveOff, w.liveTail); got != want {
		w.failCtx(where, -1, "live-internal-pointer", "checksum=%#x want=%#x (内部指针未保活/UAF)", got, want)
	}
	if got, want := tail[0], pat(w.seed, w.id, -7, w.liveOff); got != want {
		w.failCtx(where, -1, "live-internal-pointer", "first byte=%#x want=%#x", got, want)
	}
	atomic.AddInt64(&viewChecks, 1)
	atomic.AddInt64(&canaryChecks, 2)
	runtime.KeepAlive(w.livePtr)
	runtime.KeepAlive(w.liveStr)
}

func (w *worker) round(phaseName string, r int64, phase int) {
	scale := 1
	if phase > 0 {
		scale = 3
	}
	n := (6*1024 + int((r*977+int64(w.id)*131)%4096)) * scale
	if n < 1024 {
		n = 1024
	}

	// a) []byte -> string 零拷贝视图：base 丢弃后仅 string 保活，跨 GC 校验。
	sv, want := aliasProbe(w.seed, w.id, r, n)
	if len(sv) != n {
		w.failCtx(phaseName, r, "alias", "zero-copy string len=%d want=%d", len(sv), n)
	}
	if got := sumString(sv); got != want {
		w.failCtx(phaseName, r, "alias", "checksum=%#x want=%#x (零拷贝转换损坏)", got, want)
	}
	if unsafe.StringData(sv) == nil {
		w.failCtx(phaseName, r, "alias", "nil data pointer")
	}
	atomic.AddInt64(&aliasViews, 1)

	runtime.GC()
	atomic.AddInt64(&workerGC, 1)

	if got := sumString(sv); got != want {
		w.failCtx(phaseName, r, "alias-post-gc", "checksum=%#x want=%#x (string 视图未保活/UAF)", got, want)
	}
	full := unsafe.Slice(unsafe.StringData(sv), len(sv))
	if got := sumBytes(full); got != want {
		w.failCtx(phaseName, r, "alias-post-gc", "StringData checksum=%#x want=%#x", got, want)
	}
	atomic.AddInt64(&viewChecks, 1)
	atomic.AddInt64(&canaryChecks, 1)
	sv = ""
	runtime.KeepAlive(sv)

	// b) slice header 拆装：仅 header 值跨 GC。
	h, wantH := headerProbe(w.seed, w.id, r, n)
	if h.data == nil || h.len != n || h.cap != n {
		w.failCtx(phaseName, r, "header", "header=(nil=%v,len=%d,cap=%d) want len=cap=%d", h.data == nil, h.len, h.cap, n)
	}
	runtime.GC()
	atomic.AddInt64(&workerGC, 1)

	base := sliceFromHeader(h)
	if len(base) != n || cap(base) != n {
		w.failCtx(phaseName, r, "header-reassemble", "len=%d cap=%d want %d/%d", len(base), cap(base), n, n)
	}
	if got := sumBytes(base); got != wantH {
		w.failCtx(phaseName, r, "header-reassemble", "checksum=%#x want=%#x (header 未保活/UAF)", got, wantH)
	}
	atomic.AddInt64(&canaryChecks, 1)

	so := n/4 + 1
	sn := n / 2
	sub := sliceFromHeader(sliceHeader{data: unsafe.Add(h.data, so), len: sn, cap: h.cap - so})
	if cap(sub) != n-so {
		w.failCtx(phaseName, r, "header-sub", "cap=%d want=%d", cap(sub), n-so)
	}
	w.checkView(phaseName, r, "header-sub", sub, so, sn, r)

	half := sub[:sn/2]
	if cap(half) != n-so {
		w.failCtx(phaseName, r, "header-reslice", "cap=%d want=%d", cap(half), n-so)
	}
	back := half[:cap(half)]
	w.checkView(phaseName, r, "header-reslice-full", back, so, n-so, r)
	atomic.AddInt64(&headerOps, 4)

	base = nil
	sub = nil
	half = nil
	back = nil

	// c) 内部指针作为唯一引用跨 GC。
	p, off, tailN, wantTail := interiorProbe(w.seed, w.id, r, n)
	runtime.GC()
	atomic.AddInt64(&workerGC, 1)

	if got := sumBytes(unsafe.Slice(p, tailN)); got != wantTail {
		w.failCtx(phaseName, r, "interior", "checksum=%#x want=%#x (内部指针未保活/UAF)", got, wantTail)
	}
	tail := unsafe.Slice(p, tailN)
	if got, want := tail[0], pat(w.seed, w.id, r, off); got != want {
		w.failCtx(phaseName, r, "interior", "first byte=%#x want=%#x", got, want)
	}
	if got, want := tail[tailN-1], pat(w.seed, w.id, r, off+tailN-1); got != want {
		w.failCtx(phaseName, r, "interior", "last byte=%#x want=%#x", got, want)
	}
	atomic.AddInt64(&interiorProbes, 1)
	atomic.AddInt64(&canaryChecks, 1)
	runtime.KeepAlive(p)

	// d) 同一缓冲上的多视图别名一致性（GC 前 + 丢弃 base 再 GC 后）。
	b := make([]byte, n)
	fill(b, w.seed, w.id, r, 0)
	baseSum := sumBytes(b)
	s1 := bytesToString(b)
	h2 := headerOf(b)
	lo := n / 5
	ln := n / 2
	w2 := b[lo : lo+ln]
	sum2 := sumBytes(w2)
	if unsafe.Pointer(unsafe.StringData(s1)) != unsafe.Pointer(&b[0]) {
		w.failCtx(phaseName, r, "alias-identity", "string data != &buf[0]")
	}
	if h2.data != unsafe.Pointer(&b[0]) || h2.len != n || h2.cap != n {
		w.failCtx(phaseName, r, "alias-identity", "header mismatch (data=%p len=%d cap=%d)", h2.data, h2.len, h2.cap)
	}
	b = nil
	runtime.GC()
	atomic.AddInt64(&workerGC, 1)

	if got := sumString(s1); got != baseSum {
		w.failCtx(phaseName, r, "alias-multi", "string checksum=%#x want=%#x", got, baseSum)
	}
	if got := sumBytes(w2); got != sum2 {
		w.failCtx(phaseName, r, "alias-multi", "sub-slice checksum=%#x want=%#x", got, sum2)
	}
	hb := sliceFromHeader(h2)
	if len(hb) != n || cap(hb) != n {
		w.failCtx(phaseName, r, "alias-multi", "reassembled header len=%d cap=%d want %d", len(hb), cap(hb), n)
	}
	if got := sumBytes(hb); got != baseSum {
		w.failCtx(phaseName, r, "alias-multi", "header checksum=%#x want=%#x", got, baseSum)
	}
	atomic.AddInt64(&viewChecks, 1)
	atomic.AddInt64(&canaryChecks, 3)
	runtime.KeepAlive(s1)
	runtime.KeepAlive(w2)
	runtime.KeepAlive(h2)

	// 长生命周期视图。
	w.verifyLive(phaseName + "-live")

	w.localSum += int64(baseSum)
	atomic.AddInt64(&totalSum, int64(baseSum))
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
		w.round(phaseName, r, phase)
		w.next = r + 1
	}
}

// gcChurn 持续分配/释放并显式 GC，与所有 worker 的视图校验并发，扩大 UAF 窗口。
func gcChurn(stop <-chan struct{}, wg *sync.WaitGroup) {
	defer wg.Done()
	var sink [][]byte
	for {
		select {
		case <-stop:
			return
		default:
		}
		for i := 0; i < 16; i++ {
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
	fixed := int64(envInt("STRESS_FIXED_ROUNDS", 24, 1, 1000000))
	gSeed = seed

	fmt.Printf("scenario=%s phase=preflight go=%s goos=%s goarch=%s gomaxprocs=%d seed=%d seconds=%d workers=%d heartbeat=%ds phases=%d fixed_rounds=%d live_bytes=%d\n",
		scenarioName, runtime.Version(), runtime.GOOS, runtime.GOARCH, runtime.GOMAXPROCS(0), seed, seconds, workers, hb, phases, fixed, liveBytes)

	ws := make([]*worker, workers)
	for i := 0; i < workers; i++ {
		ws[i] = newWorker(int64(i), seed*1_000_003+uint64(i+1)*7919)
		ws[i].setup()
	}

	runtime.GC()
	startBase := runtime.NumGoroutine()

	stop := make(chan struct{})
	var aux sync.WaitGroup
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
			fmt.Printf("phase=%s scale=%d timed=true\n", phaseName, 1+2*p)
		} else {
			fmt.Printf("phase=%s scale=%d rounds=[%d,%d)\n", phaseName, 1+2*p, pStart, pEnd)
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
		for _, w := range ws {
			w.verifyLive("phase-boundary")
		}
	}

	close(stop)
	aux.Wait()

	runtime.GC()
	runtime.GC()
	for _, w := range ws {
		w.verifyLive("final")
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
	if atomic.LoadInt64(&aliasViews) == 0 || atomic.LoadInt64(&interiorProbes) == 0 || atomic.LoadInt64(&headerOps) == 0 {
		failf("phase=final seed=%d: zero coverage alias=%d interior=%d header=%d", seed,
			atomic.LoadInt64(&aliasViews), atomic.LoadInt64(&interiorProbes), atomic.LoadInt64(&headerOps))
	}

	ng := waitGoroutines(startBase, 5*time.Second)
	if ng > startBase+8 {
		failf("phase=final seed=%d: goroutines baseline=%d after=%d: goroutine leak", seed, startBase, ng)
	}

	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	fmt.Printf("metrics: rounds=%d alias_views=%d header_ops=%d interior_probes=%d view_checks=%d canary_checks=%d gc_churn=%d worker_gc=%d num_gc=%d total_sum=%d goroutines_start=%d goroutines_final=%d heap_alloc=%d heap_objects=%d gomaxprocs=%d\n",
		roundsTotal, atomic.LoadInt64(&aliasViews), atomic.LoadInt64(&headerOps), atomic.LoadInt64(&interiorProbes),
		atomic.LoadInt64(&viewChecks), atomic.LoadInt64(&canaryChecks), atomic.LoadInt64(&gcCycles),
		atomic.LoadInt64(&workerGC), m.NumGC, atomic.LoadInt64(&totalSum), startBase, ng, m.HeapAlloc, m.HeapObjects, runtime.GOMAXPROCS(0))
	fmt.Printf("STRESS PASS %s\n", scenarioName)
}
