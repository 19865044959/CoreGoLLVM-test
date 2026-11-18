// L12: 跨文件包级变量依赖链必须按依赖序初始化（不依赖文件顺序）。
package main

import (
	"fmt"
	"os"
	"runtime"
	"sync/atomic"
)

var (
	failed int32
	gcSink [][]byte

	initObserved int
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

func scale(v int) int { return v * 2 }

// init 在所有包级变量（含跨文件依赖链）初始化完成后运行，
// 因此这里能一次性观察到整条链的最终值。
func init() {
	initObserved = a + b + c + derived1 + derived2 + fnInit
}

func main() {
	ck(c == 3, "root c == 3")
	ck(base == 100, "root base == 100")
	ck(b == c*2 && b == 6, "b depends on c from another file")
	ck(a == b+1 && a == 7, "a depends on b from another file")
	ck(derived1 == base+1 && derived1 == 101, "derived1 = base+1")
	ck(derived2 == derived1*2+c && derived2 == 205, "derived2 = derived1*2+c")
	ck(scaled == 202, "initializer through function call")
	ck(fnInit == scale(derived1)+b && fnInit == 208, "function-call initializer chain")
	ck(table[a] == "seven", "map initializer with dependent key")
	ck(table[derived1] == "hundred-one", "map initializer second key")

	// init 函数看到的必须是最终值。
	ck(initObserved == 530, "init observed final dependency values")

	gcStress()
	ck(a == 7 && b == 6 && c == 3, "dependency chain survives GC")
	ck(derived2 == 205 && fnInit == 208, "derived values survive GC")
	ck(table[a] == "seven" && table[derived1] == "hundred-one", "map survives GC")

	// 值拷贝发生在初始化期：之后修改源 struct 不应改变副本。
	seedNode.v = 999
	seedNode.label = "changed"
	ck(seedCopy.v == 100 && seedCopy.label == "seed", "struct copy kept init-time value")
	ck(seedPtr == seedAlias, "pointer alias kept identity")
	ck(seedPtr.v == 999 && seedPtr.label == "changed", "pointer sees mutation")

	gcStress()
	ck(seedCopy.v == 100 && seedCopy.label == "seed", "copy after GC")
	ck(seedPtr == seedAlias && seedAlias.v == 999, "alias after GC")
	ck(initObserved == 530, "init-observed value after GC")

	finish("l12_var_dependency_order")
}
