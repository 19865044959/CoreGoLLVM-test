// L15: 下划线/点号约束。以 `_` 或 `.` 开头的 .go 文件必须被 go build 完全忽略，
// `_test.go` 也不参与普通构建。目录里放置了语法错误文件、重复的 impl 定义和
// 只登记 token 的文件：构建成功本身证明这些文件未参与编译，token 集合再兜底
// 检测"能编译但不该出现"的文件。
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

func recordToken(s string) { tokens = append(tokens, s) }

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
	p := &payloadNode{id: 81, name: "underscore"}

	ck(impl() == "real", "regular file implementation selected")
	ck(regularConst == 7, "regular file constant compiled")
	ck(regularMarker() == "regular", "regular file function compiled")
	ck(len(tokens) == 1, fmt.Sprintf("only regular init ran, got %d tokens", len(tokens)))
	if len(tokens) == 1 {
		ck(tokens[0] == "real", "regular init token")
	}

	gcStress()
	ck(impl() == "real" && regularConst == 7 && p.id == 81 && p.name == "underscore", "values after GC")
	ck(len(tokens) == 1 && tokens[0] == "real", "ignored files still absent after GC")
	finish("l15_underscore_ignored")
}
