// L18: 老对象→老对象→年轻对象两级链写入；以及老对象→年轻中间节点→年轻叶子。
// 所有 setter 均为 noinline，返回后年轻对象只被老对象引用，再 GC 校验。
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
	next *node
}

type holder struct {
	head *node
	mid  *node
	alt  *node
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
func promoteChain() *holder {
	h := &holder{}
	a := &node{v: 7, name: "a"}
	b := &node{v: 14, name: "b"}
	c := &node{v: 21, name: "c"}
	a.next = b
	b.next = c
	h.head = a
	h.mid = b
	h.alt = &node{v: 30, name: "alt"}
	h.alt.next = &node{v: 31, name: "alt2"}
	gcStress()
	gcStress() // head/mid/alt 均晋升为老对象
	return h
}

// setTail 把年轻节点写入老节点 mid.next：路径为 老→老→年轻。
//
//go:noinline
func setTail(mid *node, v int64) {
	mid.next = &node{v: v, name: "tail"}
}

// setAltChain 把老 alt.next 换成新的年轻中间节点，中间节点再指向年轻叶子：
// 路径为 老→年轻→年轻，验证一次写屏障后整条新链都存活。
//
//go:noinline
func setAltChain(alt *node, v int64) {
	m := &node{v: v, name: "m"}
	m.next = &node{v: v + 1, name: "leaf"}
	alt.next = m
}

func checkLink(n *node, v int64, tag string) bool {
	return n != nil && n.v == v && n.name == tag
}

func verifyOldChain(h *holder, v int64) {
	ck(checkLink(h.head, 7, "a"), "chain head corrupted")
	ck(h.head != nil && h.head.next == h.mid, "head->mid link broken")
	ck(checkLink(h.mid, 14, "b"), "old mid corrupted")
	ck(checkLink(h.mid.next, v, "tail"), "old->old->young tail lost")
}

func verifyAltChain(h *holder, v int64) {
	ck(checkLink(h.alt, 30, "alt"), "alt root corrupted")
	ck(h.alt != nil && checkLink(h.alt.next, v, "m"), "old->young middle lost")
	ck(h.alt != nil && h.alt.next != nil && checkLink(h.alt.next.next, v+1, "leaf"), "young->young leaf lost")
}

func main() {
	h := promoteChain()
	for i := 0; i < 24; i++ {
		v := int64(i*16 + 7)

		setTail(h.mid, v)
		gcStress()
		verifyOldChain(h, v)

		setAltChain(h.alt, v)
		gcStress()
		gcStress() // 多轮 GC，确保年轻链表只靠老根存活
		verifyAltChain(h, v)
	}
	runtime.KeepAlive(h)
	finish("l18_old_chain_two_hop")
}
