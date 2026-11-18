// S05: 生成代码形态长跑。
// 持续对 64 个生成 record 类型做 JSON marshal/unmarshal（每次编码/解码之间与之后
// 显式 runtime.GC）、1024 分支 dispatch、记录接口调用与深拷贝；阶段化推进并在
// shrink 阶段 debug.FreeOSMemory。校验 roundtrip 校验和、指针字段内容、深拷贝独立性。
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"runtime"
	"runtime/debug"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

type retained struct {
	rec  Record
	sum  int64
	kind RecordKind
	seed int64
}

type worker struct {
	id       int64
	seed     uint64
	retained []retained
	rounds   int64
}

var (
	beats         int64
	gcCalls       int64
	dispatchCalls int64
	copies        int64
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

// phaseName 按 24 轮一个周期推进：warmup → regrow → steady → shrink。
func phaseName(r int64) string {
	pos := r % 24
	switch {
	case r < 2:
		return "warmup"
	case pos == 0:
		return "shrink"
	case pos < 8:
		return "regrow"
	default:
		return "steady"
	}
}

// checkPayload 校验指针字段内容与构造公式一致（含嵌套 Children）。
func checkPayload(phase string, w *worker, round int64, kind RecordKind, seed int64, idx int, got *Payload) {
	want := NewPayload(seed, int64(idx))
	if got == nil {
		failf("phase=%s worker=%d round=%d seed=%d kind=%s: payload[%d] is nil", phase, w.id, round, w.seed, kind, idx)
	}
	if got.ID != want.ID || got.Key != want.Key || got.Value != want.Value || got.Weight != want.Weight {
		failf("phase=%s worker=%d round=%d seed=%d kind=%s: payload[%d] got={id:%d key:%q value:%d weight:%d} want={id:%d key:%q value:%d weight:%d}",
			phase, w.id, round, w.seed, kind, idx, got.ID, got.Key, got.Value, got.Weight, want.ID, want.Key, want.Value, want.Weight)
	}
	if !bytes.Equal(got.Data, want.Data) {
		failf("phase=%s worker=%d round=%d seed=%d kind=%s: payload[%d] data corrupted got=%x want=%x",
			phase, w.id, round, w.seed, kind, idx, got.Data, want.Data)
	}
	if len(got.Children) != len(want.Children) {
		failf("phase=%s worker=%d round=%d seed=%d kind=%s: payload[%d] children=%d want=%d",
			phase, w.id, round, w.seed, kind, idx, len(got.Children), len(want.Children))
	}
	for j := range want.Children {
		cw, cg := want.Children[j], got.Children[j]
		if cg == nil || cg.ID != cw.ID || cg.Key != cw.Key || cg.Value != cw.Value || cg.Weight != cw.Weight || !bytes.Equal(cg.Data, cw.Data) {
			failf("phase=%s worker=%d round=%d seed=%d kind=%s: payload[%d].child[%d] corrupted", phase, w.id, round, w.seed, kind, idx, j)
		}
	}
}

// checkRecordPointers 校验 []*Payload、Leader 指针与 Tags 内容。
func checkRecordPointers(phase string, w *worker, round int64, kind RecordKind, seed int64, rec Record) {
	ps := rec.Payloads()
	if len(ps) != 3 {
		failf("phase=%s worker=%d round=%d seed=%d kind=%s: payload count=%d want=3", phase, w.id, round, w.seed, kind, len(ps))
	}
	for i := 0; i < len(ps); i++ {
		checkPayload(phase, w, round, kind, seed, i, ps[i])
	}
	checkPayload(phase, w, round, kind, seed, 3, rec.LeaderPayload())
	tags := rec.TagValues()
	if len(tags) != 3 {
		failf("phase=%s worker=%d round=%d seed=%d kind=%s: tags=%d want=3", phase, w.id, round, w.seed, kind, len(tags))
	}
	for i := 0; i < len(tags); i++ {
		if tags[i] == "" {
			failf("phase=%s worker=%d round=%d seed=%d kind=%s: tag[%d] empty", phase, w.id, round, w.seed, kind, i)
		}
	}
}

func verifyRetained(phase string, w *worker, round int64, rt retained) {
	if rt.rec == nil {
		failf("phase=%s worker=%d round=%d seed=%d kind=%s: retained record is nil", phase, w.id, round, w.seed, rt.kind)
	}
	if got := rt.rec.Checksum(); got != rt.sum {
		failf("phase=%s worker=%d round=%d seed=%d kind=%s: retained checksum=%d want=%d", phase, w.id, round, w.seed, rt.kind, got, rt.sum)
	}
	checkRecordPointers(phase, w, round, rt.kind, rt.seed, rt.rec)
}

func (w *worker) round(r int64) {
	phase := phaseName(r)
	for _, rt := range w.retained {
		verifyRetained(phase, w, r, rt)
	}

	kind := RecordKind((int64(w.id)*23 + r) % int64(RecordKindCount))
	seed := int64(w.seed) + r*100003
	rec := NewRecord(kind, seed)
	if rec == nil {
		failf("phase=%s worker=%d round=%d seed=%d: NewRecord(%d) returned nil", phase, w.id, r, w.seed, kind)
	}
	if rec.Kind() != kind {
		failf("phase=%s worker=%d round=%d seed=%d: kind=%s want=%s", phase, w.id, r, w.seed, rec.Kind(), kind)
	}
	if KindName(kind) != kind.String() || KindDescription(kind) == "" {
		failf("phase=%s worker=%d round=%d seed=%d: enum table broken for kind=%d", phase, w.id, r, w.seed, kind)
	}
	if rec.GetID() <= 0 || rec.GetName() == "" {
		failf("phase=%s worker=%d round=%d seed=%d kind=%s: getter mismatch id=%d name=%q", phase, w.id, r, w.seed, kind, rec.GetID(), rec.GetName())
	}
	checkRecordPointers(phase, w, r, kind, seed, rec)

	op := int32((r*131 + w.id*17) & 1023)
	baseSum := rec.Checksum()
	baseDispatch := Dispatch(op, rec)
	atomic.AddInt64(&dispatchCalls, 1)
	if Dispatch(op, rec) != baseDispatch {
		failf("phase=%s worker=%d round=%d seed=%d kind=%s: dispatch not deterministic", phase, w.id, r, w.seed, kind)
	}

	data, err := json.Marshal(rec)
	if err != nil {
		failf("phase=%s worker=%d round=%d seed=%d kind=%s: marshal: %v", phase, w.id, r, w.seed, kind, err)
	}
	if len(data) == 0 {
		failf("phase=%s worker=%d round=%d seed=%d kind=%s: marshal produced empty output", phase, w.id, r, w.seed, kind)
	}
	atomic.AddInt64(&gcCalls, 1)
	runtime.GC()

	dec := NewEmptyRecord(kind)
	if dec == nil {
		failf("phase=%s worker=%d round=%d seed=%d: NewEmptyRecord(%d) returned nil", phase, w.id, r, w.seed, kind)
	}
	if err := json.Unmarshal(data, dec); err != nil {
		failf("phase=%s worker=%d round=%d seed=%d kind=%s: unmarshal: %v", phase, w.id, r, w.seed, kind, err)
	}
	atomic.AddInt64(&gcCalls, 1)
	runtime.GC()

	if dec.Kind() != kind {
		failf("phase=%s worker=%d round=%d seed=%d: decoded kind=%s want=%s", phase, w.id, r, w.seed, dec.Kind(), kind)
	}
	if got := dec.Checksum(); got != baseSum {
		failf("phase=%s worker=%d round=%d seed=%d kind=%s: roundtrip checksum=%d want=%d", phase, w.id, r, w.seed, kind, got, baseSum)
	}
	if dec.GetID() != rec.GetID() || dec.GetName() != rec.GetName() {
		failf("phase=%s worker=%d round=%d seed=%d kind=%s: roundtrip fields id=%d/%d name=%q/%q",
			phase, w.id, r, w.seed, kind, dec.GetID(), rec.GetID(), dec.GetName(), rec.GetName())
	}
	checkRecordPointers("roundtrip", w, r, kind, seed, dec)
	if got := Dispatch(op, dec); got != baseDispatch {
		failf("phase=%s worker=%d round=%d seed=%d kind=%s: roundtrip dispatch=%d want=%d", phase, w.id, r, w.seed, kind, got, baseDispatch)
	}
	rtags, dtags := rec.TagValues(), dec.TagValues()
	if len(rtags) != len(dtags) {
		failf("phase=%s worker=%d round=%d seed=%d kind=%s: roundtrip tag count=%d want=%d", phase, w.id, r, w.seed, kind, len(dtags), len(rtags))
	}
	for i := range rtags {
		if rtags[i] != dtags[i] {
			failf("phase=%s worker=%d round=%d seed=%d kind=%s: roundtrip tag[%d]=%q want=%q", phase, w.id, r, w.seed, kind, i, dtags[i], rtags[i])
		}
	}

	// 深拷贝独立性：副本共享任何指针字段即失败；改动原对象后副本校验和不变。
	cp := dec.DeepCopyRecord()
	if cp == nil || cp == dec {
		failf("phase=%s worker=%d round=%d seed=%d kind=%s: invalid deep copy", phase, w.id, r, w.seed, kind)
	}
	atomic.AddInt64(&copies, 1)
	if cp.Checksum() != dec.Checksum() {
		failf("phase=%s worker=%d round=%d seed=%d kind=%s: deep copy checksum mismatch", phase, w.id, r, w.seed, kind)
	}
	cps, ds := cp.Payloads(), dec.Payloads()
	if len(cps) != 3 || cps[0] == nil || len(cps[0].Children) != 1 || cps[0].Children[0] == nil ||
		len(ds) != 3 || ds[0] == nil || len(ds[0].Children) != 1 || ds[0].Children[0] == nil {
		failf("phase=%s worker=%d round=%d seed=%d kind=%s: deep copy payload shape corrupted", phase, w.id, r, w.seed, kind)
	}
	if cps[0] == ds[0] || cps[0].Children[0] == ds[0].Children[0] || cp.LeaderPayload() == dec.LeaderPayload() {
		failf("phase=%s worker=%d round=%d seed=%d kind=%s: deep copy shares pointer fields", phase, w.id, r, w.seed, kind)
	}
	cpSum := cp.Checksum()
	before := dec.Checksum()
	dec.MutatePayload(1_000_003)
	dec.Payloads()[0].Children[0].Value += 7
	dec.MutateTag("mutated")
	if dec.Checksum() == before {
		failf("phase=%s worker=%d round=%d seed=%d kind=%s: mutation did not change checksum", phase, w.id, r, w.seed, kind)
	}
	if cp.Checksum() != cpSum {
		failf("phase=%s worker=%d round=%d seed=%d kind=%s: deep copy changed with original", phase, w.id, r, w.seed, kind)
	}
	if cp.TagValues()[0] == "mutated" || cp.Payloads()[0].Children[0].Value == dec.Payloads()[0].Children[0].Value {
		failf("phase=%s worker=%d round=%d seed=%d kind=%s: deep copy is not independent", phase, w.id, r, w.seed, kind)
	}

	w.retained = append(w.retained, retained{rec: cp, sum: cpSum, kind: kind, seed: seed})
	if len(w.retained) > 3 {
		w.retained[0] = retained{}
		w.retained = w.retained[1:]
	}

	if phase == "shrink" {
		if len(w.retained) > 1 {
			for i := 0; i < len(w.retained)-1; i++ {
				w.retained[i] = retained{}
			}
			w.retained = w.retained[len(w.retained)-1:]
		}
		debug.FreeOSMemory()
		for _, rt := range w.retained {
			verifyRetained("shrink", w, r, rt)
		}
	}

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
	w.rounds = r
	for _, rt := range w.retained {
		verifyRetained("final", w, r, rt)
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
	workers := envInt("STRESS_WORKERS", 4, 1, 32)
	hb := envInt("STRESS_HEARTBEAT", 5, 1, 60)
	seed := envSeed(1)
	fixed := int64(envInt("STRESS_FIXED_ROUNDS", 32, 1, 100000))

	for k := RecordKind(0); k < RecordKindCount; k++ {
		if k.String() == "" || KindDescription(k) == "" {
			failf("phase=preflight seed=%d: enum/description table broken at kind=%d", seed, k)
		}
		if NewRecord(k, 1) == nil || NewEmptyRecord(k) == nil {
			failf("phase=preflight seed=%d: factory broken at kind=%d", seed, k)
		}
	}

	fmt.Printf("scenario=s05 phase=preflight go=%s goos=%s goarch=%s gomaxprocs=%d seed=%d seconds=%d workers=%d fixed_rounds=%d heartbeat=%ds\n",
		runtime.Version(), runtime.GOOS, runtime.GOARCH, runtime.GOMAXPROCS(0), seed, seconds, workers, fixed, hb)

	deadline := time.Now().Add(time.Duration(seconds) * time.Second)
	ws := make([]*worker, workers)
	var wg sync.WaitGroup
	stop := make(chan struct{})
	for i := 0; i < workers; i++ {
		w := &worker{id: int64(i), seed: seed*1000 + uint64(i) + 1}
		ws[i] = w
		wg.Add(1)
		go func() {
			defer wg.Done()
			w.run(deadline, seconds, fixed)
		}()
	}
	go watchdog(time.Duration(hb)*time.Second, stop)

	wg.Wait()
	close(stop)

	var total int64
	for _, w := range ws {
		total += w.rounds
	}
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	fmt.Printf("metrics: rounds=%d beats=%d gc_calls=%d dispatch=%d deepcopy=%d heap_alloc=%d heap_objects=%d num_gc=%d goroutines=%d\n",
		total, atomic.LoadInt64(&beats), atomic.LoadInt64(&gcCalls), atomic.LoadInt64(&dispatchCalls), atomic.LoadInt64(&copies),
		m.HeapAlloc, m.HeapObjects, m.NumGC, runtime.NumGoroutine())
	fmt.Println("STRESS PASS s05_generated_code_soak")
}
