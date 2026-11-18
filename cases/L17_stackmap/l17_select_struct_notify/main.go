// L17: select + chan struct{} 通知。tick（缓冲 chan struct{}）、closed 广播、
// nil 禁用与 default 四条路径都在 case body 内注册 defer；defer 跨 GC 校验本轮指针。
package main

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"sync/atomic"
)

type item struct {
	v    int64
	next *item
}

var (
	failed  int32
	uafSeed int64 = 1
)

func ck(ok bool, msg string) {
	if !ok {
		atomic.AddInt32(&failed, 1)
		fmt.Println("FAIL:", msg)
	}
}

//go:noinline
func gcStress() {
	var sink [][]byte
	for i := 0; i < 8; i++ {
		b := make([]byte, 2048)
		b[0] = byte(i)
		sink = append(sink, b)
	}
	runtime.GC()
	runtime.GC()
	runtime.KeepAlive(sink)
}

func seedFromEnv() int64 {
	if s := os.Getenv("UAF_SEED"); s != "" {
		if v, err := strconv.ParseInt(s, 10, 64); err == nil && v != 0 {
			return v
		}
	}
	return 1
}

func envRounds(def, max int) int {
	if s := os.Getenv("TORTURE_ROUNDS"); s != "" {
		if v, err := strconv.Atoi(s); err == nil && v > 0 {
			if v > max {
				return max
			}
			return v
		}
	}
	return def
}

func finish(name string) {
	if n := atomic.LoadInt32(&failed); n != 0 {
		fmt.Printf("FAILED %s (%d failed checks) seed=%d\n", name, n, uafSeed)
		os.Exit(1)
	}
	fmt.Println("ALL PASS " + name)
}

func makeChain(base int64, n int) *item {
	var head *item
	for i := 0; i < n; i++ {
		head = &item{v: base + int64(i) + 1, next: head}
	}
	return head
}

func checkChain(p *item, base int64, n int, where string) bool {
	if p == nil {
		ck(false, fmt.Sprintf("%s: chain nil seed=%d", where, uafSeed))
		return false
	}
	idx := n
	for q := p; q != nil; q = q.next {
		if q.v != base+int64(idx) {
			ck(false, fmt.Sprintf("%s: node %d v=%d want=%d seed=%d", where, idx, q.v, base+int64(idx), uafSeed))
			return false
		}
		idx--
	}
	if idx != 0 {
		ck(false, fmt.Sprintf("%s: chain length mismatch idx=%d seed=%d", where, idx, uafSeed))
		return false
	}
	return true
}

// runSelect 的 kind 与就绪路径一一对应：
//
//	0: tick 有 token       -> case <-c0
//	1: closedChan 已关闭   -> case <-c1
//	2: c0/c1 均为 nil      -> default
//	3: sent 缓冲为空       -> case send <- struct{}{}
//
//go:noinline
func runSelect(kind int, rnd int, base int64, p *item, tick, closedChan <-chan struct{}, sent chan<- struct{}, order *[]string, hits *[4]int64) (hit int) {
	hit = -1
	var c0 <-chan struct{}
	var c1 <-chan struct{}
	var send chan<- struct{}
	switch kind {
	case 0:
		c0 = tick
	case 1:
		c1 = closedChan
	case 2:
		// 两个 nil channel 都被禁用。
	case 3:
		send = sent
	}

	// 函数级 defer 先注册；case body 内的 defer 后注册、先运行。
	defer func() {
		if hit < 0 || hit > 3 {
			ck(false, fmt.Sprintf("round %d kind %d: no select path taken seed=%d", rnd, kind, uafSeed))
			return
		}
		*order = append(*order, fmt.Sprintf("func:%d", hit))
		gcStress()
		if !checkChain(p, base, 5, fmt.Sprintf("round %d func defer", rnd)) {
			// checkChain 已打印。
		}
		hits[hit]++
	}()

	select {
	case <-c0:
		hit = 0
		defer func() {
			*order = append(*order, fmt.Sprintf("case:0:%d", rnd))
			gcStress()
			checkChain(p, base, 5, fmt.Sprintf("round %d case0 defer", rnd))
		}()
	case <-c1:
		hit = 1
		defer func() {
			*order = append(*order, fmt.Sprintf("case:1:%d", rnd))
			gcStress()
			checkChain(p, base, 5, fmt.Sprintf("round %d case1 defer", rnd))
		}()
	case send <- struct{}{}:
		hit = 3
		defer func() {
			*order = append(*order, fmt.Sprintf("case:3:%d", rnd))
			gcStress()
			checkChain(p, base, 5, fmt.Sprintf("round %d case3 defer", rnd))
		}()
	default:
		hit = 2
		defer func() {
			*order = append(*order, fmt.Sprintf("case:2:%d", rnd))
			gcStress()
			checkChain(p, base, 5, fmt.Sprintf("round %d default defer", rnd))
		}()
	}
	return hit
}

func main() {
	uafSeed = seedFromEnv()
	rounds := envRounds(64, 4096)
	var hits [4]int64
	var expected [4]int64

	for r := 0; r < rounds; r++ {
		kind := r % 4
		base := int64(r*10) + uafSeed%31
		p := makeChain(base, 5)

		tick := make(chan struct{}, 1)
		closedChan := make(chan struct{})
		sent := make(chan struct{}, 1)
		switch kind {
		case 0:
			tick <- struct{}{}
		case 1:
			close(closedChan)
		}

		order := make([]string, 0, 2)
		hit := runSelect(kind, r, base, p, tick, closedChan, sent, &order, &hits)
		gcStress()
		checkChain(p, base, 5, fmt.Sprintf("round %d caller", r))
		if hit != kind {
			ck(false, fmt.Sprintf("round %d kind %d: select took path %d seed=%d", r, kind, hit, uafSeed))
		}
		expected[kind]++
		want := [2]string{fmt.Sprintf("case:%d:%d", kind, r), fmt.Sprintf("func:%d", kind)}
		if len(order) != 2 || order[0] != want[0] || order[1] != want[1] {
			ck(false, fmt.Sprintf("round %d kind %d: defer order=%v want=%v seed=%d", r, kind, order, want, uafSeed))
		}
		if kind == 3 {
			select {
			case <-sent:
			default:
				ck(false, fmt.Sprintf("round %d: send case token missing seed=%d", r, uafSeed))
			}
		}
		if kind == 0 {
			select {
			case <-tick:
				ck(false, fmt.Sprintf("round %d: tick not consumed seed=%d", r, uafSeed))
			default:
			}
		}
	}

	for k := 0; k < 4; k++ {
		if hits[k] != expected[k] {
			ck(false, fmt.Sprintf("path counters: hits[%d]=%d want=%d seed=%d", k, hits[k], expected[k], uafSeed))
		}
	}
	if rounds >= 4 && (hits[0] == 0 || hits[1] == 0 || hits[2] == 0 || hits[3] == 0) {
		ck(false, fmt.Sprintf("not all select paths exercised seed=%d", uafSeed))
	}

	finish("l17_select_struct_notify")
}
