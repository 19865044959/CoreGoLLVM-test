// L07: atomic.Pointer 无锁链表：Treiber 栈式 push/pop，节点只经 atomic 头可达，
// 显式 GC 后遍历；读者 goroutine 与主线程并发遍历/弹出，节点 payload 始终有效。
package main

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"sync"
	"sync/atomic"
	"unsafe"
)

type lnode struct {
	seq  int64
	tag  string
	data []byte
	next *lnode
}

var (
	failed int32
	gcSink [][]byte
	seed   int64
	head   atomic.Pointer[lnode]
)

func ck(ok bool, msg string) {
	if !ok {
		atomic.AddInt32(&failed, 1)
		fmt.Printf("FAIL: %s (seed=%d)\n", msg, seed)
	}
}

func gcStress() {
	for r := 0; r < 2; r++ {
		for i := 0; i < 8; i++ {
			b := make([]byte, 8192)
			b[0] = byte(i)
			b[len(b)-1] = byte(255 - i)
			gcSink = append(gcSink[:0], b)
		}
		gcSink = gcSink[:0]
		runtime.GC()
	}
}

func finish(name string) {
	if n := atomic.LoadInt32(&failed); n != 0 {
		fmt.Printf("FAILED %s (%d failed checks)\n", name, n)
		os.Exit(1)
	}
	fmt.Println("ALL PASS " + name)
}

func newNode(seq int64, s int64) *lnode {
	return &lnode{
		seq:  seq,
		tag:  "n-" + strconv.FormatInt(seq, 10),
		data: []byte("d-" + strconv.FormatInt(seq*7+s, 10)),
	}
}

func checkNode(p *lnode, seq int64, s int64, stage string) {
	if p == nil {
		ck(false, stage+": nil node seq="+strconv.FormatInt(seq, 10))
		return
	}
	ck(p.seq == seq, stage+": seq")
	ck(p.tag == "n-"+strconv.FormatInt(seq, 10), stage+": tag seq="+strconv.FormatInt(seq, 10))
	ck(string(p.data) == "d-"+strconv.FormatInt(seq*7+s, 10), stage+": data seq="+strconv.FormatInt(seq, 10))
}

// push 以 CAS 发布节点，next 在发布前设置，节点字段此后不可变。
func push(n *lnode) {
	for {
		old := head.Load()
		n.next = old
		if head.CompareAndSwap(old, n) {
			return
		}
	}
}

func pop() *lnode {
	for {
		old := head.Load()
		if old == nil {
			return nil
		}
		if head.CompareAndSwap(old, old.next) {
			return old
		}
	}
}

func traverse(check func(*lnode) bool) int {
	p := head.Load()
	count := 0
	for p != nil {
		if !check(p) {
			return -count - 1
		}
		count++
		p = p.next
	}
	return count
}

// round 完成一轮：入栈 n、GC 校验、弹出一部分、再入栈、GC、全部弹出。
//
//go:noinline
func round(r int, s int64) {
	n := 8 + int(s%5) + r%3
	for i := 0; i < n; i++ {
		push(newNode(int64(i), s))
	}
	ck(head.Load() != nil, "head non-nil after push")

	// 节点此时只经 atomic 头可达。
	gcStress()
	got := traverse(func(p *lnode) bool {
		if p.seq < 0 || p.seq >= int64(n) || p.tag != "n-"+strconv.FormatInt(p.seq, 10) ||
			string(p.data) != "d-"+strconv.FormatInt(p.seq*7+s, 10) {
			ck(false, "traverse payload mismatch")
			return false
		}
		return true
	})
	ck(got == n, "traverse count after push")

	k := n / 3
	popped := make([]*lnode, 0, k)
	for i := 0; i < k; i++ {
		popped = append(popped, pop())
	}
	ck(len(popped) == k && popped[0] != nil, "pop count")
	gcStress()
	for i, p := range popped {
		if i == 0 {
			checkNode(p, int64(n-1), s, "popped head")
		} else {
			ck(p != nil && p.next != nil, "popped chain link i="+strconv.Itoa(i))
		}
	}
	got = traverse(func(p *lnode) bool { return p.seq < int64(n) })
	ck(got == n-k, "traverse count after pop")

	m := 5
	for i := 0; i < m; i++ {
		push(newNode(int64(100+i), s))
	}
	gcStress()
	got = traverse(func(p *lnode) bool {
		return p.tag == "n-"+strconv.FormatInt(p.seq, 10)
	})
	ck(got == n-k+m, "traverse count after repush")

	var drained []*lnode
	for {
		p := pop()
		if p == nil {
			break
		}
		drained = append(drained, p)
	}
	ck(len(drained) == n-k+m, "drain count")
	ck(head.Load() == nil, "head nil after drain")
	gcStress()
	for _, p := range drained {
		ck(p.tag == "n-"+strconv.FormatInt(p.seq, 10), "drained tag")
		ck(string(p.data) == "d-"+strconv.FormatInt(p.seq*7+s, 10), "drained data")
	}
	for _, p := range popped {
		if p != nil {
			ck(p.tag == "n-"+strconv.FormatInt(p.seq, 10), "popped tag after drain GC")
		}
	}
	runtime.KeepAlive(drained)
	runtime.KeepAlive(popped)
}

// concurrent 阶段：读者并发遍历，主线程持续 push/pop 并触发 GC。
func concurrent(s int64) {
	const total = 256
	var traversals atomic.Int64
	started := make(chan struct{})
	done := make(chan struct{})
	errCh := make(chan string, 1)
	var wg sync.WaitGroup

	wg.Add(1)
	go func() {
		defer wg.Done()
		close(started)
		for {
			select {
			case <-done:
				return
			default:
			}
			p := head.Load()
			for p != nil {
				if p.tag != "n-"+strconv.FormatInt(p.seq, 10) ||
					string(p.data) != "d-"+strconv.FormatInt(p.seq*7+s, 10) {
					select {
					case errCh <- "concurrent traverse mismatch seq=" + strconv.FormatInt(p.seq, 10):
					default:
					}
					return
				}
				traversals.Add(1)
				p = p.next
			}
			runtime.Gosched()
		}
	}()

	<-started
	for i := 0; i < total; i++ {
		push(newNode(int64(1000+i), s))
		if i%64 == 0 {
			gcStress()
		}
	}
	// 等读者至少完成一次遍历，确保并发路径真的执行过。
	for i := 0; i < 100000 && traversals.Load() == 0; i++ {
		runtime.Gosched()
	}
	ck(traversals.Load() > 0, "concurrent reader traversed at least once")
	gcStress()

	var drained []*lnode
	for {
		p := pop()
		if p == nil {
			break
		}
		drained = append(drained, p)
	}
	close(done)
	wg.Wait()
	select {
	case msg := <-errCh:
		ck(false, msg)
	default:
	}
	ck(len(drained) == total, "concurrent drain count")
	ck(head.Load() == nil, "concurrent head nil")
	gcStress()
	for _, p := range drained {
		if p.tag != "n-"+strconv.FormatInt(p.seq, 10) ||
			string(p.data) != "d-"+strconv.FormatInt(p.seq*7+s, 10) {
			ck(false, "concurrent drained payload")
			break
		}
	}
	runtime.KeepAlive(drained)
}

func main() {
	if s := os.Getenv("UAF_SEED"); s != "" {
		if v, err := strconv.ParseInt(s, 10, 64); err == nil {
			seed = v
		}
	}
	if seed < 0 {
		seed = -seed
	}
	rounds := 32
	if s := os.Getenv("TORTURE_ROUNDS"); s != "" {
		if v, err := strconv.Atoi(s); err == nil && v > 0 {
			rounds = v
		}
	}

	for r := 0; r < rounds; r++ {
		round(r, seed+int64(r))
		ck(head.Load() == nil, "head empty between rounds r="+strconv.Itoa(r))
	}
	concurrent(seed)
	ck(unsafe.Sizeof(head) == unsafe.Sizeof(unsafe.Pointer(nil)), "atomic pointer slot width")

	finish("l07_atomic_list_stack")
}
