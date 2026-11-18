// L15: //go:build 约束按 GOOS 选择实现文件。
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
	got := platformImpl()
	want := "other-file"
	if runtime.GOOS == "linux" {
		want = "linux-file"
	}
	ck(got == want, "GOOS build tag selected wrong file")
	gcStress()
	ck(platformImpl() == want, "platform impl after GC")
	ck(len(runtime.GOOS) > 0 && len(runtime.GOARCH) > 0, "runtime platform strings")
	finish("l15_buildtag_os")
}
