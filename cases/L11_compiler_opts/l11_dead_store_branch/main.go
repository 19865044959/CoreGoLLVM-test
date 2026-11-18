// L11: 跨分支死存储——if/switch/循环 phi 上被后续赋值完全覆盖的存储可以
// 消除，但 RHS 副作用、仅部分路径覆盖的存储、别名可见的最终值必须保留；
// 死存储 RHS 的 panic 不能被消除。
package main

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"sync/atomic"
)

var (
	failed  int32
	gcSink  [][]byte
	seed    uint64 = 1
	dbTrace []int
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

func dbS(id int) int64 {
	dbTrace = append(dbTrace, id)
	return int64(id * 10)
}

func dbTraceEq(want ...int) bool {
	if len(dbTrace) != len(want) {
		return false
	}
	for i := range want {
		if dbTrace[i] != want[i] {
			return false
		}
	}
	return true
}

func dbKilledBoth(cond bool) int64 {
	x := dbS(1)
	if cond {
		x = dbS(2)
	} else {
		x = dbS(3)
	}
	x = dbS(4)
	return x
}

func dbOnePath(cond bool) int64 {
	x := int64(7)
	if cond {
		x = dbS(5)
		x = 11
	}
	return x
}

func dbSwitchSide(sel int) int64 {
	var x int64
	switch sel {
	case 0:
		x = dbS(20)
	case 1:
		x = dbS(21)
	default:
		x = dbS(22)
	}
	x = 50
	return x
}

func dbPhiLoop(n int) (int64, int64) {
	var sum int64
	x := int64(-1)
	for i := 0; i < n; i++ {
		x = int64(i)
		x = int64(i * 2)
		sum += x
	}
	return x, sum
}

func dbLoopDead(n int) int64 {
	last := int64(0)
	for i := 0; i < n; i++ {
		last = int64(i)
		last = int64(i) + 5
		last = int64(i) + 10
	}
	return last
}

func dbStructWhole() (int64, string) {
	s := struct {
		a   int64
		tag string
	}{a: 1, tag: "x"}
	s = struct {
		a   int64
		tag string
	}{a: 2, tag: "y"}
	s.a = 3
	return s.a, s.tag
}

func dbBranchAlias(cond bool) int64 {
	s := struct{ a int64 }{1}
	p := &s.a
	if cond {
		s.a = 2
	} else {
		s.a = 3
	}
	s.a = 4
	return *p
}

func dbArrayDouble() int64 {
	var a [4]int64
	for i := 0; i < 4; i++ {
		a[i] = int64(i)
	}
	for i := 0; i < 4; i++ {
		a[i] = int64(i * i)
	}
	return a[0]*1000 + a[1]*100 + a[2]*10 + a[3]
}

func dbMustPanic() int64 { panic("dead-store-rhs") }

func dbPanicRHS() (out int64) {
	defer func() {
		if r := recover(); r != nil {
			gcStress()
			out = 99
		}
	}()
	x := int64(1)
	x = dbMustPanic()
	x = 2
	return x
}

func dbSwitchAllDead(sel int) int64 {
	x := int64(100)
	switch sel {
	case 0:
		x = 1
	case 1:
		x = 2
	default:
		x = 3
	}
	x = int64(sel + 10)
	return x
}

func dbSelfOverwrite(n int) int64 {
	x := int64(n)
	x = x * 2
	x = int64(n * 3)
	return x
}

func main() {
	seed = loadSeed()
	if seed == 0 {
		seed = 1
	}

	dbTrace = dbTrace[:0]
	ck(dbKilledBoth(true) == 40, "killed branch value true")
	ck(dbTraceEq(1, 2, 4), "killed branch side effects true")
	dbTrace = dbTrace[:0]
	ck(dbKilledBoth(false) == 40, "killed branch value false")
	ck(dbTraceEq(1, 3, 4), "killed branch side effects false")

	dbTrace = dbTrace[:0]
	ck(dbOnePath(true) == 11, "one-path dead store")
	ck(dbTraceEq(5), "one-path dead store side effect")
	dbTrace = dbTrace[:0]
	ck(dbOnePath(false) == 7, "live store preserved")
	ck(dbTraceEq(), "live path no side effect")

	dbTrace = dbTrace[:0]
	ck(dbSwitchSide(0) == 50 && dbTraceEq(20), "switch dead store side effect 0")
	dbTrace = dbTrace[:0]
	ck(dbSwitchSide(1) == 50 && dbTraceEq(21), "switch dead store side effect 1")
	dbTrace = dbTrace[:0]
	ck(dbSwitchSide(9) == 50 && dbTraceEq(22), "switch dead store side effect default")

	n := 32 + int(seed%32)
	last, sum := dbPhiLoop(n)
	ck(last == int64(2*(n-1)), "loop phi final store")
	ck(sum == int64(n*(n-1)), "loop phi accumulated dead stores")
	ck(dbLoopDead(n) == int64(n-1+10), "loop overwritten stores")

	sv, stag := dbStructWhole()
	ck(sv == 3 && stag == "y", "whole struct overwrite")
	ck(dbBranchAlias(true) == 4, "branch alias final store true")
	ck(dbBranchAlias(false) == 4, "branch alias final store false")
	ck(dbArrayDouble() == 149, "array double loop stores")

	ck(dbSwitchAllDead(0) == 10, "switch all-dead store 0")
	ck(dbSwitchAllDead(4) == 14, "switch all-dead store 4")
	ck(dbSelfOverwrite(n) == int64(n*3), "self overwrite chain")

	ck(dbPanicRHS() == 99, "dead store RHS panic preserved")

	dbTrace = dbTrace[:0]
	gcStress()
	ck(dbKilledBoth(true) == 40 && dbTraceEq(1, 2, 4), "killed branch after GC")
	dbTrace = dbTrace[:0]
	ck(dbOnePath(false) == 7 && dbTraceEq(), "live store after GC")
	ck(dbLoopDead(n) == int64(n-1+10), "loop stores after GC")
	sv2, stag2 := dbStructWhole()
	ck(sv2 == 3 && stag2 == "y", "struct stores after GC")

	finish("l11_dead_store_branch")
}
