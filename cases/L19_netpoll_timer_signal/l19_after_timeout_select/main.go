// L19: select + time.After 的两条路径——超时路径（请求永不就绪）与就绪路径
// （请求在 GC 后到达）。payload 指针跨 GC 校验，超时判定只依赖 select 分支
// 是否命中，不做任何耗时阈值断言。
package main

import (
	"fmt"
	"os"
	"runtime"
	"sync/atomic"
	"time"
)

type node struct {
	v    int64
	next *node
}

type payload struct {
	id   int
	head *node
	name string
}

var failed int32

const (
	rounds    = 32
	waitLimit = 5 * time.Second
)

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

func newPayload(id int) *payload {
	base := int64(id)*29 + 7
	return &payload{
		id:   id,
		head: &node{v: base, next: &node{v: base + 1, next: &node{v: base + 2}}},
		name: fmt.Sprintf("p-%02d", id),
	}
}

func payloadOK(p *payload, id int) bool {
	if p == nil || p.id != id || p.name != fmt.Sprintf("p-%02d", id) {
		return false
	}
	base := int64(id)*29 + 7
	h := p.head
	if h == nil || h.v != base || h.next == nil || h.next.v != base+1 {
		return false
	}
	return h.next.next != nil && h.next.next.v == base+2 && h.next.next.next == nil
}

func main() {
	// 1) 超时路径：work 永不就绪，select 必须命中 time.After 分支。
	work := make(chan *payload) // 无发送者
	timeouts := 0
	for i := 0; i < rounds; i++ {
		p := newPayload(i)
		gcStress()
		select {
		case got := <-work:
			ck(false, "timeout path: unexpected work item")
			_ = got
		case <-time.After(time.Millisecond):
			timeouts++
			ckf(payloadOK(p, i), "timeout path: payload %d corrupted after GC", i)
		}
	}
	ckf(timeouts == rounds, "timeout path count=%d want=%d", timeouts, rounds)

	// 2) 就绪路径：worker 在 select 中同时监听请求与 5s 超时；主协程先 GC，
	//    再投递携带指针的请求，select 必须走请求分支。
	p2 := newPayload(1000)
	req := make(chan *payload)
	res := make(chan *payload, 1)
	go func() {
		select {
		case got := <-req:
			res <- got
		case <-time.After(waitLimit):
			res <- nil
		}
	}()
	gcStress()
	go func() { req <- p2 }()
	var got *payload
	select {
	case got = <-res:
	case <-time.After(waitLimit):
		ck(false, "ready path: worker did not respond")
	}
	ckf(got != nil, "ready path: timeout branch chosen")
	ckf(payloadOK(got, 1000), "ready path: payload corrupted after GC")

	// 3) 同一 select 中的 dead channel 与已就绪 channel 竞争：只有后者就绪。
	p3 := newPayload(2000)
	ready := make(chan *payload, 1)
	ready <- p3
	runtime.KeepAlive(ready)
	gcStress()
	select {
	case got3 := <-work:
		ck(false, "dead channel became ready")
		_ = got3
	case got3 := <-ready:
		ckf(payloadOK(got3, 2000), "ready-vs-dead: payload corrupted")
	case <-time.After(time.Hour):
		ck(false, "ready-vs-dead: neither branch ready")
	}

	// 4) 超时分支后再 GC，确认超时路径中捕获的指针仍然有效。
	p4 := newPayload(3000)
	dead := make(chan *payload)
	var timedOut int32
	done := make(chan struct{})
	go func() {
		select {
		case <-dead:
			atomic.StoreInt32(&timedOut, 2)
		case <-time.After(time.Millisecond):
			atomic.StoreInt32(&timedOut, 1)
		}
		close(done)
	}()
	gcStress()
	select {
	case <-done:
	case <-time.After(waitLimit):
		ck(false, "goroutine select did not finish")
	}
	ckf(atomic.LoadInt32(&timedOut) == 1, "goroutine select took timeout branch=%d", atomic.LoadInt32(&timedOut))
	ckf(payloadOK(p4, 3000), "goroutine payload corrupted after GC")

	fmt.Printf("timeouts=%d ready=2\n", timeouts)
	finish("l19_after_timeout_select")
}
