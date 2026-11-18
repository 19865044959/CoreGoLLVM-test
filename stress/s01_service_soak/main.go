// S01: 进程内 HTTP/JSON 服务长跑。
// httptest.Server 暴露 POST /work；多个客户端 worker 持续发送确定性请求。
// handler 解码/校验 → 计算 sum → 写入 map[int64]*record + ring slice + interface{} 缓存
// （按容量淘汰、按请求间隔显式 GC）→ 返回 JSON；客户端逐请求校验 id/name/sum/n。
// 阶段：warmup → steady → shrink（丢弃一半缓存 + debug.FreeOSMemory）→ regrow。
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"runtime"
	"runtime/debug"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const scenario = "s01"

const (
	stageWarmup int32 = iota
	stageSteady
	stageShrink
	stageRegrow
)

const gcEveryInserts = 128

var stageNames = [...]string{"warmup", "steady", "shrink", "regrow"}

var (
	beats      int64
	checks     int64
	requests   int64
	served     int64
	gcCycles   int64
	freeOS     int64
	cacheDrop  int64
	roundsDone int64

	stage int32

	startTime   time.Time
	deadline    time.Time
	seconds     int
	fixedRounds int64
	runWorkers  int
	gcache      *cache
)

type reqMsg struct {
	ID   int64   `json:"id"`
	Name string  `json:"name"`
	Vals []int64 `json:"vals"`
}

type respMsg struct {
	ID     int64  `json:"id"`
	Name   string `json:"name"`
	Sum    int64  `json:"sum"`
	N      int    `json:"n"`
	Served int64  `json:"served"`
}

// record 是不可变记录：入库后 vals/name/sum/iface 不再修改。
type record struct {
	id    int64
	name  string
	vals  []int64
	sum   int64
	iface interface{}
}

// cache: map + 环形 slice，按容量淘汰最老记录。
type cache struct {
	mu   sync.Mutex
	m    map[int64]*record
	ring []*record
	cap  int
}

func newCache(capacity int) *cache {
	return &cache{m: make(map[int64]*record, capacity), cap: capacity}
}

func (c *cache) len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.ring)
}

// insertChecked 在锁内完成入库与入库后一致性校验，避免“入库后被并发淘汰再校验”的误报。
func (c *cache) insertChecked(rec *record) (evicted int, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.m[rec.id] = rec
	c.ring = append(c.ring, rec)
	for len(c.ring) > c.cap {
		old := c.ring[0]
		c.ring[0] = nil
		c.ring = c.ring[1:]
		if cur, ok := c.m[old.id]; ok && cur == old {
			delete(c.m, old.id)
			evicted++
		}
	}
	got, ok := c.m[rec.id]
	if !ok || got != rec {
		return evicted, fmt.Errorf("id=%d expected=present in map right after insert actual=missing", rec.id)
	}
	if err := verifyRecord(got); err != nil {
		return evicted, fmt.Errorf("post-insert check: %w", err)
	}
	return evicted, nil
}

// dropOlderHalf 丢弃环中最老的一半记录并重建 map，返回保留/丢弃条数。
func (c *cache) dropOlderHalf() (kept, dropped int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	n := len(c.ring)
	if n == 0 {
		return 0, 0
	}
	drop := (n + 1) / 2
	for i := 0; i < drop; i++ {
		c.ring[i] = nil
	}
	c.ring = append([]*record(nil), c.ring[drop:]...)
	m := make(map[int64]*record, len(c.ring))
	for _, rec := range c.ring {
		m[rec.id] = rec
	}
	c.m = m
	return len(c.ring), drop
}

// verifyAll 全量校验缓存结构：容量、ring→map 索引、有效 record 内容/interface 视图。
func (c *cache) verifyAll() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.ring) > c.cap {
		return fmt.Errorf("ring size expected<=capacity actual=ring=%d capacity=%d", len(c.ring), c.cap)
	}
	backed := make(map[*record]struct{}, len(c.ring))
	for i, rec := range c.ring {
		if rec == nil {
			return fmt.Errorf("ring[%d] expected=*record actual=nil", i)
		}
		backed[rec] = struct{}{}
		cur, ok := c.m[rec.id]
		if !ok {
			return fmt.Errorf("ring[%d] id=%d expected=indexable in map actual=missing", i, rec.id)
		}
		if cur == rec {
			if err := verifyRecord(rec); err != nil {
				return err
			}
		}
	}
	if len(c.m) > len(c.ring) {
		return fmt.Errorf("map size expected<=ring size actual=map=%d ring=%d", len(c.m), len(c.ring))
	}
	for id, rec := range c.m {
		if _, ok := backed[rec]; !ok {
			return fmt.Errorf("map id=%d expected=backed by ring entry actual=missing", id)
		}
	}
	return nil
}

func verifyRecord(rec *record) error {
	if rec.name == "" || !strings.HasPrefix(rec.name, "s01-w") {
		return fmt.Errorf("id=%d name expected=\"s01-w*\" actual=%q", rec.id, rec.name)
	}
	if len(rec.vals) < 1 || len(rec.vals) > 64 {
		return fmt.Errorf("id=%d vals len expected=1..64 actual=%d", rec.id, len(rec.vals))
	}
	if got := sumInts(rec.vals); got != rec.sum {
		return fmt.Errorf("id=%d expected sum=%d actual sum=%d", rec.id, rec.sum, got)
	}
	arr, ok := rec.iface.([]int64)
	if !ok {
		return fmt.Errorf("id=%d iface expected=[]int64 actual=%T", rec.id, rec.iface)
	}
	if len(arr) != 2 || arr[0] != rec.id || arr[1] != rec.sum {
		return fmt.Errorf("id=%d iface expected=[%d %d] actual=%v", rec.id, rec.id, rec.sum, arr)
	}
	return nil
}

type service struct {
	cache *cache
}

func (s *service) handle(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		failf("scenario=%s phase=%s worker=-1 expected=\"POST\" actual=%q (path=%s)", scenario, phaseName(), r.Method, r.URL.Path)
	}
	var req reqMsg
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
		failf("scenario=%s phase=%s worker=-1 expected=valid JSON request actual=\"decode error: %v\"", scenario, phaseName(), err)
	}
	if req.ID <= 0 || !strings.HasPrefix(req.Name, "s01-w") || len(req.Vals) < 1 || len(req.Vals) > 64 {
		failf("scenario=%s phase=%s worker=-1 expected=\"id>0 name=s01-w* 1<=len(vals)<=64\" actual=\"id=%d name=%q n=%d\"",
			scenario, phaseName(), req.ID, req.Name, len(req.Vals))
	}
	sum := sumInts(req.Vals)
	rec := &record{
		id:   req.ID,
		name: req.Name,
		vals: append([]int64(nil), req.Vals...),
		sum:  sum,
	}
	rec.iface = []int64{rec.id, rec.sum}

	n := atomic.AddInt64(&served, 1)
	evicted, err := s.cache.insertChecked(rec)
	atomic.AddInt64(&cacheDrop, int64(evicted))
	atomic.AddInt64(&checks, 3)
	if err != nil {
		failf("scenario=%s phase=%s worker=-1 expected=\"id=%d name=%q sum=%d cached consistently\" actual=%v",
			scenario, phaseName(), rec.id, rec.name, sum, err)
	}
	if n%gcEveryInserts == 0 {
		runtime.GC()
		atomic.AddInt64(&gcCycles, 1)
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(&respMsg{ID: rec.id, Name: rec.name, Sum: sum, N: len(req.Vals), Served: n})
}

type worker struct {
	id     int64
	seed   uint64
	url    string
	client *http.Client
}

func (w *worker) requestFor(r int64) reqMsg {
	id := (w.id+1)*1_000_000_000_000 + r
	name := fmt.Sprintf("s01-w%d-r%d", w.id, r)
	n := 3 + int((w.seed+uint64(r)*7)%6)
	base := int64(w.seed%100000)*1000 + w.id*1000 + r*31
	vals := make([]int64, n)
	for i := range vals {
		vals[i] = base + int64(i*17)
	}
	return reqMsg{ID: id, Name: name, Vals: vals}
}

func (w *worker) round(r int64) {
	req := w.requestFor(r)
	body, err := json.Marshal(&req)
	if err != nil {
		failf("scenario=%s phase=%s worker=%d expected=request marshal ok actual=%v", scenario, phaseName(), w.id, err)
	}
	httpResp, err := w.client.Post(w.url, "application/json", bytes.NewReader(body))
	if err != nil {
		failf("scenario=%s phase=%s worker=%d expected=HTTP 200 actual=\"request error: %v\" (id=%d name=%q)",
			scenario, phaseName(), w.id, err, req.ID, req.Name)
	}
	if httpResp.StatusCode != http.StatusOK {
		httpResp.Body.Close()
		failf("scenario=%s phase=%s worker=%d expected=HTTP 200 actual=%d (id=%d name=%q)",
			scenario, phaseName(), w.id, httpResp.StatusCode, req.ID, req.Name)
	}
	var rsp respMsg
	err = json.NewDecoder(httpResp.Body).Decode(&rsp)
	httpResp.Body.Close()
	if err != nil {
		failf("scenario=%s phase=%s worker=%d expected=valid JSON response actual=\"decode error: %v\" (id=%d name=%q)",
			scenario, phaseName(), w.id, err, req.ID, req.Name)
	}

	expSum := sumInts(req.Vals)
	if rsp.ID != req.ID {
		failf("scenario=%s phase=%s worker=%d expected id=%d actual id=%d (name=%q)",
			scenario, phaseName(), w.id, req.ID, rsp.ID, req.Name)
	}
	if rsp.Name != req.Name {
		failf("scenario=%s phase=%s worker=%d expected name=%q actual name=%q (id=%d)",
			scenario, phaseName(), w.id, req.Name, rsp.Name, req.ID)
	}
	if rsp.Sum != expSum {
		failf("scenario=%s phase=%s worker=%d expected sum=%d actual sum=%d (id=%d name=%q vals=%v)",
			scenario, phaseName(), w.id, expSum, rsp.Sum, req.ID, req.Name, req.Vals)
	}
	if rsp.N != len(req.Vals) {
		failf("scenario=%s phase=%s worker=%d expected n=%d actual n=%d (id=%d name=%q)",
			scenario, phaseName(), w.id, len(req.Vals), rsp.N, req.ID, req.Name)
	}
	atomic.AddInt64(&checks, 4)
	atomic.AddInt64(&requests, 1)
	atomic.AddInt64(&beats, 1)
	advancePhase(atomic.AddInt64(&roundsDone, 1), w.id)
}

func (w *worker) run() {
	r := int64(0)
	for {
		if seconds > 0 {
			if time.Now().After(deadline) {
				return
			}
		} else if r >= fixedRounds {
			return
		}
		w.round(r)
		r++
	}
}

func sumInts(vals []int64) int64 {
	var s int64
	for _, v := range vals {
		s += v
	}
	return s
}

func phaseName() string {
	s := int(atomic.LoadInt32(&stage))
	if s < 0 || s >= len(stageNames) {
		return "unknown"
	}
	return stageNames[s]
}

// shrinkStage: 丢弃一半缓存 → 全量校验存活记录 → FreeOSMemory。
func shrinkStage(worker int64) {
	kept, dropped := gcache.dropOlderHalf()
	atomic.AddInt64(&cacheDrop, int64(dropped))
	if err := gcache.verifyAll(); err != nil {
		failf("scenario=%s phase=%s worker=%d expected=\"cache consistent after shrink\" actual=%v (kept=%d dropped=%d)",
			scenario, phaseName(), worker, err, kept, dropped)
	}
	atomic.AddInt64(&checks, 1)
	debug.FreeOSMemory()
	atomic.AddInt64(&freeOS, 1)
}

// advancePhase 由完成的请求推进：固定轮数按全局完成比例，时长模式按时长比例。
// 阶段只会前进；shrink/regrow 的副作用由抢占到 CAS 的 worker 执行一次。
func advancePhase(done, worker int64) {
	var frac float64
	if seconds > 0 {
		frac = time.Since(startTime).Seconds() / float64(seconds)
	} else {
		total := int64(runWorkers) * fixedRounds
		if total <= 0 {
			return
		}
		frac = float64(done) / float64(total)
	}
	if frac > 1 {
		frac = 1
	}
	want := stageWarmup
	switch {
	case frac >= 0.75:
		want = stageRegrow
	case frac >= 0.60:
		want = stageShrink
	case frac >= 0.20:
		want = stageSteady
	}
	for {
		cur := atomic.LoadInt32(&stage)
		if cur >= want {
			return
		}
		if !atomic.CompareAndSwapInt32(&stage, cur, want) {
			continue
		}
		if cur < stageShrink && want >= stageShrink {
			shrinkStage(worker)
		}
		if cur < stageRegrow && want == stageRegrow {
			runtime.GC()
			atomic.AddInt64(&gcCycles, 1)
		}
		return
	}
}

func finalize() {
	if atomic.LoadInt32(&stage) < stageShrink {
		atomic.StoreInt32(&stage, stageShrink)
		shrinkStage(-1)
	}
	if atomic.LoadInt32(&stage) < stageRegrow {
		atomic.StoreInt32(&stage, stageRegrow)
		runtime.GC()
		atomic.AddInt64(&gcCycles, 1)
	}
	if err := gcache.verifyAll(); err != nil {
		failf("scenario=%s phase=%s worker=-1 expected=\"final cache consistent\" actual=%v", scenario, phaseName(), err)
	}
	atomic.AddInt64(&checks, 1)
}

func gcChurn(stop <-chan struct{}, wg *sync.WaitGroup) {
	defer wg.Done()
	t := time.NewTicker(10 * time.Millisecond)
	defer t.Stop()
	var sink [][]byte
	for {
		select {
		case <-stop:
			return
		case <-t.C:
			for i := 0; i < 8; i++ {
				b := make([]byte, 4096)
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
}

func verifier(stop <-chan struct{}, wg *sync.WaitGroup) {
	defer wg.Done()
	t := time.NewTicker(5 * time.Millisecond)
	defer t.Stop()
	for {
		select {
		case <-stop:
			return
		case <-t.C:
			if err := gcache.verifyAll(); err != nil {
				failf("scenario=%s phase=%s worker=-1 expected=\"cache consistent\" actual=%v", scenario, phaseName(), err)
			}
			atomic.AddInt64(&checks, 1)
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
				failf("scenario=%s phase=%s worker=-1 expected=\"progress within %s\" actual=\"stalled %s\" (beats=%d)",
					scenario, phaseName(), 6*interval, time.Since(lastChange), cur)
			}
		}
	}
}

// waitGoroutines 仅用于关闭后的泄漏检查：等待 server/client 连接 goroutine 归零到基线。
func waitGoroutines(base int, grace time.Duration) int {
	giveUp := time.Now().Add(grace)
	for {
		n := runtime.NumGoroutine()
		if n <= base+2 || time.Now().After(giveUp) {
			return n
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func failf(format string, args ...interface{}) {
	fmt.Printf("FAIL: "+format+"\n", args...)
	buf := make([]byte, 4<<20)
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
	seconds = envInt("STRESS_SECONDS", 60, 0, 86400)
	runWorkers = envInt("STRESS_WORKERS", 8, 1, 64)
	hb := envInt("STRESS_HEARTBEAT", 5, 1, 60)
	seed := envSeed(1)
	fixedRounds = int64(envInt("STRESS_FIXED_ROUNDS", 32, 1, 100000))

	startTime = time.Now()
	if seconds > 0 {
		deadline = startTime.Add(time.Duration(seconds) * time.Second)
	}
	baseGoroutines := runtime.NumGoroutine()

	cacheCap := 32 + runWorkers*12
	gcache = newCache(cacheCap)
	svc := &service{cache: gcache}
	mux := http.NewServeMux()
	mux.HandleFunc("/work", svc.handle)
	srv := httptest.NewServer(mux)

	fmt.Printf("scenario=%s phase=preflight go=%s goos=%s goarch=%s gomaxprocs=%d seed=%d seconds=%d workers=%d fixed_rounds=%d heartbeat=%ds cache_cap=%d\n",
		scenario, runtime.Version(), runtime.GOOS, runtime.GOARCH, runtime.GOMAXPROCS(0), seed, seconds, runWorkers, fixedRounds, hb, cacheCap)

	transport := &http.Transport{
		MaxIdleConns:        runWorkers * 2,
		MaxIdleConnsPerHost: runWorkers * 2,
		IdleConnTimeout:     30 * time.Second,
	}
	client := &http.Client{Transport: transport}

	var wg sync.WaitGroup
	for i := 0; i < runWorkers; i++ {
		w := &worker{id: int64(i), seed: seed*1000 + uint64(i) + 1, url: srv.URL + "/work", client: client}
		wg.Add(1)
		go func() {
			defer wg.Done()
			w.run()
		}()
	}

	stop := make(chan struct{})
	var helpers sync.WaitGroup
	helpers.Add(3)
	go gcChurn(stop, &helpers)
	go verifier(stop, &helpers)
	go watchdog(time.Duration(hb)*time.Second, stop, &helpers)

	wg.Wait() // 先停生产（所有 worker 返回）
	close(stop)
	helpers.Wait()
	finalize()
	client.CloseIdleConnections()
	srv.Close() // drain：等待在途请求结束并关闭监听/连接

	n := waitGoroutines(baseGoroutines, 5*time.Second)
	if n > baseGoroutines+2 {
		failf("scenario=%s phase=%s worker=-1 expected=goroutines<=%d after shutdown actual=%d: goroutine leak",
			scenario, phaseName(), baseGoroutines+2, n)
	}

	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	fmt.Printf("metrics: requests=%d checks=%d served=%d cache=%d cache_dropped=%d gc_churn=%d freeos=%d beats=%d heap_alloc=%d heap_objects=%d num_gc=%d goroutines=%d\n",
		atomic.LoadInt64(&requests), atomic.LoadInt64(&checks), atomic.LoadInt64(&served), gcache.len(),
		atomic.LoadInt64(&cacheDrop), atomic.LoadInt64(&gcCycles), atomic.LoadInt64(&freeOS),
		atomic.LoadInt64(&beats), m.HeapAlloc, m.HeapObjects, m.NumGC, n)
	fmt.Println("STRESS PASS s01_service_soak")
}
