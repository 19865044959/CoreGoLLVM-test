// L15: 构建标签选择的 const。tagBase/tagLabel/tagMask 由 tagMath 选择；tagBase
// 还用作编译期数组长度，因此实现值错误会直接表现为编译失败或运行期长度不符。
package main

import (
	"fmt"
	"os"
	"runtime"
	"sync/atomic"
)

type constNode struct {
	id   int64
	name string
}

var (
	failed int32
	gcSink [][]byte
	table  [tagBase * 4]int
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

func pick(v int) string {
	switch v {
	case tagBase:
		return "base"
	case tagBase + 1:
		return "next"
	case tagMask:
		return "mask"
	default:
		return "other"
	}
}

func main() {
	wantLabel := os.Getenv("EXPECT_CONST")
	if wantLabel == "" {
		wantLabel = "base"
	}
	wantBase := 2
	if wantLabel == "math" {
		wantBase = 5
	}

	ck(tagBase == wantBase, "build tag selects integer constant")
	ck(tagLabel == wantLabel, "build tag selects string constant")
	ck(tagMask == wantBase*3+1, "derived constant follows selected base")
	ck(len(table) == wantBase*4, "array length uses tag-selected constant")

	for i := range table {
		table[i] = i * tagBase
	}
	ck(table[1] == wantBase && table[len(table)-1] == (len(table)-1)*wantBase, "array contents use selected constant")

	ck(pick(tagBase) == "base", "switch case uses tag-selected constant")
	ck(pick(tagBase+1) == "next", "switch case uses constant expression")

	p := &constNode{id: int64(wantBase), name: "tag-const"}
	gcStress()
	ck(p.id == int64(wantBase) && p.name == "tag-const" && tagMask == wantBase*3+1, "constant and payload after GC")
	finish("l15_tag_const_select")
}
