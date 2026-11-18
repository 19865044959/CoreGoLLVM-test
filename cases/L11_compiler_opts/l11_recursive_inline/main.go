// L11: 递归内联边界——递归函数（编译器拒绝内联）、互递归、被内联的
// 叶子包装函数与迭代版本必须给出相同结果；深递归跨栈增长/GC 后仍正确。
package main

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"sync/atomic"
)

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

func recSum(n int64) int64 {
	if n <= 0 {
		return 0
	}
	return n + recSum(n-1)
}

//go:noinline
func recSumNoInline(n int64) int64 {
	if n <= 0 {
		return 0
	}
	return n + recSumNoInline(n-1)
}

func iterSum(n int64) int64 {
	var t int64
	for i := int64(1); i <= n; i++ {
		t += i
	}
	return t
}

func isEven(n int64) bool {
	if n == 0 {
		return true
	}
	return isOdd(n - 1)
}

func isOdd(n int64) bool {
	if n == 0 {
		return false
	}
	return isEven(n - 1)
}

func leafSquare(x int64) int64 { return x*x + 3 }

func recLeaf(n int64) int64 {
	if n <= 0 {
		return leafSquare(0)
	}
	return leafSquare(n) + recLeaf(n-1)
}

func iterLeaf(n int64) int64 {
	var t int64
	for k := int64(0); k <= n; k++ {
		t += k*k + 3
	}
	return t
}

func chainA(x int64) int64 { return chainB(x) + 1 }
func chainB(x int64) int64 { return chainC(x) * 2 }
func chainC(x int64) int64 { return x ^ 0x55 }

//go:noinline
func chainANoInline(x int64) int64 { return chainBNoInline(x) + 1 }

//go:noinline
func chainBNoInline(x int64) int64 { return chainCNoInline(x) * 2 }

//go:noinline
func chainCNoInline(x int64) int64 { return x ^ 0x55 }

type rnode struct {
	v    int64
	l, r *rnode
}

func buildTree(depth int) *rnode {
	if depth < 0 {
		return nil
	}
	return &rnode{v: int64(depth), l: buildTree(depth - 1), r: buildTree(depth - 1)}
}

func treeSum(t *rnode) int64 {
	if t == nil {
		return 0
	}
	return t.v + treeSum(t.l) + treeSum(t.r)
}

func treeSumIter(t *rnode) int64 {
	if t == nil {
		return 0
	}
	var s int64
	stack := []*rnode{t}
	for len(stack) > 0 {
		n := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		s += n.v
		if n.l != nil {
			stack = append(stack, n.l)
		}
		if n.r != nil {
			stack = append(stack, n.r)
		}
	}
	return s
}

func treeCount(t *rnode) int64 {
	if t == nil {
		return 0
	}
	return 1 + treeCount(t.l) + treeCount(t.r)
}

func treeFormula(depth int) (sum, count int64) {
	count = 1<<(uint(depth)+1) - 1
	for d := 0; d <= depth; d++ {
		sum += int64(d) * (1 << uint(depth-d))
	}
	return
}

func recPanicDown(n int64) {
	if n <= 0 {
		panic("bottom")
	}
	recPanicDown(n - 1)
}

func panicRec(n int64) (out string) {
	defer func() {
		if r := recover(); r != nil {
			out = fmt.Sprint("recovered:", r)
		}
	}()
	recPanicDown(n)
	return "no panic"
}

func main() {
	seed = loadSeed()
	if seed == 0 {
		seed = 1
	}
	linear := int64(9000 + seed%1000)

	want := int64(linear * (linear + 1) / 2)
	ck(recSum(linear) == want, "deep recursion sum")
	ck(recSumNoInline(linear) == want, "noinline deep recursion sum")
	ck(iterSum(linear) == want, "iterative sum")
	ck(isEven(linear) != isOdd(linear), "mutual recursion even/odd")
	ck(isEven(linear) == (linear%2 == 0), "mutual recursion parity")

	leafRef := iterLeaf(500)
	ck(recLeaf(500) == leafRef, "recursive leaf packages vs iterative")
	ck(recLeaf(0) == 3, "recursion base leaf")

	for _, x := range []int64{0, 1, 7, 42, -3, 1<<40 + 1} {
		ck(chainA(x) == chainANoInline(x), "inline chain vs noinline chain")
	}

	depth := 10 + int(seed%3) // 10..12 -> up to 8191 节点
	tree := buildTree(depth)
	wantSum, wantCount := treeFormula(depth)
	ck(treeSum(tree) == wantSum, "tree recursive sum")
	ck(treeSumIter(tree) == wantSum, "tree iterative sum")
	ck(treeCount(tree) == wantCount, "tree node count")

	gcStress()
	ck(treeSum(tree) == wantSum, "tree sum after GC")
	ck(treeCount(tree) == wantCount, "tree count after GC")

	n := tree
	for d := 0; d < depth; d++ {
		n = n.l
		if n == nil {
			break
		}
	}
	ck(n != nil && n.v == 0 && n.l == nil && n.r == nil, "left spine survives GC")

	ck(panicRec(5000) == "recovered:bottom", "deep panic/recover at recursion bottom")
	ck(panicRec(0) == "recovered:bottom", "panic at depth zero")

	gcStress()
	ck(treeSum(tree) == wantSum, "tree sum after final GC")
	runtime.KeepAlive(tree)

	finish("l11_recursive_inline")
}
