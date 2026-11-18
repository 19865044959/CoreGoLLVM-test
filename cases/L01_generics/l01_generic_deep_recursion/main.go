// L01: 泛型深递归——深链/深树上的泛型递归下降，递归中穿插 GC；
// 覆盖空/单元素/数万层规模与 int64 第二实例化，TORTURE_ROUNDS 放大深度。
package main

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"sync/atomic"
)

type cell struct {
	V   int64
	Tag string
}

type link[T any] struct {
	val   *T
	depth int
	next  *link[T]
}

func buildChain[T any](depth int, mk func(int) *T) *link[T] {
	var head *link[T]
	for i := depth - 1; i >= 0; i-- {
		head = &link[T]{val: mk(i), depth: i, next: head}
	}
	return head
}

func chainSum[T any](n *link[T], f func(*T) int64, gcEvery int) int64 {
	if n == nil {
		return 0
	}
	if gcEvery > 0 && n.depth%gcEvery == 0 {
		runtime.GC()
	}
	return f(n.val) + chainSum(n.next, f, gcEvery)
}

func chainLen[T any](n *link[T]) int {
	c := 0
	for n != nil {
		c++
		n = n.next
	}
	return c
}

func reverseChain[T any](head *link[T]) *link[T] {
	var prev *link[T]
	for head != nil {
		next := head.next
		head.next = prev
		prev = head
		head = next
	}
	return prev
}

type tree[T any] struct {
	val  *T
	kids []*tree[T]
}

func buildTree[T any](breadth, depth int, mk func(int) *T, nextID *int) *tree[T] {
	if depth == 0 {
		return nil
	}
	id := *nextID
	*nextID = id + 1
	n := &tree[T]{val: mk(id), kids: make([]*tree[T], 0, breadth)}
	for i := 0; i < breadth; i++ {
		if kid := buildTree(breadth, depth-1, mk, nextID); kid != nil {
			n.kids = append(n.kids, kid)
		}
	}
	return n
}

func treeStats[T any](t *tree[T], f func(*T) int64, nodes *int, gcEvery int) int64 {
	if t == nil {
		return 0
	}
	*nodes++
	if gcEvery > 0 && *nodes%gcEvery == 0 {
		runtime.GC()
	}
	s := f(t.val)
	for _, k := range t.kids {
		s += treeStats(k, f, nodes, gcEvery)
	}
	return s
}

var (
	failed  int32
	gcSink  [][]byte
	uafSeed int64 = 1
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
		fmt.Printf("FAILED %s seed=%d (%d failed checks)\n", name, uafSeed, n)
		os.Exit(1)
	}
	fmt.Println("ALL PASS " + name)
}

func envSeed() int64 {
	s := os.Getenv("UAF_SEED")
	if s == "" {
		return 1
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil || n <= 0 {
		return 1
	}
	return n
}

func tortureRounds(def int) int {
	s := os.Getenv("TORTURE_ROUNDS")
	if s == "" {
		return def
	}
	n, err := strconv.Atoi(s)
	if err != nil || n <= 0 {
		return def
	}
	if n > 16 {
		n = 16
	}
	return n
}

type sample struct {
	d int
	p *cell
}

func main() {
	uafSeed = envSeed()
	rounds := tortureRounds(1)
	off := int(uafSeed % 89)

	depth := (2500 + int(uafSeed%1500)) * rounds
	mkCell := func(i int) *cell {
		return &cell{V: int64((i+off)%97) + 1, Tag: fmt.Sprintf("c%d", i%11)}
	}
	var wantCellSum int64
	for i := 0; i < depth; i++ {
		wantCellSum += int64((i+off)%97) + 1
	}

	head := buildChain(depth, mkCell)
	ck(chainLen(head) == depth, "chain length mismatch after build")
	ck(head != nil && head.depth == 0, "chain head depth mismatch")

	step := depth/257 + 1
	samples := make([]sample, 0, 300)
	for n := head; n != nil; n = n.next {
		if n.depth%step == 0 {
			samples = append(samples, sample{d: n.depth, p: n.val})
		}
	}
	ck(len(samples) > 0, "no chain samples collected")

	gcStress()
	gcEvery := depth / 8
	if gcEvery < 1 {
		gcEvery = 1
	}
	got := chainSum(head, func(p *cell) int64 { return p.V }, gcEvery)
	ck(got == wantCellSum, "deep chain sum mismatch after recursive GC")

	tail := head
	for tail.next != nil {
		tail = tail.next
	}
	ck(tail.depth == depth-1 && tail.next == nil, "chain tail mismatch")
	for _, s := range samples {
		ck(s.p != nil, "sampled chain payload became nil after GC")
		if s.p == nil {
			continue
		}
		ck(s.p.V == int64((s.d+off)%97)+1, "sampled chain value corrupted after GC")
		ck(s.p.Tag == fmt.Sprintf("c%d", s.d%11), "sampled chain tag corrupted after GC")
	}

	// 反转后再次深递归，和不变。
	rev := reverseChain(head)
	ck(rev != nil && rev.depth == depth-1, "reversed chain head mismatch")
	revSum := chainSum(rev, func(p *cell) int64 { return p.V }, gcEvery)
	ck(revSum == wantCellSum, "reversed chain sum mismatch")
	last := rev
	for last.next != nil {
		last = last.next
	}
	ck(last.depth == 0 && last.next == nil, "reversed chain tail mismatch")
	gcStress()
	for _, s := range samples {
		ck(s.p != nil && s.p.V == int64((s.d+off)%97)+1, "chain sample corrupted after reverse GC")
	}

	// 空链 / 单元素边界。
	var nilChain *link[cell]
	ck(chainSum(nilChain, func(p *cell) int64 { return p.V }, 1) == 0, "nil chain sum must be zero")
	one := buildChain(1, mkCell)
	ck(chainLen(one) == 1, "single chain length mismatch")
	ck(chainSum(one, func(p *cell) int64 { return p.V }, 0) == int64((0+off)%97)+1,
		"single chain sum mismatch")
	revOne := reverseChain(one)
	ck(revOne != nil && revOne.depth == 0 && revOne.next == nil, "single chain reverse mismatch")

	// 第二实例化：int64 载荷的深递归。
	depth2 := depth / 2
	if depth2 < 1 {
		depth2 = 1
	}
	mkScalar := func(i int) *int64 {
		v := int64(i*3 - 7 + off)
		return &v
	}
	var wantScalar int64
	for i := 0; i < depth2; i++ {
		wantScalar += int64(i*3 - 7 + off)
	}
	sh := buildChain(depth2, mkScalar)
	scalarSum := chainSum(sh, func(p *int64) int64 { return *p }, depth2/4+1)
	ck(scalarSum == wantScalar, "int64 chain sum mismatch")
	ck(chainLen(sh) == depth2, "int64 chain length mismatch")

	// 平衡深树：递归下降 + 遍历中 GC。
	const breadth = 4
	const treeDepth = 5
	nodeCount := 0
	mkTreeCell := func(id int) *cell {
		return &cell{V: int64((id+off)%97) + 1, Tag: "t"}
	}
	root := buildTree(breadth, treeDepth, mkTreeCell, &nodeCount)
	wantNodes := 1
	level := 1
	for d := 1; d < treeDepth; d++ {
		level *= breadth
		wantNodes += level
	}
	ck(nodeCount == wantNodes, "tree node count mismatch")
	var wantTreeSum int64
	for i := 0; i < wantNodes; i++ {
		wantTreeSum += int64((i+off)%97) + 1
	}
	visitedNodes := 0
	tsum := treeStats(root, func(p *cell) int64 { return p.V }, &visitedNodes, 64)
	ck(visitedNodes == wantNodes, "tree traversal node count mismatch")
	ck(tsum == wantTreeSum, "tree payload sum mismatch")

	// 前序遍历的 id 分配顺序应与 payload 一致。
	stack := []*tree[cell]{root}
	id := 0
	for len(stack) > 0 {
		n := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		ck(n.val != nil && n.val.V == int64((id+off)%97)+1, "tree payload corrupted in pre-order")
		id++
		for i := len(n.kids) - 1; i >= 0; i-- {
			stack = append(stack, n.kids[i])
		}
	}
	ck(id == wantNodes, "tree pre-order visited node count mismatch")
	gcStress()
	ck(root.val != nil && root.val.V == int64((0+off)%97)+1, "tree root corrupted after GC")

	finish("l01_generic_deep_recursion")
}
