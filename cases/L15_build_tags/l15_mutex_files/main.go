// L15: 互斥文件约束。base/red/blue/both 四个文件的约束两两互斥且并集覆盖全部
// tagRed/tagBlue 组合；构建成功本身证明没有重复符号，运行期再用 init 标记确认
// 恰好只有一个实现文件被编译。EXPECT_IMPL 可手动验证其他分支。
package main

import (
	"fmt"
	"os"
	"runtime"
	"sync/atomic"
)

type mutexNode struct {
	v    int64
	name string
}

var (
	failed       int32
	gcSink       [][]byte
	variantInits []string
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
	want := os.Getenv("EXPECT_IMPL")
	if want == "" {
		want = "base"
	}
	p := &mutexNode{v: 31, name: "mutex-files"}
	ck(variantImpl() == want, "mutually exclusive implementation selected")
	ck(len(variantInits) == 1, "exactly one variant file initialized")
	if len(variantInits) == 1 {
		ck(variantInits[0] == want, "variant init marker matches implementation")
	}
	gcStress()
	ck(variantImpl() == want && p.v == 31 && p.name == "mutex-files", "variant and payload after GC")
	finish("l15_mutex_files")
}
