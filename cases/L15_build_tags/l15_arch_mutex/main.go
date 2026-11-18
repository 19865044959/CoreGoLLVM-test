// L15: 互斥 arch 文件。三个文件的约束 amd64||386 / arm64 / 其余 两两互斥且
// 并集覆盖所有 GOARCH；构建成功本身证明没有重复实现，init 标记再确认运行期
// 恰好只有一个 arch 文件被编译。
package main

import (
	"fmt"
	"os"
	"runtime"
	"sync/atomic"
)

type archNode struct {
	id   int64
	name string
}

var (
	failed    int32
	gcSink    [][]byte
	archInits []string
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
	switch runtime.GOARCH {
	case "amd64", "386":
		want = "x86"
	case "arm64":
		want = "arm64"
	}

	p := &archNode{id: 41, name: "arch-mutex"}
	ck(archMutImpl() == want, "mutually exclusive GOARCH file selected")
	ck(len(archInits) == 1, "exactly one arch file initialized")
	if len(archInits) == 1 {
		ck(archInits[0] == want, "arch init marker matches implementation")
	}

	gcStress()
	ck(archMutImpl() == want && p.id == 41 && p.name == "arch-mutex", "arch selection and payload after GC")
	finish("l15_arch_mutex")
}
