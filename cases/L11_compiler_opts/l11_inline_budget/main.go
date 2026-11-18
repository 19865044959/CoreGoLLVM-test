// L11: 内联预算与递归边界——递归函数本身不内联，但其中的小型叶子函数、
// 线性包装函数可被内联；参数求值顺序、叶子副作用次数、栈增长与 GC 后语义
// 在默认优化与 -N -l 下必须一致。
package main

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"sync/atomic"
)

type ibNode struct {
	v           int64
	left, right *ibNode
}

var (
	failed      int32
	gcSink      [][]byte
	seed        uint64 = 1
	ibLeafCalls int64
	ibTrace     []int
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

func ibLeaf(x int64) int64 {
	ibLeafCalls++
	return x*3 + 1
}

//go:noinline
func ibLeafNoInline(x int64) int64 {
	ibLeafCalls++
	return x*3 + 1
}

func ibRec(n, acc int64) int64 {
	if n <= 0 {
		return acc
	}
	return ibRec(n-1, acc+ibLeaf(n))
}

//go:noinline
func ibRecNoInline(n, acc int64) int64 {
	if n <= 0 {
		return acc
	}
	return ibRecNoInline(n-1, acc+ibLeafNoInline(n))
}

func ibArg(id int, v int64) int64 {
	ibTrace = append(ibTrace, id)
	return v
}

func ibPair(a, b int64) int64 { return a*10 + b }

func ibEval(n int64) int64 {
	if n == 0 {
		return 0
	}
	return ibPair(ibArg(1, n), ibArg(2, n)) + ibEval(n-1)
}

func ibL0(x int64) int64 { return x + 1 }
func ibL1(x int64) int64 { return ibL0(x)*2 - 3 }
func ibL2(x int64) int64 { return ibL1(x) + x/2 }
func ibL3(x int64) int64 { return ibL2(x)*3 + 1 }
func ibL4(x int64) int64 { return ibL3(x) - x }
func ibL5(x int64) int64 { return ibL4(x) + 7 }

//go:noinline
func ibN0(x int64) int64 { return x + 1 }

//go:noinline
func ibN1(x int64) int64 { return ibN0(x)*2 - 3 }

//go:noinline
func ibN2(x int64) int64 { return ibN1(x) + x/2 }

//go:noinline
func ibN3(x int64) int64 { return ibN2(x)*3 + 1 }

//go:noinline
func ibN4(x int64) int64 { return ibN3(x) - x }

//go:noinline
func ibN5(x int64) int64 { return ibN4(x) + 7 }

func ibBig(x int64) int64 {
	s := x
	for i := int64(1); i <= 9; i++ {
		s = s*i + (x ^ i) - (s >> 2)
		if s%3 == 0 {
			s += x
		} else {
			s -= i
		}
	}
	return s
}

func ibBuild(depth int, base int64) *ibNode {
	if depth == 0 {
		return &ibNode{v: base}
	}
	return &ibNode{v: base, left: ibBuild(depth-1, base*2), right: ibBuild(depth-1, base*2+1)}
}

func ibSumRec(n *ibNode) int64 {
	if n == nil {
		return 0
	}
	return ibLeaf(n.v) + ibSumRec(n.left) + ibSumRec(n.right)
}

func ibCount(n *ibNode) int64 {
	if n == nil {
		return 0
	}
	return 1 + ibCount(n.left) + ibCount(n.right)
}

func main() {
	seed = loadSeed()
	if seed == 0 {
		seed = 1
	}

	depth := 2500 + int64(seed%500)
	wantDeep := 3*depth*(depth+1)/2 + depth

	ibLeafCalls = 0
	gotDeep := ibRec(depth, 0)
	ck(gotDeep == wantDeep, "recursive sum with inlinable leaf")
	ck(ibLeafCalls == depth, "inlinable leaf called once per level")

	ibLeafCalls = 0
	gotDeepN := ibRecNoInline(depth, 0)
	ck(gotDeepN == wantDeep, "noinline recursive sum")
	ck(ibLeafCalls == depth, "noinline leaf called once per level")

	ibTrace = ibTrace[:0]
	if got := ibEval(64); got != 11*64*65/2 {
		ck(false, "recursive argument pair sum")
	}
	ck(len(ibTrace) == 128, "recursive argument evaluation count")
	orderOK := true
	for i := 0; i < len(ibTrace); i += 2 {
		if ibTrace[i] != 1 || ibTrace[i+1] != 2 {
			orderOK = false
			break
		}
	}
	ck(orderOK, "argument evaluation order preserved across recursion")

	for _, x := range []int64{5, -7, 12345, (1 << 40) + 3} {
		want := ((x+1)*2-3+x/2)*3 + 1 - x + 7
		got := ibL5(x)
		gotN := ibN5(x)
		ck(got == want, "inline call chain result")
		ck(gotN == want, "noinline call chain result")
		ck(got == gotN, "inline vs noinline call chain")
	}

	for _, x := range []int64{0, 1, -1, 99, -1234567, 13} {
		ck(ibBig(x) == ibBigNoInline(x), "large-body inline vs noinline")
	}

	td := 8 + int(seed%3)
	root := ibBuild(td, 1)
	nodeCount := int64(1)<<(td+1) - 1
	valueSum := nodeCount * (nodeCount + 1) / 2
	wantTree := 3*valueSum + nodeCount

	ibLeafCalls = 0
	treeSum := ibSumRec(root)
	ck(treeSum == wantTree, "tree sum through recursive leaf")
	ck(ibLeafCalls == nodeCount, "tree leaf call count")

	gcStress()
	ck(ibSumRec(root) == wantTree, "tree sum after GC")
	ck(ibCount(root) == nodeCount, "tree node count after GC")
	leftMost := root
	for leftMost.left != nil {
		leftMost = leftMost.left
	}
	ck(leftMost.v == 1<<td, "tree left spine after GC")
	ck(root.v == 1 && root.right != nil, "tree root fields after GC")

	// 保留的 trace 与深层结果跨最终 GC 复核。
	ck(len(ibTrace) == 128 && ibTrace[0] == 1 && ibTrace[127] == 2, "trace after GC")
	ck(ibRec(depth, 0) == wantDeep, "deep recursion after GC")

	finish("l11_inline_budget")
}

//go:noinline
func ibBigNoInline(x int64) int64 {
	s := x
	for i := int64(1); i <= 9; i++ {
		s = s*i + (x ^ i) - (s >> 2)
		if s%3 == 0 {
			s += x
		} else {
			s -= i
		}
	}
	return s
}
