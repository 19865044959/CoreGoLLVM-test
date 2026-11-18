// L18: 嵌套对象图在两个老 root 之间“摘除 → 携载跨 GC → 重接”。移动的
// 子树含 kids/byName/any/peer 环；每次重接追加一个年轻 leaf，并把老 root 的
// slice/map/interface 三重入口指向子树。每轮 GC 后校验源 root 无残留、
// 目标 root 三重别名与整棵子树。
package main

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"sync/atomic"
)

const (
	daRounds  = 6
	daMaxRnds = 24
	daDataN   = 16
	daSubBase = 10
	daAOwn    = 20
	daBOwn    = 40
	daLeafID  = 10000
)

type daNode struct {
	id     int64
	name   string
	data   [daDataN]byte
	kids   []*daNode
	byName map[string]*daNode
	any    interface{}
	peer   *daNode
}

type daRoot struct {
	kids   []*daNode
	byName map[string]*daNode
	any    interface{}
}

var (
	failed  int32
	gcSink  [][]byte
	daA     daRoot
	daB     daRoot
	daCarry *daNode
	daLeafs []int64
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

func tortureRounds(def int) int {
	s := os.Getenv("TORTURE_ROUNDS")
	if s == "" {
		return def
	}
	n, err := strconv.Atoi(s)
	if err != nil || n <= 0 {
		return def
	}
	if n > daMaxRnds {
		n = daMaxRnds
	}
	return n
}

func daByte(id int64, salt int64, i int) byte {
	return byte((int(id)*61 + int(salt)*17 + i*9) % 251)
}

//go:noinline
func daNewNode(id int64, salt int64) *daNode {
	n := &daNode{id: id, name: "da"}
	for i := range n.data {
		n.data[i] = daByte(id, salt, i)
	}
	return n
}

//go:noinline
func daSubtree(base, salt int64) *daNode {
	a := daNewNode(base, salt)
	b := daNewNode(base+1, salt)
	c := daNewNode(base+2, salt)
	a.kids = []*daNode{b}
	a.byName = map[string]*daNode{"b": b}
	a.any = b
	a.peer = c
	b.kids = []*daNode{c}
	b.byName = map[string]*daNode{"c": c}
	b.any = c
	b.peer = a
	c.peer = b
	return a
}

//go:noinline
func daDetach(src *daRoot) *daNode {
	sub, _ := src.any.(*daNode)
	out := src.kids[:0]
	for _, k := range src.kids {
		if k != sub {
			out = append(out, k)
		}
	}
	src.kids = out
	delete(src.byName, "moved")
	delete(src.byName, "sub")
	src.any = nil
	return sub
}

//go:noinline
func daAttach(dst *daRoot, sub *daNode, ver int, salt int64) {
	leaf := daNewNode(int64(daLeafID+ver), salt)
	sub.kids = append(sub.kids, leaf)
	leaf.peer = sub
	sub.byName["leaf"] = leaf
	dst.kids = append(dst.kids, sub)
	dst.byName["moved"] = sub
	dst.any = sub
	if len(dst.kids) > 0 {
		sub.any = dst.kids[0]
	}
}

func daCheckNode(n *daNode, id, salt int64, label string) bool {
	if n == nil || n.id != id || n.name != "da" {
		ck(false, fmt.Sprintf("%s: node id=%d want=%d corrupted (salt=%d)", label, n.id, id, salt))
		return false
	}
	for i := range n.data {
		if n.data[i] != daByte(id, salt, i) {
			ck(false, fmt.Sprintf("%s: node id=%d data corrupted at %d (salt=%d)", label, id, i, salt))
			return false
		}
	}
	return true
}

func daCheckOwn(root *daRoot, ownBase, salt int64, label string) bool {
	if root.any != nil {
		ck(false, fmt.Sprintf("%s: root interface not cleared (salt=%d)", label, salt))
		return false
	}
	if len(root.kids) != 1 {
		ck(false, fmt.Sprintf("%s: root kids len=%d want=1 (salt=%d)", label, len(root.kids), salt))
		return false
	}
	own := root.kids[0]
	if !daCheckNode(own, ownBase, salt, label+"-own") {
		return false
	}
	if len(own.kids) != 1 || own.kids[0].id != ownBase+1 {
		ck(false, fmt.Sprintf("%s: own subtree structure broken (salt=%d)", label, salt))
		return false
	}
	if own.byName["c"] != nil {
		ck(false, fmt.Sprintf("%s: own subtree unexpected map entry (salt=%d)", label, salt))
		return false
	}
	if _, ok := root.byName["moved"]; ok {
		ck(false, fmt.Sprintf("%s: root map retains moved entry (salt=%d)", label, salt))
		return false
	}
	if root.byName["own"] != own {
		ck(false, fmt.Sprintf("%s: root own map entry broken (salt=%d)", label, salt))
		return false
	}
	return true
}

func daCheckTarget(root *daRoot, ownBase int64, leafs []int64, salt int64, label string) *daNode {
	sub, ok := root.any.(*daNode)
	if !ok || sub == nil {
		ck(false, fmt.Sprintf("%s: moved interface missing (salt=%d)", label, salt))
		return nil
	}
	if len(root.kids) != 2 || root.kids[1] != sub || root.byName["moved"] != sub {
		ck(false, fmt.Sprintf("%s: root containers disagree on moved subtree (salt=%d)", label, salt))
		return nil
	}
	if !daCheckNode(sub, daSubBase, salt, label+"-moved") {
		return nil
	}
	if !daCheckNode(root.kids[0], ownBase, salt, label+"-own") {
		return nil
	}
	b := sub.kids[0]
	c := b.kids[0]
	if !daCheckNode(b, daSubBase+1, salt, label+"-b") || !daCheckNode(c, daSubBase+2, salt, label+"-c") {
		return nil
	}
	if sub.peer != c || c.peer != b || b.peer != sub {
		ck(false, fmt.Sprintf("%s: subtree peer cycle broken (salt=%d)", label, salt))
		return nil
	}
	for i, lid := range leafs {
		if i+1 >= len(sub.kids) || !daCheckNode(sub.kids[i+1], lid, salt, label+"-leaf") {
			ck(false, fmt.Sprintf("%s: leaf %d mismatch (salt=%d)", label, i, salt))
			return nil
		}
	}
	if len(sub.kids) != 1+len(leafs) {
		ck(false, fmt.Sprintf("%s: subtree kids len=%d want=%d (salt=%d)", label, len(sub.kids), 1+len(leafs), salt))
		return nil
	}
	if sub.byName["leaf"] != sub.kids[len(sub.kids)-1] {
		ck(false, fmt.Sprintf("%s: moved map leaf entry broken (salt=%d)", label, salt))
		return nil
	}
	in, ok := sub.any.(*daNode)
	if !ok || in != root.kids[0] {
		ck(false, fmt.Sprintf("%s: moved interface back-ref broken (salt=%d)", label, salt))
		return nil
	}
	return sub
}

func main() {
	seed := seedValue()
	rounds := tortureRounds(daRounds)
	salt := seed % 11

	subA := daSubtree(daSubBase, salt)
	ownA := daSubtree(daAOwn, salt)
	daA.kids = []*daNode{ownA, subA}
	daA.byName = map[string]*daNode{"own": ownA, "sub": subA}
	daA.any = subA

	ownB := daSubtree(daBOwn, salt)
	daB.kids = []*daNode{ownB}
	daB.byName = map[string]*daNode{"own": ownB}
	daB.any = nil

	gcStress()
	gcStress() // root、两个子树、own 子树全部晋升为老对象

	movedInA := true
	for r := 1; r <= rounds; r++ {
		var src, dst *daRoot
		var srcOwn, dstOwn int64
		if movedInA {
			src, dst = &daA, &daB
			srcOwn, dstOwn = daAOwn, daBOwn
		} else {
			src, dst = &daB, &daA
			srcOwn, dstOwn = daBOwn, daAOwn
		}

		daCarry = daDetach(src)
		if daCarry == nil {
			ck(false, fmt.Sprintf("round %d: detach returned nil (seed=%d)", r, seed))
			break
		}
		if r == 1 {
			if daCarry.id != daSubBase {
				ck(false, fmt.Sprintf("round %d: moved wrong subtree id=%d (seed=%d)", r, daCarry.id, seed))
				break
			}
		}
		gcStress()
		if !daCheckOwn(src, srcOwn, salt, "source") {
			break
		}
		if !daCheckNode(daCarry, daSubBase, salt, "carry") {
			break
		}

		daLeafs = append(daLeafs, int64(daLeafID+r))
		daAttach(dst, daCarry, r, salt)
		daCarry = nil
		gcStress()
		if daCheckTarget(dst, dstOwn, daLeafs, salt, "target") == nil {
			break
		}
		if !daCheckOwn(src, srcOwn, salt, "source-after-attach") {
			break
		}
		movedInA = !movedInA
	}

	finish("l18_graph_detach_reattach")
}
