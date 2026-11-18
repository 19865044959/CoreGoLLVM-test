// L15: 文件名后缀选择。where_<GOOS>.go 仅靠文件名后缀生效；arch_linux_amd64.go
// 验证 GOOS+GOARCH 双段后缀，其余平台回落到 arch_fallback.go 的约束。
package main

import (
	"fmt"
	"os"
	"runtime"
	"sync/atomic"
)

type fileNode struct {
	v    int64
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
	p := &fileNode{v: 55, name: "filename-goos"}
	wantOS := "other"
	switch runtime.GOOS {
	case "linux":
		wantOS = "linux"
	case "windows":
		wantOS = "windows"
	case "darwin":
		wantOS = "darwin"
	}
	wantArch := "fallback"
	if runtime.GOOS == "linux" && runtime.GOARCH == "amd64" {
		wantArch = "linux-amd64"
	}
	ck(osSuffixImpl() == wantOS, "GOOS filename suffix selection")
	ck(archSuffixImpl() == wantArch, "GOOS+GOARCH filename suffix selection")

	gcStress()
	ck(osSuffixImpl() == wantOS && archSuffixImpl() == wantArch, "suffix selection after GC")
	ck(p.v == 55 && p.name == "filename-goos", "payload after GC")
	finish("l15_filename_goos")
}
