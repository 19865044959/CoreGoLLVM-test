// L02: select 多 channel 复用 + 关闭排空：每个 channel 由多个发送者填充，
// 关闭后 select 必须把所有消息恰好消费一次，且不 panic、不丢消息。
package main

import (
	"fmt"
	"os"
	"runtime"
	"sync"
	"sync/atomic"
)

const (
	nChans         = 3
	perChan        = 96
	perChanSenders = 2
)

var (
	failed int32
	gcMu   sync.Mutex
	gcSink [][]byte
)

type item struct {
	ch  int
	seq int
	val int64
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

func expectValue(ch, seq int) int64 { return int64(ch*1000 + seq) }

func handle(it *item, wantCh int, seen []int32) {
	if it == nil {
		ck(false, "mux received nil item")
		return
	}
	if it.ch != wantCh {
		ck(false, "item arrived on wrong channel")
	}
	if it.ch < 0 || it.ch >= nChans || it.seq < 0 || it.seq >= perChan {
		ck(false, "item fields out of range")
		return
	}
	idx := it.ch*perChan + it.seq
	if !atomic.CompareAndSwapInt32(&seen[idx], 0, 1) {
		ck(false, fmt.Sprintf("duplicate item ch=%d seq=%d", it.ch, it.seq))
	}
	if it.val != expectValue(it.ch, it.seq) {
		ck(false, "item value mismatch")
	}
}

func sender(c, s int, ch chan<- *item) {
	for seq := s; seq < perChan; seq += perChanSenders {
		it := &item{ch: c, seq: seq, val: expectValue(c, seq)}
		if seq%32 == 0 {
			gcStress()
			ck(it.ch == c && it.seq == seq, "sender item corrupted by GC")
		}
		ch <- it
	}
}

func main() {
	chans := make([]chan *item, nChans)
	for i := range chans {
		chans[i] = make(chan *item, 8)
	}

	var sw sync.WaitGroup
	for c := 0; c < nChans; c++ {
		for s := 0; s < perChanSenders; s++ {
			sw.Add(1)
			go func(c, s int) {
				defer sw.Done()
				sender(c, s, chans[c])
			}(c, s)
		}
	}
	allSent := make(chan struct{})
	go func() {
		sw.Wait()
		for _, ch := range chans {
			close(ch)
		}
		close(allSent)
	}()

	seen := make([]int32, nChans*perChan)
	c0, c1, c2 := chans[0], chans[1], chans[2]
	total := 0
	var held *item
	for c0 != nil || c1 != nil || c2 != nil {
		select {
		case it, ok := <-c0:
			if !ok {
				c0 = nil
				continue
			}
			held = it
			handle(it, 0, seen)
		case it, ok := <-c1:
			if !ok {
				c1 = nil
				continue
			}
			held = it
			handle(it, 1, seen)
		case it, ok := <-c2:
			if !ok {
				c2 = nil
				continue
			}
			held = it
			handle(it, 2, seen)
		}
		total++
		if total%48 == 0 {
			gcStress()
			if held != nil {
				ck(held.ch >= 0 && held.ch < nChans && held.seq >= 0 && held.seq < perChan,
					"held item fields corrupted by GC")
				ck(held.val == expectValue(held.ch, held.seq), "held item value corrupted by GC")
			}
		}
	}
	<-allSent

	want := nChans * perChan
	ck(total == want, fmt.Sprintf("mux total got=%d want=%d", total, want))
	missing := 0
	for i, v := range seen {
		if v != 1 {
			missing++
			if missing <= 4 {
				ck(false, fmt.Sprintf("missing item ch=%d seq=%d", i/perChan, i%perChan))
			}
		}
	}
	ck(missing == 0, fmt.Sprintf("missing items=%d", missing))

	fmt.Printf("channels=%d messages=%d\n", nChans, total)
	finish("l02_select_multiplex_close")
}
