// L01: 泛型 TTL + LRU 缓存（容器 list + map 句柄，假时钟驱动）。
// 对应 gorm internal/lru 的 TTL/淘汰语义与 grpc-go timeoutCache 的到期行为。
package main

import (
	"container/list"
	"fmt"
	"os"
	"runtime"
	"strconv"
	"sync/atomic"
)

type ttlPayload struct {
	id   int64
	name string
	blob []byte
}

type ttlEntry[K comparable, V any] struct {
	key      K
	val      *V
	expireAt int64
}

type ttlCache[K comparable, V any] struct {
	cap          int
	ll           *list.List
	m            map[K]*list.Element
	now          int64
	hits, misses int
}

func newTTLCache[K comparable, V any](cap int, now int64) *ttlCache[K, V] {
	if cap < 1 {
		cap = 1
	}
	return &ttlCache[K, V]{cap: cap, ll: list.New(), m: make(map[K]*list.Element), now: now}
}

func (c *ttlCache[K, V]) Set(k K, v *V, ttl int64) {
	if ttl < 1 {
		ttl = 1
	}
	if e, ok := c.m[k]; ok {
		ent := e.Value.(*ttlEntry[K, V])
		ent.val = v
		ent.expireAt = c.now + ttl
		c.ll.MoveToFront(e)
		return
	}
	ent := &ttlEntry[K, V]{key: k, val: v, expireAt: c.now + ttl}
	c.m[k] = c.ll.PushFront(ent)
	if len(c.m) > c.cap {
		back := c.ll.Back()
		if back != nil {
			old := back.Value.(*ttlEntry[K, V])
			c.ll.Remove(back)
			delete(c.m, old.key)
		}
	}
}

func (c *ttlCache[K, V]) removeElement(e *list.Element) {
	ent := e.Value.(*ttlEntry[K, V])
	c.ll.Remove(e)
	delete(c.m, ent.key)
}

func (c *ttlCache[K, V]) Get(k K) (*V, bool) {
	e, ok := c.m[k]
	if !ok {
		c.misses++
		return nil, false
	}
	ent := e.Value.(*ttlEntry[K, V])
	if c.now >= ent.expireAt {
		c.removeElement(e)
		c.misses++
		return nil, false
	}
	c.hits++
	c.ll.MoveToFront(e)
	return ent.val, true
}

// Peek 不改变 LRU 顺序与统计；过期项返回 false 但保留待 Purge 清理。
func (c *ttlCache[K, V]) Peek(k K) (*V, bool) {
	e, ok := c.m[k]
	if !ok {
		return nil, false
	}
	ent := e.Value.(*ttlEntry[K, V])
	if c.now >= ent.expireAt {
		return nil, false
	}
	return ent.val, true
}

func (c *ttlCache[K, V]) Delete(k K) (*V, bool) {
	e, ok := c.m[k]
	if !ok {
		return nil, false
	}
	ent := e.Value.(*ttlEntry[K, V])
	c.removeElement(e)
	return ent.val, true
}

func (c *ttlCache[K, V]) Purge() int {
	n := 0
	var next *list.Element
	for e := c.ll.Back(); e != nil; e = next {
		next = e.Prev()
		ent := e.Value.(*ttlEntry[K, V])
		if c.now >= ent.expireAt {
			c.removeElement(e)
			n++
		}
	}
	return n
}

func (c *ttlCache[K, V]) Advance(dt int64) { c.now += dt }
func (c *ttlCache[K, V]) Len() int         { return len(c.m) }
func (c *ttlCache[K, V]) Hits() int        { return c.hits }
func (c *ttlCache[K, V]) Misses() int      { return c.misses }

func (c *ttlCache[K, V]) Keys() []K {
	out := make([]K, 0, len(c.m))
	for e := c.ll.Front(); e != nil; e = e.Next() {
		out = append(out, e.Value.(*ttlEntry[K, V]).key)
	}
	return out
}

type ttlModelEntry struct {
	val      *ttlPayload
	expireAt int64
}

type ttlModel struct {
	cap          int
	now          int64
	order        []int64 // front -> back
	m            map[int64]ttlModelEntry
	hits, misses int
}

func newTTLModel(cap int, now int64) *ttlModel {
	return &ttlModel{cap: cap, now: now, m: make(map[int64]ttlModelEntry)}
}

func (mo *ttlModel) touch(k int64) {
	for i, x := range mo.order {
		if x == k {
			copy(mo.order[1:i+1], mo.order[:i])
			mo.order[0] = k
			return
		}
	}
}

func (mo *ttlModel) drop(k int64) {
	delete(mo.m, k)
	for i, x := range mo.order {
		if x == k {
			mo.order = append(mo.order[:i], mo.order[i+1:]...)
			return
		}
	}
}

func (mo *ttlModel) set(k int64, v *ttlPayload, ttl int64) {
	if e, ok := mo.m[k]; ok {
		e.val = v
		e.expireAt = mo.now + ttl
		mo.m[k] = e
		mo.touch(k)
		return
	}
	mo.m[k] = ttlModelEntry{val: v, expireAt: mo.now + ttl}
	mo.order = append(mo.order, 0)
	copy(mo.order[1:], mo.order[:len(mo.order)-1])
	mo.order[0] = k
	if len(mo.m) > mo.cap {
		back := mo.order[len(mo.order)-1]
		mo.drop(back)
	}
}

func (mo *ttlModel) get(k int64) (*ttlPayload, bool) {
	e, ok := mo.m[k]
	if !ok {
		mo.misses++
		return nil, false
	}
	if mo.now >= e.expireAt {
		mo.drop(k)
		mo.misses++
		return nil, false
	}
	mo.hits++
	mo.touch(k)
	return e.val, true
}

func (mo *ttlModel) purge() int {
	n := 0
	for _, k := range append([]int64(nil), mo.order...) {
		if mo.now >= mo.m[k].expireAt {
			mo.drop(k)
			n++
		}
	}
	return n
}

func (mo *ttlModel) keys() []int64 { return append([]int64(nil), mo.order...) }

var (
	failed      int32
	gcSink      [][]byte
	payloadSink []*ttlPayload
	uafSeed     int64 = 1
)

func ck(ok bool, msg string) {
	if !ok {
		atomic.AddInt32(&failed, 1)
		fmt.Println("FAIL:", msg)
	}
}

func gcStress() {
	for i := 0; i < 16; i++ {
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
	n, err := strconv.ParseInt(os.Getenv("UAF_SEED"), 10, 64)
	if err != nil || n <= 0 {
		return 1
	}
	return n
}

type rng64 struct{ s uint64 }

func newRNG(seed int64) *rng64 {
	s := uint64(seed)*0x9e3779b97f4a7c15 + 0xabcdef
	if s == 0 {
		s = 1
	}
	return &rng64{s: s}
}

func (r *rng64) next() uint64 {
	r.s ^= r.s << 13
	r.s ^= r.s >> 7
	r.s ^= r.s << 17
	return r.s
}

func (r *rng64) n(n int) int { return int(r.next() % uint64(n)) }

func sameKeys(a, b []int64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func runCache(seed int64) {
	cap := 8 + int(seed%7)
	now := int64(1000)
	cache := newTTLCache[int64, ttlPayload](cap, now)
	model := newTTLModel(cap, now)
	keySpace := 40
	steps := 600

	for step := 0; step < steps; step++ {
		switch r := newRNG(seed + int64(step)*131); r.n(10) {
		case 0, 1, 2, 3: // Set
			k := int64(r.n(keySpace))
			ttl := int64(1 + r.n(20))
			p := &ttlPayload{
				id:   int64(step),
				name: fmt.Sprintf("k%d", k),
				blob: make([]byte, 6+int(k%5)),
			}
			p.blob[0] = byte(k)
			p.blob[len(p.blob)-1] = byte(step)
			payloadSink = append(payloadSink, p)
			cache.Set(k, p, ttl)
			model.set(k, p, ttl)
		case 4, 5: // Get
			k := int64(r.n(keySpace))
			got, ok := cache.Get(k)
			want, wok := model.get(k)
			ck(ok == wok, fmt.Sprintf("get existence mismatch step=%d key=%d", step, k))
			if wok {
				ck(got == want, fmt.Sprintf("get pointer mismatch step=%d key=%d", step, k))
			}
		case 6: // Peek + Len + Keys
			k := int64(r.n(keySpace))
			got, ok := cache.Peek(k)
			e, present := model.m[k]
			wantOK := present && model.now < e.expireAt
			ck(ok == wantOK, fmt.Sprintf("peek visibility mismatch step=%d key=%d", step, k))
			if wantOK {
				ck(got == e.val, fmt.Sprintf("peek pointer mismatch step=%d key=%d", step, k))
			}
			ck(cache.Len() == len(model.m), fmt.Sprintf("len mismatch step=%d", step))
			ck(sameKeys(cache.Keys(), model.keys()), fmt.Sprintf("LRU order mismatch step=%d", step))
		case 7: // Advance fake clock
			dt := int64(1 + r.n(6))
			cache.Advance(dt)
			model.now += dt
		case 8: // Purge
			got := cache.Purge()
			want := model.purge()
			ck(got == want, fmt.Sprintf("purge count step=%d got=%d want=%d", step, got, want))
			ck(cache.Len() == len(model.m), "len after purge mismatch")
			ck(sameKeys(cache.Keys(), model.keys()), "order after purge mismatch")
		default: // Delete
			k := int64(r.n(keySpace))
			got, ok := cache.Delete(k)
			e, present := model.m[k]
			ck(ok == present, fmt.Sprintf("delete existence mismatch step=%d key=%d", step, k))
			if present {
				ck(got == e.val, "delete pointer mismatch")
				model.drop(k)
			}
		}

		ck(cache.Hits() == model.hits, fmt.Sprintf("hits mismatch step=%d", step))
		ck(cache.Misses() == model.misses, fmt.Sprintf("misses mismatch step=%d", step))

		if step%61 == 0 {
			gcStress()
			ck(cache.Len() == len(model.m), fmt.Sprintf("len after GC mismatch step=%d", step))
			for k, e := range model.m {
				got, ok := cache.Peek(k)
				if model.now >= e.expireAt {
					ck(!ok, fmt.Sprintf("expired entry visible after GC step=%d key=%d", step, k))
					continue
				}
				ck(ok && got == e.val, fmt.Sprintf("post-GC pointer mismatch step=%d key=%d", step, k))
				if ok && got != nil {
					ck(got.blob[0] == byte(k), "post-GC payload blob corrupted")
				}
			}
			ck(sameKeys(cache.Keys(), model.keys()), "order after GC mismatch")
		}
		if atomic.LoadInt32(&failed) != 0 {
			return
		}
	}

	// 收尾：删除部分 key，Purge 后清空，验证无复活。
	n := 0
	for k := range model.m {
		if k%4 == 0 && n < 5 {
			got, ok := cache.Delete(k)
			ck(ok && got != nil, fmt.Sprintf("final delete key=%d", k))
			model.drop(k)
			n++
		}
	}
	gcStress()
	model.now += 1000
	cache.Advance(1000)
	gotPurge := cache.Purge()
	wantPurge := model.purge()
	ck(gotPurge == wantPurge, fmt.Sprintf("final purge count got=%d want=%d", gotPurge, wantPurge))
	ck(cache.Len() == 0 && len(model.m) == 0, "cache not empty after final purge")
	ck(len(cache.Keys()) == 0, "keys not empty after final purge")
}

func runIntCache() {
	cache := newTTLCache[int64, int64](3, 0)
	v := func(n int64) *int64 { x := n; return &x }
	cache.Set(1, v(10), 5)
	cache.Set(2, v(20), 10)
	cache.Set(3, v(30), 10)
	cache.Set(4, v(40), 10) // 淘汰最旧 key 1
	ck(cache.Len() == 3, "capacity eviction failed")
	if _, ok := cache.Get(1); ok {
		ck(false, "evicted key still visible")
	}
	cache.Advance(5)
	if got, ok := cache.Get(2); !ok || got == nil || *got != 20 {
		ck(false, "live int entry lost")
	}
	cache.Set(2, v(22), 20)
	cache.Advance(20)
	if _, ok := cache.Get(2); ok {
		ck(false, "updated int entry did not expire")
	}
	gcStress()
	cache.Set(5, v(50), 1)
	gcStress()
	got, ok := cache.Get(5)
	ck(ok && got != nil && *got == 50, "int cache pointer corrupted after GC")
}

func main() {
	uafSeed = envSeed()
	runCache(uafSeed)
	runIntCache()
	finish("l01_generic_ttl_lru")
}
