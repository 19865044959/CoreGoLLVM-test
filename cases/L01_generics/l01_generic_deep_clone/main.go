// L01: 深拷贝——泛型 store[V] 的克隆必须复制 map/slice/递归子节点，
// 克隆与原件不共享任何可变载荷；GC 后两侧内容仍独立且完整。
package main

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"sync/atomic"
)

type node struct {
	ID    int64
	Name  string
	Tags  []string
	Attrs map[string]int64
	Child *node
}

type store[V any] struct {
	M     map[string]*V
	Order []string
}

func cloneStrings(src []string) []string {
	if src == nil {
		return nil
	}
	dst := make([]string, len(src))
	copy(dst, src)
	return dst
}

func cloneAttrs(src map[string]int64) map[string]int64 {
	if src == nil {
		return nil
	}
	dst := make(map[string]int64, len(src))
	for k, v := range src {
		dst[k] = v
	}
	return dst
}

func cloneNode(n *node) *node {
	if n == nil {
		return nil
	}
	return &node{
		ID:    n.ID,
		Name:  n.Name,
		Tags:  cloneStrings(n.Tags),
		Attrs: cloneAttrs(n.Attrs),
		Child: cloneNode(n.Child),
	}
}

func cloneStore[V any](s *store[V], cloneV func(*V) *V) *store[V] {
	if s == nil {
		return &store[V]{M: make(map[string]*V)}
	}
	c := &store[V]{
		M:     make(map[string]*V, len(s.M)),
		Order: cloneStrings(s.Order),
	}
	for k, v := range s.M {
		c.M[k] = cloneV(v)
	}
	return c
}

var (
	failed  int32
	gcSink  [][]byte
	uafSeed int64 = 1
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
		fmt.Printf("FAILED %s seed=%d (%d failed checks)\n", name, uafSeed, n)
		os.Exit(1)
	}
	fmt.Println("ALL PASS " + name)
}

func envSeed() int64 {
	s := os.Getenv("UAF_SEED")
	if s == "" {
		return 1
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil || n <= 0 {
		return 1
	}
	return n
}

func main() {
	uafSeed = envSeed()
	n := 28 + int(uafSeed%13)

	orig := &store[node]{M: make(map[string]*node, n), Order: make([]string, 0, n)}
	refs := make([]*node, n)
	deepOrig := make([]*node, n)
	depths := make([]int, n)
	for i := 0; i < n; i++ {
		var head, tail *node
		depth := 1 + i%3
		for d := 0; d < depth; d++ {
			c := &node{ID: int64(1000 + i*10 + d), Name: fmt.Sprintf("c%d-%d", i, d)}
			if head == nil {
				head, tail = c, c
			} else {
				tail.Child = c
				tail = c
			}
		}
		nd := &node{
			ID:    int64(i),
			Name:  fmt.Sprintf("n-%d", i),
			Tags:  []string{"a", fmt.Sprintf("t%d", i)},
			Attrs: map[string]int64{"x": int64(i), "y": int64(i * 2)},
			Child: head,
		}
		switch i % 9 {
		case 0:
			nd.Tags = nil
			nd.Attrs = nil
		case 1:
			nd.Tags = []string{}
			nd.Attrs = map[string]int64{}
		}
		key := fmt.Sprintf("r%03d", i)
		orig.M[key] = nd
		orig.Order = append(orig.Order, key)
		refs[i] = nd
		deepOrig[i] = tail
		depths[i] = depth
	}

	clone := cloneStore(orig, cloneNode)
	gcStress()

	ck(clone != orig, "clone store reused original object")
	ck(len(clone.M) == n, "clone store size mismatch")
	ck(len(clone.Order) == n, "clone order size mismatch")
	for i, key := range orig.Order {
		ck(clone.Order[i] == key, "clone order sequence mismatch")
	}

	deepClone := make([]*node, n)
	for i := 0; i < n; i++ {
		key := orig.Order[i]
		o := orig.M[key]
		c := clone.M[key]
		ck(o != nil && c != nil, "clone lost record")
		if o == nil || c == nil {
			continue
		}
		ck(o != c, "record pointer was not cloned")
		ck(c.ID == o.ID && c.Name == o.Name, "record scalar fields corrupted by clone")
		ck((c.Tags == nil) == (o.Tags == nil), "nil tags not preserved by clone")
		if o.Tags != nil {
			ck(c.Tags != nil && len(c.Tags) == len(o.Tags), "tags length mismatch after clone")
			if len(c.Tags) == len(o.Tags) && len(c.Tags) > 0 {
				ck(c.Tags[0] == o.Tags[0], "tags content mismatch after clone")
				c.Tags[0] = "mutated"
				ck(o.Tags[0] != "mutated", "clone shares tags backing array with original")
			}
		}
		ck((c.Attrs == nil) == (o.Attrs == nil), "nil attrs not preserved by clone")
		if o.Attrs != nil {
			ck(c.Attrs != nil && len(c.Attrs) == len(o.Attrs), "attrs length mismatch after clone")
			c.Attrs["mutated"] = 1
			_, leaked := o.Attrs["mutated"]
			ck(!leaked, "clone shares attrs map with original")
		}

		oc, cc := o.Child, c.Child
		depth := 0
		for oc != nil && cc != nil {
			ck(oc != cc, "child node pointer was not cloned")
			ck(cc.ID == oc.ID && cc.Name == oc.Name, "child node fields corrupted by clone")
			oc = oc.Child
			cc = cc.Child
			depth++
		}
		ck(oc == nil && cc == nil, "child chain length mismatch after clone")
		ck(depth == depths[i], "child chain depth mismatch after clone")
		if depth > 0 {
			// 找到克隆链的尾节点并变异，原链尾必须不变。
			cd := c.Child
			for cd != nil && cd.Child != nil {
				cd = cd.Child
			}
			deepClone[i] = cd
			if cd != nil {
				cd.Name = "clone-tail"
				ck(deepOrig[i].Name != "clone-tail", "deep clone shares tail node with original")
			}
		}
		// 克隆记录本身变异不得影响原件。
		c.Name = "clone-record"
		ck(o.Name != "clone-record", "clone shares record object with original")
	}

	// 原 Order 必须仍独立。
	clone.Order[0] = "zzz"
	ck(orig.Order[0] != "zzz", "clone shares order slice with original")

	gcStress()
	for i := 0; i < n; i++ {
		o := refs[i]
		c := clone.M[orig.Order[i]]
		ck(o != nil && c != nil, "record lost after second GC")
		if o == nil || c == nil {
			continue
		}
		ck(deepOrig[i] != nil && deepClone[i] != nil, "deep clone tail lost after GC")
		if deepOrig[i] != nil {
			ck(deepOrig[i].Name != "clone-tail", "original deep tail corrupted after GC")
		}
		if deepClone[i] != nil {
			ck(deepClone[i].Name == "clone-tail", "clone deep tail corrupted after GC")
		}
	}

	// nil store / nil node 边界。
	empty := cloneStore[node](nil, cloneNode)
	ck(empty != nil && empty.M != nil && len(empty.M) == 0, "nil store clone must be empty")
	ck(empty.Order == nil, "nil store clone order must stay nil")
	ck(cloneNode(nil) == nil, "nil node clone must be nil")

	// 第二个实例化：store[int64] 与同一 cloneStore 代码。
	v1, v2 := int64(7), int64(9)
	s64 := &store[int64]{
		M:     map[string]*int64{"a": &v1, "b": &v2},
		Order: []string{"a", "b"},
	}
	c64 := cloneStore(s64, func(p *int64) *int64 {
		q := *p
		return &q
	})
	ck(c64.M["a"] != s64.M["a"] && c64.M["b"] != s64.M["b"], "int64 clone shares pointers")
	ck(*c64.M["a"] == 7 && *c64.M["b"] == 9, "int64 clone value mismatch")
	*c64.M["a"] = 70
	ck(*s64.M["a"] == 7, "int64 clone mutation leaked to original")
	gcStress()
	ck(*s64.M["a"] == 7 && *c64.M["a"] == 70, "int64 clone/original corrupted after GC")

	finish("l01_generic_deep_clone")
}
