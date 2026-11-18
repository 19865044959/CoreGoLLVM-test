// L10: 生成式深拷贝（带环与别名关系）：两遍复制/身份表保活，共享指针保持一致，
// 环终止，副本与源全层级隔离；GC 后校验引用图与载荷。
package main

import (
	"bytes"
	"fmt"
	"math/rand"
	"os"
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

type shared struct {
	Tag  string
	Data []byte
}

type node struct {
	ID      int
	Name    string
	Payload []byte
	Shared  *shared
	Ref     *shared
	Next    *node
	Prev    *node
	Links   []*node
	Attrs   map[string]*shared
	Empty   []string
}

func cloneBytes(p []byte) []byte {
	if p == nil {
		return nil
	}
	out := make([]byte, len(p))
	copy(out, p)
	return out
}

type copier struct {
	nodes  map[*node]*node
	shared map[*shared]*shared
}

func copyShared(x *shared, st *copier) *shared {
	if x == nil {
		return nil
	}
	if c, ok := st.shared[x]; ok {
		return c
	}
	c := &shared{Tag: x.Tag, Data: cloneBytes(x.Data)}
	st.shared[x] = c
	return c
}

// copyNode 先登记身份再递归，环与共享子图都能安全处理。
func copyNode(x *node, st *copier) *node {
	if x == nil {
		return nil
	}
	if c, ok := st.nodes[x]; ok {
		return c
	}
	c := &node{ID: x.ID, Name: x.Name}
	st.nodes[x] = c
	c.Payload = cloneBytes(x.Payload)
	c.Shared = copyShared(x.Shared, st)
	c.Ref = copyShared(x.Ref, st)
	c.Next = copyNode(x.Next, st)
	c.Prev = copyNode(x.Prev, st)
	if x.Links != nil {
		c.Links = make([]*node, len(x.Links))
		for i, l := range x.Links {
			c.Links[i] = copyNode(l, st)
		}
	}
	if x.Attrs != nil {
		c.Attrs = make(map[string]*shared, len(x.Attrs))
		for k, v := range x.Attrs {
			c.Attrs[k] = copyShared(v, st)
		}
	}
	if x.Empty != nil {
		c.Empty = make([]string, len(x.Empty))
		copy(c.Empty, x.Empty)
	}
	return c
}

func deepCopyGraph(root *node) *node {
	return copyNode(root, &copier{nodes: map[*node]*node{}, shared: map[*shared]*shared{}})
}

type verifier struct {
	nodes     map[*node]*node
	revNodes  map[*node]*node
	shared    map[*shared]*shared
	revShared map[*shared]*shared
}

func newVerifier() *verifier {
	return &verifier{
		nodes:     map[*node]*node{},
		revNodes:  map[*node]*node{},
		shared:    map[*shared]*shared{},
		revShared: map[*shared]*shared{},
	}
}

func (v *verifier) sharedRef(a, b *shared) bool {
	if a == nil || b == nil {
		return a == b
	}
	if a == b {
		return false
	}
	if got, ok := v.shared[a]; ok {
		return got == b
	}
	if prev, ok := v.revShared[b]; ok && prev != a {
		return false
	}
	v.shared[a] = b
	v.revShared[b] = a
	if a.Tag != b.Tag || !bytes.Equal(a.Data, b.Data) || (a.Data == nil) != (b.Data == nil) {
		return false
	}
	if len(a.Data) > 0 && &a.Data[0] == &b.Data[0] {
		return false
	}
	return true
}

func (v *verifier) nodeRef(a, b *node) bool {
	if a == nil || b == nil {
		return a == b
	}
	if got, ok := v.nodes[a]; ok {
		return got == b
	}
	if a == b {
		return false
	}
	if prev, ok := v.revNodes[b]; ok && prev != a {
		return false
	}
	v.nodes[a] = b
	v.revNodes[b] = a
	if a.ID != b.ID || a.Name != b.Name || !bytes.Equal(a.Payload, b.Payload) ||
		(a.Payload == nil) != (b.Payload == nil) {
		return false
	}
	if len(a.Payload) > 0 && &a.Payload[0] == &b.Payload[0] {
		return false
	}
	if !v.sharedRef(a.Shared, b.Shared) || !v.sharedRef(a.Ref, b.Ref) {
		return false
	}
	// 源内 Shared/Ref 的别名关系必须在副本中保持。
	if (a.Shared == a.Ref) != (b.Shared == b.Ref) {
		return false
	}
	if !v.nodeRef(a.Next, b.Next) || !v.nodeRef(a.Prev, b.Prev) {
		return false
	}
	if (a.Links == nil) != (b.Links == nil) || len(a.Links) != len(b.Links) {
		return false
	}
	for i := range a.Links {
		if !v.nodeRef(a.Links[i], b.Links[i]) {
			return false
		}
	}
	if (a.Attrs == nil) != (b.Attrs == nil) || len(a.Attrs) != len(b.Attrs) {
		return false
	}
	for k, as := range a.Attrs {
		bs, ok := b.Attrs[k]
		if !ok || !v.sharedRef(as, bs) {
			return false
		}
	}
	if (a.Empty == nil) != (b.Empty == nil) || len(a.Empty) != len(b.Empty) {
		return false
	}
	for i := range a.Empty {
		if a.Empty[i] != b.Empty[i] {
			return false
		}
	}
	return true
}

func verifyGraph(a, b *node) bool {
	v := newVerifier()
	if !v.nodeRef(a, b) {
		return false
	}
	// 节点与共享对象都必须是一一映射（无重复复用、无遗漏合并）。
	return len(v.nodes) == len(v.revNodes) && len(v.shared) == len(v.revShared)
}

func randPayload(r *rand.Rand) []byte {
	if r.Intn(4) == 0 {
		return nil
	}
	p := make([]byte, r.Intn(20))
	for i := range p {
		p[i] = byte(r.Intn(256))
	}
	return p
}

func randGraph(r *rand.Rand, n int) *node {
	if n == 0 {
		return nil
	}
	pool := make([]*shared, r.Intn(5)+1)
	for i := range pool {
		pool[i] = &shared{Tag: fmt.Sprintf("s%d", r.Intn(1000)), Data: randPayload(r)}
	}
	nodes := make([]*node, n)
	for i := range nodes {
		n := &node{
			ID:      i,
			Name:    fmt.Sprintf("n%d-%d", i, r.Intn(1000)),
			Payload: randPayload(r),
		}
		if r.Intn(3) != 0 {
			n.Shared = pool[r.Intn(len(pool))]
		}
		if r.Intn(3) != 0 {
			if n.Shared != nil && r.Intn(2) == 0 {
				n.Ref = n.Shared
			} else {
				n.Ref = pool[r.Intn(len(pool))]
			}
		}
		nodes[i] = n
	}
	for i := 0; i < n-1; i++ {
		nodes[i].Next = nodes[i+1]
	}
	if n > 1 {
		nodes[n-1].Next = nodes[r.Intn(n)] // 成环
	}
	for i := 0; i < n; i++ {
		if r.Intn(2) == 0 {
			nodes[i].Prev = nodes[r.Intn(n)]
		}
		k := r.Intn(4)
		if k > 0 {
			nodes[i].Links = make([]*node, k)
			for j := range nodes[i].Links {
				nodes[i].Links[j] = nodes[r.Intn(n)]
			}
		}
		if r.Intn(2) == 0 {
			nodes[i].Attrs = make(map[string]*shared)
			for j := 0; j < r.Intn(4); j++ {
				nodes[i].Attrs[fmt.Sprintf("a%d", j)] = pool[r.Intn(len(pool))]
			}
		}
		if r.Intn(3) == 0 {
			nodes[i].Empty = []string{}
		}
	}
	return nodes[0]
}

func main() {
	curSeed = 1
	if s := os.Getenv("UAF_SEED"); s != "" {
		if v, err := strconv.ParseInt(s, 10, 64); err == nil {
			curSeed = v
		}
	}

	// 1. 固定环状图 + 共享指针：副本结构相同、别名保持、与源不共享。
	sa := &shared{Tag: "sa", Data: []byte{1, 2, 3}}
	sb := &shared{Tag: "sb", Data: []byte{}}
	n1 := &node{ID: 1, Name: "n1", Payload: []byte("p1"), Shared: sa, Ref: sa,
		Empty: []string{}}
	n2 := &node{ID: 2, Name: "n2", Payload: []byte{}, Shared: sb, Ref: sa}
	n3 := &node{ID: 3, Name: "n3", Payload: nil, Shared: sa, Ref: sb}
	n1.Next, n2.Next, n3.Next = n2, n3, n1
	n1.Prev, n2.Prev, n3.Prev = n3, n1, n2
	n2.Links = []*node{n1, n3, n2}
	n1.Links = []*node{}
	n3.Attrs = map[string]*shared{"a": sa, "b": sb}
	n2.Attrs = map[string]*shared{"x": sb, "y": sa, "z": sa}

	cp := deepCopyGraph(n1)
	ck(cp != nil && cp != n1, "fixed copy root")
	ck(verifyGraph(n1, cp), "fixed copy preserves graph and aliases")
	ck(cp.Next.Next.Next == cp, "fixed copy cycle preserved")
	ck(cp.Shared == cp.Ref, "aliased shared refs stay aliased")
	ck(cp.Shared != sa && cp.Shared != sb, "fixed copy shares no shared objects with source")
	ck(cp.Links != nil && len(cp.Links) == 0, "empty links preserved non-nil")
	ck(cp.Next.Next.Links == nil, "nil links preserved")
	ck(n1.Links != nil && len(n1.Links) == 0 && cp.Empty != nil && len(cp.Empty) == 0, "empty non-nil containers preserved")

	// 2. GC 后源与副本仍独立可读。
	snapshot := deepCopyGraph(n1)
	gcSink = append(gcSink, sa.Data, cp.Payload)
	gcStress()
	ck(verifyGraph(n1, snapshot), "source survives GC")
	ck(verifyGraph(cp, snapshot), "copy survives GC")

	// 3. 修改源：副本保持快照，所有层级隔离。
	n1.Name = "mut"
	n1.Payload[0] = 'X'
	sa.Data[0] = 99
	sa.Tag = "mut-sa"
	n2.Attrs["new"] = sb
	delete(n2.Attrs, "x")
	n3.Links = []*node{n1}
	n1.Empty = append(n1.Empty, "e")
	ck(verifyGraph(cp, snapshot), "copy isolated from source mutation")
	ck(cp.Shared.Data[0] == 1 && cp.Shared.Tag == "sa", "copy shared payload independent")
	ck(cp.Next.Attrs["x"] != nil && cp.Next.Attrs["new"] == nil, "copy attrs independent")

	// 4. 修改副本：源不受影响。
	srcSnap := deepCopyGraph(n1)
	cp.Payload[0] = 'Y'
	cp.Shared.Data[0] = 7
	cp.Name = "copy-mut"
	cp.Next.Attrs["q"] = cp.Ref
	ck(verifyGraph(n1, srcSnap), "source isolated from copy mutation")

	// 5. nil 与空容器语义。
	ck(deepCopyGraph(nil) == nil, "nil graph copy")
	empty := &node{Payload: []byte{}, Links: []*node{}, Attrs: map[string]*shared{}, Empty: []string{}}
	ec := deepCopyGraph(empty)
	ck(ec.Payload != nil && len(ec.Payload) == 0, "empty payload preserved non-nil")
	ck(ec.Links != nil && len(ec.Links) == 0, "empty links preserved non-nil")
	ck(ec.Attrs != nil && len(ec.Attrs) == 0, "empty attrs preserved non-nil")
	ck(ec.Empty != nil && len(ec.Empty) == 0, "empty strings preserved non-nil")
	zero := &node{}
	zc := deepCopyGraph(zero)
	ck(zc.Payload == nil && zc.Links == nil && zc.Attrs == nil && zc.Empty == nil, "nil containers preserved")

	// 6. 随机图 × UAF_SEED：环/共享/自引用，GC 后二次拷贝稳定。
	r := rand.New(rand.NewSource(curSeed))
	for iter := 0; iter < 180; iter++ {
		src := randGraph(r, 1+r.Intn(14))
		dst := deepCopyGraph(src)
		ckSeed(verifyGraph(src, dst), "random copy graph")
		if iter%17 == 0 {
			gcStress()
			ckSeed(verifyGraph(src, dst), "random copy survives GC")
		}
		again := deepCopyGraph(dst)
		ckSeed(verifyGraph(src, again), "second copy stable")
		// 修改源后副本与快照一致。
		snap := deepCopyGraph(src)
		src.Name += "-mut"
		if len(src.Payload) > 0 {
			src.Payload[0] ^= 0xff
		}
		if src.Shared != nil {
			src.Shared.Tag += "?"
		}
		if src.Attrs != nil {
			src.Attrs["mut"] = src.Shared
		}
		if len(src.Links) > 0 {
			src.Links[0] = nil
		}
		if src.Next != nil {
			src.Next.ID += 1000
		}
		ckSeed(verifyGraph(dst, snap), "random copy isolated from source mutation")
		srcSnap := deepCopyGraph(src)
		dst.Name += "-mut"
		if dst.Shared != nil {
			dst.Shared.Data = append(dst.Shared.Data, 0xaa)
		}
		ckSeed(verifyGraph(src, srcSnap), "random source isolated from copy mutation")
	}

	finish("l10_deepcopy_cycle")
}
