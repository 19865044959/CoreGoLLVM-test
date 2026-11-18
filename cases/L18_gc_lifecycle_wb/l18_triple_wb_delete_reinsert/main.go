// L18: 老 map/slice/interface 三重写屏障容器做“删除-收缩-置空 → GC →
// 重插-扩容-重设 → GC”循环。map 删除后重插、slice 截断后复用底层数组
// append、interface 清空后重新赋值，全部由 noinline setter 完成。
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
	twKeys    = 128
	twSlots   = 64
	twDel     = 32
	twShrink  = 8
	twDataN   = 24
	twRounds  = 12
	twMaxRnds = 40
)

type twNode struct {
	id   int64
	base int
	ver  int
	name string
	data [twDataN]byte
}

type twHolder struct {
	m   map[int]*twNode
	s   []*twNode
	ifc interface{}
}

var (
	failed int32
	gcSink [][]byte
	twH    twHolder
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
	if n > twMaxRnds {
		n = twMaxRnds
	}
	return n
}

func twByte(base, ver, i int) byte { return byte((base*17 + ver*23 + i*13) % 251) }

//go:noinline
func twMake(base, ver int) *twNode {
	n := &twNode{id: int64(base*1000 + ver), base: base, ver: ver, name: "tw"}
	for i := range n.data {
		n.data[i] = twByte(base, ver, i)
	}
	return n
}

//go:noinline
func twDelete(m map[int]*twNode, k int) { delete(m, k) }

//go:noinline
func twInsert(m map[int]*twNode, k, ver int) { m[k] = twMake(k, ver) }

//go:noinline
func twOverwrite(m map[int]*twNode, k, ver int) { m[k] = twMake(k, ver) }

//go:noinline
func twTruncate(s *[]*twNode) { *s = (*s)[:len(*s)-twShrink] }

//go:noinline
func twAppend(s *[]*twNode, start, ver int) {
	for i := 0; i < twShrink; i++ {
		*s = append(*s, twMake(1000+start+i, ver))
	}
}

//go:noinline
func twClearIface(p *interface{}) { *p = nil }

//go:noinline
func twSetIface(p *interface{}, ver int) { *p = twMake(9000, ver) }

func twCheckNode(n *twNode, base, ver int, seed int64, label string) bool {
	if n == nil || n.base != base || n.ver != ver || n.id != int64(base*1000+ver) || n.name != "tw" {
		ck(false, fmt.Sprintf("%s: node base=%d want=%d ver=%d want=%d (seed=%d)", label, n.base, base, n.ver, ver, seed))
		return false
	}
	for i := range n.data {
		if n.data[i] != twByte(base, ver, i) {
			ck(false, fmt.Sprintf("%s: node base=%d ver=%d data corrupted at %d (seed=%d)", label, base, ver, i, seed))
			return false
		}
	}
	return true
}

func main() {
	seed := seedValue()
	rounds := tortureRounds(twRounds)

	twH.m = make(map[int]*twNode, twKeys)
	for k := 0; k < twKeys; k++ {
		twH.m[k] = twMake(k, 1)
	}
	twH.s = make([]*twNode, twSlots)
	for i := 0; i < twSlots; i++ {
		twH.s[i] = twMake(1000+i, 1)
	}
	twH.ifc = twMake(9000, 1)
	gcStress()
	gcStress()

	expKey := make([]int, twKeys)
	for i := range expKey {
		expKey[i] = 1
	}
	expSlot := make([]int, twSlots)
	for i := range expSlot {
		expSlot[i] = 1
	}
	expIface := 1

	rng := rand.New(rand.NewSource(seed))
	for r := 2; r <= rounds+1; r++ {
		perm := rng.Perm(twKeys)
		delKeys := perm[:twDel]
		overKeys := perm[twDel : 2*twDel]

		// 阶段一：删除 map entry、截断老 slice、清空 interface，然后 GC。
		for _, k := range delKeys {
			twDelete(twH.m, k)
			expKey[k] = -1
		}
		twTruncate(&twH.s)
		twClearIface(&twH.ifc)
		gcStress()

		if len(twH.m) != twKeys-twDel {
			ck(false, fmt.Sprintf("round %d: map len=%d want=%d after delete (seed=%d)", r, len(twH.m), twKeys-twDel, seed))
			break
		}
		bad := false
		for k := 0; k < twKeys; k++ {
			if expKey[k] < 0 {
				if _, ok := twH.m[k]; ok {
					ck(false, fmt.Sprintf("round %d: deleted key %d still present (seed=%d)", r, k, seed))
					bad = true
					break
				}
			} else if !twCheckNode(twH.m[k], k, expKey[k], seed, "map") {
				bad = true
				break
			}
		}
		if bad {
			break
		}
		if len(twH.s) != twSlots-twShrink {
			ck(false, fmt.Sprintf("round %d: slice len=%d want=%d after shrink (seed=%d)", r, len(twH.s), twSlots-twShrink, seed))
			break
		}
		for i := 0; i < len(twH.s); i++ {
			if !twCheckNode(twH.s[i], 1000+i, expSlot[i], seed, "slice") {
				bad = true
				break
			}
		}
		if bad {
			break
		}
		if twH.ifc != nil {
			ck(false, fmt.Sprintf("round %d: interface not cleared (seed=%d)", r, seed))
			break
		}

		// 阶段二：重插 map key、复用老底层数组 append、重设 interface，然后 GC。
		for _, k := range delKeys {
			twInsert(twH.m, k, r)
			expKey[k] = r
		}
		for _, k := range overKeys {
			twOverwrite(twH.m, k, r)
			expKey[k] = r
		}
		twAppend(&twH.s, twSlots-twShrink, r)
		for i := twSlots - twShrink; i < twSlots; i++ {
			expSlot[i] = r
		}
		twSetIface(&twH.ifc, r)
		expIface = r
		gcStress()

		if len(twH.m) != twKeys {
			ck(false, fmt.Sprintf("round %d: map len=%d want=%d after reinsert (seed=%d)", r, len(twH.m), twKeys, seed))
			break
		}
		for k := 0; k < twKeys; k++ {
			if !twCheckNode(twH.m[k], k, expKey[k], seed, "map-reinsert") {
				bad = true
				break
			}
		}
		if bad {
			break
		}
		if len(twH.s) != twSlots {
			ck(false, fmt.Sprintf("round %d: slice len=%d want=%d after append (seed=%d)", r, len(twH.s), twSlots, seed))
			break
		}
		for i := 0; i < twSlots; i++ {
			if !twCheckNode(twH.s[i], 1000+i, expSlot[i], seed, "slice-append") {
				bad = true
				break
			}
		}
		if bad {
			break
		}
		in, ok := twH.ifc.(*twNode)
		if !ok || !twCheckNode(in, 9000, expIface, seed, "iface") {
			ck(false, fmt.Sprintf("round %d: interface lost or type changed (seed=%d)", r, seed))
			break
		}
	}

	finish("l18_triple_wb_delete_reinsert")
}
