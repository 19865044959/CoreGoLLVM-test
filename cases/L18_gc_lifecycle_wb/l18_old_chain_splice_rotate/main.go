// L18: 长老板链上插入年轻节点（老 anchor->年轻 splice），并在老 tail 上追加
// 年轻三节点链；所有 setter 为 noinline，返回后年轻节点只由老链引用，GC 后
// 逐节点校验 next/prev 双向链与内容。
package main

import (
	"fmt"
	"math/rand"
	"os"
	"runtime"
	"strconv"
	"sync/atomic"
)

const (
	baseChainN    = 48
	spliceRounds  = 12
	spliceIDBase  = int64(1000)
	tailChainBase = int64(2000)
)

type chainNode struct {
	id   int64
	name string
	prev *chainNode
	next *chainNode
}

type chainHolder struct {
	head *chainNode
	tail *chainNode
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

func seedValue() int64 {
	s := os.Getenv("UAF_SEED")
	if s == "" {
		return 1
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 1
	}
	return n
}

//go:noinline
func buildBaseChain() *chainHolder {
	h := &chainHolder{}
	var prev *chainNode
	for i := 0; i < baseChainN; i++ {
		n := &chainNode{id: int64(i), name: "base"}
		if prev == nil {
			h.head = n
		} else {
			prev.next = n
			n.prev = prev
		}
		prev = n
	}
	h.tail = prev
	gcStress()
	gcStress() // holder 与全部 base 节点晋升为老对象
	return h
}

// findBaseByID 在链上按 id 查找 base 节点（base 节点在插入过程中只移位不删除）。
//
//go:noinline
func findBaseByID(h *chainHolder, id int64) *chainNode {
	for n := h.head; n != nil; n = n.next {
		if n.name == "base" && n.id == id {
			return n
		}
	}
	return nil
}

// spliceYoung 在老 anchor 之后插入一个年轻节点：老->年轻 关键写屏障路径。
//
//go:noinline
func spliceYoung(anchor *chainNode, id int64) {
	next := anchor.next
	y := &chainNode{id: id, name: "sp", prev: anchor, next: next}
	if next != nil {
		next.prev = y
	}
	anchor.next = y
}

// appendYoungChain 在老 tail 之后追加 年轻 a->b->c 链，并更新老 holder 的 tail。
//
//go:noinline
func appendYoungChain(h *chainHolder, base int64) {
	a := &chainNode{id: base, name: "t"}
	b := &chainNode{id: base + 1, name: "t"}
	c := &chainNode{id: base + 2, name: "t"}
	a.next = b
	b.prev = a
	b.next = c
	c.prev = b
	h.tail.next = a
	a.prev = h.tail
	h.tail = c
}

func verifyChain(h *chainHolder, rounds int, seed int64) {
	count := 0
	prev := (*chainNode)(nil)
	baseSeen := make([]bool, baseChainN)
	splices := 0
	tails := 0
	for n := h.head; n != nil; n = n.next {
		count++
		if n.prev != prev {
			ck(false, fmt.Sprintf("prev link broken at node %d (seed=%d)", count, seed))
			return
		}
		switch n.name {
		case "base":
			if n.id < 0 || n.id >= baseChainN || baseSeen[n.id] {
				ck(false, fmt.Sprintf("base node id=%d invalid or duplicated (seed=%d)", n.id, seed))
				return
			}
			baseSeen[n.id] = true
		case "sp":
			if n.id <= spliceIDBase || n.id > spliceIDBase+int64(rounds) {
				ck(false, fmt.Sprintf("splice node id=%d unexpected (seed=%d)", n.id, seed))
				return
			}
			splices++
		case "t":
			if n.id < tailChainBase || n.id >= tailChainBase+int64(rounds*10)+3 {
				ck(false, fmt.Sprintf("tail node id=%d unexpected (seed=%d)", n.id, seed))
				return
			}
			tails++
		default:
			ck(false, fmt.Sprintf("node name %q corrupted (seed=%d)", n.name, seed))
			return
		}
		prev = n
	}
	if prev != h.tail {
		ck(false, fmt.Sprintf("tail pointer mismatch (seed=%d)", seed))
	}
	if splices != rounds {
		ck(false, fmt.Sprintf("splice count=%d want=%d (seed=%d)", splices, rounds, seed))
	}
	if tails != rounds*3 {
		ck(false, fmt.Sprintf("tail chain node count=%d want=%d (seed=%d)", tails, rounds*3, seed))
	}
	if count != baseChainN+rounds*4 {
		ck(false, fmt.Sprintf("chain length=%d want=%d (seed=%d)", count, baseChainN+rounds*4, seed))
	}
	for i, ok := range baseSeen {
		if !ok {
			ck(false, fmt.Sprintf("base node %d lost (seed=%d)", i, seed))
			break
		}
	}
	// 最后三个节点必须是本轮的 年轻 tail 链。
	if h.tail != nil && h.tail.prev != nil && h.tail.prev.prev != nil {
		a := h.tail.prev.prev
		b := h.tail.prev
		want := tailChainBase + int64(rounds*10)
		ck(a.name == "t" && b.name == "t" && h.tail.name == "t", fmt.Sprintf("tail chain names corrupted (seed=%d)", seed))
		ck(a.id == want && b.id == want+1 && h.tail.id == want+2,
			fmt.Sprintf("tail chain ids=%d,%d,%d want=%d,%d,%d (seed=%d)", a.id, b.id, h.tail.id, want, want+1, want+2, seed))
	} else {
		ck(false, fmt.Sprintf("tail chain truncated (seed=%d)", seed))
	}
}

func main() {
	seed := seedValue()
	rng := rand.New(rand.NewSource(seed))
	h := buildBaseChain()

	for r := 1; r <= spliceRounds; r++ {
		pos := int64(rng.Intn(baseChainN))
		anchor := findBaseByID(h, pos)
		ck(anchor != nil, fmt.Sprintf("base anchor %d not found (seed=%d)", pos, seed))
		spliceYoung(anchor, spliceIDBase+int64(r))
		appendYoungChain(h, tailChainBase+int64(r*10))
		gcStress()
		verifyChain(h, r, seed)
	}
	runtime.KeepAlive(h)
	finish("l18_old_chain_splice_rotate")
}
