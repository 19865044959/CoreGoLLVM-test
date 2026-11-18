// S04: 抢占风暴长跑。
// 多 worker 在无调用紧循环中持有指针、轮转 slice，穿插 unsafe 指针往返与
// 周期性 noinline runtime.GC；独立 GC churn goroutine 持续施压；
// 可选 STRESS_GMX=1,2,4 在阶段边界由主 goroutine 切换 GOMAXPROCS。
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
	"unsafe"
)

const (
	ringN    = 16 // 轮转窗口长度（2 的幂，便于取模）
	ringLen  = 2 * ringN
	spinBase = 1 << 17 // 常规轮：无调用紧循环迭代数（16 的倍数）
	spinLong = 4 << 20 // 每 gcEvery 轮：更长的无调用紧循环，跨过强抢占阈值
	gcEvery  = 8       // 每 N 轮调用一次 noinline GC helper
)

type cell struct {
	pad int64
	v   int64
}

type worker struct {
	id       int64
	seed     uint64
	phase    int
	next     int64
	localSum int64
	accSum   int64 // 只由本 worker goroutine 原子累加，收尾时与 localSum 对照
}

var (
	beats    int64 // 每完成一轮 +1，watchdog 用
	gcCycles int64
	totalSum int64
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

// parseGMX 解析 STRESS_GMX（如 "1,2,4"）；空串/全非法返回 nil 表示不改变 GOMAXPROCS。
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

// gcPulse 是每 gcEvery 轮一次的 noinline 调用：先制造垃圾，再显式 GC；
// 调用点位于紧循环之外，用于在长无调用循环之后制造安全点与回收。
//
//go:noinline
func gcPulse(round int64) {
	junk := make([][]byte, 0, 8)
	for i := 0; i < 8; i++ {
		b := make([]byte, 2048)
		b[i] = byte(round + int64(i))
		junk = append(junk, b)
	}
	runtime.GC()
	runtime.KeepAlive(junk)
	atomic.AddInt64(&gcCycles, 1)
}

// expectedSpin 是紧循环校验和的闭式解：
// 第 k 次迭代读到 base+((2k+1) mod ringN)，累加 2 份再加 (k&3)。
// 每个 ringN 迭代块指针残差和 128，每个 4 迭代块 (k&3) 和 6。
func expectedSpin(base int64, spin int) int64 {
	blocks := int64(spin / ringN)
	return 2*(base*int64(spin)+blocks*128) + 3*int64(spin)/2
}

func (w *worker) verifyCells(r int64, cells []*cell, base int64, phase string) {
	for i, c := range cells {
		if c == nil || c.pad != base+int64(i) || c.v != base+int64(i%ringN) {
			failf("phase=%s worker=%d phase_idx=%d round=%d seed=%d gomaxprocs=%d: cell[%d] corrupted",
				phase, w.id, w.phase, r, w.seed, runtime.GOMAXPROCS(0), i)
		}
	}
}

func (w *worker) round(r int64) {
	base := int64(w.seed%10_000)*1_000_000 + r*4096
	cells := make([]*cell, ringLen)
	for i := range cells {
		if cells[i] != nil {
			failf("phase=alloc worker=%d phase_idx=%d round=%d seed=%d: fresh []*cell not zeroed at %d",
				w.id, w.phase, r, w.seed, i)
		}
	}
	for i := 0; i < ringLen; i++ {
		cells[i] = &cell{pad: base + int64(i), v: base + int64(i%ringN)}
	}

	spin := spinBase
	if r%gcEvery == 0 {
		spin = spinLong
	}

	// 无函数调用的紧循环：只做 slice 轮转、指针读取、unsafe 往返与算术；
	// 局部变量 p/win/cells 在整段循环内持有指针，循环内没有任何安全点。
	win := cells[:ringN]
	off := 0
	var sum int64
	bad, badAt := 0, -1
	for k := 0; k < spin; k++ {
		if off == ringN {
			win = cells[:ringN] // 轮转窗口回到环起点
			off = 0
		}
		win = win[1:]
		win = win[:ringN]
		off++
		j := k % ringN
		p := win[j]
		if p == nil {
			bad, badAt = 1, k
			break
		}
		want := base + int64((2*k+1)%ringN)
		if win[0] != cells[off] || win[ringN-1] != cells[off+ringN-1] || p.v != want {
			bad, badAt = 2, k
			break
		}
		// 单表达式内完成 Pointer→uintptr→Pointer 往返：不让 uintptr 跨语句存活。
		if (*cell)(unsafe.Pointer(uintptr(unsafe.Pointer(p)))) != p {
			bad, badAt = 3, k
			break
		}
		vp := (*int64)(unsafe.Pointer(uintptr(unsafe.Pointer(p)) + unsafe.Offsetof(cell{}.v)))
		if *vp != want {
			bad, badAt = 4, k
			break
		}
		sum += p.v + *vp + int64(k&3)
	}
	runtime.KeepAlive(cells)
	if bad != 0 {
		failf("phase=tightloop worker=%d phase_idx=%d round=%d seed=%d gomaxprocs=%d bad=%d k=%d",
			w.id, w.phase, r, w.seed, runtime.GOMAXPROCS(0), bad, badAt)
	}
	if got := expectedSpin(base, spin); sum != got {
		failf("phase=checksum worker=%d phase_idx=%d round=%d seed=%d gomaxprocs=%d spin=%d got=%d want=%d",
			w.id, w.phase, r, w.seed, runtime.GOMAXPROCS(0), spin, sum, got)
	}

	// 每 N 轮一次 noinline GC；GC 前后校验被持有的 cell 内容。
	if r%gcEvery == 0 {
		w.verifyCells(r, cells, base, "pre-gc")
		gcPulse(r)
		w.verifyCells(r, cells, base, "post-gc")
	}

	w.next++
	w.localSum += sum
	atomic.AddInt64(&w.accSum, sum)
	atomic.AddInt64(&totalSum, sum)
	atomic.AddInt64(&beats, 1)
}

func (w *worker) run(deadline time.Time, timed bool, stopRound int64) {
	for {
		if timed {
			if !time.Now().Before(deadline) {
				return
			}
		} else if w.next >= stopRound {
			return
		}
		w.round(w.next)
	}
}

// gcChurn 持续分配短命垃圾并显式 GC，与所有 worker 并行。
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
			b[i] = byte(i)
			sink = append(sink[:0], b)
		}
		for i := range sink {
			sink[i] = nil
		}
		sink = sink[:0]
		runtime.GC()
		atomic.AddInt64(&gcCycles, 1)
	}
}

func watchdog(interval time.Duration, stop <-chan struct{}) {
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
				failf("watchdog stalled: no worker progress for %s (beats=%d)", time.Since(lastChange), cur)
			}
		}
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
	workers := envInt("STRESS_WORKERS", 6, 1, 32)
	hb := envInt("STRESS_HEARTBEAT", 5, 1, 60)
	seed := envSeed(1)
	fixed := int64(envInt("STRESS_FIXED_ROUNDS", 64, 1, 1000000))
	gmx := parseGMX(os.Getenv("STRESS_GMX"))
	phases := len(gmx)
	if phases == 0 {
		phases = 1 // 未设置 STRESS_GMX：不调用 GOMAXPROCS，尊重 profile/环境
	}

	fmt.Printf("scenario=s04 phase=preflight go=%s goos=%s goarch=%s gomaxprocs=%d seed=%d seconds=%d workers=%d heartbeat=%ds gmx=%v phases=%d fixed_rounds=%d\n",
		runtime.Version(), runtime.GOOS, runtime.GOARCH, runtime.GOMAXPROCS(0), seed, seconds, workers, hb, gmx, phases, fixed)

	ws := make([]*worker, workers)
	for i := range ws {
		ws[i] = &worker{id: int64(i), seed: seed*1000 + uint64(i) + 1}
	}

	stop := make(chan struct{})
	var cwg sync.WaitGroup
	cwg.Add(1)
	go gcChurn(stop, &cwg)
	go watchdog(time.Duration(hb)*time.Second, stop)

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

		var wg sync.WaitGroup
		for _, w := range ws {
			w.phase = p
			wg.Add(1)
			go func(w *worker) {
				defer wg.Done()
				w.run(deadline, seconds > 0, pEnd)
			}(w)
		}
		wg.Wait()
	}

	close(stop)
	cwg.Wait()

	var m runtime.MemStats
	runtime.ReadMemStats(&m)

	var roundsTotal, locals int64
	for _, w := range ws {
		roundsTotal += w.next
		if seconds == 0 && w.next != fixed {
			failf("phase=final worker=%d seed=%d: rounds=%d want=%d", w.id, w.seed, w.next, fixed)
		}
		if got := atomic.LoadInt64(&w.accSum); got != w.localSum {
			failf("phase=final worker=%d seed=%d: atomic sum=%d local sum=%d", w.id, w.seed, got, w.localSum)
		}
		locals += w.localSum
	}
	if got := atomic.LoadInt64(&totalSum); got != locals {
		failf("phase=final: global atomic sum=%d sum of workers=%d", got, locals)
	}

	fmt.Printf("metrics: rounds=%d beats=%d gc_churn=%d total_sum=%d workers=%d gomaxprocs=%d heap_alloc=%d heap_objects=%d num_gc=%d goroutines=%d\n",
		roundsTotal, atomic.LoadInt64(&beats), atomic.LoadInt64(&gcCycles), atomic.LoadInt64(&totalSum),
		workers, runtime.GOMAXPROCS(0), m.HeapAlloc, m.HeapObjects, m.NumGC, runtime.NumGoroutine())
	fmt.Println("STRESS PASS s04_preempt_storm_soak")
}
