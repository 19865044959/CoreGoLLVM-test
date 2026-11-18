// L02: atomic + map 快照的批量原子提交：多个 publisher 各自构造一批新 key，
// 通过 CAS 把「旧不可变 map + 本批」发布为新一代快照；读者只能看到整批
// 出现或整批缺失，代数单调，最终快照必须是全部批次的并集。
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
	bsBatchKeys   = 4
	bsPubs        = 4
	bsBaseBatches = 10
	bsMaxScale    = 16
	bsReadLoops   = 300
	bsReaders     = 3
)

var (
	failed  int32
	gcMu    sync.Mutex
	gcSink  [][]byte
	curSeed int64
)

type bsnap struct {
	gen int64
	sum int64
	m   map[int]int64
}

var bsCur atomic.Pointer[bsnap]

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
	if n > bsMaxScale {
		n = bsMaxScale
	}
	return n
}

func bKey(batch, j int) int { return batch*bsBatchKeys + j }

func bVal(seed int64, batch, j int) int64 {
	return seed*1000003 + int64(batch)*131 + int64(j)*17 + 5
}

func batchSum(seed int64, batch int) int64 {
	var s int64
	for j := 0; j < bsBatchKeys; j++ {
		s += bVal(seed, batch, j)
	}
	return s
}

func snapValid(s *bsnap, totalBatches int) bool {
	if s == nil || s.m == nil || len(s.m)%bsBatchKeys != 0 {
		return false
	}
	complete := 0
	var sum int64
	for b := 0; b < totalBatches; b++ {
		present := 0
		for j := 0; j < bsBatchKeys; j++ {
			if v, ok := s.m[bKey(b, j)]; ok {
				present++
				if v != bVal(curSeed, b, j) {
					return false
				}
				sum += v
			}
		}
		if present != 0 && present != bsBatchKeys {
			return false // 半批可见
		}
		if present == bsBatchKeys {
			complete++
		}
	}
	if len(s.m) != complete*bsBatchKeys || complete != int(s.gen) {
		return false
	}
	return sum == s.sum
}

func publishBatch(seed int64, batch int) {
	for {
		old := bsCur.Load()
		nm := make(map[int]int64, len(old.m)+bsBatchKeys)
		for k, v := range old.m {
			nm[k] = v
		}
		var add int64
		for j := 0; j < bsBatchKeys; j++ {
			k := bKey(batch, j)
			if _, exists := nm[k]; exists {
				ckSeed(false, "batch key already present")
			}
			v := bVal(seed, batch, j)
			nm[k] = v
			add += v
		}
		ns := &bsnap{gen: old.gen + 1, sum: old.sum + add, m: nm}
		if bsCur.CompareAndSwap(old, ns) {
			return
		}
	}
}

func main() {
	curSeed = envSeed()
	scale := tortureScale()
	batchesPerPub := bsBaseBatches * scale
	totalBatches := bsPubs * batchesPerPub

	bsCur.Store(&bsnap{gen: 0, sum: 0, m: make(map[int]int64)})

	var pwg, rwg sync.WaitGroup
	stop := make(chan struct{})
	for p := 0; p < bsPubs; p++ {
		pwg.Add(1)
		go func(p int) {
			defer pwg.Done()
			for j := 0; j < batchesPerPub; j++ {
				batch := p*batchesPerPub + j
				publishBatch(curSeed, batch)
				if j%4 == 0 {
					gcStress()
				}
			}
		}(p)
	}
	for r := 0; r < bsReaders; r++ {
		rwg.Add(1)
		go func(r int) {
			defer rwg.Done()
			lastGen := int64(0)
			for i := 0; i < bsReadLoops*scale; i++ {
				s := bsCur.Load()
				if s == nil {
					ck(false, "reader observed nil snapshot")
					continue
				}
				if s.gen < lastGen {
					ckSeed(false, "snapshot generation regressed")
				}
				lastGen = s.gen
				if !snapValid(s, totalBatches) {
					ckSeed(false, fmt.Sprintf("reader observed invalid snapshot gen=%d", s.gen))
				}
				if i%37 == 0 {
					gcStress()
					if !snapValid(s, totalBatches) {
						ckSeed(false, "snapshot corrupted after GC")
					}
				}
				select {
				case <-stop:
					return
				default:
				}
			}
		}(r)
	}
	pwg.Wait()
	close(stop)
	rwg.Wait()

	final := bsCur.Load()
	if final == nil {
		ck(false, "final snapshot is nil")
		finish("l02_atomic_batch_snapshot")
		return
	}
	ckSeed(final.gen == int64(totalBatches), fmt.Sprintf("final gen=%d want=%d", final.gen, totalBatches))
	ckSeed(len(final.m) == totalBatches*bsBatchKeys,
		fmt.Sprintf("final keys=%d want=%d", len(final.m), totalBatches*bsBatchKeys))
	ckSeed(snapValid(final, totalBatches), "final snapshot invalid")
	var wantSum int64
	for b := 0; b < totalBatches; b++ {
		wantSum += batchSum(curSeed, b)
	}
	ckSeed(final.sum == wantSum, fmt.Sprintf("final sum=%d want=%d", final.sum, wantSum))
	for b := 0; b < totalBatches; b++ {
		for j := 0; j < bsBatchKeys; j++ {
			k := bKey(b, j)
			if got, ok := final.m[k]; !ok || got != bVal(curSeed, b, j) {
				ckSeed(false, fmt.Sprintf("final key %d missing or wrong", k))
			}
		}
	}

	gcStress()
	after := bsCur.Load()
	ckSeed(after != nil && after.gen == final.gen, "final generation changed after GC")
	ckSeed(snapValid(after, totalBatches), "final snapshot invalid after GC")
	var afterSum int64
	if after != nil {
		for _, v := range after.m {
			afterSum += v
		}
	}
	ckSeed(afterSum == wantSum, "final checksum changed after GC")

	fmt.Printf("publishers=%d batches=%d keys=%d final_gen=%d scale=%d\n",
		bsPubs, totalBatches, totalBatches*bsBatchKeys, final.gen, scale)
	finish("l02_atomic_batch_snapshot")
}
