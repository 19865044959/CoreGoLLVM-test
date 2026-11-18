// L04: context 树取消 + CancelCause + AfterFunc。2 层 7 节点树，先以自定义
// cause 取消中间子树（兄弟/根不受影响，cause 只传播到子树），再取消 root；
// 每个节点的 AfterFunc 恰好执行一次，预先 Stop 的回调不得执行；GC 落在所有
// goroutine 阻塞于 ctx.Done 时。
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"runtime"
	"strconv"
	"sync"
	"sync/atomic"
)

type ctNode struct {
	name   string
	ctx    context.Context
	head   *ctLink
	exited chan ctExit
}

type ctLink struct {
	v    int64
	next *ctLink
}

type ctExit struct {
	name  string
	cause error
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

func ckf(ok bool, format string, args ...any) {
	if !ok {
		atomic.AddInt32(&failed, 1)
		fmt.Printf("FAIL: "+format+"\n", args...)
	}
}

func gcStress() {
	for i := 0; i < 8; i++ {
		b := make([]byte, 4096)
		b[0] = byte(i)
		gcSink = append(gcSink[:0], b)
	}
	gcSink = gcSink[:0]
	runtime.GC()
	runtime.GC()
}

func finish(name string, seed int64) {
	if n := atomic.LoadInt32(&failed); n != 0 {
		fmt.Printf("FAILED %s (UAF_SEED=%d, %d failed checks)\n", name, seed, n)
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

func main() {
	seed := envSeed()
	errMid := errors.New("mid-subtree canceled")
	errRoot := errors.New("root canceled")
	errLate := errors.New("late duplicate cancel")

	root, cancelRoot := context.WithCancelCause(context.Background())
	defer cancelRoot(nil)
	c0, cancelC0 := context.WithCancelCause(root)
	c1, cancelC1 := context.WithCancelCause(root)
	defer cancelC1(nil)
	c0g0, cancelC0g0 := context.WithCancelCause(c0)
	c0g1, cancelC0g1 := context.WithCancelCause(c0)
	c1g0, cancelC1g0 := context.WithCancelCause(c1)
	c1g1, cancelC1g1 := context.WithCancelCause(c1)
	defer cancelC0g0(nil)
	defer cancelC0g1(nil)
	defer cancelC1g0(nil)
	defer cancelC1g1(nil)

	nodes := []*ctNode{
		{name: "root", ctx: root, head: &ctLink{v: seed*100003 + 1, next: &ctLink{v: seed*100003 + 2}}},
		{name: "c0", ctx: c0, head: &ctLink{v: seed*100003 + 10, next: &ctLink{v: seed*100003 + 11}}},
		{name: "c1", ctx: c1, head: &ctLink{v: seed*100003 + 20, next: &ctLink{v: seed*100003 + 21}}},
		{name: "c0g0", ctx: c0g0, head: &ctLink{v: seed*100003 + 30, next: &ctLink{v: seed*100003 + 31}}},
		{name: "c0g1", ctx: c0g1, head: &ctLink{v: seed*100003 + 40, next: &ctLink{v: seed*100003 + 41}}},
		{name: "c1g0", ctx: c1g0, head: &ctLink{v: seed*100003 + 50, next: &ctLink{v: seed*100003 + 51}}},
		{name: "c1g1", ctx: c1g1, head: &ctLink{v: seed*100003 + 60, next: &ctLink{v: seed*100003 + 61}}},
	}
	exited := make(chan ctExit, len(nodes))
	for _, n := range nodes {
		n.exited = exited
	}
	ready := make(chan string, len(nodes))

	var cbMu sync.Mutex
	cbCount := make(map[string]int, len(nodes))
	cbCh := make(chan string, len(nodes)+1)
	var stoppedCalls int32
	register := func(n *ctNode) {
		context.AfterFunc(n.ctx, func() {
			cbMu.Lock()
			cbCount[n.name]++
			cbMu.Unlock()
			cbCh <- n.name
		})
	}
	for _, n := range nodes {
		register(n)
	}
	// 额外注册一个在取消前 Stop 的回调：必须永不执行。
	stopFn := context.AfterFunc(c0g1, func() {
		atomic.AddInt32(&stoppedCalls, 1)
		cbCh <- "stopped"
	})
	if !stopFn() {
		ck(false, "pre-cancel AfterFunc Stop returned false")
	}

	var wg sync.WaitGroup
	wg.Add(len(nodes))
	for _, n := range nodes {
		go func(n *ctNode) {
			defer wg.Done()
			ready <- n.name
			<-n.ctx.Done()
			if n.head == nil || n.head.next == nil || n.head.next.v != n.head.v+1 {
				ckf(false, "node %s chain corrupted on cancel", n.name)
			}
			n.exited <- ctExit{name: n.name, cause: context.Cause(n.ctx)}
		}(n)
	}
	for range nodes {
		<-ready
	}
	gcStress()

	// 中间子树取消：重复 cancel 三次，第一个 cause 必须获胜。
	cancelC0(errMid)
	cancelC0(errLate)
	cancelC0(errLate)
	first := make(map[string]error, 3)
	for i := 0; i < 3; i++ {
		e := <-exited
		first[e.name] = e.cause
	}
	ckf(len(first) == 3, "mid cancel produced %d exits want=3", len(first))
	for _, name := range []string{"c0", "c0g0", "c0g1"} {
		c, ok := first[name]
		ckf(ok, "mid subtree node %s did not exit", name)
		ckf(c == errMid, "node %s cause=%v want=errMid", name, c)
	}
	ck(root.Err() == nil && context.Cause(root) == nil, "root canceled by mid-subtree cancel")
	ck(c1.Err() == nil && c1g0.Err() == nil && c1g1.Err() == nil, "sibling subtree canceled by mid cancel")
	select {
	case <-exited:
		ck(false, "sibling subtree exited on mid cancel")
	default:
	}
	midCB := 3
	for i := 0; i < midCB; i++ {
		name := <-cbCh
		ckf(name != "stopped", "stopped AfterFunc executed on mid cancel")
	}
	cbMu.Lock()
	ckf(cbCount["c0"] == 1 && cbCount["c0g0"] == 1 && cbCount["c0g1"] == 1,
		"mid subtree AfterFunc count != 1")
	ckf(cbCount["root"] == 0 && cbCount["c1"] == 0, "uncanceled node AfterFunc ran")
	cbMu.Unlock()

	gcStress()

	// root 取消：剩余 4 节点退出，cause 为 errRoot；已取消子树保持 errMid。
	cancelRoot(errRoot)
	cancelRoot(errLate)
	rest := make(map[string]error, len(nodes))
	for i := 0; i < 4; i++ {
		e := <-exited
		rest[e.name] = e.cause
	}
	ckf(len(rest) == 4, "root cancel produced %d exits want=4", len(rest))
	for _, name := range []string{"root", "c1", "c1g0", "c1g1"} {
		c, ok := rest[name]
		ckf(ok, "root-branch node %s did not exit", name)
		ckf(c == errRoot, "node %s cause=%v want=errRoot", name, c)
	}
	for i := 0; i < 4; i++ {
		name := <-cbCh
		ckf(name != "stopped", "stopped AfterFunc executed on root cancel")
	}
	wg.Wait()
	select {
	case e := <-exited:
		ckf(false, "duplicate exit for node %s", e.name)
	default:
	}
	ck(context.Cause(c0) == errMid && context.Cause(c0g0) == errMid, "mid subtree cause overwritten by root cancel")
	ck(context.Cause(c1) == errRoot && context.Cause(c1g0) == errRoot, "root branch cause not errRoot")
	ckf(atomic.LoadInt32(&stoppedCalls) == 0, "stopped AfterFunc ran %d times", atomic.LoadInt32(&stoppedCalls))
	cbMu.Lock()
	ckf(cbCount["root"] == 1 && cbCount["c1"] == 1 && cbCount["c1g0"] == 1 && cbCount["c1g1"] == 1,
		"root branch AfterFunc count != 1")
	ckf(cbCount["c0"] == 1 && cbCount["c0g1"] == 1, "mid subtree AfterFunc ran more than once")
	cbMu.Unlock()

	gcStress()
	for _, n := range nodes {
		ok := n.head != nil && n.head.next != nil && n.head.next.v == n.head.v+1
		ckf(ok, "node %s retained chain corrupted after final GC", n.name)
	}
	finish("l04_context_cancel_cause_tree", seed)
}
