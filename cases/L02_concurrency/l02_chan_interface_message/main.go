// L02: chan of interface：3 个生产者经有界 channel 发送不同动态类型的 event 接口值，
// 单消费者用 type switch 分派并校验接口/指针跨 GC；另含 nil 接口与类型断言判读。
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
	nProducers  = 3
	basePerProd = 100
	chanCap     = 12
)

var (
	failed  int32
	gcMu    sync.Mutex
	gcSink  [][]byte
	curSeed int64
	perProd int
)

type blob struct {
	tag  int64
	name string
}

type event interface {
	Kind() int
	Prod() int
	Seq() int
	Valid() bool
	Blob() *blob
}

type addEvent struct {
	prod int
	seq  int
	tag  int64
	p    *blob
}

type delEvent struct {
	prod int
	seq  int
	tag  int64
	p    *blob
}

func (e *addEvent) Kind() int   { return 1 }
func (e *addEvent) Prod() int   { return e.prod }
func (e *addEvent) Seq() int    { return e.seq }
func (e *addEvent) Blob() *blob { return e.p }
func (e *addEvent) Valid() bool {
	return e != nil && e.p != nil && e.p.name == "ev" && e.p.tag == e.tag && e.tag == tagFor(curSeed, e.prod, e.seq)
}

func (e *delEvent) Kind() int   { return 2 }
func (e *delEvent) Prod() int   { return e.prod }
func (e *delEvent) Seq() int    { return e.seq }
func (e *delEvent) Blob() *blob { return e.p }
func (e *delEvent) Valid() bool {
	return e != nil && e.p != nil && e.p.name == "ev" && e.p.tag == e.tag && e.tag == tagFor(curSeed, e.prod, e.seq)
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

func tagFor(seed int64, prod, seq int) int64 {
	x := uint64(seed)*0x9E3779B97F4A7C15 + uint64(prod+1)*0xBF58476D1CE4E5B9 + uint64(seq+1)
	x ^= x >> 30
	x *= 0x94D049BB133111EB
	x ^= x >> 31
	return int64(x >> 1)
}

func sender(prod int, start <-chan struct{}, ch chan<- event, wg *sync.WaitGroup) {
	defer wg.Done()
	<-start
	for seq := 0; seq < perProd; seq++ {
		tag := tagFor(curSeed, prod, seq)
		p := &blob{tag: tag, name: "ev"}
		var e event
		if seq%2 == 0 {
			e = &addEvent{prod: prod, seq: seq, tag: tag, p: p}
		} else {
			e = &delEvent{prod: prod, seq: seq, tag: tag, p: p}
		}
		if seq%37 == 0 {
			gcStress()
			if !e.Valid() {
				ckSeed(false, "sender event corrupted before send")
			}
		}
		ch <- e
	}
}

func main() {
	curSeed = envSeed()
	perProd = basePerProd
	ch := make(chan event, chanCap)
	ch <- event(nil)

	start := make(chan struct{})
	var sw sync.WaitGroup
	for p := 0; p < nProducers; p++ {
		sw.Add(1)
		go sender(p, start, ch, &sw)
	}
	close(start)

	var addCount, delCount, nilCount int
	var addSum, delSum int64
	next := make([]int, nProducers)
	var held event
	done := make(chan struct{})
	go func() {
		defer close(done)
		for e := range ch {
			if e == nil {
				nilCount++
				continue
			}
			prod := e.Prod()
			seq := e.Seq()
			kind := e.Kind()
			if prod < 0 || prod >= nProducers || seq < 0 || seq >= perProd {
				ckSeed(false, "event fields out of range")
				continue
			}
			if seq != next[prod] {
				ckSeed(false, fmt.Sprintf("event out of order or duplicate prod=%d seq=%d want=%d", prod, seq, next[prod]))
			}
			next[prod] = seq + 1
			if !e.Valid() {
				ckSeed(false, "event failed interface validation")
			}
			switch v := e.(type) {
			case *addEvent:
				if kind != 1 || v.p == nil || v.p.tag != v.tag {
					ckSeed(false, "add event kind/payload mismatch")
				}
				addCount++
				addSum += v.tag
			case *delEvent:
				if kind != 2 || v.p == nil || v.p.tag != v.tag {
					ckSeed(false, "del event kind/payload mismatch")
				}
				delCount++
				delSum += v.tag
			default:
				ck(false, "unexpected dynamic event type")
			}
			held = e
			if (addCount+delCount)%24 == 0 {
				gcStress()
				if held == nil || !held.Valid() {
					ckSeed(false, "held interface event corrupted after GC")
				}
				if b := held.Blob(); b == nil || b.tag != tagFor(curSeed, held.Prod(), held.Seq()) {
					ckSeed(false, "held interface payload corrupted after GC")
				}
			}
		}
	}()

	sw.Wait()
	close(ch)
	<-done
	gcStress()

	wantTotal := nProducers * perProd
	ckSeed(addCount+delCount == wantTotal,
		fmt.Sprintf("events got=%d want=%d", addCount+delCount, wantTotal))
	ck(nilCount == 1, fmt.Sprintf("nil interface count got=%d want=1", nilCount))
	ckSeed(addCount == wantTotal/2, fmt.Sprintf("add events got=%d want=%d", addCount, wantTotal/2))
	ckSeed(delCount == wantTotal/2, fmt.Sprintf("del events got=%d want=%d", delCount, wantTotal/2))
	for p := 0; p < nProducers; p++ {
		ckSeed(next[p] == perProd, fmt.Sprintf("producer %d delivered %d/%d", p, next[p], perProd))
	}
	var wantAdd, wantDel int64
	for p := 0; p < nProducers; p++ {
		for seq := 0; seq < perProd; seq++ {
			if seq%2 == 0 {
				wantAdd += tagFor(curSeed, p, seq)
			} else {
				wantDel += tagFor(curSeed, p, seq)
			}
		}
	}
	ckSeed(addSum == wantAdd, "add checksum mismatch")
	ckSeed(delSum == wantDel, "del checksum mismatch")

	var typedNil event = (*addEvent)(nil)
	ck(typedNil != nil, "typed nil interface must not equal nil")
	var empty event
	ck(empty == nil, "zero interface must equal nil")
	if _, ok := any(42).(event); ok {
		ck(false, "int unexpectedly implements event")
	}

	fmt.Printf("producers=%d messages=%d add=%d del=%d nil=%d\n",
		nProducers, addCount+delCount, addCount, delCount, nilCount)
	finish("l02_chan_interface_message")
}
