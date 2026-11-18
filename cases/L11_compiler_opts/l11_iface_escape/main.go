// L11: 经接口逃逸——对象只通过 any/参数/返回值/接口字段被外部可见，
// 逃逸分析必须让其堆分配并在 GC 后保持完整；类型断言、typed nil、
// map/slice 容器中的接口值语义必须一致。
package main

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"sync/atomic"
)

type ieNode struct {
	id   int64
	tag  string
	data []int64
	next *ieNode
}

type ieScorer struct {
	base int64
	mult int64
}

func (s *ieScorer) Score(x int64) int64 { return (x + s.base) * s.mult }
func (s *ieScorer) Name() string        { return "scorer" }

type ieGrader interface {
	Score(int64) int64
	Name() string
}

type ieHolder struct {
	slot  any
	g     ieGrader
	label string
}

var (
	failed      int32
	gcSink      [][]byte
	seed        uint64 = 1
	ieAll       []any
	ieExpectIDs []int64
	ieKeep      []*ieNode
	ieCells     []*ieHolder
)

func ck(ok bool, msg string) {
	if !ok {
		atomic.AddInt32(&failed, 1)
		fmt.Printf("FAIL: %s (UAF_SEED=%d)\n", msg, seed)
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

func tortureMultiplier() int {
	s := os.Getenv("TORTURE_ROUNDS")
	if s == "" {
		return 1
	}
	n, err := strconv.Atoi(s)
	if err != nil || n < 1 {
		return 1
	}
	if n > 50 {
		n = 50
	}
	return n
}

func finish(name string) {
	if n := atomic.LoadInt32(&failed); n != 0 {
		fmt.Printf("FAILED %s (%d failed checks, seed=%d)\n", name, n, seed)
		os.Exit(1)
	}
	fmt.Println("ALL PASS " + name)
}

func ieStore(dst *[]any, v any) {
	*dst = append(*dst, v)
}

func ieWrap(v any) any { return v }

func ieNodeSum(n *ieNode) int64 {
	var s int64
	for _, v := range n.data {
		s += v
	}
	return s
}

func main() {
	seed = loadSeed()
	if seed == 0 {
		seed = 1
	}
	rounds := 4 * tortureMultiplier()

	for r := 0; r < rounds; r++ {
		n := 24 + int((seed+uint64(r)*7)%24)
		base := int64(1000 * (r + 1))
		nodes := make([]*ieNode, n)
		graders := make([]ieGrader, n)
		m := make(map[string]*ieNode, n)

		for i := 0; i < n; i++ {
			l := 3 + i%3
			d := make([]int64, l)
			for j := range d {
				d[j] = base + int64(i*5+j)
			}
			node := &ieNode{id: base + int64(i), tag: fmt.Sprintf("n%d-%d", r, i), data: d}
			if i > 0 {
				node.next = nodes[i-1]
			}
			nodes[i] = node
			graders[i] = &ieScorer{base: int64(i), mult: 2}
			m[strconv.Itoa(i)] = node
			ieStore(&ieAll, node)
			ieExpectIDs = append(ieExpectIDs, node.id)
			ieKeep = append(ieKeep, node)
		}

		holder := &ieHolder{slot: nodes[n/2], g: graders[n/2], label: fmt.Sprintf("round-%d", r)}
		ieCells = append(ieCells, holder)

		// 接口通道/字段/wrapper 之外再显式 GC，验证对象由接口值保活。
		gcStress()

		sinkBase := len(ieAll) - n
		sinkOK := true
		for i := 0; i < n; i++ {
			p, ok := ieAll[sinkBase+i].(*ieNode)
			if !ok || p == nil || p.id != base+int64(i) {
				sinkOK = false
				break
			}
			if ieNodeSum(p) != int64(3+i%3)*(base+int64(i*5))+int64((3+i%3)*((3+i%3)-1)/2) {
				sinkOK = false
				break
			}
		}
		ck(sinkOK, "interface sink values after GC")

		hp, ok := holder.slot.(*ieNode)
		ck(ok && hp != nil && hp.id == base+int64(n/2), "interface struct field after GC")
		ck(holder.g != nil && holder.g.Name() == "scorer", "interface grader field after GC")
		if holder.g != nil {
			ck(holder.g.Score(3) == (3+int64(n/2))*2, "interface method through field")
		}

		chainLen := 0
		for q := nodes[n-1]; q != nil; q = q.next {
			chainLen++
		}
		ck(chainLen == n, "pointer chain through interface-retained nodes")

		old := nodes[0].id
		wrapped := ieWrap(nodes[0])
		nodes[0].id = old + 1000
		wp, wok := wrapped.(*ieNode)
		ck(wok && wp != nil && wp.id == old+1000, "wrapper interface observes mutation")
		nodes[0].id = old

		for i := 0; i < n; i++ {
			key := strconv.Itoa(i)
			if m[key] == nil || m[key].id != base+int64(i) {
				ck(false, "map interface value after GC")
				break
			}
			if i%2 == 0 {
				delete(m, key)
			}
		}
		gcStress()
		ck(len(m) == n/2, "map length after delete and GC")
		mapOK := true
		for i := 1; i < n; i += 2 {
			p := m[strconv.Itoa(i)]
			if p == nil || p.id != base+int64(i) || p.tag == "" {
				mapOK = false
				break
			}
		}
		ck(mapOK, "map odd values after GC")

		var graderSum int64
		var wantSum int64
		for i := 0; i < n; i++ {
			graderSum += graders[i].Score(3)
			wantSum += (3 + int64(i)) * 2
		}
		ck(graderSum == wantSum, "interface slice dispatch after GC")

		var np *ieNode
		var typedNil any = np
		ck(typedNil != nil, "typed nil interface is not nil")
		got, tok := typedNil.(*ieNode)
		ck(tok && got == nil, "typed nil assertion returns nil pointer")
	}

	gcStress()
	allOK := len(ieAll) == len(ieExpectIDs)
	if allOK {
		for i := range ieAll {
			p, ok := ieAll[i].(*ieNode)
			if !ok || p == nil || p.id != ieExpectIDs[i] {
				allOK = false
				break
			}
		}
	}
	ck(allOK, "all interface-sunk nodes after final GC")

	cellOK := true
	for _, h := range ieCells {
		p, ok := h.slot.(*ieNode)
		if !ok || p == nil || h.g == nil || h.label == "" {
			cellOK = false
			break
		}
	}
	ck(cellOK, "retained interface holders after final GC")

	finish("l11_iface_escape")
}
