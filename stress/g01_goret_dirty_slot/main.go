// G01: goret 脏槽——issue2 核心复刻（P0，covers L17/L18）。
// 对准 issue/issue2.md §2.1/§4：gollvm 对「命名结果经 goret 内存槽返回 + 结果变量
// 堆逃逸」的形状缺少入口清零，GC 栈图在结果写入完成前把陈旧栈垃圾描述为活指针。
// 复刻 LLT 回归用例 issue_goret_slot_dangling.go 的全部要素：
//   - victim 与 overlay Stat/Update 同构：命名结果 BigInfo 超寄存器返回阈值
//     （多指针 + 非指针字段混合，位图多个指针位），经 goret 内存槽返回；
//   - 结果由经接口调用、堆逃逸的闭包晚写入（闭包签名带 *BigInfo，经 interface
//     分发，写前闭包内 runtime.GC() 扫栈——未初始化窗口）；
//   - dirtyRec 以深度 d=3+i%96 递归把各层栈帧刷满指向短命堆对象的指针后 GC
//     （清扫成悬挂）；victimRec 同深度下降（携带相同 byval BigInfo 保证帧尺寸一致），
//     使未初始化的 goret alloca 落在脏字上；
//   - 30000 轮级长跑（STRESS_FIXED_ROUNDS 可调），失败判定：进程被 runtime 崩掉
//     （fatal error: found bad pointer in Go heap / pointer to free object /
//     marked free object）即 FAIL；window profile（gcstoptheworld=2）为放大器。
//
// 官方 Go（CoreGo）zeroResults 在函数入口即清零结果槽，本程序是合法程序、恒 PASS；
// 崩溃仅随 gollvm 出现/消失（issue2 §6.3 对照结论）。
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
	scenario           = "g01_goret_dirty_slot"
	poisonIDBase       = int64(1) << 50
	fixedRoundsDefault = 100 // 自检规模；issue 强度：STRESS_FIXED_ROUNDS=30000
	depthJitter        = 96  // issue2 §2.1：d = 3 + i%96
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

// BigInfo 与 overlay snapshots.Info 同构：多指针 + 非指针字段混合，位图多指针位。
// 指针位分布 word0={0,1,3,5,8,9,12,13,14,16,17}、word1={4}（J 在 offset 544，
// 把位图推到第二个 word）。552B 超寄存器返回阈值（>64B 且 >9 指针寄存器预算），
// 强制经 goret 内存槽返回——与 issue2 现场 offset +0xb8 命中 word3 的布局同思路。
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

// ---------- 经接口调用、堆逃逸的晚写入闭包（issue2 §4 条件 3） ----------

// infoWriter：闭包经接口分发 → 堆逃逸（与 containerd 经 storage.MetaStore 接口
// 调回调写 Stat/Update 结果的形状同构）。
type infoWriter interface {
	write(info *BigInfo)
}

type writerFn func(info *BigInfo)

func (f writerFn) write(info *BigInfo) { f(info) }

func makeWriter(seed uint64, base int64) infoWriter {
	return writerFn(func(info *BigInfo) {
		runtime.GC()                    // 写前 GC：goret 槽未初始化窗口被扫描（缺陷触发点）
		poison := poisonAlloc(seed, 48) // 同 size class 分配：吞掉刚释放的 span
		*info = makeBigInfo(seed, base) // 晚写入
		runtime.GC()                    // 写后 GC：已写入的合法值必须仍被正确扫描
		checkBigInfo(seed, base, *info, "g01.writer/post-gc")
		runtime.KeepAlive(poison)
		runtime.KeepAlive(info)
	})
}

// ---------- 脏栈制造 + 同构下降（issue2 §2.1） ----------

// fillDirtyBig 把 BigInfo 的全部指针字段刷成指向短命堆对象的指针（帧刷脏）。
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

// checksumBig 读取全部指针字段：使刷脏写成为活代码（防 DSE），且 c 的最后使用点
// 先于递归调用——递归调用的 safepoint 处这些槽已死，depth 0 的 GC 把刷入的
// 短命对象清扫成悬挂，此后各层帧字节 = 悬挂指针（脏栈）。
//
//go:noinline
func checksumBig(c *BigInfo) int64 {
	return c.A.id + c.B.id + c.C.id + c.D.id + c.E.id + c.F.id +
		c.G.id + c.H.id + c.I.id + c.J.id + c.S[0].id + c.S[1].id
}

// dirtyRec：以深度 d 递归把各层栈帧刷满指向短命堆对象的指针，depth 0 处
// runtime.GC() 清扫成悬挂。c 传给子帧，使子帧的入参 carrier 区也是脏的。
//
//go:noinline
func dirtyRec(seed uint64, base int64, d int, w infoWriter, carrier BigInfo) int64 {
	c := carrier
	fillDirtyBig(&c, seed, base)
	sum := checksumBig(&c)
	if d == 0 {
		runtime.GC()
		return sum
	}
	r := dirtyRec(seed, base+1000, d-1, w, c)
	return sum + r
}

// victimRec：与 dirtyRec 同型同深度下降（相同 byval BigInfo 保证帧尺寸一致），
// 同样刷脏——保证下降链每层帧都落在旧 dirtyRec 帧的脏字节上。depth 0 处调用
// victim：其 goret alloca 落在本帧（= 旧 dirtyRec 帧）的脏字上。
//
//go:noinline
func victimRec(seed uint64, base int64, d int, w infoWriter, carrier BigInfo) (info BigInfo) {
	c := carrier
	fillDirtyBig(&c, seed, base)
	sum := checksumBig(&c)
	if d == 0 {
		info = victim(seed, base, w)
		_ = sum
		return info
	}
	info = victimRec(seed, base+1000, d-1, w, c)
	_ = sum
	return info
}

// victim：与 overlay Stat/Update 同构——命名结果超寄存器阈值，经 goret 内存槽
// 返回；结果由经接口调用、堆逃逸的闭包晚写入（窗口 = 函数入口 → 闭包写入完成）。
//
//go:noinline
func victim(seed uint64, base int64, w infoWriter) (info BigInfo) {
	w.write(&info)
	return info
}

// victimGate：一轮的完整复刻——先脏栈（dirtyRec），再同深度下降（victimRec）。
//
//go:noinline
func victimGate(seed uint64, base int64, d int, w infoWriter) (info BigInfo) {
	dirtyRec(seed, base+10, d, w, BigInfo{})
	info = victimRec(seed, base+20, d, w, BigInfo{})
	return info
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
	d := 3 + int(r%depthJitter) // issue2 §2.1 深度抖动：任意栈布局下对齐
	w.depthSum += int64(d)
	if w.depthMin == 0 || d < w.depthMin {
		w.depthMin = d
	}
	if d > w.depthMax {
		w.depthMax = d
	}

	wr := makeWriter(w.seed, base) // 每轮一枚堆逃逸闭包
	info := victimGate(w.seed, base, d, wr)
	checkBigInfo(w.seed, base, info, "g01.caller/post-return")

	// weak 强引用断言：结果仍由本地强引用，GC 后必须非 nil（漏标/UAF 探针）。
	wp := weak.Make(info.A)
	runtime.GC()
	if v := wp.Value(); v == nil {
		failf("phase=weak worker=%d round=%d seed=%d: strong ref held but object collected (goret 漏标/UAF)",
			w.id, r, w.seed)
	} else {
		checkPayload(v, w.seed, base, "g01.weak-strong-ref")
	}
	atomic.AddInt64(&gWeakChecks, 1)
	w.gcN += 4

	runtime.KeepAlive(info)
	w.lastWeak = []weak.Pointer[payload]{wp}
	w.lastWeakID = []int64{base}
	w.rounds++
	w.next = r + 1
	w.checks += 24
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

	fmt.Printf("scenario=%s phase=preflight go=%s goos=%s goarch=%s gomaxprocs=%d seed=%d seconds=%d workers=%d fixed_rounds=%d heartbeat=%ds depth_jitter=%d biginfo_size=%d\n",
		scenario, runtime.Version(), runtime.GOOS, runtime.GOARCH, runtime.GOMAXPROCS(0),
		seed, seconds, workers, fixed, hb, depthJitter, reflect.TypeOf(BigInfo{}).Size())

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
	fmt.Printf("metrics: rounds=%d canary_checks=%d worker_gc=%d weak_checks=%d weak_alive_after_drop=%d depth_min=%d depth_max=%d depth_avg=%d num_gc=%d heap_alloc=%d heap_objects=%d goroutines_start=%d goroutines_final=%d\n",
		roundsTotal, checksTotal, gcTotal, atomic.LoadInt64(&gWeakChecks),
		aliveAfterDrop, dmin, dmax, depthTotal/max(roundsTotal, 1),
		m.NumGC, m.HeapAlloc, m.HeapObjects, baseGoroutines, ng)
	fmt.Printf("STRESS PASS %s\n", scenario)
}
