// L13: 4 路有序迭代器正向/反向归并，重复 key 规则确定：源序号大者胜出。
package main

import (
	"container/heap"
	"fmt"
	"os"
	"runtime"
	"sync/atomic"
)

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

type item struct {
	text string
	n    int64
}

type kv struct {
	key int
	src int
	val *item
}

var mods = [4]int{2, 3, 5, 7}

func buildSources(n int) [4][]kv {
	var srcs [4][]kv
	for s := 0; s < 4; s++ {
		for k := 0; k < n; k++ {
			if k%mods[s] != 0 {
				continue
			}
			srcs[s] = append(srcs[s], kv{
				key: k,
				src: s,
				val: &item{text: fmt.Sprintf("s%d-k%03d", s, k), n: int64(k*10 + s)},
			})
		}
	}
	return srcs
}

type sliceIter struct {
	data []kv
	pos  int
	rev  bool
}

func (it *sliceIter) next() (kv, bool) {
	if it.rev {
		if it.pos < 0 {
			return kv{}, false
		}
		v := it.data[it.pos]
		it.pos--
		return v, true
	}
	if it.pos >= len(it.data) {
		return kv{}, false
	}
	v := it.data[it.pos]
	it.pos++
	return v, true
}

type mergeHeap struct {
	items []kv
	rev   bool
}

func (h mergeHeap) Len() int { return len(h.items) }

func (h mergeHeap) Less(i, j int) bool {
	a, b := h.items[i], h.items[j]
	if a.key != b.key {
		if h.rev {
			return a.key > b.key
		}
		return a.key < b.key
	}
	return a.src > b.src
}

func (h mergeHeap) Swap(i, j int) { h.items[i], h.items[j] = h.items[j], h.items[i] }

func (h *mergeHeap) Push(x any) { h.items = append(h.items, x.(kv)) }

func (h *mergeHeap) Pop() any {
	old := h.items
	n := len(old)
	v := old[n-1]
	h.items = old[:n-1]
	return v
}

func merge4(srcs [4][]kv, rev bool) []kv {
	iters := make([]*sliceIter, 4)
	for s := 0; s < 4; s++ {
		it := &sliceIter{data: srcs[s], rev: rev}
		if rev {
			it.pos = len(srcs[s]) - 1
		}
		iters[s] = it
	}
	h := &mergeHeap{rev: rev}
	for _, it := range iters {
		if v, ok := it.next(); ok {
			heap.Push(h, v)
		}
	}
	out := make([]kv, 0, 64)
	for h.Len() > 0 {
		v := heap.Pop(h).(kv)
		if len(out) == 0 || out[len(out)-1].key != v.key {
			out = append(out, v)
		}
		if nv, ok := iters[v.src].next(); ok {
			heap.Push(h, nv)
		}
	}
	return out
}

func main() {
	const N = 210
	srcs := buildSources(N)
	gcStress()

	totalEntries := 0
	for s := 0; s < 4; s++ {
		ck(len(srcs[s]) > 0, fmt.Sprintf("source %d non-empty", s))
		for i := 1; i < len(srcs[s]); i++ {
			ck(srcs[s][i-1].key < srcs[s][i].key, fmt.Sprintf("source %d strictly ascending", s))
		}
		totalEntries += len(srcs[s])
	}

	fwd := merge4(srcs, false)
	gcStress()
	rev := merge4(srcs, true)
	gcStress()

	expected := make(map[int]int, N)
	for s := 0; s < 4; s++ {
		for _, e := range srcs[s] {
			expected[e.key] = s // 后遍历到的更大 src 覆盖，即"后者胜出"
		}
	}
	ck(len(fwd) == len(expected), fmt.Sprintf("forward merge size: got %d want %d", len(fwd), len(expected)))
	ck(len(rev) == len(expected), "reverse merge size")
	ck(totalEntries > len(fwd), "duplicate keys were merged")

	dupKeys := 0
	for k := 0; k < N; k++ {
		c := 0
		for s := 0; s < 4; s++ {
			if k%mods[s] == 0 {
				c++
			}
		}
		if c > 1 {
			dupKeys++
		}
	}
	ck(dupKeys > 0, "test data contains duplicate keys")

	sumKey := 0
	expectedSum := 0
	maxKey := -1
	for k := range expected {
		expectedSum += k
		if k > maxKey {
			maxKey = k
		}
	}
	for i, e := range fwd {
		wantSrc, ok := expected[e.key]
		ck(ok, fmt.Sprintf("forward key %d in expected set", e.key))
		ck(e.src == wantSrc, fmt.Sprintf("forward key %d duplicate rule: src=%d want=%d", e.key, e.src, wantSrc))
		if i > 0 {
			ck(fwd[i-1].key < e.key, "forward keys strictly ascending")
		}
		ck(e.val != nil && e.val.text == fmt.Sprintf("s%d-k%03d", e.src, e.key), "forward value text")
		ck(e.val != nil && e.val.n == int64(e.key*10+e.src), "forward value number")
		sumKey += e.key
	}
	ck(sumKey == expectedSum, "forward key sum")

	for i, e := range rev {
		wantSrc, ok := expected[e.key]
		ck(ok, fmt.Sprintf("reverse key %d in expected set", e.key))
		ck(e.src == wantSrc, fmt.Sprintf("reverse key %d duplicate rule: src=%d want=%d", e.key, e.src, wantSrc))
		if i > 0 {
			ck(rev[i-1].key > e.key, "reverse keys strictly descending")
		}
		ck(e.val != nil && e.val.text == fmt.Sprintf("s%d-k%03d", e.src, e.key), "reverse value text")
		ck(e.val.n == int64(e.key*10+e.src), "reverse value number")
	}
	for i := range fwd {
		ck(fwd[i].key == rev[len(rev)-1-i].key, "reverse order mirrors forward")
		ck(fwd[i].src == rev[len(rev)-1-i].src, "reverse duplicate rule mirrors forward")
	}
	if len(fwd) > 0 {
		ck(fwd[0].key == 0, "first merged key is 0")
		ck(rev[0].key == maxKey, "reverse first key is max")
	}

	// 归并结果跨 GC 仍持有有效指针。
	gcStress()
	for _, e := range fwd {
		ck(e.val != nil && e.val.n == int64(e.key*10+e.src), "forward value survives GC")
	}
	for _, e := range rev {
		ck(e.val != nil && e.val.text[0] == 's', "reverse value survives GC")
	}

	runtime.KeepAlive(srcs)
	runtime.KeepAlive(fwd)
	runtime.KeepAlive(rev)
	finish("l13_merge_iterator_4way")
}
