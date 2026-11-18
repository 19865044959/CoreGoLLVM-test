// L15: 包间 init 顺序。trace <- base <- {left,right} <- main 的导入关系下，
// 依赖包必须先于依赖者完成 init；left/right 相互独立，只断言它们都在 base 之后、
// main 之前，不依赖其相对顺序。main 的包级变量再依赖这些跨包变量。
package main

import (
	"fmt"
	"os"
	"runtime"
	"sync/atomic"

	"l15_pkg_init_order/base"
	"l15_pkg_init_order/left"
	"l15_pkg_init_order/right"
	"l15_pkg_init_order/trace"
)

type initNode struct {
	id   int64
	name string
}

var (
	failed    int32
	gcSink    [][]byte
	mainValue = left.Derived + right.Derived + base.Value
)

func init() { trace.Mark("main") }

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

func indexOf(seq []string, s string) int {
	for i, v := range seq {
		if v == s {
			return i
		}
	}
	return -1
}

func countOf(seq []string, s string) int {
	n := 0
	for _, v := range seq {
		if v == s {
			n++
		}
	}
	return n
}

func main() {
	p := &initNode{id: 21, name: "pkg-init-order"}
	seq := trace.Snapshot()

	ck(len(seq) == 5, "exactly five package init markers")
	for _, name := range []string{"trace", "base", "left", "right", "main"} {
		ck(countOf(seq, name) == 1, "init marker "+name+" appears exactly once")
	}
	iTrace := indexOf(seq, "trace")
	iBase := indexOf(seq, "base")
	iLeft := indexOf(seq, "left")
	iRight := indexOf(seq, "right")
	iMain := indexOf(seq, "main")

	ck(iTrace == 0, "trace package initializes first")
	ck(iBase > iTrace, "base initialized after its trace dependency")
	ck(iLeft > iBase, "left initialized after base")
	ck(iRight > iBase, "right initialized after base")
	ck(iMain > iLeft && iMain > iRight && iMain > iBase, "main initialized after all imported packages")

	ck(base.Value == 21, "base package variable initialized")
	ck(left.Derived == 42, "left package variable depends on base")
	ck(right.Derived == 22, "right package variable depends on base")
	ck(mainValue == 42+22+21, "main package variable depends on imported packages")

	gcStress()
	ck(mainValue == 85 && base.Value == 21 && p.id == 21 && p.name == "pkg-init-order", "values after GC")
	finish("l15_pkg_init_order")
}
