// L18: 大于 32KB 的大 struct（指针/interface/string/tags/大数组/大字节缓冲）
// 做重叠 copy、整值清零与重新填充。每轮先校验新分配全零，再填充晋升、
// 拷贝（typedmemmove 写屏障）、clear，最后反向再拷贝并跨 GC 校验。
package main

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"sync/atomic"
)

const (
	bsArrN   = 4
	bsIntN   = 3600
	bsBufN   = 4096
	bsDataN  = 24
	bsRounds = 2
	bsMaxRnd = 12
)

type bsNode struct {
	id   int64
	name string
	data [bsDataN]byte
}

type bsHuge struct {
	n    *bsNode
	kid  *bsNode
	ifc  interface{}
	s    string
	tags [8]string
	arr  [bsIntN]int64
	buf  [bsBufN]byte
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

func tortureRounds(def int) int {
	s := os.Getenv("TORTURE_ROUNDS")
	if s == "" {
		return def
	}
	n, err := strconv.Atoi(s)
	if err != nil || n <= 0 {
		return def
	}
	if n > bsMaxRnd {
		n = bsMaxRnd
	}
	return n
}

func bsByte(base, i int64) byte { return byte((int(base)*43 + int(i)*11) % 251) }

//go:noinline
func bsAlloc() []bsHuge { return make([]bsHuge, bsArrN) }

//go:noinline
func bsNewNode(base int64, off int64) *bsNode {
	n := &bsNode{id: base*10 + off, name: "bn"}
	for i := range n.data {
		n.data[i] = bsByte(base+off, int64(i))
	}
	return n
}

//go:noinline
func bsFill(p *bsHuge, base int64) {
	n := bsNewNode(base, 0)
	kid := bsNewNode(base, 1)
	p.n = n
	p.kid = kid
	p.ifc = n
	p.s = "s" + strconv.FormatInt(base, 10)
	for i := range p.tags {
		p.tags[i] = "t" + strconv.FormatInt(base, 10) + "-" + strconv.Itoa(i)
	}
	for i := range p.arr {
		p.arr[i] = base*100000 + int64(i)
	}
	for i := range p.buf {
		p.buf[i] = bsByte(base, int64(i))
	}
}

//go:noinline
func bsClear(p *bsHuge) { *p = bsHuge{} }

//go:noinline
func bsCopyForward(arr []bsHuge) { copy(arr[0:bsArrN-1], arr[1:bsArrN]) }

//go:noinline
func bsCopyBackward(arr []bsHuge) { copy(arr[1:bsArrN], arr[0:bsArrN-1]) }

func bsCheckNode(n *bsNode, base, off int64, seed int64, label string) bool {
	if n == nil || n.id != base*10+off || n.name != "bn" {
		ck(false, fmt.Sprintf("%s: node base=%d off=%d corrupted (seed=%d)", label, base, off, seed))
		return false
	}
	for i := range n.data {
		if n.data[i] != bsByte(base+off, int64(i)) {
			ck(false, fmt.Sprintf("%s: node base=%d off=%d data at %d (seed=%d)", label, base, off, i, seed))
			return false
		}
	}
	return true
}

func bsCheck(p *bsHuge, base int64, seed int64, label string) bool {
	if !bsCheckNode(p.n, base, 0, seed, label) || !bsCheckNode(p.kid, base, 1, seed, label) {
		return false
	}
	in, ok := p.ifc.(*bsNode)
	if !ok || in != p.n {
		ck(false, fmt.Sprintf("%s: base=%d interface alias broken (seed=%d)", label, base, seed))
		return false
	}
	if p.s != "s"+strconv.FormatInt(base, 10) {
		ck(false, fmt.Sprintf("%s: base=%d string corrupted (seed=%d)", label, base, seed))
		return false
	}
	for i := range p.tags {
		if p.tags[i] != "t"+strconv.FormatInt(base, 10)+"-"+strconv.Itoa(i) {
			ck(false, fmt.Sprintf("%s: base=%d tag[%d] corrupted (seed=%d)", label, base, i, seed))
			return false
		}
	}
	for i := range p.arr {
		if p.arr[i] != base*100000+int64(i) {
			ck(false, fmt.Sprintf("%s: base=%d arr[%d] corrupted (seed=%d)", label, base, i, seed))
			return false
		}
	}
	for i := range p.buf {
		if p.buf[i] != bsByte(base, int64(i)) {
			ck(false, fmt.Sprintf("%s: base=%d buf[%d] corrupted (seed=%d)", label, base, i, seed))
			return false
		}
	}
	return true
}

func bsCheckZero(p *bsHuge, seed int64, label string) bool {
	if p.n != nil || p.kid != nil || p.ifc != nil || p.s != "" {
		ck(false, fmt.Sprintf("%s: header not zeroed (seed=%d)", label, seed))
		return false
	}
	for i := range p.tags {
		if p.tags[i] != "" {
			ck(false, fmt.Sprintf("%s: tag[%d] not zeroed (seed=%d)", label, i, seed))
			return false
		}
	}
	for i := range p.arr {
		if p.arr[i] != 0 {
			ck(false, fmt.Sprintf("%s: arr[%d] not zeroed (seed=%d)", label, i, seed))
			return false
		}
	}
	for i := range p.buf {
		if p.buf[i] != 0 {
			ck(false, fmt.Sprintf("%s: buf[%d] not zeroed (seed=%d)", label, i, seed))
			return false
		}
	}
	return true
}

func main() {
	seed := seedValue()
	rounds := tortureRounds(bsRounds)
	base0 := seed * 1000

	for r := 1; r <= rounds; r++ {
		arr := bsAlloc()
		for i := range arr {
			if !bsCheckZero(&arr[i], seed, "fresh") {
				finish("l18_big_struct_overlap_copy")
				return
			}
		}

		bases := [bsArrN]int64{}
		for i := 0; i < bsArrN; i++ {
			bases[i] = base0 + int64(r*10+i)
			bsFill(&arr[i], bases[i])
		}
		gcStress()
		for i := 0; i < bsArrN; i++ {
			if !bsCheck(&arr[i], bases[i], seed, "filled") {
				finish("l18_big_struct_overlap_copy")
				return
			}
		}

		// 重叠向前拷贝：[b1,b2,b3,b3]。
		bsCopyForward(arr)
		gcStress()
		wantF := [bsArrN]int64{bases[1], bases[2], bases[3], bases[3]}
		for i := 0; i < bsArrN; i++ {
			if !bsCheck(&arr[i], wantF[i], seed, "copy-forward") {
				finish("l18_big_struct_overlap_copy")
				return
			}
		}

		// 清零尾槽后 GC：仅 arr[2] 仍引用 b3。
		bsClear(&arr[bsArrN-1])
		gcStress()
		if !bsCheckZero(&arr[bsArrN-1], seed, "clear-tail") {
			break
		}
		if !bsCheck(&arr[bsArrN-2], wantF[bsArrN-2], seed, "survivor") {
			break
		}

		// 重叠向后拷贝：[b1,b1,b2,b3]。
		bsCopyBackward(arr)
		gcStress()
		wantB := [bsArrN]int64{wantF[0], wantF[0], wantF[1], wantF[2]}
		for i := 0; i < bsArrN; i++ {
			if !bsCheck(&arr[i], wantB[i], seed, "copy-backward") {
				finish("l18_big_struct_overlap_copy")
				return
			}
		}
		arr = nil
		gcStress()
	}

	finish("l18_big_struct_overlap_copy")
}
