// L08: 手写 amd64 ABI0 汇编函数的整数运算、指针透传与字段读取，跨 GC 校验。
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

func addAsm(a, b int64) int64
func keepPtr(p *node) *node
func readV(p *node) int64

func main() {
	for i := int64(0); i < 200; i++ {
		ck(addAsm(i, 2) == i+2, "asm add")
		ck(addAsm(-i, i) == 0, "asm add inverse")
	}

	p := &node{v: 42, name: "asm"}
	q := keepPtr(p)
	gcStress()
	ck(q == p && q.v == 42 && q.name == "asm", "asm pointer passthrough after GC")

	var sum int64
	for i := 0; i < 50; i++ {
		sum += readV(p)
	}
	ck(sum == 50*42, "asm field read loop")

	gcStress()
	ck(readV(p) == 42, "asm field read after GC")
	finish("l08_asm_abi0_amd64")
}
