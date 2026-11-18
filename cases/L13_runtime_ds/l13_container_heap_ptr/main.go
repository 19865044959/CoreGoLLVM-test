// L13: container/heap 指针元素：Push/Pop 全序、Fix/Remove、大小顶堆与分批 churn 的 GC 保活。
package main

import (
	"container/heap"
	"fmt"
	"math/rand"
	"os"
	"runtime"
	"sort"
	"strconv"
	"sync/atomic"
)

type task struct {
	prio int64
	seq  int
	name string
	salt int64
	pad  []byte
}

type taskHeap struct {
	items []*task
	max   bool
}

func lessTask(max bool, a, b *task) bool {
	if a.prio != b.prio {
		if max {
			return a.prio > b.prio
		}
		return a.prio < b.prio
	}
	return a.seq < b.seq
}

func (h *taskHeap) Len() int { return len(h.items) }
func (h *taskHeap) Less(i, j int) bool {
	return lessTask(h.max, h.items[i], h.items[j])
}
func (h *taskHeap) Swap(i, j int) { h.items[i], h.items[j] = h.items[j], h.items[i] }
func (h *taskHeap) Push(x any)    { h.items = append(h.items, x.(*task)) }
func (h *taskHeap) Pop() any {
	old := h.items
	n := len(old)
	it := old[n-1]
	old[n-1] = nil
	h.items = old[:n-1]
	return it
}

func (h *taskHeap) ok() bool {
	for i := 1; i < len(h.items); i++ {
		if h.Less(i, (i-1)/2) {
			return false
		}
	}
	return true
}

var (
	failed  int32
	gcSink  [][]byte
	curSeed int64
)

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

func newTask(prio int64, seq int, salt int64) *task {
	t := &task{prio: prio, seq: seq, name: fmt.Sprintf("t-%06d", seq), salt: salt, pad: make([]byte, 12)}
	for i := range t.pad {
		t.pad[i] = byte(seq*17 + i*3 + int(salt))
	}
	return t
}

func validTask(t *task, salt int64) bool {
	if t == nil || t.name != fmt.Sprintf("t-%06d", t.seq) || t.salt != salt || len(t.pad) != 12 {
		return false
	}
	for i := range t.pad {
		if t.pad[i] != byte(t.seq*17+i*3+int(salt)) {
			return false
		}
	}
	return true
}

func reference(items []*task, max bool) []*task {
	ref := append([]*task(nil), items...)
	sort.Slice(ref, func(i, j int) bool { return lessTask(max, ref[i], ref[j]) })
	return ref
}

func drain(h *taskHeap, salt int64) []*task {
	out := make([]*task, 0, len(h.items))
	for h.Len() > 0 {
		out = append(out, heap.Pop(h).(*task))
	}
	for i, t := range out {
		if !validTask(t, salt) {
			ckSeed(false, fmt.Sprintf("heap: drained payload corrupt at %d", i))
			break
		}
	}
	return out
}

func checkDrain(h *taskHeap, ref []*task, tag string, salt int64) {
	out := drain(h, salt)
	if len(out) != len(ref) {
		ckSeed(false, fmt.Sprintf("heap: %s drain length", tag))
		return
	}
	for i := range out {
		if out[i] != ref[i] {
			ckSeed(false, fmt.Sprintf("heap: %s pop order mismatch at %d", tag, i))
			return
		}
		if i > 0 && lessTask(h.max, out[i], out[i-1]) {
			ckSeed(false, fmt.Sprintf("heap: %s pop not monotone at %d", tag, i))
			return
		}
	}
}

func main() {
	curSeed = envSeed()
	r := rand.New(rand.NewSource(curSeed))

	// 1) 大规模 Push/Pop，Pop 顺序必须与参考全序一致。
	const n = 1500
	h := &taskHeap{items: make([]*task, 0, n)}
	items := make([]*task, 0, n)
	for i := 0; i < n; i++ {
		t := newTask(r.Int63n(500), i, curSeed)
		items = append(items, t)
		heap.Push(h, t)
		if i%300 == 299 {
			if !h.ok() {
				ck(false, "heap: invariant broken during push")
				break
			}
			gcStress()
		}
	}
	ck(h.Len() == n && h.ok(), "heap: len/invariant after push")
	ref := reference(items, false)
	checkDrain(h, ref, "push-pop", curSeed)

	// 2) heap.Fix：修改中间元素优先级后重排。
	h2 := &taskHeap{items: make([]*task, 0, 400)}
	items2 := make([]*task, 0, 400)
	for i := 0; i < 400; i++ {
		t := newTask(r.Int63n(1000), i, curSeed)
		items2 = append(items2, t)
	}
	h2.items = append(h2.items, items2...)
	// Init 需要堆切片就位后再建堆。
	heap.Init(h2)
	ck(h2.ok(), "heap: init invariant")
	gcStress()
	h2.items[10].prio = -1000
	heap.Fix(h2, 10)
	ck(h2.ok(), "heap: fix invariant")
	heap.Push(h2, newTask(-2000, 400, curSeed))
	ck(h2.ok(), "heap: push after fix invariant")
	ref2 := reference(h2.items, false)
	checkDrain(h2, ref2, "fix", curSeed)

	// 3) heap.Remove：删除中间元素，剩余全序不变。
	h3 := &taskHeap{items: make([]*task, 0, 300)}
	for i := 0; i < 300; i++ {
		heap.Push(h3, newTask(r.Int63n(700), i, curSeed))
	}
	removed := h3.items[5]
	gotRemoved := heap.Remove(h3, 5).(*task)
	ck(gotRemoved == removed, "heap: remove returned wrong element")
	ck(h3.ok() && h3.Len() == 299, "heap: remove invariant")
	for _, t := range h3.items {
		if t == removed {
			ck(false, "heap: removed element still present")
			break
		}
	}
	gcStress()
	ref3 := reference(h3.items, false)
	checkDrain(h3, ref3, "remove", curSeed)

	// 4) 大顶堆：Pop 必须按优先级降序。
	h4 := &taskHeap{items: make([]*task, 0, 600), max: true}
	items4 := make([]*task, 0, 600)
	for i := 0; i < 600; i++ {
		t := newTask(r.Int63n(300), i, curSeed)
		items4 = append(items4, t)
		heap.Push(h4, t)
	}
	ck(h4.ok(), "heap: max-heap invariant")
	ref4 := reference(items4, true)
	checkDrain(h4, ref4, "max-heap", curSeed)

	// 5) 分批 push/pop churn，跨 GC 校验剩余元素。
	h5 := &taskHeap{items: make([]*task, 0, 400)}
	seq := 10000
	for cycle := 0; cycle < 30; cycle++ {
		for k := 0; k < 100; k++ {
			heap.Push(h5, newTask(r.Int63n(1000), seq, curSeed))
			seq++
		}
		ref5 := reference(h5.items, false)
		for k := 0; k < 50 && h5.Len() > 0; k++ {
			got := heap.Pop(h5).(*task)
			if got != ref5[k] {
				ckSeed(false, fmt.Sprintf("heap: churn pop mismatch cycle %d", cycle))
				break
			}
		}
		if cycle%10 == 9 {
			gcStress()
			if !h5.ok() {
				ckSeed(false, fmt.Sprintf("heap: churn invariant after GC cycle %d", cycle))
				break
			}
			for _, t := range h5.items {
				if !validTask(t, curSeed) {
					ckSeed(false, fmt.Sprintf("heap: churn payload corrupt cycle %d", cycle))
					break
				}
			}
		}
	}
	ref5 := reference(h5.items, false)
	checkDrain(h5, ref5, "churn", curSeed)

	runtime.KeepAlive(items)
	runtime.KeepAlive(items2)
	runtime.KeepAlive(items4)
	finish("l13_container_heap_ptr")
}
