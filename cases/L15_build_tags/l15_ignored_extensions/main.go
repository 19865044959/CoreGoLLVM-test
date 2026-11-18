// L15: 只有扩展名精确为 .go 的文件参与编译。目录中放置了 .go.txt、.GO、
// .go~、.go.orig 等形似文件（内容含语法错误与重复符号）以及未被导入的子目录，
// 构建成功与 token 数唯一共同证明它们全部被排除。
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
	p := &payloadNode{id: 91, name: "ignored-extensions"}

	ck(impl() == "real", fmt.Sprintf("regular implementation got=%q", impl()))
	ck(regularConst == 17, fmt.Sprintf("regular constant got=%d", regularConst))
	ck(len(tokens) == 1, fmt.Sprintf("lookalike files compiled: tokens=%v", tokens))
	if len(tokens) == 1 {
		ck(tokens[0] == "real", fmt.Sprintf("only regular init token got=%q", tokens[0]))
	}

	gcStress()
	ck(impl() == "real" && regularConst == 17, "regular values after GC")
	ck(len(tokens) == 1 && tokens[0] == "real", "lookalike files absent after GC")
	ck(p.id == 91 && p.name == "ignored-extensions", "payload after GC")
	finish("l15_ignored_extensions")
}
