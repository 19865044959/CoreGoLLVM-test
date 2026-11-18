// L11: 含指针字段的 struct 值拷贝/函数返回/切片 copy 必须遵守 Go 语义：
// 标量字段副本独立，指针字段按值复制（共享同一目标）；GC 后关系不变。
package main

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"sync/atomic"
)

var (
	failed int32
	gcSink [][]byte
)

type cnode struct {
	id     int
	name   string
	weight int64
	next   *cnode
	data   *[4]int64
}

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
		b[len(b)-1] = byte(255 - i)
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

func buildChain(n int) (*cnode, []*cnode) {
	nodes := make([]*cnode, n)
	for i := 0; i < n; i++ {
		d := &[4]int64{int64(i), int64(i + 1), int64(i + 2), int64(i + 3)}
		nodes[i] = &cnode{id: i, name: "n" + strconv.Itoa(i), weight: int64(i) * 11, data: d}
	}
	for i := 0; i+1 < n; i++ {
		nodes[i].next = nodes[i+1]
	}
	return nodes[0], nodes
}

// copyAndBump 以值方式接收并返回 struct，调用方原对象不得被修改。
func copyAndBump(in cnode) cnode {
	out := in
	out.id += 1000
	out.name = "copied"
	out.weight *= 2
	return out
}

func main() {
	head, all := buildChain(16)
	gcStress()

	// 值赋值：标量独立，指针字段按值复制。
	cp := *head
	ck(cp.id == head.id && cp.name == head.name && cp.weight == head.weight, "value copy scalar mismatch")
	ck(cp.next == head.next, "pointer field must be copied as-is")
	ck(cp.data == head.data, "data pointer must be shared after value copy")
	cp.id = 999
	cp.name = "changed"
	cp.weight = -1
	ck(head.id == 0 && head.name == "n0" && head.weight == 0, "original scalars changed through copy")
	ck(cp.data[1] == 1, "shared data content mismatch")
	cp.data[0] = 4242
	ck(head.data[0] == 4242, "pointer target is shared, not deep copied")

	// 函数返回副本：标量独立，指针仍然共享。
	orig := all[5]
	ret := copyAndBump(*orig)
	ck(orig.id == 5 && orig.name == "n5" && orig.weight == 55, "function return copy mutated original")
	ck(ret.id == 1005 && ret.name == "copied" && ret.weight == 110, "returned copy scalar mismatch")
	ck(ret.next == orig.next && ret.data == orig.data, "returned copy pointer fields must alias original")
	ck(ret.next != nil && ret.next.id == 6, "returned copy next pointer corrupted")

	// 切片 copy：逐元素值拷贝，指针字段共享。
	dst := make([]cnode, len(all))
	for i, p := range all {
		dst[i] = *p
	}
	gcStress()
	for i := range dst {
		ck(dst[i].id == i, "slice element copy id corrupted")
		ck(dst[i].weight == int64(i)*11, "slice element copy weight corrupted")
		ck(dst[i].name == all[i].name, "slice element copy name corrupted")
		ck(dst[i].next == all[i].next, "slice element copy lost next pointer")
		ck(dst[i].data == all[i].data, "slice element copy lost data pointer")
	}
	dst[3].id = -3
	dst[3].name = "dst3"
	ck(all[3].id == 3 && all[3].name == "n3", "slice element scalars leaked into original")
	dst[3].data[2] = -300
	ck(all[3].data[2] == -300, "shared data target must be visible through original")

	// 数组值拷贝与切片同语义。
	var a1 [4]cnode
	for i := 0; i < 4; i++ {
		a1[i] = *all[10+i]
	}
	a2 := a1
	a2[1].weight = -7
	a2[1].name = "a2"
	ck(a1[1].weight == 121 && a1[1].name == "n11", "array copy scalars leaked into original")
	ck(a2[1].next == a1[1].next && a2[1].data == a1[1].data, "array copy pointer fields changed")

	gcStress()
	// GC 后链关系与原始标量仍正确（仅 all[0].data[0]/all[3].data[2] 被显式共享修改）。
	for i := 0; i < 16; i++ {
		ck(all[i].id == i, "chain node id corrupted after GC")
		ck(all[i].name == "n"+strconv.Itoa(i), "chain node name corrupted after GC")
		ck(all[i].weight == int64(i)*11, "chain node weight corrupted after GC")
		if i+1 < 16 {
			ck(all[i].next == all[i+1], "chain link corrupted after GC")
		} else {
			ck(all[i].next == nil, "chain tail must be nil after GC")
		}
	}
	ck(dst[5].next != nil && dst[5].next.id == 6, "copied chain pointer corrupted after GC")
	ck(a2[0].data != nil && a2[0].data[1] == 11, "copied array data pointer corrupted after GC")

	finish("l11_copy_elision")
}
