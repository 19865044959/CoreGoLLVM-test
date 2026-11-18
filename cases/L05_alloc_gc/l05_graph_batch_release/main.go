// L05: 批量构建对象图（每棵 1+3+6 个含 payload/links 的节点），整批校验后丢弃并 GC，
// 再整批重建；少量图长期持有，验证旧图在批量释放/重建与多轮 GC 后仍完整。
package main

import (
	"fmt"
	"hash/crc32"
	"os"
	"runtime"
	"strconv"
	"sync/atomic"
)

const (
	graphCount    = 160
	payloadSize   = 192
	defaultRounds = 3
)

var (
	failed int32
	gcSink [][]byte
)

type gnode struct {
	id      int
	ver     int
	payload []byte
	links   []*gnode
	crc     uint32
}

type graph struct {
	root  *gnode
	nodes []*gnode
}

func ck(ok bool, msg string) {
	if !ok {
		atomic.AddInt32(&failed, 1)
		fmt.Println("FAIL:", msg)
	}
}

func ckf(ok bool, format string, args ...interface{}) {
	if !ok {
		atomic.AddInt32(&failed, 1)
		fmt.Printf("FAIL: "+format+"\n", args...)
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

func loadSeed() uint64 {
	s := os.Getenv("UAF_SEED")
	if s == "" {
		return 1
	}
	n, err := strconv.ParseUint(s, 10, 64)
	if err != nil {
		return 1
	}
	return n
}

func envInt(name string, def, lo, hi int) int {
	s := os.Getenv(name)
	if s == "" {
		return def
	}
	n, err := strconv.Atoi(s)
	if err != nil || n < lo {
		return def
	}
	if n > hi {
		n = hi
	}
	return n
}

func finish(name string, seed uint64) {
	if n := atomic.LoadInt32(&failed); n != 0 {
		fmt.Printf("FAILED %s (%d failed checks, seed=%d)\n", name, n, seed)
		os.Exit(1)
	}
	fmt.Println("ALL PASS " + name)
}

func nodeByte(id, ver int, seed uint64, i int) byte {
	base := uint64(id)*37 + uint64(ver)*101 + seed*13 + 9
	return byte((base + uint64(i)*7) % 251)
}

func newNode(id, ver int, seed uint64) *gnode {
	p := make([]byte, payloadSize)
	for i := range p {
		p[i] = nodeByte(id, ver, seed, i)
	}
	return &gnode{id: id, ver: ver, payload: p, crc: crc32.ChecksumIEEE(p)}
}

func buildGraph(rootID, ver int, seed uint64) *graph {
	base := rootID * 100
	root := newNode(base, ver, seed)
	g := &graph{root: root, nodes: []*gnode{root}}
	for a := 1; a <= 3; a++ {
		child := newNode(base+a, ver, seed)
		for b := 1; b <= 2; b++ {
			grand := newNode(base+a*10+b, ver, seed)
			child.links = append(child.links, grand)
			g.nodes = append(g.nodes, grand)
		}
		root.links = append(root.links, child)
		g.nodes = append(g.nodes, child)
	}
	return g
}

func verifyNode(n *gnode, ver int, seed uint64) bool {
	if n == nil || n.ver != ver || len(n.payload) != payloadSize || len(n.links) > 3 {
		return false
	}
	for i := 0; i < payloadSize; i += 23 {
		if n.payload[i] != nodeByte(n.id, ver, seed, i) {
			return false
		}
	}
	if crc32.ChecksumIEEE(n.payload) != n.crc {
		return false
	}
	switch rem := n.id % 100; {
	case rem == 0:
		if len(n.links) != 3 {
			return false
		}
	case rem < 10:
		if len(n.links) != 2 {
			return false
		}
	default:
		if len(n.links) != 0 {
			return false
		}
	}
	for _, c := range n.links {
		if !verifyNode(c, ver, seed) {
			return false
		}
	}
	return true
}

func verifyGraph(g *graph, rootID, ver int, seed uint64) bool {
	if g == nil || g.root == nil || len(g.nodes) != 10 {
		return false
	}
	if g.root.id != rootID*100 {
		return false
	}
	for _, n := range g.nodes {
		if n == nil || len(n.payload) != payloadSize {
			return false
		}
	}
	return verifyNode(g.root, ver, seed)
}

func main() {
	seed := loadSeed()
	rounds := envInt("TORTURE_ROUNDS", defaultRounds, 1, 12)

	var pinned []*graph
	var pinnedRoot, pinnedVer []int
	lastMade := 0

	for round := 0; round < rounds; round++ {
		ver := round + 1
		current := make([]*graph, 0, graphCount)
		for id := 0; id < graphCount; id++ {
			current = append(current, buildGraph(id, ver, seed))
			if id%3 == 0 {
				junk := make([]byte, 8<<10)
				junk[0] = byte(id)
				gcSink = append(gcSink[:0], junk)
			}
		}
		gcSink = gcSink[:0]
		gcStress()
		for id, g := range current {
			if !verifyGraph(g, id, ver, seed) {
				ckf(false, "round=%d graph=%d corrupted after build+GC", round, id)
				break
			}
		}
		lastMade = ver

		// 长期保留 4 张图，其余整批丢弃后 GC。
		for _, id := range []int{0, 40, 80, 120} {
			pinned = append(pinned, current[id])
			pinnedRoot = append(pinnedRoot, id)
			pinnedVer = append(pinnedVer, ver)
		}
		current = nil
		for i := 0; i < 24; i++ {
			junk := make([]byte, 128<<10)
			junk[0] = byte(round + i)
			gcSink = append(gcSink[:0], junk)
		}
		gcSink = gcSink[:0]
		runtime.GC()
		runtime.GC()

		// 重建整批新版本图，同时校验旧图仍存活。
		rebuilt := make([]*graph, 0, graphCount)
		for id := 0; id < graphCount; id++ {
			rebuilt = append(rebuilt, buildGraph(id, ver+1, seed))
		}
		gcStress()
		for id, g := range rebuilt {
			if !verifyGraph(g, id, ver+1, seed) {
				ckf(false, "round=%d rebuilt graph=%d corrupted", round, id)
				break
			}
		}
		for k := range pinned {
			if !verifyGraph(pinned[k], pinnedRoot[k], pinnedVer[k], seed) {
				ckf(false, "round=%d pinned graph=%d ver=%d corrupted after rebuild+GC", round, pinnedRoot[k], pinnedVer[k])
			}
		}
		rebuilt = nil
		lastMade = ver + 1
	}

	// 释放全部新建图后，长期持有的图仍需完整。
	gcStress()
	for k := range pinned {
		if !verifyGraph(pinned[k], pinnedRoot[k], pinnedVer[k], seed) {
			ckf(false, "final pinned graph=%d ver=%d corrupted", pinnedRoot[k], pinnedVer[k])
		}
	}
	ck(lastMade > 0 && len(pinned) == 4*rounds, "graph rounds accounting")
	runtime.KeepAlive(pinned)

	finish("l05_graph_batch_release", seed)
}
