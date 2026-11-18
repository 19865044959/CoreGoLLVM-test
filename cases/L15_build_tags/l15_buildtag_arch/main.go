// L15: 文件名后缀（_amd64/_arm64/_other）按 GOARCH 选择实现。
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
	want := archImpl()
	switch runtime.GOARCH {
	case "amd64":
		ck(want == "amd64", "amd64 file suffix selected")
	case "arm64":
		ck(want == "arm64", "arm64 file suffix selected")
	default:
		ck(want == "other", "other arch file selected")
	}
	gcStress()
	ck(archImpl() == want, "arch impl after GC")
	finish("l15_buildtag_arch")
}
