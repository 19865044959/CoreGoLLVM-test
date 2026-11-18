// 用例模板：复制到 cases/<dim>/<case>/main.go 后修改。
// 约定：
//   - 自校验，最后一行打印 "ALL PASS " + 用例目录名；
//   - 输出确定，不打印地址/时间/随机数；
//   - 用 gcStress 把 GC 放在关键位置；
//   - 需要随机路径时读 UAF_SEED。
package main

import (
	"fmt"
	"os"
	"runtime"
	"sync/atomic"
)

type node struct {
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
	p := &node{v: 42, name: "example"}
	gcStress()
	ck(p.v == 42 && p.name == "example", "pointer survives GC")

	finish("lXX_example_case")
}
