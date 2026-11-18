// L18: 老全局数组上的重叠 copy，元素分别为指针、interface、string。
// 每轮重新写入年轻元组后，noinline setter 执行前向/反向重叠 copy，
// GC 后校验别名序列、动态类型与逐字节 payload。
package main

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
)

const (
	opN       = 12
	opRounds  = 4
	opMaxRnds = 16
	opDataN   = 16
)

type opNode struct {
	id   int64
	ver  int
	name string
	data [opDataN]byte
}

type opHolder struct {
	arr [opN]*opNode
	ifc [opN]interface{}
	str [opN]string
}

var (
	failed int32
	gcSink [][]byte
	opH    opHolder
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
	if n > opMaxRnds {
		n = opMaxRnds
	}
	return n
}

func opSalt(seed int64) int {
	s := int(seed % 197)
	if s < 0 {
		s = -s
	}
	return s
}

func opID(gen, i int) int64 { return int64(gen*1000 + i) }

func opName(id int64, ver int) string {
	return fmt.Sprintf("n%d/%d", id%100000, ver)
}

func opStr(gen, i int) string {
	return strings.Repeat(string(rune('a'+(gen+i)%26)), 6+i%5) + strconv.Itoa(gen)
}

func opByte(id int64, ver, i, salt int) byte {
	return byte((int(id%251)*23 + ver*29 + i*11 + salt) % 251)
}

//go:noinline
func opMake(gen, i int, seed int64) *opNode {
	id := opID(gen, i)
	n := &opNode{id: id, ver: gen, name: opName(id, gen)}
	salt := opSalt(seed)
	for j := range n.data {
		n.data[j] = opByte(id, gen, j, salt)
	}
	return n
}

//go:noinline
func opFill(h *opHolder, gen int, seed int64) {
	for i := 0; i < opN; i++ {
		n := opMake(gen, i, seed)
		h.arr[i] = n
		h.ifc[i] = n
		h.str[i] = opStr(gen, i)
	}
}

//go:noinline
func opCopyFwd(h *opHolder) {
	copy(h.arr[:opN-1], h.arr[1:])
	copy(h.ifc[:opN-1], h.ifc[1:])
	copy(h.str[:opN-1], h.str[1:])
}

//go:noinline
func opCopyBwd(h *opHolder) {
	copy(h.arr[1:], h.arr[:opN-1])
	copy(h.ifc[1:], h.ifc[:opN-1])
	copy(h.str[1:], h.str[:opN-1])
}

func opVerify(h *opHolder, gen int, expect []int, seed int64, label string) bool {
	salt := opSalt(seed)
	for i := 0; i < opN; i++ {
		wantIdx := expect[i]
		n := h.arr[i]
		wantID := opID(gen, wantIdx)
		if n == nil || n.id != wantID || n.ver != gen || n.name != opName(wantID, gen) {
			ck(false, fmt.Sprintf("%s: arr[%d] node corrupted want id=%d (seed=%d)", label, i, wantID, seed))
			return false
		}
		for j := range n.data {
			if n.data[j] != opByte(wantID, gen, j, salt) {
				ck(false, fmt.Sprintf("%s: arr[%d] data corrupted at %d (seed=%d)", label, i, j, seed))
				return false
			}
		}
		in, ok := h.ifc[i].(*opNode)
		if !ok || in != n {
			ck(false, fmt.Sprintf("%s: ifc[%d] interface alias broken (seed=%d)", label, i, seed))
			return false
		}
		if h.str[i] != opStr(gen, wantIdx) {
			ck(false, fmt.Sprintf("%s: str[%d] content corrupted (seed=%d)", label, i, seed))
			return false
		}
	}
	return true
}

func main() {
	seed := seedValue()
	rounds := tortureRounds(opRounds)

	opFill(&opH, 1, seed)
	gcStress()
	gcStress() // 全局数组晋升，后续元素写入均为老->年轻

	fwdExpect := make([]int, opN)
	for i := 0; i < opN; i++ {
		if i < opN-1 {
			fwdExpect[i] = i + 1
		} else {
			fwdExpect[i] = opN - 1
		}
	}
	bwdExpect := make([]int, opN)
	for i := 0; i < opN; i++ {
		if i == 0 {
			bwdExpect[i] = 0
		} else {
			bwdExpect[i] = i - 1
		}
	}

	gen := 2
	for r := 1; r <= rounds; r++ {
		opFill(&opH, gen, seed)
		gcStress()
		if (int(seed)+r)%2 == 0 {
			opCopyFwd(&opH)
			gcStress()
			if !opVerify(&opH, gen, fwdExpect, seed, "forward") {
				break
			}
		} else {
			opCopyBwd(&opH)
			gcStress()
			if !opVerify(&opH, gen, bwdExpect, seed, "backward") {
				break
			}
		}
		gen++

		opFill(&opH, gen, seed)
		gcStress()
		if (int(seed)+r)%2 == 0 {
			opCopyBwd(&opH)
			gcStress()
			if !opVerify(&opH, gen, bwdExpect, seed, "backward") {
				break
			}
		} else {
			opCopyFwd(&opH)
			gcStress()
			if !opVerify(&opH, gen, fwdExpect, seed, "forward") {
				break
			}
		}
		gen++
	}

	finish("l18_overlap_ptr_array_copy")
}
