// L15: module 内多包变量依赖。seed.Base 在包变量初始化阶段被 calc/report 逐层
// 引用，calc.Table 由 var 初始化调用函数构建；main 校验值链、初始化顺序与
// 跨包指针在 GC 后仍有效。
package main

import (
	"fmt"
	"os"
	"runtime"
	"sync/atomic"

	"l15_module_var_deps/calc"
	"l15_module_var_deps/report"
	"l15_module_var_deps/seed"
	"l15_module_var_deps/trace"
)

type varNode struct {
	id   int64
	name string
}

var (
	failed   int32
	gcSink   [][]byte
	mainBase = seed.Base + calc.Plus
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

func main() {
	p := &varNode{id: 71, name: "var-deps"}

	ck(seed.Base == 6, "seed package variable initialized")
	ck(calc.Doubled == 12, "calc variable depends on seed at init time")
	ck(calc.Plus == 7, "calc second variable depends on seed")
	ck(len(calc.Table) == 3 && calc.Table[0] == 6 && calc.Table[1] == 12 && calc.Table[2] == 7,
		"calc table built by var-init function")
	ck(report.Total == 25, "report variable depends on calc and seed")
	ck(mainBase == 13, "main variable depends on imported packages")
	ck(len(report.Items) == 2 && report.Items[0] == seed.Node && report.Items[1].ID == 3,
		"report item slice links seed node")

	seq := trace.Snapshot()
	ck(len(seq) == 5, "five package init markers")
	iTrace := indexOf(seq, "trace")
	iSeed := indexOf(seq, "seed")
	iCalc := indexOf(seq, "calc")
	iReport := indexOf(seq, "report")
	iMain := indexOf(seq, "main")
	ck(iTrace == 0, "trace initialized first")
	ck(iSeed > iTrace, "seed initializes after trace")
	ck(iCalc > iSeed, "calc var init after seed")
	ck(iReport > iCalc, "report var init after calc")
	ck(iMain > iReport, "main var init after report")

	gcStress()
	ck(seed.Node != nil && seed.Node.Next != nil && seed.Node.Next.Label == "linked", "seed node chain after GC")
	ck(report.Items[0] == seed.Node && report.Items[1].Label == "report", "report items after GC")
	ck(report.Total == 25 && mainBase == 13 && p.id == 71 && p.name == "var-deps", "derived values after GC")
	finish("l15_module_var_deps")
}
