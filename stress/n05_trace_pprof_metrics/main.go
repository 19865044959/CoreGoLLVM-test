// N05: trace/pprof/metrics 长跑。
// 负载 worker 持续深递归 + 指针分配 + channel 交换 + pprof.Do labels；
// 观察者并发反复 runtime/trace.Start/Stop（io.Discard 优先，失败退 /dev/null 或内存 buffer），
// 周期性写 goroutine/heap/block/mutex profile（debug=1 校验头部），并解析 runtime/metrics 样本。
// 独立 GC churn；结束做 goroutine 数基线检查。
package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"runtime"
	"runtime/debug"
	"runtime/metrics"
	"runtime/pprof"
	"runtime/trace"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

type node struct {
	v    int64
	name string
	next *node
}

var (
	beats      int64
	gcCycles   int64
	traceRuns  int64
	profRuns   int64
	metricRuns int64
	lastGCRead uint64

	traceWindow time.Duration
)

var metricNames = []string{
	"/sched/goroutines:goroutines",
	"/memory/classes/heap/objects:bytes",
	"/gc/cycles/total:gc-cycles",
}

var profileSpecs = []struct {
	name   string
	header string
}{
	{"goroutine", "goroutine profile: total "},
	{"heap", "heap profile: "},
	{"block", "--- contention:"},
	{"mutex", "--- mutex:"},
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

func head(b []byte) []byte {
	if len(b) > 96 {
		return b[:96]
	}
	return b
}

// deep 在每层分配指针槽并在展开时校验；调用方用 pprof.Do 加 label。
//
//go:noinline
func (w *worker) deep(d int, seed int64) int64 {
	local := &node{v: seed + int64(d), name: "n05"}
	var slots [8]*node
	slots[0] = local
	slots[7] = &node{v: seed, name: "n05s"}
	if d == 0 {
		if slots[0] != local || slots[7].v != seed {
			failf("phase=deep worker=%d depth=0 seed=%d: slot corrupted", w.id, seed)
		}
		return local.v + slots[7].v
	}
	r := w.deep(d-1, seed+1)
	if local.v != seed+int64(d) || local.name != "n05" || slots[0] != local || slots[7].v != seed {
		failf("phase=deep worker=%d depth=%d seed=%d: frame corrupted", w.id, d, seed)
	}
	return r + local.v
}

// deepExpected: 每层 local.v 恒为 seed0+depth，叶子再加一份同值。
func deepExpected(seed int64, depth int) int64 {
	v := seed + int64(depth)
	return int64(depth+2) * v
}

type worker struct {
	id     int64
	seed   uint64
	depth  int
	rounds int64
}

func (w *worker) round(r int64) {
	seed := int64(w.seed*1000003) + r*100003
	ctx := pprof.WithLabels(context.Background(), pprof.Labels(
		"worker", strconv.FormatInt(w.id, 10),
		"round", strconv.FormatInt(r%97, 10),
	))
	var got int64
	pprof.Do(ctx, pprof.Labels("phase", "deep"), func(context.Context) {
		got = w.deep(w.depth, seed)
	})
	runtime.KeepAlive(ctx)
	if want := deepExpected(seed, w.depth); got != want {
		failf("phase=deep worker=%d round=%d seed=%d: checksum=%d want=%d", w.id, r, w.seed, got, want)
	}

	ch := make(chan *node, 3)
	for i := 0; i < 3; i++ {
		ch <- &node{v: seed + int64(i), name: "chan"}
	}
	close(ch)
	i := 0
	for q := range ch {
		if q == nil || q.v != seed+int64(i) || q.name != "chan" {
			failf("phase=chan worker=%d round=%d seed=%d: item[%d] corrupted", w.id, r, w.seed, i)
		}
		i++
	}
	if i != 3 {
		failf("phase=chan worker=%d round=%d: received %d items", w.id, r, i)
	}

	sl := make([]*node, 256)
	for j := range sl {
		sl[j] = &node{v: seed + int64(j), name: "alloc"}
	}
	m := make(map[string]*node, 32)
	for j := 0; j < 32; j++ {
		m["k"+strconv.Itoa(j)] = sl[j]
	}
	if len(m) != 32 || sl[0].name != "alloc" || sl[255].v != seed+255 {
		failf("phase=alloc worker=%d round=%d seed=%d: allocation content corrupted", w.id, r, w.seed)
	}
	runtime.KeepAlive(sl)
	runtime.KeepAlive(m)
	if r%6 == 0 {
		runtime.GC()
	}
	w.rounds = r
	atomic.AddInt64(&beats, 1)
}

func (w *worker) run(deadline time.Time, seconds int, fixed int64) {
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

// traceCycle 串行执行一次 Start/Stop（并发 Start 会报 already enabled）。
// validate 时用内存 buffer 并校验 trace 头部；否则写 io.Discard。
func traceCycle(validate bool) {
	var mem bytes.Buffer
	var f *os.File
	var w io.Writer = io.Discard
	if validate {
		w = &mem
	}
	err := trace.Start(w)
	if err != nil {
		// 退化路径：/dev/null → 内存 buffer。
		f, err = os.OpenFile(os.DevNull, os.O_WRONLY, 0)
		if err == nil {
			err = trace.Start(f)
		}
		if err != nil {
			mem.Reset()
			validate = true
			err = trace.Start(&mem)
		}
	}
	if err != nil {
		failf("phase=trace: Start failed: %v", err)
	}
	time.Sleep(traceWindow)
	trace.Stop()
	if f != nil {
		_ = f.Close()
	}
	if validate {
		b := mem.Bytes()
		if len(b) < 24 {
			failf("phase=trace: output too short (%d bytes)", len(b))
		}
		if !bytes.HasPrefix(b, []byte("go 1.")) {
			failf("phase=trace: header prefix corrupted: %q", head(b))
		}
		if !bytes.Contains(head(b), []byte(" trace\x00\x00\x00")) {
			failf("phase=trace: version marker missing: %q", head(b))
		}
	}
	atomic.AddInt64(&traceRuns, 1)
}

func writeProfiles() {
	var buf bytes.Buffer
	for _, s := range profileSpecs {
		p := pprof.Lookup(s.name)
		if p == nil {
			failf("phase=pprof: profile %q not registered", s.name)
		}
		buf.Reset()
		if err := p.WriteTo(&buf, 1); err != nil {
			failf("phase=pprof: %s WriteTo: %v", s.name, err)
		}
		if !bytes.HasPrefix(buf.Bytes(), []byte(s.header)) {
			failf("phase=pprof: %s header corrupted: %q", s.name, head(buf.Bytes()))
		}
	}
	atomic.AddInt64(&profRuns, 1)
}

func newMetricSamples() []metrics.Sample {
	s := make([]metrics.Sample, len(metricNames))
	for i := range s {
		s[i].Name = metricNames[i]
	}
	return s
}

func initMetrics() {
	have := make(map[string]bool)
	for _, d := range metrics.All() {
		have[d.Name] = true
	}
	for _, n := range metricNames {
		if !have[n] {
			failf("phase=metrics: metric %q not registered", n)
		}
	}
}

func readMetricsNow() {
	samples := newMetricSamples()
	metrics.Read(samples)
	readMetricSamples(samples)
}

func readMetricSamples(samples []metrics.Sample) {
	for i := range samples {
		if samples[i].Value.Kind() != metrics.KindUint64 {
			failf("phase=metrics: %q kind=%v want uint64", samples[i].Name, samples[i].Value.Kind())
		}
	}
	g := samples[0].Value.Uint64()
	if g == 0 {
		failf("phase=metrics: /sched/goroutines:goroutines parsed 0")
	}
	gc := samples[2].Value.Uint64()
	if gc < lastGCRead {
		failf("phase=metrics: gc cycles went backwards (%d → %d)", lastGCRead, gc)
	}
	lastGCRead = gc
	atomic.AddInt64(&metricRuns, 1)
}

func observer(stop <-chan struct{}, wg *sync.WaitGroup) {
	defer wg.Done()
	samples := newMetricSamples()
	var cycle int64
	for {
		select {
		case <-stop:
			return
		default:
		}
		traceCycle(cycle%3 == 0)
		writeProfiles()
		metrics.Read(samples)
		readMetricSamples(samples)
		cycle++
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
			b := make([]byte, 4096)
			b[0] = byte(i)
			sink = append(sink[:0], b)
		}
		for i := range sink {
			sink[i] = nil
		}
		sink = sink[:0]
		runtime.KeepAlive(sink)
		runtime.GC()
		atomic.AddInt64(&gcCycles, 1)
		time.Sleep(2 * time.Millisecond)
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
	workers := envInt("STRESS_WORKERS", 4, 1, 16)
	hb := envInt("STRESS_HEARTBEAT", 5, 1, 60)
	seed := envSeed(1)
	fixed := int64(envInt("STRESS_FIXED_ROUNDS", 16, 1, 100000))
	depth := envInt("STRESS_DEPTH", 96, 16, 512)
	traceWindow = time.Duration(envInt("STRESS_TRACE_MS", 8, 1, 200)) * time.Millisecond

	initMetrics()
	runtime.SetBlockProfileRate(1 << 20)
	runtime.SetMutexProfileFraction(1)

	fmt.Printf("scenario=n05 phase=preflight go=%s goos=%s goarch=%s gomaxprocs=%d seed=%d seconds=%d workers=%d depth=%d trace_ms=%d heartbeat=%ds\n",
		runtime.Version(), runtime.GOOS, runtime.GOARCH, runtime.GOMAXPROCS(0), seed, seconds, workers, depth, int(traceWindow/time.Millisecond), hb)

	// 预检：trace 头部、profile 头部、metrics 键在起负载前就验证一次。
	traceCycle(true)
	writeProfiles()
	readMetricsNow()

	startBase := runtime.NumGoroutine()
	deadline := time.Now().Add(time.Duration(seconds) * time.Second)
	stop := make(chan struct{})

	ws := make([]*worker, workers)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		w := &worker{id: int64(i), seed: seed*1000 + uint64(i) + 1, depth: depth}
		ws[i] = w
		wg.Add(1)
		go func() {
			defer wg.Done()
			w.run(deadline, seconds, fixed)
		}()
	}
	var helpers sync.WaitGroup
	helpers.Add(3)
	go observer(stop, &helpers)
	go gcChurn(stop, &helpers)
	go watchdog(time.Duration(hb)*time.Second, stop, &helpers)

	wg.Wait()
	close(stop)
	helpers.Wait()

	// quiesce 后 goroutine 基线：观察者/GC/watchdog 都应退出。
	runtime.GC()
	runtime.GC()
	final := runtime.NumGoroutine()
	if final > startBase+8 {
		failf("phase=quiesce: goroutine baseline exceeded: final=%d start=%d", final, startBase)
	}

	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	fmt.Printf("metrics: rounds=%d beats=%d gc_churn=%d trace_runs=%d prof_runs=%d metric_runs=%d gc_cycles=%d goroutines_start=%d goroutines_final=%d heap_alloc=%d heap_objects=%d num_gc=%d\n",
		ws[0].rounds, atomic.LoadInt64(&beats), atomic.LoadInt64(&gcCycles),
		atomic.LoadInt64(&traceRuns), atomic.LoadInt64(&profRuns), atomic.LoadInt64(&metricRuns),
		lastGCRead, startBase, final, m.HeapAlloc, m.HeapObjects, m.NumGC)
	fmt.Println("STRESS PASS n05_trace_pprof_metrics")
}
