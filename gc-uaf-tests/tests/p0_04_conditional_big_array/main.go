// P0-04 (报告风险 A3/A2): 条件分支/循环中的大指针数组与零初始化点。
package main

import (
	"fmt"
	"os"
	"runtime"
	"sync/atomic"
)

type node struct{ v int64 }

var (
	failed int32
	gcSink [][]byte
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

//go:noinline
func bigCond(seed int64) int64 {
	if seed&1 == 0 {
		var a [4096]*node
		for i := range a {
			a[i] = &node{v: int64(i) + seed}
		}
		gcStress()
		var s int64
		for _, p := range a {
			s += p.v
		}
		return s
	}
	// 部分初始化：其余槽位必须在 GC 扫描前是安全的 nil。
	var a [4096]*node
	a[0] = &node{v: 7}
	gcStress()
	return a[0].v
}

//go:noinline
func loopLifetime() int64 {
	var total int64
	for k := 0; k < 40; k++ {
		var a [16]*node
		a[0] = &node{v: int64(k)}
		a[15] = &node{v: int64(k * 10)}
		gcStress()
		total += a[0].v + a[15].v
	}
	return total
}

//go:noinline
func reuseSlots() int64 {
	var a [64]*node
	a[3] = &node{v: 300}
	gcStress()
	a[5] = &node{v: 500}
	gcStress()
	return a[3].v + a[5].v
}

func main() {
	base := int64(4095 * 4096 / 2) // 8386560
	for i := 0; i < 3; i++ {
		ck(bigCond(0) == base, "bigCond even")
		ck(bigCond(1) == 7, "bigCond odd")
		ck(bigCond(2) == base+4096*2, "bigCond seed2")
	}
	ck(loopLifetime() == 8580, "loop lifetime")
	ck(reuseSlots() == 800, "slot reuse across GCs")

	var total int64
	for k := 0; k < 10; k++ {
		if k%2 == 0 {
			var a [256]*node
			a[0] = &node{v: int64(k)}
			a[255] = &node{v: int64(k) + 1}
			gcStress()
			total += a[0].v + a[255].v
		}
	}
	ck(total == 45, "conditional local array")
	finish("p0_04_conditional_big_array")
}
