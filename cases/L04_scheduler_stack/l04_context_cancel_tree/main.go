// L04: context 取消传播：root -> 3 child -> 6 grandchild 的 ctx 树，每个节点一个
// goroutine 阻塞在 ctx.Done() 上。先取消中间子树：该子树全部退出、兄弟子树不受影响；
// 再取消 root：其余全部退出。取消前 GC，退出时校验各自捕获的指针链。
package main

import (
	"context"
	"fmt"
	"os"
	"runtime"
	"strconv"
	"sync"
	"sync/atomic"
)

type cxNode struct {
	v    int64
	next *cxNode
}

type cxWorker struct {
	name string
	ctx  context.Context
	done chan string
	head *cxNode
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

func main() {
	seed := envSeed()

	root, cancelRoot := context.WithCancel(context.Background())
	defer cancelRoot()
	children := make([]context.Context, 3)
	cancelChild := make([]context.CancelFunc, 3)
	for i := range children {
		children[i], cancelChild[i] = context.WithCancel(root)
	}
	grand := make([][]context.Context, 3)
	grandCancel := make([][]context.CancelFunc, 3)
	for i := range children {
		grand[i] = make([]context.Context, 2)
		grandCancel[i] = make([]context.CancelFunc, 2)
		for j := range grand[i] {
			grand[i][j], grandCancel[i][j] = context.WithCancel(children[i])
		}
	}

	var workers []*cxWorker
	add := func(name string, ctx context.Context, id int) {
		b := seed*100003 + int64(id)*211
		workers = append(workers, &cxWorker{
			name: name,
			ctx:  ctx,
			done: make(chan string, 1),
			head: &cxNode{v: b, next: &cxNode{v: b + 9}},
		})
	}
	add("root", root, 0)
	id := 1
	for i := 0; i < 3; i++ {
		add(fmt.Sprintf("child%d", i), children[i], id)
		id++
		for j := 0; j < 2; j++ {
			add(fmt.Sprintf("child%d-g%d", i, j), grand[i][j], id)
			id++
		}
	}

	ready := make(chan string, len(workers))
	exited := make(chan string, len(workers))
	var wg sync.WaitGroup
	wg.Add(len(workers))
	for _, w := range workers {
		go func(w *cxWorker) {
			defer wg.Done()
			ready <- w.name
			<-w.ctx.Done()
			if w.head == nil || w.head.next == nil || w.head.next.v != w.head.v+9 {
				atomic.AddInt32(&failed, 1)
				fmt.Printf("FAIL: %s chain corrupted on cancel\n", w.name)
			}
			exited <- w.name
		}(w)
	}
	for range workers {
		<-ready
	}
	// 所有 goroutine 阻塞在 ctx.Done 上时 GC。
	gcStress()

	cancelChild[1]()
	first := make(map[string]int)
	for i := 0; i < 3; i++ {
		name := <-exited
		first[name]++
	}
	ckf(first["child1"] == 1, "child1 exited %d times want=1", first["child1"])
	ckf(first["child1-g0"] == 1, "child1-g0 exited %d times want=1", first["child1-g0"])
	ckf(first["child1-g1"] == 1, "child1-g1 exited %d times want=1", first["child1-g1"])
	ckf(len(first) == 3, "canceled subtree produced %d exits want=3", len(first))
	ckf(root.Err() == nil, "root canceled by child cancel")
	ckf(children[0].Err() == nil, "child0 canceled by sibling cancel")
	ckf(children[1].Err() == context.Canceled, "child1 err=%v want=Canceled", children[1].Err())
	ckf(grand[1][0].Err() == context.Canceled, "grandchild did not inherit cancellation")
	ckf(grand[1][1].Err() == context.Canceled, "grandchild did not inherit cancellation")

	gcStress()
	cancelRoot()
	counts := make(map[string]int)
	for i := 0; i < len(workers)-3; i++ {
		counts[<-exited]++
	}
	counts["child1"]++
	counts["child1-g0"]++
	counts["child1-g1"]++
	wg.Wait()

	ckf(root.Err() == context.Canceled, "root err=%v want=Canceled", root.Err())
	for _, w := range workers {
		ckf(counts[w.name] == 1, "%s exit count=%d want=1", w.name, counts[w.name])
		if w.head == nil || w.head.next == nil || w.head.next.v != w.head.v+9 {
			ckf(false, "%s chain corrupted after cancel tree GC", w.name)
		}
	}

	gcStress()
	for _, w := range workers {
		ckf(w.head != nil && w.head.next != nil && w.head.next.v == w.head.v+9,
			"%s retained chain corrupted after final GC", w.name)
	}
	for i := range children {
		for j := range grand[i] {
			grandCancel[i][j]()
		}
		cancelChild[i]()
	}
	finish("l04_context_cancel_tree")
}
