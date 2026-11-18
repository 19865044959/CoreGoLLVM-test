// L05: goroutine churn 中的分配：每波启动一批短命 goroutine，各自分配
// 小对象图/字符串/切片并计算确定性摘要；后台 churn goroutine 持续制造垃圾，
// 波间显式 GC。校验所有结果、重复运行与 goroutine 数回落后不泄漏。
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
	defaultRounds = 2
	workers       = 48
	perWorker     = 192
)

var (
	failed int32
	gcSink [][]byte

	churnSink []byte
)

type churnItem struct {
	id   int64
	v    int64
	name string
	pad  []byte
}

type churnResult struct {
	wave   int
	worker int
	sum    int64
	hash   uint64
	n      int
}

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

func gcStress() {
	for i := 0; i < 16; i++ {
		b := make([]byte, 4096)
		b[0] = byte(i)
		gcSink = append(gcSink[:0], b)
	}
	gcSink = gcSink[:0]
	runtime.GC()
	runtime.GC()
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

func itemVal(wave, worker, i int, seed uint64) int64 {
	id := uint64(wave)*100000 + uint64(worker)*1000 + uint64(i)
	return int64((id*17 + seed*13) % 251)
}

func itemName(worker, i int) string {
	return "w" + strconv.Itoa(worker) + "-" + strconv.Itoa(i)
}

func hashStep(h uint64, x uint64) uint64 {
	h ^= x
	h *= 1099511628211
	return h
}

func hashString(h uint64, s string) uint64 {
	for i := 0; i < len(s); i++ {
		h = hashStep(h, uint64(s[i]))
	}
	return h
}

func expectedResult(wave, worker int, seed uint64) (int64, uint64, int) {
	var sum int64
	h := uint64(1469598103934665603)
	for i := 0; i < perWorker; i++ {
		v := itemVal(wave, worker, i, seed)
		sum += v
		h = hashString(h, itemName(worker, i))
		h = hashStep(h, uint64(v))
	}
	return sum, h, perWorker
}

func runWorker(wave, worker int, seed uint64) churnResult {
	items := make([]*churnItem, 0, perWorker)
	var sum int64
	h := uint64(1469598103934665603)
	for i := 0; i < perWorker; i++ {
		v := itemVal(wave, worker, i, seed)
		it := &churnItem{
			id:   int64(wave*100000 + worker*1000 + i),
			v:    v,
			name: itemName(worker, i),
			pad:  make([]byte, 8+(i*7+worker)%24),
		}
		for j := range it.pad {
			it.pad[j] = byte((uint64(v) + uint64(j)*3 + seed) % 251)
		}
		items = append(items, it)
		sum += v
		h = hashString(h, it.name)
		h = hashStep(h, uint64(v))
	}
	// items 随 goroutine 退出成为垃圾，下一波 GC 可回收。
	return churnResult{wave: wave, worker: worker, sum: sum, hash: h, n: len(items)}
}

func main() {
	seed := loadSeed()
	rounds := envInt("TORTURE_ROUNDS", defaultRounds, 1, 8)

	baseGoroutines := runtime.NumGoroutine()
	stop := make(chan struct{})
	var churnWG sync.WaitGroup
	churnWG.Add(1)
	go func() {
		defer churnWG.Done()
		i := 0
		for {
			select {
			case <-stop:
				return
			default:
			}
			b := make([]byte, 16<<10)
			b[i%(16<<10)] = byte(i)
			churnSink = b
			i++
		}
	}()

	type pinned struct {
		wave int
		r    churnResult
	}
	var pins []pinned

	for wave := 0; wave < rounds; wave++ {
		resCh := make(chan churnResult, workers)
		var wg sync.WaitGroup
		for w := 0; w < workers; w++ {
			wg.Add(1)
			go func(worker int) {
				defer wg.Done()
				resCh <- runWorker(wave, worker, seed)
			}(w)
		}
		wg.Wait()
		close(resCh)

		count := 0
		seen := make([]bool, workers)
		for r := range resCh {
			if r.wave != wave || r.worker < 0 || r.worker >= workers {
				ckf(false, "wave=%d bad result header worker=%d", wave, r.worker)
				continue
			}
			if seen[r.worker] {
				ckf(false, "wave=%d duplicate worker=%d", wave, r.worker)
			}
			seen[r.worker] = true
			wantSum, wantHash, wantN := expectedResult(wave, r.worker, seed)
			if r.sum != wantSum || r.hash != wantHash || r.n != wantN {
				ckf(false, "wave=%d worker=%d got sum=%d hash=%d n=%d want sum=%d hash=%d n=%d",
					wave, r.worker, r.sum, r.hash, r.n, wantSum, wantHash, wantN)
			}
			if r.worker == 0 && wave%2 == 0 {
				pins = append(pins, pinned{wave: wave, r: r})
			}
			count++
		}
		ckf(count == workers, "wave=%d collected=%d want=%d", wave, count, workers)
		for w := 0; w < workers; w++ {
			if !seen[w] {
				ckf(false, "wave=%d worker=%d missing", wave, w)
				break
			}
		}
		if wave%2 == 0 && !seen[0] {
			ckf(false, "wave=%d worker=0 not collected for pin", wave)
		}
		gcStress()
	}

	close(stop)
	churnWG.Wait()
	for i := 0; i < 8; i++ {
		junk := make([]byte, 1<<20)
		junk[0] = byte(i)
		gcSink = append(gcSink[:0], junk)
	}
	gcSink = gcSink[:0]
	runtime.GC()
	runtime.GC()
	runtime.Gosched()

	// 所有 worker 与 churn goroutine 已退出，数量应回落到基线附近。
	if after := runtime.NumGoroutine(); after > baseGoroutines+8 {
		ckf(false, "goroutine churn leak base=%d after=%d", baseGoroutines, after)
	}
	for k := range pins {
		wantSum, wantHash, wantN := expectedResult(pins[k].wave, 0, seed)
		if pins[k].r.sum != wantSum || pins[k].r.hash != wantHash || pins[k].r.n != wantN {
			ckf(false, "pinned wave=%d result corrupted", pins[k].wave)
		}
	}
	_ = churnSink

	finish("l05_goroutine_churn_alloc", seed)
}
