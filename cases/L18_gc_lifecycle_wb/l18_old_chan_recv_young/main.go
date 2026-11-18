// L18: 老 chan 接收年轻对象——缓冲 chan 中跨 GC 保留，接收后存入老 holder；
// 以及无缓冲老 chan 上由 goroutine 生产、主协程接收并跨 GC 保留。
package main

import (
	"fmt"
	"os"
	"runtime"
	"sync/atomic"
)

const (
	pktDataN = 24
	phase1N  = 64
	phase2N  = 64
)

type packet struct {
	seq  int64
	tag  string
	data []byte
}

type hub struct {
	ch   chan *packet
	got  []*packet
	got2 []*packet
}

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

func gcStress() {
	for i := 0; i < 16; i++ {
		b := make([]byte, 4096)
		b[0] = byte(i)
		gcSink = append(gcSink[:0], b)
	}
	gcSink = gcSink[:0]
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

func pktByte(seq int64, i int) byte { return byte((int(seq)*23 + i*7) % 251) }

func newPacket(seq int64) *packet {
	p := &packet{seq: seq, tag: "pk", data: make([]byte, pktDataN)}
	for i := range p.data {
		p.data[i] = pktByte(seq, i)
	}
	return p
}

func checkPacket(p *packet, seq int64) bool {
	if p == nil || p.seq != seq || p.tag != "pk" || len(p.data) != pktDataN {
		return false
	}
	for i := range p.data {
		if p.data[i] != pktByte(seq, i) {
			return false
		}
	}
	return true
}

// promoteHub 让 chan 与接收容器晋升为老对象。
//
//go:noinline
func promoteHub(capacity int) *hub {
	h := &hub{
		ch:   make(chan *packet, capacity),
		got:  make([]*packet, 0, phase1N),
		got2: make([]*packet, 0, phase2N),
	}
	gcStress()
	gcStress()
	return h
}

// produce 在 goroutine 内分配年轻 packet 并发送，完成后关闭 chan。
//
//go:noinline
func produce(h *hub, n int) {
	for i := 0; i < n; i++ {
		h.ch <- newPacket(int64(i))
	}
	close(h.ch)
}

// recvAllBuffered 在 noinline 内接收，并写入老 holder 的 got。
//
//go:noinline
func recvAllBuffered(h *hub, n int) {
	for i := 0; i < n; i++ {
		p := <-h.ch
		h.got = append(h.got, p)
	}
}

// recvOne 接收一条消息并追加到老 holder 的 got2。
//
//go:noinline
func recvOne(h *hub) bool {
	p, ok := <-h.ch
	if ok {
		h.got2 = append(h.got2, p)
	}
	return ok
}

func verifyPackets(ps []*packet) {
	if len(ps) != phase1N {
		ck(false, fmt.Sprintf("received packet count %d", len(ps)))
		return
	}
	for i, p := range ps {
		if !checkPacket(p, int64(i)) {
			ck(false, fmt.Sprintf("packet %d corrupted", i))
			return
		}
	}
}

func main() {
	// 阶段一：老缓冲 chan 内只靠老 chan 引用年轻对象，跨 GC 后再接收。
	h1 := promoteHub(phase1N)
	produce(h1, phase1N)
	gcStress()
	gcStress()
	recvAllBuffered(h1, phase1N)
	gcStress()
	verifyPackets(h1.got)

	// 阶段二：无缓冲老 chan，生产者 goroutine 分配，主协程接收并跨 GC 保留。
	h2 := promoteHub(0)
	go produce(h2, phase2N)
	for i := 0; i < phase2N; i++ {
		ok := recvOne(h2)
		ck(ok, "unexpected channel close before all packets received")
		if i%16 == 15 {
			gcStress()
		}
	}
	ok := recvOne(h2)
	ck(!ok, "channel delivered packets after expected close")
	gcStress()
	if len(h2.got2) != phase2N {
		ck(false, "phase2 received packet count mismatch")
	} else {
		for i, p := range h2.got2 {
			if !checkPacket(p, int64(i)) {
				ck(false, fmt.Sprintf("phase2 packet %d corrupted", i))
				break
			}
		}
	}

	runtime.KeepAlive(h1)
	runtime.KeepAlive(h2)
	finish("l18_old_chan_recv_young")
}
