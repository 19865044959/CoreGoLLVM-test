// X14: weak / finalizer / 复活 / AddCleanup（P1）。
// 目标：覆盖 n03 未打的三块：
//   - 终结器内把对象存回全局（复活）→ 再 SetFinalizer 重新武装 → 再次失活；
//   - AddCleanup 同对象多注册 / Stop / 跨对象交错，断言“不提前、不重复、Stop 生效”；
//   - weak.Pointer 直接判据：持强引用时 Value() 必须非 nil（活对象被回收 = FAIL）；
//     失活后 Value() 应在轮询上限内转 nil（tiny/zero-size 按运行时文档不做该断言）；
//   - interior / tiny / zero-size 变体。
//
// 注：Go 文档规定同一对象的多个 cleanup 执行顺序未定义，因此这里断言的是精确集合、
// 每项恰好一次、Stop 项永不执行、对象强持有期间零执行——而不是执行次序。
// finale profile（checkfinalizers=1）在支持的 Go 版本上会把任何可达对象被终结直接 throw，
// 本场景自身的检查与其互补；在 Go 1.24.1 上未知 GODEBUG 键被忽略，检查仍全部生效。
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

const scenario = "x14_weak_resurrect"

const finWait = 15 * time.Second

type wobj struct {
	id   int64
	v    int64
	pad  [2]uint64
	next *wobj
}

type interObj struct {
	id   int64
	v    int64
	pad  [4]int64
	next *wobj
}

func objV(seed uint64, id int64) int64 {
	return int64(seed*0x9E3779B97F4A7C15^uint64(id)*0xBF58476D1CE4E5B9) | 1
}

var (
	gSeed    uint64
	beats    int64
	started  int64
	finished int64
	gcCycles int64
)

func failf(format string, args ...interface{}) {
	fmt.Printf("FAIL: seed=%d gcCycles=%d "+format+"\n",
		append([]interface{}{gSeed, atomic.LoadInt64(&gcCycles)}, args...)...)
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

//go:noinline
func checkW(seed uint64, p *wobj, id int64, phase string) {
	if p == nil {
		failf("phase=%s id=%d: nil *wobj", phase, id)
	}
	if p.id != id || p.v != objV(seed, id) {
		failf("phase=%s id=%d: object corrupted (got id=%d v=%#x want=%#x)", phase, id, p.id, p.v, objV(seed, id))
	}
}

//go:noinline
func checkInter(seed uint64, h *interObj, id int64, phase string) {
	if h == nil {
		failf("phase=%s id=%d: nil *interObj", phase, id)
	}
	if h.id != id || h.v != objV(seed, id) || h.pad != [4]int64{11, 22, 33, 44} {
		failf("phase=%s id=%d: interior object corrupted", phase, id)
	}
}

// dropW/dropInter/dropTiny/dropZero 是显式失活点：参数只在调用期间存活，
// 返回后对象仅剩 weak/cleanup/finalizer 关联。
//
//go:noinline
func dropW(p *wobj) { runtime.KeepAlive(p) }

//go:noinline
func dropInter(h *interObj) { runtime.KeepAlive(h) }

//go:noinline
func dropTiny(p *int8) { runtime.KeepAlive(p) }

//go:noinline
func dropZero(p *[0]int64) { runtime.KeepAlive(p) }

// makeWeakPlain 在 GC 期间保持强引用，断言 weak.Value() 非 nil；返回后强引用消失。
//
//go:noinline
func makeWeakPlain(seed uint64, id int64) weak.Pointer[wobj] {
	obj := &wobj{id: id, v: objV(seed, id)}
	wp := weak.Make(obj)
	runtime.GC()
	atomic.AddInt64(&gcCycles, 1)
	v := wp.Value()
	if v == nil {
		failf("phase=weak.plain id=%d: 强引用持有时 weak.Value()==nil（活对象被回收）", id)
	}
	if v.id != id || v.v != objV(seed, id) {
		failf("phase=weak.plain id=%d: weak 取回对象损坏", id)
	}
	runtime.KeepAlive(obj)
	return wp
}

// makeWeakInterior 对 interior 指针做强持有判定。
//
//go:noinline
func makeWeakInterior(seed uint64, id int64) weak.Pointer[int64] {
	h := &interObj{id: id, v: objV(seed, id), pad: [4]int64{11, 22, 33, 44}}
	wp := weak.Make(&h.pad[1])
	runtime.GC()
	atomic.AddInt64(&gcCycles, 1)
	v := wp.Value()
	if v == nil {
		failf("phase=weak.interior id=%d: 强引用持有时 interior weak.Value()==nil", id)
	}
	if *v != 22 {
		failf("phase=weak.interior id=%d: interior 值=%d want=22", id, *v)
	}
	runtime.KeepAlive(h)
	return wp
}

// checkTinyWeak 断言 tiny 对象强持有时 weak 非 nil（tiny 失活后不保证转 nil）。
//
//go:noinline
func checkTinyWeak() {
	t := new(int8)
	*t = 7
	wt := weak.Make(t)
	runtime.GC()
	atomic.AddInt64(&gcCycles, 1)
	v := wt.Value()
	if v == nil {
		failf("phase=weak.tiny: 强引用持有时 weak.Value()==nil")
	}
	if *t != 7 {
		failf("phase=weak.tiny: tiny 值=%d want=7", *t)
	}
	runtime.KeepAlive(t)
}

func waitWeakNil(wp weak.Pointer[wobj], phase string) {
	deadline := time.Now().Add(finWait)
	for {
		runtime.GC()
		atomic.AddInt64(&gcCycles, 1)
		if wp.Value() == nil {
			return
		}
		if time.Now().After(deadline) {
			failf("phase=%s: 失活对象 weak.Value() 未在 %s 内转 nil（漏回收/泄漏）", phase, finWait)
		}
		time.Sleep(time.Millisecond)
	}
}

func waitWeakIntNil(wp weak.Pointer[int64], phase string) {
	deadline := time.Now().Add(finWait)
	for {
		runtime.GC()
		atomic.AddInt64(&gcCycles, 1)
		if wp.Value() == nil {
			return
		}
		if time.Now().After(deadline) {
			failf("phase=%s: 失活 interior weak.Value() 未在 %s 内转 nil", phase, finWait)
		}
		time.Sleep(time.Millisecond)
	}
}

type finMsg struct {
	id  int64
	gen int
}

type cleanMsg struct {
	id  int64
	ord int
	arg int64
}

func waitFin(ev <-chan finMsg, gen int, phase string) finMsg {
	deadline := time.Now().Add(finWait)
	for {
		runtime.GC()
		atomic.AddInt64(&gcCycles, 1)
		select {
		case m := <-ev:
			if m.gen != gen {
				failf("phase=%s: finalizer 事件顺序错乱 gen=%d want=%d", phase, m.gen, gen)
			}
			return m
		case <-time.After(time.Millisecond):
		}
		if time.Now().After(deadline) {
			failf("phase=%s: finalizer gen=%d 未在 %s 内执行", phase, gen, finWait)
		}
	}
}

func waitClean(ev <-chan cleanMsg, want int, phase string) []cleanMsg {
	got := make([]cleanMsg, 0, want)
	deadline := time.Now().Add(finWait)
	for len(got) < want {
		runtime.GC()
		atomic.AddInt64(&gcCycles, 1)
		select {
		case m := <-ev:
			got = append(got, m)
		case <-time.After(time.Millisecond):
		}
		if time.Now().After(deadline) {
			failf("phase=%s: cleanup 只收到 %d/%d", phase, len(got), want)
		}
	}
	return got
}

func drainClean(ev <-chan cleanMsg, phase string) {
	// 已收到期望事件后再做一次 GC，任何额外事件（重复/Stop 未生效）都判 FAIL。
	runtime.GC()
	atomic.AddInt64(&gcCycles, 1)
	for {
		select {
		case m := <-ev:
			failf("phase=%s: 额外 cleanup id=%d ord=%d（重复或 Stop 未生效）", phase, m.id, m.ord)
		default:
			return
		}
	}
}

type worker struct {
	id   int64
	seed uint64

	rounds int64
	next   int64
	anchor *wobj

	anchorFins    int64
	tinyFins      int64
	zeroFins      int64
	weakChecks    int64
	resurrections int64
	cleanups      int64
	cleanupStops  int64
	zeroCleanups  int64

	resSlot  *wobj
	resSlot2 *wobj
}

func (w *worker) setup() {
	aid := w.id*1_000_000_000 + 7
	w.anchor = &wobj{id: aid, v: objV(w.seed, aid)}
	runtime.SetFinalizer(w.anchor, func(*wobj) {
		atomic.AddInt64(&w.anchorFins, 1)
	})
}

// resurrection：fin1 存回 w.resSlot（复活）并在终结器内重新 SetFinalizer；
// 再失活后 fin2 存回 w.resSlot2；之后不再注册，验证不会出现第三次终结。
func (w *worker) resurrection(seed uint64, id int64) {
	ev := make(chan finMsg, 4)
	obj := &wobj{id: id, v: objV(seed, id)}

	fin2 := func(p *wobj) {
		if p == nil || p.id != id || p.v != objV(seed, id) {
			failf("phase=resurrect.fin2 id=%d: 复活后对象损坏", id)
		}
		w.resSlot2 = p
		ev <- finMsg{id: id, gen: 2}
	}
	fin1 := func(p *wobj) {
		if p == nil || p.id != id || p.v != objV(seed, id) {
			failf("phase=resurrect.fin1 id=%d: 终结器收到损坏对象", id)
		}
		w.resSlot = p // 复活：从终结器把对象存回强引用槽
		runtime.SetFinalizer(p, fin2)
		ev <- finMsg{id: id, gen: 1}
	}

	runtime.SetFinalizer(obj, fin1)
	wp := weak.Make(obj)
	runtime.GC()
	atomic.AddInt64(&gcCycles, 1)
	if wp.Value() == nil {
		failf("phase=resurrect id=%d: 强引用持有时 weak.Value()==nil", id)
	}
	w.weakChecks++

	dropW(obj)
	obj = nil

	waitFin(ev, 1, "resurrect.gen1")
	checkW(seed, w.resSlot, id, "resurrect.gen1")
	w.resurrections++
	w.resSlot = nil // 再失活

	waitFin(ev, 2, "resurrect.gen2")
	checkW(seed, w.resSlot2, id, "resurrect.gen2")
	w.resurrections++
	w.resSlot2 = nil

	// 未再注册：不应有第三次终结事件。
	runtime.GC()
	runtime.GC()
	atomic.AddInt64(&gcCycles, 2)
	select {
	case m := <-ev:
		failf("phase=resurrect id=%d: 未再注册仍发生第三次终结 gen=%d", id, m.gen)
	default:
	}
}

// cleanupMulti：同一对象 3 个 cleanup，其中 ord2 立即 Stop；断言 ord1/ord3 各一次、ord2 永不执行。
func (w *worker) cleanupMulti(seed uint64, id int64) {
	ev := make(chan cleanMsg, 8)
	obj := &wobj{id: id, v: objV(seed, id)}

	c2 := runtime.AddCleanup(obj, func(a int64) {
		ev <- cleanMsg{id: id, ord: 2, arg: a}
	}, int64(2))
	runtime.AddCleanup(obj, func(a int64) {
		ev <- cleanMsg{id: id, ord: 1, arg: a}
	}, int64(1))
	runtime.AddCleanup(obj, func(a int64) {
		ev <- cleanMsg{id: id, ord: 3, arg: a}
	}, int64(3))
	c2.Stop()
	w.cleanupStops++

	// 强持有期间：不得有任何 cleanup 执行。
	runtime.GC()
	atomic.AddInt64(&gcCycles, 1)
	select {
	case m := <-ev:
		failf("phase=cleanup.multi id=%d: 强持有期间执行 cleanup ord=%d", id, m.ord)
	default:
	}

	dropW(obj)
	obj = nil

	got := waitClean(ev, 2, "cleanup.multi")
	seen := map[int]int{}
	for _, m := range got {
		if m.id != id {
			failf("phase=cleanup.multi id=%d: 收到错误对象 id=%d", id, m.id)
		}
		if m.arg != int64(m.ord) {
			failf("phase=cleanup.multi id=%d: cleanup arg=%d want=%d", id, m.arg, m.ord)
		}
		if m.ord == 2 {
			failf("phase=cleanup.multi id=%d: Stop 的 cleanup 仍执行", id)
		}
		seen[m.ord]++
	}
	if seen[1] != 1 || seen[3] != 1 || seen[2] != 0 {
		failf("phase=cleanup.multi id=%d: 执行集合=%v want {1:1,3:1}", id, seen)
	}
	w.cleanups += 2
	drainClean(ev, "cleanup.multi")
}

// cleanupInterleaved：两个对象交错注册/Stop；A 失活时 B 仍强持有，不得出现 B 的 cleanup。
func (w *worker) cleanupInterleaved(seed uint64, idA, idB int64) {
	ev := make(chan cleanMsg, 16)
	a := &wobj{id: idA, v: objV(seed, idA)}
	b := &wobj{id: idB, v: objV(seed, idB)}

	mk := func(ord int) {
		runtime.AddCleanup(a, func(x int64) {
			ev <- cleanMsg{id: idA, ord: ord, arg: x}
		}, int64(ord))
		runtime.AddCleanup(b, func(x int64) {
			ev <- cleanMsg{id: idB, ord: ord, arg: x}
		}, int64(ord))
	}
	mk(1)
	cA2 := runtime.AddCleanup(a, func(x int64) {
		ev <- cleanMsg{id: idA, ord: 2, arg: x}
	}, int64(2))
	cB2 := runtime.AddCleanup(b, func(x int64) {
		ev <- cleanMsg{id: idB, ord: 2, arg: x}
	}, int64(2))
	cA2.Stop()
	cB2.Stop()
	w.cleanupStops += 2
	runtime.AddCleanup(a, func(x int64) {
		ev <- cleanMsg{id: idA, ord: 3, arg: x}
	}, int64(3))
	runtime.AddCleanup(b, func(x int64) {
		ev <- cleanMsg{id: idB, ord: 3, arg: x}
	}, int64(3))

	runtime.GC()
	atomic.AddInt64(&gcCycles, 1)
	select {
	case m := <-ev:
		failf("phase=cleanup.interleaved: 两者均强持有时执行 cleanup id=%d ord=%d", m.id, m.ord)
	default:
	}

	// 只丢 A。
	dropW(a)
	a = nil
	gotA := waitClean(ev, 2, "cleanup.interleaved.A")
	for _, m := range gotA {
		if m.id != idA {
			failf("phase=cleanup.interleaved: B 仍强持有却执行 cleanup id=%d ord=%d", m.id, m.ord)
		}
		if m.ord == 2 {
			failf("phase=cleanup.interleaved: A 的 Stop cleanup 仍执行")
		}
		if m.arg != int64(m.ord) {
			failf("phase=cleanup.interleaved: A arg=%d ord=%d", m.arg, m.ord)
		}
	}
	runtime.KeepAlive(b) // B 必须仍可达
	w.cleanups += 2

	// 再丢 B。
	dropW(b)
	b = nil
	gotB := waitClean(ev, 2, "cleanup.interleaved.B")
	seenB := map[int]int{}
	for _, m := range gotB {
		if m.id != idB {
			failf("phase=cleanup.interleaved: 收到 A 的延迟 cleanup id=%d ord=%d", m.id, m.ord)
		}
		if m.ord == 2 {
			failf("phase=cleanup.interleaved: B 的 Stop cleanup 仍执行")
		}
		seenB[m.ord]++
	}
	if seenB[1] != 1 || seenB[3] != 1 {
		failf("phase=cleanup.interleaved: B 执行集合=%v want {1:1,3:1}", seenB)
	}
	w.cleanups += 2
	drainClean(ev, "cleanup.interleaved")
}

// cleanupInterior：cleanup 注册在对象的 interior 指针（&pad[1]）上，仍应在整个对象失活后执行。
func (w *worker) cleanupInterior(seed uint64, id int64) {
	ev := make(chan cleanMsg, 2)
	h := &interObj{id: id, v: objV(seed, id), pad: [4]int64{11, 22, 33, 44}}
	runtime.AddCleanup(&h.pad[1], func(a int64) {
		ev <- cleanMsg{id: id, ord: 1, arg: a}
	}, int64(1))

	runtime.GC()
	atomic.AddInt64(&gcCycles, 1)
	select {
	case m := <-ev:
		failf("phase=cleanup.interior id=%d: 强持有期间执行 cleanup ord=%d", id, m.ord)
	default:
	}

	dropInter(h)
	h = nil
	got := waitClean(ev, 1, "cleanup.interior")
	if got[0].id != id || got[0].ord != 1 || got[0].arg != 1 {
		failf("phase=cleanup.interior id=%d: cleanup 事件损坏 %+v", id, got[0])
	}
	w.cleanups++
	drainClean(ev, "cleanup.interior")
}

// tinyZero：tiny 用 weak 强持有判定；zero-size 按运行时文档不保证 weak/cleanup 语义，
// 只注册 finalizer+cleanup 并记录（不断言执行），同时确认强持有期间无提前触发。
func (w *worker) tinyZero(seed uint64, id int64) {
	checkTinyWeak()
	w.weakChecks++

	// tiny：每对象独立事件通道，只断言“强持有期间不得提前触发”；最终是否执行不断言。
	tev := make(chan struct{}, 1)
	t := new(int8)
	*t = 7
	runtime.SetFinalizer(t, func(*int8) {
		select {
		case tev <- struct{}{}:
		default:
		}
		atomic.AddInt64(&w.tinyFins, 1)
	})
	runtime.GC()
	atomic.AddInt64(&gcCycles, 1)
	select {
	case <-tev:
		failf("phase=tiny id=%d: 强持有期间 finalizer 提前执行", id)
	default:
	}
	if *t != 7 {
		failf("phase=tiny id=%d: 值=%d want=7", id, *t)
	}
	dropTiny(t)
	t = nil

	// zero-size：finalizer/cleanup 都不保证最终执行（运行时文档），只做提前触发断言。
	zev := make(chan struct{}, 2)
	z := new([0]int64)
	runtime.SetFinalizer(z, func(*[0]int64) {
		select {
		case zev <- struct{}{}:
		default:
		}
		atomic.AddInt64(&w.zeroFins, 1)
	})
	runtime.AddCleanup(z, func(int64) {
		select {
		case zev <- struct{}{}:
		default:
		}
		atomic.AddInt64(&w.zeroCleanups, 1)
	}, int64(0))
	runtime.GC()
	atomic.AddInt64(&gcCycles, 1)
	select {
	case <-zev:
		failf("phase=zero id=%d: 强持有期间 finalizer/cleanup 提前执行", id)
	default:
	}
	dropZero(z)
	z = nil
	runtime.KeepAlive(tev)
	runtime.KeepAlive(zev)
}

func (w *worker) round(r int64) {
	seed := w.seed ^ (uint64(r+1) * 0x9E3779B97F4A7C15)
	base := w.id*1_000_000_000 + r*1000 + 1

	runtime.GC()
	atomic.AddInt64(&gcCycles, 1)
	if n := atomic.LoadInt64(&w.anchorFins); n != 0 {
		failf("phase=round r=%d: 可达 anchor 被终结 (%d)", r, n)
	}

	wp := makeWeakPlain(seed, base)
	w.weakChecks++
	waitWeakNil(wp, fmt.Sprintf("weak.plain id=%d", base))

	wpi := makeWeakInterior(seed, base+10)
	w.weakChecks++
	waitWeakIntNil(wpi, fmt.Sprintf("weak.interior id=%d", base+10))

	w.tinyZero(seed, base+20)
	w.resurrection(seed, base+30)
	w.cleanupMulti(seed, base+40)
	w.cleanupInterleaved(seed, base+50, base+51)
	w.cleanupInterior(seed, base+60)

	runtime.GC()
	atomic.AddInt64(&gcCycles, 1)
	if n := atomic.LoadInt64(&w.anchorFins); n != 0 {
		failf("phase=round r=%d: 可达 anchor 被终结 (%d)", r, n)
	}

	w.rounds++
	w.next = r + 1
	atomic.AddInt64(&beats, 1)
}

func (w *worker) run(seconds int, deadline time.Time, fixed int64) {
	atomic.AddInt64(&started, 1)
	defer atomic.AddInt64(&finished, 1)
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
				failf("phase=watchdog: no progress for %s (beats=%d)", time.Since(lastChange), cur)
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
	workers := envInt("STRESS_WORKERS", 2, 1, 16)
	hb := envInt("STRESS_HEARTBEAT", 5, 1, 60)
	seed := envSeed(1)
	fixed := int64(envInt("STRESS_FIXED_ROUNDS", 8, 1, 1000000))
	gSeed = seed

	fmt.Printf("scenario=%s phase=preflight go=%s goos=%s goarch=%s gomaxprocs=%d seed=%d seconds=%d workers=%d fixed_rounds=%d heartbeat=%ds\n",
		scenario, runtime.Version(), runtime.GOOS, runtime.GOARCH, runtime.GOMAXPROCS(0),
		seed, seconds, workers, fixed, hb)

	ws := make([]*worker, workers)
	for i := range ws {
		ws[i] = &worker{
			id:   int64(i),
			seed: seed*1_000_003 + uint64(i+1)*7919,
		}
		ws[i].setup()
	}
	// 让 runtime 的 finalizer/cleanup 内部 goroutine 先启动，再取 goroutine 基线。
	for i := 0; i < 3; i++ {
		runtime.GC()
		time.Sleep(time.Millisecond)
	}
	baseGoroutines := runtime.NumGoroutine()

	deadline := time.Now().Add(time.Duration(seconds) * time.Second)
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

	if s, f := atomic.LoadInt64(&started), atomic.LoadInt64(&finished); s != int64(workers) || f != s {
		failf("phase=final: started=%d finished=%d workers=%d", s, f, workers)
	}

	var rounds, weakChecks, resurrections, cleanups, anchorFins, tinyFins, zeroFins, zeroClean int64
	for _, w := range ws {
		rounds += w.rounds
		weakChecks += w.weakChecks
		resurrections += w.resurrections
		cleanups += w.cleanups
		anchorFins += atomic.LoadInt64(&w.anchorFins)
		tinyFins += atomic.LoadInt64(&w.tinyFins)
		zeroFins += atomic.LoadInt64(&w.zeroFins)
		zeroClean += atomic.LoadInt64(&w.zeroCleanups)
		if w.rounds == 0 {
			failf("phase=final worker=%d: no rounds completed", w.id)
		}
		if seconds == 0 && w.rounds != fixed {
			failf("phase=final worker=%d: rounds=%d want=%d", w.id, w.rounds, fixed)
		}
		if w.next != w.rounds {
			failf("phase=final worker=%d: rounds=%d next=%d", w.id, w.rounds, w.next)
		}
	}
	if anchorFins != 0 {
		failf("phase=final: 可达 anchor 被终结 (%d)", anchorFins)
	}
	if want := rounds * 2; resurrections != want {
		failf("phase=final: resurrections=%d want=%d (每轮 2 次终结/复活)", resurrections, want)
	}
	// 每轮 cleanup 执行数：multi 2 + interleaved(A 2 + B 2) 4 + interior 1 = 7。
	if want := rounds * 7; cleanups != want {
		failf("phase=final: cleanups=%d want=%d", cleanups, want)
	}

	// 收尾 GC 后再确认 anchor 未被终结；ws/w.anchor 仍为强引用。
	runtime.GC()
	runtime.GC()
	for _, w := range ws {
		if n := atomic.LoadInt64(&w.anchorFins); n != 0 {
			failf("phase=final worker=%d: 可达 anchor 被终结 (%d)", w.id, n)
		}
	}
	runtime.KeepAlive(ws)

	ng := waitGoroutines(baseGoroutines, 5*time.Second)
	if ng > baseGoroutines+4 {
		failf("phase=final: goroutines baseline=%d after=%d: goroutine leak", baseGoroutines, ng)
	}

	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	fmt.Printf("metrics: rounds=%d weak_checks=%d resurrections=%d cleanups_run=%d anchor_finalized=%d tiny_finalized=%d zero_finalized=%d zero_cleanups=%d gc_cycles=%d num_gc=%d heap_alloc=%d heap_objects=%d goroutines=%d\n",
		rounds, weakChecks, resurrections, cleanups, anchorFins, tinyFins, zeroFins, zeroClean,
		atomic.LoadInt64(&gcCycles), m.NumGC, m.HeapAlloc, m.HeapObjects, ng)
	fmt.Printf("STRESS PASS %s\n", scenario)
}
