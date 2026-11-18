// L01: 泛型图遍历：泛型节点/邻接表、BFS/DFS、Kahn 拓扑与环检测，遍历中 GC。
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

type Node[T any] struct {
	ID      int64
	Payload *T
	Edges   []*Node[T]
}

type Graph[T any] struct {
	nodes map[int64]*Node[T]
}

func NewGraph[T any]() *Graph[T] { return &Graph[T]{nodes: make(map[int64]*Node[T])} }

func (g *Graph[T]) Add(id int64, p *T) *Node[T] {
	n := &Node[T]{ID: id, Payload: p}
	g.nodes[id] = n
	return n
}

func (g *Graph[T]) Node(id int64) (*Node[T], bool) {
	n, ok := g.nodes[id]
	return n, ok
}

func (g *Graph[T]) Link(from, to int64) {
	a, ok1 := g.nodes[from]
	b, ok2 := g.nodes[to]
	if !ok1 || !ok2 {
		return
	}
	for _, e := range a.Edges {
		if e == b {
			return
		}
	}
	a.Edges = append(a.Edges, b)
}

func (g *Graph[T]) SortEdges() {
	for _, n := range g.nodes {
		sort.Slice(n.Edges, func(i, j int) bool { return n.Edges[i].ID < n.Edges[j].ID })
	}
}

func (g *Graph[T]) Len() int { return len(g.nodes) }

// BFS 返回访问节点数；相邻边已按 ID 排序，故顺序确定。
func (g *Graph[T]) BFS(start int64, visit func(*Node[T])) int {
	s, ok := g.nodes[start]
	if !ok {
		return 0
	}
	seen := map[int64]bool{start: true}
	queue := []*Node[T]{s}
	count := 0
	for len(queue) > 0 {
		n := queue[0]
		queue = queue[1:]
		visit(n)
		count++
		for _, e := range n.Edges {
			if !seen[e.ID] {
				seen[e.ID] = true
				queue = append(queue, e)
			}
		}
	}
	return count
}

// DFS 迭代前序，固定按升序压栈。
func (g *Graph[T]) DFS(start int64, visit func(*Node[T])) int {
	s, ok := g.nodes[start]
	if !ok {
		return 0
	}
	seen := map[int64]bool{start: true}
	stack := []*Node[T]{s}
	count := 0
	for len(stack) > 0 {
		n := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		visit(n)
		count++
		for i := len(n.Edges) - 1; i >= 0; i-- {
			e := n.Edges[i]
			if !seen[e.ID] {
				seen[e.ID] = true
				stack = append(stack, e)
			}
		}
	}
	return count
}

// HasCycle 用三色标记检测有向环。
func (g *Graph[T]) HasCycle() bool {
	state := make(map[int64]uint8, len(g.nodes))
	var dfs func(n *Node[T]) bool
	dfs = func(n *Node[T]) bool {
		switch state[n.ID] {
		case 1:
			return true
		case 2:
			return false
		}
		state[n.ID] = 1
		for _, e := range n.Edges {
			if dfs(e) {
				return true
			}
		}
		state[n.ID] = 2
		return false
	}
	for _, n := range g.nodes {
		if dfs(n) {
			return true
		}
	}
	return false
}

// Topo 用 Kahn 算法返回拓扑序；有环时 ok=false。
func (g *Graph[T]) Topo() ([]int64, bool) {
	indeg := make(map[int64]int, len(g.nodes))
	for id := range g.nodes {
		indeg[id] = 0
	}
	for _, n := range g.nodes {
		for _, e := range n.Edges {
			indeg[e.ID]++
		}
	}
	ready := make([]int64, 0, len(g.nodes))
	for id, d := range indeg {
		if d == 0 {
			ready = append(ready, id)
		}
	}
	sort.Slice(ready, func(i, j int) bool { return ready[i] < ready[j] })
	out := make([]int64, 0, len(g.nodes))
	for len(ready) > 0 {
		id := ready[0]
		ready = ready[1:]
		out = append(out, id)
		for _, e := range g.nodes[id].Edges {
			indeg[e.ID]--
			if indeg[e.ID] == 0 {
				ready = append(ready, e.ID)
				sort.Slice(ready, func(i, j int) bool { return ready[i] < ready[j] })
			}
		}
	}
	return out, len(out) == len(g.nodes)
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
	n := (300 + int(uafSeed%40)) * rounds

	cells := make([]*pcell, n)
	for i := range cells {
		cells[i] = &pcell{id: int64(i), name: fmt.Sprintf("g%d", i), blob: make([]byte, 7+i%5)}
		cells[i].blob[0] = byte(i)
		cells[i].blob[len(cells[i].blob)-1] = byte(i >> 1)
	}
	cellSink = cells

	g := NewGraph[pcell]()
	for i := 0; i < n; i++ {
		g.Add(int64(i), cells[i])
	}
	// 二叉树骨架：i -> 2i+1, 2i+2；再稀疏加跳边（Link 去重）。
	for i := 0; i < n; i++ {
		for _, c := range []int64{int64(2*i + 1), int64(2*i + 2)} {
			if c < int64(n) {
				g.Link(int64(i), c)
			}
		}
		if i%17 == 3 {
			c := int64(i + 29)
			if c < int64(n) {
				g.Link(int64(i), c)
			}
		}
	}
	g.SortEdges()
	ck(g.Len() == n, "graph node count corrupted")
	gcStress()

	// BFS：数量、唯一性、负载指针身份，每 32 个节点 GC 一次。
	visited := make(map[int64]bool, n)
	order := make([]int64, 0, n)
	count := g.BFS(0, func(nd *Node[pcell]) {
		ck(!visited[nd.ID], "BFS visited a node twice")
		visited[nd.ID] = true
		order = append(order, nd.ID)
		ck(nd.Payload == cells[nd.ID], "BFS payload pointer identity changed")
		if nd.Payload != nil {
			ck(nd.Payload.id == nd.ID && nd.Payload.name == fmt.Sprintf("g%d", nd.ID), "BFS payload corrupted")
			ck(nd.Payload.blob[0] == byte(nd.ID), "BFS blob corrupted")
		}
		if len(order)%32 == 0 {
			gcStress() // 遍历过程中移动对象/触发 GC
		}
	})
	ck(count == n, fmt.Sprintf("BFS count got=%d want=%d", count, n))
	ck(len(visited) == n, "BFS missed nodes")
	ck(order[0] == 0, "BFS did not start at 0")

	// DFS：同样覆盖全部节点，顺序与 BFS 不必相同。
	seen := make(map[int64]bool, n)
	dc := g.DFS(0, func(nd *Node[pcell]) {
		ck(!seen[nd.ID], "DFS visited a node twice")
		seen[nd.ID] = true
		ck(nd.Payload == cells[nd.ID], "DFS payload pointer identity changed")
	})
	ck(dc == n && len(seen) == n, fmt.Sprintf("DFS count got=%d want=%d", dc, n))
	gcStress()
	for i := 0; i < n; i += 37 {
		nd, ok := g.Node(int64(i))
		ck(ok && nd.Payload == cells[i], "post-GC node payload lost")
	}

	// 拓扑：DAG 必须成功，且每条边满足 pos[u] < pos[v]。
	topo, ok := g.Topo()
	ck(ok && len(topo) == n, fmt.Sprintf("topo got len=%d ok=%v want=%d true", len(topo), ok, n))
	pos := make(map[int64]int, n)
	for i, id := range topo {
		pos[id] = i
	}
	edgeChecked := 0
	for _, nd := range g.nodes {
		for _, e := range nd.Edges {
			ck(pos[nd.ID] < pos[e.ID], "topo order violated an edge")
			edgeChecked++
		}
	}
	ck(edgeChecked >= n-1, "topo graph lost tree edges")
	ck(!g.HasCycle(), "DAG reported as cyclic")

	// 环检测：在独立小图上加回边。
	cg := NewGraph[pcell]()
	cc := make([]*pcell, 8)
	for i := range cc {
		cc[i] = &pcell{id: int64(i), name: "cyc"}
		cg.Add(int64(i), cc[i])
	}
	cellSink = append(cellSink, cc...)
	for i := 0; i < 7; i++ {
		cg.Link(int64(i), int64(i+1))
	}
	ck(!cg.HasCycle(), "chain graph falsely reported cyclic")
	cg.Link(7, 0)
	ck(cg.HasCycle(), "cycle not detected")
	_, ok = cg.Topo()
	ck(!ok, "Topo succeeded on cyclic graph")

	// 第二次实例化 Graph[string]：不同负载类型。
	sg := NewGraph[string]()
	strs := make([]*string, 32)
	for i := range strs {
		v := fmt.Sprintf("s%d", i)
		strs[i] = &v
		sg.Add(int64(i), strs[i])
		if i > 0 {
			sg.Link(int64((i-1)/2), int64(i))
		}
	}
	gcStress()
	sc := sg.BFS(0, func(nd *Node[string]) {
		ck(nd.Payload == strs[nd.ID], "Graph[string] payload pointer changed")
		ck(*nd.Payload == fmt.Sprintf("s%d", nd.ID), "Graph[string] payload corrupted")
	})
	ck(sc == 32, fmt.Sprintf("Graph[string] BFS got=%d want=32", sc))

	finish("l01_generic_graph")
}
