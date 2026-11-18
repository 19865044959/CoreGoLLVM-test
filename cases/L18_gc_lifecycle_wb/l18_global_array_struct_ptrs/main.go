// L18: 全局二维结构体数组 [48][8]gslot，每个槽位含 *gnode / string / []byte /
// interface{} 多个指针字段，另有扁平 *gnode 视图同步更新。先整组晋升，再按
// UAF_SEED 稀疏写入年轻对象并立即 GC，校验结构体数组各指针字段与扁平视图一致。
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
	gRows     = 48
	gCols     = 8
	gSlots    = gRows * gCols
	gDataN    = 24
	gRounds   = 4
	gMaxRound = 24
)

type gNode struct {
	v    int64
	name string
}

type gSlot struct {
	n    *gNode
	tag  string
	data []byte
	ifc  interface{}
	ver  int
}

var (
	failed int32
	gcSink [][]byte
	gArr   [gRows][gCols]gSlot
	gFlat  [gSlots]*gNode
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
	if n > gMaxRound {
		n = gMaxRound
	}
	return n
}

func gByte(slot, ver, i int) byte { return byte((slot*19 + ver*13 + i*7) % 251) }

//go:noinline
func setGlobalSlot(r, c, ver int) {
	n := &gNode{v: int64((r*gCols+c)*100 + ver), name: "gs"}
	d := make([]byte, gDataN)
	for i := range d {
		d[i] = gByte(r*gCols+c, ver, i)
	}
	gArr[r][c] = gSlot{n: n, tag: "g" + strconv.Itoa(ver), data: d, ifc: n, ver: ver}
	gFlat[r*gCols+c] = n
}

func verifySlot(r, c, wantVer int, seed int64) bool {
	slot := r*gCols + c
	s := &gArr[r][c]
	if s.ver != wantVer || s.n == nil || s.tag != "g"+strconv.Itoa(wantVer) {
		ck(false, fmt.Sprintf("global array slot %d header corrupted ver=%d want=%d (seed=%d)", slot, s.ver, wantVer, seed))
		return false
	}
	if s.n.v != int64(slot*100+wantVer) || s.n.name != "gs" {
		ck(false, fmt.Sprintf("global array slot %d node corrupted (seed=%d)", slot, seed))
		return false
	}
	if len(s.data) != gDataN {
		ck(false, fmt.Sprintf("global array slot %d data len=%d (seed=%d)", slot, len(s.data), seed))
		return false
	}
	for i := range s.data {
		if s.data[i] != gByte(slot, wantVer, i) {
			ck(false, fmt.Sprintf("global array slot %d data corrupted at %d (seed=%d)", slot, i, seed))
			return false
		}
	}
	in, ok := s.ifc.(*gNode)
	if !ok || in != s.n {
		ck(false, fmt.Sprintf("global array slot %d interface lost or diverged (seed=%d)", slot, seed))
		return false
	}
	if gFlat[slot] != s.n {
		ck(false, fmt.Sprintf("global flat view diverged at slot %d (seed=%d)", slot, seed))
		return false
	}
	return true
}

func verifyAll(exp []int, seed int64) {
	for r := 0; r < gRows; r++ {
		for c := 0; c < gCols; c++ {
			if !verifySlot(r, c, exp[r*gCols+c], seed) {
				return
			}
		}
	}
}

func verifyGlobalZero() {
	for r := 0; r < gRows; r++ {
		for c := 0; c < gCols; c++ {
			s := &gArr[r][c]
			if s.n != nil || s.tag != "" || s.data != nil || s.ifc != nil || s.ver != 0 || gFlat[r*gCols+c] != nil {
				ck(false, fmt.Sprintf("initial global array slot %d not zeroed", r*gCols+c))
				return
			}
		}
	}
}

func main() {
	seed := seedValue()
	rounds := tortureRounds(gRounds)
	rng := rand.New(rand.NewSource(seed))

	verifyGlobalZero()

	// 初始整组写入并晋升为老对象。
	for r := 0; r < gRows; r++ {
		for c := 0; c < gCols; c++ {
			setGlobalSlot(r, c, 1)
		}
	}
	gcStress()
	gcStress()
	exp := make([]int, gSlots)
	for i := range exp {
		exp[i] = 1
	}
	verifyAll(exp, seed)

	for round := 2; round <= rounds+1; round++ {
		updated := 0
		for r := 0; r < gRows; r++ {
			for c := 0; c < gCols; c++ {
				if rng.Intn(3) == 0 {
					setGlobalSlot(r, c, round)
					exp[r*gCols+c] = round
					updated++
				}
			}
		}
		ck(updated > 0, fmt.Sprintf("round %d updated no slots (seed=%d)", round, seed))
		gcStress()
		verifyAll(exp, seed)
	}
	runtime.KeepAlive(&gArr)
	finish("l18_global_array_struct_ptrs")
}
