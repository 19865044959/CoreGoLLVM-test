// G05: 多返回值 × 深帧复用 × 逐边界 GC——两 issue 组合（P1，covers L17/L04）。
// 组合 issue2（goret 结果槽未入口清零，§4 四条件）与 issue1（深调用 → 浅返回
// 栈形，§1）：
//   - 多返回值（含指针 + 聚合混合）：func multiVictim() (p *payload, info BigInfo,
//     n int64)——p 最后写（其槽在全部前置 GC 窗口内都未初始化）、info 分字段写、
//     n 标量先写；
//   - GC 落在「结果声明 → 写入完成」窗口的**每个**边界：分字段逐步写入，
//     每步后 boundaryGC 一次（19 个边界穷举——包括 Pad/标量组边界）；
//   - 深帧复用：dirtyRec（刷满指向短命堆对象的指针 + depth 0 GC）与
//     victimMultiRec 同构下降（d 派生偏移、帧尺寸一致），使 multiVictim 的三个
//     结果槽全部落在旧 dirtyRec 帧的脏字上；
//   - 深度抖动 d = 3 + i%24 × 长跑。
//
// 官方 Go zeroResults 入口清零 → 恒 PASS；崩溃（found bad pointer in Go heap /
// pointer to free object）仅随 gollvm 出现/消失。
package main

import (
	"fmt"
	"os"
	"reflect"
	"runtime"
	"runtime/debug"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
	"weak"
)

const (
	scenario           = "g05_multiret_deep_frames"
	poisonIDBase       = int64(1) << 50
	fixedRoundsDefault = 60
	depthJitter        = 24 // 每轮 19 个 GC 边界，深度抖动取小值控制时长
	boundaries         = 19
)

type payload struct {
	magic uint64
	id    int64
	aux   int64
	pad   [5]uint64
}

func magicFor(seed uint64, id int64) uint64 {
	x := seed*0x9E3779B97F4A7C15 + uint64(id)*0x2545F4914F6CDD1D + 0x5EED5EED
	x ^= x >> 31
	x *= 0xBF58476D1CE4E5B9
	x ^= x >> 27
	return x
}

func newPayload(seed uint64, id int64) *payload {
	return &payload{magic: magicFor(seed, id), id: id, aux: id ^ 0x5A5A5A5A}
}

//go:noinline
func checkPayload(p *payload, seed uint64, want int64, where string) {
	if p == nil {
		failf("%s: nil payload (want id=%d): UAF/漏标", where, want)
	}
	if p.id != want || p.magic != magicFor(seed, p.id) || p.aux != p.id^0x5A5A5A5A {
		failf("%s: canary mismatch id=%d want=%d magic=%#x want_magic=%#x aux=%#x: UAF/复用",
			where, p.id, want, p.magic, magicFor(seed, p.id), p.aux)
	}
	runtime.KeepAlive(p)
}

var poisonSeq int64

//go:noinline
func poisonAlloc(seed uint64, n int) []*payload {
	seq := atomic.AddInt64(&poisonSeq, 1)
	out := make([]*payload, n)
	for i := range out {
		out[i] = newPayload(seed, poisonIDBase+seq*1_000_000+int64(i))
	}
	return out
}

// BigInfo：多指针 + 非指针字段混合，位图多指针位（word0 + word1），552B 超寄存器
// 返回阈值，强制经 goret 内存槽返回。
type BigInfo struct {
	A   *payload   // off 0   位图 word0 bit 0
	B   *payload   // off 8   bit 1
	N0  int64      // off 16  非指针
	C   *payload   // off 24  bit 3
	N1  int64      // off 32  非指针
	D   *payload   // off 40  bit 5
	N2  int64      // off 48  非指针
	N3  int64      // off 56  非指针
	E   *payload   // off 64  bit 8
	F   *payload   // off 72  bit 9
	N4  int64      // off 80  非指针
	N5  int64      // off 88  非指针
	G   *payload   // off 96  bit 12
	S   []*payload // off 104 bit 13,14
	H   *payload   // off 128 bit 16
	I   *payload   // off 136 bit 17
	N6  int64      // off 144 非指针
	N7  int64      // off 152 非指针
	Pad [48]uint64 // off 160..544 非指针 padding（把 J 推进 word1）
	J   *payload   // off 544 word1 bit 4
}

func makePad(base int64) [48]uint64 {
	var p [48]uint64
	for i := range p {
		p[i] = uint64(base) + uint64(i) + 1
	}
	return p
}

//go:noinline
func checkBigInfo(seed uint64, base int64, b BigInfo, where string) {
	checkPayload(b.A, seed, base+0, where+".A")
	checkPayload(b.B, seed, base+1, where+".B")
	if b.N0 != base+100 || b.N1 != base+101 {
		failf("%s: N0/N1=%d/%d want=%d/%d", where, b.N0, b.N1, base+100, base+101)
	}
	checkPayload(b.C, seed, base+2, where+".C")
	checkPayload(b.D, seed, base+3, where+".D")
	if b.N2 != base+102 || b.N3 != base+103 {
		failf("%s: N2/N3=%d/%d want=%d/%d", where, b.N2, b.N3, base+102, base+103)
	}
	checkPayload(b.E, seed, base+4, where+".E")
	checkPayload(b.F, seed, base+5, where+".F")
	if b.N4 != base+104 || b.N5 != base+105 {
		failf("%s: N4/N5=%d/%d want=%d/%d", where, b.N4, b.N5, base+104, base+105)
	}
	checkPayload(b.G, seed, base+6, where+".G")
	if len(b.S) != 2 {
		failf("%s: S len=%d want=2", where, len(b.S))
	}
	checkPayload(b.S[0], seed, base+7, where+".S[0]")
	checkPayload(b.S[1], seed, base+8, where+".S[1]")
	checkPayload(b.H, seed, base+9, where+".H")
	checkPayload(b.I, seed, base+10, where+".I")
	if b.N6 != base+106 || b.N7 != base+107 {
		failf("%s: N6/N7=%d/%d want=%d/%d", where, b.N6, b.N7, base+106, base+107)
	}
	if b.Pad[0] != uint64(base)+1 || b.Pad[47] != uint64(base)+48 {
		failf("%s: Pad corrupted: %v", where, b.Pad[:2])
	}
	checkPayload(b.J, seed, base+11, where+".J")
	runtime.KeepAlive(&b)
}

// ---------- 脏栈制造 + 同构下降（issue2 §2.1 机制） ----------

func fillDirtyBig(c *BigInfo, seed uint64, base int64) {
	c.A = newPayload(seed, base+0)
	c.B = newPayload(seed, base+1)
	c.C = newPayload(seed, base+2)
	c.D = newPayload(seed, base+3)
	c.E = newPayload(seed, base+4)
	c.F = newPayload(seed, base+5)
	c.G = newPayload(seed, base+6)
	c.S = []*payload{newPayload(seed, base+7), newPayload(seed, base+8)}
	c.H = newPayload(seed, base+9)
	c.I = newPayload(seed, base+10)
	c.J = newPayload(seed, base+11)
}

//go:noinline
func checksumBig(c *BigInfo) int64 {
	return c.A.id + c.B.id + c.C.id + c.D.id + c.E.id + c.F.id +
		c.G.id + c.H.id + c.I.id + c.J.id + c.S[0].id + c.S[1].id
}

// dirtyRec：各层帧刷满指向短命堆对象的指针（每层偏移由 d 派生），depth 0 处 GC
// 清扫成悬挂。
//
//go:noinline
func dirtyRec(seed uint64, base int64, d int, carrier BigInfo) int64 {
	c := carrier
	fillDirtyBig(&c, seed, base+10+int64(d)*1000)
	sum := checksumBig(&c)
	if d == 0 {
		runtime.GC()
		return sum
	}
	r := dirtyRec(seed, base, d-1, c)
	return sum + r
}

// victimMultiRec：与 dirtyRec 同型同深度下降（帧尺寸一致），depth 0 处调用
// multiVictim——其三个结果槽（p/info/n 的 goret alloca）落在本帧（= 旧 dirtyRec
// 帧）的脏字上。
//
//go:noinline
func victimMultiRec(seed uint64, base int64, d int, carrier BigInfo) (p *payload, info BigInfo, n int64) {
	c := carrier
	fillDirtyBig(&c, seed, base+10+int64(d)*1000)
	sum := checksumBig(&c)
	if d == 0 {
		p, info, n = multiVictim(seed, base)
		_ = sum
		return p, info, n
	}
	p, info, n = victimMultiRec(seed, base, d-1, c)
	_ = sum
	return p, info, n
}

// boundaryGC：结果写入窗口的边界 GC——GC 落在「结果声明 → 写入完成」窗口的
// 每个边界（穷举：分字段逐步写入，每步后一次）。
//
//go:noinline
func boundaryGC(seed uint64, base int64, step string) {
	runtime.GC()
	poison := poisonAlloc(seed, 16)
	runtime.KeepAlive(poison)
}

// multiVictim：多返回值（指针 + 聚合 + 标量混合）。p 最后写（其槽在全部前置
// 边界 GC 内都未初始化）、info 分字段逐步写、n 先写。每个边界 GC 都按位图扫描
// 未写入完成的结果槽——未入口清零时脏字被当活指针（issue2 缺陷触发点）。
//
//go:noinline
func multiVictim(seed uint64, base int64) (p *payload, info BigInfo, n int64) {
	n = base + 7
	boundaryGC(seed, base, "n")
	info.A = newPayload(seed, base+0)
	boundaryGC(seed, base, "A")
	info.B = newPayload(seed, base+1)
	boundaryGC(seed, base, "B")
	info.N0 = base + 100
	boundaryGC(seed, base, "N0")
	info.C = newPayload(seed, base+2)
	boundaryGC(seed, base, "C")
	info.N1 = base + 101
	boundaryGC(seed, base, "N1")
	info.D = newPayload(seed, base+3)
	boundaryGC(seed, base, "D")
	info.N2 = base + 102
	info.N3 = base + 103
	boundaryGC(seed, base, "N2N3")
	info.E = newPayload(seed, base+4)
	boundaryGC(seed, base, "E")
	info.F = newPayload(seed, base+5)
	boundaryGC(seed, base, "F")
	info.N4 = base + 104
	info.N5 = base + 105
	boundaryGC(seed, base, "N4N5")
	info.G = newPayload(seed, base+6)
	boundaryGC(seed, base, "G")
	info.S = []*payload{newPayload(seed, base+7), newPayload(seed, base+8)}
	boundaryGC(seed, base, "S")
	info.H = newPayload(seed, base+9)
	boundaryGC(seed, base, "H")
	info.I = newPayload(seed, base+10)
	boundaryGC(seed, base, "I")
	info.N6 = base + 106
	info.N7 = base + 107
	boundaryGC(seed, base, "N6N7")
	info.Pad = makePad(base)
	boundaryGC(seed, base, "Pad")
	info.J = newPayload(seed, base+11)
	boundaryGC(seed, base, "J")
	p = newPayload(seed, base+500)
	boundaryGC(seed, base, "p")
	return p, info, n
}

// multiVictimGate：一轮的完整复刻——先脏栈（dirtyRec + depth 0 GC），再同深度
// 下降（victimMultiRec），底部 multiVictim 的结果槽落在脏字上。
//
//go:noinline
func multiVictimGate(seed uint64, base int64, d int) (p *payload, info BigInfo, n int64) {
	dirtyRec(seed, base, d, BigInfo{})
	p, info, n = victimMultiRec(seed, base, d, BigInfo{})
	return p, info, n
}

// ---------- worker ----------

type worker struct {
	id    int64
	seed  uint64
	base0 int64

	next   int64
	rounds int64
	checks int64
	gcN    int64

	depthSum int64
	depthMin int
	depthMax int

	lastWeak   []weak.Pointer[payload]
	lastWeakID []int64
}

//go:noinline
func (w *worker) round(r int64) {
	base := w.base0 + r*50_000
	d := 3 + int(r%depthJitter)
	w.depthSum += int64(d)
	if w.depthMin == 0 || d < w.depthMin {
		w.depthMin = d
	}
	if d > w.depthMax {
		w.depthMax = d
	}

	p, info, n := multiVictimGate(w.seed, base, d)
	if n != base+7 {
		failf("phase=round worker=%d round=%d seed=%d: n=%d want=%d", w.id, r, w.seed, n, base+7)
	}
	checkPayload(p, w.seed, base+500, "g05.caller/post-return.p")
	checkBigInfo(w.seed, base, info, "g05.caller/post-return.info")

	// weak 强引用断言：p 仍由本地强引用，GC 后必须非 nil。
	wp := weak.Make(p)
	runtime.GC()
	if v := wp.Value(); v == nil {
		failf("phase=weak worker=%d round=%d seed=%d: strong ref held but object collected (多返回值漏标/UAF)",
			w.id, r, w.seed)
	} else {
		checkPayload(v, w.seed, base+500, "g05.weak-strong-ref")
	}
	atomic.AddInt64(&gWeakChecks, 1)
	w.gcN += int64(boundaries) + 1

	runtime.KeepAlive(p)
	runtime.KeepAlive(info)
	w.lastWeak = []weak.Pointer[payload]{wp}
	w.lastWeakID = []int64{base + 500}
	w.rounds++
	w.next = r + 1
	w.checks += 26
	atomic.AddInt64(&gBeats, 1)
}

// ---------- 通用 harness ----------

var (
	gBeats      int64
	gWeakChecks int64
	gStarted    int64
	gFinished   int64
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

func (w *worker) run(seconds int, deadline time.Time, fixed int64) {
	atomic.AddInt64(&gStarted, 1)
	defer atomic.AddInt64(&gFinished, 1)
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

func main() {
	setupCrashOutput()
	seconds := envInt("STRESS_SECONDS", 60, 0, 86400)
	workers := envInt("STRESS_WORKERS", 4, 1, 16)
	hb := envInt("STRESS_HEARTBEAT", 5, 1, 60)
	seed := envSeed(1)
	fixed := int64(envInt("STRESS_FIXED_ROUNDS", fixedRoundsDefault, 1, 1000000))
	gSeed = seed

	if sz := reflect.TypeOf(BigInfo{}).Size(); sz < 512 {
		failf("phase=preflight: BigInfo size=%d want >=512 (J 必须落在位图 word1)", sz)
	}

	fmt.Printf("scenario=%s phase=preflight go=%s goos=%s goarch=%s gomaxprocs=%d seed=%d seconds=%d workers=%d fixed_rounds=%d heartbeat=%ds depth_jitter=%d boundaries=%d biginfo_size=%d\n",
		scenario, runtime.Version(), runtime.GOOS, runtime.GOARCH, runtime.GOMAXPROCS(0),
		seed, seconds, workers, fixed, hb, depthJitter, boundaries, reflect.TypeOf(BigInfo{}).Size())

	runtime.GC()
	baseGoroutines := runtime.NumGoroutine()

	deadline := time.Now().Add(time.Duration(seconds) * time.Second)
	ws := make([]*worker, workers)
	for i := 0; i < workers; i++ {
		ws[i] = &worker{
			id:    int64(i),
			seed:  seed*1_000_003 + uint64(i+1)*7919,
			base0: int64(i) * 1_000_000_000_000,
		}
	}

	stop := make(chan struct{})
	var aux sync.WaitGroup
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

	// quiesce：全量 GC 后再验最后一次 weak（此时应已失活，仅记录）。
	runtime.GC()
	runtime.GC()
	aliveAfterDrop := 0
	for _, w := range ws {
		for _, wp := range w.lastWeak {
			if wp.Value() != nil {
				aliveAfterDrop++
			}
		}
	}

	if s, f := atomic.LoadInt64(&gStarted), atomic.LoadInt64(&gFinished); s != int64(workers) || f != s {
		failf("phase=final: started=%d finished=%d workers=%d", s, f, workers)
	}
	var roundsTotal, checksTotal, gcTotal, depthTotal int64
	for _, w := range ws {
		roundsTotal += w.rounds
		checksTotal += w.checks
		gcTotal += w.gcN
		depthTotal += w.depthSum
		if seconds == 0 && w.next != fixed {
			failf("phase=final worker=%d: rounds=%d want=%d", w.id, w.next, fixed)
		}
	}
	if roundsTotal == 0 {
		failf("phase=final: no rounds completed")
	}

	ng := waitGoroutines(baseGoroutines, 5*time.Second)
	if ng > baseGoroutines+4 {
		failf("phase=final: goroutines baseline=%d after=%d: goroutine leak", baseGoroutines, ng)
	}

	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	var dmin, dmax int
	for _, w := range ws {
		if dmin == 0 || w.depthMin < dmin {
			dmin = w.depthMin
		}
		if w.depthMax > dmax {
			dmax = w.depthMax
		}
	}
	fmt.Printf("metrics: rounds=%d canary_checks=%d worker_gc=%d boundary_gc=%d weak_checks=%d weak_alive_after_drop=%d depth_min=%d depth_max=%d depth_avg=%d num_gc=%d heap_alloc=%d heap_objects=%d goroutines_start=%d goroutines_final=%d\n",
		roundsTotal, checksTotal, gcTotal, roundsTotal*boundaries, atomic.LoadInt64(&gWeakChecks),
		aliveAfterDrop, dmin, dmax, depthTotal/max(roundsTotal, 1),
		m.NumGC, m.HeapAlloc, m.HeapObjects, baseGoroutines, ng)
	fmt.Printf("STRESS PASS %s\n", scenario)
}
