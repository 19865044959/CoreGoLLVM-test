// N01: timer/netpoll/context 长跑。
// 多 worker 持续 Timer.Reset/Stop、AfterFunc 自重挂、ticker 多消费者、
// context 超时树；loopback TCP accept/dial/短 deadline/半关；
// 可选 STRESS_GMX 在阶段边界切换 GOMAXPROCS（默认不改，尊重 soak-single）；
// 独立 GC churn goroutine 持续施压。
package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"runtime"
	"runtime/debug"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	scenario     = "n01"
	timerSlots   = 16
	tickerPeriod = 2 * time.Millisecond
	netTimeout   = 3 * time.Second
	ctxTimeout   = 500 * time.Microsecond
	ctxDeadline  = 250 * time.Microsecond
	warmPayload  = "warm"
)

var (
	beats      int64
	gcCycles   int64
	serverEcho int64
)

type worker struct {
	id   int64
	seed uint64
	next int64

	// Timer.Reset/Stop storm
	timers         []*time.Timer
	timerArms      int64
	timerExpired   int64
	timerCancelled int64
	timerObserved  int64

	// AfterFunc 自重挂链
	afterMu        sync.Mutex
	afterTimer     *time.Timer
	afterStopFlag  bool
	afterArms      int64
	afterFired     int64
	afterCancelled int64
	afterQuit      chan struct{}
	afterQuitOnce  sync.Once

	// ticker 多消费者
	ticker         *time.Ticker
	tickerObserved int64
	tickerMu       sync.Mutex
	tickerSeen     map[int64]struct{}

	// 统计
	rounds    int64
	conns     int64
	echoBytes int64
	ctxDone   int64
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

func newWorker(id int64, seed uint64) *worker {
	w := &worker{
		id:         id,
		seed:       seed,
		tickerSeen: make(map[int64]struct{}),
	}
	for i := 0; i < timerSlots; i++ {
		w.timers = append(w.timers, time.NewTimer(time.Duration(1+i%3)*time.Millisecond))
	}
	w.timerArms = int64(timerSlots)
	w.startAfterChain()
	w.ticker = time.NewTicker(tickerPeriod)
	return w
}

// startAfterChain 启动一条自重挂 AfterFunc 链：每次回调计数后重新挂一个 AfterFunc，
// 直到 quiesce 时设置 afterStopFlag。afterTimer 与计数都由 afterMu 保护。
// arms == fired + cancelled 是收尾不变量。
func (w *worker) startAfterChain() {
	w.afterQuit = make(chan struct{})
	var cb func()
	cb = func() {
		w.afterMu.Lock()
		w.afterFired++
		if w.afterStopFlag {
			w.afterMu.Unlock()
			w.afterQuitOnce.Do(func() { close(w.afterQuit) })
			return
		}
		w.afterArms++
		w.afterTimer = time.AfterFunc(time.Millisecond, cb)
		w.afterMu.Unlock()
	}
	// 先挂长周期定时器并登记，再 Reset 到 1ms，避免回调与初始赋值竞争。
	t := time.AfterFunc(time.Hour, cb)
	w.afterMu.Lock()
	w.afterTimer = t
	w.afterArms = 1
	w.afterMu.Unlock()
	t.Reset(time.Millisecond)
}

// stormTimers 每轮对每个 timer 做 收取/Stop/Reset：
// 每次 Reset 记一个 arm；Stop 返回 true 表示取消（不会触发），
// 返回 false 表示该 arm 已过期（Go 1.23+ 无缓冲，值可能被丢弃）。
// 因此收尾不变量是 arms == cancelled + expired，且 observed <= expired。
func (w *worker) stormTimers(r int64) {
	for i, t := range w.timers {
		select {
		case <-t.C:
			w.timerObserved++
		default:
		}
		if t.Stop() {
			w.timerCancelled++
		} else {
			w.timerExpired++
			select {
			case <-t.C:
				w.timerObserved++
			default:
			}
		}
		w.timerArms++
		t.Reset(time.Duration(1+(int(r)+i)%3) * time.Millisecond)
	}
}

func (w *worker) stopTimers() {
	for _, t := range w.timers {
		select {
		case <-t.C:
			w.timerObserved++
		default:
		}
		if t.Stop() {
			w.timerCancelled++
		} else {
			w.timerExpired++
			select {
			case <-t.C:
				w.timerObserved++
			default:
			}
		}
	}
}

func (w *worker) stopAfterChain() {
	w.afterMu.Lock()
	w.afterStopFlag = true
	t := w.afterTimer
	w.afterMu.Unlock()
	if t.Stop() {
		w.afterMu.Lock()
		w.afterCancelled++
		w.afterMu.Unlock()
		return
	}
	select {
	case <-w.afterQuit:
	case <-time.After(10 * time.Second):
		failf("phase=quiesce worker=%d seed=%d: AfterFunc 自重挂链未在超时内停止", w.id, w.seed)
	}
}

func (w *worker) afterStats() (arms, fired, cancelled int64) {
	w.afterMu.Lock()
	defer w.afterMu.Unlock()
	return w.afterArms, w.afterFired, w.afterCancelled
}

// consumeTicker 是 ticker 的多消费者之一：只断言同一 tick 不被投递两次
// （全局唯一），以及计数只记录不断言速率。
func (w *worker) consumeTicker(stop <-chan struct{}, wg *sync.WaitGroup) {
	defer wg.Done()
	for {
		select {
		case <-stop:
			return
		case tv, ok := <-w.ticker.C:
			if !ok {
				return
			}
			ns := tv.UnixNano()
			w.tickerMu.Lock()
			_, dup := w.tickerSeen[ns]
			if !dup {
				w.tickerSeen[ns] = struct{}{}
			}
			w.tickerMu.Unlock()
			if dup {
				failf("phase=ticker worker=%d seed=%d: tick %d 被重复投递", w.id, w.seed, ns)
			}
			atomic.AddInt64(&w.tickerObserved, 1)
		}
	}
}

// contextRound 构造一棵超时 context 树与一个独立 deadline context，
// 校验 Err 类型与全部 Done 关闭。
func (w *worker) contextRound(r int64) {
	root, cancelRoot := context.WithTimeout(context.Background(), ctxTimeout)
	c1, cancel1 := context.WithCancel(root)
	c2, cancel2 := context.WithCancel(c1)
	dl, cancelDL := context.WithDeadline(context.Background(), time.Now().Add(ctxDeadline))

	<-root.Done()
	<-c1.Done()
	<-c2.Done()
	<-dl.Done()

	if !errors.Is(root.Err(), context.DeadlineExceeded) {
		failf("phase=context worker=%d round=%d seed=%d: root.Err=%v want=DeadlineExceeded", w.id, r, w.seed, root.Err())
	}
	// 父 context 超时取消时，cancelCtx 子节点继承父的 cause（DeadlineExceeded）。
	if !errors.Is(c1.Err(), context.DeadlineExceeded) {
		failf("phase=context worker=%d round=%d seed=%d: child1.Err=%v want=DeadlineExceeded", w.id, r, w.seed, c1.Err())
	}
	if !errors.Is(c2.Err(), context.DeadlineExceeded) {
		failf("phase=context worker=%d round=%d seed=%d: child2.Err=%v want=DeadlineExceeded", w.id, r, w.seed, c2.Err())
	}
	if !errors.Is(dl.Err(), context.DeadlineExceeded) {
		failf("phase=context worker=%d round=%d seed=%d: deadline.Err=%v want=DeadlineExceeded", w.id, r, w.seed, dl.Err())
	}
	cancel2()
	cancel1()
	cancelRoot()
	cancelDL()
	w.ctxDone += 4
}

func (w *worker) payload(r int64) []byte {
	n := 16 + int((w.seed+uint64(r)*7)%49)
	b := make([]byte, n)
	for i := range b {
		b[i] = byte((w.seed + uint64(r)*13 + uint64(i)*31) & 0xff)
	}
	return b
}

// netRound 做一次 dial → 写 → 半关 → 读完 echo → 校验字节；
// 每 4 轮额外用已过期 deadline 验证读超时路径。
func (w *worker) netRound(r int64, addr string) {
	c, err := net.DialTimeout("tcp", addr, netTimeout)
	if err != nil {
		failf("phase=dial worker=%d round=%d seed=%d addr=%s: %v", w.id, r, w.seed, addr, err)
	}
	if err := c.SetDeadline(time.Now().Add(netTimeout)); err != nil {
		failf("phase=deadline worker=%d round=%d: %v", w.id, r, err)
	}
	payload := w.payload(r)
	if _, err := c.Write(payload); err != nil {
		failf("phase=write worker=%d round=%d: %v", w.id, r, err)
	}
	tc, ok := c.(*net.TCPConn)
	if !ok {
		failf("phase=dial worker=%d round=%d: conn type=%T want=*net.TCPConn", w.id, r, c)
	}
	if err := tc.CloseWrite(); err != nil {
		failf("phase=halfclose worker=%d round=%d: %v", w.id, r, err)
	}
	got, err := io.ReadAll(c)
	if err != nil {
		failf("phase=read worker=%d round=%d: %v", w.id, r, err)
	}
	if !bytes.Equal(got, payload) {
		failf("phase=echo worker=%d round=%d seed=%d: echo=%d bytes want=%d bytes, mismatch at=%d",
			w.id, r, w.seed, len(got), len(payload), firstDiff(got, payload))
	}
	c.Close()
	w.conns++
	w.echoBytes += int64(len(got))

	if r%4 == 0 {
		c2, err := net.DialTimeout("tcp", addr, netTimeout)
		if err != nil {
			failf("phase=expired-dial worker=%d round=%d: %v", w.id, r, err)
		}
		if err := c2.SetReadDeadline(time.Now().Add(500 * time.Microsecond)); err != nil {
			failf("phase=expired-deadline worker=%d round=%d: %v", w.id, r, err)
		}
		time.Sleep(2 * time.Millisecond)
		var buf [1]byte
		_, rerr := c2.Read(buf[:])
		var ne net.Error
		if rerr == nil || !errors.As(rerr, &ne) || !ne.Timeout() {
			failf("phase=expired-read worker=%d round=%d: err=%v want=timeout", w.id, r, rerr)
		}
		c2.Close()
	}
}

func firstDiff(a, b []byte) int {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	for i := 0; i < n; i++ {
		if a[i] != b[i] {
			return i
		}
	}
	return n
}

func (w *worker) round(r int64, addr string) {
	w.stormTimers(r)
	w.contextRound(r)
	w.netRound(r, addr)
	if r%8 == 0 {
		runtime.GC()
	}
	w.rounds++
	atomic.AddInt64(&beats, 1)
}

func (w *worker) runPhase(deadline time.Time, timed bool, end int64) {
	for {
		if timed {
			if !time.Now().Before(deadline) {
				return
			}
		} else if w.next >= end {
			return
		}
		w.round(w.next, netAddr)
		w.next++
	}
}

func acceptLoop(ln net.Listener, handlerWG, acceptWG *sync.WaitGroup) {
	defer acceptWG.Done()
	for {
		c, err := ln.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return
			}
			failf("phase=accept: %v", err)
		}
		handlerWG.Add(1)
		go func() {
			defer handlerWG.Done()
			serveConn(c)
		}()
	}
}

// serveConn 回显收到的字节；客户端半关后本端也半关并关闭。
func serveConn(c net.Conn) {
	defer c.Close()
	buf := make([]byte, 4096)
	for {
		n, err := c.Read(buf)
		if n > 0 {
			wn, werr := c.Write(buf[:n])
			atomic.AddInt64(&serverEcho, int64(wn))
			if werr != nil {
				return
			}
		}
		if err != nil {
			break
		}
	}
	if tc, ok := c.(*net.TCPConn); ok {
		_ = tc.CloseWrite()
	}
}

func warmup(addr string) error {
	c, err := net.DialTimeout("tcp", addr, netTimeout)
	if err != nil {
		return err
	}
	defer c.Close()
	if err := c.SetDeadline(time.Now().Add(netTimeout)); err != nil {
		return err
	}
	if _, err := c.Write([]byte(warmPayload)); err != nil {
		return err
	}
	tc := c.(*net.TCPConn)
	if err := tc.CloseWrite(); err != nil {
		return err
	}
	got, err := io.ReadAll(c)
	if err != nil {
		return err
	}
	if string(got) != warmPayload {
		return fmt.Errorf("warmup echo=%q", got)
	}
	return nil
}

// countFDs 仅 Linux 可用；其他平台返回 -1 表示只记录不校验。
func countFDs() int {
	if runtime.GOOS != "linux" {
		return -1
	}
	es, err := os.ReadDir("/proc/self/fd")
	if err != nil {
		return -1
	}
	return len(es)
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

var (
	netAddr string
	gSeed   uint64
)

func main() {
	setupCrashOutput()
	seconds := envInt("STRESS_SECONDS", 60, 0, 86400)
	workers := envInt("STRESS_WORKERS", 6, 1, 32)
	hb := envInt("STRESS_HEARTBEAT", 5, 1, 60)
	seed := envSeed(1)
	fixed := int64(envInt("STRESS_FIXED_ROUNDS", 32, 1, 1000000))
	gmx := parseGMX(os.Getenv("STRESS_GMX"))
	phases := len(gmx)
	if phases == 0 {
		phases = 1 // 未设置 STRESS_GMX：不改 GOMAXPROCS，尊重 soak-single 等 profile
	}
	gSeed = seed

	runtime.GC()
	baseGoroutines := runtime.NumGoroutine()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		failf("phase=listen seed=%d: %v", seed, err)
	}
	netAddr = ln.Addr().String()

	var handlerWG, acceptWG sync.WaitGroup
	acceptWG.Add(1)
	go acceptLoop(ln, &handlerWG, &acceptWG)

	if err := warmup(netAddr); err != nil {
		failf("phase=warmup seed=%d: %v", seed, err)
	}
	baseFD := countFDs()

	fmt.Printf("scenario=%s phase=preflight go=%s goos=%s goarch=%s gomaxprocs=%d seed=%d seconds=%d workers=%d heartbeat=%ds fixed_rounds=%d gmx=%v phases=%d addr=%s\n",
		scenario, runtime.Version(), runtime.GOOS, runtime.GOARCH, runtime.GOMAXPROCS(0), seed, seconds, workers, hb, fixed, gmx, phases, netAddr)

	ws := make([]*worker, workers)
	for i := range ws {
		ws[i] = newWorker(int64(i), seed*1000+uint64(i)+1)
	}

	stop := make(chan struct{})
	var aux sync.WaitGroup
	aux.Add(1)
	go gcChurn(stop, &aux)
	aux.Add(1)
	go watchdog(time.Duration(hb)*time.Second, stop, &aux)
	var tickerWG sync.WaitGroup
	for _, w := range ws {
		for c := 0; c < 2; c++ { // 同一 ticker 的多个消费者
			tickerWG.Add(1)
			go w.consumeTicker(stop, &tickerWG)
		}
	}

	start := time.Now()
	var total time.Duration
	if seconds > 0 {
		total = time.Duration(seconds) * time.Second
	}

	for p := 0; p < phases; p++ {
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
			wg.Add(1)
			go func(w *worker) {
				defer wg.Done()
				w.runPhase(deadline, seconds > 0, pEnd)
			}(w)
		}
		wg.Wait()
	}

	// quiesce：停辅助 goroutine → 停 timer/AfterFunc → 关 listener → 等在途 handler。
	for _, w := range ws {
		w.stopTimers()
		w.stopAfterChain()
	}
	close(stop)
	tickerWG.Wait()
	aux.Wait()
	for _, w := range ws {
		w.ticker.Stop()
	}
	_ = ln.Close()
	acceptWG.Wait()
	handlerWG.Wait()

	// 收尾不变量。
	var roundsTotal, arms, expired, cancelled, observed, aftArms, aftFired, aftCancelled, tickTicks, conns, echoBytes, ctxDone int64
	for _, w := range ws {
		roundsTotal += w.rounds
		arms += w.timerArms
		expired += w.timerExpired
		cancelled += w.timerCancelled
		observed += w.timerObserved
		tickTicks += w.tickerObserved
		conns += w.conns
		echoBytes += w.echoBytes
		ctxDone += w.ctxDone
		a, f, c := w.afterStats()
		aftArms += a
		aftFired += f
		aftCancelled += c
		if w.timerArms != w.timerCancelled+w.timerExpired {
			failf("phase=final worker=%d seed=%d: timer arms=%d cancelled=%d expired=%d (arms != cancelled+expired)",
				w.id, w.seed, w.timerArms, w.timerCancelled, w.timerExpired)
		}
		if w.timerObserved > w.timerExpired {
			failf("phase=final worker=%d seed=%d: timer observed=%d expired=%d (spurious timer delivery)",
				w.id, w.seed, w.timerObserved, w.timerExpired)
		}
		if a != f+c {
			failf("phase=final worker=%d seed=%d: AfterFunc arms=%d fired=%d cancelled=%d", w.id, w.seed, a, f, c)
		}
		if seconds == 0 && w.next != fixed {
			failf("phase=final worker=%d seed=%d: rounds=%d want=%d", w.id, w.seed, w.next, fixed)
		}
	}
	if roundsTotal == 0 {
		failf("phase=final seed=%d: no rounds completed", seed)
	}
	if ew := atomic.LoadInt64(&serverEcho); ew != echoBytes+int64(len(warmPayload)) {
		failf("phase=final seed=%d: server echo=%d client echo=%d warmup=%d", seed, ew, echoBytes, len(warmPayload))
	}
	if conns < roundsTotal {
		failf("phase=final seed=%d: conns=%d rounds=%d", seed, conns, roundsTotal)
	}

	afterFD := countFDs()
	if baseFD >= 0 && afterFD > baseFD+4 {
		failf("phase=final seed=%d: fd baseline=%d after=%d (leak > 4)", seed, baseFD, afterFD)
	}
	ng := waitGoroutines(baseGoroutines, 5*time.Second)
	if ng > baseGoroutines+4 {
		failf("phase=final seed=%d: goroutines baseline=%d after=%d: goroutine leak", seed, baseGoroutines, ng)
	}

	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	fmt.Printf("metrics: rounds=%d timer_arms=%d timer_expired=%d timer_cancelled=%d timer_observed=%d after_arms=%d after_fired=%d after_cancelled=%d ticker_ticks=%d conns=%d echo_bytes=%d server_echo=%d ctx_done=%d gc_churn=%d fds_before=%d fds_after=%d gomaxprocs=%d heap_alloc=%d heap_objects=%d num_gc=%d goroutines=%d\n",
		roundsTotal, arms, expired, cancelled, observed, aftArms, aftFired, aftCancelled,
		tickTicks, conns, echoBytes, atomic.LoadInt64(&serverEcho), ctxDone,
		atomic.LoadInt64(&gcCycles), baseFD, afterFD, runtime.GOMAXPROCS(0),
		m.HeapAlloc, m.HeapObjects, m.NumGC, ng)
	fmt.Printf("STRESS PASS %s\n", "n01_timer_netpoll_storm")
}
