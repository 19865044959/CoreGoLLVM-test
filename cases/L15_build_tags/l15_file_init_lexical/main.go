// L15: 同一包内跨文件 init 的词典序。go 命令按文件名字典序把文件交给编译器，
// init 按文件顺序、文件内源码顺序执行；tag 文件只在它的文件名槽位插入。
package main

import (
	"fmt"
	"os"
	"runtime"
	"strings"
	"sync/atomic"
)

type initNode struct {
	v    int64
	name string
}

var (
	failed  int32
	gcSink  [][]byte
	seq     []string
	payload *initNode
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
	platform := "linux"
	if runtime.GOOS != "linux" {
		platform = "other"
	}
	want := "a1,a2,b-" + platform + ",m,z"
	got := strings.Join(seq, ",")

	ck(got == want, fmt.Sprintf("init order got=%q want=%q", got, want))
	ck(payload != nil && payload.v == 21 && payload.name == "tagged-init", "tag-selected init payload")

	gcStress()
	ck(payload != nil && payload.v == 21 && payload.name == "tagged-init", "init payload after GC")
	ck(strings.Join(seq, ",") == want, "init trace unchanged after GC")
	finish("l15_file_init_lexical")
}
