// L18: 老对象持有的 map / slice / interface 三种容器同时写入年轻对象。
// 每个 setter 均为 noinline，返回后年轻对象只被老容器引用；写入后立即 GC，
// 按种子决定的期望代次逐项校验，未写槽位必须保持上一代内容。
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
	triMapN     = 128
	triSliceN   = 64
	triRounds   = 20
	triPerRound = 24
	triDataN    = 24
)

type triNode struct {
	slot int
	ver  int
	name string
	data [triDataN]byte
}

type triHolder struct {
	m   map[string]*triNode
	s   []*triNode
	ifc interface{}
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

func keyOf(i int) string { return fmt.Sprintf("k%03d", i) }

func triByte(slot, ver, i int) byte { return byte((slot*17 + ver*11 + i*5) % 251) }

//go:noinline
func newTriNode(slot, ver int) *triNode {
	n := &triNode{slot: slot, ver: ver, name: "tri"}
	for i := range n.data {
		n.data[i] = triByte(slot, ver, i)
	}
	return n
}

//go:noinline
func buildTriHolder() *triHolder {
	h := &triHolder{m: make(map[string]*triNode, triMapN), s: make([]*triNode, triSliceN)}
	for i := 0; i < triMapN; i++ {
		h.m[keyOf(i)] = newTriNode(i, 0)
	}
	for i := 0; i < triSliceN; i++ {
		h.s[i] = newTriNode(i, 0)
	}
	h.ifc = newTriNode(999, 0)
	gcStress()
	gcStress() // holder/map/slice/初始对象全部晋升为老对象
	return h
}

//go:noinline
func setMapVal(h *triHolder, key string, slot, ver int) {
	h.m[key] = newTriNode(slot, ver)
}

//go:noinline
func setSliceVal(h *triHolder, idx, ver int) {
	h.s[idx] = newTriNode(idx, ver)
}

//go:noinline
func setIfaceVal(h *triHolder, slot, ver int) {
	h.ifc = newTriNode(slot, ver)
}

func checkNode(n *triNode, slot, ver int) bool {
	if n == nil || n.slot != slot || n.ver != ver || n.name != "tri" {
		return false
	}
	for i := range n.data {
		if n.data[i] != triByte(slot, ver, i) {
			return false
		}
	}
	return true
}

func verifyTri(h *triHolder, expMap []int, expSlice []int, expIfaceVer, expIfaceSlot int, seed int64) {
	for i := 0; i < triMapN; i++ {
		if !checkNode(h.m[keyOf(i)], i, expMap[i]) {
			ck(false, fmt.Sprintf("map entry %s ver=%d corrupted (seed=%d)", keyOf(i), expMap[i], seed))
			return
		}
	}
	for i := 0; i < triSliceN; i++ {
		if !checkNode(h.s[i], i, expSlice[i]) {
			ck(false, fmt.Sprintf("slice slot %d ver=%d corrupted (seed=%d)", i, expSlice[i], seed))
			return
		}
	}
	in, ok := h.ifc.(*triNode)
	if !ok {
		ck(false, fmt.Sprintf("interface dynamic type lost (seed=%d)", seed))
		return
	}
	if !checkNode(in, expIfaceSlot, expIfaceVer) {
		ck(false, fmt.Sprintf("interface payload slot=%d ver=%d corrupted (seed=%d)", expIfaceSlot, expIfaceVer, seed))
	}
}

func main() {
	seed := seedValue()
	rng := rand.New(rand.NewSource(seed))
	h := buildTriHolder()

	expMap := make([]int, triMapN)
	expSlice := make([]int, triSliceN)
	expIfaceVer := 0
	expIfaceSlot := 999
	verifyTri(h, expMap, expSlice, expIfaceVer, expIfaceSlot, seed)

	keyOrder := make([]int, 0, triMapN)
	slotOrder := make([]int, 0, triSliceN)
	for r := 1; r <= triRounds; r++ {
		// 每轮重新打乱，种子决定选择顺序；选择集合本身固定为前 triPerRound 个。
		keyOrder = rng.Perm(triMapN)
		slotOrder = rng.Perm(triSliceN)
		for i := 0; i < triPerRound; i++ {
			ki := keyOrder[i]
			setMapVal(h, keyOf(ki), ki, r)
			expMap[ki] = r

			si := slotOrder[i]
			setSliceVal(h, si, r)
			expSlice[si] = r
		}
		ifaceSlot := keyOrder[triPerRound] + 1000
		setIfaceVal(h, ifaceSlot, r)
		expIfaceVer = r
		expIfaceSlot = ifaceSlot

		gcStress()
		verifyTri(h, expMap, expSlice, expIfaceVer, expIfaceSlot, seed)
	}
	runtime.KeepAlive(h)
	finish("l18_map_slice_iface_triple_wb")
}
