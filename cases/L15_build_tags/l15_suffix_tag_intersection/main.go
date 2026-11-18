// L15: 文件名后缀与 //go:build 是交集（AND），不是并集。目录中两个互斥文件
// 的后缀与约束故意矛盾，任何平台都不应编译它们；正常文件则要求两者同时成立。
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
	tokens []string
)

func mark(s string) { tokens = append(tokens, s) }

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
	wantImpl := "other"
	wantDual := "fallback"
	if runtime.GOOS == "linux" {
		wantImpl = "linux"
	}
	if runtime.GOOS == "linux" && runtime.GOARCH == "amd64" {
		wantDual = "suffix+tag:linux-amd64"
	}
	p := &payloadNode{id: 72, name: "suffix-tag-intersection"}

	ck(impl() == wantImpl, fmt.Sprintf("impl got=%q want=%q", impl(), wantImpl))
	ck(dualArc() == wantDual, fmt.Sprintf("dual arc got=%q want=%q", dualArc(), wantDual))
	ck(len(tokens) == 1, fmt.Sprintf("contradictory files compiled: tokens=%v", tokens))
	if len(tokens) == 1 {
		ck(tokens[0] == wantImpl, fmt.Sprintf("selected token got=%q want=%q", tokens[0], wantImpl))
	}

	gcStress()
	ck(impl() == wantImpl && dualArc() == wantDual, "tag-selected values after GC")
	ck(len(tokens) == 1 && tokens[0] == wantImpl, "no contradictory init after GC")
	ck(p.id == 72 && p.name == "suffix-tag-intersection", "payload after GC")
	finish("l15_suffix_tag_intersection")
}
