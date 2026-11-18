// L13: slices.Concat/Insert/Replace/Grow 的指针元素组合语义：新底层数组、位移与容量增长。
package main

import (
	"fmt"
	"os"
	"runtime"
	"slices"
	"strconv"
	"sync/atomic"
)

type si struct {
	id   int64
	name string
	pad  []byte
}

var (
	failed  int32
	gcSink  [][]byte
	curSeed int64
)

func ck(ok bool, msg string) {
	if !ok {
		atomic.AddInt32(&failed, 1)
		fmt.Println("FAIL:", msg)
	}
}

func ckSeed(ok bool, msg string) {
	if !ok {
		atomic.AddInt32(&failed, 1)
		fmt.Printf("FAIL: %s (seed=%d)\n", msg, curSeed)
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

func envSeed() int64 {
	s := os.Getenv("UAF_SEED")
	if s == "" {
		return 1
	}
	v, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 1
	}
	return v
}

func newSI(id, salt int64) *si {
	e := &si{id: id, name: fmt.Sprintf("si-%04d-%02d", id, salt%79), pad: make([]byte, 8)}
	for i := range e.pad {
		e.pad[i] = byte(id*31 + int64(i*5) + salt)
	}
	return e
}

func siValid(e *si, id, salt int64) bool {
	if e == nil || e.id != id || e.name != fmt.Sprintf("si-%04d-%02d", id, salt%79) || len(e.pad) != 8 {
		return false
	}
	for i := range e.pad {
		if e.pad[i] != byte(id*31+int64(i*5)+salt) {
			return false
		}
	}
	return true
}

func mkSI(n int, base, salt int64) []*si {
	s := make([]*si, n)
	for i := range s {
		s[i] = newSI(base+int64(i), salt)
	}
	return s
}

func ptrSeqEqualSI(a, b []*si) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func main() {
	curSeed = envSeed()

	refA := mkSI(90, 0, curSeed)
	refB := mkSI(70, 1000, curSeed)
	extras := mkSI(4, 2000, curSeed)

	// Concat 返回新切片：改写结果槽位不影响输入。
	c := slices.Concat(refA, refB)
	ck(len(c) == 160, "slices.Concat: len")
	ck(ptrSeqEqualSI(c[:90], refA) && ptrSeqEqualSI(c[90:], refB), "slices.Concat: content")
	other := newSI(9999, curSeed)
	c[0] = other
	ck(refA[0] != other, "slices.Concat: result aliased input array")
	ck(len(slices.Concat(nil, refA[:0], nil)) == 0, "slices.Concat: empty result len")
	ck(len(slices.Concat(refA, nil)) == 90, "slices.Concat: nil tail")

	// Insert：头、中、尾三个位置。
	pos := 1 + int(curSeed%30)
	aWork := append([]*si(nil), refA...)
	aRef := append([]*si(nil), refA...)
	ins := slices.Insert(aWork, pos, extras...)
	var wantIns []*si
	wantIns = append(wantIns, aRef[:pos]...)
	wantIns = append(wantIns, extras...)
	wantIns = append(wantIns, aRef[pos:]...)
	ck(ptrSeqEqualSI(ins, wantIns), "slices.Insert: middle")

	insHead := slices.Insert(append([]*si(nil), refA...), 0, extras...)
	var wantHead []*si
	wantHead = append(wantHead, extras...)
	wantHead = append(wantHead, aRef...)
	ck(ptrSeqEqualSI(insHead, wantHead), "slices.Insert: head")

	insTail := slices.Insert(append([]*si(nil), refA...), len(refA), extras...)
	var wantTail []*si
	wantTail = append(wantTail, aRef...)
	wantTail = append(wantTail, extras...)
	ck(ptrSeqEqualSI(insTail, wantTail), "slices.Insert: tail")

	// Replace：中段替换为 extras。
	bWork := append([]*si(nil), refB...)
	bRef := append([]*si(nil), refB...)
	rep := slices.Replace(bWork, 10, 30, extras...)
	var wantRep []*si
	wantRep = append(wantRep, bRef[:10]...)
	wantRep = append(wantRep, extras...)
	wantRep = append(wantRep, bRef[30:]...)
	ck(ptrSeqEqualSI(rep, wantRep), "slices.Replace: composition")

	// Grow：长度与内容不变，容量至少增加 n。
	growN := 300 + int(curSeed%100)
	gRef := append([]*si(nil), refA...)
	g := slices.Grow(gRef, growN)
	ck(len(g) == len(gRef), "slices.Grow: len changed")
	ck(ptrSeqEqualSI(g, gRef), "slices.Grow: content changed")
	if cap(g) < len(g)+growN {
		ckSeed(false, "slices.Grow: capacity not grown")
	}
	appended := g
	for i := 0; i < growN; i++ {
		appended = append(appended, newSI(3000+int64(i), curSeed))
	}
	ck(len(appended) == len(refA)+growN, "grow append: len")
	ck(ptrSeqEqualSI(appended[:len(refA)], gRef), "grow append: prefix")
	for i := 0; i < growN; i++ {
		if !siValid(appended[len(refA)+i], 3000+int64(i), curSeed) {
			ckSeed(false, fmt.Sprintf("grow append: new element invalid at %d", i))
			break
		}
	}

	// 链式组合：Concat(Concat(a,b), extras)。
	chain := slices.Concat(slices.Concat(refA, refB), extras)
	var wantChain []*si
	wantChain = append(wantChain, refA...)
	wantChain = append(wantChain, refB...)
	wantChain = append(wantChain, extras...)
	ck(ptrSeqEqualSI(chain, wantChain), "slices.Concat: chained")

	gcStress()
	for _, group := range [][]*si{refA, refB, extras} {
		for i, e := range group {
			if !siValid(e, e.id, curSeed) {
				ckSeed(false, fmt.Sprintf("slices concat/insert: payload corrupted at group index %d after GC", i))
				break
			}
		}
	}
	ck(ptrSeqEqualSI(appended[:len(refA)], gRef), "slices: grown prefix after GC")
	for i := 0; i < len(extras); i++ {
		if chain[len(refA)+len(refB)+i] != extras[i] {
			ckSeed(false, fmt.Sprintf("slices: chained extras pointer %d after GC", i))
			break
		}
	}
	gcStress()

	runtime.KeepAlive(refA)
	runtime.KeepAlive(refB)
	runtime.KeepAlive(extras)
	runtime.KeepAlive(c)
	runtime.KeepAlive(ins)
	runtime.KeepAlive(insHead)
	runtime.KeepAlive(insTail)
	runtime.KeepAlive(rep)
	runtime.KeepAlive(appended)
	runtime.KeepAlive(chain)
	finish("l13_slices_concat_insert")
}
