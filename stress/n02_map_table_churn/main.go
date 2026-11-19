// N02: map 表翻腾长跑。
// 分片 map[int64]*node / map[string][]*node 并发写读；遍历中删除、重插、clear；
// 全局 sync.Map Load/Store/Range；reflect MapKeys/MapIndex 抽查；
// NaN key / struct key 边界；遍历点插入 runtime.GC。
// 每个分片只由 owner 写，其他 worker 用 RLock 并发读；结束时按模型校验 key 集完整性。
package main

import (
	"fmt"
	"math"
	"os"
	"reflect"
	"runtime"
	"runtime/debug"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

const (
	intKeySpace = 48
	strKeySpace = 24
	shardMaps   = 2
)

type node struct {
	key  int64
	idx  int64
	v    int64
	name string
	tail *node
}

type shard struct {
	mu sync.RWMutex
	im map[int64]*node
	sm map[string][]*node
}

type worker struct {
	id     int64
	seed   uint64
	next   int64
	own    []int
	modelI map[int]map[int64]int64
	modelS map[int]map[string]int
	rounds int64
}

var (
	shards     []*shard
	numShards  int
	runWorkers int
	gSeed      uint64
	beats      int64
	gcCycles   int64
	probes     int64
	reflectOps int64
	specialOps int64
	syncOps    int64
	started    int64
	finished   int64
	smap       sync.Map
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

func hashStr(s string) uint32 {
	var h uint32 = 2166136261
	for i := 0; i < len(s); i++ {
		h ^= uint32(s[i])
		h *= 16777619
	}
	return h
}

func nodeV(key, idx int64) int64 {
	return key*13 + 5 + idx
}

func makeNode(key, idx int64, name string) *node {
	v := nodeV(key, idx)
	return &node{
		key:  key,
		idx:  idx,
		v:    v,
		name: name,
		tail: &node{key: key, idx: idx, v: v + 1, name: name},
	}
}

// checkNode 是所有 node 的通用内容不变量（不看 key 来源）。
func checkNode(phase string, n *node) {
	if n == nil {
		failf("phase=%s seed=%d: nil node", phase, gSeed)
	}
	if n.v != nodeV(n.key, n.idx) {
		failf("phase=%s seed=%d: node v=%d want=%d key=%d idx=%d", phase, gSeed, n.v, nodeV(n.key, n.idx), n.key, n.idx)
	}
	if n.tail == nil || n.tail.key != n.key || n.tail.idx != n.idx || n.tail.v != n.v+1 || n.tail.name != n.name {
		failf("phase=%s seed=%d: node tail corrupted key=%d idx=%d", phase, gSeed, n.key, n.idx)
	}
}

func checkINode(k int64, n *node) {
	if n == nil || n.key != k || n.idx != 0 || n.name != "n02i" {
		failf("phase=check-inode seed=%d: key=%d node identity corrupted", gSeed, k)
	}
	checkNode("check-inode", n)
}

func strLen(key string) int {
	return 1 + int(hashStr(key)%4)
}

func makeStrNodes(key string) []*node {
	h := int64(hashStr(key))
	arr := make([]*node, strLen(key))
	for i := range arr {
		arr[i] = makeNode(h, int64(i), key)
	}
	return arr
}

func checkStrEntry(key string, arr []*node) {
	h := int64(hashStr(key))
	if len(arr) != strLen(key) {
		failf("phase=check-strentry seed=%d: key=%q len=%d want=%d", gSeed, key, len(arr), strLen(key))
	}
	for i, n := range arr {
		if n == nil || n.key != h || n.idx != int64(i) || n.name != key {
			failf("phase=check-strentry seed=%d: key=%q node[%d] identity corrupted", gSeed, key, i)
		}
		checkNode("check-strentry", n)
	}
}

func newSyncNode(key string) *node {
	return makeNode(int64(hashStr(key)), 0, key)
}

func checkSyncNode(key string, n *node) {
	h := int64(hashStr(key))
	if n == nil || n.key != h || n.idx != 0 || n.name != key {
		failf("phase=check-syncmap seed=%d: key=%q node identity corrupted", gSeed, key)
	}
	checkNode("check-syncmap", n)
}

func (w *worker) ikey(seq int) int64 {
	return w.id*1_000_000 + int64(seq)
}

func (w *worker) skey(seq int) string {
	return fmt.Sprintf("%d-%02d", w.id, seq)
}

// mutate 只由 owner 调用；持写锁完成删除/重插/遍历删除/抽样/clear。
func (w *worker) mutate(si int, r int64) {
	sh := shards[si]
	mi := w.modelI[si]
	ms := w.modelS[si]

	sh.mu.Lock()

	for j := 0; j < 3; j++ {
		seq := (int(r)*5 + j*7 + 1) % intKeySpace
		k := w.ikey(seq)
		if _, ok := sh.im[k]; ok {
			delete(sh.im, k)
			delete(mi, k)
		}
	}
	for j := 0; j < 4; j++ {
		seq := (int(r)*7 + j*11 + 2) % intKeySpace
		k := w.ikey(seq)
		n := makeNode(k, 0, "n02i")
		checkINode(k, n)
		sh.im[k] = n
		mi[k] = n.v
	}
	for j := 0; j < 2; j++ {
		seq := (int(r)*3 + j*5 + 3) % strKeySpace
		key := w.skey(seq)
		if _, ok := sh.sm[key]; ok {
			delete(sh.sm, key)
			delete(ms, key)
		}
	}
	for j := 0; j < 3; j++ {
		seq := (int(r)*11 + j*13 + 4) % strKeySpace
		key := w.skey(seq)
		arr := makeStrNodes(key)
		checkStrEntry(key, arr)
		sh.sm[key] = arr
		ms[key] = len(arr)
	}

	// 遍历中删除并校验存活项。
	for k, n := range sh.im {
		if k%11 == 0 {
			delete(sh.im, k)
			delete(mi, k)
			continue
		}
		checkINode(k, n)
	}
	for key, arr := range sh.sm {
		if hashStr(key)%11 == 0 {
			delete(sh.sm, key)
			delete(ms, key)
			continue
		}
		checkStrEntry(key, arr)
	}

	// 模型抽样。
	sampled := 0
	for k, want := range mi {
		n, ok := sh.im[k]
		if !ok || n.v != want || n.name != "n02i" || n.idx != 0 {
			failf("phase=mutate seed=%d shard=%d key=%d: model entry missing/corrupted", gSeed, si, k)
		}
		checkNode("mutate-sample", n)
		sampled++
		if sampled >= 3 {
			break
		}
	}
	sampled = 0
	for key, want := range ms {
		arr, ok := sh.sm[key]
		if !ok || len(arr) != want {
			failf("phase=mutate seed=%d shard=%d key=%q: model entry missing/corrupted", gSeed, si, key)
		}
		checkStrEntry(key, arr)
		sampled++
		if sampled >= 3 {
			break
		}
	}

	// clear：模型同步重置。
	if r%13 == 12 {
		clear(sh.im)
		clear(sh.sm)
		w.modelI[si] = make(map[int64]int64, intKeySpace)
		w.modelS[si] = make(map[string]int, strKeySpace)
	}
	sh.mu.Unlock()
}

// readOther 用 RLock 并发读别人的分片；present 项必须内容正确，absent 合法。
func (w *worker) readOther(r int64) {
	if numShards < 2 || len(w.own) == 0 {
		return
	}
	si := (w.own[0] + 1) % numShards
	sh := shards[si]
	owner := si % runWorkers

	sh.mu.RLock()
	seq := (int(r)*9 + si + 1) % intKeySpace
	k := int64(owner)*1_000_000 + int64(seq)
	if n, ok := sh.im[k]; ok {
		checkINode(k, n)
		atomic.AddInt64(&probes, 1)
	}
	if (r+int64(si))%3 == 0 {
		i := 0
		for kk, nn := range sh.im {
			checkINode(kk, nn)
			i++
			if i >= 2 {
				break
			}
		}
	}
	sk := fmt.Sprintf("%d-%02d", owner, (int(r)*7+si+2)%strKeySpace)
	if arr, ok := sh.sm[sk]; ok {
		checkStrEntry(sk, arr)
		atomic.AddInt64(&probes, 1)
	}
	sh.mu.RUnlock()
}

// syncMapRound 在共享 sync.Map 上与所有 worker 并发 Store/Load/Range。
func (w *worker) syncMapRound(r int64) {
	key := fmt.Sprintf("sm-%d-%02d", w.id, int(r)%32)
	n := newSyncNode(key)
	smap.Store(key, n)
	got, ok := smap.Load(key)
	if !ok || got == nil {
		failf("phase=syncmap-load seed=%d key=%q: load failed after store", gSeed, key)
	}
	if gn, ok := got.(*node); !ok || gn != n {
		failf("phase=syncmap-load seed=%d key=%q: stored pointer identity lost", gSeed, key)
	}
	cnt := 0
	smap.Range(func(k, v interface{}) bool {
		ks, ok := k.(string)
		if !ok {
			failf("phase=syncmap-range seed=%d: key type=%T", gSeed, k)
		}
		nn, ok := v.(*node)
		if !ok || nn == nil {
			failf("phase=syncmap-range seed=%d key=%q: value type=%T", gSeed, ks, v)
		}
		checkSyncNode(ks, nn)
		cnt++
		return true
	})
	if cnt < 1 {
		failf("phase=syncmap-range seed=%d: empty range after store", gSeed)
	}
	if int(r)%5 == 4 {
		smap.Delete(key)
		if _, ok := smap.Load(key); ok {
			failf("phase=syncmap-delete seed=%d key=%q: still present after delete", gSeed, key)
		}
	}
	atomic.AddInt64(&syncOps, 1)
}

// reflectRound 持锁用 reflect 抽查 map[int64]*node 与 map[string][]*node；
// 每 4 轮把抽到的指针带出锁外，插入 GC 后复核内容。
func (w *worker) reflectRound(r int64) {
	if len(w.own) == 0 {
		return
	}
	si := w.own[0]
	sh := shards[si]

	sh.mu.Lock()
	held := make([]*node, 0, 8)
	rv := reflect.ValueOf(sh.im)
	keys := rv.MapKeys()
	if len(keys) > 4 {
		keys = keys[:4]
	}
	for _, kv := range keys {
		if kv.Kind() != reflect.Int64 {
			failf("phase=reflect seed=%d: int map key kind=%v", gSeed, kv.Kind())
		}
		k := kv.Int()
		val := rv.MapIndex(kv)
		if !val.IsValid() {
			failf("phase=reflect seed=%d: MapIndex(%d) invalid", gSeed, k)
		}
		n, ok := val.Interface().(*node)
		if !ok || n == nil {
			failf("phase=reflect seed=%d: MapIndex(%d) type=%T", gSeed, k, val.Interface())
		}
		checkINode(k, n)
		held = append(held, n)
	}
	rv2 := reflect.ValueOf(sh.sm)
	keys2 := rv2.MapKeys()
	if len(keys2) > 2 {
		keys2 = keys2[:2]
	}
	for _, kv := range keys2 {
		key := kv.String()
		val := rv2.MapIndex(kv)
		if !val.IsValid() {
			failf("phase=reflect seed=%d: MapIndex(%q) invalid", gSeed, key)
		}
		arr, ok := val.Interface().([]*node)
		if !ok || len(arr) == 0 {
			failf("phase=reflect seed=%d: MapIndex(%q) slice invalid", gSeed, key)
		}
		checkStrEntry(key, arr)
		held = append(held, arr[0])
	}
	sh.mu.Unlock()

	if r%4 == 0 {
		runtime.GC()
		for _, n := range held {
			checkNode("post-gc", n)
		}
	}
	atomic.AddInt64(&reflectOps, 1)
}

// specialRound 覆盖 NaN key 与 struct key 边界。
func specialRound() {
	nm := make(map[float64]*node, 8)
	for i := 0; i < 4; i++ {
		nm[math.NaN()] = makeNode(int64(i), 0, "n02nan")
	}
	if len(nm) != 4 {
		failf("phase=nan seed=%d: len(NaN map)=%d want=4", gSeed, len(nm))
	}
	if _, ok := nm[math.NaN()]; ok {
		failf("phase=nan seed=%d: NaN lookup unexpectedly succeeded", gSeed)
	}
	cnt := 0
	for k, v := range nm {
		if !math.IsNaN(k) {
			failf("phase=nan seed=%d: key %v not NaN", gSeed, k)
		}
		checkNode("nan", v)
		cnt++
	}
	if cnt != 4 {
		failf("phase=nan seed=%d: iterated=%d want=4", gSeed, cnt)
	}
	clear(nm)
	if len(nm) != 0 {
		failf("phase=nan seed=%d: clear left %d entries", gSeed, len(nm))
	}

	type skey struct {
		a int64
		b string
		c [2]uint32
	}
	sm := make(map[skey]*node, 8)
	for i := 0; i < 4; i++ {
		k := skey{int64(i), fmt.Sprintf("k%d", i), [2]uint32{uint32(i), uint32(i + 1)}}
		sm[k] = makeNode(int64(i), 0, "n02sk")
	}
	if len(sm) != 4 {
		failf("phase=structkey seed=%d: len=%d want=4", gSeed, len(sm))
	}
	for i := 0; i < 4; i++ {
		k := skey{int64(i), fmt.Sprintf("k%d", i), [2]uint32{uint32(i), uint32(i + 1)}}
		n, ok := sm[k]
		if !ok {
			failf("phase=structkey seed=%d: lookup %d failed", gSeed, i)
		}
		checkNode("structkey", n)
	}
	k0 := skey{0, "k0", [2]uint32{0, 1}}
	sm[k0] = makeNode(99, 0, "n02sk")
	if len(sm) != 4 {
		failf("phase=structkey seed=%d: overwrite grew len=%d", gSeed, len(sm))
	}
	if n, ok := sm[k0]; !ok || n.key != 99 {
		failf("phase=structkey seed=%d: overwrite lost", gSeed)
	}
	delete(sm, k0)
	if len(sm) != 3 {
		failf("phase=structkey seed=%d: delete left len=%d", gSeed, len(sm))
	}
	clear(sm)
	if len(sm) != 0 {
		failf("phase=structkey seed=%d: clear left %d entries", gSeed, len(sm))
	}
	atomic.AddInt64(&specialOps, 1)
}

func (w *worker) round(r int64) {
	for _, si := range w.own {
		w.mutate(si, r)
	}
	w.readOther(r)
	w.syncMapRound(r)
	w.reflectRound(r)
	if r%4 == 0 {
		specialRound()
	}
	if r%8 == 0 {
		runtime.GC()
	}
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
}

// verifyFinal 在 owner 写路径结束后按模型精确校验 key 集与值。
func (w *worker) verifyFinal() {
	for _, si := range w.own {
		sh := shards[si]
		mi := w.modelI[si]
		ms := w.modelS[si]
		sh.mu.RLock()
		if len(sh.im) != len(mi) {
			failf("phase=final seed=%d worker=%d shard=%d: int map len=%d model=%d", gSeed, w.id, si, len(sh.im), len(mi))
		}
		for k, want := range mi {
			n, ok := sh.im[k]
			if !ok || n.v != want {
				failf("phase=final seed=%d worker=%d shard=%d key=%d: missing/model value mismatch", gSeed, w.id, si, k)
			}
			checkINode(k, n)
		}
		for k, n := range sh.im {
			if _, ok := mi[k]; !ok {
				failf("phase=final seed=%d worker=%d shard=%d key=%d: unexpected key", gSeed, w.id, si, k)
			}
			checkINode(k, n)
		}
		if len(sh.sm) != len(ms) {
			failf("phase=final seed=%d worker=%d shard=%d: string map len=%d model=%d", gSeed, w.id, si, len(sh.sm), len(ms))
		}
		for key, want := range ms {
			arr, ok := sh.sm[key]
			if !ok || len(arr) != want {
				failf("phase=final seed=%d worker=%d shard=%d key=%q: missing/model len mismatch", gSeed, w.id, si, key)
			}
			checkStrEntry(key, arr)
		}
		for key, arr := range sh.sm {
			if _, ok := ms[key]; !ok {
				failf("phase=final seed=%d worker=%d shard=%d key=%q: unexpected key", gSeed, w.id, si, key)
			}
			checkStrEntry(key, arr)
		}
		sh.mu.RUnlock()
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
	runWorkers = envInt("STRESS_WORKERS", 4, 1, 32)
	hb := envInt("STRESS_HEARTBEAT", 5, 1, 60)
	seed := envSeed(1)
	fixed := int64(envInt("STRESS_FIXED_ROUNDS", 32, 1, 1000000))
	gSeed = seed

	if shardMaps != 2 {
		failf("phase=preflight: shardMaps constant changed")
	}
	numShards = runWorkers * 2
	if numShards < 8 {
		numShards = 8
	}
	if numShards > 64 {
		numShards = 64
	}
	shards = make([]*shard, numShards)
	for i := range shards {
		shards[i] = &shard{
			im: make(map[int64]*node, intKeySpace),
			sm: make(map[string][]*node, strKeySpace),
		}
	}

	fmt.Printf("scenario=n02 phase=preflight go=%s goos=%s goarch=%s gomaxprocs=%d seed=%d seconds=%d workers=%d shards=%d fixed_rounds=%d heartbeat=%ds\n",
		runtime.Version(), runtime.GOOS, runtime.GOARCH, runtime.GOMAXPROCS(0), seed, seconds, runWorkers, numShards, fixed, hb)

	baseGoroutines := runtime.NumGoroutine()
	deadline := time.Now().Add(time.Duration(seconds) * time.Second)
	ws := make([]*worker, runWorkers)
	for i := 0; i < runWorkers; i++ {
		w := &worker{
			id:     int64(i),
			seed:   seed*1000 + uint64(i) + 1,
			modelI: make(map[int]map[int64]int64),
			modelS: make(map[int]map[string]int),
		}
		for si := 0; si < numShards; si++ {
			if si%runWorkers == i {
				w.own = append(w.own, si)
				w.modelI[si] = make(map[int64]int64, intKeySpace)
				w.modelS[si] = make(map[string]int, strKeySpace)
			}
		}
		ws[i] = w
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

	if s, f := atomic.LoadInt64(&started), atomic.LoadInt64(&finished); s != int64(runWorkers) || f != s {
		failf("phase=final seed=%d: started=%d finished=%d workers=%d", seed, s, f, runWorkers)
	}

	var roundsTotal, intEntries, strEntries int64
	for _, w := range ws {
		roundsTotal += w.rounds
		if seconds == 0 && w.next != fixed {
			failf("phase=final seed=%d worker=%d: rounds=%d want=%d", seed, w.id, w.next, fixed)
		}
		w.verifyFinal()
		for _, si := range w.own {
			intEntries += int64(len(shards[si].im))
			strEntries += int64(len(shards[si].sm))
		}
	}
	if roundsTotal == 0 {
		failf("phase=final seed=%d: no rounds completed", seed)
	}

	syncCount := 0
	smap.Range(func(k, v interface{}) bool {
		ks, ok := k.(string)
		if !ok {
			failf("phase=final seed=%d: sync.Map key type=%T", seed, k)
		}
		nn, ok := v.(*node)
		if !ok || nn == nil {
			failf("phase=final seed=%d: sync.Map value type=%T", seed, v)
		}
		checkSyncNode(ks, nn)
		syncCount++
		return true
	})
	if syncCount == 0 {
		failf("phase=final seed=%d: sync.Map empty", seed)
	}

	ng := waitGoroutines(baseGoroutines, 5*time.Second)
	if ng > baseGoroutines+4 {
		failf("phase=final seed=%d: goroutines baseline=%d after=%d: goroutine leak", seed, baseGoroutines, ng)
	}

	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	fmt.Printf("metrics: rounds=%d shards=%d int_entries=%d str_entries=%d probes=%d reflect_ops=%d special_ops=%d sync_ops=%d sync_entries=%d gc_churn=%d heap_alloc=%d heap_objects=%d num_gc=%d goroutines=%d\n",
		roundsTotal, numShards, intEntries, strEntries, atomic.LoadInt64(&probes),
		atomic.LoadInt64(&reflectOps), atomic.LoadInt64(&specialOps), atomic.LoadInt64(&syncOps), syncCount,
		atomic.LoadInt64(&gcCycles), m.HeapAlloc, m.HeapObjects, m.NumGC, ng)
	fmt.Printf("STRESS PASS %s\n", "n02_map_table_churn")
}
