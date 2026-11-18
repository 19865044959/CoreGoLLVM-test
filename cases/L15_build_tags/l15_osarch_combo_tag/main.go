// L15: OS/arch 组合标签。多个文件分别由 "linux && arm64"、"windows && amd64"、
// "darwin && arm64"、"freebsd && amd64"、"(linux || freebsd) && 386" 选择，
// 其余平台由组合取反的兜底文件提供实现。期望值完全由运行时 GOOS/GOARCH 推导。
package main

import (
	"fmt"
	"os"
	"runtime"
	"sync/atomic"
)

type comboNode struct {
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

func expected() string {
	switch {
	case runtime.GOOS == "linux" && runtime.GOARCH == "arm64":
		return "linux-arm64"
	case runtime.GOOS == "windows" && runtime.GOARCH == "amd64":
		return "windows-amd64"
	case runtime.GOOS == "darwin" && runtime.GOARCH == "arm64":
		return "darwin-arm64"
	case runtime.GOOS == "freebsd" && runtime.GOARCH == "amd64":
		return "freebsd-amd64"
	case (runtime.GOOS == "linux" || runtime.GOOS == "freebsd") && runtime.GOARCH == "386":
		return "unix-386"
	default:
		return "other"
	}
}

func main() {
	p := &comboNode{id: 1, name: "osarch-combo"}
	want := expected()
	ck(osArchImpl() == want, "OS/arch combined build tag selection")

	gcStress()
	ck(osArchImpl() == want, "OS/arch selection after GC")
	ck(p.id == 1 && p.name == "osarch-combo", "payload after GC")
	finish("l15_osarch_combo_tag")
}
