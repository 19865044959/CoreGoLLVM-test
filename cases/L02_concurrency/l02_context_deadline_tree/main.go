// L02: context deadline 树：显式构建 root/mid/leaf 两级树，验证取消向子树传播、
// 兄弟分支不受影响、已到期叶子报 DeadlineExceeded，Deadline() 取值稳定。
package main

import (
	"context"
	"fmt"
	"os"
	"runtime"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

var (
	failed  int32
	gcMu    sync.Mutex
	gcSink  [][]byte
	curSeed int64
)

type token struct {
	id   int
	tag  int64
	name string
}

type waitSpec struct {
	name string
	ctx  context.Context
	done chan error
	tok  *token
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

func (t *token) valid() bool {
	return t != nil && t.name == "tok" && t.tag == tagFor(curSeed, t.id)
}

func waiter(s waitSpec, started chan<- struct{}, wg *sync.WaitGroup) {
	defer wg.Done()
	started <- struct{}{}
	<-s.ctx.Done()
	gcStress()
	if !s.tok.valid() {
		ckSeed(false, s.name+" token corrupted after GC")
	}
	s.done <- s.ctx.Err()
}

func main() {
	curSeed = envSeed()

	root, cancelRoot := context.WithCancel(context.Background())
	sib, cancelSib := context.WithCancel(root)
	sibChild, cancelSibChild := context.WithCancel(sib)
	far := time.Unix(253402300799, 0)
	later := time.Unix(253402300799+1000, 0)
	mid, cancelMid := context.WithDeadline(root, far)
	leaf1, cancelLeaf1 := context.WithCancel(mid)
	leaf2, cancelLeaf2 := context.WithDeadline(mid, later)
	expired, cancelExpired := context.WithDeadline(mid, time.Unix(0, 0))
	defer func() {
		cancelExpired()
		cancelLeaf1()
		cancelLeaf2()
		cancelMid()
		cancelSibChild()
		cancelSib()
		cancelRoot()
	}()

	if expired.Err() != context.DeadlineExceeded {
		ck(false, "already-expired leaf did not report DeadlineExceeded")
	}
	select {
	case <-expired.Done():
	default:
		ck(false, "already-expired leaf Done is not closed")
	}
	if d, ok := leaf1.Deadline(); !ok || !d.Equal(far) {
		ck(false, "leaf1 did not inherit mid deadline")
	}
	if d, ok := leaf2.Deadline(); !ok || !d.Equal(far) {
		ck(false, "leaf2 did not inherit the earlier ancestor deadline")
	}
	if _, ok := sib.Deadline(); ok {
		ck(false, "sibling unexpectedly has a deadline")
	}

	specs := []waitSpec{
		{name: "root", ctx: root, done: make(chan error, 1), tok: &token{id: 0, tag: tagFor(curSeed, 0), name: "tok"}},
		{name: "sib", ctx: sib, done: make(chan error, 1), tok: &token{id: 1, tag: tagFor(curSeed, 1), name: "tok"}},
		{name: "sibChild", ctx: sibChild, done: make(chan error, 1), tok: &token{id: 2, tag: tagFor(curSeed, 2), name: "tok"}},
		{name: "mid", ctx: mid, done: make(chan error, 1), tok: &token{id: 3, tag: tagFor(curSeed, 3), name: "tok"}},
		{name: "leaf1", ctx: leaf1, done: make(chan error, 1), tok: &token{id: 4, tag: tagFor(curSeed, 4), name: "tok"}},
		{name: "leaf2", ctx: leaf2, done: make(chan error, 1), tok: &token{id: 5, tag: tagFor(curSeed, 5), name: "tok"}},
		{name: "expired", ctx: expired, done: make(chan error, 1), tok: &token{id: 6, tag: tagFor(curSeed, 6), name: "tok"}},
	}
	started := make(chan struct{}, len(specs))
	var wg sync.WaitGroup
	for _, s := range specs {
		wg.Add(1)
		go waiter(s, started, &wg)
	}
	for range specs {
		<-started
	}
	gcStress()
	gcStress()

	if err := <-specs[6].done; err != context.DeadlineExceeded {
		ck(false, fmt.Sprintf("expired leaf err got=%v want=DeadlineExceeded", err))
	}

	cancelMid()
	for _, idx := range []int{3, 4, 5} {
		if err := <-specs[idx].done; err != context.Canceled {
			ck(false, fmt.Sprintf("%s err got=%v want=Canceled", specs[idx].name, err))
		}
	}
	if mid.Err() != context.Canceled {
		ck(false, "mid did not report Canceled")
	}
	for _, idx := range []int{0, 1, 2} {
		select {
		case <-specs[idx].ctx.Done():
			ck(false, specs[idx].name+" was canceled by mid")
		default:
		}
		if specs[idx].ctx.Err() != nil {
			ck(false, specs[idx].name+" Err is non-nil before root cancel")
		}
	}

	late, cancelLate := context.WithCancel(mid)
	if late.Err() != context.Canceled {
		ck(false, "late child of canceled mid did not inherit Canceled")
	}
	select {
	case <-late.Done():
	default:
		ck(false, "late child of canceled mid Done is not closed")
	}
	cancelLate()

	cancelRoot()
	for _, idx := range []int{0, 1, 2} {
		if err := <-specs[idx].done; err != context.Canceled {
			ck(false, fmt.Sprintf("%s err got=%v want=Canceled", specs[idx].name, err))
		}
	}
	wg.Wait()
	gcStress()
	for i := range specs {
		if !specs[i].tok.valid() {
			ckSeed(false, specs[i].name+" token invalid after final GC")
		}
	}

	fmt.Printf("nodes=%d mid_subtree=%d root_subtree=%d expired=%d\n",
		len(specs), 4, 3, 1)
	finish("l02_context_deadline_tree")
}
