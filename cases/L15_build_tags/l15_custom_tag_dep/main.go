// L15: 自定义 tag 的依赖文件。tagFeat 打开时 featImpl 依赖同一 tag 下的
// helper_on.go（函数 + 常量 + init 标记）；默认构建必须完全排除该依赖文件。
// 手动验证：go build -tags=tagFeat && EXPECT_FEAT=on。
package main

import (
	"fmt"
	"os"
	"runtime"
	"sync/atomic"
)

type depNode struct {
	v    int64
	name string
}

var (
	failed     int32
	gcSink     [][]byte
	helperInit bool
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
	p := &depNode{v: 42, name: "tag-dep"}
	want := "base"
	wantHelper := false
	if os.Getenv("EXPECT_FEAT") == "on" {
		want = "feat+linked:gated"
		wantHelper = true
	}
	ck(featImpl() == want, "custom tag selects implementation and its dependency")
	ck(helperInit == wantHelper, "tag-gated dependency init matches build")
	gcStress()
	ck(featImpl() == want && p.v == 42 && p.name == "tag-dep", "tag impl and payload after GC")
	finish("l15_custom_tag_dep")
}
