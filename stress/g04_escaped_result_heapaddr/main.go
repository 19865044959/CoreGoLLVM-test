// G04: 堆逃逸结果的 heapaddr 槽——issue2 变体（P1，covers L18/L17）。
// 对准 issue2 §4.7/§5.1：命名结果被闭包/接口持有而**堆逃逸**时，gollvm 的既有
// 修复 cd24507a（非逃逸 goret 槽入口清零，CreateFunctionStart 的 !heap_ 分支）
// 被整体跳过——与 CoreGo liveness/plive.go 对堆逃逸结果 heapaddr 槽的 Needzero
// 注记（"防 malloc 触发的栈扫撞垃圾"）相对应。形状：
//   - victim 的命名结果本身逃逸到堆（&res 经接口送走，被 resultHolder 持有）；
//   - victim 返回前调 runtime.GC()：扫调用方帧上的 heapaddr 槽——未初始化窗口
//     （陈旧堆地址被当活指针 = 缺陷触发点）；
//   - 脏堆预热：每轮先分配大量同 size class 短命 BigInfo 后 GC 再调 victim
//     （堆里布满刚释放的 span，陈旧槽值更可能命中 → unallocated span /
//     pointer to free object）；
//   - 结果写后经 holder 读回校验（跨 GC 存活）+ 返回值双路校验。
//
// 官方 Go：结果对象由分配器零化 + Needzero 入口清零 → 恒 PASS。
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
	scenario           = "g04_escaped_result_heapaddr"
	poisonIDBase       = int64(1) << 50
	fixedRoundsDefault = 100
	warmupN            = 32
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

func makeBigInfo(seed uint64, base int64) BigInfo {
	b := BigInfo{
		A:  newPayload(seed, base+0),
		B:  newPayload(seed, base+1),
		N0: base + 100,
		C:  newPayload(seed, base+2),
		N1: base + 101,
		D:  newPayload(seed, base+3),
		N2: base + 102,
		N3: base + 103,
		E:  newPayload(seed, base+4),
		F:  newPayload(seed, base+5),
		N4: base + 104,
		N5: base + 105,
		G:  newPayload(seed, base+6),
		S:  []*payload{newPayload(seed, base+7), newPayload(seed, base+8)},
		H:  newPayload(seed, base+9),
		I:  newPayload(seed, base+10),
		N6: base + 106,
		N7: base + 107,
		J:  newPayload(seed, base+11),
	}
	for i := range b.Pad {
		b.Pad[i] = uint64(base) + uint64(i) + 1
	}
	return b
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

// ---------- 逃逸结果 holder（issue2 §4.7：结果被接口持有 → 堆逃逸） ----------

type resultHolder interface {
	hold(p *BigInfo)
	get() *BigInfo
}

type holderImpl struct{ p *BigInfo }

func (h *holderImpl) hold(p *BigInfo) { h.p = p }
func (h *holderImpl) get() *BigInfo   { return h.p }

// warmupDirtyHeap：脏堆预热——分配大量同 size class 短命 BigInfo 后 GC，
// 堆里布满刚释放的 BigInfo span（陈旧 heapaddr 槽值命中它们 → unallocated
// span / pointer to free object 形态）。
//
//go:noinline
func warmupDirtyHeap(seed uint64, base int64, n int) {
	var keep []*BigInfo
	for i := 0; i < n; i++ {
		b := makeBigInfo(seed, base+int64(i)*100)
		keep = append(keep, &b)
	}
	_ = keep
	runtime.GC() // keep 失活 → 全部回收
}

// victimEscape：命名结果堆逃逸（&res 经接口送走被持有）→ heapaddr 形态。
// 返回前 runtime.GC() 扫调用方帧上的 heapaddr 槽——未初始化窗口（缺陷触发点）。
//
//go:noinline
func victimEscape(seed uint64, base int64, h resultHolder) (res BigInfo) {
	h.hold(&res) // &res 经接口送走 → 结果堆逃逸
	runtime.GC() // 返回前 GC：heapaddr 槽未初始化窗口被扫描
	poison := poisonAlloc(seed, 64)
	res = makeBigInfo(seed, base) // 晚写
	runtime.GC()                  // 写后 GC：已写入的合法值必须仍被正确扫描
	checkBigInfo(seed, base, res, "g04.victimEscape/post-gc")
	runtime.KeepAlive(poison)
	return res
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

	lastWeak   []weak.Pointer[payload]
	lastWeakID []int64
}

//go:noinline
func (w *worker) round(r int64) {
	base := w.base0 + r*50_000

	warmupDirtyHeap(w.seed, base+100_000, warmupN) // 脏堆预热后再调 victim

	h := &holderImpl{}
	res := victimEscape(w.seed, base, h)
	checkBigInfo(w.seed, base, res, "g04.caller/post-return.value")

	// GC 后经 holder 读回：逃逸结果仍被接口持有 → 必须存活且内容正确。
	runtime.GC()
	held := h.get()
	if held == nil {
		failf("phase=round worker=%d round=%d seed=%d: holder lost escaped result", w.id, r, w.seed)
	}
	checkBigInfo(w.seed, base, *held, "g04.caller/post-gc.held")

	// weak 强引用断言：结果仍由本地强引用，GC 后必须非 nil。
	wp := weak.Make(res.A)
	runtime.GC()
	if v := wp.Value(); v == nil {
		failf("phase=weak worker=%d round=%d seed=%d: strong ref held but object collected (heapaddr 漏标/UAF)",
			w.id, r, w.seed)
	} else {
		checkPayload(v, w.seed, base, "g04.weak-strong-ref")
	}
	atomic.AddInt64(&gWeakChecks, 1)
	w.gcN += 5

	runtime.KeepAlive(res)
	runtime.KeepAlive(h)
	w.lastWeak = []weak.Pointer[payload]{wp}
	w.lastWeakID = []int64{base}
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

	fmt.Printf("scenario=%s phase=preflight go=%s goos=%s goarch=%s gomaxprocs=%d seed=%d seconds=%d workers=%d fixed_rounds=%d heartbeat=%ds warmup_n=%d biginfo_size=%d\n",
		scenario, runtime.Version(), runtime.GOOS, runtime.GOARCH, runtime.GOMAXPROCS(0),
		seed, seconds, workers, fixed, hb, warmupN, reflect.TypeOf(BigInfo{}).Size())

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
	var roundsTotal, checksTotal, gcTotal int64
	for _, w := range ws {
		roundsTotal += w.rounds
		checksTotal += w.checks
		gcTotal += w.gcN
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
	fmt.Printf("metrics: rounds=%d canary_checks=%d worker_gc=%d warmup_objs=%d weak_checks=%d weak_alive_after_drop=%d num_gc=%d heap_alloc=%d heap_objects=%d goroutines_start=%d goroutines_final=%d\n",
		roundsTotal, checksTotal, gcTotal, roundsTotal*warmupN, atomic.LoadInt64(&gWeakChecks),
		aliveAfterDrop, m.NumGC, m.HeapAlloc, m.HeapObjects, baseGoroutines, ng)
	fmt.Printf("STRESS PASS %s\n", scenario)
}
