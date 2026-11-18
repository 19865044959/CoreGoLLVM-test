// L12: 包级函数值图。方法值、方法表达式、闭包与普通函数值在包级变量
// 初始化阶段写入全局 map/变量；init 与后续变量初始化器调用它们。用例验证
// 函数值重定位、闭包捕获的指针在 GC 后仍有效、方法值绑定指针接收者语义，
// 并用 TORTURE_ROUNDS 放大派发与显式 GC。
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
		return 16
	}
	return v
}

func main() {
	seedVal = readSeed()
	seedStr = strconv.FormatInt(seedVal, 10)
	rounds := tortureRounds()

	ck(len(table) == 5, fmt.Sprintf("table length %d, want 5", len(table)))
	ck(len(exprs) == 2, fmt.Sprintf("exprs length %d, want 2", len(exprs)))
	ck(len(chain) == 1, "chain length")
	ck(initChecks == 1, "init side effect count")
	ck(sumProbe == 33, fmt.Sprintf("sumProbe %d, want 33", sumProbe))
	ck(methodProbe == 8, fmt.Sprintf("methodProbe %d, want 8", methodProbe))
	ck(initExprsProbe == 11, fmt.Sprintf("initExprsProbe %d, want 11", initExprsProbe))
	ck(initFuncType == "main.handler", "function value static type "+initFuncType)
	for _, k := range []string{"add", "mul", "add10", "state", "double"} {
		ck(table[k] != nil, "nil function value for "+k)
	}
	for _, k := range []string{"add", "mul"} {
		ck(exprs[k] != nil, "nil method expression for "+k)
	}

	ck(table["add"](5) == 12, "add handler before mutation")
	ck(table["mul"](3) == 21, "mul handler before mutation")
	ck(table["add10"](5) == 22, "closure add10 before mutation")
	ck(table["double"](5) == 10, "plain func value")
	ck(exprs["add"](&counter{base: 100}, 5) == 105, "method expression add")
	ck(exprs["mul"](&counter{base: 6}, 7) == 42, "method expression mul")
	ck(chain["plus3thenDouble"](4) == 14, "composed closure")
	ck(table["state"](5) == 5, "stateful closure first call")
	ck(table["state"](3) == 8, "stateful closure second call")
	ck(state.last == 3, "stateful closure captured state")
	ck(bumpFn() == 1, "bound method Bump first call")
	ck(bumpFn() == 2, "bound method Bump second call")

	// Method values bind the receiver pointer, so later field mutations are
	// visible through the stored function values.
	shared.base = 9
	ck(table["add"](1) == 10, "method value did not see receiver mutation")
	ck(table["mul"](2) == 18, "method value mul did not see receiver mutation")
	ck(exprs["add"](shared, 1) == 10, "method expression did not see receiver mutation")

	// TORTURE_ROUNDS scales single-threaded dispatch; expected results are
	// computed independently from the handler formulas.
	calls := 20000 * rounds
	if calls > 320000 {
		calls = 320000
	}
	start := int(seedVal % 4)
	var got, want int64
	keys := []string{"add", "mul", "add10", "double"}
	for i := 0; i < calls; i++ {
		x := (i*7 + start) % 1001
		sel := (i + start) % 4
		got += int64(table[keys[sel]](x))
		switch sel {
		case 0:
			want += int64(9 + x)
		case 1:
			want += int64(9 * x)
		case 2:
			want += int64(19 + x)
		default:
			want += int64(2 * x)
		}
	}
	ck(got == want, fmt.Sprintf("dispatch sum got %d want %d", got, want))

	// Concurrent read-only dispatch while main allocates garbage and GCs.
	var wg sync.WaitGroup
	var bad int32
	workers := 8
	itersPer := 100 * rounds
	if itersPer > 1600 {
		itersPer = 1600
	}
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < itersPer; i++ {
				x := (w*13 + i + start) % 1001
				sel := (w + i) % 4
				var expect int
				switch sel {
				case 0:
					expect = 9 + x
				case 1:
					expect = 9 * x
				case 2:
					expect = 19 + x
				default:
					expect = 2 * x
				}
				if table[keys[sel]](x) != expect {
					atomic.AddInt32(&bad, 1)
					return
				}
				if chain["plus3thenDouble"](x) != (x+3)*2 {
					atomic.AddInt32(&bad, 1)
					return
				}
			}
		}(w)
	}
	for r := 0; r < 4; r++ {
		for i := 0; i < 16; i++ {
			b := make([]byte, 8192)
			b[i%len(b)] = byte(r)
			gcSink = append(gcSink[:0], b)
		}
		runtime.GC()
	}
	wg.Wait()
	gcSink = gcSink[:0]
	ck(atomic.LoadInt32(&bad) == 0, "concurrent function value dispatch failed under GC")

	gcStress()
	ck(table["add"](1) == 10 && table["mul"](2) == 18, "function values broken after GC")
	ck(table["state"](0) == 8 && state.last == 0, "closure state broken after GC")
	ck(bumpFn() == 3, "bound method Bump after GC")
	ck(exprs["add"](shared, 1) == 10 && chain["plus3thenDouble"](5) == 16, "method expr/composed closure after GC")
	ck(len(table) == 5 && len(exprs) == 2 && len(chain) == 1, "function value tables changed after GC")

	finish("l12_global_func_values")
}
