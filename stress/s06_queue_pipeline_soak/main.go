// S06: 多级有界 channel 流水线长跑。
// producers -> in -> 2×stage1（校验/转换 + map 指针缓存）-> mid ->
// 2×stage2（interface{} 装箱/类型断言 + 深拷贝 + slice 缓存）-> out -> aggregator。
// 优雅关闭：producers 停 -> close(in) -> stage1 排空后 FreeOSMemory 并 close(mid)
// -> stage2 排空后 FreeOSMemory 并 close(out) -> aggregator 收齐后校验。
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
	headLen  = 4
	cacheCap = 256
	paceStep = 32
	maxSeq   = int64(1) << 30
)

const (
	tagIn = "q0"
	tagS1 = "q1"
	tagS2 = "q2"
)

type node struct {
	v    int64
	name string
	next *node
}

type msg struct {
	prod  int
	seq   int64
	head  *node
	tail  []*node
	label string
	sum   int64
}

type msgKey struct {
	prod int
	seq  int64
}

var (
	beats    int64
	gcCycles int64

	s1In, s1Out int64
	s2In, s2Out int64

	gSeed  int64
	labels = [...]string{"alpha", "beta", "gamma", "delta"}
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

func baseFor(prod int, seq int64) int64 {
	return gSeed*1_000_000_000 + int64(prod)*10_000_000 + seq*100
}

func expectLabel(prod int, seq int64) string {
	return labels[(prod+int(seq&3))&3]
}

func tailLen(seq int64) int {
	return 4 + int(seq%5)
}

func makeMsg(prod int, seq int64) *msg {
	base := baseFor(prod, seq)
	head := &node{v: base, name: tagIn}
	cur := head
	for i := 1; i < headLen; i++ {
		cur.next = &node{v: base + int64(i), name: tagIn}
		cur = cur.next
	}
	n := tailLen(seq)
	tail := make([]*node, n)
	sum := 4*base + 6
	for i := range tail {
		q := &node{v: base + 100 + int64(i), name: tagIn}
		tail[i] = q
		sum += q.v
	}
	return &msg{
		prod:  prod,
		seq:   seq,
		head:  head,
		tail:  tail,
		label: expectLabel(prod, seq),
		sum:   sum,
	}
}

// copyMsg 深拷贝一条消息，并把节点名改写成当前阶段标签。
func copyMsg(src *msg, tag string) *msg {
	base := baseFor(src.prod, src.seq)
	head := &node{v: base, name: tag}
	cur := head
	for i := 1; i < headLen; i++ {
		cur.next = &node{v: base + int64(i), name: tag}
		cur = cur.next
	}
	tail := make([]*node, len(src.tail))
	for i := range tail {
		tail[i] = &node{v: base + 100 + int64(i), name: tag}
	}
	return &msg{
		prod:  src.prod,
		seq:   src.seq,
		head:  head,
		tail:  tail,
		label: src.label,
		sum:   src.sum,
	}
}

func verifyMsg(phase string, who int, m *msg, tag string) {
	if m == nil {
		failf("phase=%s who=%d: nil message", phase, who)
	}
	if m.prod < 0 || m.seq < 0 || m.seq >= maxSeq {
		failf("phase=%s who=%d prod=%d seq=%d: invalid identity", phase, who, m.prod, m.seq)
	}
	if m.head == nil {
		failf("phase=%s who=%d prod=%d seq=%d: nil head", phase, who, m.prod, m.seq)
	}
	n := tailLen(m.seq)
	if len(m.tail) != n {
		failf("phase=%s who=%d prod=%d seq=%d: tail len=%d want=%d", phase, who, m.prod, m.seq, len(m.tail), n)
	}
	wantLabel := expectLabel(m.prod, m.seq)
	if m.label != wantLabel {
		failf("phase=%s who=%d prod=%d seq=%d: label=%q want=%q", phase, who, m.prod, m.seq, m.label, wantLabel)
	}
	base := baseFor(m.prod, m.seq)
	var sum int64
	cur := m.head
	for i := 0; i < headLen; i++ {
		if cur == nil || cur.v != base+int64(i) || cur.name != tag {
			failf("phase=%s who=%d prod=%d seq=%d: head[%d] corrupted", phase, who, m.prod, m.seq, i)
		}
		sum += cur.v
		cur = cur.next
	}
	if cur != nil {
		failf("phase=%s who=%d prod=%d seq=%d: head chain longer than %d", phase, who, m.prod, m.seq, headLen)
	}
	for i := 0; i < n; i++ {
		q := m.tail[i]
		if q == nil || q.v != base+100+int64(i) || q.name != tag {
			failf("phase=%s who=%d prod=%d seq=%d: tail[%d] corrupted", phase, who, m.prod, m.seq, i)
		}
		sum += q.v
	}
	if sum != m.sum {
		failf("phase=%s who=%d prod=%d seq=%d: checksum=%d want=%d", phase, who, m.prod, m.seq, sum, m.sum)
	}
}

// gate 让所有 producer 停在同一个每-producer 条数上：
// 固定模式直接 target=perProd 且 closed=true；定时模式由 pace 追赶最慢 producer。
type gate struct {
	mu     sync.Mutex
	cond   *sync.Cond
	target int64
	closed bool
}

func newGate(target int64, closed bool) *gate {
	g := &gate{target: target, closed: closed}
	g.cond = sync.NewCond(&g.mu)
	return g
}

type producer struct {
	id   int
	sent int64
}

func (p *producer) run(g *gate, counts []int64, in chan<- *msg) {
	var count int64
	for {
		g.mu.Lock()
		for !g.closed && count >= g.target {
			g.cond.Wait()
		}
		done := count >= g.target
		g.mu.Unlock()
		if done {
			break
		}
		in <- makeMsg(p.id, count)
		count++
		atomic.StoreInt64(&counts[p.id], count)
		atomic.AddInt64(&beats, 1)
	}
	p.sent = count
}

func pace(g *gate, deadline time.Time, counts []int64, wg *sync.WaitGroup) {
	defer wg.Done()
	t := time.NewTicker(time.Millisecond)
	defer t.Stop()
	for {
		if !time.Now().Before(deadline) {
			g.mu.Lock()
			g.closed = true
			g.cond.Broadcast()
			g.mu.Unlock()
			return
		}
		g.mu.Lock()
		min := int64(1) << 62
		for i := range counts {
			if v := atomic.LoadInt64(&counts[i]); v < min {
				min = v
			}
		}
		if min >= g.target {
			g.target += paceStep
			g.cond.Broadcast()
		}
		g.mu.Unlock()
		<-t.C
	}
}

type s1ent struct {
	n    *node
	want int64
}

type stage1 struct {
	id    int
	in    <-chan *msg
	out   chan<- *msg
	cache map[msgKey]*s1ent
	ring  []msgKey
	next  int
	proc  int64
}

func (s *stage1) run() {
	for m := range s.in {
		atomic.AddInt64(&s1In, 1)
		verifyMsg("stage1-in", s.id, m, tagIn)

		k := msgKey{prod: m.prod, seq: m.seq}
		if _, dup := s.cache[k]; dup {
			failf("phase=stage1-cache worker=%d prod=%d seq=%d: duplicate key", s.id, m.prod, m.seq)
		}
		if len(s.ring) < cacheCap {
			s.ring = append(s.ring, k)
		} else {
			delete(s.cache, s.ring[s.next])
			s.ring[s.next] = k
			s.next = (s.next + 1) % cacheCap
		}
		s.cache[k] = &s1ent{n: m.head, want: baseFor(m.prod, m.seq)}

		ent := s.cache[k]
		if ent == nil || ent.n == nil || ent.n.v != ent.want || ent.n.name != tagIn {
			failf("phase=stage1-cache worker=%d prod=%d seq=%d: fresh entry corrupted", s.id, m.prod, m.seq)
		}
		sk := s.ring[int(m.seq)%len(s.ring)]
		se := s.cache[sk]
		if se == nil || se.n == nil || se.n.v != se.want || se.n.name != tagIn {
			failf("phase=stage1-cache worker=%d prod=%d seq=%d: sampled entry corrupted", s.id, m.prod, m.seq)
		}

		out := copyMsg(m, tagS1)
		verifyMsg("stage1-out", s.id, out, tagS1)
		s.out <- out
		s.proc++
		atomic.AddInt64(&s1Out, 1)
		atomic.AddInt64(&beats, 1)
	}
}

type s2ent struct {
	base int64
	vals []int64
}

type stage2 struct {
	id    int
	in    <-chan *msg
	out   chan<- *msg
	cache map[msgKey]*s2ent
	ring  []msgKey
	next  int
	proc  int64
}

func (s *stage2) run() {
	for m := range s.in {
		atomic.AddInt64(&s2In, 1)
		verifyMsg("stage2-in", s.id, m, tagS1)

		// interface{} 装箱 + 类型断言。
		var boxed interface{} = m
		bm, ok := boxed.(*msg)
		if !ok || bm != m {
			failf("phase=stage2-box worker=%d prod=%d seq=%d: *msg assertion failed", s.id, m.prod, m.seq)
		}
		base := baseFor(m.prod, m.seq)
		var hb interface{} = m.head
		h, ok := hb.(*node)
		if !ok || h == nil || h.v != base || h.name != tagS1 {
			failf("phase=stage2-box worker=%d prod=%d seq=%d: *node assertion failed", s.id, m.prod, m.seq)
		}
		if _, bad := hb.([]*node); bad {
			failf("phase=stage2-box worker=%d prod=%d seq=%d: unexpected []*node assertion success", s.id, m.prod, m.seq)
		}
		var tb interface{} = m.tail
		t, ok := tb.([]*node)
		if !ok || len(t) != len(m.tail) || len(t) == 0 || t[0] != m.tail[0] || t[len(t)-1] != m.tail[len(t)-1] {
			failf("phase=stage2-box worker=%d prod=%d seq=%d: []*node assertion failed", s.id, m.prod, m.seq)
		}
		var eb interface{} = t[len(t)-1]
		en, ok := eb.(*node)
		if !ok || en == nil || en.v != base+100+int64(len(t)-1) || en.name != tagS1 {
			failf("phase=stage2-box worker=%d prod=%d seq=%d: tail element assertion failed", s.id, m.prod, m.seq)
		}

		cp := copyMsg(m, tagS2)
		verifyMsg("stage2-postcopy", s.id, m, tagS1)
		verifyMsg("stage2-out", s.id, cp, tagS2)

		k := msgKey{prod: cp.prod, seq: cp.seq}
		if _, dup := s.cache[k]; dup {
			failf("phase=stage2-cache worker=%d prod=%d seq=%d: duplicate key", s.id, cp.prod, cp.seq)
		}
		vals := make([]int64, len(cp.tail))
		for i, q := range cp.tail {
			vals[i] = q.v
		}
		if len(s.ring) < cacheCap {
			s.ring = append(s.ring, k)
		} else {
			delete(s.cache, s.ring[s.next])
			s.ring[s.next] = k
			s.next = (s.next + 1) % cacheCap
		}
		s.cache[k] = &s2ent{base: base, vals: vals}

		ent := s.cache[k]
		if ent == nil || len(ent.vals) != len(cp.tail) {
			failf("phase=stage2-cache worker=%d prod=%d seq=%d: fresh entry corrupted", s.id, cp.prod, cp.seq)
		}
		for i := range ent.vals {
			if ent.vals[i] != base+100+int64(i) {
				failf("phase=stage2-cache worker=%d prod=%d seq=%d: vals[%d] corrupted", s.id, cp.prod, cp.seq, i)
			}
		}
		sk := s.ring[int(cp.seq)%len(s.ring)]
		se := s.cache[sk]
		if se == nil || len(se.vals) != tailLen(sk.seq) {
			failf("phase=stage2-cache worker=%d prod=%d seq=%d: sampled entry missing", s.id, cp.prod, cp.seq)
		}
		if se.vals[0] != se.base+100 || se.vals[len(se.vals)-1] != se.base+100+int64(len(se.vals)-1) {
			failf("phase=stage2-cache worker=%d prod=%d seq=%d: sampled entry corrupted", s.id, cp.prod, cp.seq)
		}

		s.out <- cp
		s.proc++
		atomic.AddInt64(&s2Out, 1)
		atomic.AddInt64(&beats, 1)
	}
}

type seqBits struct {
	words []uint64
	n     int64
	max   int64
}

func newSeqBits() *seqBits {
	return &seqBits{max: -1}
}

func (b *seqBits) add(prod int, seq int64) {
	if seq < 0 || seq >= maxSeq {
		failf("phase=aggregator prod=%d: seq=%d out of range", prod, seq)
	}
	idx := seq >> 6
	if idx >= int64(len(b.words)) {
		grown := make([]uint64, idx+1)
		copy(grown, b.words)
		b.words = grown
	}
	mask := uint64(1) << uint(seq&63)
	if b.words[idx]&mask != 0 {
		failf("phase=aggregator prod=%d seq=%d: duplicate message", prod, seq)
	}
	b.words[idx] |= mask
	b.n++
	if seq > b.max {
		b.max = seq
	}
}

func (b *seqBits) verify(prod int, want int64) {
	if b.n != want {
		failf("phase=aggregator prod=%d: received=%d producer_sent=%d", prod, b.n, want)
	}
	if want == 0 {
		if b.max != -1 {
			failf("phase=aggregator prod=%d: empty producer has max=%d", prod, b.max)
		}
		return
	}
	if b.max != want-1 {
		failf("phase=aggregator prod=%d: sequence gap/out-of-range: max=%d count=%d", prod, b.max, want)
	}
}

type aggregator struct {
	producers int
	bits      []*seqBits
	recv      int64
}

func (a *aggregator) run(out <-chan *msg) {
	a.bits = make([]*seqBits, a.producers)
	for i := range a.bits {
		a.bits[i] = newSeqBits()
	}
	for m := range out {
		if m == nil || m.prod < 0 || m.prod >= a.producers {
			failf("phase=aggregator: invalid producer identity")
		}
		verifyMsg("aggregator", m.prod, m, tagS2)
		a.bits[m.prod].add(m.prod, m.seq)
		a.recv++
		atomic.AddInt64(&beats, 1)
	}
}

func (a *aggregator) verify(started []int64, perProd int64, fixed bool) {
	var total int64
	for i, c := range started {
		total += c
		a.bits[i].verify(i, c)
	}
	if a.recv != total {
		failf("phase=final: aggregator received=%d producers started=%d", a.recv, total)
	}
	for _, n := range []struct {
		name string
		v    int64
	}{
		{"stage1_in", atomic.LoadInt64(&s1In)},
		{"stage1_out", atomic.LoadInt64(&s1Out)},
		{"stage2_in", atomic.LoadInt64(&s2In)},
		{"stage2_out", atomic.LoadInt64(&s2Out)},
	} {
		if n.v != total {
			failf("phase=final: %s=%d want=%d", n.name, n.v, total)
		}
	}
	min, max := started[0], started[0]
	for _, c := range started {
		if c < min {
			min = c
		}
		if c > max {
			max = c
		}
	}
	if min != max {
		failf("phase=final: per-producer counts differ: %v", started)
	}
	if fixed && min != perProd {
		failf("phase=final: per-producer count=%d want=%d", min, perProd)
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
				failf("watchdog stalled: no pipeline progress for %s (beats=%d)", time.Since(lastChange), cur)
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
	workers := envInt("STRESS_WORKERS", 8, 1, 32)
	hb := envInt("STRESS_HEARTBEAT", 5, 1, 60)
	seed := envSeed(1)
	fixedTotal := int64(envInt("STRESS_FIXED_ROUNDS", 512, 1, 100000000))
	producers := workers / 2
	if producers < 2 {
		producers = 2
	}

	fixed := seconds == 0
	perProd := int64(0)
	var total int64
	if fixed {
		perProd = (fixedTotal + int64(producers) - 1) / int64(producers)
		total = perProd * int64(producers)
	}
	gSeed = int64(seed%1_000_000) * 1_000_000_000

	mode := "timed"
	if fixed {
		mode = "fixed"
	}
	fmt.Printf("scenario=s06 phase=preflight go=%s goos=%s goarch=%s gomaxprocs=%d seed=%d mode=%s seconds=%d workers=%d producers=%d stage1=2 stage2=2 heartbeat=%ds total_messages=%d\n",
		runtime.Version(), runtime.GOOS, runtime.GOARCH, runtime.GOMAXPROCS(0), seed, mode, seconds, workers, producers, hb, total)

	in := make(chan *msg, producers*4)
	mid := make(chan *msg, 16)
	out := make(chan *msg, 16)
	g := newGate(perProd, fixed)
	counts := make([]int64, producers)
	stop := make(chan struct{})
	deadline := time.Now().Add(time.Duration(seconds) * time.Second)

	var prodWG, s1WG, s2WG, closeWG, auxWG sync.WaitGroup

	ps := make([]*producer, producers)
	for i := 0; i < producers; i++ {
		p := &producer{id: i}
		ps[i] = p
		prodWG.Add(1)
		go func() {
			defer prodWG.Done()
			p.run(g, counts, in)
		}()
	}

	s1s := make([]*stage1, 2)
	for i := range s1s {
		s := &stage1{id: i, in: in, out: mid, cache: make(map[msgKey]*s1ent)}
		s1s[i] = s
		s1WG.Add(1)
		go func() {
			defer s1WG.Done()
			s.run()
		}()
	}

	s2s := make([]*stage2, 2)
	for i := range s2s {
		s := &stage2{id: i, in: mid, out: out, cache: make(map[msgKey]*s2ent)}
		s2s[i] = s
		s2WG.Add(1)
		go func() {
			defer s2WG.Done()
			s.run()
		}()
	}

	closeWG.Add(3)
	go func() {
		defer closeWG.Done()
		prodWG.Wait()
		close(in)
	}()
	go func() {
		defer closeWG.Done()
		s1WG.Wait()
		debug.FreeOSMemory()
		close(mid)
	}()
	go func() {
		defer closeWG.Done()
		s2WG.Wait()
		debug.FreeOSMemory()
		close(out)
	}()

	auxWG.Add(1)
	go gcChurn(stop, &auxWG)
	auxWG.Add(1)
	go watchdog(time.Duration(hb)*time.Second, stop, &auxWG)
	if !fixed {
		auxWG.Add(1)
		go pace(g, deadline, counts, &auxWG)
	}

	agg := &aggregator{producers: producers}
	agg.run(out)
	closeWG.Wait()
	close(stop)
	auxWG.Wait()

	started := make([]int64, producers)
	for i := range started {
		started[i] = atomic.LoadInt64(&counts[i])
	}
	agg.verify(started, perProd, fixed)

	var s1cache, s2cache int
	for _, s := range s1s {
		s1cache += len(s.cache)
	}
	for _, s := range s2s {
		s2cache += len(s.cache)
	}
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	fmt.Printf("metrics: messages=%d per_producer=%v producers=%d stage1_cache=%d stage2_cache=%d gc_churn=%d beats=%d heap_alloc=%d heap_objects=%d num_gc=%d goroutines=%d\n",
		agg.recv, started, producers, s1cache, s2cache, atomic.LoadInt64(&gcCycles), atomic.LoadInt64(&beats),
		m.HeapAlloc, m.HeapObjects, m.NumGC, runtime.NumGoroutine())
	fmt.Println("STRESS PASS s06_queue_pipeline_soak")
}
