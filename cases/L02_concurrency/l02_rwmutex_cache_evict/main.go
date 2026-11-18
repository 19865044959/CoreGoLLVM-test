// L02: RWMutex 缓存淘汰（tailscale dnscache/flowtrack LRU 场景）：读侧 RLock
// 并发命中、写侧 Lock 下按 LRU 淘汰；先做可判定的精确淘汰，再做并发读 + 单写者
// 插入，hot 条目 pin 住，最终保留集合与淘汰计数完全确定。
package main

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"sync"
	"sync/atomic"
)

type entry struct {
	key    int
	val    int64
	tag    string
	pinned bool
	prev   *entry
	next   *entry
}

type lruCache struct {
	mu      sync.RWMutex
	m       map[int]*entry
	cap     int
	head    *entry
	tail    *entry
	evicted int64
	hits    atomic.Int64
	misses  atomic.Int64
}

var (
	failed int32
	gcMu   sync.Mutex
	gcSink [][]byte
)

func ck(ok bool, msg string) {
	if !ok {
		atomic.AddInt32(&failed, 1)
		fmt.Println("FAIL:", msg)
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

func seedVal() int64 {
	if s := os.Getenv("UAF_SEED"); s != "" {
		if n, err := strconv.Atoi(s); err == nil {
			return int64(n)
		}
	}
	return 1
}

func newLRU(capacity int) *lruCache {
	return &lruCache{m: make(map[int]*entry), cap: capacity}
}

func (c *lruCache) unlinkLocked(e *entry) {
	if e.prev != nil {
		e.prev.next = e.next
	} else {
		c.head = e.next
	}
	if e.next != nil {
		e.next.prev = e.prev
	} else {
		c.tail = e.prev
	}
	e.prev = nil
	e.next = nil
}

func (c *lruCache) pushBackLocked(e *entry) {
	e.prev = c.tail
	e.next = nil
	if c.tail != nil {
		c.tail.next = e
	} else {
		c.head = e
	}
	c.tail = e
}

func (c *lruCache) evictOneLocked() bool {
	victim := c.head
	for victim != nil && victim.pinned {
		victim = victim.next
	}
	if victim == nil {
		victim = c.head
	}
	if victim == nil {
		return false
	}
	c.unlinkLocked(victim)
	delete(c.m, victim.key)
	c.evicted++
	return true
}

func (c *lruCache) Set(e *entry) {
	c.mu.Lock()
	if old, ok := c.m[e.key]; ok {
		c.unlinkLocked(old)
		delete(c.m, e.key)
	}
	for len(c.m) >= c.cap {
		if !c.evictOneLocked() {
			break
		}
	}
	c.m[e.key] = e
	c.pushBackLocked(e)
	c.mu.Unlock()
}

func (c *lruCache) GetPromote(key int) (*entry, bool) {
	c.mu.Lock()
	e, ok := c.m[key]
	if ok {
		c.unlinkLocked(e)
		c.pushBackLocked(e)
		c.hits.Add(1)
	} else {
		c.misses.Add(1)
	}
	c.mu.Unlock()
	return e, ok
}

func (c *lruCache) Get(key int) (*entry, bool) {
	c.mu.RLock()
	e, ok := c.m[key]
	c.mu.RUnlock()
	if ok {
		c.hits.Add(1)
	} else {
		c.misses.Add(1)
	}
	return e, ok
}

func (c *lruCache) sizeEvicted() (int, int64) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.m), c.evicted
}

func (c *lruCache) contains(key int) bool {
	c.mu.RLock()
	_, ok := c.m[key]
	c.mu.RUnlock()
	return ok
}

func waitFor(cond func() bool, msg string) {
	for i := 0; i < 4000000; i++ {
		if cond() {
			return
		}
		runtime.Gosched()
	}
	ck(false, msg)
}

func main() {
	seed := seedVal()

	// 阶段一：精确 LRU。容量 4，promote 0 和 2 后依次插入 4、5。
	c := newLRU(4)
	for k := 0; k < 4; k++ {
		c.Set(&entry{key: k, val: seed + int64(k), tag: "base"})
	}
	c.GetPromote(0)
	c.GetPromote(2)
	c.Set(&entry{key: 4, val: seed + 4, tag: "base"})
	ck(!c.contains(1), "LRU must evict key 1")
	c.Set(&entry{key: 5, val: seed + 5, tag: "base"})
	ck(!c.contains(3), "LRU must evict key 3")
	for _, k := range []int{0, 2, 4, 5} {
		if !c.contains(k) {
			ck(false, fmt.Sprintf("LRU lost key %d", k))
		}
	}
	size, ev := c.sizeEvicted()
	ck(size == 4 && ev == 2, fmt.Sprintf("phase1 size=%d evicted=%d want 4/2", size, ev))
	gcStress()

	// 阶段二：并发 RLock 读者 + 单写者 LRU 淘汰。hot 条目 pin 住，淘汰集合确定。
	c2 := newLRU(8)
	for k := 100; k < 104; k++ {
		c2.Set(&entry{key: k, val: seed + int64(k), tag: "hot", pinned: true})
	}
	for k := 200; k < 204; k++ {
		c2.Set(&entry{key: k, val: seed + int64(k), tag: "cold"})
	}

	// 8 个读者同时持有 RLock，证明读侧不互斥。
	var rwg sync.WaitGroup
	var active int32
	var peak int32
	for i := 0; i < 8; i++ {
		rwg.Add(1)
		go func() {
			defer rwg.Done()
			c2.mu.RLock()
			n := atomic.AddInt32(&active, 1)
			for {
				p := atomic.LoadInt32(&peak)
				if n <= p {
					break
				}
				if atomic.CompareAndSwapInt32(&peak, p, n) {
					break
				}
			}
			for j := 0; j < 4000000; j++ {
				if atomic.LoadInt32(&active) == 8 {
					break
				}
				runtime.Gosched()
			}
			c2.mu.RUnlock()
			atomic.AddInt32(&active, -1)
		}()
	}
	rwg.Wait()
	ck(atomic.LoadInt32(&peak) == 8, fmt.Sprintf("peak RLock holders=%d want=8", peak))

	// 并发读者只访问 pinned hot key；单写者插入 36 个 cold key。
	var crwg sync.WaitGroup
	for i := 0; i < 8; i++ {
		crwg.Add(1)
		go func(i int) {
			defer crwg.Done()
			for j := 0; j < 500; j++ {
				key := 100 + (i+j)%4
				e, ok := c2.Get(key)
				if !ok || e == nil {
					ck(false, "pinned hot key missing during concurrent read")
					continue
				}
				if e.val != seed+int64(key) || e.tag != "hot" {
					ck(false, "hot entry corrupted")
				}
				if j%64 == 0 {
					gcStress()
					if e.val != seed+int64(key) || e.tag != "hot" {
						ck(false, "hot entry corrupted by GC")
					}
				}
			}
		}(i)
	}

	var wwg sync.WaitGroup
	wwg.Add(1)
	go func() {
		defer wwg.Done()
		for k := 300; k < 336; k++ {
			c2.Set(&entry{key: k, val: seed + int64(k), tag: "cold"})
			if k%8 == 0 {
				gcStress()
			}
		}
	}()
	crwg.Wait()
	wwg.Wait()

	size2, evicted2 := c2.sizeEvicted()
	ck(size2 == 8, fmt.Sprintf("final cache size=%d want=8", size2))
	ck(evicted2 == 36, fmt.Sprintf("evicted=%d want=36", evicted2))
	for k := 100; k < 104; k++ {
		if !c2.contains(k) {
			ck(false, fmt.Sprintf("pinned hot key %d was evicted", k))
		}
	}
	for k := 332; k < 336; k++ {
		if !c2.contains(k) {
			ck(false, fmt.Sprintf("newest cold key %d missing", k))
		}
	}
	for k := 300; k < 332; k++ {
		if c2.contains(k) {
			ck(false, fmt.Sprintf("old cold key %d should be evicted", k))
		}
	}
	ck(c2.hits.Load() == 8*500, fmt.Sprintf("hits=%d want=4000", c2.hits.Load()))
	ck(c2.misses.Load() == 0, fmt.Sprintf("misses=%d want=0", c2.misses.Load()))

	gcStress()
	for k := 100; k < 104; k++ {
		e, ok := c2.Get(k)
		if !ok || e.val != seed+int64(k) || e.tag != "hot" {
			ck(false, "hot entry corrupted by final GC")
		}
	}

	fmt.Printf("cap=8 hot=4 cold=36 evicted=36 final=8 readers=8 peak_readers=8\n")
	finish("l02_rwmutex_cache_evict")
}
