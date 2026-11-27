// Y09: 参数槽不初始化（P1，覆盖 L17/L11）。
// 对准 A8：参数槽（内联后成为调用方局部槽 / defer 参数求值槽 / 溢出到栈传参区）
// 未正确进栈图时，GC 要么扫到脏值（悬挂指针 → 崩溃）要么漏扫（对象被回收 →
// canary 失配）。
//   - 形态 A（内联参数槽）：可内联小函数 useParam(seed, p, want) 内联进调用方 →
//     参数成为调用方局部槽；调用前该栈位是脏值（dirtyRec 同深度同帧型下降铺垫，
//     同 y01/g01 手法，深度抖动）；取址导出（&p 存入局部 chan，不逃逸）→ GC →
//     经导出指针读回校验；槽复用变体（同槽换新对象后再 GC 再校验）；
//   - 形态 B（defer 参数求值时点）：defer useParamDefer(seed, p, want, ch)——参数在
//     defer 语句时求值、defer 执行时使用（窗口内 2×GC）；B2 变体：循环内 defer
//     （堆 _defer 记录参数区）；
//   - 形态 C（多参数混合）：12 指针 + int/string/slice/iface/float 大参数列表
//     （noinline callee），第 10+ 指针溢出到栈传参区（a9 末寄存器、a10 首栈槽），
//     callee 内取址导出 a10 + GC + 全参数精确校验。
//
// 官方 Go 的参数槽栈图/入口初始化正确 → 恒 PASS；崩溃/漏标仅随 gollvm 出现。
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
	"weak"
)

const (
	scenario = "y09_param_slot_dirty"

	carrierN = 16 // dirtyRec/victimRec 帧的指针槽数（帧型一致）
	depthJit = 48 // 深度抖动：d = 16 + round%depthJit

	fixedRoundsDefault = 100
)

// payload 是探针对象：magic 由 (seed,id) 决定，任何回收/复用都会失配。
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
		failf("%s: nil payload (want id=%d): 参数槽漏标/UAF", where, want)
	}
	if p.id != want || p.magic != magicFor(seed, p.id) || p.aux != p.id^0x5A5A5A5A {
		failf("%s: canary mismatch id=%d want=%d magic=%#x aux=%#x: UAF/复用",
			where, p.id, want, p.magic, p.aux)
	}
	atomic.AddInt64(&canaryChecks, 1)
}

// ---------- 脏栈制造 + 同构下降（同 y01/g01 手法，issue2 §2.1） ----------

//go:noinline
func fillDirty(c *[carrierN]*payload, seed uint64, base int64) {
	for i := range c {
		c[i] = newPayload(seed, base+int64(i))
	}
}

// checksumDirty 读取全部指针槽：使刷脏写成为活代码（防 DSE），且 c 的最后使用点
// 先于递归调用——depth 0 的 GC 把刷入的短命对象清扫成悬挂，此后各层帧字节 =
// 悬挂指针（脏栈）。
//
//go:noinline
func checksumDirty(c *[carrierN]*payload) int64 {
	var s int64
	for i := range c {
		s += c[i].id
	}
	return s
}

//go:noinline
func dirtyRec(seed uint64, base int64, d int, carrier [carrierN]*payload) int64 {
	c := carrier
	fillDirty(&c, seed, base+int64(d)*1000)
	sum := checksumDirty(&c)
	if d == 0 {
		runtime.GC()
		atomic.AddInt64(&gcCycles, 1)
		return sum
	}
	r := dirtyRec(seed, base, d-1, c)
	return sum + r
}

// ---------- 形态 A：内联参数槽 ----------

// useParam：刻意保持小（可内联）。内联后参数成为调用方局部槽——该槽在调用前是
// 脏值（victimInlineRec 底部帧落在旧 dirtyRec 帧的脏字节上）；不内联则覆盖
// callee 参数区。两种结果都命中 A8 的参数槽栈图路径。
func useParam(seed uint64, p *payload, want int64) {
	if p == nil {
		failf("y09.inline.useParam: nil payload want id=%d（参数槽漏标/UAF）", want)
	}
	if p.id != want || p.magic != magicFor(seed, p.id) || p.aux != p.id^0x5A5A5A5A {
		failf("y09.inline.useParam: canary mismatch id=%d want=%d magic=%#x aux=%#x（参数槽漏标/脏值扫描）",
			p.id, want, p.magic, p.aux)
	}
}

//go:noinline
func victimInlineBody(seed uint64, base int64) int64 {
	ch := make(chan **payload, 1) // 局部 chan：不逃逸 → &p 槽留在栈上
	p := newPayload(seed, base)
	useParam(seed, p, base) // 内联参数形态：参数槽进调用方帧
	pp := &p                // 取址导出：槽必须留在栈上且被栈图覆盖
	ch <- pp
	runtime.GC() // 窗口：栈图必须覆盖 p 槽
	atomic.AddInt64(&gcCycles, 1)
	got := <-ch
	checkPayload(*got, seed, base, "y09.inline.via-exported-slot")
	useParam(seed, p, base) // 正常路径仍活
	// 槽复用变体：同槽换新对象，再 GC 再校验（槽位图跨两代对象）。
	p = newPayload(seed, base+1)
	useParam(seed, p, base+1)
	ch <- &p
	runtime.GC()
	atomic.AddInt64(&gcCycles, 1)
	got2 := <-ch
	checkPayload(*got2, seed, base+1, "y09.inline.reused-slot")
	atomic.AddInt64(&inlineChecks, 1)
	runtime.KeepAlive(&p)
	runtime.KeepAlive(got)
	runtime.KeepAlive(got2)
	return p.id ^ int64(p.magic)
}

// victimInlineRec：与 dirtyRec 同型同深度下降（帧尺寸一致），底部 victimInlineBody
// 的槽位落在旧 dirtyRec 帧的脏字上。
//
//go:noinline
func victimInlineRec(seed uint64, base int64, d int, carrier [carrierN]*payload) int64 {
	c := carrier
	fillDirty(&c, seed, base+int64(d)*1000)
	sum := checksumDirty(&c)
	if d == 0 {
		return victimInlineBody(seed, base)
	}
	r := victimInlineRec(seed, base, d-1, c)
	return sum + r
}

//go:noinline
func victimInlineGate(seed uint64, base int64, d int) int64 {
	dirtyRec(seed, base, d, [carrierN]*payload{})
	return victimInlineRec(seed, base, d, [carrierN]*payload{})
}

// ---------- 形态 B：defer 参数求值时点 ----------

// useParamDefer：defer 执行时点校验。参数 p 在 defer 语句时求值——求值与执行之间
// 的窗口内 GC 必须覆盖 defer 参数槽（开放编码 defer 的栈槽 / 堆 _defer 记录）。
//
//go:noinline
func useParamDefer(seed uint64, p *payload, want int64, ch chan<- int64) {
	runtime.GC() // defer 执行时点再 GC：参数槽跨两个 GC 窗口
	atomic.AddInt64(&gcCycles, 1)
	checkPayload(p, seed, want, "y09.defer.exec")
	ch <- p.id ^ int64(p.magic)
}

//go:noinline
func victimDeferBody(seed uint64, base int64, ch chan<- int64) int64 {
	p := newPayload(seed, base)
	// 参数在 defer 语句时求值：求值时点与执行时点之间两个 GC。
	defer func() { // 注册在前，执行在后：只 GC + 分配（不引用 p，防闭包兜底保活）
		runtime.GC()
		atomic.AddInt64(&gcCycles, 1)
		junk := newPayload(seed, base+99)
		runtime.KeepAlive(junk)
	}()
	defer useParamDefer(seed, p, base, ch) // 参数此刻求值
	runtime.GC()                           // 窗口 1
	atomic.AddInt64(&gcCycles, 1)
	junk := newPayload(seed, base+1)
	sum := junk.id
	runtime.GC() // 窗口 2
	atomic.AddInt64(&gcCycles, 1)
	return sum
}

// victimDeferLoop：循环内 defer（每次迭代注册一个带参 defer）——循环 defer 走堆
// _defer 记录参数区（与开放编码 defer 的栈槽是两条实现路径）。
//
//go:noinline
func victimDeferLoop(seed uint64, base int64, ch chan<- int64) int64 {
	var sum int64
	for i := 0; i < 3; i++ {
		pi := newPayload(seed, base+int64(i)*10)
		defer useParamDefer(seed, pi, base+int64(i)*10, ch)
		runtime.GC()
		atomic.AddInt64(&gcCycles, 1)
		sum += pi.id
	}
	return sum
}

//go:noinline
func victimDeferRec(seed uint64, base int64, d int, carrier [carrierN]*payload, ch chan<- int64) int64 {
	c := carrier
	fillDirty(&c, seed, base+int64(d)*1000)
	sum := checksumDirty(&c)
	if d == 0 {
		return victimDeferBody(seed, base, ch)
	}
	r := victimDeferRec(seed, base, d-1, c, ch)
	return sum + r
}

//go:noinline
func victimDeferLoopRec(seed uint64, base int64, d int, carrier [carrierN]*payload, ch chan<- int64) int64 {
	c := carrier
	fillDirty(&c, seed, base+int64(d)*1000)
	sum := checksumDirty(&c)
	if d == 0 {
		return victimDeferLoop(seed, base, ch)
	}
	r := victimDeferLoopRec(seed, base, d-1, c, ch)
	return sum + r
}

// victimDeferGate：B1（开放编码 defer）与 B2（循环 defer）按轮次交替。
//
//go:noinline
func victimDeferGate(seed uint64, base int64, d int, variant int64) int64 {
	dirtyRec(seed, base, d, [carrierN]*payload{})
	ch := make(chan int64, 4)
	if variant%2 == 0 {
		victimDeferRec(seed, base, d, [carrierN]*payload{}, ch)
	} else {
		victimDeferLoopRec(seed, base, d, [carrierN]*payload{}, ch)
	}
	// 每个 deferred call 各回传一份校验和；B1 一份、B2 三份。defer 按 LIFO 执行，
	// 用 XOR 累积做顺序无关的整体校验（每个期望值各不相同，任何缺/错都失配）。
	n := int64(1)
	if variant%2 == 1 {
		n = 3
	}
	var acc, wantAcc int64
	for i := int64(0); i < n; i++ {
		acc ^= <-ch
		wantAcc ^= int64(magicFor(seed, base+i*10)) ^ (base + i*10)
	}
	if acc != wantAcc {
		failf("y09.defer: 校验和=%#x want=%#x（defer 参数槽漏标）", acc, wantAcc)
	}
	atomic.AddInt64(&deferChecks, 1)
	return acc
}

// ---------- 形态 C：多参数混合（8+ 指针溢出到栈传参区） ----------

func manyStr(base int64) string {
	return "m-" + strconv.FormatInt(base, 10)
}

func manyBytes(base int64) []byte {
	b := make([]byte, 8)
	for i := range b {
		b[i] = byte(base + int64(i))
	}
	return b
}

// manyArgsUse：12 指针参数 + int/string/slice/iface/float 混合。ABIInternal 下
// a1..a9 占满 9 个整型寄存器，a10 起溢出到栈传参区（args 区边界：a9 末寄存器、
// a10 首栈槽）。callee 内取址导出 a10 + GC + 全参数精确校验。
//
//go:noinline
func manyArgsUse(seed uint64, base int64,
	a1, a2, a3, a4, a5, a6, a7, a8, a9, a10, a11, a12 *payload,
	x1, x2 int64, s string, b []byte, e any, f1, f2 float64) int64 {
	ch := make(chan **payload, 1)
	pp := &a10 // 栈传参区参数取址导出
	ch <- pp
	runtime.GC() // 窗口：栈传参区必须被栈图覆盖
	atomic.AddInt64(&gcCycles, 1)
	got := <-ch
	checkPayload(*got, seed, base+9, "y09.many.a10-via-slot")
	runtime.GC()
	atomic.AddInt64(&gcCycles, 1)
	for i, p := range []*payload{a1, a2, a3, a4, a5, a6, a7, a8, a9, a10, a11, a12} {
		checkPayload(p, seed, base+int64(i), "y09.many.arg")
	}
	if x1 != base+100 || x2 != base+101 {
		failf("y09.many: x1/x2=%d/%d want=%d/%d", x1, x2, base+100, base+101)
	}
	if s != manyStr(base) {
		failf("y09.many: s=%q want=%q", s, manyStr(base))
	}
	if len(b) != 8 || b[0] != byte(base) || b[7] != byte(base+7) {
		failf("y09.many: b len=%d（args 区 slice 被改写）", len(b))
	}
	en, ok := e.(*payload)
	if !ok {
		failf("y09.many: e 类型=%T want *payload", e)
	}
	checkPayload(en, seed, base+12, "y09.many.e")
	if f1 != 1.5 || f2 != -2.5 {
		failf("y09.many: f1/f2=%v/%v want=1.5/-2.5", f1, f2)
	}
	runtime.GC()
	atomic.AddInt64(&gcCycles, 1)
	atomic.AddInt64(&manyChecks, 1)
	runtime.KeepAlive(&a10)
	runtime.KeepAlive(got)
	return a1.id + a12.id + x1 + x2
}

// manyGate：脏栈铺垫下降（同 dirtyRec 帧型），底部把 carrier 的值作为实参传入
// manyArgsUse——调用方 outgoing args 区与 callee 栈传参区都落在脏字上。
//
//go:noinline
func manyGate(seed uint64, base int64, d int, carrier [carrierN]*payload) int64 {
	c := carrier
	fillDirty(&c, seed, base+int64(d)*1000)
	sum := checksumDirty(&c)
	if d == 0 {
		return manyArgsUse(seed, base,
			c[0], c[1], c[2], c[3], c[4], c[5], c[6], c[7], c[8], c[9], c[10], c[11],
			base+100, base+101, manyStr(base), manyBytes(base), c[12], 1.5, -2.5)
	}
	r := manyGate(seed, base, d-1, c)
	return sum + r
}

// ---------- worker ----------

type worker struct {
	id     int64
	seed   uint64
	base0  int64
	rounds int64

	depthSum int64
	depthMin int
	depthMax int
}

//go:noinline
func (w *worker) round(r int64) {
	base := w.base0 + r*100_000
	d := 16 + int(r%depthJit)
	w.depthSum += int64(d)
	if w.depthMin == 0 || d < w.depthMin {
		w.depthMin = d
	}
	if d > w.depthMax {
		w.depthMax = d
	}

	s1 := victimInlineGate(w.seed, base, d)
	s2 := victimDeferGate(w.seed, base+10_000, d, r)
	s3 := manyGate(w.seed, base+20_000, d, [carrierN]*payload{})
	_ = s1 + s2 + s3

	// weak 强引用探针：对象仍被本地强引用时 GC 后必须非 nil（漏标/UAF 探针）。
	anchor := newPayload(w.seed, base+30_000)
	wk := weak.Make(anchor)
	runtime.GC()
	atomic.AddInt64(&gcCycles, 1)
	if v := wk.Value(); v == nil {
		failf("y09.weak worker=%d round=%d: strong ref held but object collected（栈图漏标/UAF）", w.id, r)
	} else {
		checkPayload(v, w.seed, base+30_000, "y09.weak-strong-ref")
	}
	atomic.AddInt64(&weakChecks, 1)
	runtime.KeepAlive(anchor)
	runtime.KeepAlive(wk)

	w.rounds++
	atomic.AddInt64(&beats, 1)
}

func (w *worker) run(seconds int, deadline time.Time, fixed int64) {
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

// ---------- 通用 harness ----------

var (
	beats        int64
	gcCycles     int64
	started      int64
	finished     int64
	gSeed        uint64
	canaryChecks int64
	inlineChecks int64
	deferChecks  int64
	manyChecks   int64
	weakChecks   int64
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

func gcChurn(stop <-chan struct{}, wg *sync.WaitGroup) {
	defer wg.Done()
	for {
		select {
		case <-stop:
			return
		default:
		}
		runtime.GC()
		atomic.AddInt64(&gcCycles, 1)
		// window（GOMAXPROCS=1+STW）下无休眠 GC 循环会饿死 worker/watchdog。
		if runtime.GOMAXPROCS(0) == 1 {
			runtime.Gosched()
			time.Sleep(time.Millisecond) // 节奏控制，非断言
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
				failf("phase=watchdog: 6 个心跳周期无进展 (%s, beats=%d)", time.Since(lastChange), cur)
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
	workers := envInt("STRESS_WORKERS", 4, 1, 16)
	hb := envInt("STRESS_HEARTBEAT", 5, 1, 60)
	seed := envSeed(1)
	fixed := int64(envInt("STRESS_FIXED_ROUNDS", fixedRoundsDefault, 1, 1000000))
	gSeed = seed

	fmt.Printf("scenario=%s phase=preflight go=%s goos=%s goarch=%s gomaxprocs=%d seed=%d seconds=%d workers=%d fixed_rounds=%d heartbeat=%ds depth_jitter=%d\n",
		scenario, runtime.Version(), runtime.GOOS, runtime.GOARCH, runtime.GOMAXPROCS(0),
		seed, seconds, workers, fixed, hb, depthJit)

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
	go gcChurn(stop, &aux)
	aux.Add(1)
	go watchdog(time.Duration(hb)*time.Second, stop, &aux)

	var wg sync.WaitGroup
	for _, w := range ws {
		wg.Add(1)
		go func(w *worker) {
			atomic.AddInt64(&started, 1)
			defer func() {
				atomic.AddInt64(&finished, 1)
				wg.Done()
			}()
			w.run(seconds, deadline, fixed)
		}(w)
	}
	wg.Wait()
	close(stop)
	aux.Wait()

	if s, f := atomic.LoadInt64(&started), atomic.LoadInt64(&finished); s != int64(workers) || f != s {
		failf("phase=final: started=%d finished=%d workers=%d", s, f, workers)
	}
	var rounds int64
	for _, w := range ws {
		rounds += w.rounds
		if seconds == 0 && w.rounds != fixed {
			failf("phase=final worker=%d: rounds=%d want=%d", w.id, w.rounds, fixed)
		}
	}
	if rounds == 0 {
		failf("phase=final: 没有完成任何轮次")
	}
	if want := rounds; atomic.LoadInt64(&inlineChecks) != want ||
		atomic.LoadInt64(&deferChecks) != want || atomic.LoadInt64(&manyChecks) != want {
		failf("phase=final: 形态计数 inline=%d defer=%d many=%d want 各=%d",
			atomic.LoadInt64(&inlineChecks), atomic.LoadInt64(&deferChecks),
			atomic.LoadInt64(&manyChecks), want)
	}
	if want := rounds; atomic.LoadInt64(&weakChecks) != want {
		failf("phase=final: weak 探针=%d want=%d", atomic.LoadInt64(&weakChecks), want)
	}
	if atomic.LoadInt64(&canaryChecks) == 0 {
		failf("phase=final: 没有执行 canary 校验")
	}

	ng := waitGoroutines(baseGoroutines, 5*time.Second)
	if ng > baseGoroutines+4 {
		failf("phase=final: goroutines baseline=%d after=%d: goroutine 泄漏", baseGoroutines, ng)
	}

	var dmin, dmax int
	var depthTotal int64
	for _, w := range ws {
		depthTotal += w.depthSum
		if dmin == 0 || w.depthMin < dmin {
			dmin = w.depthMin
		}
		if w.depthMax > dmax {
			dmax = w.depthMax
		}
	}
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	fmt.Printf("metrics: rounds=%d canary_checks=%d inline=%d defer=%d many=%d weak=%d depth_min=%d depth_max=%d depth_avg=%d gc_churn=%d num_gc=%d heap_alloc=%d heap_objects=%d goroutines=%d\n",
		rounds, atomic.LoadInt64(&canaryChecks), atomic.LoadInt64(&inlineChecks),
		atomic.LoadInt64(&deferChecks), atomic.LoadInt64(&manyChecks),
		atomic.LoadInt64(&weakChecks), dmin, dmax, depthTotal/max(rounds, 1),
		atomic.LoadInt64(&gcCycles), m.NumGC, m.HeapAlloc, m.HeapObjects, ng)
	fmt.Printf("STRESS PASS %s\n", scenario)
}
