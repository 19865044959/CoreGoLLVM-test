// L13: map 扩容/删除/重插路径上的指针值。
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

//go:noinline
func build(n int) map[int]*node {
	m := make(map[int]*node, 8)
	for i := 0; i < n; i++ {
		m[i] = &node{v: int64(i), name: "m"}
	}
	gcStress()
	return m
}

func main() {
	m := build(1000)
	gcStress()
	for i := 0; i < 1000; i++ {
		if m[i] == nil || m[i].v != int64(i) {
			ck(false, "map value corrupted after build")
			break
		}
	}

	for i := 0; i < 1000; i += 2 {
		delete(m, i)
	}
	gcStress()
	ck(len(m) == 500, "map len after delete")
	if _, ok := m[0]; ok {
		ck(false, "deleted key resurrected")
	}
	ck(m[1] != nil && m[1].v == 1, "odd key after delete+GC")

	for i := 0; i < 1000; i += 2 {
		m[i] = &node{v: int64(i) + 100000, name: "m2"}
	}
	gcStress()
	ck(len(m) == 1000, "map len after reinsert")
	ck(m[0] != nil && m[0].v == 100000 && m[998].v == 100998, "reinserted values")
	ck(m[1] != nil && m[1].v == 1, "old value after reinsert")

	for i := 1000; i < 3000; i++ {
		m[i] = &node{v: int64(i), name: "m3"}
	}
	gcStress()
	ck(len(m) == 3000 && m[2999] != nil && m[2999].v == 2999, "map growth")

	var sum int64
	for _, p := range m {
		if p == nil {
			ck(false, "nil map value during iteration")
			break
		}
		sum += p.v % 1000
	}
	ck(sum == 1498500, "map iteration sum")

	runtime.KeepAlive(m)
	finish("l13_map_grow_delete_gc")
}
