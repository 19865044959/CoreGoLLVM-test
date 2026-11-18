// L15: 自定义 -tags 选择实现；默认构建走 !featureA 分支。
// 手动验证另一分支：go build -tags=featureA ... && EXPECT_TAG=on ./bin
package main

import (
	"fmt"
	"os"
	"runtime"
	"sync/atomic"
)

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
	want := os.Getenv("EXPECT_TAG")
	if want == "" {
		want = "off"
	}
	ck(featureImpl() == want, "custom build tag selected wrong implementation")
	gcStress()
	ck(featureImpl() == want, "feature impl after GC")
	finish("l15_buildtag_custom")
}
