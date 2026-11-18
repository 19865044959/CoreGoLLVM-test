// L02: 并发 LRU 淘汰（按字节配额）：map + container/list + mutex，
// 超配额从队尾淘汰并记录淘汰载体；串行阶段验证精确淘汰顺序与超大条目拒绝，
// 并发阶段验证总字节恒 <= 配额、淘汰/存活记账恒等、载荷跨 GC 完整。
package main

import (
	"container/list"
	"fmt"
	"os"
	"runtime"
	"strconv"
	"sync"
	"sync/atomic"
)

const (
	lwCapacity = 200
	lwWorkers  = 8
	lwBaseOps  = 300
	lwKeyspace = 40
	lwMaxScale = 8
)

var (
	failed  int32
	gcMu    sync.Mutex
	gcSink  [][]byte
	curSeed int64
)

type lwEntry struct {
	key    int
	gen    int
	weight int64
	val    int64
	tag    string
	data   []byte
}

type wlru struct {
	mu          sync.Mutex
	capacity    int64
	total       int64
	ll          *list.List
	m           map[int]*list.Element
	evicted     []*lwEntry
	insertedNew int64
	evictions   int64
	rejected    int64
}

func newWLRU(capacity int64) *wlru {
	return &wlru{capacity: capacity, ll: list.New(), m: make(map[int]*list.Element)}
}

func (c *wlru) put(e *lwEntry) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if e.weight <= 0 || e.weight > c.capacity {
		c.rejected++
		return false
	}
	if el, ok := c.m[e.key]; ok {
		old := el.Value.(*lwEntry)
		c.total += e.weight - old.weight
		el.Value = e
		c.ll.MoveToFront(el)
	} else {
		el := c.ll.PushFront(e)
		c.m[e.key] = el
		c.total += e.weight
		c.insertedNew++
	}
	c.evictLocked()
	if c.total > c.capacity {
		ck(false, fmt.Sprintf("total=%d exceeds capacity=%d", c.total, c.capacity))
	}
	if len(c.m) != c.ll.Len() {
		ck(false, "map/list length mismatch")
	}
	return true
}

func (c *wlru) get(key int) (*lwEntry, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	el, ok := c.m[key]
	if !ok {
		return nil, false
	}
	c.ll.MoveToFront(el)
	return el.Value.(*lwEntry), true
}

func (c *wlru) evictLocked() {
	for c.total > c.capacity {
		back := c.ll.Back()
		if back == nil {
			return
		}
		e := back.Value.(*lwEntry)
		c.ll.Remove(back)
		delete(c.m, e.key)
		c.total -= e.weight
		c.evictions++
		c.evicted = append(c.evicted, e)
	}
}

func ck(ok bool, msg string) {
	if !ok {
		atomic.AddInt32(&failed, 1)
		fmt.Println("FAIL:", msg)
	}
}

func ckSeed(ok bool, msg string) {
	if !ok {
		atomic.AddInt32(&failed, 1)
		fmt.Printf("FAIL: %s (seed=%d)\n", msg, curSeed)
	}
}

func gcStress() {
	gcMu.Lock()
	for i := 0; i < 8; i++ {
		b := make([]byte, 2048)
		b[0] = byte(i)
		gcSink = append(gcSink[:0], b)
	}
	gcSink = gcSink[:0]
	gcMu.Unlock()
	runtime.GC()
	runtime.GC()
}

func finish(name string) {
	if n := atomic.LoadInt32(&failed); n != 0 {
		fmt.Printf("FAILED %s (%d failed checks)\n", name, n)
		os.Exit(1)
	}
	fmt.Println("ALL PASS " + name)
}

func envSeed() int64 {
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

func tortureScale() int {
	s := os.Getenv("TORTURE_ROUNDS")
	if s == "" {
		return 1
	}
	n, err := strconv.Atoi(s)
	if err != nil || n <= 0 {
		return 1
	}
	if n > lwMaxScale {
		n = lwMaxScale
	}
	return n
}

func newLWEntry(seed int64, key, gen int, weight int64) *lwEntry {
	v := seed*577 + int64(key)*31 + int64(gen)*7 + 3
	data := make([]byte, 6)
	for i := range data {
		data[i] = byte(seed + int64(key*11+i) + int64(gen))
	}
	return &lwEntry{
		key:    key,
		gen:    gen,
		weight: weight,
		val:    v,
		tag:    fmt.Sprintf("k%d-g%d", key, gen),
		data:   data,
	}
}

func (e *lwEntry) valid(seed int64) bool {
	if e == nil || e.weight <= 0 || e.tag != fmt.Sprintf("k%d-g%d", e.key, e.gen) ||
		e.val != seed*577+int64(e.key)*31+int64(e.gen)*7+3 {
		return false
	}
	for i := range e.data {
		if e.data[i] != byte(seed+int64(e.key*11+i)+int64(e.gen)) {
			return false
		}
	}
	return true
}

func weightFor(key int) int64 { return int64(10 + (key%5)*10) }

func checkSerial(seed int64) {
	c := newWLRU(100)
	if !c.put(newLWEntry(seed, 1, 0, 30)) || !c.put(newLWEntry(seed, 2, 0, 40)) || !c.put(newLWEntry(seed, 3, 0, 50)) {
		ck(false, "serial put failed")
	}
	if c.total != 90 || c.evictions != 1 {
		ck(false, fmt.Sprintf("weighted eviction total=%d evictions=%d", c.total, c.evictions))
	}
	if _, ok := c.get(1); ok {
		ck(false, "least recently used key 1 must be evicted")
	}
	if _, ok := c.get(2); !ok {
		ck(false, "key 2 must survive first eviction")
	}
	if !c.put(newLWEntry(seed, 4, 0, 25)) {
		ck(false, "serial put 4 failed")
	}
	if c.total != 65 || c.evictions != 2 {
		ck(false, fmt.Sprintf("second eviction total=%d evictions=%d", c.total, c.evictions))
	}
	if _, ok := c.get(3); ok {
		ck(false, "recency promotion failed: key 3 must be evicted")
	}
	for _, key := range []int{2, 4} {
		if e, ok := c.get(key); !ok || !e.valid(seed) {
			ck(false, fmt.Sprintf("expected live key %d missing or invalid", key))
		}
	}
	beforeTotal, beforeLen := c.total, c.ll.Len()
	if c.put(newLWEntry(seed, 5, 0, 101)) {
		ck(false, "oversized entry must be rejected")
	}
	if c.total != beforeTotal || c.ll.Len() != beforeLen || c.rejected != 1 {
		ck(false, "oversized rejection mutated cache")
	}
	for _, e := range c.evicted {
		if !e.valid(seed) {
			ck(false, "serial evicted payload corrupted")
		}
	}
}

func main() {
	curSeed = envSeed()
	scale := tortureScale()
	ops := lwBaseOps * scale

	checkSerial(curSeed)

	c := newWLRU(lwCapacity)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for w := 0; w < lwWorkers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			<-start
			for i := 0; i < ops; i++ {
				key := (w*ops + i*7) % lwKeyspace
				if i%4 == 0 {
					if e, ok := c.get(key); ok && !e.valid(curSeed) {
						ckSeed(false, "worker observed corrupted live entry")
					}
				} else {
					c.put(newLWEntry(curSeed, key, i%3, weightFor(key)))
				}
				if i%32 == 31 {
					gcStress()
				}
				c.mu.Lock()
				if c.total > c.capacity {
					ck(false, "capacity exceeded during concurrent churn")
				}
				c.mu.Unlock()
			}
		}(w)
	}
	close(start)
	wg.Wait()

	c.mu.Lock()
	total := c.total
	inserted := c.insertedNew
	evictions := c.evictions
	live := len(c.m)
	rejected := c.rejected
	listLen := c.ll.Len()
	var recomputed int64
	var liveBad, evictedBad int
	for e := c.ll.Front(); e != nil; e = e.Next() {
		ent := e.Value.(*lwEntry)
		recomputed += ent.weight
		if !ent.valid(curSeed) {
			liveBad++
		}
		if el, ok := c.m[ent.key]; !ok || el != e {
			liveBad++
		}
	}
	for _, e := range c.evicted {
		if !e.valid(curSeed) {
			evictedBad++
		}
	}
	c.mu.Unlock()

	if liveBad != 0 {
		ckSeed(false, fmt.Sprintf("live entries invalid=%d", liveBad))
	}
	if evictedBad != 0 {
		ckSeed(false, fmt.Sprintf("evicted entries invalid=%d", evictedBad))
	}
	if listLen != live || len(c.evicted) != int(evictions) {
		ck(false, "list/eviction accounting mismatch")
	}
	if recomputed != total || total > c.capacity {
		ck(false, fmt.Sprintf("recomputed total=%d cached=%d cap=%d", recomputed, total, c.capacity))
	}
	if inserted != evictions+int64(live)+rejected {
		ck(false, fmt.Sprintf("insert accounting inserted=%d evictions=%d live=%d rejected=%d",
			inserted, evictions, live, rejected))
	}
	if evictions == 0 {
		ck(false, "concurrent phase produced no evictions")
	}

	gcStress()
	c.mu.Lock()
	for e := c.ll.Front(); e != nil; e = e.Next() {
		if !e.Value.(*lwEntry).valid(curSeed) {
			ckSeed(false, "live entry corrupted by final GC")
		}
	}
	for _, e := range c.evicted {
		if !e.valid(curSeed) {
			ckSeed(false, "evicted entry corrupted by final GC")
		}
	}
	finalTotal := c.total
	c.mu.Unlock()
	if finalTotal != total {
		ck(false, "total changed after final GC")
	}

	fmt.Printf("capacity_bytes=%d workers=%d ops=%d scale=%d\n",
		lwCapacity, lwWorkers, lwWorkers*ops, scale)
	finish("l02_lru_weighted_bytes")
}
