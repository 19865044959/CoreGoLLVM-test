// L15: 扩展名大小写与目录边界。只有精确小写 `.go` 结尾的普通文件参与构建：
// 大写/混合大小写、多重点号、无扩展名的形似文件必须被忽略；名为 inner.go
// 的目录也不能被递归当作文件编译。
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
	p := &payloadNode{id: 88, name: "ext-case-dir"}

	ck(impl() == "real", fmt.Sprintf("regular implementation got=%q", impl()))
	ck(regularConst == 17, fmt.Sprintf("regular constant got=%d", regularConst))
	ck(len(tokens) == 1, fmt.Sprintf("lookalike files compiled: tokens=%v", tokens))
	if len(tokens) == 1 {
		ck(tokens[0] == "real", fmt.Sprintf("only regular init token got=%q", tokens[0]))
	}

	gcStress()
	ck(impl() == "real" && regularConst == 17, "regular values after GC")
	ck(len(tokens) == 1 && tokens[0] == "real", "lookalike files absent after GC")
	ck(p.id == 88 && p.name == "ext-case-dir", "payload after GC")
	finish("l15_ext_case_and_dir")
}
