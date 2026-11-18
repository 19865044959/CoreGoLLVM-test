// L02: 保序 fan-out（nats fanout 有序投递 / webrtc datachannel 保序场景）：
// 打乱任务后按轮转分发给多个 worker，worker 逆序完成；sequencer 用 pending map
// 缓存乱序结果并按 0..N-1 输出，校验不丢不重、payload 跨 GC 完整。
package main

import (
	"fmt"
	"math/rand"
	"os"
	"runtime"
	"strconv"
	"sync"
	"sync/atomic"
)

const (
	ofWorkers   = 4
	ofBaseItems = 120
)

type ofPayload struct {
	idx int
	val int64
	tag string
}

type ofResult struct {
	idx int
	pay *ofPayload
}

var (
	failed int32
	gcMu   sync.Mutex
	gcSink [][]byte
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
		fmt.Printf("FAILED %s (%d failed checks)\n", name, n)
		os.Exit(1)
	}
	fmt.Println("ALL PASS " + name)
}

func seedVal() int64 {
	if s := os.Getenv("UAF_SEED"); s != "" {
		if n, err := strconv.Atoi(s); err == nil {
			return int64(n)
		}
	}
	return 1
}

func tortureScale() int {
	if s := os.Getenv("TORTURE_ROUNDS"); s != "" {
		if n, err := strconv.Atoi(s); err == nil && n > 1 {
			if n > 8 {
				n = 8
			}
			return n
		}
	}
	return 1
}

func (p *ofPayload) valid(seed int64) bool {
	if p == nil || p.idx < 0 || p.idx >= ofBaseItems*tortureScale() {
		return false
	}
	return p.val == seed+int64(p.idx)*3 && p.tag == fmt.Sprintf("i%d", p.idx)
}

func main() {
	seed := seedVal()
	scale := tortureScale()
	total := ofBaseItems * scale

	perm := make([]int, total)
	for i := range perm {
		perm[i] = i
	}
	r := rand.New(rand.NewSource(seed + 991))
	r.Shuffle(total, func(i, j int) { perm[i], perm[j] = perm[j], perm[i] })

	jobs := make([][]int, ofWorkers)
	for pos, idx := range perm {
		jobs[pos%ofWorkers] = append(jobs[pos%ofWorkers], idx)
	}
	// 每个 worker 逆序完成自己的任务，保证 sequencer 必须缓存乱序结果。
	for w := range jobs {
		for i, j := 0, len(jobs[w])-1; i < j; i, j = i+1, j-1 {
			jobs[w][i], jobs[w][j] = jobs[w][j], jobs[w][i]
		}
	}

	results := make(chan ofResult, ofWorkers*2)
	var wg sync.WaitGroup
	for w := 0; w < ofWorkers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for n, idx := range jobs[w] {
				pay := &ofPayload{idx: idx, val: seed + int64(idx)*3, tag: fmt.Sprintf("i%d", idx)}
				if n%23 == 0 {
					gcStress()
				}
				if pay.val != seed+int64(pay.idx)*3 || pay.tag != fmt.Sprintf("i%d", pay.idx) {
					ck(false, fmt.Sprintf("worker %d payload corrupted idx=%d", w, idx))
				}
				results <- ofResult{idx: idx, pay: pay}
			}
		}(w)
	}
	go func() {
		wg.Wait()
		close(results)
	}()

	next := 0
	emitted := 0
	maxPending := 0
	pending := make(map[int]*ofPayload)
	seen := make([]bool, total)
	all := make([]*ofPayload, 0, total)

	for res := range results {
		if res.idx < 0 || res.idx >= total {
			ck(false, fmt.Sprintf("result index out of range: %d", res.idx))
			continue
		}
		if seen[res.idx] {
			ck(false, fmt.Sprintf("duplicate result idx=%d", res.idx))
			continue
		}
		seen[res.idx] = true
		pending[res.idx] = res.pay
		if len(pending) > maxPending {
			maxPending = len(pending)
		}
		for {
			pay, ok := pending[next]
			if !ok {
				break
			}
			if pay.val != seed+int64(next)*3 || pay.tag != fmt.Sprintf("i%d", next) {
				ck(false, fmt.Sprintf("sequenced payload corrupted idx=%d", next))
			}
			all = append(all, pay)
			delete(pending, next)
			next++
			emitted++
			if emitted%50 == 0 {
				gcStress()
			}
		}
	}

	if emitted != total {
		ck(false, fmt.Sprintf("emitted=%d want=%d", emitted, total))
	}
	if next != total {
		ck(false, fmt.Sprintf("sequencer next=%d want=%d", next, total))
	}
	if len(pending) != 0 {
		ck(false, fmt.Sprintf("pending results left=%d", len(pending)))
	}
	if maxPending == 0 {
		ck(false, "sequencer never buffered an out-of-order result")
	}
	if len(all) != total {
		ck(false, fmt.Sprintf("ordered payloads=%d want=%d", len(all), total))
	}

	gcStress()
	for i, pay := range all {
		if pay.idx != i {
			ck(false, fmt.Sprintf("final order mismatch at %d got idx=%d", i, pay.idx))
		}
		if pay.val != seed+int64(pay.idx)*3 || pay.tag != fmt.Sprintf("i%d", pay.idx) {
			ck(false, fmt.Sprintf("ordered payload corrupted by final GC idx=%d", pay.idx))
		}
	}

	fmt.Printf("workers=%d items=%d emitted=%d\n", ofWorkers, total, emitted)
	finish("l02_ordered_fanout_sequence")
}
