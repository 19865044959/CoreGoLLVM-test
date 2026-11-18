// L11: 死存储消除——被覆盖的纯计算 store 可以删除，但 RHS 副作用、
// 指针别名可见的 store、全局/字段 store 必须保留；GC 后最终值一致。
package main

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"sync/atomic"
)

type dsNode struct{ v int64 }

type dsStruct struct {
	a, b int64
	p    *dsNode
}

var (
	failed   int32
	gcSink   [][]byte
	seed     uint64 = 1
	dsGlobal int64
	dsSide   int64
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

func dsSideEffect(v int64) int64 {
	dsSide += v
	return v
}

func deadStoreButCall() int64 {
	x := int64(0)
	x = dsSideEffect(7)
	x = 3
	return x
}

func overwriteLocal() int64 {
	x := int64(11)
	x = 22
	x = 33
	return x
}

func fieldStores() int64 {
	var s dsStruct
	s.a = 1
	s.a = 2
	s.b = s.a * 10
	return s.b
}

func aliasFieldStore() (int64, int64) {
	var s dsStruct
	s.a = 1
	p := &s.a
	s.a = 2
	return *p, s.a
}

func aliasArrayStore() (int64, int64) {
	var a [4]int64
	a[0] = 1
	p := &a[0]
	a[0] = 2
	return *p, a[0]
}

func sliceIndependence() (int64, int64) {
	s := make([]int64, 4)
	s[0] = 1
	s[1] = 2
	s[0] = 3
	return s[0], s[1]
}

func mapOverwriteBefore(before int64) (int64, int64) {
	m := make(map[int64]int64)
	m[1] = dsSideEffect(5)
	m[1] = dsSideEffect(6)
	m[1] = 9
	return m[1], dsSide - before
}

func dsPanicValue(v int64) int64 {
	panic("store-panic")
}

func storeThenPanic() (out string) {
	defer func() {
		if r := recover(); r != nil {
			out = fmt.Sprint("caught:", r)
		}
	}()
	var x int64
	x = dsPanicValue(7)
	x = 3
	return fmt.Sprint(x)
}

func keepThroughField() int64 {
	var s dsStruct
	s.p = &dsNode{v: 41}
	b := make([]byte, 2048)
	b[0] = 1
	gcSink = append(gcSink[:0], b)
	runtime.GC()
	runtime.GC()
	return s.p.v + 1
}

func nilOverwrite() bool {
	var s dsStruct
	s.p = &dsNode{v: 1}
	s.p = &dsNode{v: 2}
	runtime.GC()
	return s.p.v == 2
}

func twoNodeValues() int64 {
	s := &dsStruct{}
	s.p = &dsNode{v: 5}
	old := s.p
	s.p = &dsNode{v: 7}
	gcStress()
	return old.v + s.p.v
}

func conditionalStore(flag bool) int64 {
	x := int64(1)
	if flag {
		x = 2
	}
	x = 3
	return x
}

func loopStores(n int) (int64, int64) {
	var last, keep int64
	for i := 0; i < n; i++ {
		last = int64(i * 3)
		if i == n/2 {
			keep = last
		}
	}
	return last, keep
}

func loopCarried(n int) int64 {
	x := int64(0)
	for i := 0; i < n; i++ {
		x = int64(i)
		x = x*2 + 1
	}
	return x
}

func partialField() int64 {
	var s dsStruct
	s.a = 7
	s.b = 9
	s.a = 11
	return s.a + s.b
}

func globalStores() int64 {
	dsGlobal = 1
	dsGlobal = 2
	return dsGlobal
}

func interfaceStore() int64 {
	var x int64 = 1
	var i any = &x
	x = 2
	return *(i.(*int64))
}

func main() {
	seed = loadSeed()
	if seed == 0 {
		seed = 1
	}
	n := 32 + int(seed%32)

	before := dsSide
	ck(deadStoreButCall() == 3, "dead local store final value")
	ck(dsSide-before == 7, "dead store RHS side effect executed")

	ck(overwriteLocal() == 33, "overwritten local stores")
	ck(fieldStores() == 20, "overwritten field stores")
	if v1, v2 := aliasFieldStore(); v1 != 2 || v2 != 2 {
		ck(false, "field store visible through alias")
	}
	if v1, v2 := aliasArrayStore(); v1 != 2 || v2 != 2 {
		ck(false, "array store visible through alias")
	}
	if v1, v2 := sliceIndependence(); v1 != 3 || v2 != 2 {
		ck(false, "slice element stores independent")
	}

	before = dsSide
	if v, inc := mapOverwriteBefore(before); v != 9 || inc != 11 {
		ck(false, "map overwrite and RHS side effects")
	}
	ck(storeThenPanic() == "caught:store-panic", "store RHS panic preserved")
	ck(keepThroughField() == 42, "pointer field keeps object alive through GC")
	ck(nilOverwrite(), "overwritten pointer field final value")
	ck(twoNodeValues() == 12, "both old and new node values readable")
	ck(conditionalStore(true) == 3 && conditionalStore(false) == 3, "conditional store")
	last, keep := loopStores(n)
	ck(last == int64((n-1)*3), "loop final store")
	ck(keep == int64((n/2)*3), "loop selected store")
	ck(partialField() == 20, "partial field update")
	ck(globalStores() == 2, "global overwritten store")
	ck(loopCarried(n) == int64(2*(n-1)+1), "loop-carried store chain")
	ck(interfaceStore() == 2, "store visible through interface pointer")

	gcStress()
	ck(dsGlobal == 2, "global final value after GC")
	if last2, keep2 := loopStores(n); last2 != last || keep2 != keep {
		ck(false, "loop stores after GC")
	}
	ck(loopCarried(n) == int64(2*(n-1)+1), "loop-carried store after GC")

	finish("l11_dead_store")
}
