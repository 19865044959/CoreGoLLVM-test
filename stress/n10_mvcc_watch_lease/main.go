// N10: 纯内存 revisioned KV + watcher + lease 长跑。
// 随机 op 生成器（seed 派生）在多 worker 并发执行 put/delete/get/range/lease/watch/compact：
//   - watcher 支持 prefix/range 过滤与 start-rev（可回放历史），每个事件按 rev 严格递增投递；
//   - lease grant/renew/revoke/expire（虚拟时钟推进，非真实 sleep 驱动到期）；
//   - compaction 只在所有活跃 watcher 仍需要的最老 rev 之下裁剪历史。
//
// 模型检查：每个 watcher 取消时，其收到的事件集必须与历史重放逐条一致（不丢/不重/有序）；
// 数据侧持续校验 lease 附着关系与值 revision；结束做一次全量 drain + 不变量检查。
package main

import (
	"fmt"
	"os"
	"runtime"
	"runtime/debug"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const scenario = "n10"

const (
	keyShards    = 8
	keysPerShard = 32
	opsPerRound  = 16
	maxWatches   = 6
	maxLeases    = 4
)

var (
	beats          int64
	gcCycles       int64
	ops            int64
	puts           int64
	deletes        int64
	gets           int64
	scans          int64
	watchesCreated int64
	watchesChecked int64
	watcherEvents  int64
	leaseGrants    int64
	leaseRenews    int64
	leaseRevokes   int64
	leaseExpires   int64
	leaseErrors    int64
	compacted      int64
	compactions    int64
	startClamped   int64
	checks         int64

	keyspace []string
)

func initKeyspace() {
	for s := 0; s < keyShards; s++ {
		for n := 0; n < keysPerShard; n++ {
			keyspace = append(keyspace, fmt.Sprintf("%c/%02d", 'a'+s, n))
		}
	}
}

type event struct {
	rev   int64
	key   string
	val   string
	del   bool
	lease int64
}

type filter struct {
	prefix  string
	lo, hi  string
	isRange bool
}

func (f filter) match(key string) bool {
	if f.isRange {
		return key >= f.lo && key < f.hi
	}
	return strings.HasPrefix(key, f.prefix)
}

func (f filter) String() string {
	if f.isRange {
		return fmt.Sprintf("range[%s,%s)", f.lo, f.hi)
	}
	return "prefix[" + f.prefix + "]"
}

type watcher struct {
	id       int64
	f        filter
	startRev int64
	nextRev  int64 // 仅 store.mu 下修改

	mu     sync.Mutex
	q      []event
	closed bool
	notify chan struct{}
	done   chan struct{}

	// 仅 consumer goroutine 使用。
	lastRev int64
	got     []event
	bad     string

	// 注册与 emit 时在 store.mu 下同步累积，done 关闭后由 worker 读取。
	expected []event
}

func (w *watcher) signal() {
	select {
	case w.notify <- struct{}{}:
	default:
	}
}

func (w *watcher) consume() {
	defer close(w.done)
	for {
		w.mu.Lock()
		if len(w.q) == 0 {
			if w.closed {
				w.mu.Unlock()
				return
			}
			w.mu.Unlock()
			<-w.notify
			continue
		}
		ev := w.q[0]
		w.q[0] = event{}
		w.q = w.q[1:]
		w.mu.Unlock()

		if !w.f.match(ev.key) {
			w.bad = fmt.Sprintf("delivered non-matching key=%s filter=%s", ev.key, w.f)
			return
		}
		if ev.rev <= w.lastRev {
			w.bad = fmt.Sprintf("event rev not increasing: last=%d got=%d key=%s", w.lastRev, ev.rev, ev.key)
			return
		}
		w.lastRev = ev.rev
		w.got = append(w.got, ev)
		atomic.AddInt64(&watcherEvents, 1)
	}
}

type value struct {
	rev   int64
	val   string
	lease int64
}

type leaseRec struct {
	id       int64
	ttl      int64
	expireAt int64 // 虚拟时钟
	keys     map[string]struct{}
}

type store struct {
	mu         sync.Mutex
	rev        int64
	compactRev int64
	clock      int64
	data       map[string]*value
	history    []event
	watchers   map[int64]*watcher
	leases     map[int64]*leaseRec
	nextWatch  int64
	nextLease  int64
}

func newStore() *store {
	return &store{
		data:     make(map[string]*value),
		watchers: make(map[int64]*watcher),
		leases:   make(map[int64]*leaseRec),
	}
}

var errLease = fmt.Errorf("lease not found or expired")

func (s *store) revs() (rev, compactRev int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.rev, s.compactRev
}

func (s *store) emitLocked(ev event) {
	s.history = append(s.history, ev)
	for _, w := range s.watchers {
		if ev.rev >= w.startRev && w.f.match(ev.key) {
			w.expected = append(w.expected, ev)
			w.mu.Lock()
			w.q = append(w.q, ev)
			w.mu.Unlock()
			if w.nextRev <= ev.rev {
				w.nextRev = ev.rev + 1
			}
			w.signal()
		}
	}
}

// watch 注册 watcher；startRev 被压到 [compactRev+1, rev+1]，并回放历史中匹配的事件。
func (s *store) watch(f filter, startRev int64) *watcher {
	s.mu.Lock()
	clamped := false
	if startRev < s.compactRev+1 {
		startRev = s.compactRev + 1
		clamped = true
	}
	if startRev > s.rev+1 {
		startRev = s.rev + 1
	}
	s.nextWatch++
	w := &watcher{
		id:       s.nextWatch,
		f:        f,
		startRev: startRev,
		nextRev:  startRev,
		notify:   make(chan struct{}, 1),
		done:     make(chan struct{}),
	}
	for _, ev := range s.history {
		if ev.rev >= startRev && f.match(ev.key) {
			w.q = append(w.q, ev)
			w.expected = append(w.expected, ev)
			w.nextRev = ev.rev + 1
		}
	}
	s.watchers[w.id] = w
	s.mu.Unlock()
	go w.consume()
	if clamped {
		atomic.AddInt64(&startClamped, 1)
	}
	atomic.AddInt64(&watchesCreated, 1)
	return w
}

// cancel 关闭 queue，再由 consumer 排空；expected 在注册/emit 时已同步累积。
func (s *store) cancel(w *watcher) {
	s.mu.Lock()
	w.mu.Lock()
	w.closed = true
	w.mu.Unlock()
	delete(s.watchers, w.id)
	w.signal()
	s.mu.Unlock()
	<-w.done
}

func (s *store) compactLocked() int {
	safe := s.rev
	for _, w := range s.watchers {
		if w.nextRev-1 < safe {
			safe = w.nextRev - 1
		}
	}
	if safe <= s.compactRev {
		return 0
	}
	idx := sort.Search(len(s.history), func(i int) bool { return s.history[i].rev > safe })
	if idx == 0 {
		s.compactRev = safe
		return 0
	}
	s.history = append([]event(nil), s.history[idx:]...)
	s.compactRev = safe
	return idx
}

func (s *store) compact() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := s.compactLocked()
	if n > 0 {
		atomic.AddInt64(&compacted, int64(n))
		atomic.AddInt64(&compactions, 1)
	}
	return n
}

func (s *store) detachLocked(leaseID int64, key string) {
	if l := s.leases[leaseID]; l != nil {
		delete(l.keys, key)
	}
}

// putChecked 在同一把锁内插入并立即验证 map 内容，随后发出事件。
func (s *store) putChecked(key, val string, leaseID int64) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if leaseID != 0 {
		l := s.leases[leaseID]
		if l == nil || l.expireAt <= s.clock {
			return 0, errLease
		}
	}
	s.rev++
	if old := s.data[key]; old != nil && old.lease != 0 {
		s.detachLocked(old.lease, key)
	}
	s.data[key] = &value{rev: s.rev, val: val, lease: leaseID}
	if leaseID != 0 {
		s.leases[leaseID].keys[key] = struct{}{}
	}
	v := s.data[key]
	if v == nil || v.rev != s.rev || v.val != val || v.lease != leaseID {
		return 0, fmt.Errorf("post-put invariant: key=%s stored=%+v want rev=%d val=%q lease=%d", key, v, s.rev, val, leaseID)
	}
	ev := event{rev: s.rev, key: key, val: val, lease: leaseID}
	s.emitLocked(ev)
	atomic.AddInt64(&puts, 1)
	return s.rev, nil
}

func (s *store) delChecked(key string) (bool, int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.data[key]
	if !ok {
		atomic.AddInt64(&deletes, 1)
		return false, 0, nil
	}
	s.rev++
	if v.lease != 0 {
		s.detachLocked(v.lease, key)
	}
	delete(s.data, key)
	if s.data[key] != nil {
		return false, 0, fmt.Errorf("post-delete invariant: key=%s still present", key)
	}
	ev := event{rev: s.rev, key: key, del: true, lease: v.lease}
	s.emitLocked(ev)
	atomic.AddInt64(&deletes, 1)
	return true, ev.rev, nil
}

type getResult struct {
	found bool
	key   string
	val   string
	rev   int64
	lease int64
}

func (s *store) checkValueLocked(key string, v *value) error {
	if v.rev <= 0 || v.rev > s.rev {
		return fmt.Errorf("value rev out of range: key=%s rev=%d store_rev=%d", key, v.rev, s.rev)
	}
	if v.lease != 0 {
		l := s.leases[v.lease]
		if l == nil {
			return fmt.Errorf("dangling lease: key=%s lease=%d", key, v.lease)
		}
		if l.expireAt <= s.clock {
			return fmt.Errorf("expired lease still attached: key=%s lease=%d expire=%d clock=%d",
				key, v.lease, l.expireAt, s.clock)
		}
		if _, ok := l.keys[key]; !ok {
			return fmt.Errorf("lease attachment missing: key=%s lease=%d", key, v.lease)
		}
	}
	return nil
}

func (s *store) getChecked(key string) (getResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.data[key]
	if !ok {
		atomic.AddInt64(&gets, 1)
		return getResult{key: key}, nil
	}
	if err := s.checkValueLocked(key, v); err != nil {
		return getResult{}, err
	}
	atomic.AddInt64(&gets, 1)
	return getResult{found: true, key: key, val: v.val, rev: v.rev, lease: v.lease}, nil
}

func (s *store) rangeChecked(lo, hi string) ([]getResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []getResult
	for k, v := range s.data {
		if k < lo || k >= hi {
			continue
		}
		if err := s.checkValueLocked(k, v); err != nil {
			return nil, err
		}
		out = append(out, getResult{found: true, key: k, val: v.val, rev: v.rev, lease: v.lease})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].key < out[j].key })
	for i := 1; i < len(out); i++ {
		if out[i-1].key >= out[i].key {
			return nil, fmt.Errorf("range not sorted: %q >= %q", out[i-1].key, out[i].key)
		}
	}
	atomic.AddInt64(&scans, 1)
	return out, nil
}

func (s *store) grant(ttl int64) int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	if ttl <= 0 {
		ttl = 1
	}
	s.nextLease++
	id := s.nextLease
	s.leases[id] = &leaseRec{id: id, ttl: ttl, expireAt: s.clock + ttl, keys: make(map[string]struct{})}
	atomic.AddInt64(&leaseGrants, 1)
	return id
}

func (s *store) renew(id int64) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	l := s.leases[id]
	if l == nil {
		return false
	}
	l.expireAt = s.clock + l.ttl
	atomic.AddInt64(&leaseRenews, 1)
	return true
}

func sortedLeaseKeys(l *leaseRec) []string {
	ks := make([]string, 0, len(l.keys))
	for k := range l.keys {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return ks
}

func (s *store) revokeLocked(id int64) int {
	l := s.leases[id]
	if l == nil {
		return 0
	}
	n := 0
	for _, k := range sortedLeaseKeys(l) {
		v := s.data[k]
		if v != nil && v.lease == id {
			s.rev++
			delete(s.data, k)
			s.emitLocked(event{rev: s.rev, key: k, del: true, lease: id})
			n++
		}
	}
	delete(s.leases, id)
	return n
}

func (s *store) revoke(id int64) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := s.revokeLocked(id)
	if n > 0 {
		atomic.AddInt64(&leaseRevokes, 1)
	}
	return n
}

func (s *store) advance(delta int64) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.clock += delta
	var ids []int64
	for id, l := range s.leases {
		if l.expireAt <= s.clock {
			ids = append(ids, id)
		}
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	n := 0
	for _, id := range ids {
		n += s.revokeLocked(id)
		atomic.AddInt64(&leaseExpires, 1)
	}
	return n
}

// verifyInvariants 全量检查数据 map / lease 附着 / watcher/compaction 元数据。
func (s *store) verifyInvariants() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.compactRev > s.rev {
		return fmt.Errorf("compactRev=%d > rev=%d", s.compactRev, s.rev)
	}
	for k, v := range s.data {
		if !validKey(k) {
			return fmt.Errorf("invalid key in map: %q", k)
		}
		if err := s.checkValueLocked(k, v); err != nil {
			return err
		}
	}
	for id, l := range s.leases {
		if l.id != id || l.ttl <= 0 {
			return fmt.Errorf("lease metadata corrupted: id=%d rec=%+v", id, l)
		}
		for k := range l.keys {
			v := s.data[k]
			if v == nil || v.lease != id {
				return fmt.Errorf("lease %d attached key %q has value=%+v", id, k, v)
			}
		}
	}
	for _, w := range s.watchers {
		if w.nextRev < w.startRev {
			return fmt.Errorf("watcher %d nextRev=%d < startRev=%d", w.id, w.nextRev, w.startRev)
		}
	}
	return nil
}

func validKey(k string) bool {
	return len(k) == 4 && k[0] >= 'a' && k[0] < 'a'+keyShards && k[1] == '/'
}

type worker struct {
	id     int64
	seed   uint64
	rng    uint64
	st     *store
	leases []int64
	watch  []*watcher
	rounds int64
}

func (w *worker) next() uint64 {
	x := w.rng
	x ^= x << 13
	x ^= x >> 7
	x ^= x << 17
	w.rng = x
	return x
}

func (w *worker) randKey() string { return keyspace[w.next()%uint64(len(keyspace))] }

func (w *worker) removeLease(id int64) {
	for i, v := range w.leases {
		if v == id {
			w.leases[i] = w.leases[len(w.leases)-1]
			w.leases = w.leases[:len(w.leases)-1]
			return
		}
	}
}

func (w *worker) cancelWatch(idx int) {
	wt := w.watch[idx]
	w.st.cancel(wt)
	if wt.bad != "" {
		failf("scenario=%s seed=%d worker=%d watcher=%d start_rev=%d filter=%s: %s",
			scenario, w.seed, w.id, wt.id, wt.startRev, wt.f, wt.bad)
	}
	if len(wt.got) != len(wt.expected) {
		failf("scenario=%s seed=%d worker=%d watcher=%d start_rev=%d filter=%s expected_events=%d actual=%d: watcher event loss/dup",
			scenario, w.seed, w.id, wt.id, wt.startRev, wt.f, len(wt.expected), len(wt.got))
	}
	for i := range wt.got {
		if wt.got[i] != wt.expected[i] {
			failf("scenario=%s seed=%d worker=%d watcher=%d start_rev=%d filter=%s index=%d expected=%+v actual=%+v: watcher replay mismatch (lost/reordered)",
				scenario, w.seed, w.id, wt.id, wt.startRev, wt.f, i, wt.expected[i], wt.got[i])
		}
	}
	atomic.AddInt64(&watchesChecked, 1)
	w.watch[idx] = w.watch[len(w.watch)-1]
	w.watch = w.watch[:len(w.watch)-1]
}

func (w *worker) randFilter() filter {
	x := w.next()
	if x%2 == 0 {
		return filter{prefix: fmt.Sprintf("%c/", 'a'+x%keyShards)}
	}
	a := int(x % keyShards)
	width := 1 + int((x/3)%4)
	end := a + width
	lo := fmt.Sprintf("%c/00", 'a'+a)
	hi := "i/00"
	if end < keyShards {
		hi = fmt.Sprintf("%c/00", 'a'+end)
	}
	return filter{lo: lo, hi: hi, isRange: true}
}

func (w *worker) startRevForWatch() int64 {
	rev, compactRev := w.st.revs()
	switch w.next() % 3 {
	case 0:
		return rev + 1
	case 1:
		return rev
	default:
		avail := rev - compactRev
		if avail <= 0 {
			return rev + 1
		}
		k := int64(w.next() % uint64(avail))
		return rev - k
	}
}

func (w *worker) opPut() {
	key := w.randKey()
	val := fmt.Sprintf("w%d.v%x", w.id, w.next())
	lease := int64(0)
	if len(w.leases) > 0 && w.next()%3 == 0 {
		lease = w.leases[w.next()%uint64(len(w.leases))]
	}
	rev, err := w.st.putChecked(key, val, lease)
	if err == errLease {
		atomic.AddInt64(&leaseErrors, 1)
		w.removeLease(lease)
		return
	}
	if err != nil {
		failf("scenario=%s seed=%d worker=%d key=%s: %v", scenario, w.seed, w.id, key, err)
	}
	// 写后读：若读到的仍是本次写入的 revision，内容必须一致。
	if w.next()%4 == 0 {
		got, err := w.st.getChecked(key)
		if err != nil {
			failf("scenario=%s seed=%d worker=%d key=%s read-after-put: %v", scenario, w.seed, w.id, key, err)
		}
		if got.found && got.rev == rev && got.val != val {
			failf("scenario=%s seed=%d worker=%d key=%s read-after-put value mismatch: rev=%d got=%q want=%q",
				scenario, w.seed, w.id, key, rev, got.val, val)
		}
	}
}

func (w *worker) opDelete() {
	if _, _, err := w.st.delChecked(w.randKey()); err != nil {
		failf("scenario=%s seed=%d worker=%d: %v", scenario, w.seed, w.id, err)
	}
}

func (w *worker) opGet() {
	if _, err := w.st.getChecked(w.randKey()); err != nil {
		failf("scenario=%s seed=%d worker=%d: %v", scenario, w.seed, w.id, err)
	}
}

func (w *worker) opScan() {
	x := w.next()
	a := int(x % keyShards)
	width := 1 + int((x/5)%4)
	end := a + width
	lo := fmt.Sprintf("%c/00", 'a'+a)
	hi := "i/00"
	if end < keyShards {
		hi = fmt.Sprintf("%c/00", 'a'+end)
	}
	if _, err := w.st.rangeChecked(lo, hi); err != nil {
		failf("scenario=%s seed=%d worker=%d range=[%s,%s): %v", scenario, w.seed, w.id, lo, hi, err)
	}
}

func (w *worker) opLease() {
	if len(w.leases) < maxLeases {
		id := w.st.grant(3 + int64(w.next()%10))
		w.leases = append(w.leases, id)
		return
	}
	id := w.leases[w.next()%uint64(len(w.leases))]
	if !w.st.renew(id) {
		atomic.AddInt64(&leaseErrors, 1)
		w.removeLease(id)
	}
}

func (w *worker) opRenew() {
	if len(w.leases) == 0 {
		return
	}
	id := w.leases[w.next()%uint64(len(w.leases))]
	if !w.st.renew(id) {
		atomic.AddInt64(&leaseErrors, 1)
		w.removeLease(id)
	}
}

func (w *worker) opRevoke() {
	if len(w.leases) == 0 {
		return
	}
	id := w.leases[w.next()%uint64(len(w.leases))]
	w.st.revoke(id)
	w.removeLease(id)
}

func (w *worker) opWatch() {
	if len(w.watch) >= maxWatches {
		w.cancelWatch(int(w.next() % uint64(len(w.watch))))
	}
	f := w.randFilter()
	start := w.startRevForWatch()
	w.watch = append(w.watch, w.st.watch(f, start))
}

func (w *worker) opCancelWatch() {
	if len(w.watch) == 0 {
		return
	}
	w.cancelWatch(int(w.next() % uint64(len(w.watch))))
}

func (w *worker) round(r int64) {
	for i := 0; i < opsPerRound; i++ {
		op := w.next() % 100
		switch {
		case op < 40:
			w.opPut()
		case op < 55:
			w.opDelete()
		case op < 68:
			w.opGet()
		case op < 76:
			w.opScan()
		case op < 84:
			w.opLease()
		case op < 90:
			w.opRenew()
		case op < 94:
			w.opRevoke()
		case op < 97:
			w.opWatch()
		case op < 99:
			w.opCancelWatch()
		default:
			w.st.advance(1)
		}
		atomic.AddInt64(&ops, 1)
		atomic.AddInt64(&beats, 1)
	}
	if r%4 == 0 {
		w.st.advance(1)
	}
	if r%8 == 0 {
		w.st.compact()
	}
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
	for len(w.watch) > 0 {
		w.cancelWatch(len(w.watch) - 1)
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
		runtime.GC()
		atomic.AddInt64(&gcCycles, 1)
		time.Sleep(20 * time.Millisecond)
	}
}

func verifier(st *store, seed uint64, stop <-chan struct{}, wg *sync.WaitGroup) {
	defer wg.Done()
	t := time.NewTicker(25 * time.Millisecond)
	defer t.Stop()
	for {
		select {
		case <-stop:
			return
		case <-t.C:
			if err := st.verifyInvariants(); err != nil {
				failf("scenario=%s seed=%d phase=verify: %v", scenario, seed, err)
			}
			atomic.AddInt64(&checks, 1)
		}
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
				failf("scenario=%s watchdog stalled: no worker progress for %s (beats=%d)",
					scenario, time.Since(lastChange), cur)
			}
		}
	}
}

func main() {
	setupCrashOutput()
	initKeyspace()
	seconds := envInt("STRESS_SECONDS", 60, 0, 86400)
	workers := envInt("STRESS_WORKERS", 4, 1, 32)
	hb := envInt("STRESS_HEARTBEAT", 5, 1, 60)
	seed := envSeed(1)
	fixed := int64(envInt("STRESS_FIXED_ROUNDS", 24, 1, 100000))

	baseGoroutines := runtime.NumGoroutine()
	st := newStore()

	fmt.Printf("scenario=%s phase=preflight go=%s goos=%s goarch=%s gomaxprocs=%d seed=%d seconds=%d workers=%d fixed_rounds=%d heartbeat=%ds keys=%d\n",
		scenario, runtime.Version(), runtime.GOOS, runtime.GOARCH, runtime.GOMAXPROCS(0), seed, seconds, workers, fixed, hb, len(keyspace))

	deadline := time.Now().Add(time.Duration(seconds) * time.Second)
	ws := make([]*worker, workers)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		w := &worker{id: int64(i), seed: seed, rng: seed*10000 + uint64(i)*7919 + 1, st: st}
		ws[i] = w
		wg.Add(1)
		go func() {
			defer wg.Done()
			w.run(deadline, seconds, fixed)
		}()
	}

	stop := make(chan struct{})
	var helpers sync.WaitGroup
	helpers.Add(3)
	go gcChurn(stop, &helpers)
	go verifier(st, seed, stop, &helpers)
	go func() {
		defer helpers.Done()
		watchdog(time.Duration(hb)*time.Second, stop)
	}()

	wg.Wait()
	close(stop)
	helpers.Wait()

	// 收尾：虚拟时钟一次推进到所有 lease 之后，验证到期删除与附着关系。
	st.advance(1_000_000)
	if err := st.verifyInvariants(); err != nil {
		failf("scenario=%s seed=%d phase=final-drain: %v", scenario, seed, err)
	}
	st.mu.Lock()
	activeLeases := len(st.leases)
	activeWatches := len(st.watchers)
	clock := st.clock
	st.mu.Unlock()
	if activeLeases != 0 {
		failf("scenario=%s seed=%d expected=0 active leases after drain actual=%d", scenario, seed, activeLeases)
	}
	if activeWatches != 0 {
		failf("scenario=%s seed=%d expected=0 active watchers after workers actual=%d", scenario, seed, activeWatches)
	}

	n := runtime.NumGoroutine()
	if n > baseGoroutines+2 {
		failf("scenario=%s phase=shutdown expected=goroutines<=%d actual=%d: goroutine leak (watcher consumer?)",
			scenario, baseGoroutines+2, n)
	}

	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	fmt.Printf("metrics: ops=%d puts=%d deletes=%d gets=%d scans=%d watchers_created=%d watchers_checked=%d watcher_events=%d start_rev_clamped=%d lease_grants=%d lease_renews=%d lease_revokes=%d lease_expires=%d lease_errors=%d compacted_events=%d compactions=%d checks=%d virtual_clock=%d gc_churn=%d beats=%d heap_alloc=%d heap_objects=%d num_gc=%d goroutines=%d\n",
		atomic.LoadInt64(&ops), atomic.LoadInt64(&puts), atomic.LoadInt64(&deletes), atomic.LoadInt64(&gets),
		atomic.LoadInt64(&scans), atomic.LoadInt64(&watchesCreated), atomic.LoadInt64(&watchesChecked),
		atomic.LoadInt64(&watcherEvents), atomic.LoadInt64(&startClamped), atomic.LoadInt64(&leaseGrants),
		atomic.LoadInt64(&leaseRenews), atomic.LoadInt64(&leaseRevokes), atomic.LoadInt64(&leaseExpires),
		atomic.LoadInt64(&leaseErrors), atomic.LoadInt64(&compacted), atomic.LoadInt64(&compactions),
		atomic.LoadInt64(&checks), clock, atomic.LoadInt64(&gcCycles), atomic.LoadInt64(&beats),
		m.HeapAlloc, m.HeapObjects, m.NumGC, n)
	fmt.Println("STRESS PASS n10_mvcc_watch_lease")
}
