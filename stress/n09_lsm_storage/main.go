// N09: 纯内存 mini-LSM 长跑。
// WAL（内存记录，flush 后截断）+ memtable（map + skiplist 有序索引，多版本）
// + 不可变 memtable 队列 + L0/L1 SSTable（内存有序数组）+ 全量 compaction
// （按 min snapshot seq 合并、淘汰旧版本与墓碑）+ 快照迭代器（旧版本可见性）。
// 并发写/读/范围迭代/快照/compaction 使用分锁：writeMu（WAL+memtable+model）、
// memtable 锁、db 表链锁、flushMu、compactMu。随机 op + seed 派生；
// 持续校验读写一致性、value checksum、快照 digest 稳定性；GC churn 与 goroutine 基线。
package main

import (
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"hash/fnv"
	"os"
	"runtime"
	"runtime/debug"
	"sort"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

const scenario = "n09"

const (
	numKeys         = 256
	memVersionLimit = 256
	memByteLimit    = 128 << 10
	immHighWater    = 3
	slMaxLevel      = 8
)

var (
	beats           int64
	gcCycles        int64
	ops             int64
	puts            int64
	dels            int64
	gets            int64
	scans           int64
	snapsCreated    int64
	snapsVerified   int64
	flushes         int64
	compactions     int64
	compactedDrop   int64
	checks          int64
	walChecks       int64
	maxTableEntries int64
)

func keyAt(i int) string { return fmt.Sprintf("k/%03d", i) }

func hiKey(end int) string {
	if end >= numKeys {
		return "l"
	}
	return keyAt(end)
}

// ---------------- skiplist ----------------

type slNode struct {
	key  string
	next []*slNode
}

type skiplist struct {
	head  *slNode
	level int
	rng   uint64
}

func newSkiplist(seed uint64) *skiplist {
	if seed == 0 {
		seed = 1
	}
	return &skiplist{head: &slNode{next: make([]*slNode, slMaxLevel)}, level: 1, rng: seed}
}

func (s *skiplist) rand() uint64 {
	x := s.rng
	x ^= x << 13
	x ^= x >> 7
	x ^= x << 17
	s.rng = x
	return x
}

// insert 要求 key 之前不存在（memtable 用 map 保证）。
func (s *skiplist) insert(key string) {
	var update [slMaxLevel]*slNode
	x := s.head
	for i := s.level - 1; i >= 0; i-- {
		for x.next[i] != nil && x.next[i].key < key {
			x = x.next[i]
		}
		update[i] = x
	}
	lvl := 1
	for lvl < slMaxLevel && s.rand()%4 == 0 {
		lvl++
	}
	if lvl > s.level {
		for i := s.level; i < lvl; i++ {
			update[i] = s.head
		}
		s.level = lvl
	}
	n := &slNode{key: key, next: make([]*slNode, lvl)}
	for i := 0; i < lvl; i++ {
		n.next[i] = update[i].next[i]
		update[i].next[i] = n
	}
}

// scanRange 在 [lo,hi) 上按 key 升序回调（调用方持读锁）。
func (s *skiplist) scanRange(lo, hi string, fn func(key string)) {
	x := s.head.next[0]
	for x != nil && x.key < lo {
		x = x.next[0]
	}
	for x != nil && x.key < hi {
		fn(x.key)
		x = x.next[0]
	}
}

// ---------------- memtable ----------------

type version struct {
	seq int64
	val string
	del bool
	sum uint32
}

type memtable struct {
	mu       sync.RWMutex
	m        map[string][]version
	sl       *skiplist
	versions int
	bytes    int
}

func newMemtable(seed uint64) *memtable {
	return &memtable{m: make(map[string][]version), sl: newSkiplist(seed)}
}

func (mt *memtable) insert(key string, v version) (versions, bytesN int) {
	mt.mu.Lock()
	vs := mt.m[key]
	if len(vs) == 0 {
		mt.sl.insert(key)
	}
	mt.m[key] = append(vs, v)
	mt.versions++
	mt.bytes += len(key) + len(v.val) + 32
	versions, bytesN = mt.versions, mt.bytes
	mt.mu.Unlock()
	return versions, bytesN
}

func (mt *memtable) latest(key string) (version, bool) {
	mt.mu.RLock()
	defer mt.mu.RUnlock()
	vs := mt.m[key]
	if len(vs) == 0 {
		return version{}, false
	}
	return vs[len(vs)-1], true
}

func (mt *memtable) scan(lo, hi string, snap int64, consider func(key string, v version)) {
	mt.mu.RLock()
	defer mt.mu.RUnlock()
	mt.sl.scanRange(lo, hi, func(k string) {
		vs := mt.m[k]
		idx := sort.Search(len(vs), func(i int) bool { return vs[i].seq > snap })
		if idx > 0 {
			consider(k, vs[idx-1])
		}
	})
}

// ---------------- sstable ----------------

type ventry struct {
	key string
	seq int64
	val string
	del bool
	sum uint32
}

type sstable struct {
	entries []ventry // key asc, seq desc
	maxSeq  int64
}

func buildTable(mt *memtable) *sstable {
	mt.mu.RLock()
	defer mt.mu.RUnlock()
	var ents []ventry
	mt.sl.scanRange("", "~", func(k string) {
		vs := mt.m[k]
		for i := len(vs) - 1; i >= 0; i-- {
			v := vs[i]
			ents = append(ents, ventry{key: k, seq: v.seq, val: v.val, del: v.del, sum: v.sum})
		}
	})
	t := &sstable{entries: ents}
	for _, e := range ents {
		if e.seq > t.maxSeq {
			t.maxSeq = e.seq
		}
	}
	if int64(len(ents)) > atomic.LoadInt64(&maxTableEntries) {
		atomic.StoreInt64(&maxTableEntries, int64(len(ents)))
	}
	return t
}

func (t *sstable) latest(key string) (version, bool) {
	if t == nil {
		return version{}, false
	}
	i := sort.Search(len(t.entries), func(i int) bool { return t.entries[i].key >= key })
	if i < len(t.entries) && t.entries[i].key == key {
		e := t.entries[i]
		return version{seq: e.seq, val: e.val, del: e.del, sum: e.sum}, true
	}
	return version{}, false
}

func (t *sstable) scan(lo, hi string, snap int64, consider func(key string, v version)) {
	if t == nil {
		return
	}
	i := sort.Search(len(t.entries), func(i int) bool { return t.entries[i].key >= lo })
	for i < len(t.entries) {
		k := t.entries[i].key
		if k >= hi {
			return
		}
		j := i
		for j < len(t.entries) && t.entries[j].key == k {
			j++
		}
		for x := i; x < j; x++ {
			e := t.entries[x]
			if e.seq <= snap {
				consider(k, version{seq: e.seq, val: e.val, del: e.del, sum: e.sum})
				break
			}
		}
		i = j
	}
}

// mergeTables 合并所有输入表；对每个 key 保留 safe 之后的所有版本 +
// 第一个 <= safe 的版本（若为墓碑则连同更旧版本一起丢弃）。
func mergeTables(tables []*sstable, safe int64) (*sstable, int64) {
	var all []ventry
	for _, t := range tables {
		if t != nil {
			all = append(all, t.entries...)
		}
	}
	sort.Slice(all, func(i, j int) bool {
		if all[i].key != all[j].key {
			return all[i].key < all[j].key
		}
		return all[i].seq > all[j].seq
	})
	out := make([]ventry, 0, len(all))
	var dropped int64
	for i := 0; i < len(all); {
		j := i
		for j < len(all) && all[j].key == all[i].key {
			j++
		}
		for k := i; k < j; k++ {
			e := all[k]
			if e.seq > safe {
				out = append(out, e)
				continue
			}
			if !e.del {
				out = append(out, e)
			} else {
				dropped++
			}
			dropped += int64(j - k - 1)
			break
		}
		i = j
	}
	t := &sstable{entries: out}
	for _, e := range out {
		if e.seq > t.maxSeq {
			t.maxSeq = e.seq
		}
	}
	return t, dropped
}

// ---------------- LSM ----------------

type walRec struct {
	seq int64
	key string
	val string
	del bool
	sum uint32
}

type modelVal struct {
	seq int64
	val string
	del bool
}

type lsm struct {
	writeMu sync.Mutex
	seq     int64
	wal     []walRec
	model   map[string]modelVal

	mu      sync.RWMutex
	mem     *memtable
	immQ    []*memtable
	l0      []*sstable // 新表在前
	l1      *sstable
	snaps   map[int64]int
	flushNo int64

	flushMu   sync.Mutex
	compactMu sync.Mutex

	flushCh       chan struct{}
	compactCh     chan struct{}
	stopCh        chan struct{}
	flusherDone   chan struct{}
	compactorDone chan struct{}
}

func newLSM() *lsm {
	d := &lsm{
		model:         make(map[string]modelVal),
		mem:           newMemtable(1),
		snaps:         make(map[int64]int),
		flushCh:       make(chan struct{}, 1),
		compactCh:     make(chan struct{}, 1),
		stopCh:        make(chan struct{}),
		flusherDone:   make(chan struct{}),
		compactorDone: make(chan struct{}),
	}
	go d.flusher()
	go d.compactor()
	return d
}

func (d *lsm) currentSeq() int64 { return atomic.LoadInt64(&d.seq) }

func (d *lsm) signal(ch chan struct{}) {
	select {
	case ch <- struct{}{}:
	default:
	}
}

func (d *lsm) rotateLocked() {
	d.mu.Lock()
	d.immQ = append(d.immQ, d.mem)
	d.flushNo++
	d.mem = newMemtable(uint64(d.flushNo)*0x9e3779b97f4a7c15 + 1)
	d.mu.Unlock()
}

func (d *lsm) queued() int {
	d.mu.RLock()
	n := len(d.immQ)
	d.mu.RUnlock()
	return n
}

func (d *lsm) Put(key, val string) int64 {
	sum := crc32.ChecksumIEEE([]byte(val))
	d.writeMu.Lock()
	seq := atomic.AddInt64(&d.seq, 1)
	d.wal = append(d.wal, walRec{seq: seq, key: key, val: val, sum: sum})
	d.model[key] = modelVal{seq: seq, val: val}
	cnt, bytesN := d.mem.insert(key, version{seq: seq, val: val, sum: sum})
	rotate := cnt >= memVersionLimit || bytesN >= memByteLimit
	if rotate {
		d.rotateLocked()
	}
	d.verifyWALSampleLocked()
	d.writeMu.Unlock()
	if rotate {
		d.signal(d.flushCh)
	}
	if d.queued() > immHighWater {
		d.flushOne()
	}
	atomic.AddInt64(&puts, 1)
	return seq
}

func (d *lsm) Delete(key string) int64 {
	d.writeMu.Lock()
	seq := atomic.AddInt64(&d.seq, 1)
	d.wal = append(d.wal, walRec{seq: seq, key: key, del: true})
	d.model[key] = modelVal{seq: seq, del: true}
	cnt, bytesN := d.mem.insert(key, version{seq: seq, del: true})
	rotate := cnt >= memVersionLimit || bytesN >= memByteLimit
	if rotate {
		d.rotateLocked()
	}
	d.verifyWALSampleLocked()
	d.writeMu.Unlock()
	if rotate {
		d.signal(d.flushCh)
	}
	if d.queued() > immHighWater {
		d.flushOne()
	}
	atomic.AddInt64(&dels, 1)
	return seq
}

func (d *lsm) verifyWALSampleLocked() {
	n := len(d.wal)
	if n == 0 {
		return
	}
	for _, idx := range []int{0, n / 2, n - 1} {
		r := d.wal[idx]
		if r.del {
			if r.val != "" {
				failf("scenario=%s phase=wal expected=\"delete record empty value\" actual=%q seq=%d", scenario, r.val, r.seq)
			}
		} else if got := crc32.ChecksumIEEE([]byte(r.val)); got != r.sum {
			failf("scenario=%s phase=wal key=%s seq=%d expected=checksum %d actual=%d", scenario, r.key, r.seq, r.sum, got)
		}
		if idx > 0 && d.wal[idx-1].seq >= r.seq {
			failf("scenario=%s phase=wal expected=\"seq increasing\" actual prev=%d cur=%d", scenario, d.wal[idx-1].seq, r.seq)
		}
	}
	atomic.AddInt64(&walChecks, 1)
}

func (d *lsm) truncateWAL(lastSeq int64) {
	d.writeMu.Lock()
	idx := sort.Search(len(d.wal), func(i int) bool { return d.wal[i].seq > lastSeq })
	if idx > 0 {
		d.wal = append([]walRec(nil), d.wal[idx:]...)
	}
	d.writeMu.Unlock()
}

func (d *lsm) flushOne() bool {
	d.flushMu.Lock()
	defer d.flushMu.Unlock()
	d.mu.RLock()
	if len(d.immQ) == 0 {
		d.mu.RUnlock()
		return false
	}
	mt := d.immQ[0]
	d.mu.RUnlock()

	// 构建期间 memtable 仍留在 immQ 中可见；安装时在同一临界区内
	// 从 immQ 移除并前插 L0，读者不会看到两者都不存在的窗口。
	tbl := buildTable(mt)
	d.mu.Lock()
	if len(d.immQ) > 0 && d.immQ[0] == mt {
		d.immQ[0] = nil
		d.immQ = d.immQ[1:]
		if len(tbl.entries) > 0 {
			d.l0 = append([]*sstable{tbl}, d.l0...)
		}
	} else if len(tbl.entries) > 0 {
		// 防御：队列头意外变化时仍安装表，避免丢失版本。
		d.l0 = append([]*sstable{tbl}, d.l0...)
	}
	d.mu.Unlock()

	if len(tbl.entries) == 0 {
		return true
	}
	atomic.AddInt64(&flushes, 1)
	d.truncateWAL(tbl.maxSeq)
	d.signal(d.compactCh)
	return true
}

func (d *lsm) flusher() {
	defer close(d.flusherDone)
	for {
		select {
		case <-d.stopCh:
			return
		case <-d.flushCh:
			for d.flushOne() {
			}
		}
	}
}

func (d *lsm) compactor() {
	defer close(d.compactorDone)
	for {
		select {
		case <-d.stopCh:
			return
		case <-d.compactCh:
			d.compact(false)
		}
	}
}

func (d *lsm) minSnapLocked() int64 {
	if len(d.snaps) == 0 {
		return d.currentSeq()
	}
	min := int64(1 << 62)
	for s := range d.snaps {
		if s < min {
			min = s
		}
	}
	return min
}

// compact 合并 L1 + 全部当前 L0，保留 snapshot 所需版本；期间新 flush 的 L0 不受影响。
func (d *lsm) compact(force bool) {
	d.compactMu.Lock()
	defer d.compactMu.Unlock()
	d.mu.RLock()
	if len(d.l0) == 0 && d.l1 == nil {
		d.mu.RUnlock()
		return
	}
	if !force && len(d.l0) < 2 && d.l1 != nil {
		d.mu.RUnlock()
		return
	}
	included := make(map[*sstable]bool)
	var tables []*sstable
	if d.l1 != nil {
		tables = append(tables, d.l1)
		included[d.l1] = true
	}
	for _, t := range d.l0 {
		tables = append(tables, t)
		included[t] = true
	}
	safe := d.minSnapLocked()
	l1 := d.l1
	d.mu.RUnlock()

	merged, dropped := mergeTables(tables, safe)

	d.mu.Lock()
	if d.l1 != l1 {
		d.mu.Unlock()
		return
	}
	var newL0 []*sstable
	for _, t := range d.l0 {
		if !included[t] {
			newL0 = append(newL0, t)
		}
	}
	d.l0 = newL0
	d.l1 = merged
	d.mu.Unlock()
	atomic.AddInt64(&compactions, 1)
	atomic.AddInt64(&compactedDrop, dropped)
}

func (d *lsm) sources() (*memtable, []*memtable, []*sstable, *sstable) {
	d.mu.RLock()
	mt := d.mem
	imms := append([]*memtable(nil), d.immQ...)
	l0 := append([]*sstable(nil), d.l0...)
	l1 := d.l1
	d.mu.RUnlock()
	return mt, imms, l0, l1
}

// Get 返回最新可见值；若最新版本是墓碑则 found=false，但仍返回其 seq 便于写后校验。
func (d *lsm) Get(key string) (val string, seq int64, found bool, err error) {
	mt, imms, l0, l1 := d.sources()
	bestSeq := int64(-1)
	var best version
	consider := func(v version) {
		if v.seq > bestSeq {
			bestSeq = v.seq
			best = v
		}
	}
	if v, ok := mt.latest(key); ok {
		consider(v)
	}
	for _, m := range imms {
		if v, ok := m.latest(key); ok {
			consider(v)
		}
	}
	for _, t := range l0 {
		if v, ok := t.latest(key); ok {
			consider(v)
		}
	}
	if v, ok := l1.latest(key); ok {
		consider(v)
	}
	if bestSeq < 0 {
		return "", 0, false, nil
	}
	if best.del {
		return "", bestSeq, false, nil
	}
	if got := crc32.ChecksumIEEE([]byte(best.val)); got != best.sum {
		return "", 0, false, fmt.Errorf("value checksum mismatch: key=%s seq=%d expected=%d actual=%d", key, best.seq, best.sum, got)
	}
	return best.val, bestSeq, true, nil
}

type scanKV struct {
	key string
	val string
	seq int64
	sum uint32
}

// ScanSnapshot 返回 [lo,hi) 在 snap 时刻的可见键值（升序，跳过墓碑）。
func (d *lsm) ScanSnapshot(lo, hi string, snap int64) ([]scanKV, error) {
	mt, imms, l0, l1 := d.sources()
	best := make(map[string]version)
	consider := func(k string, v version) {
		if cur, ok := best[k]; !ok || v.seq > cur.seq {
			best[k] = v
		}
	}
	mt.scan(lo, hi, snap, consider)
	for _, m := range imms {
		m.scan(lo, hi, snap, consider)
	}
	for _, t := range l0 {
		t.scan(lo, hi, snap, consider)
	}
	l1.scan(lo, hi, snap, consider)

	out := make([]scanKV, 0, len(best))
	for k, v := range best {
		if v.del {
			continue
		}
		if got := crc32.ChecksumIEEE([]byte(v.val)); got != v.sum {
			return nil, fmt.Errorf("snapshot value checksum mismatch: key=%s seq=%d expected=%d actual=%d", k, v.seq, v.sum, got)
		}
		out = append(out, scanKV{key: k, val: v.val, seq: v.seq, sum: v.sum})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].key < out[j].key })
	for i := 1; i < len(out); i++ {
		if out[i-1].key >= out[i].key {
			return nil, fmt.Errorf("snapshot scan not strictly ordered: %q >= %q", out[i-1].key, out[i].key)
		}
	}
	return out, nil
}

func (d *lsm) NewSnapshot() int64 {
	// 与写临界区同步：seq 在 Put/Delete 内部先递增后插入，快照点必须落在
	// “某次写完整提交之后”，否则可能切到半提交写导致快照视图抖动。
	d.writeMu.Lock()
	s := d.currentSeq()
	d.mu.Lock()
	d.snaps[s]++
	d.mu.Unlock()
	d.writeMu.Unlock()
	atomic.AddInt64(&snapsCreated, 1)
	return s
}

func (d *lsm) ReleaseSnapshot(s int64) {
	d.mu.Lock()
	d.snaps[s]--
	if d.snaps[s] <= 0 {
		delete(d.snaps, s)
	}
	d.mu.Unlock()
}

func (d *lsm) scanDigest(snap int64, lo, hi string) (uint64, int, error) {
	kvs, err := d.ScanSnapshot(lo, hi, snap)
	if err != nil {
		return 0, 0, err
	}
	h := fnv.New64a()
	var buf [8]byte
	for _, kv := range kvs {
		_, _ = h.Write([]byte(kv.key))
		binary.LittleEndian.PutUint64(buf[:], uint64(kv.seq))
		_, _ = h.Write(buf[:])
		binary.LittleEndian.PutUint64(buf[:], uint64(kv.sum))
		_, _ = h.Write(buf[:])
		_, _ = h.Write([]byte(kv.val))
	}
	return h.Sum64(), len(kvs), nil
}

// ---------------- worker ----------------

type snapTrack struct {
	seq    int64
	lo, hi string
	digest uint64
	count  int
}

type worker struct {
	id     int64
	seed   uint64
	rng    uint64
	db     *lsm
	snaps  []snapTrack
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

func (w *worker) opPut() {
	key := keyAt(int(w.next() % numKeys))
	val := fmt.Sprintf("w%d-%x", w.id, w.next())
	seq := w.db.Put(key, val)
	val2, seq2, found, err := w.db.Get(key)
	if err != nil {
		failf("scenario=%s seed=%d worker=%d key=%s read-after-put: %v", scenario, w.seed, w.id, key, err)
	}
	if seq2 < seq {
		failf("scenario=%s seed=%d worker=%d key=%s lost write: put_seq=%d read_seq=%d", scenario, w.seed, w.id, key, seq, seq2)
	}
	if found && seq2 == seq && val2 != val {
		failf("scenario=%s seed=%d worker=%d key=%s read-after-put mismatch: seq=%d got=%q want=%q",
			scenario, w.seed, w.id, key, seq, val2, val)
	}
}

func (w *worker) opDelete() {
	key := keyAt(int(w.next() % numKeys))
	seq := w.db.Delete(key)
	_, seq2, found, err := w.db.Get(key)
	if err != nil {
		failf("scenario=%s seed=%d worker=%d key=%s read-after-delete: %v", scenario, w.seed, w.id, key, err)
	}
	if seq2 < seq {
		failf("scenario=%s seed=%d worker=%d key=%s lost tombstone: del_seq=%d read_seq=%d", scenario, w.seed, w.id, key, seq, seq2)
	}
	if found && seq2 == seq {
		failf("scenario=%s seed=%d worker=%d key=%s tombstone invisible: seq=%d", scenario, w.seed, w.id, key, seq)
	}
}

func (w *worker) opGet() {
	key := keyAt(int(w.next() % numKeys))
	if _, _, _, err := w.db.Get(key); err != nil {
		failf("scenario=%s seed=%d worker=%d key=%s get: %v", scenario, w.seed, w.id, key, err)
	}
	atomic.AddInt64(&gets, 1)
}

func (w *worker) randRange() (string, string) {
	x := w.next()
	a := int(x % numKeys)
	width := 8 + int((x/7)%64)
	end := a + width
	if end > numKeys {
		end = numKeys
	}
	return keyAt(a), hiKey(end)
}

func (w *worker) opSnapshot() {
	if len(w.snaps) >= 2 || (len(w.snaps) > 0 && w.next()%2 == 1) {
		idx := int(w.next() % uint64(len(w.snaps)))
		s := w.snaps[idx]
		digest, count, err := w.db.scanDigest(s.seq, s.lo, s.hi)
		if err != nil {
			failf("scenario=%s seed=%d worker=%d snapshot=%d range=[%s,%s): %v", scenario, w.seed, w.id, s.seq, s.lo, s.hi, err)
		}
		if digest != s.digest || count != s.count {
			failf("scenario=%s seed=%d worker=%d snapshot=%d range=[%s,%s) expected=digest %x/%d actual=%x/%d: snapshot isolation broken",
				scenario, w.seed, w.id, s.seq, s.lo, s.hi, s.digest, s.count, digest, count)
		}
		w.db.ReleaseSnapshot(s.seq)
		w.snaps[idx] = w.snaps[len(w.snaps)-1]
		w.snaps = w.snaps[:len(w.snaps)-1]
		atomic.AddInt64(&snapsVerified, 1)
		return
	}
	lo, hi := w.randRange()
	snap := w.db.NewSnapshot()
	digest, count, err := w.db.scanDigest(snap, lo, hi)
	if err != nil {
		failf("scenario=%s seed=%d worker=%d snapshot=%d range=[%s,%s): %v", scenario, w.seed, w.id, snap, lo, hi, err)
	}
	w.snaps = append(w.snaps, snapTrack{seq: snap, lo: lo, hi: hi, digest: digest, count: count})
	atomic.AddInt64(&scans, 1)
}

func (w *worker) round(r int64) {
	for i := 0; i < 16; i++ {
		op := w.next() % 100
		switch {
		case op < 50:
			w.opPut()
		case op < 65:
			w.opDelete()
		case op < 85:
			w.opGet()
		default:
			w.opSnapshot()
		}
		atomic.AddInt64(&ops, 1)
		atomic.AddInt64(&beats, 1)
	}
	if r%4 == 0 {
		w.db.compact(false)
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
	// 释放所有快照并做最后一次 digest 校验。
	for len(w.snaps) > 0 {
		idx := len(w.snaps) - 1
		s := w.snaps[idx]
		digest, count, err := w.db.scanDigest(s.seq, s.lo, s.hi)
		if err != nil {
			failf("scenario=%s seed=%d worker=%d final snapshot=%d: %v", scenario, w.seed, w.id, s.seq, err)
		}
		if digest != s.digest || count != s.count {
			failf("scenario=%s seed=%d worker=%d final snapshot=%d: digest changed (%x/%d -> %x/%d)",
				scenario, w.seed, w.id, s.seq, s.digest, s.count, digest, count)
		}
		w.db.ReleaseSnapshot(s.seq)
		w.snaps = w.snaps[:idx]
		atomic.AddInt64(&snapsVerified, 1)
	}
}

// ---------------- shutdown & final check ----------------

func (d *lsm) finalize() {
	close(d.stopCh)
	<-d.flusherDone
	<-d.compactorDone
	d.writeMu.Lock()
	if d.mem.versions > 0 {
		d.rotateLocked()
	}
	d.writeMu.Unlock()
	for d.flushOne() {
	}
	d.compact(true)
	d.writeMu.Lock()
	walN := len(d.wal)
	d.writeMu.Unlock()
	if walN != 0 {
		failf("scenario=%s phase=final expected=\"WAL truncated after final flush\" actual=%d records", scenario, walN)
	}
	d.mu.RLock()
	immN := len(d.immQ)
	l0N := len(d.l0)
	snapN := len(d.snaps)
	d.mu.RUnlock()
	if immN != 0 || l0N != 0 || snapN != 0 {
		failf("scenario=%s phase=final expected=\"imm=0 l0=0 snaps=0\" actual imm=%d l0=%d snaps=%d",
			scenario, immN, l0N, snapN)
	}
}

func finalVerify(d *lsm) {
	seq := d.currentSeq()
	live, err := d.ScanSnapshot("", "l", seq)
	if err != nil {
		failf("scenario=%s phase=final-scan: %v", scenario, err)
	}
	got := make(map[string]string, len(live))
	for _, kv := range live {
		got[kv.key] = kv.val
	}
	for k, mv := range d.model {
		if mv.del {
			if _, ok := got[k]; ok {
				failf("scenario=%s phase=final key=%s expected=deleted actual=present", scenario, k)
			}
			continue
		}
		v, ok := got[k]
		if !ok || v != mv.val {
			failf("scenario=%s phase=final key=%s expected=%q actual=%q (present=%v) model_seq=%d",
				scenario, k, mv.val, v, ok, mv.seq)
		}
	}
	for k := range got {
		mv, ok := d.model[k]
		if !ok || mv.del {
			failf("scenario=%s phase=final key=%s unexpected live value=%q", scenario, k, got[k])
		}
	}
	for k, mv := range d.model {
		val, _, found, err := d.Get(k)
		if err != nil {
			failf("scenario=%s phase=final key=%s: %v", scenario, k, err)
		}
		if mv.del {
			if found {
				failf("scenario=%s phase=final key=%s expected=absent actual=%q", scenario, k, val)
			}
		} else if !found || val != mv.val {
			failf("scenario=%s phase=final key=%s expected=%q actual=%q found=%v", scenario, k, mv.val, val, found)
		}
	}
}

// ---------------- harness ----------------

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

func verifier(db *lsm, seed uint64, stop <-chan struct{}, wg *sync.WaitGroup) {
	defer wg.Done()
	t := time.NewTicker(25 * time.Millisecond)
	defer t.Stop()
	for {
		select {
		case <-stop:
			return
		case <-t.C:
			key := keyAt(int((seed*7 + uint64(atomic.LoadInt64(&checks))) % numKeys))
			if _, _, _, err := db.Get(key); err != nil {
				failf("scenario=%s seed=%d phase=verify key=%s: %v", scenario, seed, key, err)
			}
			lo, hi := keyAt(0), "l"
			if _, err := db.ScanSnapshot(lo, hi, db.currentSeq()); err != nil {
				failf("scenario=%s seed=%d phase=verify scan: %v", scenario, seed, err)
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

func main() {
	setupCrashOutput()
	seconds := envInt("STRESS_SECONDS", 60, 0, 86400)
	workers := envInt("STRESS_WORKERS", 4, 1, 32)
	hb := envInt("STRESS_HEARTBEAT", 5, 1, 60)
	seed := envSeed(1)
	fixed := int64(envInt("STRESS_FIXED_ROUNDS", 24, 1, 100000))

	baseGoroutines := runtime.NumGoroutine()
	db := newLSM()

	fmt.Printf("scenario=%s phase=preflight go=%s goos=%s goarch=%s gomaxprocs=%d seed=%d seconds=%d workers=%d fixed_rounds=%d heartbeat=%ds keys=%d\n",
		scenario, runtime.Version(), runtime.GOOS, runtime.GOARCH, runtime.GOMAXPROCS(0), seed, seconds, workers, fixed, hb, numKeys)

	deadline := time.Now().Add(time.Duration(seconds) * time.Second)
	ws := make([]*worker, workers)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		w := &worker{id: int64(i), seed: seed, rng: seed*10000 + uint64(i)*7919 + 1, db: db}
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
	go verifier(db, seed, stop, &helpers)
	go func() {
		defer helpers.Done()
		watchdog(time.Duration(hb)*time.Second, stop)
	}()

	wg.Wait()
	close(stop)
	helpers.Wait()

	db.finalize()
	finalVerify(db)

	n := waitGoroutines(baseGoroutines, 10*time.Second)
	if n > baseGoroutines+2 {
		failf("scenario=%s phase=shutdown expected=goroutines<=%d actual=%d: goroutine leak",
			scenario, baseGoroutines+2, n)
	}

	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	fmt.Printf("metrics: ops=%d puts=%d deletes=%d gets=%d snapshot_scans=%d snaps_created=%d snaps_verified=%d flushes=%d compactions=%d compacted_entries_dropped=%d max_table_entries=%d wal_checks=%d checks=%d gc_churn=%d beats=%d heap_alloc=%d heap_objects=%d num_gc=%d goroutines=%d\n",
		atomic.LoadInt64(&ops), atomic.LoadInt64(&puts), atomic.LoadInt64(&dels), atomic.LoadInt64(&gets),
		atomic.LoadInt64(&scans), atomic.LoadInt64(&snapsCreated), atomic.LoadInt64(&snapsVerified),
		atomic.LoadInt64(&flushes), atomic.LoadInt64(&compactions), atomic.LoadInt64(&compactedDrop),
		atomic.LoadInt64(&maxTableEntries), atomic.LoadInt64(&walChecks), atomic.LoadInt64(&checks),
		atomic.LoadInt64(&gcCycles), atomic.LoadInt64(&beats), m.HeapAlloc, m.HeapObjects, m.NumGC, n)
	fmt.Println("STRESS PASS n09_lsm_storage")
}
