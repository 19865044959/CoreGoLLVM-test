// L01: 泛型图深拷贝：克隆节点/边/负载，保留共享节点与环，校验克隆与原图
// 无别名、结构同构，并在克隆过程中穿插 GC。
// 与 l01_generic_graph 的遍历算法不同，本例聚焦深拷贝与别名隔离。
package main

import (
	"fmt"
	"os"
	"runtime"
	"sort"
	"strconv"
	"sync/atomic"
)

type pcell struct {
	id   int64
	name string
	blob []byte
}

type gnode[T any] struct {
	ID      int64
	Payload *T
	Edges   []*gnode[T]
}

type ggraph[T any] struct {
	nodes map[int64]*gnode[T]
}

func newGGraph[T any]() *ggraph[T] {
	return &ggraph[T]{nodes: make(map[int64]*gnode[T])}
}

func (g *ggraph[T]) add(id int64, p *T) *gnode[T] {
	n := &gnode[T]{ID: id, Payload: p}
	g.nodes[id] = n
	return n
}

func (g *ggraph[T]) link(from, to int64) bool {
	a, ok1 := g.nodes[from]
	b, ok2 := g.nodes[to]
	if !ok1 || !ok2 {
		return false
	}
	for _, e := range a.Edges {
		if e == b {
			return false
		}
	}
	a.Edges = append(a.Edges, b)
	return true
}

func (g *ggraph[T]) sortEdges() {
	for _, n := range g.nodes {
		sort.Slice(n.Edges, func(i, j int) bool { return n.Edges[i].ID < n.Edges[j].ID })
	}
}

func (g *ggraph[T]) sortedIDs() []int64 {
	ids := make([]int64, 0, len(g.nodes))
	for id := range g.nodes {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}

// clone 深拷贝整图：先用 memo 建立节点映射，再按边表重连，
// 因此共享节点与环都保持拓扑同构；负载由调用方提供的函数独立复制。
func (g *ggraph[T]) clone(clonePayload func(*T) *T, tick func(int)) *ggraph[T] {
	out := newGGraph[T]()
	memo := make(map[*gnode[T]]*gnode[T], len(g.nodes))
	ids := g.sortedIDs()
	for i, id := range ids {
		src := g.nodes[id]
		var cp *T
		if src.Payload != nil {
			cp = clonePayload(src.Payload)
		}
		dst := &gnode[T]{ID: src.ID, Payload: cp}
		out.nodes[id] = dst
		memo[src] = dst
		if tick != nil && (i+1)%64 == 0 {
			tick(i + 1)
		}
	}
	for _, id := range ids {
		src := g.nodes[id]
		dst := memo[src]
		for _, e := range src.Edges {
			dst.Edges = append(dst.Edges, memo[e])
		}
	}
	if tick != nil {
		tick(len(ids))
	}
	return out
}

func (g *ggraph[T]) hasCycle() bool {
	const (
		white = 0
		gray  = 1
		black = 2
	)
	color := make(map[int64]uint8, len(g.nodes))
	var dfs func(n *gnode[T]) bool
	dfs = func(n *gnode[T]) bool {
		switch color[n.ID] {
		case gray:
			return true
		case black:
			return false
		}
		color[n.ID] = gray
		for _, e := range n.Edges {
			if dfs(e) {
				return true
			}
		}
		color[n.ID] = black
		return false
	}
	for _, id := range g.sortedIDs() {
		if dfs(g.nodes[id]) {
			return true
		}
	}
	return false
}

var (
	failed   int32
	gcSink   [][]byte
	cellSink []*pcell
	uafSeed  int64 = 1
)

func ck(ok bool, msg string) {
	if !ok {
		atomic.AddInt32(&failed, 1)
		fmt.Println("FAIL:", msg)
	}
}

func gcStress() {
	for i := 0; i < 8; i++ {
		b := make([]byte, 2048)
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

func tortureRounds(def int) int {
	s := os.Getenv("TORTURE_ROUNDS")
	if s == "" {
		return def
	}
	n, err := strconv.Atoi(s)
	if err != nil || n <= 0 {
		return def
	}
	if n > 8 {
		n = 8
	}
	return n
}

func main() {
	uafSeed = envSeed()
	rounds := tortureRounds(1)
	n := (140 + int(uafSeed%31)) * rounds

	cells := make([]*pcell, n)
	for i := range cells {
		cells[i] = &pcell{
			id:   int64(i),
			name: fmt.Sprintf("g%d", i),
			blob: make([]byte, 8+i%6),
		}
		cells[i].blob[0] = byte(i)
		cells[i].blob[len(cells[i].blob)-1] = byte(i >> 1)
	}
	cellSink = cells

	g := newGGraph[pcell]()
	for i := 0; i < n; i++ {
		g.add(int64(i), cells[i])
	}
	for i := 0; i < n; i++ {
		if 2*i+1 < n {
			g.link(int64(i), int64(2*i+1))
		}
		if 2*i+2 < n {
			g.link(int64(i), int64(2*i+2))
		}
		if i > 0 && i%11 == 0 {
			g.link(int64(i), int64(i/2)) // 稀疏跳边
		}
	}
	g.link(int64(n-1), 0) // 固定回边：整棵二叉树从 0 可达，构成环
	g.sortEdges()
	gcStress()

	c := g.clone(func(p *pcell) *pcell {
		cp := &pcell{id: p.id, name: p.name + "_c", blob: append([]byte(nil), p.blob...)}
		cellSink = append(cellSink, cp)
		return cp
	}, func(progress int) {
		if progress%64 == 0 {
			gcStress()
		}
	})
	gcStress()

	ids := g.sortedIDs()
	ck(len(c.nodes) == len(g.nodes), "clone node count mismatch")
	ck(len(ids) == n, "original id set corrupted")
	origNodeSet := make(map[*gnode[pcell]]bool, n)
	cloneNodeSet := make(map[*gnode[pcell]]bool, n)
	clonePayloadSet := make(map[*pcell]bool, n)
	edgeTotal := 0
	for _, id := range ids {
		src := g.nodes[id]
		dst, ok := c.nodes[id]
		ck(ok, fmt.Sprintf("clone missing node %d", id))
		if !ok {
			continue
		}
		ck(src != dst, fmt.Sprintf("clone node %d aliases original", id))
		ck(dst.ID == id, "clone node id corrupted")
		ck(dst.Payload != src.Payload, fmt.Sprintf("clone payload %d aliases original", id))
		ck(dst.Payload != nil && dst.Payload.id == src.Payload.id, "clone payload id corrupted")
		ck(dst.Payload != nil && dst.Payload.name == src.Payload.name+"_c", "clone payload name corrupted")
		ck(dst.Payload != nil && len(dst.Payload.blob) == len(src.Payload.blob), "clone payload blob length corrupted")
		if dst.Payload != nil && src.Payload != nil {
			ck(dst.Payload.blob[0] == src.Payload.blob[0], "clone payload blob content corrupted")
			ck(!clonePayloadSet[dst.Payload], "clone payload pointer shared between nodes")
			clonePayloadSet[dst.Payload] = true
		}
		ck(len(dst.Edges) == len(src.Edges), fmt.Sprintf("node %d edge count mismatch", id))
		edgeTotal += len(dst.Edges)
		for j := range src.Edges {
			want := c.nodes[src.Edges[j].ID]
			ck(dst.Edges[j] == want, fmt.Sprintf("node %d edge %d rewired to wrong clone", id, j))
			ck(dst.Edges[j].ID == src.Edges[j].ID, "clone edge target id corrupted")
			ck(dst.Edges[j] != src.Edges[j], "clone edge aliases original node")
		}
		origNodeSet[src] = true
		cloneNodeSet[dst] = true
	}
	ck(len(origNodeSet) == n && len(cloneNodeSet) == n, "node sets incomplete")
	for node := range cloneNodeSet {
		ck(!origNodeSet[node], "node pointer shared between original and clone")
	}
	ck(edgeTotal > n-1, "clone lost tree edges")
	ck(g.hasCycle() && c.hasCycle(), "cycle not preserved by clone")
	ck(len(clonePayloadSet) == n, fmt.Sprintf("clone payload copies got=%d want=%d", len(clonePayloadSet), n))

	// 修改克隆负载不得影响原图（深拷贝隔离）。
	victim := int64(0)
	savedName := g.nodes[victim].Payload.name
	savedByte := g.nodes[victim].Payload.blob[0]
	c.nodes[victim].Payload.name = "mutated"
	c.nodes[victim].Payload.blob[0] = 0xee
	gcStress()
	ck(g.nodes[victim].Payload.name == savedName, "mutating clone changed original payload name")
	ck(g.nodes[victim].Payload.blob[0] == savedByte, "mutating clone changed original payload blob")
	ck(c.nodes[victim].Payload.name == "mutated", "clone payload mutation lost")
	ck(g.hasCycle() && c.hasCycle(), "cycle lost after clone mutation+GC")
	for _, id := range []int64{1, int64(n / 2), int64(n - 1)} {
		ck(c.nodes[id].Payload != g.nodes[id].Payload, "payload alias reappeared after GC")
		ck(c.nodes[id].Payload.id == id, "clone payload id corrupted by GC")
	}

	// nil 负载图：clone 不得 panic，nil 仍为 nil。
	ng := newGGraph[pcell]()
	ng.add(0, nil)
	ng.add(1, nil)
	ng.link(0, 1)
	ng.sortEdges()
	nc := ng.clone(func(p *pcell) *pcell { return nil }, nil)
	ck(len(nc.nodes) == 2, "nil-payload clone node count mismatch")
	ck(nc.nodes[0].Payload == nil && nc.nodes[1].Payload == nil, "nil payload became non-nil")
	ck(len(nc.nodes[0].Edges) == 1 && nc.nodes[0].Edges[0] == nc.nodes[1], "nil-payload clone edge rewiring corrupted")
	gcStress()

	// 第二次实例化 Graph[string]：值负载深拷贝。
	sg := newGGraph[string]()
	src := make([]*string, 32)
	for i := range src {
		v := fmt.Sprintf("s%d", i)
		src[i] = &v
		sg.add(int64(i), src[i])
		if i > 0 {
			sg.link(int64((i-1)/2), int64(i))
		}
	}
	sg.sortEdges()
	sc := sg.clone(func(p *string) *string {
		v := *p + "_copy"
		return &v
	}, nil)
	gcStress()
	ck(len(sc.nodes) == 32, "string clone node count mismatch")
	for id, dst := range sc.nodes {
		ck(dst.Payload != sg.nodes[id].Payload, "string clone payload aliases original")
		ck(*dst.Payload == *sg.nodes[id].Payload+"_copy", "string clone payload value corrupted")
	}
	ck(sc.hasCycle() == false, "string tree clone reported cycle")

	finish("l01_generic_graph_clone")
}
