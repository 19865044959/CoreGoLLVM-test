// L05: goroutine 内分配与所有权交接：8 个常驻 worker 各自持有一块 64KB 本地
// arena，按轮把 256 条记录切进 arena，构建指针链并计算摘要；每轮把 arena 与
// 一条 pin 记录“拷贝”后经 channel 交给主协程。主协程收齐一轮结果后制造垃圾并
// GC，再校验 handoff/pin 不受 worker 下一轮复用 arena 影响。
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
	gwWorkers = 8
	gwRecs    = 256
	gwBlock   = 64 << 10
	gwSlot    = gwBlock / gwRecs
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
		return hi
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

func gcStress() {
	for i := 0; i < 24; i++ {
		b := make([]byte, 64<<10)
		b[0] = byte(i)
		gcSink = append(gcSink[:0], b)
	}
	gcSink = gcSink[:0]
	runtime.GC()
	runtime.GC()
}

type gwRec struct {
	id   int
	name string
	data []byte
	next *gwRec
}

type gwResult struct {
	worker  int
	round   int
	digest  uint64
	handoff []byte
	pin     gwRec
	pinSum  uint64
}

func gwRecLen(i int) int {
	return 32 + (i*7)%120
}

func gwByte(worker, round, i, off int, seed uint64) byte {
	return byte((seed*19 + uint64(worker)*7 + uint64(round)*31 + uint64(i)*23 + uint64(off)*3) % 251)
}

func gwName(worker, round, i int) string {
	return fmt.Sprintf("w%d-r%d-%d", worker, round, i)
}

func gwDigestRecs(recs []gwRec) uint64 {
	h := uint64(1469598103934665603)
	for i := range recs {
		h ^= uint64(recs[i].id)
		h *= 1099511628211
		for j := 0; j < len(recs[i].name); j++ {
			h ^= uint64(recs[i].name[j])
			h *= 1099511628211
		}
		for j := range recs[i].data {
			h ^= uint64(recs[i].data[j])
			h *= 1099511628211
		}
	}
	return h
}

func gwExpected(worker, round int, handoff []byte, seed uint64) uint64 {
	h := uint64(1469598103934665603)
	for i := 0; i < gwRecs; i++ {
		id := round*100000 + worker*1000 + i
		name := gwName(worker, round, i)
		h ^= uint64(id)
		h *= 1099511628211
		for j := 0; j < len(name); j++ {
			h ^= uint64(name[j])
			h *= 1099511628211
		}
		n := gwRecLen(i)
		off := i * gwSlot
		for j := 0; j < n; j++ {
			h ^= uint64(handoff[off+j])
			h *= 1099511628211
		}
	}
	return h
}

func gwWorker(worker int, gates []chan struct{}, res chan<- gwResult, seed uint64, wg *sync.WaitGroup) {
	defer wg.Done()
	arena := make([]byte, gwBlock)
	for round := 0; round < len(gates); round++ {
		<-gates[round]
		recs := make([]gwRec, gwRecs)
		for i := 0; i < gwRecs; i++ {
			n := gwRecLen(i)
			off := i * gwSlot
			b := arena[off : off+n]
			for j := range b {
				b[j] = gwByte(worker, round, i, j, seed)
			}
			recs[i] = gwRec{id: round*100000 + worker*1000 + i, name: gwName(worker, round, i), data: b}
		}
		for i := 0; i < gwRecs-1; i++ {
			recs[i].next = &recs[i+1]
		}

		// 逐条沿 next 链访问，确保所有记录指针在 goroutine 内被使用。
		for p := &recs[0]; p != nil; p = p.next {
			if p.id < 0 {
				ckf(false, "worker=%d invalid rec id", worker)
				break
			}
		}
		digest := gwDigestRecs(recs)

		// 所有权交接：handoff 是 arena 的独立拷贝；pin 也是深拷贝。
		handoff := append([]byte(nil), arena...)
		pin := recs[37]
		pin.data = append([]byte(nil), recs[37].data...)
		pin.next = nil
		pinSum := gwDigestRecs([]gwRec{pin})

		res <- gwResult{worker: worker, round: round, digest: digest, handoff: handoff, pin: pin, pinSum: pinSum}
	}
}

func main() {
	seed := loadSeed()
	rounds := envInt("TORTURE_ROUNDS", 2, 1, 8)

	gates := make([]chan struct{}, rounds)
	for i := range gates {
		gates[i] = make(chan struct{})
	}
	resCh := make(chan gwResult, gwWorkers)
	var wg sync.WaitGroup
	for w := 0; w < gwWorkers; w++ {
		wg.Add(1)
		go gwWorker(w, gates, resCh, seed, &wg)
	}

	results := make([]gwResult, gwWorkers*rounds)
	seen := make([]bool, len(results))
	for round := 0; round < rounds; round++ {
		close(gates[round])
		for n := 0; n < gwWorkers; n++ {
			r := <-resCh
			idx := r.worker*rounds + r.round
			ckf(r.round == round, "result round=%d got=%d", round, r.round)
			ckf(!seen[idx], "duplicate result worker=%d round=%d", r.worker, r.round)
			seen[idx] = true
			results[idx] = r
		}
		// 收齐一轮后所有 worker 都在等待下一轮 gate；此时做垃圾分配 + GC。
		gcStress()
		verifyGwRound(results, seen, round, rounds, seed)
	}
	wg.Wait()

	gcStress()
	for i := range results {
		verifyGwResult(fmt.Sprintf("final idx=%d", i), &results[i], seed)
	}
	ckf(len(results) == gwWorkers*rounds, "result count changed")

	// 各结果的 handoff/pin 必须是独立副本：改写一个不应影响另一个。
	r0 := &results[0]
	r1 := &results[1]
	if len(r0.handoff) > 0 && len(r1.handoff) > 0 {
		before := r1.handoff[0]
		r0.handoff[0] ^= 0xff
		ckf(r1.handoff[0] == before, "handoff results alias each other")
	}
	if len(r0.pin.data) > 1 && len(r1.pin.data) > 1 {
		before := r1.pin.data[1]
		r0.pin.data[1] ^= 0xff
		ckf(r1.pin.data[1] == before, "pin results alias each other")
	}

	finish("l05_goroutine_local_arena", seed)
}

func verifyGwRound(results []gwResult, seen []bool, round, rounds int, seed uint64) {
	for w := 0; w < gwWorkers; w++ {
		idx := w*rounds + round
		ckf(seen[idx], "missing result worker=%d round=%d", w, round)
		if seen[idx] {
			verifyGwResult(fmt.Sprintf("round=%d worker=%d", round, w), &results[idx], seed)
		}
	}
}

func verifyGwResult(tag string, r *gwResult, seed uint64) {
	ckf(len(r.handoff) == gwBlock, "%s handoff len=%d want=%d", tag, len(r.handoff), gwBlock)
	if len(r.handoff) != gwBlock {
		return
	}
	for i := 0; i < gwRecs; i++ {
		n := gwRecLen(i)
		off := i * gwSlot
		for j := 0; j < n; j++ {
			if r.handoff[off+j] != gwByte(r.worker, r.round, i, j, seed) {
				ckf(false, "%s handoff byte i=%d off=%d", tag, i, j)
				break
			}
		}
	}
	ckf(r.digest == gwExpected(r.worker, r.round, r.handoff, seed), "%s digest mismatch", tag)
	ckf(r.pin.id == r.round*100000+r.worker*1000+37, "%s pin id=%d", tag, r.pin.id)
	ckf(r.pin.name == gwName(r.worker, r.round, 37), "%s pin name=%q", tag, r.pin.name)
	ckf(len(r.pin.data) == gwRecLen(37), "%s pin data len=%d", tag, len(r.pin.data))
	for j := 0; j < len(r.pin.data); j += 5 {
		if r.pin.data[j] != gwByte(r.worker, r.round, 37, j, seed) {
			ckf(false, "%s pin byte off=%d", tag, j)
			break
		}
	}
	ckf(r.pin.next == nil, "%s pin still links to worker recs", tag)
	ckf(r.pinSum == gwDigestRecs([]gwRec{r.pin}), "%s pin digest changed", tag)
}
