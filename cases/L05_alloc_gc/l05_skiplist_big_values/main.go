// L05: 缩小版 badger skiplist 场景：64 个 256KB 大对象并发写入与读取，穿插显式 GC。
// TORTURE_SCALE 放大对象数与对象大小，TORTURE_ROUNDS 放大轮数。
package main

import (
	"fmt"
	"hash/crc32"
	"os"
	"runtime"
	"strconv"
	"sync"
	"sync/atomic"
)

const (
	maxLevel   = 12
	defaultKey = 64
	valSize    = 256 << 10
	writerN    = 4
	readerN    = 4
)

var (
	failed int32
	gcSink [][]byte
)

func ck(ok bool, msg string) {
	if !ok {
		atomic.AddInt32(&failed, 1)
		fmt.Println("FAIL:", msg)
	}
}

func ckf(ok bool, format string, args ...interface{}) {
	if !ok {
		atomic.AddInt32(&failed, 1)
		fmt.Printf("FAIL: "+format+"\n", args...)
	}
}

func loadSeed() uint64 {
	s := os.Getenv("UAF_SEED")
	if s == "" {
		return 1
	}
	n, err := strconv.ParseUint(s, 10, 64)
	if err != nil {
		return 1
	}
	return n
}

func envInt(name string, def, lo, hi int) int {
	s := os.Getenv(name)
	if s == "" {
		return def
	}
	n, err := strconv.Atoi(s)
	if err != nil || n < lo {
		return def
	}
	if n > hi {
		n = hi
	}
	return n
}

func finish(name string, seed uint64) {
	if n := atomic.LoadInt32(&failed); n != 0 {
		fmt.Printf("FAILED %s (%d failed checks, seed=%d)\n", name, n, seed)
		os.Exit(1)
	}
	fmt.Println("ALL PASS " + name)
}

type rng struct {
	s uint64
}

func newRNG(seed uint64) *rng {
	if seed == 0 {
		seed = 1
	}
	return &rng{s: seed*0x9E3779B97F4A7C15 + 1}
}

func (r *rng) next() uint64 {
	x := r.s
	x ^= x >> 12
	x ^= x << 25
	x ^= x >> 27
	r.s = x
	return x * 2685821657736338717
}

func (r *rng) intn(n int) int {
	if n <= 0 {
		return 0
	}
	return int(r.next() % uint64(n))
}

func makeVal(key, size, round int, seed uint64) []byte {
	b := make([]byte, size)
	base := uint64(key)*37 + uint64(round)*101 + seed*13 + 3
	for i := range b {
		b[i] = byte((base + uint64(i)*7) % 251)
	}
	return b
}

type node struct {
	key  int
	val  []byte
	next []*node
}

type skiplist struct {
	mu    sync.RWMutex
	head  *node
	level int
	rng   *rng
}

func newSkiplist(seed uint64) *skiplist {
	return &skiplist{
		head:  &node{next: make([]*node, maxLevel)},
		level: 1,
		rng:   newRNG(seed),
	}
}

func (s *skiplist) randomLevel() int {
	lvl := 1
	for lvl < maxLevel && s.rng.next()&1 == 0 {
		lvl++
	}
	return lvl
}

func (s *skiplist) insert(key int, val []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var update [maxLevel]*node
	x := s.head
	for i := s.level - 1; i >= 0; i-- {
		for x.next[i] != nil && x.next[i].key < key {
			x = x.next[i]
		}
		update[i] = x
	}
	x = x.next[0]
	if x != nil && x.key == key {
		x.val = val
		return
	}
	lvl := s.randomLevel()
	if lvl > s.level {
		for i := s.level; i < lvl; i++ {
			update[i] = s.head
		}
		s.level = lvl
	}
	n := &node{key: key, val: val, next: make([]*node, lvl)}
	for i := 0; i < lvl; i++ {
		n.next[i] = update[i].next[i]
		update[i].next[i] = n
	}
}

func (s *skiplist) find(key int) []byte {
	s.mu.RLock()
	defer s.mu.RUnlock()
	x := s.head
	for i := s.level - 1; i >= 0; i-- {
		for x.next[i] != nil && x.next[i].key < key {
			x = x.next[i]
		}
	}
	x = x.next[0]
	if x != nil && x.key == key {
		return x.val
	}
	return nil
}

func (s *skiplist) orderedKeys() []int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]int, 0, defaultKey)
	for x := s.head.next[0]; x != nil; x = x.next[0] {
		out = append(out, x.key)
	}
	return out
}

func main() {
	seed := loadSeed()
	scale := envInt("TORTURE_SCALE", 1, 1, 4)
	rounds := envInt("TORTURE_ROUNDS", 1, 1, 20)

	keyCount := defaultKey * scale
	valueSize := valSize * scale
	prefill := keyCount / 2

	for round := 0; round < rounds; round++ {
		sl := newSkiplist(seed + uint64(round)*101)
		expected := make([]uint32, keyCount)

		// 先串行写入一半 key，供并发读者读取。
		for k := 0; k < prefill; k++ {
			data := makeVal(k, valueSize, round, seed)
			expected[k] = crc32.ChecksumIEEE(data)
			sl.insert(k, data)
		}

		var wg sync.WaitGroup
		var reads int64
		wDone := make(chan struct{})
		stop := make(chan struct{})

		for w := 0; w < writerN; w++ {
			wg.Add(1)
			go func(w int) {
				defer wg.Done()
				for k := prefill + w; k < keyCount; k += writerN {
					data := makeVal(k, valueSize, round, seed)
					expected[k] = crc32.ChecksumIEEE(data)
					sl.insert(k, data)
					if k%8 == 0 {
						junk := make([]byte, 8<<10)
						junk[0] = byte(k)
						runtime.KeepAlive(junk)
					}
				}
			}(w)
		}

		var rwg sync.WaitGroup
		for w := 0; w < readerN; w++ {
			rwg.Add(1)
			go func(w int) {
				defer rwg.Done()
				r := newRNG(seed*1000 + uint64(w)*31 + 7)
				for {
					select {
					case <-stop:
						return
					default:
					}
					k := r.intn(prefill)
					v := sl.find(k)
					if v == nil {
						ckf(false, "concurrent read key=%d returned nil", k)
						return
					}
					if crc32.ChecksumIEEE(v) != expected[k] {
						ckf(false, "concurrent read key=%d checksum mismatch", k)
						return
					}
					atomic.AddInt64(&reads, 1)
				}
			}(w)
		}

		go func() {
			wg.Wait()
			close(wDone)
		}()

		// 写入期间主协程持续制造垃圾并显式 GC。
		for {
			select {
			case <-wDone:
				goto writersDone
			default:
			}
			junk := make([]byte, 64<<10)
			junk[0] = byte(round)
			gcSink = append(gcSink[:0], junk)
			runtime.GC()
			runtime.Gosched()
		}
	writersDone:
		close(stop)
		rwg.Wait()
		wg.Wait()
		gcSink = gcSink[:0]

		// 最终全量校验：有序性 + 每个 key 的内容。
		keys := sl.orderedKeys()
		if len(keys) != keyCount {
			ckf(false, "round=%d size=%d want=%d", round, len(keys), keyCount)
		}
		for i := 1; i < len(keys); i++ {
			if keys[i-1] >= keys[i] {
				ckf(false, "round=%d order broken at %d: %d>=%d", round, i, keys[i-1], keys[i])
				break
			}
		}
		for k := 0; k < keyCount; k++ {
			v := sl.find(k)
			if v == nil {
				ckf(false, "round=%d key=%d missing after concurrent phase", round, k)
				continue
			}
			if len(v) != valueSize {
				ckf(false, "round=%d key=%d len=%d want=%d", round, k, len(v), valueSize)
				continue
			}
			if crc32.ChecksumIEEE(v) != expected[k] {
				ckf(false, "round=%d key=%d checksum after concurrent phase", round, k)
			}
		}

		runtime.GC()
		runtime.KeepAlive(sl)
	}

	finish("l05_skiplist_big_values", seed)
}
