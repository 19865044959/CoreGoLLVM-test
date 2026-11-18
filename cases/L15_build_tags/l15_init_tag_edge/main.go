// L15: 包间 init 顺序 + 构建标签选择的依赖边。mid 无条件依赖 base，另一条边
// 由 GOOS 选择：linux 走 za、其他平台走 zb。被选中边上的包必须先于 mid 完成
// init，top 依赖 mid，main 最后；未选中的包不进入依赖图。断言只依赖规范保证
// 的偏序（base/edge 与彼此的相对顺序不作要求）。
package main

import (
	"fmt"
	"os"
	"runtime"
	"sync/atomic"

	"l15_init_tag_edge/mid"
	"l15_init_tag_edge/top"
	"l15_init_tag_edge/trace"
)

type nodeViewer interface {
	ID() int64
	Label() string
}

var (
	failed int32
	gcSink [][]byte
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

func view(v any) (int64, string, bool) {
	nv, ok := v.(nodeViewer)
	if !ok {
		return 0, "", false
	}
	return nv.ID(), nv.Label(), true
}

func main() {
	pkgName, otherPkg := "za", "zb"
	edgeV, sum, nodeID := int64(11), int64(16), int64(11)
	if runtime.GOOS != "linux" {
		pkgName, otherPkg = "zb", "za"
		edgeV, sum, nodeID = 13, 18, 13
	}

	p := &payloadNode{id: 51, name: "init-tag-edge"}

	ck(midBase() == 5, "base package variable")
	ck(midEdge() == edgeV, "tag-selected edge package variable")
	ck(top.Sum == sum, "top variable depends on tag-selected edge")
	ck(edgeName() == pkgName, "tag-selected edge package identity")

	seq := trace.Snapshot()
	ck(len(seq) == 7, "exactly seven package init markers")
	for _, name := range []string{"trace", "base", pkgName, "edge-" + pkgName, "mid", "top", "main"} {
		ck(countOf(seq, name) == 1, "init marker "+name+" appears exactly once")
	}
	ck(countOf(seq, otherPkg) == 0, "non-selected edge package not initialized")
	ck(countOf(seq, "edge-"+otherPkg) == 0, "non-selected edge file not compiled")

	iTrace := indexOf(seq, "trace")
	iBase := indexOf(seq, "base")
	iEdge := indexOf(seq, pkgName)
	iEdgeMark := indexOf(seq, "edge-"+pkgName)
	iMid := indexOf(seq, "mid")
	iTop := indexOf(seq, "top")
	iMain := indexOf(seq, "main")

	ck(iTrace == 0, "trace package initializes first")
	ck(iBase > iTrace, "base initializes after trace")
	ck(iEdge > iTrace, "edge package initializes after trace")
	ck(iEdgeMark > iEdge, "edge init marker after edge package")
	ck(iMid > iBase, "mid variable init after base")
	ck(iMid > iEdge, "mid variable init after tag-selected edge")
	ck(iTop > iMid, "top initialized after mid")
	ck(iMain > iTop, "main initialized after top")

	id, label, ok := view(edgeNodeAny())
	ck(ok && id == nodeID && label == pkgName, "tag-selected edge node view")

	gcStress()
	id2, label2, ok2 := view(edgeNodeAny())
	ck(ok2 && id2 == nodeID && label2 == pkgName, "edge node view after GC")
	ck(p.id == 51 && p.name == "init-tag-edge", "payload after GC")
	finish("l15_init_tag_edge")
}

type payloadNode struct {
	id   int64
	name string
}

func midBase() int64 { return mid.Base }

func midEdge() int64 { return mid.Edge }

func edgeName() string { return mid.EdgeName() }

func edgeNodeAny() any { return mid.EdgeNodeAny() }
