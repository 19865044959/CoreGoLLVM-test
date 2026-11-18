// L18: 全局二维结构体数组 [3][40]grSlot 分代轮转。每轮整行 noinline 覆写，
// 三个行始终保留不同代次的指针/interface/slice；GC 后全量校验 120 个槽位
// 的别名关系（n==ifc==kids[0]、kid==kids[1]）与扁平指针视图。
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
	grRows    = 3
	grCols    = 40
	grDataN   = 20
	grRounds  = 6
	grMaxRoun = 36
)

type grNode struct {
	id   int64
	base int
	ver  int
	name string
	data [grDataN]byte
}

type grSlot struct {
	n    *grNode
	kid  *grNode
	ifc  interface{}
	kids []*grNode
	tag  string
	ver  int
}

var (
	failed int32
	gcSink [][]byte
	grArr  [grRows][grCols]grSlot
	grFlat [grRows][grCols]*grNode
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
	if n > grMaxRoun {
		n = grMaxRoun
	}
	return n
}

func grByte(base, ver, i int) byte { return byte((base*23 + ver*31 + i*7) % 251) }

//go:noinline
func grMakeNode(base, ver, off int) *grNode {
	id := int64(base*100 + ver*10 + off)
	n := &grNode{id: id, base: base, ver: ver, name: "gr"}
	for i := range n.data {
		n.data[i] = grByte(base+off, ver, i)
	}
	return n
}

//go:noinline
func fillGlobalRow(row, ver int, seed int64) {
	rng := rand.New(rand.NewSource(seed ^ int64(row*97+ver*13)))
	for _, col := range rng.Perm(grCols) {
		base := row*1000 + col
		n := grMakeNode(base, ver, 0)
		kid := grMakeNode(base, ver, 1)
		grArr[row][col] = grSlot{
			n:    n,
			kid:  kid,
			ifc:  n,
			kids: []*grNode{n, kid},
			tag:  "v" + strconv.Itoa(ver),
			ver:  ver,
		}
		grFlat[row][col] = n
	}
}

func grCheckNode(n *grNode, base, ver, off int, seed int64, label string) bool {
	if n == nil || n.base != base || n.ver != ver || n.id != int64(base*100+ver*10+off) || n.name != "gr" {
		ck(false, fmt.Sprintf("%s: node base=%d ver=%d off=%d corrupted (seed=%d)", label, base, ver, off, seed))
		return false
	}
	for i := range n.data {
		if n.data[i] != grByte(base+off, ver, i) {
			ck(false, fmt.Sprintf("%s: node base=%d ver=%d data corrupted at %d (seed=%d)", label, base, ver, i, seed))
			return false
		}
	}
	return true
}

func verifyGlobalRows(exp [grRows]int, seed int64) bool {
	for row := 0; row < grRows; row++ {
		for col := 0; col < grCols; col++ {
			base := row*1000 + col
			s := &grArr[row][col]
			if s.ver != exp[row] || s.tag != "v"+strconv.Itoa(exp[row]) {
				ck(false, fmt.Sprintf("row %d col %d header ver=%d want=%d (seed=%d)", row, col, s.ver, exp[row], seed))
				return false
			}
			if !grCheckNode(s.n, base, exp[row], 0, seed, "slot-n") {
				return false
			}
			if !grCheckNode(s.kid, base, exp[row], 1, seed, "slot-kid") {
				return false
			}
			if len(s.kids) != 2 || s.kids[0] != s.n || s.kids[1] != s.kid {
				ck(false, fmt.Sprintf("row %d col %d kids alias broken (seed=%d)", row, col, seed))
				return false
			}
			in, ok := s.ifc.(*grNode)
			if !ok || in != s.n {
				ck(false, fmt.Sprintf("row %d col %d interface alias broken (seed=%d)", row, col, seed))
				return false
			}
			if grFlat[row][col] != s.n {
				ck(false, fmt.Sprintf("row %d col %d flat view diverged (seed=%d)", row, col, seed))
				return false
			}
		}
	}
	return true
}

func main() {
	seed := seedValue()
	rounds := tortureRounds(grRounds)

	for row := 0; row < grRows; row++ {
		for col := 0; col < grCols; col++ {
			s := &grArr[row][col]
			if s.n != nil || s.kid != nil || s.ifc != nil || s.kids != nil || s.tag != "" || s.ver != 0 || grFlat[row][col] != nil {
				ck(false, fmt.Sprintf("initial global row %d col %d not zeroed", row, col))
			}
		}
	}

	exp := [grRows]int{}
	for row := 0; row < grRows; row++ {
		fillGlobalRow(row, 1, seed)
		exp[row] = 1
	}
	gcStress()
	gcStress() // 三个行全部晋升为老对象

	ok := true
	for r := 2; r <= rounds+1; r++ {
		row := (r - 2) % grRows
		fillGlobalRow(row, r, seed)
		exp[row] = r
		gcStress()
		if !verifyGlobalRows(exp, seed) {
			ok = false
			break
		}
		if r >= 4 {
			for i := 1; i < grRows; i++ {
				if exp[i] == exp[0] {
					ck(false, fmt.Sprintf("round %d: rows not generation-mixed exp=%v (seed=%d)", r, exp, seed))
					ok = false
					break
				}
			}
			if !ok {
				break
			}
		}
	}
	if !ok {
		finish("l18_global_array_gen_rotate")
		return
	}
	finish("l18_global_array_gen_rotate")
}
