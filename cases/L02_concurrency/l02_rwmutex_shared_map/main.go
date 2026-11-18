// L02: RWMutex 保护的 map + slice of *T：多写者替换不可变记录，多读者快照指针，
// 快照跨 GC 后重校验，最终状态确定。
package main

import (
	"fmt"
	"os"
	"runtime"
	"sync"
	"sync/atomic"
)

const (
	nKeys         = 64
	nWriters      = 4
	nReaders      = 4
	writeRounds   = 90
	readRounds    = 180
	keysPerWriter = nKeys / nWriters
)

var (
	failed int32
	gcMu   sync.Mutex
	gcSink [][]byte
)

type record struct {
	key   int
	gen   int
	name  string
	val   int64
	probe [4]int64
}

type store struct {
	mu sync.RWMutex
	m  map[int]*record
	sl []*record
}

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

func makeRecord(key, gen int) *record {
	r := &record{key: key, gen: gen, name: "rec", val: int64(key*1000 + gen)}
	for i := range r.probe {
		r.probe[i] = int64(gen*10 + i)
	}
	return r
}

func validRecord(r *record, key, maxGen int) bool {
	if r == nil || r.name != "rec" || r.key != key || r.gen < 0 || r.gen > maxGen {
		return false
	}
	if r.val != int64(key*1000+r.gen) {
		return false
	}
	for i := range r.probe {
		if r.probe[i] != int64(r.gen*10+i) {
			return false
		}
	}
	return true
}

func writer(w int, s *store, start <-chan struct{}, wg *sync.WaitGroup) {
	defer wg.Done()
	<-start
	base := w * keysPerWriter
	for r := 0; r < writeRounds; r++ {
		key := base + r%keysPerWriter
		rec := makeRecord(key, r)
		s.mu.Lock()
		s.m[key] = rec
		s.sl[key] = rec
		s.mu.Unlock()
		if r%23 == 0 {
			gcStress()
		}
	}
}

func reader(r int, s *store, start <-chan struct{}, wg *sync.WaitGroup) {
	defer wg.Done()
	<-start
	for i := 0; i < readRounds; i++ {
		key := (r*17 + i*13) % nKeys
		s.mu.RLock()
		mr := s.m[key]
		sr := s.sl[key]
		s.mu.RUnlock()
		if !validRecord(mr, key, writeRounds-1) {
			ck(false, fmt.Sprintf("map record invalid key=%d", key))
		}
		if !validRecord(sr, key, writeRounds-1) {
			ck(false, fmt.Sprintf("slice record invalid key=%d", key))
		}
		if i%20 == 0 {
			gcStress()
			if !validRecord(mr, key, writeRounds-1) {
				ck(false, "map snapshot corrupted after GC")
			}
			if !validRecord(sr, key, writeRounds-1) {
				ck(false, "slice snapshot corrupted after GC")
			}
		}
	}
}

func main() {
	s := &store{
		m:  make(map[int]*record, nKeys),
		sl: make([]*record, nKeys),
	}
	for k := 0; k < nKeys; k++ {
		rec := makeRecord(k, 0)
		s.m[k] = rec
		s.sl[k] = rec
	}

	start := make(chan struct{})
	var wg sync.WaitGroup
	for w := 0; w < nWriters; w++ {
		wg.Add(1)
		go writer(w, s, start, &wg)
	}
	for r := 0; r < nReaders; r++ {
		wg.Add(1)
		go reader(r, s, start, &wg)
	}
	close(start)
	wg.Wait()

	gcStress()

	s.mu.RLock()
	bad := 0
	for k := 0; k < nKeys; k++ {
		mr := s.m[k]
		sr := s.sl[k]
		if mr == nil || sr == nil || mr != sr {
			bad++
			continue
		}
		offset := k % keysPerWriter
		lastGen := offset + ((writeRounds-1-offset)/keysPerWriter)*keysPerWriter
		if mr.gen != lastGen || mr.val != int64(k*1000+lastGen) {
			bad++
		}
		if !validRecord(mr, k, writeRounds-1) {
			bad++
		}
	}
	length := len(s.m)
	s.mu.RUnlock()
	ck(bad == 0, fmt.Sprintf("final store invalid entries=%d", bad))
	ck(length == nKeys, fmt.Sprintf("final map length got=%d want=%d", length, nKeys))

	fmt.Printf("keys=%d writers=%d writes=%d readers=%d reads=%d\n",
		nKeys, nWriters, nWriters*writeRounds, nReaders, nReaders*readRounds)
	finish("l02_rwmutex_shared_map")
}
