// L01: 泛型 map 缓存（LRU）：命中/未命中/淘汰/删除与引用模型逐步对比，跨 GC 校验缓存指针。
package main

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"sync/atomic"
)

type rec struct {
	id   int64
	tag  string
	blob []byte
}

type lruEntry[K comparable, V any] struct {
	key        K
	val        *V
	prev, next *lruEntry[K, V]
}

// Cache 是 map[K]*V + 手写双向链表的泛型 LRU，模拟 pgx encode plan cache /
// kitex lbcache 按 key 共享实例的场景。
type Cache[K comparable, V any] struct {
	capacity     int
	m            map[K]*lruEntry[K, V]
	head, tail   *lruEntry[K, V] // 哨兵：head.next 最新，tail.prev 最旧
	hits, misses int
}

func NewCache[K comparable, V any](capacity int) *Cache[K, V] {
	if capacity < 1 {
		capacity = 1
	}
	h := &lruEntry[K, V]{}
	t := &lruEntry[K, V]{}
	h.next = t
	t.prev = h
	return &Cache[K, V]{capacity: capacity, m: make(map[K]*lruEntry[K, V]), head: h, tail: t}
}

func (c *Cache[K, V]) unlink(e *lruEntry[K, V]) {
	e.prev.next = e.next
	e.next.prev = e.prev
}

func (c *Cache[K, V]) pushFront(e *lruEntry[K, V]) {
	e.next = c.head.next
	e.prev = c.head
	c.head.next.prev = e
	c.head.next = e
}

func (c *Cache[K, V]) Get(k K) (*V, bool) {
	e, ok := c.m[k]
	if !ok {
		c.misses++
		return nil, false
	}
	c.hits++
	c.unlink(e)
	c.pushFront(e)
	return e.val, true
}

// Peek 不改变 LRU 顺序与命中统计，用于 GC 后核对。
func (c *Cache[K, V]) Peek(k K) (*V, bool) {
	e, ok := c.m[k]
	if !ok {
		return nil, false
	}
	return e.val, true
}

func (c *Cache[K, V]) Put(k K, v *V) (*V, bool) {
	if e, ok := c.m[k]; ok {
		old := e.val
		e.val = v
		c.unlink(e)
		c.pushFront(e)
		return old, true
	}
	e := &lruEntry[K, V]{key: k, val: v}
	c.m[k] = e
	c.pushFront(e)
	if len(c.m) > c.capacity {
		oldest := c.tail.prev
		c.unlink(oldest)
		delete(c.m, oldest.key)
	}
	return nil, false
}

func (c *Cache[K, V]) Delete(k K) (*V, bool) {
	e, ok := c.m[k]
	if !ok {
		return nil, false
	}
	c.unlink(e)
	delete(c.m, k)
	return e.val, true
}

func (c *Cache[K, V]) Len() int    { return len(c.m) }
func (c *Cache[K, V]) Hits() int   { return c.hits }
func (c *Cache[K, V]) Misses() int { return c.misses }

// model 是独立的引用实现，用于逐步比对淘汰/命中语义。
type model struct {
	capacity int
	m        map[int64]*rec
	order    []int64 // order[0] 最新
	hits     int
	misses   int
}

func newModel(capacity int) *model {
	return &model{capacity: capacity, m: make(map[int64]*rec)}
}

func (mo *model) touch(k int64) {
	for i, x := range mo.order {
		if x == k {
			copy(mo.order[1:i+1], mo.order[:i])
			mo.order[0] = k
			return
		}
	}
}

func (mo *model) Get(k int64) (*rec, bool) {
	v, ok := mo.m[k]
	if !ok {
		mo.misses++
		return nil, false
	}
	mo.hits++
	mo.touch(k)
	return v, true
}

func (mo *model) Put(k int64, v *rec) {
	if _, ok := mo.m[k]; ok {
		mo.m[k] = v
		mo.touch(k)
		return
	}
	mo.m[k] = v
	mo.order = append(mo.order, 0)
	copy(mo.order[1:], mo.order[:len(mo.order)-1])
	mo.order[0] = k
	if len(mo.m) > mo.capacity {
		oldest := mo.order[len(mo.order)-1]
		mo.order = mo.order[:len(mo.order)-1]
		delete(mo.m, oldest)
	}
}

func (mo *model) Delete(k int64) {
	if _, ok := mo.m[k]; !ok {
		return
	}
	delete(mo.m, k)
	for i, x := range mo.order {
		if x == k {
			mo.order = append(mo.order[:i], mo.order[i+1:]...)
			return
		}
	}
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

func main() {
	uafSeed = envSeed()

	const nrec = 40
	recs := make([]*rec, nrec)
	for i := range recs {
		recs[i] = &rec{id: int64(i), tag: fmt.Sprintf("r%d", i), blob: make([]byte, 12+i%9)}
		recs[i].blob[0] = byte(i)
		recs[i].blob[len(recs[i].blob)-1] = byte(i * 3)
	}
	recSink = recs

	capacity := 48 + int(uafSeed%8)
	keySpace := capacity * 3
	cache := NewCache[int64, rec](capacity)
	mo := newModel(capacity)
	state := uint64(uafSeed)*0x9e3779b97f4a7c15 + 0xdeadbeef

	const ops = 1200
	for step := 0; step < ops; step++ {
		k := int64(nextRand(&state) % uint64(keySpace))
		if nextRand(&state)%10 < 3 {
			v := recs[nextRand(&state)%uint64(nrec)]
			oldC, hadC := cache.Put(k, v)
			oldM, hadM := mo.m[k]
			ck(hadC == hadM, fmt.Sprintf("Put old-presence mismatch at step %d", step))
			ck(oldC == oldM, "Put old pointer mismatch")
			mo.Put(k, v)
		} else {
			gp, gok := cache.Get(k)
			mp, mok := mo.Get(k)
			ck(gok == mok, fmt.Sprintf("Get hit mismatch at step %d key=%d", step, k))
			ck(gp == mp, "Get pointer mismatch")
			if gok && gp != nil {
				ck(gp.tag == fmt.Sprintf("r%d", gp.id), "cache payload tag corrupted")
			}
		}
		ck(cache.Len() == len(mo.m), fmt.Sprintf("cache len got=%d want=%d at step %d", cache.Len(), len(mo.m), step))
		ck(cache.Hits() == mo.hits, fmt.Sprintf("hits got=%d want=%d at step %d", cache.Hits(), mo.hits, step))
		ck(cache.Misses() == mo.misses, fmt.Sprintf("misses got=%d want=%d at step %d", cache.Misses(), mo.misses, step))

		if step%128 == 127 {
			gcStress()
			for mk, mp := range mo.m {
				cp, ok := cache.Peek(mk)
				ck(ok && cp == mp, fmt.Sprintf("post-GC cache pointer mismatch key=%d", mk))
				ck(cp != nil && cp.id == mp.id && cp.blob[0] == byte(mp.id), "post-GC cache payload corrupted")
			}
		}
	}

	// 删除部分 key，GC 后核对缓存与模型一致、被删项不可见。
	for k := int64(0); k < int64(keySpace); k += 5 {
		cp, cok := cache.Delete(k)
		mp, mok := mo.m[k]
		ck(cok == mok, "Delete presence mismatch")
		ck(cp == mp, "Delete returned wrong pointer")
		mo.Delete(k)
	}
	gcStress()
	ck(cache.Len() == len(mo.m), "cache len after delete mismatch")
	for mk, mp := range mo.m {
		cp, ok := cache.Peek(mk)
		ck(ok && cp == mp, "surviving key pointer changed after delete+GC")
	}
	_, zeroVisible := cache.Peek(0)
	ck(!zeroVisible, "deleted key 0 resurrected")

	// 第二次实例化 Cache[string,*rec]：同一批指针，不同 key 类型。
	sc := NewCache[string, rec](16)
	keys := make([]string, nrec)
	for i := range keys {
		keys[i] = fmt.Sprintf("k%02d", i)
	}
	perm := make([]int, nrec)
	for i := range perm {
		perm[i] = i
	}
	for i := nrec - 1; i > 0; i-- {
		j := int(nextRand(&state) % uint64(i+1))
		perm[i], perm[j] = perm[j], perm[i]
	}
	for _, idx := range perm {
		sc.Put(keys[idx], recs[idx])
	}
	gcStress()
	ck(sc.Len() == 16, fmt.Sprintf("string cache len got=%d want=16", sc.Len()))
	sum := 0
	for _, idx := range perm {
		if p, ok := sc.Peek(keys[idx]); ok {
			ck(p == recs[idx], "string cache pointer identity changed")
			ck(p.id == int64(idx) && p.tag == fmt.Sprintf("r%d", idx), "string cache payload corrupted")
			sum++
		}
	}
	ck(sum == 16, fmt.Sprintf("string cache visible entries got=%d want=16", sum))

	finish("l01_generic_map_cache")
}
