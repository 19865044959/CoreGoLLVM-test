// L02: sync.Map 指针缓存：主键由单写者递增替换不可变 *entry，读者并发 Load，
// 另有一组 key 用 LoadOrStore 竞争插入（每 key 恰一个赢家）；最后 Range 全量校验。
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
	nKeys         = 64
	nWriters      = 4
	keysPerWriter = nKeys / nWriters
	nReaders      = 3
	baseOps       = 192
	baseReaderOps = 300
	nOnceKeys     = 16
	onceKeyBase   = 1000
	nRacers       = 8
)

var (
	failed  int32
	gcMu    sync.Mutex
	gcSink  [][]byte
	curSeed int64
	cache   sync.Map
)

type entry struct {
	key   int
	gen   int64
	tag   int64
	name  string
	probe [4]int64
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

func tortureRounds(def int) int {
	s := os.Getenv("TORTURE_ROUNDS")
	if s == "" {
		return def
	}
	n, err := strconv.Atoi(s)
	if err != nil || n <= 0 {
		return def
	}
	if n > 100 {
		n = 100
	}
	return n
}

func tagFor(seed int64, key int, gen int64) int64 {
	x := uint64(seed)*0x9E3779B97F4A7C15 + uint64(key+1)*0xBF58476D1CE4E5B9 + uint64(gen+1)
	x ^= x >> 30
	x *= 0x94D049BB133111EB
	x ^= x >> 31
	return int64(x >> 1)
}

func newEntry(key int, gen int64) *entry {
	e := &entry{key: key, gen: gen, tag: tagFor(curSeed, key, gen), name: "sm"}
	for i := range e.probe {
		e.probe[i] = gen*10 + int64(i) + int64(key)
	}
	return e
}

func validEntry(e *entry, key int, maxGen int64) bool {
	if e == nil || e.key != key || e.gen < 0 || e.gen > maxGen || e.name != "sm" {
		return false
	}
	if e.tag != tagFor(curSeed, key, e.gen) {
		return false
	}
	for i := range e.probe {
		if e.probe[i] != e.gen*10+int64(i)+int64(key) {
			return false
		}
	}
	return true
}

var ops int

func writer(w int, start <-chan struct{}, wg *sync.WaitGroup) {
	defer wg.Done()
	<-start
	base := w * keysPerWriter
	var held *entry
	for i := 0; i < ops; i++ {
		key := base + i%keysPerWriter
		v, ok := cache.Load(key)
		if !ok {
			ckSeed(false, "writer found predefined key missing")
			continue
		}
		cur, ok := v.(*entry)
		if !ok || !validEntry(cur, key, int64(ops/keysPerWriter)) {
			ckSeed(false, "writer loaded invalid entry")
			continue
		}
		next := newEntry(key, cur.gen+1)
		cache.Store(key, next)
		held = next
		if i%29 == 0 {
			gcStress()
			if !validEntry(held, key, int64(ops/keysPerWriter)) {
				ckSeed(false, "stored entry corrupted after GC")
			}
		}
	}
	runtime.KeepAlive(held)
}

func reader(id int, loops int, start <-chan struct{}, wg *sync.WaitGroup) {
	defer wg.Done()
	<-start
	var held *entry
	for i := 0; i < loops; i++ {
		key := (id*17 + i*11) % nKeys
		v, ok := cache.Load(key)
		if !ok {
			ckSeed(false, "reader found predefined key missing")
			continue
		}
		e, ok := v.(*entry)
		if !ok || !validEntry(e, key, int64(ops/keysPerWriter)) {
			ckSeed(false, "reader loaded invalid entry")
			continue
		}
		held = e
		if i%37 == 0 {
			gcStress()
			if !validEntry(held, key, int64(ops/keysPerWriter)) {
				ckSeed(false, "loaded entry corrupted after GC")
			}
		}
	}
	runtime.KeepAlive(held)
}

func racer(id int, start <-chan struct{}, wg *sync.WaitGroup, winners, losers *int32) {
	defer wg.Done()
	<-start
	for k := 0; k < nOnceKeys; k++ {
		e := newEntry(onceKeyBase+k, 1)
		if _, loaded := cache.LoadOrStore(onceKeyBase+k, e); loaded {
			atomic.AddInt32(losers, 1)
		} else {
			atomic.AddInt32(winners, 1)
		}
	}
}

func main() {
	curSeed = envSeed()
	rounds := tortureRounds(1)
	ops = baseOps * rounds
	readerLoops := baseReaderOps * rounds
	finalGen := int64(ops / keysPerWriter)

	for k := 0; k < nKeys; k++ {
		cache.Store(k, newEntry(k, 0))
	}

	start := make(chan struct{})
	var ww, rw, raceWG sync.WaitGroup
	for w := 0; w < nWriters; w++ {
		ww.Add(1)
		go writer(w, start, &ww)
	}
	for r := 0; r < nReaders; r++ {
		rw.Add(1)
		go reader(r, readerLoops, start, &rw)
	}
	var winners, losers int32
	for i := 0; i < nRacers; i++ {
		raceWG.Add(1)
		go racer(i, start, &raceWG, &winners, &losers)
	}
	close(start)
	ww.Wait()
	rw.Wait()
	raceWG.Wait()

	ckSeed(atomic.LoadInt32(&winners) == nOnceKeys,
		fmt.Sprintf("LoadOrStore winners got=%d want=%d", winners, nOnceKeys))
	ckSeed(atomic.LoadInt32(&losers) == nRacers*nOnceKeys-nOnceKeys,
		fmt.Sprintf("LoadOrStore losers got=%d want=%d", losers, nRacers*nOnceKeys-nOnceKeys))

	gcStress()

	count := 0
	bad := 0
	var genSum int64
	cache.Range(func(k, v any) bool {
		count++
		key, kok := k.(int)
		e, eok := v.(*entry)
		if !kok || !eok {
			bad++
			return true
		}
		if key >= onceKeyBase {
			if !validEntry(e, key, 1) {
				bad++
			}
		} else if key < 0 || key >= nKeys || !validEntry(e, key, finalGen) {
			bad++
		}
		genSum += e.gen
		return true
	})
	wantCount := nKeys + nOnceKeys
	wantGenSum := int64(nKeys)*finalGen + int64(nOnceKeys)
	ckSeed(bad == 0, fmt.Sprintf("invalid entries after final GC=%d", bad))
	ckSeed(count == wantCount, fmt.Sprintf("cache size got=%d want=%d", count, wantCount))
	ckSeed(genSum == wantGenSum, fmt.Sprintf("generation sum got=%d want=%d", genSum, wantGenSum))
	for k := 0; k < nKeys; k++ {
		v, ok := cache.Load(k)
		if !ok {
			ckSeed(false, fmt.Sprintf("final key %d missing", k))
			continue
		}
		if e, _ := v.(*entry); !validEntry(e, k, finalGen) || e.gen != finalGen {
			ckSeed(false, fmt.Sprintf("final key %d generation mismatch", k))
		}
	}

	fmt.Printf("keys=%d raced_keys=%d writers=%d readers=%d ops=%d\n",
		nKeys, nOnceKeys, nWriters, nReaders, ops)
	finish("l02_syncmap_pointer_cache")
}
