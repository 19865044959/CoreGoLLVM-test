// L02: 多级 pipeline + WaitGroup：source -> transform -> sink，全部传递指针 payload；
// 每级用 WaitGroup 收敛并在关闭下游 channel 前排空，GC 穿插在级间。
package main

import (
	"fmt"
	"os"
	"runtime"
	"sync"
	"sync/atomic"
)

const (
	nSources    = 4
	itemsPerSrc = 80
	nTransforms = 3
	totalItems  = nSources * itemsPerSrc
)

var (
	failed int32
	gcMu   sync.Mutex
	gcSink [][]byte
)

type item struct {
	id   int
	v    int64
	name string
}

type sinkResult struct {
	count int
	sum   int64
	seen  []int32
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

func sourceVal(id int) int64    { return int64(id*3 + 7) }
func transformVal(id int) int64 { return sourceVal(id)*2 + 1 }

func source(g int, out chan<- *item, start <-chan struct{}, wg *sync.WaitGroup) {
	defer wg.Done()
	<-start
	for i := 0; i < itemsPerSrc; i++ {
		id := g*itemsPerSrc + i
		it := &item{id: id, v: sourceVal(id), name: "src"}
		if i%31 == 0 {
			gcStress()
			ck(it.id == id && it.name == "src" && it.v == sourceVal(id),
				"source item corrupted by GC")
		}
		out <- it
	}
}

func transform(in <-chan *item, out chan<- *item, start <-chan struct{}, wg *sync.WaitGroup) {
	defer wg.Done()
	<-start
	n := 0
	for it := range in {
		if it == nil || it.name != "src" || it.v != sourceVal(it.id) {
			ck(false, "transform received corrupted input")
			continue
		}
		o := &item{id: it.id, v: transformVal(it.id), name: "t"}
		n++
		if n%8 == 0 {
			gcStress()
			if it.name != "src" || it.v != sourceVal(it.id) {
				ck(false, "transform input corrupted after GC")
			}
			if o.name != "t" || o.v != transformVal(o.id) {
				ck(false, "transform output corrupted after GC")
			}
		}
		out <- o
	}
}

func sink(in <-chan *item, res *sinkResult, wg *sync.WaitGroup) {
	defer wg.Done()
	seen := make([]int32, totalItems)
	var count int
	var sum int64
	for it := range in {
		if it == nil || it.name != "t" || it.id < 0 || it.id >= totalItems {
			ck(false, "sink received invalid item")
			continue
		}
		if it.v != transformVal(it.id) {
			ck(false, "sink value mismatch")
		}
		if !atomic.CompareAndSwapInt32(&seen[it.id], 0, 1) {
			ck(false, fmt.Sprintf("duplicate item at sink id=%d", it.id))
		}
		count++
		sum += it.v
		if count%32 == 0 {
			gcStress()
			if it.name != "t" || it.v != transformVal(it.id) {
				ck(false, "sink item corrupted after GC")
			}
		}
	}
	res.count = count
	res.sum = sum
	res.seen = seen
}

func main() {
	ch1 := make(chan *item, 8)
	ch2 := make(chan *item, 6)
	start := make(chan struct{})

	var srcWG, trWG, sinkWG sync.WaitGroup
	for g := 0; g < nSources; g++ {
		srcWG.Add(1)
		go source(g, ch1, start, &srcWG)
	}
	for i := 0; i < nTransforms; i++ {
		trWG.Add(1)
		go transform(ch1, ch2, start, &trWG)
	}
	var res sinkResult
	sinkWG.Add(1)
	go sink(ch2, &res, &sinkWG)

	go func() {
		srcWG.Wait()
		close(ch1)
	}()
	go func() {
		trWG.Wait()
		close(ch2)
	}()
	close(start)
	sinkWG.Wait()
	srcWG.Wait()
	trWG.Wait()

	ck(res.count == totalItems, fmt.Sprintf("sink count got=%d want=%d", res.count, totalItems))
	var wantSum int64
	for id := 0; id < totalItems; id++ {
		wantSum += transformVal(id)
	}
	ck(res.sum == wantSum, "sink checksum mismatch")
	missing := 0
	for id, v := range res.seen {
		if v != 1 {
			missing++
			if missing <= 4 {
				ck(false, fmt.Sprintf("missing item id=%d", id))
			}
		}
	}
	ck(missing == 0, fmt.Sprintf("missing items=%d", missing))

	runtime.KeepAlive(res.seen)
	fmt.Printf("sources=%d transforms=%d items=%d\n", nSources, nTransforms, res.count)
	finish("l02_waitgroup_pipeline")
}
