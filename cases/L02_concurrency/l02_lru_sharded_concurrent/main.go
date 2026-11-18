// L02: 分片并发 LRU（gorm 并发 LRU / tailscale flowtrack 场景）：
// 每 shard 一把锁 + container/list；读命中提升到队首，超容量从队尾淘汰。
// 单线程阶段验证精确 LRU 顺序，并发阶段验证不丢条目、payload 跨 GC 完整。
package main

import (
	"container/list"
	"fmt"
	"math/rand"
	"os"
	"runtime"
	"strconv"
	"sync"
	"sync/atomic"
)

const (
	lruShards   = 4
	lruCapacity = 16
	lruWorkers  = 8
	lruBaseOps  = 300
	lruKeyspace = 256
)

type lruPayload struct {
	key  int
	gen  int
	val  int64
	tag  string
	data []byte
}

func newLRUPayload(key, gen int, seed int64) *lruPayload {
	data := make([]byte, 8)
	for i := range data {
		data[i] = byte(seed + int64(key*13+i) + int64(gen)*5)
	}
	return &lruPayload{
		key:  key,
		gen:  gen,
		val:  seed + int64(key)*7 + int64(gen)*3,
		tag:  fmt.Sprintf("k%d-g%d", key, gen),
		data: data,
	}
}

func (p *lruPayload) valid(seed int64) bool {
	if p == nil {
		return false
	}
	if p.tag != fmt.Sprintf("k%d-g%d", p.key, p.gen) || p.val != seed+int64(p.key)*7+int64(p.gen)*3 {
		return false
	}
	for i := range p.data {
		if p.data[i] != byte(seed+int64(p.key*13+i)+int64(p.gen)*5) {
			return false
		}
	}
	return true
}

type lruShard struct {
	mu       sync.Mutex
	capacity int
	ll       *list.List
	m        map[int]*list.Element
}

func newLRUShard(capacity int) *lruShard {
	return &lruShard{capacity: capacity, ll: list.New(), m: make(map[int]*list.Element)}
}

func (s *lruShard) get(key int) (*lruPayload, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.m[key]
	if !ok {
		return nil, false
	}
	s.ll.MoveToFront(e)
	return e.Value.(*lruPayload), true
}

func (s *lruShard) put(p *lruPayload) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if e, ok := s.m[p.key]; ok {
		e.Value = p
		s.ll.MoveToFront(e)
		return
	}
	e := s.ll.PushFront(p)
	s.m[p.key] = e
	if s.ll.Len() > s.capacity {
		back := s.ll.Back()
		old := back.Value.(*lruPayload)
		delete(s.m, old.key)
		s.ll.Remove(back)
	}
}

func (s *lruShard) len() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.ll.Len()
}

func (s *lruShard) validate(seed int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ll.Len() > s.capacity {
		ck(false, fmt.Sprintf("shard over capacity: len=%d cap=%d", s.ll.Len(), s.capacity))
	}
	if len(s.m) != s.ll.Len() {
		ck(false, fmt.Sprintf("shard map/list mismatch: map=%d list=%d", len(s.m), s.ll.Len()))
	}
	for e := s.ll.Front(); e != nil; e = e.Next() {
		p := e.Value.(*lruPayload)
		if !p.valid(seed) {
			ck(false, fmt.Sprintf("cached payload corrupted key=%d", p.key))
		}
		if el, ok := s.m[p.key]; !ok || el != e {
			ck(false, fmt.Sprintf("shard index broken key=%d", p.key))
		}
	}
}

type shardedLRU struct {
	shards []*lruShard
	gets   int64
	hits   int64
	misses int64
}

func newShardedLRU(seed int64) *shardedLRU {
	c := &shardedLRU{shards: make([]*lruShard, lruShards)}
	for i := range c.shards {
		c.shards[i] = newLRUShard(lruCapacity)
	}
	_ = seed
	return c
}

func (c *shardedLRU) shardFor(key int) *lruShard {
	return c.shards[key%len(c.shards)]
}

func (c *shardedLRU) get(key int) (*lruPayload, bool) {
	atomic.AddInt64(&c.gets, 1)
	p, ok := c.shardFor(key).get(key)
	if ok {
		atomic.AddInt64(&c.hits, 1)
	} else {
		atomic.AddInt64(&c.misses, 1)
	}
	return p, ok
}

func (c *shardedLRU) put(p *lruPayload) {
	c.shardFor(p.key).put(p)
}

func (c *shardedLRU) total() int {
	n := 0
	for _, s := range c.shards {
		n += s.len()
	}
	return n
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

func tortureScale() int {
	if s := os.Getenv("TORTURE_ROUNDS"); s != "" {
		if n, err := strconv.Atoi(s); err == nil && n > 1 {
			if n > 8 {
				n = 8
			}
			return n
		}
	}
	return 1
}

func checkSingleShardLRU(seed int64) {
	s := newLRUShard(4)
	for i := 0; i < 4; i++ {
		key := i * lruShards // 全部落到 shard 0
		s.put(newLRUPayload(key, 0, seed))
	}
	if p, ok := s.get(0); !ok || !p.valid(seed) {
		ck(false, "single-shard get(0) failed")
	}
	s.put(newLRUPayload(16, 0, seed)) // 淘汰最久未用的 key=4
	if _, ok := s.get(4); ok {
		ck(false, "LRU must evict least recently used key 4")
	}
	if p, ok := s.get(8); !ok || !p.valid(seed) {
		ck(false, "key 8 missing before second eviction")
	}
	s.put(newLRUPayload(20, 0, seed)) // 淘汰 key=12
	if _, ok := s.get(12); ok {
		ck(false, "LRU must evict key 12 after recency promotion")
	}
	for _, key := range []int{0, 8, 16, 20} {
		if p, ok := s.get(key); !ok || !p.valid(seed) {
			ck(false, fmt.Sprintf("expected key %d missing after LRU evictions", key))
		}
	}
	s.validate(seed)
}

func main() {
	seed := seedVal()
	scale := tortureScale()
	opsPerWorker := lruBaseOps * scale

	checkSingleShardLRU(seed)

	c := newShardedLRU(seed)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for w := 0; w < lruWorkers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			r := rand.New(rand.NewSource(seed*1000 + int64(w)*97 + 11))
			<-start
			for i := 0; i < opsPerWorker; i++ {
				key := r.Intn(lruKeyspace)
				if r.Intn(10) < 6 {
					if p, ok := c.get(key); ok {
						if !p.valid(seed) {
							ck(false, fmt.Sprintf("worker %d hit invalid payload key=%d", w, key))
						}
					} else {
						c.put(newLRUPayload(key, 0, seed))
					}
				} else {
					c.put(newLRUPayload(key, i%3, seed))
				}
				if i%64 == 63 {
					gcStress()
				}
			}
		}(w)
	}
	close(start)
	wg.Wait()

	if n := c.total(); n <= 0 || n > lruShards*lruCapacity {
		ck(false, fmt.Sprintf("total entries=%d want in (0,%d]", n, lruShards*lruCapacity))
	}
	for _, s := range c.shards {
		s.validate(seed)
	}
	if gets := atomic.LoadInt64(&c.gets); gets != atomic.LoadInt64(&c.hits)+atomic.LoadInt64(&c.misses) {
		ck(false, fmt.Sprintf("get accounting mismatch gets=%d hits=%d misses=%d",
			gets, c.hits, c.misses))
	}

	gcStress()
	for _, s := range c.shards {
		s.validate(seed)
	}

	fmt.Printf("shards=%d capacity=%d workers=%d ops=%d scale=%d\n",
		lruShards, lruCapacity, lruWorkers, opsPerWorker, scale)
	finish("l02_lru_sharded_concurrent")
}
