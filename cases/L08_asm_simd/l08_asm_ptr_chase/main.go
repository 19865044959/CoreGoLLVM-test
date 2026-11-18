// L08: 手写 amd64 汇编沿 next 指针追逐链表并累加字段，
// 节点含 string 字段；跨 GC 校验节点存活、链结构与求和结果。
package main

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"sync/atomic"
	"unsafe"
)

type node struct {
	v    int64
	next *node
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

func chaseSumAsm(head *node) int64

// buildChain 构造 n 个节点，并按固定置换 order 串成链，使指针追逐不按
// 分配顺序进行；返回链头、全部节点与 Go 侧求和。
func buildChain(n int) (*node, []*node, int64) {
	if n == 0 {
		return nil, nil, 0
	}
	nodes := make([]*node, n)
	for i := 0; i < n; i++ {
		nodes[i] = &node{v: int64(i*17%997) - 400, name: "node-" + strconv.Itoa(i)}
	}
	order := make([]int, n)
	seen := make([]bool, n)
	for i := 0; i < n; i++ {
		idx := (i*37 + 11) % n
		if seen[idx] {
			panic("permutation collision")
		}
		seen[idx] = true
		order[i] = idx
	}
	for i := 0; i < n-1; i++ {
		nodes[order[i]].next = nodes[order[i+1]]
	}
	nodes[order[n-1]].next = nil

	head := nodes[order[0]]
	var want int64
	cnt := 0
	for p := head; p != nil; p = p.next {
		want += p.v
		cnt++
	}
	ck(cnt == n, fmt.Sprintf("chain length n=%d got=%d", n, cnt))
	return head, nodes, want
}

func verifyNodes(nodes []*node) {
	for i, p := range nodes {
		ck(p != nil && p.v == int64(i*17%997)-400 && p.name == "node-"+strconv.Itoa(i),
			fmt.Sprintf("node content i=%d", i))
	}
}

func run(n int, rounds int) {
	for r := 0; r < rounds; r++ {
		head, nodes, want := buildChain(n)
		got := chaseSumAsm(head)
		ck(got == want, fmt.Sprintf("chase sum n=%d round=%d got=%d want=%d", n, r, got, want))

		gcStress()
		ck(chaseSumAsm(head) == want, fmt.Sprintf("chase sum after GC n=%d round=%d", n, r))
		verifyNodes(nodes)

		cnt := 0
		var sum int64
		for p := head; p != nil; p = p.next {
			cnt++
			sum += p.v
		}
		ck(cnt == n && sum == want, fmt.Sprintf("chain after GC n=%d round=%d cnt=%d", n, r, cnt))
		runtime.KeepAlive(head)
		runtime.KeepAlive(nodes)
	}
}

func main() {
	ck(unsafe.Offsetof(node{}.v) == 0, "layout v offset")
	ck(unsafe.Offsetof(node{}.next) == 8, "layout next offset")
	ck(unsafe.Offsetof(node{}.name) == 16, "layout name offset")
	ck(unsafe.Sizeof(node{}) == 32, "layout node size")

	ck(chaseSumAsm(nil) == 0, "chase nil list")
	for _, n := range []int{0, 1, 2, 7, 64, 512} {
		run(n, 3)
	}
	run(1000, 20)
	finish("l08_asm_ptr_chase")
}
