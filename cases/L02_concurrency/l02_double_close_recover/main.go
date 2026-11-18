// L02: 幂等 Close + recover 判读：16 路并发调用 Once 保护的 Close 不 panic，
// 原始 channel 的双重 close、nil close、向已关闭 channel 发送均被 recover 判定；
// 排空者与 done 等待者必须被精确唤醒。
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
	prefill   = 8
	nDrainers = 4
	nWaiters  = 8
	nClosers  = 16
)

var (
	failed  int32
	gcMu    sync.Mutex
	gcSink  [][]byte
	curSeed int64
)

type packet struct {
	id   int
	tag  int64
	name string
}

type hub struct {
	data      chan *packet
	done      chan struct{}
	once      sync.Once
	rawCloses int32
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

func tagFor(seed int64, id int) int64 {
	x := uint64(seed)*0x9E3779B97F4A7C15 + uint64(id+1)*0xBF58476D1CE4E5B9
	x ^= x >> 30
	x *= 0x94D049BB133111EB
	x ^= x >> 31
	return int64(x >> 1)
}

func newPacket(id int) *packet {
	return &packet{id: id, tag: tagFor(curSeed, id), name: "pkt"}
}

func validPacket(p *packet, id int) bool {
	return p != nil && p.id == id && p.name == "pkt" && p.tag == tagFor(curSeed, id)
}

func newHub() *hub {
	return &hub{data: make(chan *packet, prefill), done: make(chan struct{})}
}

func (h *hub) Close() {
	h.once.Do(func() {
		atomic.AddInt32(&h.rawCloses, 1)
		close(h.data)
		close(h.done)
	})
}

func safeHubClose(h *hub) (panicked bool) {
	defer func() {
		if recover() != nil {
			panicked = true
		}
	}()
	h.Close()
	return false
}

func safeClose[T any](ch chan T) (panicked bool) {
	defer func() {
		if recover() != nil {
			panicked = true
		}
	}()
	close(ch)
	return false
}

func safeSend(ch chan *packet, p *packet) (panicked bool) {
	defer func() {
		if recover() != nil {
			panicked = true
		}
	}()
	ch <- p
	return false
}

func safeCloseNil() (panicked bool) {
	var ch chan *packet
	defer func() {
		if recover() != nil {
			panicked = true
		}
	}()
	close(ch)
	return false
}

func noPanicRecover() (panicked bool) {
	defer func() {
		if recover() != nil {
			panicked = true
		}
	}()
	return false
}

func main() {
	curSeed = envSeed()
	h := newHub()
	for i := 0; i < prefill; i++ {
		h.data <- newPacket(i)
	}
	seen := make([]int32, prefill)
	started := make(chan struct{}, nDrainers+nWaiters)
	var drainWG, waitWG, closeWG sync.WaitGroup
	var drained int32
	var woke int32

	for i := 0; i < nDrainers; i++ {
		drainWG.Add(1)
		go func() {
			defer drainWG.Done()
			started <- struct{}{}
			var held *packet
			n := 0
			for p := range h.data {
				if !validPacket(p, p.id) {
					ckSeed(false, "drainer received corrupted packet")
					continue
				}
				if p.id < 0 || p.id >= prefill {
					ckSeed(false, "drainer received out-of-range id")
					continue
				}
				if !atomic.CompareAndSwapInt32(&seen[p.id], 0, 1) {
					ckSeed(false, "drainer saw duplicate packet")
				}
				held = p
				n++
				atomic.AddInt32(&drained, 1)
				if n%3 == 0 {
					gcStress()
					if !validPacket(held, held.id) {
						ckSeed(false, "drained packet corrupted after GC")
					}
				}
			}
		}()
	}
	for i := 0; i < nWaiters; i++ {
		waitWG.Add(1)
		go func(id int) {
			defer waitWG.Done()
			tok := newPacket(100 + id)
			started <- struct{}{}
			<-h.done
			gcStress()
			if !validPacket(tok, 100+id) {
				ckSeed(false, "waiter token corrupted after GC")
			}
			atomic.AddInt32(&woke, 1)
		}(i)
	}
	for i := 0; i < nDrainers+nWaiters; i++ {
		<-started
	}
	gcStress()

	var closePanics int32
	for i := 0; i < nClosers; i++ {
		closeWG.Add(1)
		go func() {
			defer closeWG.Done()
			if safeHubClose(h) {
				atomic.AddInt32(&closePanics, 1)
			}
		}()
	}
	closeWG.Wait()
	drainWG.Wait()
	waitWG.Wait()

	ck(atomic.LoadInt32(&closePanics) == 0, "idempotent Close panicked")
	ck(atomic.LoadInt32(&h.rawCloses) == 1, "underlying close ran more than once")
	ck(atomic.LoadInt32(&drained) == prefill,
		fmt.Sprintf("drained got=%d want=%d", drained, prefill))
	ck(atomic.LoadInt32(&woke) == nWaiters,
		fmt.Sprintf("woken waiters got=%d want=%d", woke, nWaiters))
	missing := 0
	for _, v := range seen {
		if v != 1 {
			missing++
		}
	}
	ck(missing == 0, fmt.Sprintf("missing drained packets=%d", missing))

	if !safeClose(h.data) {
		ck(false, "raw double close of data channel did not panic")
	}
	if !safeClose(h.data) {
		ck(false, "second raw close of data channel did not panic")
	}
	if !safeClose(h.done) {
		ck(false, "raw close of already-closed done channel did not panic")
	}
	if !safeCloseNil() {
		ck(false, "close of nil channel did not panic")
	}
	late := newPacket(999)
	if !safeSend(h.data, late) {
		ck(false, "send on closed data channel did not panic")
	}
	if noPanicRecover() {
		ck(false, "recover on non-panicking path returned non-nil")
	}
	gcStress()
	if !validPacket(late, 999) {
		ckSeed(false, "packet corrupted after post-close GC")
	}

	fmt.Printf("drained=%d waiters=%d close_panics=0 raw_double_close=1 raw_nil_close=1 raw_send_closed=1\n",
		drained, woke)
	finish("l02_double_close_recover")
}
