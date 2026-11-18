// L08: 手写 amd64 汇编遍历 []*node 指针数组并读取首字段 v，
// 数组含 nil 槽位；跨 GC 校验指针数组与节点内容（含 string 字段）不被破坏。
package main

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
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

func sumNodeVAsm(pp **node, n int) int64

func build(n int) ([]*node, int64) {
	arr := make([]*node, n)
	var want int64
	for i := 0; i < n; i++ {
		if i%7 == 6 {
			continue // nil 槽位
		}
		v := int64(i*11 - 50)
		arr[i] = &node{v: v, name: "n" + strconv.Itoa(i)}
		want += v
	}
	return arr, want
}

func verify(arr []*node) {
	for i, p := range arr {
		if i%7 == 6 {
			ck(p == nil, fmt.Sprintf("nil slot %d", i))
			continue
		}
		ck(p != nil && p.v == int64(i*11-50) && p.name == "n"+strconv.Itoa(i),
			fmt.Sprintf("node content %d", i))
	}
}

func main() {
	for round := 0; round < 100; round++ {
		n := 64
		arr, want := build(n)
		got := sumNodeVAsm(&arr[0], n)
		ck(got == want, fmt.Sprintf("asm ptr-array sum round=%d", round))

		gcStress()
		verify(arr)
		ck(sumNodeVAsm(&arr[0], n) == want, fmt.Sprintf("asm ptr-array after GC round=%d", round))

		if round%10 == 0 {
			// 子数组起点偏移。
			sub := sumNodeVAsm(&arr[3], n-3)
			var subWant int64
			for i := 3; i < n; i++ {
				if arr[i] != nil {
					subWant += arr[i].v
				}
			}
			ck(sub == subWant, "asm ptr-array sub-slice")
		}
		runtime.KeepAlive(arr)
	}

	// 全 nil 与空输入。
	var nilArr [4]*node
	ck(sumNodeVAsm(&nilArr[0], 4) == 0, "all nil array")
	ck(sumNodeVAsm(nil, 0) == 0, "nil/0")
	finish("l08_asm_ptr_array")
}
