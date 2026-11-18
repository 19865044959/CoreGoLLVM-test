// L11: 内联预算 × 变参/多返回值包装——小型包装链、变参转发、切片透传别名、
// 参数求值顺序与副作用次数在默认优化与 -N -l 下必须一致；变参切片与
// 指针元素逃逸后跨显式 GC 保持有效。
package main

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"sync/atomic"
)

type ivNode struct {
	v   int64
	tag string
}

var (
	failed  int32
	gcSink  [][]byte
	seed    uint64 = 1
	ivTrace []int
	ivCalls int64
	ivKeep  []*ivNode
	ivVars  [][]*ivNode
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

func ivLeaf(x int64) int64 {
	ivCalls++
	return x*3 + 1
}

//go:noinline
func ivLeafNI(x int64) int64 {
	ivCalls++
	return x*3 + 1
}

func ivWrap1(x int64) int64 { return ivLeaf(x) + 1 }
func ivWrap2(x int64) int64 { return ivWrap1(x)*2 - 3 }
func ivWrap3(x int64) int64 { return ivWrap2(x) + ivLeaf(x+1) }

func ivWrap1NI(x int64) int64 { return ivLeafNI(x) + 1 }
func ivWrap2NI(x int64) int64 { return ivWrap1NI(x)*2 - 3 }
func ivWrap3NI(x int64) int64 { return ivWrap2NI(x) + ivLeafNI(x+1) }

func ivVarSum(xs ...int64) int64 {
	var s int64
	for _, x := range xs {
		s += x
	}
	return s
}

//go:noinline
func ivVarSumNI(xs ...int64) int64 {
	var s int64
	for _, x := range xs {
		s += x
	}
	return s
}

func ivForward(xs ...int64) int64 { return ivVarSum(xs...) + int64(len(xs)) }

//go:noinline
func ivForwardNI(xs ...int64) int64 { return ivVarSumNI(xs...) + int64(len(xs)) }

func ivTouch(xs ...int64) int64 {
	if len(xs) > 0 {
		xs[0] += 1000
	}
	return ivVarSum(xs...)
}

//go:noinline
func ivTouchNI(xs ...int64) int64 {
	if len(xs) > 0 {
		xs[0] += 1000
	}
	return ivVarSumNI(xs...)
}

func ivExtend(xs ...int64) []int64 { return append(xs, -1) }

//go:noinline
func ivExtendNI(xs ...int64) []int64 { return append(xs, -1) }

func ivTwo(x int64) (int64, int64) { return ivLeaf(x), ivLeaf(x + 1) }

//go:noinline
func ivTwoNI(x int64) (int64, int64) { return ivLeafNI(x), ivLeafNI(x + 1) }

func ivPair(x int64) int64 {
	a, b := ivTwo(x)
	return a*1000 + b
}

func ivPairNI(x int64) int64 {
	a, b := ivTwoNI(x)
	return a*1000 + b
}

func ivArg(id int, v int64) int64 {
	ivTrace = append(ivTrace, id)
	return v
}

func ivArgs3(a, b, c int64) int64 { return a*100 + b*10 + c }

//go:noinline
func ivArgs3NI(a, b, c int64) int64 { return a*100 + b*10 + c }

func ivBig(x int64) int64 {
	ivCalls++
	s := x
	for i := int64(1); i <= 13; i++ {
		s = s*i + (x ^ i) - (s >> 2)
		if s%3 == 0 {
			s += x
		} else {
			s -= i
		}
	}
	return s
}

//go:noinline
func ivBigNI(x int64) int64 {
	ivCalls++
	s := x
	for i := int64(1); i <= 13; i++ {
		s = s*i + (x ^ i) - (s >> 2)
		if s%3 == 0 {
			s += x
		} else {
			s -= i
		}
	}
	return s
}

func ivNodeSum(xs ...*ivNode) int64 {
	var s int64
	for _, n := range xs {
		if n != nil {
			s += n.v + int64(len(n.tag))
		}
	}
	return s
}

func ivCollect(xs ...*ivNode) []*ivNode { return xs }

func main() {
	seed = loadSeed()
	if seed == 0 {
		seed = 1
	}

	inputs := []int64{0, 1, -7, 999999, -(1 << 40)}
	for _, x := range inputs {
		ivCalls = 0
		got := ivWrap3(x)
		ck(got == 9*x+5, "inline wrapper chain result")
		ck(ivCalls == 2, "inline wrapper leaf call count")

		ivCalls = 0
		gotN := ivWrap3NI(x)
		ck(gotN == 9*x+5, "noinline wrapper chain result")
		ck(ivCalls == 2, "noinline wrapper leaf call count")
		ck(got == gotN, "inline vs noinline wrapper chain")

		ivCalls = 0
		pv := ivPair(x)
		ck(pv == (3*x+1)*1000+(3*x+4), "multi-return wrapper value")
		ck(ivCalls == 2, "multi-return wrapper leaf call count")

		ivCalls = 0
		pvN := ivPairNI(x)
		ck(pvN == pv, "multi-return noinline wrapper value")
		ck(ivCalls == 2, "multi-return noinline leaf call count")
	}

	n := 24 + int(seed%13)
	arr := make([]int64, n, 2*n)
	var manual int64
	for i := 0; i < n; i++ {
		arr[i] = int64(i*3 + 1)
		manual += arr[i]
	}

	ck(ivVarSum(arr...) == manual, "variadic sum")
	ck(ivVarSumNI(arr...) == manual, "variadic noinline sum")
	ck(ivForward(arr...) == manual+int64(n), "variadic forward")
	ck(ivForwardNI(arr...) == manual+int64(n), "variadic noinline forward")

	first := arr[0]
	touched := ivTouch(arr...)
	ck(arr[0] == first+1000, "variadic slice aliases caller storage")
	ck(touched == manual+1000, "variadic touched sum")
	arr[0] = first
	touchedN := ivTouchNI(arr...)
	ck(arr[0] == first+1000, "noinline variadic slice aliases caller storage")
	ck(touchedN == manual+1000, "noinline variadic touched sum")
	arr[0] = first

	ext := ivExtend(arr...)
	ck(len(ext) == n+1 && ext[n] == -1, "variadic extend result")
	extOK := true
	for i := 0; i < n; i++ {
		if arr[i] != int64(i*3+1) || ext[i] != arr[i] {
			extOK = false
			break
		}
	}
	ck(extOK, "variadic extend leaves caller prefix")

	full := make([]int64, n, n)
	for i := range full {
		full[i] = int64(i*5 - 21)
	}
	fullSnap := append([]int64(nil), full...)
	ext2 := ivExtendNI(full...)
	ck(len(ext2) == n+1 && ext2[n] == -1, "noinline variadic extend grows backing")
	for i := range full {
		if full[i] != fullSnap[i] {
			ck(false, "grown variadic extend changed caller")
			break
		}
	}

	ivTrace = ivTrace[:0]
	val := ivArgs3(ivArg(1, 2), ivArg(2, 3), ivArg(3, 4))
	ck(val == 234, "variadic wrapper argument values")
	ck(len(ivTrace) == 3 && ivTrace[0] == 1 && ivTrace[1] == 2 && ivTrace[2] == 3, "argument evaluation order")

	ivTrace = ivTrace[:0]
	valN := ivArgs3NI(ivArg(1, 2), ivArg(2, 3), ivArg(3, 4))
	ck(valN == 234, "noinline wrapper argument values")
	ck(len(ivTrace) == 3 && ivTrace[0] == 1 && ivTrace[1] == 2 && ivTrace[2] == 3, "noinline argument evaluation order")

	for _, x := range []int64{0, 3, -11, 1234567} {
		ivCalls = 0
		b := ivBig(x)
		ck(ivCalls == 1, "large body called once")
		ivCalls = 0
		bN := ivBigNI(x)
		ck(ivCalls == 1, "noinline large body called once")
		ck(b == bN, "large body inline vs noinline")
	}

	m := 6 + int(seed%5)
	nodes := make([]*ivNode, m)
	var wantNodes int64
	for i := 0; i < m; i++ {
		tag := "n" + strconv.Itoa(i)
		nodes[i] = &ivNode{v: int64(i*7 + 3), tag: tag}
		wantNodes += nodes[i].v + int64(len(tag))
	}
	ck(ivNodeSum(nodes...) == wantNodes, "pointer variadic sum")
	kept := ivCollect(nodes...)
	ivVars = append(ivVars, kept)
	ivKeep = append(ivKeep, nodes...)

	gcStress()
	ck(ivNodeSum(nodes...) == wantNodes, "pointer variadic sum after GC")
	ck(len(ivVars) == 1 && len(ivVars[0]) == m, "variadic slice escaped intact")
	nodeOK := true
	for i := 0; i < m; i++ {
		if ivVars[0][i] != nodes[i] || nodes[i].v != int64(i*7+3) || nodes[i].tag != "n"+strconv.Itoa(i) {
			nodeOK = false
			break
		}
	}
	ck(nodeOK, "variadic pointer elements after GC")
	ck(ivVarSum(arr...) == manual, "variadic sum after GC")

	ivCalls = 0
	ck(ivWrap3(17) == 158 && ivCalls == 2, "wrapper chain after GC")
	ck(ivWrap3NI(17) == 158 && ivCalls == 4, "noinline wrapper chain after GC")

	finish("l11_inline_budget_variadic")
}
