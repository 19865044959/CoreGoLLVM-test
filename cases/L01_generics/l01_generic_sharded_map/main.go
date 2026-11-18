// L01: 并发安全泛型容器——16 分片 RWMutex map，多 worker 并发插入/更新/删除，
// 每 N 次操作显式 GC；结束后逐 key 复算，验证指针身份与 TORTURE_ROUNDS 放大。
package main

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"sync"
	"sync/atomic"
)

const shardCount = 16

type keyish interface {
	~string | ~int64
}

type entry struct {
	id     int64
	name   string
	weight int64
}

type shard[K keyish, V any] struct {
	mu sync.RWMutex
	m  map[K]*V
}

type sharded[K keyish, V any] struct {
	shards [shardCount]shard[K, V]
}

func newSharded[K keyish, V any]() *sharded[K, V] {
	s := &sharded[K, V]{}
	for i := 0; i < shardCount; i++ {
		s.shards[i].m = make(map[K]*V)
	}
	return s
}

func hashKey[K keyish](k K) uint64 {
	const offset = 1469598103934665603
	const prime = 1099511628211
	h := uint64(offset)
	switch v := any(k).(type) {
	case string:
		for i := 0; i < len(v); i++ {
			h ^= uint64(v[i])
			h *= prime
		}
	case int64:
		x := uint64(v)
		for i := 0; i < 8; i++ {
			h ^= x & 0xff
			h *= prime
			x >>= 8
		}
	}
	return h
}

func (s *sharded[K, V]) at(k K) *shard[K, V] {
	return &s.shards[int(hashKey(k)%shardCount)]
}

func (s *sharded[K, V]) Put(k K, v *V) {
	sh := s.at(k)
	sh.mu.Lock()
	sh.m[k] = v
	sh.mu.Unlock()
}

func (s *sharded[K, V]) Get(k K) (*V, bool) {
	sh := s.at(k)
	sh.mu.RLock()
	v, ok := sh.m[k]
	sh.mu.RUnlock()
	return v, ok
}

func (s *sharded[K, V]) Delete(k K) {
	sh := s.at(k)
	sh.mu.Lock()
	delete(sh.m, k)
	sh.mu.Unlock()
}

func (s *sharded[K, V]) Update(k K, f func(*V)) bool {
	sh := s.at(k)
	sh.mu.Lock()
	v, ok := sh.m[k]
	if ok {
		f(v)
	}
	sh.mu.Unlock()
	return ok
}

func (s *sharded[K, V]) Len() int {
	n := 0
	for i := 0; i < shardCount; i++ {
		s.shards[i].mu.RLock()
		n += len(s.shards[i].m)
		s.shards[i].mu.RUnlock()
	}
	return n
}

func (s *sharded[K, V]) Snapshot(keys []K) []*V {
	out := make([]*V, len(keys))
	for i, k := range keys {
		out[i], _ = s.Get(k)
	}
	return out
}

var (
	failed  int32
	gcSink  [][]byte
	gcMu    sync.Mutex
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

func keyString(w, i int) string {
	return fmt.Sprintf("w%02d-k%05d", w, i)
}

func nameString(w, i int) string {
	return fmt.Sprintf("w%d-%d", w, i)
}

func main() {
	uafSeed = envSeed()
	rounds := tortureRounds(1)
	workers := 8
	per := (48 + int(uafSeed%17)) * rounds
	total := workers * per

	sm := newSharded[string, entry]()
	refs := make([]*entry, total)
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < per; i++ {
				idx := w*per + i
				id := int64(idx)
				p := &entry{id: id, name: nameString(w, i), weight: id*2 + 1}
				refs[idx] = p
				key := keyString(w, i)
				sm.Put(key, p)
				sm.Update(key, func(q *entry) { q.weight++ })
				if i%7 == 3 {
					sm.Delete(key)
				}
				if i%32 == 31 {
					gcStress()
				}
			}
		}(w)
	}
	wg.Wait()
	gcStress()

	want := 0
	var wantSum int64
	keys := make([]string, 0, total)
	for w := 0; w < workers; w++ {
		for i := 0; i < per; i++ {
			key := keyString(w, i)
			if i%7 == 3 {
				_, ok := sm.Get(key)
				ck(!ok, "deleted sharded entry is still visible")
				continue
			}
			want++
			keys = append(keys, key)
			p, ok := sm.Get(key)
			ck(ok && p != nil, "surviving sharded entry missing after GC")
			if !ok || p == nil {
				continue
			}
			idx := w*per + i
			ck(p == refs[idx], "sharded map lost pointer identity")
			ck(p.id == int64(idx), "sharded entry id corrupted after GC")
			ck(p.name == nameString(w, i), "sharded entry name corrupted after GC")
			ck(p.weight == int64(idx)*2+2, "sharded entry weight not updated exactly once")
			wantSum += p.weight
		}
	}
	ck(sm.Len() == want, fmt.Sprintf("sharded len got=%d want=%d", sm.Len(), want))
	ck(!sm.Update("missing", func(*entry) {}), "Update on missing sharded key returned true")

	snap := sm.Snapshot(keys)
	ck(len(snap) == want, "snapshot length mismatch")
	var snapSum int64
	for _, p := range snap {
		if p == nil {
			ck(false, "snapshot returned nil for live key")
			continue
		}
		snapSum += p.weight
	}
	ck(snapSum == wantSum, "snapshot weight sum mismatch")

	// 并发只读阶段：命中数必须等于存活数。
	var hits int64
	var rg sync.WaitGroup
	for w := 0; w < workers; w++ {
		rg.Add(1)
		go func(w int) {
			defer rg.Done()
			for i := 0; i < per; i++ {
				if _, ok := sm.Get(keyString(w, i)); ok {
					atomic.AddInt64(&hits, 1)
				}
			}
		}(w)
	}
	rg.Wait()
	ck(hits == int64(want), fmt.Sprintf("concurrent read hits got=%d want=%d", hits, want))

	// 第二个实例化：int64 键/值，验证同一泛型容器代码在不同实例上互不干扰。
	n2 := 160 * rounds
	im := newSharded[int64, int64]()
	for i := 0; i < n2; i++ {
		v := int64(i * 3)
		im.Put(int64(i), &v)
	}
	for i := 0; i < n2/2; i++ {
		ck(im.Update(int64(i), func(p *int64) { *p = *p + 1 }), "int64 update missing key")
	}
	for i := 0; i < n2; i += 5 {
		im.Delete(int64(i))
	}
	gcStress()

	want2 := 0
	var sum2 int64
	for i := 0; i < n2; i++ {
		p, ok := im.Get(int64(i))
		if i%5 == 0 {
			ck(!ok, "deleted int64 entry is still visible")
			continue
		}
		want2++
		exp := int64(i * 3)
		if i < n2/2 {
			exp++
		}
		ck(ok && p != nil, "int64 entry missing after GC")
		if ok && p != nil {
			ck(*p == exp, "int64 entry value corrupted after GC")
			sum2 += *p
		}
	}
	ck(im.Len() == want2, fmt.Sprintf("int64 sharded len got=%d want=%d", im.Len(), want2))
	ck(!im.Update(-1, func(*int64) {}), "int64 Update on missing key returned true")
	gcStress()
	ck(sum2 != 0 || want2 == 0, "int64 sum sanity check")

	finish("l01_generic_sharded_map")
}
