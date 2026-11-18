// L12: 函数调用式变量依赖 + memoization。多个包级变量初始化器调用同一个
// 带记忆的构造函数：显式参数引用建立规范保证的初始化顺序，重复 key 共享
// 同一对象。init 在全部变量初始化后捕获调用/分配计数；主程序验证共享指针、
// 闭包与方法值，并在 TORTURE_ROUNDS 放大的重复查找/GC 下确认不再分配。
package main

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"sync"
	"sync/atomic"
)

var (
	failed  int32
	gcSink  [][]byte
	seedVal int64
	seedStr string
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
		fmt.Printf("FAILED %s (%d failed checks) seed=%s\n", name, n, seedStr)
		os.Exit(1)
	}
	fmt.Println("ALL PASS " + name)
}

func readSeed() int64 {
	s := os.Getenv("UAF_SEED")
	if s == "" {
		return 1
	}
	v, err := strconv.ParseInt(s, 10, 64)
	if err != nil || v < 0 {
		return 1
	}
	return v
}

func tortureRounds() int {
	s := os.Getenv("TORTURE_ROUNDS")
	if s == "" {
		return 1
	}
	v, err := strconv.Atoi(s)
	if err != nil || v < 1 {
		return 1
	}
	if v > 16 {
		v = 16
	}
	return v
}

func main() {
	seedVal = readSeed()
	seedStr = strconv.FormatInt(seedVal, 10)
	rounds := tortureRounds()

	// Initialization-time results.
	ck(base != nil && base.seed == 11, "base constructed by function call")
	ck(cfgA == cfgA2, "same key memoized across two initializers")
	ck(cfgA != cfgB && alias == cfgA, "distinct keys and alias")
	ck(cfgA.key == "alpha" && cfgA.value == 511, "alpha value")
	ck(cfgB.key == "beta" && cfgB.value == 411, "beta value")
	ck(cfgA.base == base && cfgB.base == base, "explicit base dependency shared")
	ck(len(built) == 2 && built["alpha"] == cfgA && built["beta"] == cfgB,
		"memo table points at the initialized objects")
	ck(allocCounts["alpha"] == 1 && allocCounts["beta"] == 1,
		"constructor allocates once per key")
	ck(atomic.LoadInt32(&buildCalls) == 3, "three call-site evaluations")
	ck(initCalls == 3 && initAllocs == 2, "init observes final constructor counters")
	ck(adder(5) == 416, "closure captured cfgB value")
	ck(view == "alpha=511", "function-call initializer result")
	ck(boundView() == "alpha=511", "method value initializer")

	// Post-init repeated lookups must hit the memo without allocating.
	callsBefore := atomic.LoadInt32(&buildCalls)
	iters := 20000 * rounds
	for i := 0; i < iters; i++ {
		a := buildCfg("alpha", base)
		b := buildCfg("beta", base)
		if a != cfgA || b != cfgB || a.value != 511 || b.value != 411 {
			ck(false, "memo lookup returned a foreign object")
			break
		}
		if i%2048 == 0 {
			gcSink = append(gcSink[:0], make([]byte, 16384))
			runtime.GC()
		}
		_ = (int(seedVal) + i) % 7
	}
	gcSink = gcSink[:0]
	ck(atomic.LoadInt32(&buildCalls) == callsBefore+int32(2*iters),
		"every post-init call counted")
	ck(allocCounts["alpha"] == 1 && allocCounts["beta"] == 1,
		"post-init lookups never allocate")
	ck(len(built) == 2 && built["alpha"] == cfgA, "memo table unchanged")

	// Concurrent memoized readers + GC.
	var wg sync.WaitGroup
	var bad int32
	for w := 0; w < 4; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < 1000; i++ {
				a := buildCfg("alpha", base)
				b := buildCfg("beta", base)
				if a != cfgA || b != cfgB || a.base != base {
					atomic.AddInt32(&bad, 1)
					return
				}
				_ = (w + i) % 4
			}
		}(w)
	}
	for i := 0; i < 8; i++ {
		gcSink = append(gcSink, make([]byte, 16384))
		runtime.GC()
	}
	wg.Wait()
	ck(atomic.LoadInt32(&bad) == 0, "concurrent memo reads under GC")
	gcSink = gcSink[:0]

	gcStress()
	ck(cfgA == built["alpha"] && cfgB == built["beta"], "memo identities survive GC")
	ck(cfgA.base == base && alias == cfgA && cfgA2 == cfgA, "aliases survive GC")
	ck(adder(1) == 412 && boundView() == "alpha=511", "closures survive GC")
	ck(allocCounts["alpha"] == 1 && allocCounts["beta"] == 1, "no allocations after GC")

	finish("l12_var_init_call_memo")
}
