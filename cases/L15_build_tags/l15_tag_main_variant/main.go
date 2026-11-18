// L15: main 函数本身由构建标签选择。三个互斥文件各定义一个 main，链接期
// 必须恰好选中一个；如果选择错误，入口传入的平台/偏置与 runtime.GOOS 不符。
// 本文件不含 main，只放共享校验逻辑与 GC 压力。
package main

import (
	"fmt"
	"os"
	"runtime"
	"sync/atomic"
)

type payloadNode struct {
	platform string
	bias     int64
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

func runMain(platform string, bias int64) {
	wantPlatform := "other"
	wantBias := int64(3)
	switch runtime.GOOS {
	case "linux":
		wantPlatform = "linux"
		wantBias = 7
	case "windows":
		wantPlatform = "windows"
		wantBias = 5
	}

	ck(platform == wantPlatform, fmt.Sprintf("tag-selected main platform got=%q want=%q (GOOS=%s)", platform, wantPlatform, runtime.GOOS))
	ck(bias == wantBias, fmt.Sprintf("tag-selected main bias got=%d want=%d", bias, wantBias))

	p := &payloadNode{platform: platform, bias: bias}
	gcStress()
	ck(p.platform == wantPlatform && p.bias == wantBias, "entry payload after GC")
	ck(platform == wantPlatform, "tag-selected main after GC")
	finish("l15_tag_main_variant")
}
