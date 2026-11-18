// L11: nil 检查消除——重复解引用、循环中逐元素 nil 检查、跨调用 nil 检查
// 与指针链检查都必须保留语义；真正解引用 nil 仍 panic 且为 runtime.Error。
package main

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
)

type ncNode struct {
	v    int64
	next *ncNode
}

var (
	failed int32
	gcSink [][]byte
	seed   uint64 = 1
)

func ck(ok bool, msg string) {
	if !ok {
		atomic.AddInt32(&failed, 1)
		fmt.Printf("FAIL: %s (UAF_SEED=%d)\n", msg, seed)
	}
}

func gcStress() {
	for i := 0; i < 16; i++ {
		b := make([]byte, 4096)
		b[0] = byte(i)
		b[len(b)-1] = byte(255 - i)
		gcSink = append(gcSink[:0], b)
	}
	gcSink = gcSink[:0]
	runtime.GC()
	runtime.GC()
}

func loadSeed() uint64 {
	s := os.Getenv("UAF_SEED")
	if s == "" {
		return 1
	}
	n, err := strconv.ParseUint(s, 10, 64)
	if err != nil {
		return 1
	}
	return n
}

func finish(name string) {
	if n := atomic.LoadInt32(&failed); n != 0 {
		fmt.Printf("FAILED %s (%d failed checks, seed=%d)\n", name, n, seed)
		os.Exit(1)
	}
	fmt.Println("ALL PASS " + name)
}

func sumNode(n *ncNode) int64 {
	if n == nil {
		return -1
	}
	return n.v + n.v + n.v
}

func deref(n *ncNode) int64 { return n.v }

func doubleDeref(pp **ncNode) int64 {
	if pp == nil {
		return -1
	}
	p := *pp
	if p == nil {
		return -2
	}
	return p.v
}

func sumPtrs(ns []*ncNode) (sum int64, nilCount int) {
	for _, n := range ns {
		if n == nil {
			nilCount++
			continue
		}
		sum += n.v + n.v
	}
	return
}

func loopDeref(ns []*ncNode) int64 {
	var t int64
	for i := 0; i < len(ns); i++ {
		n := ns[i]
		if n == nil {
			t += 100
			continue
		}
		t += n.v
	}
	return t
}

func safeIndex(ns []*ncNode, i int) int64 {
	if i < 0 || i >= len(ns) {
		return -1
	}
	n := ns[i]
	if n == nil {
		return -2
	}
	return n.v + n.v
}

func maybeNode(flag bool) *ncNode {
	if flag {
		return &ncNode{v: 9}
	}
	return nil
}

func derefAfterCall(n *ncNode) int64 {
	if n == nil {
		return -1
	}
	gcStress()
	return n.v + n.v
}

func chainValue(n *ncNode) int64 {
	if n == nil || n.next == nil || n.next.next == nil {
		return -1
	}
	return n.next.next.v
}

func expectNilPanic(name string, fn func()) {
	defer func() {
		r := recover()
		if r == nil {
			ck(false, name+": expected panic")
			return
		}
		if _, ok := r.(runtime.Error); !ok {
			ck(false, name+": panic is not runtime.Error")
			return
		}
		if !strings.Contains(fmt.Sprint(r), "nil pointer dereference") {
			ck(false, name+": unexpected panic text: "+fmt.Sprint(r))
		}
	}()
	fn()
}

func main() {
	seed = loadSeed()
	if seed == 0 {
		seed = 1
	}

	ck(sumNode(nil) == -1, "nil receiver guarded sum")
	ck(sumNode(&ncNode{v: 5}) == 15, "non-nil repeated dereference")
	expectNilPanic("direct nil dereference", func() { _ = deref(nil) })

	var nilNode *ncNode
	ck(doubleDeref(nil) == -1, "nil pointer-to-pointer")
	ck(doubleDeref(&nilNode) == -2, "inner nil pointer")
	p2 := &ncNode{v: 8}
	ck(doubleDeref(&p2) == 8, "double deref non-nil")

	n := 48 + int(seed%17)
	nodes := make([]*ncNode, n)
	var wantSum, wantSingle int64
	wantNil := 0
	for i := 0; i < n; i++ {
		if (i+int(seed%7))%3 != 0 {
			nodes[i] = &ncNode{v: int64(i)}
			wantSum += 2 * int64(i)
			wantSingle += int64(i)
		} else {
			wantNil++
		}
	}
	sum, nilCount := sumPtrs(nodes)
	ck(sum == wantSum && nilCount == wantNil, "mixed slice nil checks")
	ck(loopDeref(nodes) == wantSingle+int64(100*wantNil), "loop nil checks")

	ck(safeIndex(nodes, -1) == -1, "negative index guard")
	ck(safeIndex(nodes, n) == -1, "upper index guard")
	firstNil := -1
	for i := 0; i < n; i++ {
		if nodes[i] == nil {
			firstNil = i
			break
		}
	}
	if firstNil >= 0 {
		ck(safeIndex(nodes, firstNil) == -2, "nil element guard")
	}

	a := &ncNode{v: 1}
	b := &ncNode{v: 2}
	c := &ncNode{v: 3}
	a.next = b
	b.next = c
	ck(chainValue(a) == 3, "pointer chain after nil checks")
	ck(chainValue(&ncNode{v: 1}) == -1, "short chain guard")
	ck(chainValue(nil) == -1, "nil chain guard")

	ck(derefAfterCall(&ncNode{v: 21}) == 42, "deref after call and nil check")
	ck(derefAfterCall(nil) == -1, "nil check before call")
	ck(maybeNode(true).v == 9, "returned non-nil pointer")
	ck(maybeNode(false) == nil, "returned nil pointer")

	keep := &ncNode{v: 55}
	keep.next = &ncNode{v: 66}
	gcStress()
	ck(keep.v == 55 && keep.next.v == 66, "pointer fields after GC")
	sum2, nil2 := sumPtrs(nodes)
	ck(sum2 == wantSum && nil2 == wantNil, "mixed slice after GC")

	expectNilPanic("nil deref after GC", func() {
		gcStress()
		_ = deref(nil)
	})

	finish("l11_nilcheck_elim")
}
