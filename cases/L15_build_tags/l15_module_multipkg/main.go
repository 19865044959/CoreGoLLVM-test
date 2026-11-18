// L15: 多包 module 跨包链接。main -> lib -> lib/inner 两层导入，子包内还有
// GOOS 构建标签；同时验证跨包 init 顺序（依赖包先就绪）与跨包指针在 GC 后有效。
package main

import (
	"fmt"
	"os"
	"runtime"
	"sync/atomic"

	"l15_module_multipkg/lib"
)

type moduleNode struct {
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
	p := &moduleNode{v: 66, name: "module"}
	ck(lib.InitOK, "transitive package initialized before lib init")

	rec := lib.Build(7, "multipkg")
	ck(rec != nil, "cross-package record allocated")
	ck(rec.ID == 7 && rec.Name == "multipkg", "cross-package fields")
	ck(rec.Weight == 7*3+1+100, "transitive package computation")

	wantPlatform := "other-lib"
	if runtime.GOOS == "linux" {
		wantPlatform = "linux-lib"
	}
	ck(lib.Platform() == wantPlatform, "build tag inside subpackage")

	gcStress()
	ck(rec.ID == 7 && rec.Name == "multipkg" && rec.Weight == 122, "record after GC")
	ck(p.v == 66 && p.name == "module", "main payload after GC")
	finish("l15_module_multipkg")
}
