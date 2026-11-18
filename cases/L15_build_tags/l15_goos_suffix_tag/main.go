// L15: 文件名后缀 GOOS 与构建标签的合取。where_linux.go 同时受文件名 _linux 与
// tagFast 约束；默认构建必须排除它而选择 where_linux_base.go，从而检验后缀约束
// 与 tag 约束是 AND 关系而非互相覆盖。
package main

import (
	"fmt"
	"os"
	"runtime"
	"sync/atomic"
)

type suffixNode struct {
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
	want := "other"
	if runtime.GOOS == "linux" {
		want = "linux-slow"
		if os.Getenv("EXPECT_FAST") == "1" {
			want = "linux-fast"
		}
	}

	p := &suffixNode{id: 61, name: "goos-suffix-tag"}
	ck(suffixImpl() == want, "GOOS filename suffix AND build tag selection")

	gcStress()
	ck(suffixImpl() == want && p.id == 61 && p.name == "goos-suffix-tag", "suffix selection after GC")
	finish("l15_goos_suffix_tag")
}
