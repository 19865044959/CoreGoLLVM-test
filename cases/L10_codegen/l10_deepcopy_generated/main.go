// L10: 生成风格 DeepCopy/DeepCopyInto：嵌套 struct、slice、map、指针与二维切片，复制后与原对象完全隔离。
package main

import (
	"fmt"
	"math/rand"
	"os"
	"reflect"
	"runtime"
	"strconv"
	"sync/atomic"
)

var (
	failed  int32
	gcSink  [][]byte
	curSeed int64
)

func ck(ok bool, msg string) {
	if !ok {
		atomic.AddInt32(&failed, 1)
		fmt.Println("FAIL:", msg)
	}
}

func ckSeed(ok bool, msg string) {
	if !ok {
		atomic.AddInt32(&failed, 1)
		fmt.Printf("FAIL: %s (seed=%d)\n", msg, curSeed)
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

// 以下 DeepCopy 方法为生成风格（手写模拟）。

type leaf struct {
	S string
	N int64
	B []byte
}

func (x *leaf) DeepCopyInto(out *leaf) {
	*out = *x
	if x.B != nil {
		out.B = make([]byte, len(x.B))
		copy(out.B, x.B)
	}
}

func (x *leaf) DeepCopy() *leaf {
	if x == nil {
		return nil
	}
	out := new(leaf)
	x.DeepCopyInto(out)
	return out
}

type node struct {
	Name     string
	Count    int64
	Leaf     *leaf
	Ref      *leaf
	Tags     []string
	Props    map[string]string
	Children []*node
	Matrix   [][]int64
}

func (x *node) DeepCopyInto(out *node) {
	*out = *x
	if x.Leaf != nil {
		out.Leaf = x.Leaf.DeepCopy()
	}
	if x.Ref != nil {
		out.Ref = x.Ref.DeepCopy()
	}
	if x.Tags != nil {
		out.Tags = make([]string, len(x.Tags))
		copy(out.Tags, x.Tags)
	}
	if x.Props != nil {
		out.Props = make(map[string]string, len(x.Props))
		for k, v := range x.Props {
			out.Props[k] = v
		}
	}
	if x.Children != nil {
		out.Children = make([]*node, len(x.Children))
		for i, c := range x.Children {
			out.Children[i] = c.DeepCopy()
		}
	}
	if x.Matrix != nil {
		out.Matrix = make([][]int64, len(x.Matrix))
		for i, row := range x.Matrix {
			if row != nil {
				out.Matrix[i] = make([]int64, len(row))
				copy(out.Matrix[i], row)
			}
		}
	}
}

func (x *node) DeepCopy() *node {
	if x == nil {
		return nil
	}
	out := new(node)
	x.DeepCopyInto(out)
	return out
}

// noShared 检查两棵树的引用是否完全独立（递归）。
func noShared(a, b *node) bool {
	if a == nil || b == nil {
		return a == b
	}
	if a == b {
		return false
	}
	if a.Leaf != nil && b.Leaf != nil && a.Leaf == b.Leaf {
		return false
	}
	if a.Ref != nil && b.Ref != nil && a.Ref == b.Ref {
		return false
	}
	if len(a.Tags) > 0 && len(b.Tags) > 0 && &a.Tags[0] == &b.Tags[0] {
		return false
	}
	if len(a.Matrix) > 0 && len(b.Matrix) > 0 && len(a.Matrix[0]) > 0 && len(b.Matrix[0]) > 0 &&
		&a.Matrix[0][0] == &b.Matrix[0][0] {
		return false
	}
	if a.Props != nil && b.Props != nil && reflect.ValueOf(a.Props).Pointer() == reflect.ValueOf(b.Props).Pointer() {
		return false
	}
	if len(a.Children) != len(b.Children) {
		return false
	}
	for i := range a.Children {
		if !noShared(a.Children[i], b.Children[i]) {
			return false
		}
	}
	return true
}

func randLeaf(r *rand.Rand) *leaf {
	b := make([]byte, r.Intn(16))
	for i := range b {
		b[i] = byte(r.Intn(256))
	}
	return &leaf{S: fmt.Sprintf("l%d", r.Intn(1000)), N: r.Int63n(1 << 40), B: b}
}

func randTree(r *rand.Rand, depth int) *node {
	n := &node{
		Name:  fmt.Sprintf("node-%d", r.Intn(100000)),
		Count: r.Int63n(1 << 30),
	}
	if r.Intn(3) != 0 {
		n.Leaf = randLeaf(r)
	}
	if r.Intn(3) != 0 {
		if n.Leaf != nil && r.Intn(2) == 0 {
			n.Ref = n.Leaf // 共享指针：DeepCopy 应各自复制
		} else {
			n.Ref = randLeaf(r)
		}
	}
	for i := 0; i < r.Intn(5); i++ {
		n.Tags = append(n.Tags, fmt.Sprintf("tag%d", r.Intn(100)))
	}
	if r.Intn(3) != 0 {
		n.Props = map[string]string{}
		for i := 0; i < r.Intn(4); i++ {
			n.Props[fmt.Sprintf("k%d", r.Intn(20))] = fmt.Sprintf("v%d", r.Intn(100))
		}
	}
	if r.Intn(2) == 0 {
		n.Matrix = make([][]int64, r.Intn(3)+1)
		for i := range n.Matrix {
			row := make([]int64, r.Intn(4))
			for j := range row {
				row[j] = r.Int63n(1000)
			}
			n.Matrix[i] = row
		}
	}
	if depth > 0 {
		for i := 0; i < r.Intn(3); i++ {
			n.Children = append(n.Children, randTree(r, depth-1))
		}
	}
	return n
}

func main() {
	curSeed = 1
	if s := os.Getenv("UAF_SEED"); s != "" {
		if v, err := strconv.ParseInt(s, 10, 64); err == nil {
			curSeed = v
		}
	}

	// 1. 固定树：深拷贝相等且不共享内存，GC 后双向修改互不影响。
	shared := &leaf{S: "shared", N: 7, B: []byte{1, 2, 3}}
	orig := &node{
		Name: "root", Count: 10,
		Leaf: shared, Ref: shared,
		Tags:     []string{"a", "b"},
		Props:    map[string]string{"x": "1", "y": "2"},
		Matrix:   [][]int64{{1, 2}, {}, {3}},
		Children: []*node{{Name: "c1", Tags: []string{"z"}}, {Name: "c2"}},
	}
	cp := orig.DeepCopy()
	ck(reflect.DeepEqual(orig, cp), "fixed deep copy equal")
	ck(noShared(orig, cp), "fixed deep copy no shared refs")
	ck(cp.Leaf != cp.Ref, "shared source leaf is duplicated")
	ck(cp.Leaf.S == "shared" && cp.Ref.N == 7 && cp.Leaf != orig.Leaf, "leaf contents copied")
	ck(len(cp.Matrix) == 3 && len(cp.Matrix[1]) == 0 && cp.Matrix[1] != nil, "empty matrix row preserved")

	snapshot := orig.DeepCopy()
	gcSink = append(gcSink, shared.B, snapshot.Leaf.B)
	gcStress()
	ck(reflect.DeepEqual(orig, snapshot), "original survives GC")
	ck(reflect.DeepEqual(cp, snapshot), "copy survives GC")

	// 修改原对象：所有层级都不影响副本。
	orig.Name = "mut-root"
	orig.Tags[0] = "mut-tag"
	orig.Props["x"] = "mut"
	delete(orig.Props, "y")
	orig.Leaf.B[0] = 99
	orig.Leaf.S = "mut-leaf"
	orig.Matrix[0][0] = 777
	orig.Children[0].Name = "mut-child"
	orig.Children[0].Tags[0] = "mut-child-tag"
	ck(reflect.DeepEqual(cp, snapshot), "copy unaffected by source mutation")
	ck(cp.Props["x"] == "1" && cp.Props["y"] == "2", "copy map independent")
	ck(cp.Leaf.B[0] == 1 && cp.Matrix[0][0] == 1, "copy slices independent")

	// 修改副本：原对象（已是 mutation 后的状态）不受影响。
	before := orig.DeepCopy()
	cp.Children[1].Name = "mut-copy-child"
	cp.Tags[1] = "mut-copy-tag"
	cp.Matrix[2][0] = -1
	ck(reflect.DeepEqual(orig, before), "source unaffected by copy mutation")

	// 2. nil 接收者与 nil/空容器语义。
	var nilNode *node
	ck(nilNode.DeepCopy() == nil, "nil deep copy")
	var nilLeaf *leaf
	ck(nilLeaf.DeepCopy() == nil, "nil leaf deep copy")
	empty := &node{Tags: []string{}, Props: map[string]string{}, Children: []*node{}, Matrix: [][]int64{}}
	ec := empty.DeepCopy()
	ck(ec.Tags != nil && len(ec.Tags) == 0, "empty tags preserved non-nil")
	ck(ec.Props != nil && len(ec.Props) == 0, "empty map preserved non-nil")
	ck(ec.Children != nil && len(ec.Children) == 0, "empty children preserved non-nil")
	ck(ec.Matrix != nil && len(ec.Matrix) == 0, "empty matrix preserved non-nil")
	z := &node{}
	zc := z.DeepCopy()
	ck(zc.Tags == nil && zc.Props == nil && zc.Children == nil && zc.Matrix == nil, "nil containers preserved")

	// 3. 随机树 × UAF_SEED：相等性、GC 存活、深拷贝再拷贝稳定、无共享引用。
	r := rand.New(rand.NewSource(curSeed))
	for iter := 0; iter < 120; iter++ {
		src := randTree(r, 4)
		dst := src.DeepCopy()
		ckSeed(reflect.DeepEqual(src, dst), "random deep copy equal")
		ckSeed(noShared(src, dst), "random deep copy no shared refs")
		if iter%11 == 0 {
			gcSink = append(gcSink, []byte(src.Name))
			gcStress()
		}
		ckSeed(reflect.DeepEqual(src, dst), "random copy survives GC")
		// 二级拷贝仍与源相等。
		again := dst.DeepCopy()
		ckSeed(reflect.DeepEqual(src, again), "second copy equal")
		// 修改源后副本保持原值。
		snap := src.DeepCopy()
		src.Name = src.Name + "-mut"
		src.Count++
		if src.Leaf != nil {
			src.Leaf.N++
		}
		if src.Tags != nil && len(src.Tags) > 0 {
			src.Tags[0] += "!"
		}
		if src.Props != nil {
			for k := range src.Props {
				src.Props[k] += "?"
				break
			}
		}
		if len(src.Children) > 0 {
			src.Children[0].Name += "-mut"
		}
		if len(src.Matrix) > 0 && len(src.Matrix[0]) > 0 {
			src.Matrix[0][0]++
		}
		ckSeed(reflect.DeepEqual(dst, snap), "random copy isolated from source mutation")
	}

	finish("l10_deepcopy_generated")
}
