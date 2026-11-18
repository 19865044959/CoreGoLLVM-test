// L18: 老 channel 中传递“年轻 channel”，年轻 channel 的缓冲再持有年轻
// packet 链。先把子 channel 送入已晋升的老缓冲 chan，跨 GC 后再取回；
// 第二阶段在老无缓冲 chan 上由 goroutine 交接子 channel，接收端穿插 GC。
package main

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"sync/atomic"
)

const (
	cfAPkts  = 4
	cfASubs  = 8
	cfBSubs  = 6
	cfBPkts  = 3
	cfDataN  = 32
	cfOutCap = 16
)

type cfPkt struct {
	seq  int64
	data [cfDataN]byte
	next *cfPkt
}

type cfHub struct {
	outer  chan chan *cfPkt
	outer2 chan chan *cfPkt
	sink   []*cfPkt
}

var (
	failed int32
	gcSink [][]byte
	cfH    cfHub
	cfSalt int64
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

func seedValue() int64 {
	s := os.Getenv("UAF_SEED")
	if s == "" {
		return 1
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 1
	}
	return n
}

func cfByte(seq, i int64) byte { return byte((int(seq)*41 + int(i)*17 + int(cfSalt)*7) % 251) }

//go:noinline
func cfMakePkt(seq int64) *cfPkt {
	p := &cfPkt{seq: seq}
	for i := range p.data {
		p.data[i] = cfByte(seq, int64(i))
	}
	return p
}

// cfMakeAndSend 创建年轻子 channel 并预装 packet 链，然后送入 dst。
//
//go:noinline
func cfMakeAndSend(dst chan chan *cfPkt, base int64, n int) {
	ch := make(chan *cfPkt, n)
	var prev *cfPkt
	for i := 0; i < n; i++ {
		p := cfMakePkt(base + int64(i))
		if prev != nil {
			prev.next = p
		}
		prev = p
		ch <- p
	}
	dst <- ch
}

//go:noinline
func cfRecvChan(src chan chan *cfPkt) chan *cfPkt { return <-src }

//go:noinline
func cfDrainChan(ch chan *cfPkt, out []*cfPkt) []*cfPkt {
	for {
		select {
		case p := <-ch:
			out = append(out, p)
		default:
			return out
		}
	}
}

func cfCheckPkts(pkts []*cfPkt, base int64, n int, label string) bool {
	if len(pkts) != n {
		ck(false, fmt.Sprintf("%s: got %d packets want %d", label, len(pkts), n))
		return false
	}
	for i, p := range pkts {
		want := base + int64(i)
		if p == nil || p.seq != want {
			ck(false, fmt.Sprintf("%s: packet %d seq mismatch", label, i))
			return false
		}
		for j := range p.data {
			if p.data[j] != cfByte(want, int64(j)) {
				ck(false, fmt.Sprintf("%s: packet seq=%d data corrupted at %d", label, want, j))
				return false
			}
		}
		if i+1 < n {
			if p.next != pkts[i+1] {
				ck(false, fmt.Sprintf("%s: packet seq=%d next link broken", label, want))
				return false
			}
		} else if p.next != nil {
			ck(false, fmt.Sprintf("%s: last packet seq=%d has next", label, want))
			return false
		}
	}
	return true
}

func main() {
	seed := seedValue()
	cfSalt = seed % 7
	cfH.outer = make(chan chan *cfPkt, cfOutCap)
	cfH.outer2 = make(chan chan *cfPkt)
	cfH.sink = make([]*cfPkt, 0, cfASubs*cfAPkts+cfBSubs*cfBPkts)
	gcStress()
	gcStress() // outer/outer2 及 hub 晋升为老对象

	// 阶段一：年轻子 channel 只被老缓冲 chan 引用。
	for i := 0; i < cfASubs; i++ {
		cfMakeAndSend(cfH.outer, int64(i*100), cfAPkts)
	}
	gcStress()
	if len(cfH.outer) != cfASubs {
		ck(false, fmt.Sprintf("old buffered chan len=%d want=%d (seed=%d)", len(cfH.outer), cfASubs, seed))
	}
	for i := 0; i < cfASubs; i++ {
		ch := cfRecvChan(cfH.outer)
		runtime.GC()
		before := len(cfH.sink)
		cfH.sink = cfDrainChan(ch, cfH.sink)
		if len(cfH.sink) != before+cfAPkts {
			ck(false, fmt.Sprintf("phase A subchan %d packets=%d want=%d (seed=%d)", i, len(cfH.sink)-before, cfAPkts, seed))
			break
		}
		if !cfCheckPkts(cfH.sink[before:], int64(i*100), cfAPkts, "phaseA") {
			break
		}
	}

	// 阶段二：老无缓冲 chan 上由 goroutine 交接年轻子 channel。
	outer2 := cfH.outer2
	go func() {
		for i := 0; i < cfBSubs; i++ {
			ch := make(chan *cfPkt, cfBPkts)
			var prev *cfPkt
			base := int64(2000 + i*100)
			for j := 0; j < cfBPkts; j++ {
				p := cfMakePkt(base + int64(j))
				if prev != nil {
					prev.next = p
				}
				prev = p
				ch <- p
			}
			runtime.GC()
			outer2 <- ch
		}
		close(outer2)
	}()

	i := 0
	for ch := range outer2 {
		runtime.GC()
		before := len(cfH.sink)
		cfH.sink = cfDrainChan(ch, cfH.sink)
		if len(cfH.sink) != before+cfBPkts {
			ck(false, fmt.Sprintf("phase B subchan %d packets=%d want=%d (seed=%d)", i, len(cfH.sink)-before, cfBPkts, seed))
			break
		}
		if !cfCheckPkts(cfH.sink[before:], int64(2000+i*100), cfBPkts, "phaseB") {
			break
		}
		i++
	}
	if i != cfBSubs {
		ck(false, fmt.Sprintf("phase B received %d subchans want %d (seed=%d)", i, cfBSubs, seed))
	}
	// 全部碰撞后 final GC，再复查最早与最新的 packet。
	gcStress()
	if len(cfH.sink) != cfASubs*cfAPkts+cfBSubs*cfBPkts {
		ck(false, fmt.Sprintf("sink total=%d want=%d (seed=%d)", len(cfH.sink), cfASubs*cfAPkts+cfBSubs*cfBPkts, seed))
	} else {
		cfCheckPkts(cfH.sink[:cfAPkts], 0, cfAPkts, "final-head")
		lastBase := int64(2000 + (cfBSubs-1)*100)
		cfCheckPkts(cfH.sink[len(cfH.sink)-cfBPkts:], lastBase, cfBPkts, "final-tail")
	}

	finish("l18_chan_of_chans_fanout")
}
