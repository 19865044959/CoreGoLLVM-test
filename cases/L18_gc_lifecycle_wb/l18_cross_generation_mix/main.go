// L18: 三代对象跨代写。gen0 槽位最老、gen1 子节点次老（经额外 GC 晋升）、
// gen2 每轮全新。noinline attachCross 创建 gen2 对象后按 UAF_SEED 顺序写入：
//
//	老->新（next/any）、新->老（prev/interface）、新->新（chain/kids）。
//
// 每轮 GC 后沿槽位遍历三代对象，校验双向链、接口反向引用与数据。
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
	cgSlots  = 12
	cgMids   = 4
	cgRounds = 20
	cgDataN  = 24
)

type cgNode struct {
	id   int64
	gen  int
	name string
	data [cgDataN]byte
	next *cgNode
	prev *cgNode
	any  interface{}
	kids []*cgNode
}

type cgHolder struct {
	slots []*cgNode
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

func cgByte(id int64, i int) byte { return byte((int(id)*31 + i*17) % 251) }

//go:noinline
func buildCohort(gen, n int, idBase int64) []*cgNode {
	out := make([]*cgNode, n)
	for i := 0; i < n; i++ {
		id := idBase + int64(i)
		node := &cgNode{id: id, gen: gen, name: fmt.Sprintf("g%d", gen)}
		for j := range node.data {
			node.data[j] = cgByte(id, j)
		}
		out[i] = node
	}
	return out
}

//go:noinline
func attachMid(slot, a, b *cgNode) {
	slot.kids = []*cgNode{a, b}
}

// 以下 link* 都是 noinline setter，顺序由 attachCross 内的种子排列决定。
//
//go:noinline
func linkOldNext(old, y *cgNode) {
	old.next = y
	y.prev = old
}

//go:noinline
func linkOldAny(old, y *cgNode) { old.any = y }

//go:noinline
func linkMidAny(mid, y2 *cgNode) { mid.any = y2 }

//go:noinline
func linkYoungKids(y2, mid *cgNode) { y2.kids = []*cgNode{mid, mid} }

//go:noinline
func linkYoungChain(y, y2 *cgNode) {
	y.next = y2
	y2.prev = y
}

//go:noinline
func linkYoungToOldIface(y, old *cgNode) { y.any = old }

//go:noinline
func attachCross(old, mid *cgNode, v int64, seed int64) {
	y := &cgNode{id: v, gen: 2, name: "g2"}
	y2 := &cgNode{id: v + 1, gen: 2, name: "g2"}
	for i := range y.data {
		y.data[i] = cgByte(v, i)
		y2.data[i] = cgByte(v+1, i)
	}
	rng := rand.New(rand.NewSource(seed ^ v))
	for _, k := range rng.Perm(6) {
		switch k {
		case 0:
			linkOldNext(old, y)
		case 1:
			linkOldAny(old, y)
		case 2:
			linkMidAny(mid, y2)
		case 3:
			linkYoungKids(y2, mid)
		case 4:
			linkYoungChain(y, y2)
		case 5:
			linkYoungToOldIface(y, old)
		}
	}
}

func checkCg(n *cgNode, id int64, gen int, name string) bool {
	if n == nil || n.id != id || n.gen != gen || n.name != name {
		return false
	}
	for i := range n.data {
		if n.data[i] != cgByte(id, i) {
			return false
		}
	}
	return true
}

func verifyCross(h *cgHolder, mids []*cgNode, expNext []int64, expMidAny []int64, seed int64) {
	for i, s := range h.slots {
		if !checkCg(s, int64(i), 0, "g0") {
			ck(false, fmt.Sprintf("gen0 slot %d corrupted (seed=%d)", i, seed))
			return
		}
		if expNext[i] == 0 {
			if s.next != nil || s.any != nil {
				ck(false, fmt.Sprintf("gen0 slot %d unexpected link (seed=%d)", i, seed))
				return
			}
			continue
		}
		y := s.next
		if !checkCg(y, expNext[i], 2, "g2") || y.prev != s {
			ck(false, fmt.Sprintf("gen0 slot %d old->young next link lost (seed=%d)", i, seed))
			return
		}
		if in, ok := s.any.(*cgNode); !ok || in != y {
			ck(false, fmt.Sprintf("gen0 slot %d old->young interface link lost (seed=%d)", i, seed))
			return
		}
		if in, ok := y.any.(*cgNode); !ok || in != s {
			ck(false, fmt.Sprintf("gen2 node %d young->old interface link lost (seed=%d)", y.id, seed))
			return
		}
		if !checkCg(y.next, expNext[i]+1, 2, "g2") || y.next.prev != y {
			ck(false, fmt.Sprintf("gen2 chain from slot %d lost (seed=%d)", i, seed))
			return
		}
	}
	for j, m := range mids {
		if !checkCg(m, int64(100+j), 1, "g1") {
			ck(false, fmt.Sprintf("gen1 mid %d corrupted (seed=%d)", j, seed))
			return
		}
		if expMidAny[j] == 0 {
			if m.any != nil {
				ck(false, fmt.Sprintf("gen1 mid %d unexpected interface link (seed=%d)", j, seed))
				return
			}
			continue
		}
		in, ok := m.any.(*cgNode)
		if !ok || in.id != expMidAny[j] {
			ck(false, fmt.Sprintf("gen1 mid %d old->young interface link lost (seed=%d)", j, seed))
			return
		}
		if len(in.kids) != 2 || in.kids[0] != m || in.kids[1] != m {
			ck(false, fmt.Sprintf("gen2 node %d young->old kids link lost (seed=%d)", in.id, seed))
			return
		}
	}
}

func main() {
	seed := seedValue()
	rng := rand.New(rand.NewSource(seed))

	h := &cgHolder{slots: buildCohort(0, cgSlots, 0)}
	gcStress()
	gcStress()
	gcStress() // gen0 最老

	mids := buildCohort(1, cgMids, 100)
	for i, m := range mids {
		attachMid(h.slots[i%cgSlots], m, h.slots[(i+1)%cgSlots])
	}
	gcStress()
	gcStress() // gen1 次老；attachMid 的 老->年轻 写入也必须被屏障保住
	for i, s := range h.slots {
		if i < cgMids && (len(s.kids) != 2 || s.kids[0] != mids[i] || s.kids[1] != h.slots[(i+1)%cgSlots]) {
			ck(false, fmt.Sprintf("gen0 slot %d mid kids corrupted (seed=%d)", i, seed))
		}
	}

	expNext := make([]int64, cgSlots)
	expMidAny := make([]int64, cgMids)
	for r := 1; r <= cgRounds; r++ {
		oi := rng.Intn(cgSlots)
		mi := rng.Intn(cgMids)
		v := int64(1000 + r*10)
		attachCross(h.slots[oi], mids[mi], v, seed)
		expNext[oi] = v
		expMidAny[mi] = v + 1
		gcStress()
		verifyCross(h, mids, expNext, expMidAny, seed)
	}
	runtime.KeepAlive(h)
	runtime.KeepAlive(mids)
	finish("l18_cross_generation_mix")
}
