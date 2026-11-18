// L19: select + default 非阻塞轮询。3 个预填充 channel 用固定 48 轮
// default 轮询消费，空 channel 必须走 default 分支；回合中途显式 GC 并
// 校验已消费消息的指针链。另测全空 channel 的 default 计数与就绪分支优先。
package main

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"sync/atomic"
)

type node struct {
	v    int64
	next *node
}

type msg struct {
	ch    int
	id    int
	v     int64
	chain *node
}

var failed int32

func ck(ok bool, msg string) {
	if !ok {
		atomic.AddInt32(&failed, 1)
		fmt.Println("FAIL:", msg)
	}
}

func ckf(ok bool, format string, args ...any) {
	if !ok {
		atomic.AddInt32(&failed, 1)
		fmt.Printf("FAIL: "+format+"\n", args...)
	}
}

func gcStress() {
	bufs := make([][]byte, 0, 16)
	for i := 0; i < 16; i++ {
		b := make([]byte, 4096)
		b[len(b)-1] = byte(i)
		bufs = append(bufs, b)
	}
	runtime.GC()
	runtime.GC()
	runtime.KeepAlive(bufs)
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

func makeChain(base int64) *node {
	return &node{v: base, next: &node{v: base + 1, next: &node{v: base + 2}}}
}

func chainOK(head *node, base int64) bool {
	if head == nil || head.v != base || head.next == nil || head.next.v != base+1 {
		return false
	}
	return head.next.next != nil && head.next.next.v == base+2 && head.next.next.next == nil
}

func fill(ch chan<- *msg, which, n int, tag int64) {
	for i := 0; i < n; i++ {
		v := tag + int64(i)
		ch <- &msg{ch: which, id: i, v: v, chain: makeChain(v)}
	}
}

func main() {
	seed := envSeed()
	const n0, n1, n2 = 48, 16, 8
	const total = n0 + n1 + n2
	var bad int32

	c0 := make(chan *msg, n0)
	c1 := make(chan *msg, n1)
	c2 := make(chan *msg, n2)
	fill(c0, 0, n0, seed*1000+1)
	fill(c1, 1, n1, seed*1000+100)
	fill(c2, 2, n2, seed*1000+200)

	kept := make([]*msg, 0, total)
	got, defaults := 0, 0

	// 固定 48 轮，每轮对 3 个 channel 各做一次 select/default。
	for round := 0; round < n0; round++ {
		for _, ch := range []chan *msg{c0, c1, c2} {
			select {
			case m := <-ch:
				got++
				kept = append(kept, m)
			default:
				defaults++
			}
		}
		if round%12 == 11 {
			gcStress()
			for _, m := range kept {
				if !chainOK(m.chain, m.v) {
					atomic.StoreInt32(&bad, 1)
				}
			}
		}
	}

	// c0/c1/c2 分别在 48/16/8 轮内有无数据：default 次数 = 0 + 32 + 40。
	ckf(got == total, "got=%d want=%d (seed=%d)", got, total, seed)
	ckf(defaults == 72, "defaults=%d want=72 (seed=%d)", defaults, seed)
	ckf(len(c0) == 0 && len(c1) == 0 && len(c2) == 0, "channels not drained (seed=%d)", seed)

	var count [3]int
	var last [3]int
	for i := range last {
		last[i] = -1
	}
	for _, m := range kept {
		if m.id != last[m.ch]+1 {
			atomic.StoreInt32(&bad, 1)
		}
		last[m.ch] = m.id
		count[m.ch]++
	}
	ckf(count[0] == n0 && count[1] == n1 && count[2] == n2,
		"per-channel counts=%v want=[%d %d %d] (seed=%d)", count, n0, n1, n2, seed)

	for _, ch := range []chan *msg{c0, c1, c2} {
		close(ch)
		for range ch {
			ck(false, "extra item after fixed polling rounds")
		}
	}

	// 全空 channel 的 select/default：固定 64 次全部命中 default。
	e0 := make(chan *msg)
	e1 := make(chan *msg)
	e2 := make(chan *msg)
	emptyDefaults := 0
	for i := 0; i < 64; i++ {
		select {
		case <-e0:
			ck(false, "empty select chose e0")
		case <-e1:
			ck(false, "empty select chose e1")
		case <-e2:
			ck(false, "empty select chose e2")
		default:
			emptyDefaults++
		}
		if i%16 == 15 {
			gcStress()
		}
	}
	ckf(emptyDefaults == 64, "empty defaults=%d want=64 (seed=%d)", emptyDefaults, seed)

	// 就绪 channel 必须优先于 default。
	r0 := make(chan *msg)
	r1 := make(chan *msg, 1)
	r2 := make(chan *msg)
	rv := seed*1000 + 999
	r1 <- &msg{ch: 1, id: 0, v: rv, chain: makeChain(rv)}
	select {
	case m := <-r0:
		ckf(false, "ready select chose empty r0: %+v", m)
	case m := <-r1:
		ck(m.v == rv && chainOK(m.chain, rv), "ready payload corrupted")
	case <-r2:
		ck(false, "ready select chose empty r2")
	default:
		ck(false, "ready select chose default instead of ready channel")
	}
	gcStress()
	ckf(atomic.LoadInt32(&bad) == 0, "payload corrupted during polling (seed=%d)", seed)

	fmt.Printf("processed=%d defaults=%d empty_defaults=%d\n", got, defaults, emptyDefaults)
	finish("l19_select_default_poll")
}
