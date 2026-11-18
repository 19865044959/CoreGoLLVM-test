// L09: C 结构体指针数组（node_t **）：C 侧 calloc 指针数组、逐节点
// malloc 并填充；Go 用 unsafe.Slice 得到 []*C.node_t，直接读写字段、
// 跨多轮 GC 复核，最后交还 C 统一 free。C 不保留任何 Go 指针。
package main

/*
#include <stdint.h>
#include "ptrarray.h"
*/
import "C"

import (
	"fmt"
	"os"
	"runtime"
	"sync/atomic"
	"unsafe"
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
	const n = 48
	var count C.int32_t
	pp := C.c_nodes_new(C.int32_t(n), &count)
	ck(pp != nil, "C node pointer array allocated")
	if pp == nil {
		finish("l09_cgo_struct_ptr_array")
		return
	}
	ck(int(count) == n, "C node count")

	nodes := unsafe.Slice(pp, int(count))
	var wantSum int64
	for i, p := range nodes {
		ck(p != nil, fmt.Sprintf("node[%d] non-nil", i))
		ck(int32(p.id) == int32(i), fmt.Sprintf("node[%d].id", i))
		ck(int32(p.weight) == int32((i*7)%13), fmt.Sprintf("node[%d].weight", i))
		ck(int64(p.value) == int64(i)*1000+7, fmt.Sprintf("node[%d].value", i))
		for j := 0; j < 7; j++ {
			ck(byte(p.tag[j]) == byte('a'+(i+j)%26), fmt.Sprintf("node[%d].tag[%d]", i, j))
		}
		ck(byte(p.tag[7]) == 0, fmt.Sprintf("node[%d].tag NUL", i))
		wantSum += int64(i) + int64((i*7)%13) + int64(i)*1000 + 7
	}
	ck(int64(C.c_nodes_sum(pp, count)) == wantSum, "C sum after init")

	// Go 通过指针数组原地修改，C 立即观察到；同时验证直接写 struct 字段。
	for i := 0; i < n; i += 5 {
		C.c_node_add_value(nodes[i], C.int64_t(i))
		wantSum += int64(i)
	}
	nodes[3].weight = 99
	wantSum += 99 - int64((3*7)%13)
	old := int64(nodes[7].value)
	nodes[7].value = C.int64_t(old + 500)
	wantSum += 500

	// 修改后跨多轮 GC：数组与节点指针都由 C 持有，Go 侧只保存指针。
	for round := 0; round < 12; round++ {
		gcStress()
		ck(len(nodes) == n, fmt.Sprintf("pointer array len round=%d", round))
		for i := 0; i < n; i += 7 {
			ck(nodes[i] != nil, fmt.Sprintf("node ptr round=%d i=%d", round, i))
			ck(int32(nodes[i].id) == int32(i), fmt.Sprintf("node id round=%d i=%d", round, i))
			ck(byte(nodes[i].tag[0]) == byte('a'+i%26), fmt.Sprintf("node tag round=%d i=%d", round, i))
		}
		ck(int64(C.c_nodes_sum(pp, count)) == wantSum, fmt.Sprintf("C sum round=%d", round))
		ck(int32(nodes[3].weight) == 99, fmt.Sprintf("weight round=%d", round))
		ck(int64(nodes[7].value) == old+500, fmt.Sprintf("direct field write round=%d", round))
		runtime.KeepAlive(nodes)
	}

	// 再分配一组小数组，确认重复分配/释放路径正确。
	var count2 C.int32_t
	pp2 := C.c_nodes_new(4, &count2)
	ck(pp2 != nil && int(count2) == 4, "second pointer array")
	if pp2 != nil {
		nodes2 := unsafe.Slice(pp2, int(count2))
		for i, p := range nodes2 {
			ck(p != nil && int32(p.id) == int32(i), fmt.Sprintf("second node[%d]", i))
		}
		C.c_nodes_free(pp2, count2)
	}

	C.c_nodes_free(pp, count)
	C.c_nodes_free(nil, 0) // free(NULL) 语义安全
	finish("l09_cgo_struct_ptr_array")
}
