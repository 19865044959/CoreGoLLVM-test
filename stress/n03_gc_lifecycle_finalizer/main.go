// N03: GC 生命周期/finalizer 长跑。
// 分配-丢弃-GC 循环：普通对象（anchor 可达不得终结，失活最终终结，带超时 channel 探针）、
// 带 interior pointer 字段的 holder、tiny/zero-size 对象、sync.Pool + finalizer。
// 每轮只记录各类终结数，不断言终结速率；硬不变量是可达对象不得被终结、同一对象不得重复终结。
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
	scenario   = "n03"
	finClasses = 5
)

const (
	classPlain = iota
	classInterior
	classTiny
	classZero
	classPool
)

var classNames = [...]string{"plain", "interior", "tiny", "zero", "pool"}

type payloadObj struct {
	v    int64
	name string
}

type plainObj struct {
	id      int64
	v       int64
	name    string
	payload *payloadObj
}

type inner struct {
	id  int64
	v   int64
	pad [3]int64
}

type interiorHolder struct {
	base *inner
	mid  *int64 // base.pad[1] 的内部指针
	name string
}

type poolObj struct {
	id   int64
	v    int64
	name string
}

type finMsg struct {
	id    int64
	class int
}

var (
	idGen      int64
	allocCount [finClasses]int64
	finCount   [finClasses]int64
	finSent    int64
	finDropped int64
	finCh      chan finMsg
	finStop    chan struct{}
	finDone    chan struct{}
	probeCh    chan int64

	liveMu  sync.Mutex
	liveSet map[int64]struct{}

	// finBits 只由 collector goroutine 访问，用于检测同一 id 被终结两次。
	finBits []uint64

	beats      int64
	gcCycles   int64
	started    int64
	finished   int64
	gSeed      uint64
	probeArmed int32
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

func nextID() int64 {
	return atomic.AddInt64(&idGen, 1)
}

func sendFin(m finMsg) {
	atomic.AddInt64(&finSent, 1)
	select {
	case finCh <- m:
	default:
		atomic.AddInt64(&finDropped, 1)
	}
}

func finPlain(p *plainObj) {
	sendFin(finMsg{id: p.id, class: classPlain})
}

func finInterior(h *interiorHolder) {
	sendFin(finMsg{id: h.base.id, class: classInterior})
}

func finTiny(p *int8) {
	sendFin(finMsg{id: 0, class: classTiny})
}

func finZero(p *[0]int64) {
	sendFin(finMsg{id: 0, class: classZero})
}

func finPool(p *poolObj) {
	sendFin(finMsg{id: p.id, class: classPool})
}

func finProbe(p *plainObj) {
	sendFin(finMsg{id: p.id, class: classPlain})
	select {
	case probeCh <- p.id:
	default:
	}
}

func liveAdd(id int64) {
	liveMu.Lock()
	liveSet[id] = struct{}{}
	liveMu.Unlock()
}

func liveRemove(id int64) {
	liveMu.Lock()
	delete(liveSet, id)
	liveMu.Unlock()
}

// handleFin 只由 collector goroutine 调用。
func handleFin(m finMsg) {
	if m.class < 0 || m.class >= finClasses {
		failf("phase=finalizer seed=%d: invalid class=%d id=%d", gSeed, m.class, m.id)
	}
	atomic.AddInt64(&finCount[m.class], 1)
	if m.class == classTiny || m.class == classZero {
		// tiny/zero-size 的 finalizer 在 Go 运行时可能永不执行（tinyalloc），只记录。
		return
	}
	max := atomic.LoadInt64(&idGen)
	if m.id <= 0 || m.id > max {
		failf("phase=finalizer seed=%d: id=%d class=%d outside allocated range [1,%d]", gSeed, m.id, m.class, max)
	}
	liveMu.Lock()
	_, live := liveSet[m.id]
	liveMu.Unlock()
	if live {
		failf("phase=finalizer seed=%d: 可达对象被终结 id=%d class=%s", gSeed, m.id, classNames[m.class])
	}
	w := m.id >> 6
	if int(w) >= len(finBits) {
		grow := len(finBits) * 2
		if grow <= int(w) {
			grow = int(w) + 16
		}
		nb := make([]uint64, grow)
		copy(nb, finBits)
		finBits = nb
	}
	bit := uint64(1) << uint(m.id&63)
	if finBits[w]&bit != 0 {
		failf("phase=finalizer seed=%d: 同一对象重复终结 id=%d class=%s", gSeed, m.id, classNames[m.class])
	}
	finBits[w] |= bit
}

func finisher(stop <-chan struct{}, done chan struct{}) {
	defer close(done)
	for {
		select {
		case m := <-finCh:
			handleFin(m)
		case <-stop:
			for {
				select {
				case m := <-finCh:
					handleFin(m)
				default:
					return
				}
			}
		}
	}
}

func checkPlain(p *plainObj) {
	if p == nil || p.v != p.id*17+5 || p.name != "n03p" || p.payload == nil ||
		p.payload.v != p.id*19+7 || p.payload.name != "n03pay" {
		failf("phase=check-plain seed=%d: object corrupted id=%v", gSeed, p)
	}
}

func checkInterior(h *interiorHolder) {
	if h == nil || h.base == nil || h.mid == nil || h.name != "n03i" ||
		h.base.v != 4242 || h.base.pad != [3]int64{7, 8, 9} || *h.mid != 8 {
		failf("phase=check-interior seed=%d: interior pointer object corrupted", gSeed)
	}
}

type worker struct {
	id       int64
	seed     uint64
	next     int64
	rounds   int64
	anchors  []*plainObj
	ianchors []*interiorHolder
	pool     *sync.Pool
	lastFin  [finClasses]int64
	observed int64
}

func newPlain() *plainObj {
	id := nextID()
	p := &plainObj{
		id:      id,
		v:       id*17 + 5,
		name:    "n03p",
		payload: &payloadObj{v: id*19 + 7, name: "n03pay"},
	}
	runtime.SetFinalizer(p, finPlain)
	atomic.AddInt64(&allocCount[classPlain], 1)
	return p
}

//go:noinline
func allocProbe() {
	p := &plainObj{id: nextID(), v: 1, name: "n03probe", payload: &payloadObj{v: 2, name: "n03pay"}}
	runtime.SetFinalizer(p, finProbe)
	atomic.AddInt64(&allocCount[classPlain], 1)
	atomic.StoreInt32(&probeArmed, 1)
}

func newPoolObj() *poolObj {
	id := nextID()
	p := &poolObj{id: id, v: id*23 + 9, name: "n03pool"}
	runtime.SetFinalizer(p, finPool)
	atomic.AddInt64(&allocCount[classPool], 1)
	return p
}

func (w *worker) interiorRound() {
	base := &inner{id: nextID(), v: 4242, pad: [3]int64{7, 8, 9}}
	h := &interiorHolder{base: base, mid: &base.pad[1], name: "n03i"}
	runtime.SetFinalizer(h, finInterior)
	atomic.AddInt64(&allocCount[classInterior], 1)
	checkInterior(h)
	liveAdd(base.id)
	old := w.ianchors
	w.ianchors = []*interiorHolder{h}
	runtime.KeepAlive(old)
	for _, o := range old {
		liveRemove(o.base.id)
	}
}

func (w *worker) tinyZeroRound() {
	t := new(int8)
	*t = 7
	runtime.SetFinalizer(t, finTiny)
	atomic.AddInt64(&allocCount[classTiny], 1)
	runtime.KeepAlive(t)

	z := new([0]int64)
	runtime.SetFinalizer(z, finZero)
	atomic.AddInt64(&allocCount[classZero], 1)
	runtime.KeepAlive(z)
}

func (w *worker) poolRound(r int64) {
	p := newPoolObj()
	w.pool.Put(p)
	got := w.pool.Get()
	if got != nil {
		q, ok := got.(*poolObj)
		if !ok || q.id <= 0 || q.v != q.id*23+9 || q.name != "n03pool" {
			failf("phase=pool seed=%d worker=%d round=%d: pooled object corrupted", gSeed, w.id, r)
		}
		if r%3 != 0 {
			w.pool.Put(q)
		}
	}
	if r%16 == 0 {
		// 丢弃整个池（victim cache 中的对象随后可被回收并终结）。
		w.pool = &sync.Pool{}
	}
}

func (w *worker) round(r int64) {
	batch := make([]*plainObj, 6)
	for i := range batch {
		batch[i] = newPlain()
		checkPlain(batch[i])
	}
	newAnchors := make([]*plainObj, 0, 2)
	for i := 0; i < 2; i++ {
		liveAdd(batch[i].id)
		newAnchors = append(newAnchors, batch[i])
	}
	for i := 2; i < len(batch); i++ {
		batch[i] = nil // 失活，交由 GC 终结
	}
	old := w.anchors
	w.anchors = newAnchors
	runtime.KeepAlive(old)
	for _, p := range old {
		liveRemove(p.id)
	}

	if r%2 == 0 {
		w.interiorRound()
	}
	if r%4 == 0 {
		w.tinyZeroRound()
	}
	w.poolRound(r)

	// 每轮统计各类终结增量，只记录不断言速率。
	for c := 0; c < finClasses; c++ {
		cur := atomic.LoadInt64(&finCount[c])
		w.observed += cur - w.lastFin[c]
		w.lastFin[c] = cur
	}

	runtime.GC()
	for _, p := range w.anchors {
		checkPlain(p)
	}
	for _, h := range w.ianchors {
		checkInterior(h)
	}
	runtime.KeepAlive(w.anchors)
	runtime.KeepAlive(w.ianchors)

	w.rounds++
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
		w.next = r + 1
		r++
	}
	w.pool = nil
}

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
			b[0] = byte(i)
			sink = append(sink[:0], b)
		}
		for i := range sink {
			sink[i] = nil
		}
		sink = sink[:0]
		runtime.GC()
		atomic.AddInt64(&gcCycles, 1)
		// window（GOMAXPROCS=1+STW）下无休眠 GC 循环会饿死 worker/watchdog。
		if runtime.GOMAXPROCS(0) == 1 {
			runtime.Gosched()
			time.Sleep(time.Millisecond)
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
				failf("phase=watchdog seed=%d: 无 worker 进展 %s (beats=%d)", gSeed, time.Since(lastChange), cur)
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

func main() {
	setupCrashOutput()
	seconds := envInt("STRESS_SECONDS", 60, 0, 86400)
	workers := envInt("STRESS_WORKERS", 4, 1, 32)
	hb := envInt("STRESS_HEARTBEAT", 5, 1, 60)
	seed := envSeed(1)
	fixed := int64(envInt("STRESS_FIXED_ROUNDS", 32, 1, 1000000))
	gSeed = seed

	finCh = make(chan finMsg, 1<<16)
	finStop = make(chan struct{})
	finDone = make(chan struct{})
	probeCh = make(chan int64, 1)
	liveSet = make(map[int64]struct{})
	finBits = make([]uint64, 1024)

	baseGoroutines := runtime.NumGoroutine()
	go finisher(finStop, finDone)

	fmt.Printf("scenario=%s phase=preflight go=%s goos=%s goarch=%s gomaxprocs=%d seed=%d seconds=%d workers=%d fixed_rounds=%d heartbeat=%ds finbuf=%d\n",
		scenario, runtime.Version(), runtime.GOOS, runtime.GOARCH, runtime.GOMAXPROCS(0), seed, seconds, workers, fixed, hb, cap(finCh))

	deadline := time.Now().Add(time.Duration(seconds) * time.Second)
	ws := make([]*worker, workers)
	for i := 0; i < workers; i++ {
		ws[i] = &worker{
			id:   int64(i),
			seed: seed*1000 + uint64(i) + 1,
			pool: &sync.Pool{},
		}
	}

	var wg sync.WaitGroup
	for _, w := range ws {
		wg.Add(1)
		go func(w *worker) {
			defer wg.Done()
			w.run(seconds, deadline, fixed)
		}(w)
	}
	stop := make(chan struct{})
	var aux sync.WaitGroup
	aux.Add(1)
	go gcChurn(stop, &aux)
	aux.Add(1)
	go watchdog(time.Duration(hb)*time.Second, stop, &aux)

	wg.Wait()
	close(stop)
	aux.Wait()

	// 让残留 finalizer 有机会执行，再验证"失活对象最终可在超时内被终结"的探针。
	for i := 0; i < 3; i++ {
		runtime.GC()
		time.Sleep(10 * time.Millisecond)
	}
	atomic.StoreInt32(&probeArmed, 0)
	allocProbe()
	probeDeadline := time.Now().Add(10 * time.Second)
	probeOK := false
	for !probeOK {
		runtime.GC()
		select {
		case id := <-probeCh:
			if id <= 0 {
				failf("phase=probe seed=%d: invalid probe id=%d", seed, id)
			}
			probeOK = true
		case <-time.After(10 * time.Millisecond):
		}
		if !probeOK && time.Now().After(probeDeadline) {
			failf("phase=probe seed=%d: 失活对象的 finalizer 未在 10s 超时内执行（probeArmed=%d）",
				seed, atomic.LoadInt32(&probeArmed))
		}
	}

	close(finStop)
	<-finDone

	if s, f := atomic.LoadInt64(&started), atomic.LoadInt64(&finished); s != int64(workers) || f != s {
		failf("phase=final seed=%d: started=%d finished=%d workers=%d", seed, s, f, workers)
	}

	var roundsTotal, observed int64
	for _, w := range ws {
		roundsTotal += w.rounds
		observed += w.observed
		if seconds == 0 && w.next != fixed {
			failf("phase=final seed=%d worker=%d: rounds=%d want=%d", seed, w.id, w.next, fixed)
		}
	}
	if roundsTotal == 0 {
		failf("phase=final seed=%d: no rounds completed", seed)
	}
	for c := 0; c < finClasses; c++ {
		a := atomic.LoadInt64(&allocCount[c])
		f := atomic.LoadInt64(&finCount[c])
		if f > a {
			failf("phase=final seed=%d class=%s: finalized=%d allocated=%d", seed, classNames[c], f, a)
		}
	}

	ng := waitGoroutines(baseGoroutines, 5*time.Second)
	if ng > baseGoroutines+4 {
		failf("phase=final seed=%d: goroutines baseline=%d after=%d: goroutine leak", seed, baseGoroutines, ng)
	}

	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	fmt.Printf("metrics: rounds=%d allocated_plain=%d allocated_interior=%d allocated_tiny=%d allocated_zero=%d allocated_pool=%d finalized_plain=%d finalized_interior=%d finalized_tiny=%d finalized_zero=%d finalized_pool=%d finalized_observed=%d fin_sent=%d fin_dropped=%d gc_churn=%d heap_alloc=%d heap_objects=%d num_gc=%d goroutines=%d\n",
		roundsTotal,
		atomic.LoadInt64(&allocCount[classPlain]), atomic.LoadInt64(&allocCount[classInterior]),
		atomic.LoadInt64(&allocCount[classTiny]), atomic.LoadInt64(&allocCount[classZero]),
		atomic.LoadInt64(&allocCount[classPool]),
		atomic.LoadInt64(&finCount[classPlain]), atomic.LoadInt64(&finCount[classInterior]),
		atomic.LoadInt64(&finCount[classTiny]), atomic.LoadInt64(&finCount[classZero]),
		atomic.LoadInt64(&finCount[classPool]), observed,
		atomic.LoadInt64(&finSent), atomic.LoadInt64(&finDropped),
		atomic.LoadInt64(&gcCycles), m.HeapAlloc, m.HeapObjects, m.NumGC, ng)
	fmt.Printf("STRESS PASS %s\n", "n03_gc_lifecycle_finalizer")
}
