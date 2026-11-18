// L05: 带环对象图的代际 churn：每轮构建 48 张含环、共享 hub 与交叉边的图，
// pin 少量图根并丢弃其余；GC + 垃圾分配后递归校验，改写 pin 图的边再 GC。
// 默认 2 轮，TORTURE_ROUNDS 可放大。
package main

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"sync/atomic"
)

const (
	graphPerRound = 48
	graphNodes    = 24
	graphRing     = 8
	graphPins     = 3
)

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

func ckf(ok bool, format string, args ...interface{}) {
	if !ok {
		atomic.AddInt32(&failed, 1)
		fmt.Printf("FAIL: "+format+"\n", args...)
	}
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
		return hi
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

func gcStress() {
	for i := 0; i < 24; i++ {
		b := make([]byte, 8<<10)
		b[0] = byte(i)
		gcSink = append(gcSink[:0], b)
	}
	gcSink = gcSink[:0]
	runtime.GC()
	runtime.GC()
}

type gnode struct {
	id   int
	tag  string
	data []byte
	out  []*gnode
}

type graphPin struct {
	root  *gnode
	alt   *gnode
	count int
	sum   uint64
}

// digestGraph 用显式栈 + visited 处理环；只做求和，与遍历顺序无关。
func digestGraph(root *gnode) (int, uint64) {
	seen := make(map[*gnode]bool, graphNodes+2)
	stack := []*gnode{root}
	count := 0
	var sum uint64
	for len(stack) > 0 {
		n := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if n == nil || seen[n] {
			continue
		}
		seen[n] = true
		count++
		sum = sum*1099511628211 + uint64(n.id)*131 + uint64(len(n.out))*7
		for i := 0; i < len(n.tag); i++ {
			sum = sum*1099511628211 + uint64(n.tag[i])
		}
		for i := range n.data {
			sum = sum*1099511628211 + uint64(n.data[i])
		}
		for _, o := range n.out {
			if o != nil && !seen[o] {
				stack = append(stack, o)
			}
		}
	}
	return count, sum
}

func graphByte(gen, g, i, j int, seed uint64) byte {
	return byte((seed*13 + uint64(gen)*101 + uint64(g)*17 + uint64(i)*7 + uint64(j)*3) % 251)
}

// buildCycleGraph 返回根和可变节点；图含 8 节点环、16 节点树、回边与共享 hub。
func buildCycleGraph(gen, g int, seed uint64, hub *gnode) (*gnode, *gnode) {
	nodes := make([]*gnode, graphNodes)
	for i := 0; i < graphNodes; i++ {
		data := make([]byte, 24+(i*11+g*5)%48)
		for j := range data {
			data[j] = graphByte(gen, g, i, j, seed)
		}
		nodes[i] = &gnode{
			id:   gen*100000 + g*1000 + i,
			tag:  fmt.Sprintf("g%d-%d", g, i),
			data: data,
		}
	}
	for i := 0; i < graphRing; i++ {
		nodes[i].out = []*gnode{nodes[(i+1)%graphRing], nodes[(i+3)%graphRing], hub}
	}
	for i := graphRing + 1; i < graphNodes; i++ {
		parent := graphRing + (i-graphRing)/2
		nodes[parent].out = append(nodes[parent].out, nodes[i])
		nodes[i].out = []*gnode{nodes[graphRing+((i-graphRing+1)%(graphNodes-graphRing))], hub}
	}
	nodes[graphRing].out = append(nodes[graphRing].out, hub)
	nodes[graphNodes-1].out = append(nodes[graphNodes-1].out, nodes[graphRing], nodes[0])
	return nodes[graphRing], nodes[16]
}

func verifyPin(tag string, p *graphPin) {
	c, s := digestGraph(p.root)
	ckf(c == p.count, "%s count=%d want=%d", tag, c, p.count)
	ckf(s == p.sum, "%s digest changed", tag)
}

func main() {
	seed := loadSeed()
	rounds := envInt("TORTURE_ROUNDS", 2, 1, 8)

	hub := &gnode{id: -7, tag: "hub", data: make([]byte, 96)}
	for i := range hub.data {
		hub.data[i] = graphByte(99, 99, 99, i, seed)
	}
	hub.out = []*gnode{hub} // 自环

	pins := make([]graphPin, 0, rounds*graphPins)
	var carried *gnode

	for round := 0; round < rounds; round++ {
		var roundPins []graphPin
		for g := 0; g < graphPerRound; g++ {
			root, alt := buildCycleGraph(round, g, seed, hub)
			c, s := digestGraph(root)
			ckf(c == graphNodes+1, "round=%d g=%d reachable=%d want=%d", round, g, c, graphNodes+1)
			if g < graphPins {
				roundPins = append(roundPins, graphPin{root: root, alt: alt, count: c, sum: s})
			}
			if g == graphPins {
				carried = root // 暂时仅由局部变量持有，GC 后仍应有效
			}
		}

		gcStress()
		for i := range roundPins {
			verifyPin(fmt.Sprintf("round=%d pin=%d after build+GC", round, i), &roundPins[i])
		}
		if carried != nil {
			if c, s := digestGraph(carried); c != graphNodes+1 || s == 0 {
				ckf(false, "round=%d carried graph corrupted after build+GC", round)
			}
			ck(carried.out[0] != nil || len(carried.out) > 0, "carried graph lost edges")
		}

		// 改写 pin 图的边并加自环，验证跨 GC 的旧→新引用与环扫描。
		for i := range roundPins {
			p := &roundPins[i]
			p.alt.out = append(p.alt.out, p.root, hub)
			p.alt.tag = "mut"
			p.count, p.sum = digestGraph(p.root)
		}
		carried = nil
		gcStress()
		for i := range roundPins {
			verifyPin(fmt.Sprintf("round=%d pin=%d after rewire+GC", round, i), &roundPins[i])
		}

		pins = append(pins, roundPins...)
		// 丢掉本轮除 pin 以外的全部图，只留 hub 与 pin；再制造垃圾并 GC。
		roundPins = nil
		runtime.GC()
		gcStress()
		for i := range pins {
			verifyPin(fmt.Sprintf("round=%d keep=%d after drop+GC", round, i), &pins[i])
		}
		ckf(len(pins) >= graphPins, "round=%d pinned graphs lost", round)
	}

	// 重建一代新图，与所有旧 pin 图共存；再 GC 后一起校验。
	fresh := make([]*gnode, 0, graphPerRound)
	for g := 0; g < graphPerRound; g++ {
		root, _ := buildCycleGraph(rounds+50, g, seed, hub)
		fresh = append(fresh, root)
	}
	gcStress()
	for g := range fresh {
		if c, s := digestGraph(fresh[g]); c != graphNodes+1 || s == 0 {
			ckf(false, "fresh graph=%d corrupted after GC", g)
		}
	}
	for i := range pins {
		verifyPin(fmt.Sprintf("final pin=%d", i), &pins[i])
	}
	ckf(len(pins) == rounds*graphPins, "pinned total=%d want=%d", len(pins), rounds*graphPins)

	// 释放新图与 pin，仅保留 hub，确认环图可整体成为垃圾。
	fresh = nil
	pins = nil
	gcStress()
	if c, _ := digestGraph(hub); c != 1 {
		ck(false, "hub self-cycle corrupted")
	}
	runtime.KeepAlive(seed)

	finish("l05_graph_cycle_churn", seed)
}
