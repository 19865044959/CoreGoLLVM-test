// L15: 构建范围边界。_test.go 与 tag 排除的文件在解析包体和 //go:embed
// 指令之前就被剔除：bomb_test.go 的语法错误、bomb_embed_*.go 的非法 embed
// 模式都不应影响 go build。构建成功与 init 计数共同证明排除发生在解析之前。
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
	p := &payloadNode{id: 58, name: "excluded-scope"}

	ck(baseImpl() == "base", fmt.Sprintf("base implementation got=%q", baseImpl()))
	ck(baseConst == 7, fmt.Sprintf("base constant got=%d", baseConst))
	ck(baseInits == 1, fmt.Sprintf("test/tag-excluded init ran: baseInits=%d", baseInits))

	gcStress()
	ck(baseImpl() == "base" && baseConst == 7 && baseInits == 1, "base state after GC")
	ck(p.id == 58 && p.name == "excluded-scope", "payload after GC")
	finish("l15_excluded_scope_boundary")
}
