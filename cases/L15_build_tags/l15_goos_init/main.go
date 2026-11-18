// L15: GOOS 专属 init。每个平台文件在 init 里写入自己的标记，只有当前 GOOS
// 对应的文件会执行；通用 init 与平台 init 都必须在 main 之前完成。
package main

import (
	"fmt"
	"os"
	"runtime"
	"sync/atomic"
)

type initNode struct {
	v    int64
	name string
}

var (
	failed        int32
	gcSink        [][]byte
	commonInit    bool
	platformInit  string
	platformInits []string
	initPayload   *initNode
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
	switch runtime.GOOS {
	case "linux":
		want = "linux"
	case "windows":
		want = "windows"
	case "darwin":
		want = "darwin"
	}
	ck(commonInit, "common init ran before main")
	ck(platformInit == want, "GOOS-specific init selected")
	ck(len(platformInits) == 1 && platformInits[0] == want, "exactly one GOOS init ran")
	ck(initPayload != nil && initPayload.v == 11 && initPayload.name == "from-init", "payload created in init")
	gcStress()
	ck(initPayload != nil && initPayload.v == 11 && initPayload.name == "from-init", "init payload after GC")
	finish("l15_goos_init")
}
