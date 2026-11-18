// L11: 多赋值/交换中的死存储——并行赋值的 RHS 求值顺序与副作用、
// 被后续覆盖的交换中间值、结构体/数组/切片元素交换、指针别名可见性
// 必须保持 Go 语义，最终值跨 GC 不变。
package main

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"sync/atomic"
)

type ds2Node struct {
	a, b int64
	p    *ds2Node
}

var (
	failed  int32
	gcSink  [][]byte
	seed    uint64 = 1
	ds2Side []int
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

func ds2S1() int64 { ds2Side = append(ds2Side, 1); return 10 }
func ds2S2() int64 { ds2Side = append(ds2Side, 2); return 20 }
func ds2S3() int64 { ds2Side = append(ds2Side, 3); return 30 }

func ds2SwapPair(a, b int64) (int64, int64) {
	a, b = b, a
	return a, b
}

func ds2EvalOrder() (int64, int64, int64) {
	var x, y, z int64
	x, y, z = ds2S1(), ds2S2(), ds2S3()
	return x, y, z
}

func ds2BlankLHS() int64 {
	var x int64
	_, x = ds2S1(), ds2S2()
	return x
}

func ds2OverwrittenSwap() int64 {
	x, y := ds2S1(), ds2S2()
	x, y = y, x
	x, y = 7, 9
	return x*100 + y
}

func ds2SwapFields(s *ds2Node) int64 {
	s.a, s.b = s.b, s.a
	return s.a*10 + s.b
}

func ds2Reverse(a []int64) {
	for i, j := 0, len(a)-1; i < j; i, j = i+1, j-1 {
		a[i], a[j] = a[j], a[i]
	}
}

func ds2SelfAssign(x int64) int64 {
	x = x
	return x
}

func ds2Alias() int64 {
	var s ds2Node
	s.a = 1
	p := &s.a
	s.a = 2
	s.a = 3
	return *p + s.a
}

func ds2MapSwap() int64 {
	m := map[string]int64{"a": 1, "b": 2}
	m["a"], m["b"] = m["b"], m["a"]
	return m["a"]*10 + m["b"]
}

func ds2ArraySwap() int64 {
	var a [4]int64
	a[0], a[1] = 1, 2
	p := &a[0]
	a[0], a[1] = a[1], a[0]
	return *p*10 + a[1]
}

func ds2LoopStores(n int) (int64, int64) {
	var last, keep int64
	for i := 0; i < n; i++ {
		last = int64(i)
		last = last*2 + 1
		if i == n/2 {
			keep = last
		}
	}
	return last, keep
}

func ds2GCNode() int64 {
	s := &ds2Node{a: 5, b: 7, p: &ds2Node{a: 11}}
	s.a, s.b = s.b, s.a
	gcStress()
	return s.a + s.b + s.p.a
}

func main() {
	seed = loadSeed()
	if seed == 0 {
		seed = 1
	}

	if a, b := ds2SwapPair(1, 2); a != 2 || b != 1 {
		ck(false, "parallel assignment swap")
	}

	ds2Side = ds2Side[:0]
	x, y, z := ds2EvalOrder()
	ck(x == 10 && y == 20 && z == 30, "parallel assignment results")
	ck(len(ds2Side) == 3 && ds2Side[0] == 1 && ds2Side[1] == 2 && ds2Side[2] == 3, "parallel assignment evaluation order")

	ds2Side = ds2Side[:0]
	ck(ds2BlankLHS() == 20, "blank LHS keeps value")
	ck(len(ds2Side) == 2 && ds2Side[0] == 1 && ds2Side[1] == 2, "blank LHS still evaluates RHS")

	ds2Side = ds2Side[:0]
	ck(ds2OverwrittenSwap() == 709, "overwritten swap final values")
	ck(len(ds2Side) == 2 && ds2Side[0] == 1 && ds2Side[1] == 2, "overwritten swap RHS side effects")

	s := &ds2Node{a: 1, b: 2}
	ck(ds2SwapFields(s) == 21, "struct field swap")

	n := 64 + int(seed%32)
	orig := make([]int64, n)
	for i := range orig {
		orig[i] = int64(i*3 + 1)
	}
	rev := append([]int64(nil), orig...)
	ds2Reverse(rev)
	ok := true
	for i := range rev {
		if rev[i] != orig[n-1-i] {
			ok = false
			break
		}
	}
	ck(ok, "slice swap reversal")
	gcStress()
	ok = true
	for i := range rev {
		if rev[i] != int64((n-1-i)*3+1) {
			ok = false
			break
		}
	}
	ck(ok, "slice reversal after GC")

	ck(ds2SelfAssign(42) == 42, "self assignment")
	ck(ds2Alias() == 6, "alias sees final store")
	ck(ds2MapSwap() == 21, "map element swap")
	ck(ds2ArraySwap() == 21, "array element swap through alias")
	ck(ds2GCNode() == 23, "swapped pointer fields after GC")

	last, keep := ds2LoopStores(n)
	ck(last == int64(2*(n-1)+1), "loop final overwritten store")
	ck(keep == int64(2*(n/2)+1), "loop selected intermediate store")
	gcStress()
	if l2, k2 := ds2LoopStores(n); l2 != last || k2 != keep {
		ck(false, "loop stores after GC")
	}

	finish("l11_dead_store_swap")
}
