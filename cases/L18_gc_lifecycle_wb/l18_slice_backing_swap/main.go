// L18: 老 slice 头整体替换。每轮 noinline setter 分配全新底层数组装年轻节点
// 并写回 slice 头；按种子决定是否把旧底层数组存入 retired。末段再做
// 截断 + 原地 append，验证旧底层数组复用时的写屏障。
package main

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"sync/atomic"
)

const (
	sbN       = 48
	sbRounds  = 8
	sbMaxRnds = 32
	sbDataN   = 20
)

type sbNode struct {
	id   int64
	ver  int
	name string
	data [sbDataN]byte
}

type sbHolder struct {
	live    []*sbNode
	retired []*sbNode
}

var (
	failed int32
	gcSink [][]byte
	sbH    sbHolder
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
	if n > sbMaxRnds {
		n = sbMaxRnds
	}
	return n
}

func sbSalt(seed int64) int {
	s := int(seed % 229)
	if s < 0 {
		s = -s
	}
	return s
}

func sbByte(id int64, ver, i, salt int) byte {
	return byte((int(id%251)*27 + ver*19 + i*11 + salt) % 251)
}

//go:noinline
func sbMake(id int64, ver int, seed int64) *sbNode {
	n := &sbNode{id: id, ver: ver, name: fmt.Sprintf("s%d/%d", id%1000, ver)}
	salt := sbSalt(seed)
	for i := range n.data {
		n.data[i] = sbByte(id, ver, i, salt)
	}
	return n
}

//go:noinline
func sbFill(dst []*sbNode, ver int, seed int64) {
	for i := range dst {
		dst[i] = sbMake(int64(ver*1000+i), ver, seed)
	}
}

//go:noinline
func sbSwap(h *sbHolder, ver int, seed int64) []*sbNode {
	next := make([]*sbNode, sbN)
	sbFill(next, ver, seed)
	old := h.live
	h.live = next
	return old
}

//go:noinline
func sbTruncate(h *sbHolder) { h.live = h.live[:sbN/2] }

//go:noinline
func sbAppendTail(h *sbHolder, ver int, seed int64) {
	for i := 0; i < sbN/2; i++ {
		h.live = append(h.live, sbMake(int64(ver*1000+sbN/2+i), ver, seed))
	}
}

func sbVerify(s []*sbNode, ver int, seed int64, label string) bool {
	if len(s) < sbN/2 {
		ck(false, fmt.Sprintf("%s: slice too short len=%d (seed=%d)", label, len(s), seed))
		return false
	}
	salt := sbSalt(seed)
	for i := 0; i < len(s); i++ {
		n := s[i]
		wantID := int64(ver*1000 + i)
		if n == nil || n.id != wantID || n.ver != ver || n.name != fmt.Sprintf("s%d/%d", wantID%1000, ver) {
			ck(false, fmt.Sprintf("%s: slot %d node corrupted want id=%d (seed=%d)", label, i, wantID, seed))
			return false
		}
		for j := range n.data {
			if n.data[j] != sbByte(wantID, ver, j, salt) {
				ck(false, fmt.Sprintf("%s: slot %d data corrupted at %d (seed=%d)", label, i, j, seed))
				return false
			}
		}
	}
	return true
}

func main() {
	seed := seedValue()
	rounds := tortureRounds(sbRounds)

	sbH.live = make([]*sbNode, sbN)
	sbFill(sbH.live, 1, seed)
	gcStress()
	gcStress() // holder 与初始底层数组晋升

	lastVer := 1
	for r := 2; r <= rounds+1; r++ {
		old := sbSwap(&sbH, r, seed)
		keep := (int(seed)+r)%2 == 0
		if keep {
			sbH.retired = old
		} else {
			sbH.retired = nil
		}
		gcStress()
		if !sbVerify(sbH.live, r, seed, "live") {
			break
		}
		if keep {
			if !sbVerify(sbH.retired, r-1, seed, "retired") {
				break
			}
			if len(sbH.live) > 0 && len(sbH.retired) > 0 && sbH.live[0] == sbH.retired[0] {
				ck(false, fmt.Sprintf("round %d: live and retired share backing objects (seed=%d)", r, seed))
				break
			}
		}
		lastVer = r
	}

	sbTruncate(&sbH)
	gcStress()
	if len(sbH.live) != sbN/2 || cap(sbH.live) != sbN {
		ck(false, fmt.Sprintf("truncate: len=%d cap=%d want=%d/%d (seed=%d)", len(sbH.live), cap(sbH.live), sbN/2, sbN, seed))
	} else if !sbVerify(sbH.live, lastVer, seed, "truncated") {
		finish("l18_slice_backing_swap")
		return
	}

	sbAppendTail(&sbH, lastVer+1, seed)
	gcStress()
	if len(sbH.live) != sbN {
		ck(false, fmt.Sprintf("append: len=%d want=%d (seed=%d)", len(sbH.live), sbN, seed))
	} else {
		if !sbVerify(sbH.live[:sbN/2], lastVer, seed, "append-head") {
			finish("l18_slice_backing_swap")
			return
		}
		head := sbH.live[sbN/2:]
		salt := sbSalt(seed)
		for i, n := range head {
			wantID := int64((lastVer+1)*1000 + sbN/2 + i)
			if n == nil || n.id != wantID || n.ver != lastVer+1 {
				ck(false, fmt.Sprintf("append-tail: slot %d node corrupted (seed=%d)", i, seed))
				break
			}
			bad := false
			for j := range n.data {
				if n.data[j] != sbByte(wantID, lastVer+1, j, salt) {
					ck(false, fmt.Sprintf("append-tail: slot %d data corrupted at %d (seed=%d)", i, j, seed))
					bad = true
					break
				}
			}
			if bad {
				break
			}
		}
	}

	finish("l18_slice_backing_swap")
}
