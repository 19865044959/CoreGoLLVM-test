// L01: 泛型并发容器（RWMutex 保护）多 goroutine 写入/删除 + GC，结果确定可复算。
package main

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"sync"
	"sync/atomic"
)

type item struct {
	id     int64
	name   string
	weight int64
}

type ConcurrentMap[K comparable, V any] struct {
	mu sync.RWMutex
	m  map[K]*V
}

func NewConcurrentMap[K comparable, V any]() *ConcurrentMap[K, V] {
	return &ConcurrentMap[K, V]{m: make(map[K]*V)}
}

func (c *ConcurrentMap[K, V]) Set(k K, v *V) {
	c.mu.Lock()
	c.m[k] = v
	c.mu.Unlock()
}

func (c *ConcurrentMap[K, V]) Get(k K) (*V, bool) {
	c.mu.RLock()
	v, ok := c.m[k]
	c.mu.RUnlock()
	return v, ok
}

func (c *ConcurrentMap[K, V]) Delete(k K) {
	c.mu.Lock()
	delete(c.m, k)
	c.mu.Unlock()
}

func (c *ConcurrentMap[K, V]) Update(k K, f func(*V)) bool {
	c.mu.Lock()
	v, ok := c.m[k]
	if ok {
		f(v)
	}
	c.mu.Unlock()
	return ok
}

func (c *ConcurrentMap[K, V]) Len() int {
	c.mu.RLock()
	n := len(c.m)
	c.mu.RUnlock()
	return n
}

func (c *ConcurrentMap[K, V]) Snapshot(keys []K) []*V {
	c.mu.RLock()
	out := make([]*V, len(keys))
	for i, k := range keys {
		out[i] = c.m[k]
	}
	c.mu.RUnlock()
	return out
}

var (
	failed  int32
	gcMu    sync.Mutex
	gcSink  [][]byte
	uafSeed int64 = 1
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

func tortureRounds(def int) int {
	s := os.Getenv("TORTURE_ROUNDS")
	if s == "" {
		return def
	}
	n, err := strconv.Atoi(s)
	if err != nil || n <= 0 {
		return def
	}
	if n > 64 {
		n = 64
	}
	return n
}

func main() {
	uafSeed = envSeed()
	rounds := tortureRounds(1)
	workers := 4
	per := 64 * rounds

	cm := NewConcurrentMap[int64, item]()
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < per; i++ {
				id := int64(w*per + i)
				it := &item{id: id, name: fmt.Sprintf("w%d-%d", w, i), weight: id*2 + 1}
				cm.Set(id, it)
				cm.Update(id, func(p *item) { p.weight++ })
				if i%8 == 3 {
					cm.Delete(id)
				}
				if i%16 == 0 {
					gcStress()
				}
			}
		}(w)
	}
	wg.Wait()
	gcStress()

	want := 0
	var wantSum int64
	keys := make([]int64, 0, workers*per)
	for w := 0; w < workers; w++ {
		for i := 0; i < per; i++ {
			id := int64(w*per + i)
			p, ok := cm.Get(id)
			if i%8 == 3 {
				ck(!ok, "deleted entry is still visible")
				continue
			}
			want++
			keys = append(keys, id)
			ck(ok && p != nil, "surviving entry missing after GC")
			if !ok || p == nil {
				continue
			}
			ck(p.id == id, "entry id corrupted after GC")
			ck(p.name == fmt.Sprintf("w%d-%d", w, i), "entry name corrupted after GC")
			ck(p.weight == id*2+2, "entry weight not updated correctly")
			wantSum += p.weight
		}
	}
	ck(cm.Len() == want, fmt.Sprintf("container len got=%d want=%d", cm.Len(), want))
	ck(!cm.Update(-1, func(*item) {}), "Update on missing key returned true")

	// 单次 RLock 下做快照，校验指针与权重。
	vals := cm.Snapshot(keys)
	var gotSum int64
	for _, p := range vals {
		if p == nil {
			ck(false, "snapshot returned nil for present key")
			continue
		}
		gotSum += p.weight
	}
	ck(len(vals) == want, "snapshot length mismatch")
	ck(gotSum == wantSum, fmt.Sprintf("snapshot sum got=%d want=%d", gotSum, wantSum))

	// 并发只读阶段：4 个 reader 各扫自己的 key 区间，命中数可复算。
	var hits int64
	var rg sync.WaitGroup
	for w := 0; w < workers; w++ {
		rg.Add(1)
		go func(w int) {
			defer rg.Done()
			for i := 0; i < per; i++ {
				if _, ok := cm.Get(int64(w*per + i)); ok {
					atomic.AddInt64(&hits, 1)
				}
			}
		}(w)
	}
	rg.Wait()
	ck(hits == int64(want), fmt.Sprintf("concurrent read hits got=%d want=%d", hits, want))

	// 第二种实例化：string 键复用同一批指针。
	byName := NewConcurrentMap[string, item]()
	for _, p := range vals {
		if p != nil {
			byName.Set(p.name, p)
		}
	}
	gcStress()
	ck(byName.Len() == want, "string-key container length mismatch")
	for _, p := range vals {
		if p == nil {
			continue
		}
		got, ok := byName.Get(p.name)
		ck(ok && got == p, "string-key container lost pointer identity")
		ck(got.id == p.id && got.weight == p.weight, "string-key container payload corrupted")
	}

	fmt.Printf("rounds=%d workers=%d entries=%d sum=%d\n", rounds, workers, want, wantSum)
	finish("l01_generic_sync_container")
}
