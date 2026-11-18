// L18: 老 channel 向老消费者传递年轻对象。
//
//	阶段一：老缓冲 chan（cap=64）先缓存 64 个含 *packet/string 的组合值，
//	        跨两轮 GC 后由 noinline 消费者取出写入老 ring slice；
//	阶段二：老无缓冲 chan 上 goroutine 生产 96 个年轻 packet，主协程接收并
//	        写入老 ring slice，期间穿插 GC，最后校验 ring 中最近 64 条。
package main

import (
	"fmt"
	"os"
	"runtime"
	"sync/atomic"
	"time"
)

const (
	cBufferedN = 64
	cDirectN   = 96
	cRingN     = 64
	cDataN     = 32
)

type cNode struct {
	v    int64
	name string
}

type cPkt struct {
	seq  int64
	tag  string
	n    *cNode
	data [cDataN]byte
}

type cEnv struct {
	seq  int64
	pkt  *cPkt
	mode string
}

type cHub struct {
	buffered chan cEnv
	direct   chan *cPkt
	ring     []*cPkt
	idx      int
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

func pktByte(seq int64, i int) byte { return byte((int(seq)*9 + i*5) % 251) }

//go:noinline
func newPkt(seq int64, tag string) *cPkt {
	p := &cPkt{seq: seq, tag: tag, n: &cNode{v: seq * 10, name: "cn"}}
	for i := range p.data {
		p.data[i] = pktByte(seq, i)
	}
	return p
}

//go:noinline
func buildHub() *cHub {
	h := &cHub{
		buffered: make(chan cEnv, cBufferedN),
		direct:   make(chan *cPkt),
		ring:     make([]*cPkt, cRingN),
	}
	gcStress()
	gcStress() // hub / 两个 chan / ring 底层数组晋升为老对象
	return h
}

//go:noinline
func fillBuffered(ch chan<- cEnv, n int) {
	for i := 0; i < n; i++ {
		seq := int64(i)
		ch <- cEnv{seq: seq, pkt: newPkt(seq, "b"), mode: "buffered"}
	}
}

//go:noinline
func drainBuffered(ch <-chan cEnv, ring []*cPkt) int {
	i := 0
	for i < len(ring) {
		e := <-ch
		ring[i] = e.pkt
		i++
	}
	return i
}

func checkPkt(p *cPkt, seq int64, tag string) bool {
	if p == nil || p.seq != seq || p.tag != tag {
		return false
	}
	if p.n == nil || p.n.v != seq*10 || p.n.name != "cn" {
		return false
	}
	for i := range p.data {
		if p.data[i] != pktByte(seq, i) {
			return false
		}
	}
	return true
}

func main() {
	h := buildHub()

	fillBuffered(h.buffered, cBufferedN)
	// 对象只被老 chan 缓冲引用，跨两轮 GC 必须存活。
	gcStress()
	gcStress()
	n := drainBuffered(h.buffered, h.ring)
	ck(n == cBufferedN, fmt.Sprintf("buffered drain got %d want %d", n, cBufferedN))
	ck(len(h.buffered) == 0, "buffered channel not empty after drain")
	for i := 0; i < cBufferedN; i++ {
		if !checkPkt(h.ring[i], int64(i), "b") {
			ck(false, fmt.Sprintf("buffered packet %d corrupted", i))
			break
		}
	}

	done := make(chan struct{})
	go func() {
		for i := 0; i < cDirectN; i++ {
			seq := int64(1000 + i)
			h.direct <- newPkt(seq, "d")
		}
		close(h.direct)
		close(done)
	}()

	timeout := false
	for i := 0; i < cDirectN; i++ {
		select {
		case p, ok := <-h.direct:
			if !ok {
				ck(false, "direct channel closed before all packets received")
				timeout = true
				break
			}
			h.ring[h.idx] = p
			h.idx = (h.idx + 1) % cRingN
			if i%16 == 15 {
				runtime.GC()
			}
		case <-time.After(10 * time.Second):
			ck(false, "direct channel receive timeout")
			timeout = true
		}
		if timeout {
			break
		}
	}
	if timeout {
		go func() {
			for range h.direct {
			}
		}()
	}
	<-done

	// 最后 64 条按环形顺序为 1032..1095。
	for k := 0; k < cRingN; k++ {
		p := h.ring[(h.idx+k)%cRingN]
		want := int64(1000 + cDirectN - cRingN + k)
		if !checkPkt(p, want, "d") {
			ck(false, fmt.Sprintf("ring slot %d expected seq %d corrupted", k, want))
			break
		}
	}
	// 老 ring slice 中较早接收的 packet 在多次 GC 后仍应完整。
	if !timeout {
		gcStress()
		first := h.ring[h.idx]
		ck(checkPkt(first, int64(1000+cDirectN-cRingN), "d"), "oldest retained direct packet corrupted after GC")
	}
	runtime.KeepAlive(h)
	finish("l18_old_chan_composite_ring")
}
