// L02: RWMutex 保护 struct 值切片：写者原地更新各自区间的 record，
// 读者在 RLock 内拷贝窗口后再解锁校验，证明快照拷贝独立且跨 GC 一致。
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
	nRecords    = 128
	nWriters    = 4
	perWriter   = nRecords / nWriters
	writeRounds = 60
	nReaders    = 4
	readRounds  = 120
	window      = 16
)

var (
	failed  int32
	gcMu    sync.Mutex
	gcSink  [][]byte
	curSeed int64
)

type record struct {
	id    int
	gen   int
	val   int64
	tag   int64
	name  string
	probe [3]int64
}

type store struct {
	mu   sync.RWMutex
	rows []record
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

func tagFor(seed int64, id, gen int) int64 {
	x := uint64(seed)*0x9E3779B97F4A7C15 + uint64(id+1)*0xBF58476D1CE4E5B9 + uint64(gen+1)
	x ^= x >> 30
	x *= 0x94D049BB133111EB
	x ^= x >> 31
	return int64(x >> 1)
}

func newRecord(id, gen int) record {
	r := record{id: id, gen: gen, val: int64(id)*1000 + int64(gen), tag: tagFor(curSeed, id, gen), name: "row"}
	for i := range r.probe {
		r.probe[i] = int64(gen*10 + i)
	}
	return r
}

func validRecord(r *record, id, maxGen int) bool {
	if r == nil || r.id != id || r.gen < 0 || r.gen > maxGen || r.name != "row" {
		return false
	}
	if r.val != int64(id)*1000+int64(r.gen) || r.tag != tagFor(curSeed, id, r.gen) {
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
	base := w * perWriter
	for round := 0; round < writeRounds; round++ {
		s.mu.Lock()
		for k := 0; k < perWriter; k++ {
			id := base + k
			s.rows[id] = newRecord(id, round+1)
		}
		s.mu.Unlock()
		if round%17 == 0 {
			gcStress()
		}
	}
}

func reader(r int, s *store, start <-chan struct{}, wg *sync.WaitGroup) {
	defer wg.Done()
	<-start
	buf := make([]record, window)
	for i := 0; i < readRounds; i++ {
		begin := (r*37 + i*11) % (nRecords - window)
		s.mu.RLock()
		copy(buf, s.rows[begin:begin+window])
		s.mu.RUnlock()
		for j := range buf {
			if !validRecord(&buf[j], begin+j, writeRounds) {
				ckSeed(false, fmt.Sprintf("reader copied invalid record id=%d", begin+j))
			}
		}
		if i%23 == 0 {
			gcStress()
			for j := range buf {
				if !validRecord(&buf[j], begin+j, writeRounds) {
					ckSeed(false, "copied record snapshot corrupted after GC")
				}
			}
		}
	}
}

func main() {
	curSeed = envSeed()
	s := &store{rows: make([]record, nRecords)}
	for i := 0; i < nRecords; i++ {
		s.rows[i] = newRecord(i, 0)
	}

	start := make(chan struct{})
	var ww, rw sync.WaitGroup
	for w := 0; w < nWriters; w++ {
		ww.Add(1)
		go writer(w, s, start, &ww)
	}
	for r := 0; r < nReaders; r++ {
		rw.Add(1)
		go reader(r, s, start, &rw)
	}
	close(start)
	ww.Wait()
	rw.Wait()

	final := make([]record, nRecords)
	s.mu.RLock()
	copy(final, s.rows)
	s.mu.RUnlock()
	gcStress()

	bad := 0
	var sum int64
	for i := range final {
		if !validRecord(&final[i], i, writeRounds) || final[i].gen != writeRounds {
			bad++
			if bad <= 4 {
				ckSeed(false, fmt.Sprintf("final record %d invalid", i))
			}
		}
		sum += final[i].val
	}
	ckSeed(bad == 0, fmt.Sprintf("invalid final records=%d", bad))
	ckSeed(len(s.rows) == nRecords, fmt.Sprintf("slice length changed got=%d", len(s.rows)))
	var wantSum int64
	for id := 0; id < nRecords; id++ {
		wantSum += int64(id)*1000 + int64(writeRounds)
	}
	ckSeed(sum == wantSum, fmt.Sprintf("final checksum got=%d want=%d", sum, wantSum))

	// 写后读：写者全部结束后再取一次窗口，必须看到完整的最新代。
	s.mu.RLock()
	last := make([]record, window)
	copy(last, s.rows[nRecords-window:])
	s.mu.RUnlock()
	for i := range last {
		if last[i].gen != writeRounds {
			ck(false, "post-write snapshot did not observe the final generation")
			break
		}
	}

	fmt.Printf("records=%d writers=%d writes=%d readers=%d reads=%d\n",
		nRecords, nWriters, nWriters*writeRounds, nReaders, nReaders*readRounds)
	finish("l02_rwmutex_struct_slice")
}
