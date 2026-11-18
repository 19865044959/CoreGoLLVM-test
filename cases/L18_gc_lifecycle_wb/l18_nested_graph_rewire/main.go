// L18: 嵌套对象图跨容器改写。老 root 的 slice/map/interface 同时指向同一个
// 年轻子图；子图内部还有 slice/map/interface/peer 环，并回指老节点
// （年轻->老）。每轮 noinline attachAll 创建子图并按 UAF_SEED 决定 setter
// 顺序，写入老容器后 GC，随后沿所有路径遍历校验别名关系与内容。
package main

import (
	"fmt"
	"math/rand"
	"os"
	"runtime"
	"strconv"
	"sync/atomic"
)

const (
	ngRounds = 16
	ngDataN  = 16
)

type ngNode struct {
	id     int64
	name   string
	data   [ngDataN]byte
	kids   []*ngNode
	byName map[string]*ngNode
	any    interface{}
	peer   *ngNode
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

func seedValue() int64 {
	s := os.Getenv("UAF_SEED")
	if s == "" {
		return 1
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 1
	}
	return n
}

func ngByte(id int64, i int) byte { return byte((int(id)*23 + i*13) % 251) }

//go:noinline
func newOldNode(id int64) *ngNode {
	n := &ngNode{id: id, name: "old"}
	for i := range n.data {
		n.data[i] = ngByte(id, i)
	}
	return n
}

//go:noinline
func buildOldGraph() *ngNode {
	root := newOldNode(0)
	root.name = "root"
	a := newOldNode(1)
	b := newOldNode(2)
	c := newOldNode(3)
	root.kids = []*ngNode{a, b}
	root.byName = map[string]*ngNode{"a": a}
	root.peer = root
	a.kids = []*ngNode{c}
	a.byName = map[string]*ngNode{"c": c}
	a.any = c
	b.peer = root
	c.peer = a
	gcStress()
	gcStress() // root/a/b/c 及容器晋升为老对象
	return root
}

// newSub 创建年轻子图：y0 的 kids/byName/any 互相别名，y2.peer 回指老节点。
//
//go:noinline
func newSub(v int64, old *ngNode) *ngNode {
	y0 := &ngNode{id: v, name: "ysub"}
	y1 := &ngNode{id: v + 1, name: "yleaf"}
	y2 := &ngNode{id: v + 2, name: "yleaf"}
	for i := range y0.data {
		y0.data[i] = ngByte(v, i)
		y1.data[i] = ngByte(v+1, i)
		y2.data[i] = ngByte(v+2, i)
	}
	y0.kids = []*ngNode{y1, y2}
	y0.byName = map[string]*ngNode{"leaf": y1, "alt": y2}
	y0.any = y2
	y0.peer = y1
	y1.peer = y0
	y2.peer = old
	return y0
}

//go:noinline
func attachSlice(root, y *ngNode) { root.kids[0] = y }

//go:noinline
func attachMap(root, y *ngNode) { root.byName["dyn"] = y }

//go:noinline
func attachIface(old, y *ngNode) { old.any = y }

//go:noinline
func attachAll(root, oldA *ngNode, v int64, seed int64) {
	y := newSub(v, oldA)
	rng := rand.New(rand.NewSource(seed ^ v))
	for _, k := range rng.Perm(3) {
		switch k {
		case 0:
			attachSlice(root, y)
		case 1:
			attachMap(root, y)
		case 2:
			attachIface(oldA, y)
		}
	}
}

func checkOldNode(n *ngNode, id int64, name string) bool {
	if n == nil || n.id != id || n.name != name {
		return false
	}
	for i := range n.data {
		if n.data[i] != ngByte(id, i) {
			return false
		}
	}
	return true
}

func verifyGraph(root *ngNode, v int64, seed int64) {
	if !checkOldNode(root, 0, "root") {
		ck(false, fmt.Sprintf("old root corrupted (seed=%d)", seed))
		return
	}
	if root.peer != root {
		ck(false, fmt.Sprintf("root self-cycle broken (seed=%d)", seed))
		return
	}
	a := root.byName["a"]
	if !checkOldNode(a, 1, "old") {
		ck(false, fmt.Sprintf("old child a lost via old map (seed=%d)", seed))
		return
	}
	b := root.kids[1]
	if !checkOldNode(b, 2, "old") || b.peer != root {
		ck(false, fmt.Sprintf("old child b corrupted (seed=%d)", seed))
		return
	}
	c := a.kids[0]
	if !checkOldNode(c, 3, "old") || a.byName["c"] != c || c.peer != a {
		ck(false, fmt.Sprintf("old grandchild c corrupted (seed=%d)", seed))
		return
	}

	y := root.kids[0]
	if y == nil || y.id != v || y.name != "ysub" || root.byName["dyn"] != y {
		ck(false, fmt.Sprintf("young subgraph lost from old slice/map (seed=%d)", seed))
		return
	}
	if y != a.any {
		ck(false, fmt.Sprintf("young subgraph lost from old interface (seed=%d)", seed))
		return
	}
	for i := range y.data {
		if y.data[i] != ngByte(v, i) {
			ck(false, fmt.Sprintf("young subgraph data corrupted (seed=%d)", seed))
			return
		}
	}
	if len(y.kids) != 2 || y.kids[0] == nil || y.kids[1] == nil {
		ck(false, fmt.Sprintf("young subgraph kids corrupted (seed=%d)", seed))
		return
	}
	if y.kids[0].id != v+1 || y.kids[1].id != v+2 || y.kids[0].name != "yleaf" || y.kids[1].name != "yleaf" {
		ck(false, fmt.Sprintf("young leaves corrupted (seed=%d)", seed))
		return
	}
	if y.byName["leaf"] != y.kids[0] || y.byName["alt"] != y.kids[1] {
		ck(false, fmt.Sprintf("young map aliases broken (seed=%d)", seed))
		return
	}
	if in, ok := y.any.(*ngNode); !ok || in != y.kids[1] {
		ck(false, fmt.Sprintf("young interface alias broken (seed=%d)", seed))
		return
	}
	if y.peer != y.kids[0] || y.kids[0].peer != y {
		ck(false, fmt.Sprintf("young peer cycle broken (seed=%d)", seed))
		return
	}
	if y.kids[1].peer != a {
		ck(false, fmt.Sprintf("young->old back-pointer lost (seed=%d)", seed))
		return
	}
}

func main() {
	seed := seedValue()
	root := buildOldGraph()
	oldA := root.byName["a"]

	for r := 1; r <= ngRounds; r++ {
		v := int64(100 + r*10)
		attachAll(root, oldA, v, seed)
		gcStress()
		gcStress()
		verifyGraph(root, v, seed)
	}
	runtime.KeepAlive(root)
	finish("l18_nested_graph_rewire")
}
