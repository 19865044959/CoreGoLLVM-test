// L15: 多包 + 构建标签改变 import 图。hub 的 linux 版导入 leaf/lnx，
// 其他平台版导入 leaf/oth；被导入的 leaf 包 init 先于 hub，未选中的 leaf
// 根本不进入 import 图（trace 中没有它的 init 标记）。运行期同时校验跨包
// 值、init 顺序约束与接口视图下的指针在 GC 后仍有效。
package main

import (
	"fmt"
	"os"
	"runtime"
	"sync/atomic"

	"l15_tag_import_graph/hub"
	"l15_tag_import_graph/trace"
)

type nodeViewer interface {
	ID() int64
	Label() string
	NextLabel() string
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

func inspect(v any) (int64, string, string, bool) {
	nv, ok := v.(nodeViewer)
	if !ok {
		return 0, "", "", false
	}
	return nv.ID(), nv.Label(), nv.NextLabel(), true
}

func main() {
	leaf, other := "lnx", "oth"
	scale, bias, nodeID := int64(2), int64(7), int64(11)
	nextLabel := "lnx-next"
	if runtime.GOOS != "linux" {
		leaf, other = "oth", "lnx"
		scale, bias, nodeID = 5, 9, 21
		nextLabel = "oth-next"
	}

	p := &payloadNode{id: 31, name: "tag-import-graph"}

	ck(hub.Platform == leaf, "tag-selected leaf package exported name")
	ck(hub.Scale == scale, "tag-selected leaf package constant")
	ck(hub.Bias == bias, "tag-selected route bias")
	ck(hub.Combine(10) == 10*scale+bias, "cross-package computation through tagged import")

	id, label, next, ok := inspect(hub.NodeRef())
	ck(ok, "tag-selected node implements expected view")
	ck(id == nodeID && label == leaf && next == nextLabel, "node identity from selected leaf")

	seq := trace.Snapshot()
	ck(len(seq) == 5, "exactly five init markers")
	for _, name := range []string{"trace", leaf, "route-" + leaf, "hub", "main"} {
		ck(countOf(seq, name) == 1, "init marker "+name+" appears exactly once")
	}
	ck(countOf(seq, other) == 0, "non-selected leaf package not imported")
	ck(countOf(seq, "route-"+other) == 0, "non-selected route file not compiled")
	ck(indexOf(seq, "trace") == 0, "trace package initializes first")
	ck(indexOf(seq, leaf) > indexOf(seq, "trace"), "leaf initializes after trace")
	ck(indexOf(seq, "hub") > indexOf(seq, leaf), "hub var init after selected leaf")
	ck(indexOf(seq, "route-"+leaf) > indexOf(seq, leaf), "route init after selected leaf")
	ck(indexOf(seq, "main") > indexOf(seq, "hub") && indexOf(seq, "main") > indexOf(seq, "route-"+leaf),
		"main initializes after hub package")

	gcStress()
	id2, label2, next2, ok2 := inspect(hub.NodeRef())
	ck(ok2 && id2 == nodeID && label2 == leaf && next2 == nextLabel, "node view after GC")
	ck(p.id == 31 && p.name == "tag-import-graph", "payload after GC")
	finish("l15_tag_import_graph")
}

type payloadNode struct {
	id   int64
	name string
}
