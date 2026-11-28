// Z09 init_1：大局部帧 + 帧中部栈增长。
//
// [4096]*node 局部数组（32KB，超过初始栈）+ map/slice 混合；填满后在函数中部调用
// noinline 深递归，强制 morestack——栈复制必须正确搬运 init 帧中的 4096 个指针槽。
// 增长（及递归内 GC）之后逐项校验 canary，并把结果发布给 main 长期复查。
package main

import "runtime"

const (
	init1N         = 4096
	init1Depth     = 512
	init1DepthSeed = int64(424242)
)

var (
	init1Nodes    []*initNode
	init1Checksum int64
	init1MapLen   int
	init1SliceLen int
	init1Stage    int
	initOrder     []int
)

func init() {
	init1Runs++

	// 32KB 局部帧：进入函数即触发一次 morestack。
	var a [init1N]*initNode
	m := make(map[int]*initNode, 128)
	s := make([]*initNode, 0, 128)
	for i := 0; i < init1N; i++ {
		id := init1IDBase + int64(i)
		n := &initNode{id: id, v: initMagic(id), tag: "init1"}
		a[i] = n
		if i%64 == 0 {
			m[i] = n
			s = append(s, n)
		}
	}

	// 帧中部：深递归远大于当前栈，栈增长会复制本帧（含 4096 个指针）。
	got := deepRec(init1Depth, init1DepthSeed)
	if want := deepExpected(init1Depth, init1DepthSeed); got != want {
		failf("phase=init1: deep recursion sum=%d want=%d", got, want)
	}
	runtime.GC()

	// 增长 + GC 后校验局部数组、map、slice。
	var sum int64
	for i := 0; i < init1N; i++ {
		n := a[i]
		id := init1IDBase + int64(i)
		if n == nil || n.id != id || n.v != initMagic(id) || n.tag != "init1" {
			failf("phase=init1: local array[%d] corrupted after stack growth (id=%v)", i, n)
		}
		sum += n.v
	}
	if len(m) != init1N/64 {
		failf("phase=init1: map len=%d want=%d", len(m), init1N/64)
	}
	for k, n := range m {
		if n == nil || n.id != init1IDBase+int64(k) || n.v != initMagic(init1IDBase+int64(k)) {
			failf("phase=init1: map[%d] corrupted after stack growth", k)
		}
	}
	if len(s) != init1N/64 {
		failf("phase=init1: slice len=%d want=%d", len(s), init1N/64)
	}
	for i, n := range s {
		if n == nil || n.id != init1IDBase+int64(i*64) || n.v != initMagic(init1IDBase+int64(i*64)) {
			failf("phase=init1: slice[%d] corrupted after stack growth", i)
		}
	}

	init1Checksum = sum
	init1MapLen = len(m)
	init1SliceLen = len(s)
	init1Nodes = append([]*initNode(nil), a[:]...)
	init1Stage = 1
	initOrder = append(initOrder, 1)
}
