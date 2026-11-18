// L01: 泛型分段 LRU（SLRU）：probation/protected 双段淘汰 + 引用模型逐步比对，跨 GC 校验缓存指针。
// 与 l01_generic_map_cache 的单段 LRU 不同，本例实现 SLRU 的晋升/降级/段容量约束。
package main

import (
	"fmt"
	"os"
	"runtime"
	"sort"
	"strconv"
	"sync/atomic"
)

type rec struct {
	id   int64
	tag  string
	blob []byte
}

type segEntry[K comparable, V any] struct {
	key        K
	val        *V
	prev, next *segEntry[K, V]
	prot       bool
}

type segList[K comparable, V any] struct {
	root segEntry[K, V]
	n    int
}

func (l *segList[K, V]) init() {
	l.root.next = &l.root
	l.root.prev = &l.root
}

func (l *segList[K, V]) pushFront(e *segEntry[K, V]) {
	e.prev = &l.root
	e.next = l.root.next
	l.root.next.prev = e
	l.root.next = e
	l.n++
}

func (l *segList[K, V]) unlink(e *segEntry[K, V]) {
	e.prev.next = e.next
	e.next.prev = e.prev
	e.prev, e.next = nil, nil
	l.n--
}

func (l *segList[K, V]) back() *segEntry[K, V] {
	if l.n == 0 {
		return nil
	}
	return l.root.prev
}

// SLRU 是两段式 LRU：新条目进 probation，命中后晋升 protected；
// protected 满时把最旧条目降级回 probation，淘汰只发生在 probation 尾部。
type SLRU[K comparable, V any] struct {
	capacity int
	protCap  int
	m        map[K]*segEntry[K, V]
	prob     segList[K, V]
	prot     segList[K, V]
	hits     int
	misses   int
}

func NewSLRU[K comparable, V any](capacity int) *SLRU[K, V] {
	if capacity < 2 {
		capacity = 2
	}
	c := &SLRU[K, V]{
		capacity: capacity,
		protCap:  capacity / 2,
		m:        make(map[K]*segEntry[K, V]),
	}
	c.prob.init()
	c.prot.init()
	return c
}

func (c *SLRU[K, V]) promote(e *segEntry[K, V]) {
	if e.prot {
		c.prot.unlink(e)
		c.prot.pushFront(e)
		return
	}
	c.prob.unlink(e)
	if c.prot.n >= c.protCap {
		if d := c.prot.back(); d != nil {
			c.prot.unlink(d)
			d.prot = false
			c.prob.pushFront(d)
		}
	}
	e.prot = true
	c.prot.pushFront(e)
}

func (c *SLRU[K, V]) Get(k K) (*V, bool) {
	e, ok := c.m[k]
	if !ok {
		c.misses++
		return nil, false
	}
	c.hits++
	c.promote(e)
	return e.val, true
}

// Peek 不改变段归属与命中统计，用于 GC 后核对。
func (c *SLRU[K, V]) Peek(k K) (*V, bool) {
	e, ok := c.m[k]
	if !ok {
		return nil, false
	}
	return e.val, true
}

func (c *SLRU[K, V]) Put(k K, v *V) (*V, bool) {
	if e, ok := c.m[k]; ok {
		old := e.val
		e.val = v
		c.promote(e)
		return old, true
	}
	e := &segEntry[K, V]{key: k, val: v}
	c.m[k] = e
	c.prob.pushFront(e)
	if len(c.m) > c.capacity {
		if victim := c.prob.back(); victim != nil {
			c.prob.unlink(victim)
			delete(c.m, victim.key)
		}
	}
	return nil, false
}

func (c *SLRU[K, V]) Delete(k K) (*V, bool) {
	e, ok := c.m[k]
	if !ok {
		return nil, false
	}
	if e.prot {
		c.prot.unlink(e)
	} else {
		c.prob.unlink(e)
	}
	delete(c.m, k)
	return e.val, true
}

func (c *SLRU[K, V]) Len() int          { return len(c.m) }
func (c *SLRU[K, V]) ProtectedLen() int { return c.prot.n }
func (c *SLRU[K, V]) ProbationLen() int { return c.prob.n }
func (c *SLRU[K, V]) Hits() int         { return c.hits }
func (c *SLRU[K, V]) Misses() int       { return c.misses }

// model 是独立的顺序表引用实现，用于逐步比对 SLRU 语义。
type model struct {
	capacity int
	protCap  int
	m        map[int64]*rec
	prob     []int64 // 最新在前
	prot     []int64 // 最新在前
	hits     int
	misses   int
}

func newModel(capacity int) *model {
	if capacity < 2 {
		capacity = 2
	}
	return &model{capacity: capacity, protCap: capacity / 2, m: make(map[int64]*rec)}
}

func indexOf(s []int64, k int64) int {
	for i, v := range s {
		if v == k {
			return i
		}
	}
	return -1
}

func moveFront(s []int64, i int) []int64 {
	k := s[i]
	copy(s[1:i+1], s[:i])
	s[0] = k
	return s
}

func removeIdx(s []int64, i int) []int64 {
	copy(s[i:], s[i+1:])
	return s[:len(s)-1]
}

func prepend(s []int64, k int64) []int64 {
	s = append(s, 0)
	copy(s[1:], s[:len(s)-1])
	s[0] = k
	return s
}

func (mo *model) promote(k int64) {
	if i := indexOf(mo.prot, k); i >= 0 {
		mo.prot = moveFront(mo.prot, i)
		return
	}
	i := indexOf(mo.prob, k)
	if i < 0 {
		return
	}
	mo.prob = removeIdx(mo.prob, i)
	if len(mo.prot) >= mo.protCap {
		d := mo.prot[len(mo.prot)-1]
		mo.prot = mo.prot[:len(mo.prot)-1]
		mo.prob = prepend(mo.prob, d)
	}
	mo.prot = prepend(mo.prot, k)
}

func (mo *model) Get(k int64) (*rec, bool) {
	v, ok := mo.m[k]
	if !ok {
		mo.misses++
		return nil, false
	}
	mo.hits++
	mo.promote(k)
	return v, true
}

func (mo *model) Put(k int64, v *rec) (*rec, bool) {
	if old, ok := mo.m[k]; ok {
		mo.m[k] = v
		mo.promote(k)
		return old, true
	}
	mo.m[k] = v
	mo.prob = prepend(mo.prob, k)
	if len(mo.m) > mo.capacity {
		victim := mo.prob[len(mo.prob)-1]
		mo.prob = mo.prob[:len(mo.prob)-1]
		delete(mo.m, victim)
	}
	return nil, false
}

func (mo *model) Delete(k int64) (*rec, bool) {
	v, ok := mo.m[k]
	if !ok {
		return nil, false
	}
	delete(mo.m, k)
	if i := indexOf(mo.prot, k); i >= 0 {
		mo.prot = removeIdx(mo.prot, i)
	} else if i := indexOf(mo.prob, k); i >= 0 {
		mo.prob = removeIdx(mo.prob, i)
	}
	return v, true
}

var (
	failed  int32
	gcSink  [][]byte
	recSink []*rec
	uafSeed int64 = 1
)

func ck(ok bool, msg string) {
	if !ok {
		atomic.AddInt32(&failed, 1)
		fmt.Println("FAIL:", msg)
	}
}

func gcStress() {
	for i := 0; i < 8; i++ {
		b := make([]byte, 2048)
		b[0] = byte(i)
		gcSink = append(gcSink[:0], b)
	}
	gcSink = gcSink[:0]
	runtime.GC()
	runtime.GC()
}

func finish(name string) {
	if n := atomic.LoadInt32(&failed); n != 0 {
		fmt.Printf("FAILED %s seed=%d (%d failed checks)\n", name, uafSeed, n)
		os.Exit(1)
	}
	fmt.Println("ALL PASS " + name)
}

func envSeed() int64 {
	s := os.Getenv("UAF_SEED")
	if s == "" {
		return 1
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil || n <= 0 {
		return 1
	}
	return n
}

func nextRand(state *uint64) uint64 {
	*state = *state*6364136223846793005 + 1442695040888963407
	return *state >> 33
}

func sortedModelKeys(m map[int64]*rec) []int64 {
	keys := make([]int64, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
	return keys
}

func main() {
	uafSeed = envSeed()
	nrec := 48 + int(uafSeed%9)
	recs := make([]*rec, nrec)
	for i := range recs {
		recs[i] = &rec{id: int64(i), tag: fmt.Sprintf("r%d", i), blob: make([]byte, 11+i%9)}
		recs[i].blob[0] = byte(i)
		recs[i].blob[len(recs[i].blob)-1] = byte(i * 5)
	}
	recSink = recs

	capacity := 24 + int(uafSeed%8)
	keySpace := capacity * 3
	cache := NewSLRU[int64, rec](capacity)
	mo := newModel(capacity)
	state := uint64(uafSeed)*0x9e3779b97f4a7c15 + 0x5deece66d
	ops := 900 + int(uafSeed%100)

	for step := 0; step < ops; step++ {
		k := int64(nextRand(&state) % uint64(keySpace))
		if nextRand(&state)%10 < 3 {
			v := recs[nextRand(&state)%uint64(nrec)]
			oldC, exC := cache.Put(k, v)
			oldM, exM := mo.Put(k, v)
			ck(exC == exM, fmt.Sprintf("Put presence mismatch step=%d key=%d", step, k))
			ck(oldC == oldM, fmt.Sprintf("Put old pointer mismatch step=%d key=%d", step, k))
		} else {
			gp, gok := cache.Get(k)
			mp, mok := mo.Get(k)
			ck(gok == mok, fmt.Sprintf("Get hit mismatch step=%d key=%d", step, k))
			ck(gp == mp, fmt.Sprintf("Get pointer mismatch step=%d key=%d", step, k))
			if gp != nil {
				ck(gp.id == mp.id && gp.tag == mp.tag, "Get payload corrupted")
			}
		}
		ck(cache.Len() == len(mo.m), fmt.Sprintf("len got=%d want=%d step=%d", cache.Len(), len(mo.m), step))
		ck(cache.ProtectedLen() == len(mo.prot), fmt.Sprintf("protected len got=%d want=%d step=%d", cache.ProtectedLen(), len(mo.prot), step))
		ck(cache.ProbationLen() == len(mo.prob), fmt.Sprintf("probation len got=%d want=%d step=%d", cache.ProbationLen(), len(mo.prob), step))
		ck(cache.Hits() == mo.hits && cache.Misses() == mo.misses, "hit/miss counters diverged")

		if step%96 == 95 {
			gcStress()
			for _, mk := range sortedModelKeys(mo.m) {
				cp, ok := cache.Peek(mk)
				want := mo.m[mk]
				ck(ok && cp == want, fmt.Sprintf("post-GC pointer mismatch key=%d", mk))
				ck(cp != nil && cp.id == want.id && cp.tag == want.tag, "post-GC payload corrupted")
				ck(cp != nil && len(cp.blob) == len(want.blob) && cp.blob[0] == byte(want.id), "post-GC blob corrupted")
			}
			// 引用模型的段内顺序必须仍能通过 Peek 一一对应。
			for _, key := range mo.prot {
				cp, ok := cache.Peek(key)
				ck(ok && cp == mo.m[key], "protected segment lost an entry after GC")
			}
			for _, key := range mo.prob {
				cp, ok := cache.Peek(key)
				ck(ok && cp == mo.m[key], "probation segment lost an entry after GC")
			}
		}
	}

	// 删除每 7 个 key 中的一个（跨段），GC 后核对剩余集合。
	deleted := make(map[int64]bool)
	for k := int64(0); k < int64(keySpace); k += 7 {
		cp, cok := cache.Delete(k)
		mp, mok := mo.Delete(k)
		ck(cok == mok, fmt.Sprintf("Delete presence mismatch key=%d", k))
		ck(cp == mp, fmt.Sprintf("Delete pointer mismatch key=%d", k))
		deleted[k] = true
	}
	gcStress()
	ck(cache.Len() == len(mo.m), "len after delete mismatch")
	for _, mk := range sortedModelKeys(mo.m) {
		cp, ok := cache.Peek(mk)
		ck(ok && cp == mo.m[mk], fmt.Sprintf("surviving key %d changed pointer after delete+GC", mk))
	}
	for k := range deleted {
		_, ok := cache.Peek(k)
		ck(!ok, fmt.Sprintf("deleted key %d resurrected", k))
	}

	// 第二次实例化 SLRU[string,rec]：同一批负载，不同 key 类型与容量。
	sc := NewSLRU[string, rec](8)
	keys := make([]string, 24)
	for i := range keys {
		keys[i] = fmt.Sprintf("k%02d", i)
	}
	for i := 0; i < 24; i++ {
		sc.Put(keys[i], recs[i])
	}
	gcStress()
	ck(sc.Len() == 8, fmt.Sprintf("string slru len got=%d want=8", sc.Len()))
	visible := 0
	for i := 16; i < 24; i++ {
		p, ok := sc.Peek(keys[i])
		ck(ok && p == recs[i], "string slru recent entry pointer changed")
		if ok {
			visible++
		}
	}
	ck(visible == 8, fmt.Sprintf("string slru visible got=%d want=8", visible))
	for i := 0; i < 16; i++ {
		_, ok := sc.Peek(keys[i])
		ck(!ok, "string slru evicted key resurrected")
	}
	hits := 0
	for i := 16; i < 24; i++ {
		p, ok := sc.Get(keys[i])
		ck(ok && p == recs[i], "string slru Get pointer mismatch")
		hits++
	}
	ck(sc.Hits() == hits, "string slru hit counter corrupted")
	ck(sc.ProtectedLen() <= sc.capacity/2, "string slru protected segment over capacity")
	gcStress()
	for i := 16; i < 24; i++ {
		p, ok := sc.Peek(keys[i])
		ck(ok && p == recs[i], "string slru pointer changed after second GC")
	}

	finish("l01_lru_segmented")
}
