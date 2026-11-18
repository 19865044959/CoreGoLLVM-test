// L02: 两级 fan-in/fan-out：2 源 fan-in 到 raw，raw fan-out 给 4 个 worker，
// 每个 worker 再按 id 奇偶 fan-out 给 2 个 leaf；8 个 leaf fan-in 到 2 个 collector，
// 最后 2 个 partial 结果 fan-in。所有阶段用 WaitGroup + close 级联收敛。
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
	nSources        = 2
	basePerSource   = 60
	nWorkers        = 4
	leavesPerWorker = 2
	nLeaves         = nWorkers * leavesPerWorker
	nCollectors     = 2
)

var (
	failed  int32
	gcMu    sync.Mutex
	gcSink  [][]byte
	curSeed int64
)

type item struct {
	id   int
	src  int
	seq  int
	val  int64
	name string
}

type result struct {
	count     int
	sum       int64
	collector int
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

func tagFor(seed int64, id, stage int) int64 {
	x := uint64(seed)*0x9E3779B97F4A7C15 + uint64(id+1)*0xBF58476D1CE4E5B9 + uint64(stage+1)
	x ^= x >> 30
	x *= 0x94D049BB133111EB
	x ^= x >> 31
	return int64(x >> 1)
}

func srcVal(id int) int64 { return tagFor(curSeed, id, 0) }
func midVal(id int) int64 { return srcVal(id)*3 + 1 }
func finVal(id int) int64 { return midVal(id)*5 + 2 }

func validItem(it *item, total int, name string, wantVal int64) bool {
	if it == nil || it.id < 0 || it.id >= total || it.name != name {
		return false
	}
	return it.val == wantVal
}

func main() {
	curSeed = envSeed()
	rounds := tortureRounds(1)
	perSource := basePerSource * rounds
	total := nSources * perSource

	raw := make(chan *item, 16)
	leafCh := make([]chan *item, nLeaves)
	for i := range leafCh {
		leafCh[i] = make(chan *item, 8)
	}
	colCh := make([]chan *item, nCollectors)
	for i := range colCh {
		colCh[i] = make(chan *item, 8)
	}
	results := make(chan result, nCollectors)
	seen := make([]int32, total)
	start := make(chan struct{})

	var srcWG, workWG, leafWG, colWG sync.WaitGroup

	for s := 0; s < nSources; s++ {
		srcWG.Add(1)
		go func(src int) {
			defer srcWG.Done()
			<-start
			for seq := 0; seq < perSource; seq++ {
				id := src*perSource + seq
				it := &item{id: id, src: src, seq: seq, val: srcVal(id), name: "src"}
				if seq%31 == 0 {
					gcStress()
					if !validItem(it, total, "src", srcVal(id)) {
						ckSeed(false, "source item corrupted before send")
					}
				}
				raw <- it
			}
		}(s)
	}
	go func() {
		srcWG.Wait()
		close(raw)
	}()

	for w := 0; w < nWorkers; w++ {
		workWG.Add(1)
		go func(worker int) {
			defer workWG.Done()
			<-start
			for it := range raw {
				if !validItem(it, total, "src", srcVal(it.id)) {
					ckSeed(false, "worker received corrupted input")
					continue
				}
				out := &item{id: it.id, src: it.src, seq: it.seq, val: midVal(it.id), name: "mid"}
				if it.id%16 == 0 {
					gcStress()
					if !validItem(it, total, "src", srcVal(it.id)) || !validItem(out, total, "mid", midVal(out.id)) {
						ckSeed(false, "worker input/output corrupted after GC")
					}
				}
				leaf := worker*leavesPerWorker + it.id%leavesPerWorker
				leafCh[leaf] <- out
			}
			close(leafCh[worker*leavesPerWorker])
			close(leafCh[worker*leavesPerWorker+1])
		}(w)
	}

	for l := 0; l < nLeaves; l++ {
		leafWG.Add(1)
		go func(leaf int) {
			defer leafWG.Done()
			<-start
			for it := range leafCh[leaf] {
				if !validItem(it, total, "mid", midVal(it.id)) {
					ckSeed(false, "leaf received corrupted input")
					continue
				}
				if !atomic.CompareAndSwapInt32(&seen[it.id], 0, 1) {
					ckSeed(false, fmt.Sprintf("duplicate item at leaf id=%d", it.id))
				}
				out := &item{id: it.id, src: it.src, seq: it.seq, val: finVal(it.id), name: "fin"}
				if it.id%8 == 0 {
					gcStress()
					if !validItem(out, total, "fin", finVal(out.id)) {
						ckSeed(false, "leaf output corrupted after GC")
					}
				}
				colCh[leaf%nCollectors] <- out
			}
		}(l)
	}
	go func() {
		leafWG.Wait()
		for _, ch := range colCh {
			close(ch)
		}
	}()

	for c := 0; c < nCollectors; c++ {
		colWG.Add(1)
		go func(col int) {
			defer colWG.Done()
			<-start
			res := result{collector: col}
			var held *item
			for it := range colCh[col] {
				if !validItem(it, total, "fin", finVal(it.id)) {
					ckSeed(false, "collector received corrupted input")
					continue
				}
				res.count++
				res.sum += it.val
				held = it
				if res.count%12 == 0 {
					gcStress()
					if !validItem(held, total, "fin", finVal(held.id)) {
						ckSeed(false, "collector input corrupted after GC")
					}
				}
			}
			results <- res
		}(c)
	}
	go func() {
		colWG.Wait()
		close(results)
	}()

	close(start)
	var gotCount int
	var gotSum int64
	for res := range results {
		gotCount += res.count
		gotSum += res.sum
	}
	srcWG.Wait()
	workWG.Wait()
	leafWG.Wait()
	colWG.Wait()

	var wantSum int64
	for id := 0; id < total; id++ {
		wantSum += finVal(id)
	}
	ckSeed(gotCount == total, fmt.Sprintf("merged count got=%d want=%d", gotCount, total))
	ckSeed(gotSum == wantSum, fmt.Sprintf("merged checksum got=%d want=%d", gotSum, wantSum))
	missing := 0
	for id, v := range seen {
		if v != 1 {
			missing++
			if missing <= 4 {
				ckSeed(false, fmt.Sprintf("item missing at leaves id=%d", id))
			}
		}
	}
	ckSeed(missing == 0, fmt.Sprintf("missing items=%d", missing))

	gcStress()
	fmt.Printf("sources=%d workers=%d leaves=%d collectors=%d items=%d\n",
		nSources, nWorkers, nLeaves, nCollectors, gotCount)
	finish("l02_fanin_fanout_two_level")
}
