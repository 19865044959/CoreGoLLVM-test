// L13: append(s, s...) 自重叠语义：扩容与原地两种路径、full slice 断链、指针身份与 GC。
package main

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"sync/atomic"
)

type ae struct {
	id  int64
	tag string
	pad []byte
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

func newAE(id, salt int64) *ae {
	e := &ae{id: id, tag: fmt.Sprintf("ae-%04d-%02d", id, salt%73), pad: make([]byte, 9)}
	for i := range e.pad {
		e.pad[i] = byte(id*17 + int64(i*3) + salt)
	}
	return e
}

func aeValid(e *ae, id, salt int64) bool {
	if e == nil || e.id != id || e.tag != fmt.Sprintf("ae-%04d-%02d", id, salt%73) || len(e.pad) != 9 {
		return false
	}
	for i := range e.pad {
		if e.pad[i] != byte(id*17+int64(i*3)+salt) {
			return false
		}
	}
	return true
}

func mkAE(n int, salt int64) []*ae {
	s := make([]*ae, n)
	for i := range s {
		s[i] = newAE(int64(i), salt)
	}
	return s
}

func ptrSeqEqualAE(a, b []*ae) bool {
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

	ref := mkAE(64, curSeed)
	refCopy := append([]*ae(nil), ref...)

	// A) cap == len，append(s, s...) 必须扩容，两个半区指向同一批对象。
	workA := append([]*ae(nil), refCopy...)
	grown := append(workA, workA...)
	ck(len(grown) == 128, "self append: grown len")
	ck(cap(grown) >= 128, "self append: grown cap")
	half := append([]*ae(nil), refCopy...)
	ck(ptrSeqEqualAE(grown[:64], half) && ptrSeqEqualAE(grown[64:], half), "self append: grown halves")
	ck(ptrSeqEqualAE(workA, refCopy), "self append: original half modified")

	// B) cap 有富余，原地重叠复制。
	workB := make([]*ae, 64, 128)
	copy(workB, refCopy)
	resB := append(workB, workB...)
	ck(len(resB) == 128, "self append: room len")
	ck(ptrSeqEqualAE(resB[:64], refCopy) && ptrSeqEqualAE(resB[64:], refCopy), "self append: room halves")
	ck(resB[0] == resB[64], "self append: room pointer identity")

	// C) full slice expression 限容，append 后必须与原底层数组断链。
	full := refCopy[:32:32]
	resC := append(full, full...)
	ck(len(resC) == 64, "self append: full-slice len")
	ck(ptrSeqEqualAE(resC[:32], refCopy[:32]) && ptrSeqEqualAE(resC[32:], refCopy[:32]), "self append: full-slice content")
	other := newAE(9999, curSeed)
	resC[0] = other
	ck(refCopy[0] != other, "self append: full-slice append aliased parent array")
	ck(resC[32] == refCopy[0], "self append: full-slice pointer slots")

	// D) 源区间与目标区间重叠（非首尾对齐）。
	workD := make([]*ae, 64, 128)
	copy(workD, refCopy)
	dst := workD[5:15]
	src := workD[10:20]
	prefix := append([]*ae(nil), dst...)
	srcSnap := append([]*ae(nil), src...)
	resD := append(dst, src...)
	ck(len(resD) == 20, "self append: overlap len")
	ck(ptrSeqEqualAE(resD[:10], prefix), "self append: overlap prefix")
	ck(ptrSeqEqualAE(resD[10:], srcSnap), "self append: overlap tail")

	// E) UAF_SEED 驱动初始长度，反复自身追加。
	k := 8 + int(curSeed%13)
	workE := append([]*ae(nil), refCopy[:k]...)
	prev := append([]*ae(nil), workE...)
	for j := 0; j < 6; j++ {
		beforeLen := len(workE)
		workE = append(workE, workE...)
		ck(len(workE) == beforeLen*2, "self append: doubling len")
		if !ptrSeqEqualAE(workE[:beforeLen], prev) || !ptrSeqEqualAE(workE[beforeLen:], prev) {
			ckSeed(false, fmt.Sprintf("self append: doubling content at step %d", j))
			break
		}
		prev = append([]*ae(nil), workE...)
	}
	gcStress()
	for i, e := range ref {
		if !aeValid(e, int64(i), curSeed) {
			ckSeed(false, fmt.Sprintf("self append: payload corrupted at %d after GC", i))
			break
		}
	}
	ck(ptrSeqEqualAE(workA, refCopy), "self append: workA after GC")
	ck(ptrSeqEqualAE(resB[:64], refCopy), "self append: resB after GC")
	gcStress()
	for i := 1; i < 32; i++ { // resC[0] 已被显式改写为 other
		if resC[i] != refCopy[i] || resC[32+i] != refCopy[i] {
			ckSeed(false, fmt.Sprintf("self append: resC pointer slot %d after GC", i))
			break
		}
	}
	ck(resC[0] == other && resC[32] == refCopy[0], "self append: resC explicit overwrite after GC")

	runtime.KeepAlive(ref)
	runtime.KeepAlive(workA)
	runtime.KeepAlive(resB)
	runtime.KeepAlive(resC)
	runtime.KeepAlive(resD)
	runtime.KeepAlive(workE)
	finish("l13_slice_append_self_overlap")
}
