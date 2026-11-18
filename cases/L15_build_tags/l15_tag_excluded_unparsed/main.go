// L15: 被 //go:build 排除的文件不参与编译，也不解析 import 图与包体。
// 目录中放置了语法错误、缺失 import、重复符号和不同 package 名的"炸弹"文件，
// 它们全部由永不成立的 tag 排除；构建成功本身证明排除发生在解析之前。
package main

import (
	"fmt"
	"os"
	"runtime"
	"sync/atomic"
)

type payloadNode struct {
	id   int64
	name string
}

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

func main() {
	p := &payloadNode{id: 64, name: "excluded-unparsed"}

	ck(baseImpl() == "base", fmt.Sprintf("base implementation got=%q", baseImpl()))
	ck(baseConst == 7, fmt.Sprintf("base constant got=%d", baseConst))
	ck(baseInits == 1, fmt.Sprintf("excluded init ran: baseInits=%d", baseInits))

	gcStress()
	ck(baseImpl() == "base" && baseConst == 7 && baseInits == 1, "base state after GC")
	ck(p.id == 64 && p.name == "excluded-unparsed", "payload after GC")
	finish("l15_tag_excluded_unparsed")
}
