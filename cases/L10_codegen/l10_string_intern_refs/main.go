// l10_string_intern_refs: generated-code string interning table with reference
// counting, collision chains, non-reusable IDs, freeze, snapshots and a GC
// lifecycle: released entries are tombstoned, live values survive buffer reuse.
package main

import (
	"fmt"
	"math/rand"
	"os"
	"runtime"
	"sort"
	"strconv"
)

var (
	failed int
	seed   = int64(1)
	gcSink [][]byte
)

func ck(ok bool, msg string) {
	if !ok {
		failed++
		fmt.Println("FAIL:", msg)
	}
}

func ckSeed(ok bool, msg string) {
	if !ok {
		failed++
		fmt.Printf("FAIL: %s (UAF_SEED=%d)\n", msg, seed)
	}
}

func gcStress() {
	for i := 0; i < 16; i++ {
		b := make([]byte, 4096)
		b[0] = byte(i)
		b[len(b)-1] = byte(i * 11)
		gcSink = append(gcSink, b)
	}
	for i := range gcSink {
		gcSink[i] = nil
	}
	gcSink = gcSink[:0]
	runtime.GC()
	runtime.GC()
}

func finish(name string) {
	if failed != 0 {
		fmt.Printf("FAILED %s (%d failed checks)\n", name, failed)
		os.Exit(1)
	}
	fmt.Println("ALL PASS " + name)
}

func readSeed() int64 {
	s := os.Getenv("UAF_SEED")
	if s == "" {
		return 1
	}
	v, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 1
	}
	return v
}

func envInt(name string, def int) int {
	s := os.Getenv(name)
	if s == "" {
		return def
	}
	v, err := strconv.Atoi(s)
	if err != nil || v < 0 {
		return def
	}
	return v
}

var (
	errFrozen  = fmt.Errorf("intern table frozen")
	errUnknown = fmt.Errorf("unknown or released id")
)

type hashFn func(string) uint64

func fnv1a(s string) uint64 {
	var h uint64 = 1469598103934665603
	for i := 0; i < len(s); i++ {
		h ^= uint64(s[i])
		h *= 1099511628211
	}
	return h
}

// weakHash intentionally collapses strings of equal length into few buckets.
func weakHash(s string) uint64 { return uint64(len(s) % 3) }

type internEntry struct {
	s    string
	id   uint32
	refs int
}

type internTable struct {
	buckets  [][]*internEntry
	byID     map[uint32]*internEntry
	hash     hashFn
	nextID   uint32
	issued   uint32
	frozen   bool
	numFinds int
}

func newInternTable(bucketCount int, h hashFn) *internTable {
	if bucketCount < 1 {
		bucketCount = 1
	}
	return &internTable{
		buckets: make([][]*internEntry, bucketCount),
		byID:    map[uint32]*internEntry{},
		hash:    h,
		nextID:  1,
	}
}

func (t *internTable) bucket(s string) int {
	return int(t.hash(s) % uint64(len(t.buckets)))
}

func (t *internTable) find(s string) *internEntry {
	b := t.buckets[t.bucket(s)]
	for _, e := range b {
		t.numFinds++
		if e.s == s {
			return e
		}
	}
	return nil
}

func (t *internTable) Acquire(s string) (uint32, error) {
	if e := t.find(s); e != nil {
		e.refs++
		return e.id, nil
	}
	if t.frozen {
		return 0, errFrozen
	}
	e := &internEntry{s: s, id: t.nextID, refs: 1}
	t.nextID++
	t.issued++
	t.buckets[t.bucket(s)] = append(t.buckets[t.bucket(s)], e)
	t.byID[e.id] = e
	return e.id, nil
}

func (t *internTable) Release(id uint32) error {
	e, ok := t.byID[id]
	if !ok {
		return errUnknown
	}
	e.refs--
	if e.refs > 0 {
		return nil
	}
	if e.refs < 0 {
		return fmt.Errorf("negative reference count")
	}
	// Remove from its bucket; the ID stays reserved and is never reused.
	b := t.buckets[t.bucket(e.s)]
	for i, x := range b {
		if x == e {
			b[i] = nil
			copy(b[i:], b[i+1:])
			b = b[:len(b)-1]
			break
		}
	}
	t.buckets[t.bucket(e.s)] = b
	delete(t.byID, id)
	return nil
}

func (t *internTable) Lookup(s string) (uint32, bool) {
	e := t.find(s)
	if e == nil {
		return 0, false
	}
	return e.id, true
}

func (t *internTable) ByID(id uint32) (string, int, bool) {
	e, ok := t.byID[id]
	if !ok {
		return "", 0, false
	}
	return e.s, e.refs, true
}

func (t *internTable) Freeze() { t.frozen = true }

func (t *internTable) LiveCount() int { return len(t.byID) }

func (t *internTable) IssuedCount() uint32 { return t.issued }

type snapshotEntry struct {
	id   uint32
	refs int
	s    string
}

func (t *internTable) Snapshot() []snapshotEntry {
	out := make([]snapshotEntry, 0, len(t.byID))
	for _, e := range t.byID {
		out = append(out, snapshotEntry{id: e.id, refs: e.refs, s: e.s})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].id < out[j].id })
	return out
}

// ---------- model for the seeded torture phase ----------

type model struct {
	refs   map[string]int
	ids    map[string]uint32
	live   map[uint32]string
	issued uint32
	nextID uint32
}

func newModel() *model {
	return &model{
		refs:   map[string]int{},
		ids:    map[string]uint32{},
		live:   map[uint32]string{},
		nextID: 1,
	}
}

func (m *model) acquire(s string) uint32 {
	if id, ok := m.ids[s]; ok {
		m.refs[s]++
		return id
	}
	id := m.nextID
	m.nextID++
	m.issued++
	m.refs[s] = 1
	m.ids[s] = id
	m.live[id] = s
	return id
}

func (m *model) release(id uint32) {
	s, ok := m.live[id]
	if !ok {
		return
	}
	m.refs[s]--
	if m.refs[s] == 0 {
		delete(m.refs, s)
		delete(m.ids, s)
		delete(m.live, id)
	}
}

func reconcile(t *internTable, m *model, tag string) bool {
	ok := true
	if t.LiveCount() != len(m.live) {
		ckSeed(false, tag+": live count")
		ok = false
	}
	if t.IssuedCount() != m.issued {
		ckSeed(false, tag+": issued count")
		ok = false
	}
	for id, s := range m.live {
		gotS, gotRefs, found := t.ByID(id)
		if !found || gotS != s || gotRefs != m.refs[s] {
			ckSeed(false, fmt.Sprintf("%s: id %d state", tag, id))
			ok = false
		}
		if gotID, found := t.Lookup(s); !found || gotID != id {
			ckSeed(false, fmt.Sprintf("%s: lookup %q", tag, s))
			ok = false
		}
	}
	for s, refs := range m.refs {
		if refs <= 0 {
			ckSeed(false, tag+": non-positive model refs")
			ok = false
			continue
		}
		if _, found := t.Lookup(s); !found {
			ckSeed(false, fmt.Sprintf("%s: missing string %q", tag, s))
			ok = false
		}
	}
	snap := t.Snapshot()
	if len(snap) != len(m.live) {
		ckSeed(false, tag+": snapshot size")
		ok = false
	}
	for i := 1; i < len(snap); i++ {
		if snap[i-1].id >= snap[i].id {
			ckSeed(false, tag+": snapshot not sorted")
			ok = false
			break
		}
	}
	return ok
}

func main() {
	seed = readSeed()

	// 1. Content equality across distinct backing arrays -> one ID, refcount 2.
	t := newInternTable(4, fnv1a)
	b1 := []byte("alpha")
	b2 := []byte("alpha")
	id1, err := t.Acquire(string(b1))
	ck(err == nil, "acquire alpha")
	id2, err := t.Acquire(string(b2))
	ck(err == nil && id1 == id2, "equal content shares ID")
	_, refs, _ := t.ByID(id1)
	ck(refs == 2, "reference count reflects two acquisitions")
	ck(t.Release(id1) == nil, "release first reference")
	_, refs, okLive := t.ByID(id1)
	ck(okLive && refs == 1, "entry stays live with one reference")
	ck(t.Release(id2) == nil, "release second reference")
	_, _, okLive = t.ByID(id1)
	ck(!okLive, "zero references removes entry")
	_, found := t.Lookup("alpha")
	ck(!found, "released string no longer resolvable")
	firstID := id1
	id3, err := t.Acquire("alpha")
	ck(err == nil && id3 != firstID && id3 > firstID, "released ID is never reused")
	ck(t.IssuedCount() == 2, "issued ID counter advances")
	ck(t.Release(id3) == nil, "release re-interned value")
	ck(t.Release(9999) == errUnknown, "unknown id release rejected")

	// 2. Collision-heavy table: 200 same-length strings in a tiny weak-hash table.
	ct := newInternTable(4, weakHash)
	var ids []uint32
	for i := 0; i < 200; i++ {
		id, err := ct.Acquire(fmt.Sprintf("k%03d", i))
		if err != nil {
			ck(false, "collision acquire")
		}
		ids = append(ids, id)
	}
	for i := 0; i < 200; i++ {
		id, found := ct.Lookup(fmt.Sprintf("k%03d", i))
		ck(found && id == ids[i], fmt.Sprintf("collision lookup %d", i))
	}
	ck(ct.LiveCount() == 200, "collision table live count")
	// Release every even entry.
	for i := 0; i < 200; i += 2 {
		ck(ct.Release(ids[i]) == nil, "collision release")
	}
	ck(ct.LiveCount() == 100, "half of collision entries released")
	for i := 0; i < 200; i++ {
		_, found := ct.Lookup(fmt.Sprintf("k%03d", i))
		ck(found == (i%2 == 1), fmt.Sprintf("collision liveness %d", i))
	}
	// Re-intern evens: new IDs, never the old ones.
	for i := 0; i < 200; i += 2 {
		id, err := ct.Acquire(fmt.Sprintf("k%03d", i))
		ck(err == nil && id != ids[i], fmt.Sprintf("collision ID not reused %d", i))
	}
	ck(ct.LiveCount() == 200 && ct.IssuedCount() == 300, "tombstoned IDs stay reserved")
	snap := ct.Snapshot()
	// Snapshot must be a stable copy sorted by ID.
	last := uint32(0)
	for _, e := range snap {
		ck(e.id > last, "snapshot strictly increasing IDs")
		last = e.id
	}
	if len(snap) > 0 {
		original := snap[0].s
		snap[0].s = "mutated"
		if got, _, _ := ct.ByID(snap[0].id); got != original {
			ck(false, "snapshot mutation leaks into table")
		}
	}
	for i := 0; i < 200; i++ {
		ck(ct.Release(func() uint32 {
			id, _ := ct.Lookup(fmt.Sprintf("k%03d", i))
			return id
		}()) == nil, "cleanup release")
	}
	ck(ct.LiveCount() == 0, "collision table empty after cleanup")

	// 3. Freeze: existing entries can still be acquired/released; new strings fail.
	ft := newInternTable(8, fnv1a)
	fid, err := ft.Acquire("frozen")
	ck(err == nil, "freeze setup")
	ft.Freeze()
	fid2, err := ft.Acquire("frozen")
	ck(err == nil && fid2 == fid, "frozen table shares existing entry")
	_, err = ft.Acquire("new")
	ck(err == errFrozen, "frozen table rejects new strings")
	ck(ft.Release(fid) == nil && ft.Release(fid2) == nil, "frozen table still releases")
	_, err = ft.Acquire("frozen")
	ck(err == errFrozen, "frozen table cannot re-insert released string")

	// 4. Exact byte semantics: empty, embedded NUL, invalid UTF-8, long strings.
	bt := newInternTable(16, fnv1a)
	cases := []string{"", "a\x00b", "\xff\xfe\xfd", string(make([]byte, 8192))}
	var baseIDs []uint32
	for i, s := range cases {
		id, err := bt.Acquire(s)
		ck(err == nil, fmt.Sprintf("byte case %d acquire", i))
		baseIDs = append(baseIDs, id)
	}
	// The long all-zero string is 8192 NUL bytes; distinct lengths/content do not merge.
	otherID, _ := bt.Acquire("")
	ck(otherID == baseIDs[0], "empty string interning")
	for i, s := range cases {
		id, found := bt.Lookup(s)
		ck(found && id == baseIDs[i], fmt.Sprintf("byte case %d lookup", i))
	}
	_, found = bt.Lookup("A")
	ck(!found, "case-sensitive table")
	for _, id := range baseIDs {
		ck(bt.Release(id) == nil, "byte case release")
	}
	ck(bt.Release(otherID) == nil, "empty string second reference release")
	ck(bt.LiveCount() == 0, "byte table empty")

	// 5. GC lifecycle: held strings survive source-buffer poisoning.
	gt := newInternTable(8, fnv1a)
	var gids []uint32
	var sources [][]byte
	for i := 0; i < 300; i++ {
		buf := make([]byte, 32)
		copy(buf, fmt.Sprintf("%08d", i))
		for j := 8; j < len(buf); j++ {
			buf[j] = byte('A' + (i+j)%26)
		}
		sources = append(sources, buf)
		id, err := gt.Acquire(string(buf))
		ck(err == nil, "gc acquire")
		gids = append(gids, id)
	}
	for _, b := range sources {
		for i := range b {
			b[i] = 0xEE
		}
	}
	gcStress()
	ck(gt.LiveCount() == 300, "gc table live after buffers poisoned")
	for i := 0; i < 300; i += 37 {
		s, refs, okLive := gt.ByID(gids[i])
		ck(okLive && refs == 1 && len(s) == 32, "gc value survives buffer poison")
	}
	for _, id := range gids {
		ck(gt.Release(id) == nil, "gc release")
	}
	ck(gt.LiveCount() == 0, "gc table drained")

	// 6. Seeded torture against an independent reference-count model.
	tt := newInternTable(4, weakHash)
	m := newModel()
	pool := make([]string, 0, 64)
	for i := 0; i < 32; i++ {
		pool = append(pool, fmt.Sprintf("k%03d", i))
	}
	pool = append(pool, "", "a\x00b", "\xff\xfe")
	rounds := envInt("TORTURE_ROUNDS", 400)
	r := rand.New(rand.NewSource(seed))
	okRounds := 0
	for round := 0; round < rounds; round++ {
		switch r.Intn(10) {
		case 0, 1, 2, 3, 4:
			target := pool[r.Intn(len(pool))]
			id, err := tt.Acquire(target)
			want := m.acquire(target)
			if err != nil || id != want {
				ckSeed(false, fmt.Sprintf("torture %d acquire", round))
			}
		case 5, 6, 7:
			if len(m.live) > 0 {
				var liveIDs []uint32
				for id := range m.live {
					liveIDs = append(liveIDs, id)
				}
				sort.Slice(liveIDs, func(i, j int) bool { return liveIDs[i] < liveIDs[j] })
				id := liveIDs[r.Intn(len(liveIDs))]
				if err := tt.Release(id); err != nil {
					ckSeed(false, fmt.Sprintf("torture %d release", round))
				}
				m.release(id)
			}
		default:
			target := pool[r.Intn(len(pool))]
			got, found := tt.Lookup(target)
			wantID, inModel := m.ids[target]
			if found != inModel {
				ckSeed(false, fmt.Sprintf("torture %d lookup presence", round))
			}
			if found && got != wantID {
				ckSeed(false, fmt.Sprintf("torture %d lookup id", round))
			}
		}
		if round%25 == 0 {
			gcStress()
		}
		if round%50 == 0 && !reconcile(tt, m, fmt.Sprintf("torture %d", round)) {
			break
		}
		okRounds++
	}
	ck(okRounds == rounds, "torture: all rounds completed")
	ck(reconcile(tt, m, "torture final"), "torture: final model reconciliation")

	// Drain and confirm the table reflects the model.
	var drain []uint32
	for id := range m.live {
		drain = append(drain, id)
	}
	sort.Slice(drain, func(i, j int) bool { return drain[i] < drain[j] })
	for _, id := range drain {
		s := m.live[id]
		for m.refs[s] > 0 {
			if err := tt.Release(id); err != nil {
				ckSeed(false, "drain release")
				break
			}
			m.release(id)
		}
	}
	gcStress()
	ck(tt.LiveCount() == 0 && m.issued == tt.IssuedCount(), "drained table and stable issued count")

	finish("l10_string_intern_refs")
}
