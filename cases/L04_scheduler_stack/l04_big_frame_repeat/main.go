// L04: 反复调用 64KB 指针数组栈帧，并叠加 16KB 帧的嵌套调用，触发多次
// morestack/栈复制；帧内显式 GC 后逐槽校验指针身份与内容。
package main

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"sync/atomic"
)

type node struct{ v int64 }

const (
	bigSlots  = 8192 // 64KB（64 位平台）
	midSlots  = 2048 // 16KB
	nodeCount = 64
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

func ckf(ok bool, format string, args ...any) {
	if !ok {
		atomic.AddInt32(&failed, 1)
		fmt.Printf("FAIL: "+format+"\n", args...)
	}
}

func gcStress() {
	for i := 0; i < 8; i++ {
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

func envSeed() int64 {
	s := os.Getenv("UAF_SEED")
	if s == "" {
		return 1
	}
	v, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 1
	}
	return v
}

//go:noinline
func leafBigFrame(rot int, nodes []*node) int64 {
	var frame [bigSlots]*node
	for i := 0; i < len(frame); i++ {
		frame[i] = nodes[(i+rot)%len(nodes)]
	}
	// 64KB 指针帧全部活跃时 GC，栈扫描必须覆盖每一个槽位。
	gcStress()
	var sum int64
	for i := 0; i < len(frame); i++ {
		p := frame[i]
		if p != nodes[(i+rot)%len(nodes)] {
			return -1 << 60
		}
		sum += p.v
	}
	return sum
}

//go:noinline
func nestedFrame(rot int, nodes []*node, depth int) int64 {
	var frame [midSlots]*node
	for i := 0; i < len(frame); i++ {
		frame[i] = nodes[(i+rot)%len(nodes)]
	}
	var child int64
	if depth == 0 {
		child = leafBigFrame(rot, nodes)
	} else {
		child = nestedFrame(rot+1, nodes, depth-1)
	}
	if child == -1<<60 {
		return child
	}
	if depth == 0 {
		// 64KB 叶子帧返回后，四层 16KB 父帧仍在栈上，再扫一次。
		gcStress()
	}
	var sum int64
	for i := 0; i < len(frame); i++ {
		p := frame[i]
		if p != nodes[(i+rot)%len(nodes)] {
			return -1 << 60
		}
		sum += p.v
	}
	return child + sum
}

func frameSum(rot int, nodes []*node) int64 {
	var s int64
	for i := 0; i < bigSlots; i++ {
		s += nodes[(i+rot)%len(nodes)].v
	}
	return s
}

func midSum(rot int, nodes []*node) int64 {
	var s int64
	for i := 0; i < midSlots; i++ {
		s += nodes[(i+rot)%len(nodes)].v
	}
	return s
}

func expectedNested(rot int, nodes []*node, depth int) int64 {
	var s int64
	for d := 0; d <= depth; d++ {
		s += midSum(rot+d, nodes)
	}
	s += frameSum(rot+depth, nodes)
	return s
}

func main() {
	seed := envSeed()
	nodes := make([]*node, nodeCount)
	for i := range nodes {
		nodes[i] = &node{v: seed*97 + int64(i)*13}
	}
	baseRot := int(seed % nodeCount)
	if baseRot < 0 {
		baseRot += nodeCount
	}
	for r := 0; r < 3; r++ {
		rot := (baseRot + r*17) % nodeCount
		got := nestedFrame(rot, nodes, 3)
		want := expectedNested(rot, nodes, 3)
		ckf(got == want, "nested rot=%d got=%d want=%d", rot, got, want)
		for k := 0; k < 2; k++ {
			rk := (rot + k*23) % nodeCount
			got = leafBigFrame(rk, nodes)
			want = frameSum(rk, nodes)
			ckf(got == want, "big frame rot=%d got=%d want=%d", rk, got, want)
		}
		gcStress()
	}
	finish("l04_big_frame_repeat")
}
