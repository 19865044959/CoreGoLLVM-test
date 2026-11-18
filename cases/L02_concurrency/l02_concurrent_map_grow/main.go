// L02: 并发容器扩容：分片 map 在并发读写中按负载阈值倍增分片并搬迁条目。
// 写入持有 resize RLock + shard 锁；扩容持有 resize 写锁，保证迁移期间
// 不丢更新。最终条目数、每 key 计数、分片数必须与串行期望一致。
package main

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"sync"
	"sync/atomic"
)

const (
	gStartShards = 4
	gMaxShards   = 64
	gLoadFactor  = 8
	gWorkers     = 5
	gReaders     = 4
	gBaseOps     = 48
	gReadLoops   = 400
	gKeyspace    = 200
	gMaxScale    = 16
)

var (
	failed  int32
	gcMu    sync.Mutex
	gcSink  [][]byte
	curSeed int64
)

type gentry struct {
	key   int
	count int64
	tag   int64
}

type gshard struct {
	mu sync.Mutex
	m  map[int]*gentry
}

type gtable struct {
	shards []*gshard
}

type growMap struct {
	resizeMu sync.RWMutex
	table    atomic.Pointer[gtable]
	nEntries atomic.Int64
}

func newGrowMap() *growMap {
	g := &growMap{}
	g.table.Store(&gtable{shards: make([]*gshard, gStartShards)})
	for i := range g.table.Load().shards {
		g.table.Load().shards[i] = &gshard{m: make(map[int]*gentry)}
	}
	return g
}

func (g *growMap) add(key int) int64 {
	g.resizeMu.RLock()
	t := g.table.Load()
	s := t.shards[key%len(t.shards)]
	s.mu.Lock()
	e := s.m[key]
	created := false
	if e == nil {
		e = &gentry{key: key, tag: gEntryTag(curSeed, key)}
		s.m[key] = e
		created = true
	}
	e.count++
	c := e.count
	s.mu.Unlock()
	g.resizeMu.RUnlock()
	if created {
		g.nEntries.Add(1)
		g.maybeGrow()
	}
	return c
}

func (g *growMap) get(key int) (int64, bool) {
	g.resizeMu.RLock()
	t := g.table.Load()
	s := t.shards[key%len(t.shards)]
	s.mu.Lock()
	e := s.m[key]
	var c int64
	if e != nil {
		c = e.count
	}
	s.mu.Unlock()
	g.resizeMu.RUnlock()
	return c, e != nil
}

func (g *growMap) growOnce(old *gtable) {
	g.resizeMu.Lock()
	defer g.resizeMu.Unlock()
	cur := g.table.Load()
	if cur != old {
		return
	}
	if g.nEntries.Load() < int64(len(cur.shards)*gLoadFactor) || len(cur.shards) >= gMaxShards {
		return
	}
	nt := &gtable{shards: make([]*gshard, len(cur.shards)*2)}
	for i := range nt.shards {
		nt.shards[i] = &gshard{m: make(map[int]*gentry)}
	}
	for _, os := range cur.shards {
		os.mu.Lock()
		for k, e := range os.m {
			ns := nt.shards[k%len(nt.shards)]
			ns.m[k] = e
		}
		os.mu.Unlock()
	}
	g.table.Store(nt)
}

func (g *growMap) maybeGrow() {
	for {
		t := g.table.Load()
		if g.nEntries.Load() < int64(len(t.shards)*gLoadFactor) || len(t.shards) >= gMaxShards {
			return
		}
		g.growOnce(t)
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
	if n > gMaxScale {
		n = gMaxScale
	}
	return n
}

func gEntryTag(seed int64, key int) int64 {
	return seed*1000003 + int64(key)*7919 + 13
}

func keyAt(worker, i, ops int) int { return (worker*ops + i) % gKeyspace }

func main() {
	curSeed = envSeed()
	scale := tortureScale()
	ops := gBaseOps * scale
	readLoops := gReadLoops * scale

	want := make([]int64, gKeyspace)
	for w := 0; w < gWorkers; w++ {
		for i := 0; i < ops; i++ {
			want[keyAt(w, i, ops)]++
		}
	}
	var wantTotal int64
	for _, c := range want {
		wantTotal += c
	}

	g := newGrowMap()
	start := make(chan struct{})
	var wwg, rwg sync.WaitGroup
	for w := 0; w < gWorkers; w++ {
		wwg.Add(1)
		go func(w int) {
			defer wwg.Done()
			<-start
			for i := 0; i < ops; i++ {
				key := keyAt(w, i, ops)
				g.add(key)
				if i%47 == 0 {
					gcStress()
				}
			}
		}(w)
	}
	var regress int32
	for r := 0; r < gReaders; r++ {
		rwg.Add(1)
		go func(r int) {
			defer rwg.Done()
			<-start
			last := make(map[int]int64, gKeyspace)
			for i := 0; i < readLoops; i++ {
				key := (r*gReadLoops + i) % gKeyspace
				c, ok := g.get(key)
				if ok && c < last[key] {
					atomic.AddInt32(&regress, 1)
					ckSeed(false, fmt.Sprintf("key %d count regressed %d -> %d", key, last[key], c))
				}
				last[key] = c
				if i%71 == 0 {
					gcStress()
				}
			}
		}(r)
	}
	close(start)
	wwg.Wait()
	rwg.Wait()
	// 保证最后一次插入触发的倍增一定被观察到。
	g.maybeGrow()

	final := g.table.Load()
	shards := len(final.shards)
	if shards != 32 {
		ckSeed(false, fmt.Sprintf("final shards=%d want=32", shards))
	}
	if got := g.nEntries.Load(); got != gKeyspace {
		ckSeed(false, fmt.Sprintf("entries=%d want=%d", got, gKeyspace))
	}
	var total int64
	var mismatches int
	for k := 0; k < gKeyspace; k++ {
		s := final.shards[k%len(final.shards)]
		s.mu.Lock()
		e := s.m[k]
		var c int64
		if e != nil {
			c = e.count
			if e.key != k || e.tag != gEntryTag(curSeed, k) {
				mismatches++
				if mismatches <= 4 {
					ckSeed(false, fmt.Sprintf("entry %d metadata corrupted", k))
				}
			}
		}
		s.mu.Unlock()
		if c != want[k] {
			mismatches++
			if mismatches <= 8 {
				ckSeed(false, fmt.Sprintf("key %d count=%d want=%d", k, c, want[k]))
			}
		}
		total += c
	}
	ckSeed(mismatches == 0, fmt.Sprintf("final mismatches=%d", mismatches))
	ckSeed(total == wantTotal, fmt.Sprintf("total count=%d want=%d", total, wantTotal))
	ckSeed(atomic.LoadInt32(&regress) == 0, "reader observed non-monotonic counts")

	// 最终表必须自洽（无重复 key、计数可重算）。
	seen := 0
	var sum int64
	for _, s := range final.shards {
		s.mu.Lock()
		for k, e := range s.m {
			seen++
			if e == nil || e.key != k || e.tag != gEntryTag(curSeed, k) {
				ckSeed(false, "final table entry corrupted")
			}
			sum += e.count
		}
		s.mu.Unlock()
	}
	ckSeed(seen == gKeyspace, fmt.Sprintf("final table entries=%d want=%d", seen, gKeyspace))
	ckSeed(sum == wantTotal, "final table checksum mismatch")

	gcStress()
	var after int64
	for _, s := range final.shards {
		s.mu.Lock()
		for _, e := range s.m {
			if e.tag != gEntryTag(curSeed, e.key) {
				ckSeed(false, "entry corrupted by final GC")
			}
			after += e.count
		}
		s.mu.Unlock()
	}
	ckSeed(after == wantTotal, "final checksum changed after GC")

	fmt.Printf("workers=%d keys=%d ops=%d shards=%d scale=%d\n",
		gWorkers, gKeyspace, wantTotal, shards, scale)
	finish("l02_concurrent_map_grow")
}
