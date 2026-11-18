// L17: chan struct{} 令牌环。4 个 worker 用无缓冲 chan struct{} 依次传递信号，
// 每轮持针 GC 并跨校验；最后一轮 close(stop) 广播退出。TORTURE_ROUNDS 放大圈数。
package main

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"sync"
	"sync/atomic"
)

const ringWorkers = 4

type item struct {
	v    int64
	tag  string
	next *item
}

type workerResult struct {
	idx      int
	laps     int
	chainSum int64
	seenSum  int64
	err      string
}

var (
	failed  int32
	uafSeed int64 = 1
)

func ck(ok bool, msg string) {
	if !ok {
		atomic.AddInt32(&failed, 1)
		fmt.Println("FAIL:", msg)
	}
}

//go:noinline
func gcStress() {
	var sink [][]byte
	for i := 0; i < 4; i++ {
		b := make([]byte, 1024)
		b[0] = byte(i)
		sink = append(sink, b)
	}
	runtime.GC()
	runtime.GC()
	runtime.KeepAlive(sink)
}

func seedFromEnv() int64 {
	if s := os.Getenv("UAF_SEED"); s != "" {
		if v, err := strconv.ParseInt(s, 10, 64); err == nil && v != 0 {
			return v
		}
	}
	return 1
}

func envRounds(def, max int) int {
	if s := os.Getenv("TORTURE_ROUNDS"); s != "" {
		if v, err := strconv.Atoi(s); err == nil && v > 0 {
			if v > max {
				return max
			}
			return v
		}
	}
	return def
}

func finish(name string) {
	if n := atomic.LoadInt32(&failed); n != 0 {
		fmt.Printf("FAILED %s (%d failed checks) seed=%d\n", name, n, uafSeed)
		os.Exit(1)
	}
	fmt.Println("ALL PASS " + name)
}

func mkItem(base int64) *item {
	return &item{v: base, tag: "ring", next: &item{v: base + 5, tag: "ring-next"}}
}

func newChain(base int64, idx int) []*item {
	chain := make([]*item, 6)
	for i := range chain {
		chain[i] = mkItem(base + int64(i)*10 + int64(idx))
	}
	return chain
}

func evalItems(list []*item) (int64, string) {
	var sum int64
	for i, p := range list {
		if p == nil {
			return sum, fmt.Sprintf("nil item at %d", i)
		}
		if p.tag != "ring" || p.next == nil || p.next.tag != "ring-next" || p.next.v != p.v+5 {
			return sum, fmt.Sprintf("item chain corrupted at %d", i)
		}
		sum += p.v + p.next.v
	}
	return sum, ""
}

func worker(idx int, base int64, laps int, turn []chan struct{}, stop chan struct{}, done chan<- workerResult, steps *int64, once *sync.Once) {
	chain := newChain(base, idx)
	_, initErr := evalItems(chain)
	errMsg := initErr

	var seen []*item
	var seenSum, chainTotal int64
	lapsDone := 0
	for {
		select {
		case <-turn[idx]:
			pre, err1 := evalItems(chain)
			gcStress()
			post, err2 := evalItems(chain)
			if errMsg == "" && (err1 != "" || err2 != "" || pre != post) {
				errMsg = "chain across GC: " + err1 + err2
			}
			chainTotal += post
			pick := chain[lapsDone%len(chain)]
			seen = append(seen, pick)
			if pick != nil && pick.next != nil {
				seenSum += pick.v + pick.next.v
			}
			lapsDone++
			gcStress()
			got, err3 := evalItems(seen)
			if errMsg == "" && (err3 != "" || got != seenSum) {
				errMsg = "seen across GC: " + err3
			}
			total := atomic.AddInt64(steps, 1)
			if total == int64(laps*ringWorkers) {
				once.Do(func() { close(stop) })
				done <- workerResult{idx: idx, laps: lapsDone, chainSum: chainTotal, seenSum: seenSum, err: errMsg}
				return
			}
			turn[(idx+1)%ringWorkers] <- struct{}{}
		case <-stop:
			done <- workerResult{idx: idx, laps: lapsDone, chainSum: chainTotal, seenSum: seenSum, err: errMsg}
			return
		}
	}
}

func main() {
	uafSeed = seedFromEnv()
	laps := envRounds(16, 2048)

	for r := 0; r < 2; r++ {
		turn := make([]chan struct{}, ringWorkers)
		for i := range turn {
			turn[i] = make(chan struct{})
		}
		stop := make(chan struct{})
		done := make(chan workerResult, ringWorkers)
		var steps int64
		var once sync.Once
		var wg sync.WaitGroup

		for i := 0; i < ringWorkers; i++ {
			base := int64(r*100000) + uafSeed%71 + int64(i*1000)
			wg.Add(1)
			go func(i int, base int64) {
				defer wg.Done()
				worker(i, base, laps, turn, stop, done, &steps, &once)
			}(i, base)
		}

		// 启动令牌环：先交给 worker 0。
		turn[0] <- struct{}{}
		wg.Wait()

		results := make([]workerResult, ringWorkers)
		for i := 0; i < ringWorkers; i++ {
			res := <-done
			results[res.idx] = res
		}
		if atomic.LoadInt64(&steps) != int64(laps*ringWorkers) {
			ck(false, fmt.Sprintf("round %d: processed tokens=%d want=%d seed=%d", r, steps, laps*ringWorkers, uafSeed))
		}
		var totalChain, wantTotalChain int64
		for i := 0; i < ringWorkers; i++ {
			base := int64(r*100000) + uafSeed%71 + int64(i*1000)
			chain := newChain(base, i)
			perChain, err := evalItems(chain)
			if err != "" {
				ck(false, fmt.Sprintf("round %d worker %d: expected chain bad: %s seed=%d", r, i, err, uafSeed))
			}
			wantTotalChain += int64(laps) * perChain
			res := results[i]
			if res.err != "" {
				ck(false, fmt.Sprintf("round %d worker %d: %s seed=%d", r, i, res.err, uafSeed))
			}
			if res.laps != laps {
				ck(false, fmt.Sprintf("round %d worker %d: laps=%d want=%d seed=%d", r, i, res.laps, laps, uafSeed))
			}
			if res.chainSum != int64(laps)*perChain {
				ck(false, fmt.Sprintf("round %d worker %d: chainSum=%d want=%d seed=%d", r, i, res.chainSum, int64(laps)*perChain, uafSeed))
			}
			var wantSeen int64
			for j := 0; j < laps; j++ {
				v := base + int64(j%6)*10 + int64(i)
				wantSeen += 2*v + 5
			}
			if res.seenSum != wantSeen {
				ck(false, fmt.Sprintf("round %d worker %d: seenSum=%d want=%d seed=%d", r, i, res.seenSum, wantSeen, uafSeed))
			}
			totalChain += res.chainSum
		}
		if totalChain != wantTotalChain {
			ck(false, fmt.Sprintf("round %d: total chain sum=%d want=%d seed=%d", r, totalChain, wantTotalChain, uafSeed))
		}
		gcStress()
	}

	finish("l17_chan_struct_token_ring")
}
