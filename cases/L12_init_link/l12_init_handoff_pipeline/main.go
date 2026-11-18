// L12: init-started goroutine handoff pipeline. init launches three chained
// pipeline workers plus a token handoff worker; all block on a start gate
// closed by main, which uses the channel close as the happens-before edge for
// the whole pointer graph. Unbuffered stage channels preserve job order; the
// result channel is buffered so the producer side can drain while main runs
// explicit GC.
package main

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"sync"
	"sync/atomic"
)

type payload struct {
	tag   string
	bytes []byte
}

type job struct {
	id   int
	v    int64
	note *payload
}

var (
	startGate = make(chan struct{})
	inCh      = make(chan *job)
	midCh     = make(chan *job)
	outCh     = make(chan *job)
	resultCh  = make(chan *job, 128)
	tokenCh   = make(chan *payload, 1)

	pipeWG      sync.WaitGroup
	tokenPtr    = &payload{tag: "handoff", bytes: []byte{1, 2, 3, 4}}
	stageStarts [4]int32
	gateSeen    [4]int32
)

var (
	failed  int32
	gcSink  [][]byte
	seedVal int64
	seedStr string
)

func ck(ok bool, msg string) {
	if !ok {
		atomic.AddInt32(&failed, 1)
		fmt.Println("FAIL:", msg)
	}
}

func gcStress(rounds int) {
	for r := 0; r < rounds; r++ {
		for i := 0; i < 16; i++ {
			b := make([]byte, 4096)
			b[0] = byte(i + r)
			gcSink = append(gcSink[:0], b)
		}
		runtime.GC()
	}
	gcSink = gcSink[:0]
}

func finish(name string) {
	if n := atomic.LoadInt32(&failed); n != 0 {
		fmt.Printf("FAILED %s (%d failed checks) seed=%s\n", name, n, seedStr)
		os.Exit(1)
	}
	fmt.Println("ALL PASS " + name)
}

func readSeed() int64 {
	s := os.Getenv("UAF_SEED")
	if s == "" {
		return 1
	}
	v, err := strconv.ParseInt(s, 10, 64)
	if err != nil || v < 0 {
		return 1
	}
	return v
}

func init() {
	pipeWG.Add(4)
	go stage1()
	go stage2()
	go stage3()
	go tokenWorker()
}

func main() {
	seedVal = readSeed()
	seedStr = strconv.FormatInt(seedVal, 10)

	const n = 64
	base := int64(seedVal % 7)
	jobs := make([]*job, n)
	for i := 0; i < n; i++ {
		jobs[i] = &job{
			id: i,
			v:  base + int64(i),
			note: &payload{
				tag:   fmt.Sprintf("job-%02d", i),
				bytes: []byte{byte(i), byte(base), 0x5a},
			},
		}
	}

	// Release the init-started workers and feed the pipeline.
	close(startGate)
	for _, j := range jobs {
		inCh <- j
	}
	close(inCh)

	// Main produces garbage and forces GC while the pipeline is in flight.
	gcStress(2)

	pipeWG.Wait()
	var got []*job
	for j := range resultCh {
		got = append(got, j)
	}

	tok := <-tokenCh
	ck(tok == tokenPtr, "token pointer identity")
	ck(tok != nil && tok.tag == "handoff" && len(tok.bytes) == 4 && tok.bytes[3] == 4, "token payload")

	ck(len(got) == n, fmt.Sprintf("result count %d", len(got)))
	for i := 0; i < len(got) && i < n; i++ {
		r := got[i]
		if r != jobs[i] {
			ck(false, fmt.Sprintf("result %d pointer identity", i))
			break
		}
		if r.id != i {
			ck(false, fmt.Sprintf("result %d id %d", i, r.id))
			break
		}
		wantV := (base+int64(i))*3 + 3
		if r.v != wantV {
			ck(false, fmt.Sprintf("result %d v=%d want %d", i, r.v, wantV))
			break
		}
		if r.note != jobs[i].note || r.note.tag != fmt.Sprintf("job-%02d", i) ||
			len(r.note.bytes) != 3 || r.note.bytes[0] != byte(i) || r.note.bytes[2] != 0x5a {
			ck(false, fmt.Sprintf("result %d payload", i))
			break
		}
	}

	// Every init-started worker must have started and observed the gate.
	for i := 0; i < 4; i++ {
		ck(atomic.LoadInt32(&stageStarts[i]) == 1, fmt.Sprintf("worker %d start count", i))
		ck(atomic.LoadInt32(&gateSeen[i]) == 1, fmt.Sprintf("worker %d gate count", i))
	}

	// Concurrent read-only walk while main forces more GC.
	var wg sync.WaitGroup
	var bad int32
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for k := 0; k < 200; k++ {
				idx := (g + k) % n
				if jobs[idx].v != (base+int64(idx))*3+3 || jobs[idx].note.bytes[1] != byte(base) {
					atomic.AddInt32(&bad, 1)
					return
				}
				if jobs[idx].note.tag != fmt.Sprintf("job-%02d", idx) {
					atomic.AddInt32(&bad, 1)
					return
				}
			}
		}(g)
	}
	gcStress(3)
	wg.Wait()
	ck(atomic.LoadInt32(&bad) == 0, "concurrent job graph read failed under GC")

	gcStress(2)
	ck(tokenPtr.tag == "handoff" && tokenPtr.bytes[0] == 1, "token changed after GC")
	sum := int64(0)
	for _, j := range jobs {
		sum += j.v
	}
	wantSum := int64(0)
	for i := 0; i < n; i++ {
		wantSum += (base+int64(i))*3 + 3
	}
	ck(sum == wantSum, "job graph changed after GC")
	ck(got[0] == jobs[0] && got[n-1] == jobs[n-1], "result pointers changed after GC")

	finish("l12_init_handoff_pipeline")
}
