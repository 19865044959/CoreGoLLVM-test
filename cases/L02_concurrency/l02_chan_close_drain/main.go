// L02: 关闭 channel 后仍需排空剩余指针消息；重复 close / 向已关闭 channel
// 发送用 recover 判读，并发 close 恰好一个成功。
package main

import (
	"fmt"
	"os"
	"runtime"
	"sync"
	"sync/atomic"
)

const (
	prefill  = 12
	chCap    = 16
	nClosers = 8
)

var (
	failed int32
	gcMu   sync.Mutex
	gcSink [][]byte
)

type blob struct {
	tag  int64
	name string
}

type item struct {
	id int
	p  *blob
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

func expectTag(id int) int64 { return int64(id*7 + 1) }

func safeClose(ch chan *item) (panicked bool) {
	defer func() {
		if recover() != nil {
			panicked = true
		}
	}()
	close(ch)
	return false
}

func safeSend(ch chan *item, it *item) (panicked bool) {
	defer func() {
		if recover() != nil {
			panicked = true
		}
	}()
	ch <- it
	return false
}

func main() {
	ch := make(chan *item, chCap)
	first := &blob{tag: expectTag(0), name: "blob"}
	ch <- &item{id: 0, p: first}
	for i := 1; i < prefill; i++ {
		ch <- &item{id: i, p: &blob{tag: expectTag(i), name: "blob"}}
	}
	gcStress()
	ck(first.tag == expectTag(0) && first.name == "blob",
		"held payload corrupted by GC before close")

	close(ch)

	got := 0
	for it := range ch {
		if it == nil || it.id != got {
			ck(false, "drain order mismatch")
		} else if it.p == nil || it.p.tag != expectTag(it.id) || it.p.name != "blob" {
			ck(false, "drained payload corrupted")
		}
		got++
		if got%4 == 0 {
			gcStress()
			if it.p == nil || it.p.tag != expectTag(it.id) || it.p.name != "blob" {
				ck(false, "drained payload corrupted after GC")
			}
		}
	}
	ck(got == prefill, fmt.Sprintf("drained=%d want=%d", got, prefill))

	// 已关闭 channel 的接收永远就绪，select 不应落到 default。
	for i := 0; i < 16; i++ {
		select {
		case v, ok := <-ch:
			if ok || v != nil {
				ck(false, "closed channel returned a value")
			}
		default:
			ck(false, "closed channel receive not ready in select")
		}
	}

	// 并发 close：恰好一个成功，其余 recover 到 panic。
	raceCh := make(chan *item, 1)
	raceCh <- &item{id: -1, p: &blob{tag: -1, name: "race"}}
	start := make(chan struct{})
	var wg sync.WaitGroup
	var successes, panics int32
	for i := 0; i < nClosers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			if safeClose(raceCh) {
				atomic.AddInt32(&panics, 1)
			} else {
				atomic.AddInt32(&successes, 1)
			}
		}()
	}
	close(start)
	wg.Wait()
	ck(atomic.LoadInt32(&successes) == 1, fmt.Sprintf("close winners got=%d want=1", successes))
	ck(atomic.LoadInt32(&panics) == nClosers-1, fmt.Sprintf("close panics got=%d want=%d", panics, nClosers-1))
	ck(safeClose(raceCh), "closing an already closed channel must panic")
	ck(safeSend(raceCh, &item{id: 99, p: &blob{tag: 99, name: "late"}}),
		"sending on a closed channel must panic")

	drainedRace := 0
	for it := range raceCh {
		if it == nil || it.id != -1 || it.p == nil || it.p.name != "race" {
			ck(false, "race channel residual item corrupted")
		}
		drainedRace++
	}
	ck(drainedRace == 1, fmt.Sprintf("race channel residual got=%d want=1", drainedRace))

	fmt.Printf("prefill=%d drained=%d close_winners=1\n", prefill, got)
	finish("l02_chan_close_drain")
}
