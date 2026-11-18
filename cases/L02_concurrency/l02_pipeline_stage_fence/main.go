// L02: 五级流水线 + 故障栅栏：source -> s1 -> s2 -> s3(fence) -> s4 -> sink。
// 第 3 级只放行前 fenceAfter 条，其余全部排空丢弃；上游必须全部完成、
// 下游只收到放行条目；各级计数、载荷在 GC 前后必须自洽。
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
	pSources    = 3
	pPerSource  = 40
	pTotal      = pSources * pPerSource
	wStage1     = 4
	wStage2     = 3
	wStage3     = 4
	wStage4     = 2
	fenceAfter  = 50
	pipelineEnd = 5
)

var (
	failed  int32
	gcMu    sync.Mutex
	gcSink  [][]byte
	curSeed int64
)

type pmsg struct {
	id    int
	v     int64
	stage int
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

func valAt(seed int64, stage, id int) int64 {
	v := seed*131 + int64(id)*7 + 1
	mul := [pipelineEnd]int64{0, 3, 5, 7, 11}
	add := [pipelineEnd]int64{0, 2, 3, 4, 5}
	for s := 1; s <= stage; s++ {
		v = v*mul[s] + add[s]
	}
	return v
}

func validStage(m *pmsg, stage int) bool {
	return m != nil && m.stage == stage && m.v == valAt(curSeed, stage, m.id) &&
		m.id >= 0 && m.id < pTotal
}

// stageWorker 处理 stage 1..4；stage 3 额外做故障栅栏（原子领取位置，前
// fenceAfter 条放行，其余排空丢弃，保证上游不会被反压卡死）。
func stageWorker(in <-chan *pmsg, out chan<- *pmsg, stage, workers int, inCnt, outCnt, dropCnt *int64,
	wg *sync.WaitGroup, claimed *int64) {
	defer wg.Done()
	n := 0
	for m := range in {
		if !validStage(m, stage-1) {
			ckSeed(false, fmt.Sprintf("stage %d received corrupted input", stage))
			continue
		}
		atomic.AddInt64(inCnt, 1)
		n++
		forward := true
		if stage == 3 {
			pos := atomic.AddInt64(claimed, 1) - 1
			if pos >= fenceAfter {
				forward = false
			}
		}
		if forward {
			o := &pmsg{id: m.id, v: valAt(curSeed, stage, m.id), stage: stage}
			if n%16 == 0 {
				gcStress()
				if !validStage(o, stage) {
					ckSeed(false, fmt.Sprintf("stage %d output corrupted after GC", stage))
				}
			}
			out <- o
			atomic.AddInt64(outCnt, 1)
		} else {
			atomic.AddInt64(dropCnt, 1)
		}
	}
}

func main() {
	curSeed = envSeed()
	chans := make([]chan *pmsg, 5)
	for i := range chans {
		chans[i] = make(chan *pmsg, 8)
	}

	src := chans[0]
	s1 := chans[1]
	s2 := chans[2]
	s3 := chans[3]
	s4 := chans[4]

	var srcWG sync.WaitGroup
	var s1In, s1Out, s2In, s2Out, s3In, s3Out, s3Drop, s4In, s4Out int64
	var claimed int64
	var wg1, wg2, wg3, wg4 sync.WaitGroup

	for g := 0; g < pSources; g++ {
		srcWG.Add(1)
		go func(g int) {
			defer srcWG.Done()
			for i := 0; i < pPerSource; i++ {
				id := g*pPerSource + i
				m := &pmsg{id: id, v: valAt(curSeed, 0, id), stage: 0}
				if i%17 == 0 {
					gcStress()
					if !validStage(m, 0) {
						ckSeed(false, "source item corrupted before send")
					}
				}
				src <- m
			}
		}(g)
	}
	for i := 0; i < wStage1; i++ {
		wg1.Add(1)
		go stageWorker(src, s1, 1, wStage1, &s1In, &s1Out, new(int64), &wg1, &claimed)
	}
	for i := 0; i < wStage2; i++ {
		wg2.Add(1)
		go stageWorker(s1, s2, 2, wStage2, &s2In, &s2Out, new(int64), &wg2, &claimed)
	}
	for i := 0; i < wStage3; i++ {
		wg3.Add(1)
		go stageWorker(s2, s3, 3, wStage3, &s3In, &s3Out, &s3Drop, &wg3, &claimed)
	}
	for i := 0; i < wStage4; i++ {
		wg4.Add(1)
		go stageWorker(s3, s4, 4, wStage4, &s4In, &s4Out, new(int64), &wg4, &claimed)
	}

	go func() {
		srcWG.Wait()
		close(src)
	}()
	go func() {
		wg1.Wait()
		close(s1)
	}()
	go func() {
		wg2.Wait()
		close(s2)
	}()
	go func() {
		wg3.Wait()
		close(s3)
	}()
	go func() {
		wg4.Wait()
		close(s4)
	}()

	seen := make([]int32, pTotal)
	count := 0
	var sum int64
	var gotMismatch int
	for m := range s4 {
		if !validStage(m, 4) {
			ckSeed(false, "sink received corrupted item")
			continue
		}
		if !atomic.CompareAndSwapInt32(&seen[m.id], 0, 1) {
			ckSeed(false, fmt.Sprintf("sink received duplicate id=%d", m.id))
		}
		count++
		sum += m.v
		if count%13 == 0 {
			gcStress()
			if !validStage(m, 4) {
				ckSeed(false, "sink item corrupted after GC")
			}
		}
	}
	srcWG.Wait()
	wg1.Wait()
	wg2.Wait()
	wg3.Wait()
	wg4.Wait()

	var wantSum int64
	for id := 0; id < pTotal; id++ {
		if seen[id] == 1 {
			wantSum += valAt(curSeed, 4, id)
			gotMismatch++
		}
	}
	ckSeed(s1In == pTotal, fmt.Sprintf("stage1 input=%d want=%d", s1In, pTotal))
	ckSeed(s2In == pTotal, fmt.Sprintf("stage2 input=%d want=%d", s2In, pTotal))
	ckSeed(s3In == pTotal, fmt.Sprintf("stage3 input=%d want=%d", s3In, pTotal))
	ckSeed(s3Out == fenceAfter, fmt.Sprintf("stage3 forwarded=%d want=%d", s3Out, fenceAfter))
	ckSeed(s3Drop == pTotal-fenceAfter, fmt.Sprintf("stage3 dropped=%d want=%d", s3Drop, pTotal-fenceAfter))
	ckSeed(s4In == fenceAfter, fmt.Sprintf("stage4 input=%d want=%d", s4In, fenceAfter))
	ckSeed(count == fenceAfter, fmt.Sprintf("sink count=%d want=%d", count, fenceAfter))
	ckSeed(gotMismatch == fenceAfter, fmt.Sprintf("sink distinct=%d want=%d", gotMismatch, fenceAfter))
	ckSeed(sum == wantSum, "sink checksum mismatch")
	ckSeed(s1Out == pTotal && s2Out == pTotal && s4Out == fenceAfter, "per-stage forwarded counters mismatch")

	gcStress()
	ckSeed(sum == wantSum, "sink checksum changed after final GC")

	fmt.Printf("sources=%d items=%d fence=%d sink=%d\n", pSources, pTotal, fenceAfter, count)
	finish("l02_pipeline_stage_fence")
}
