// L11: slice copy 重叠 × 自追加——append(s, s...) 在容量内/需扩容两种路径
// 必须给出同样的倍增内容；dst/src 区域部分重叠时按源快照语义拷贝；
// 子切片追加、字节切片与含指针结构体切片的浅拷贝跨显式 GC 保持正确。
package main

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"sync/atomic"
)

type saRec struct {
	v int64
	p *saRec
}

type saSnap struct {
	s    []int64
	want []int64
}

var (
	failed  int32
	gcSink  [][]byte
	seed    uint64 = 1
	saSnaps []saSnap
)

func ck(ok bool, msg string) {
	if !ok {
		atomic.AddInt32(&failed, 1)
		fmt.Printf("FAIL: %s (UAF_SEED=%d)\n", msg, seed)
	}
}

func gcStress() {
	for i := 0; i < 16; i++ {
		b := make([]byte, 4096)
		b[0] = byte(i)
		b[len(b)-1] = byte(255 - i)
		gcSink = append(gcSink[:0], b)
	}
	gcSink = gcSink[:0]
	runtime.GC()
	runtime.GC()
}

func loadSeed() uint64 {
	s := os.Getenv("UAF_SEED")
	if s == "" {
		return 1
	}
	n, err := strconv.ParseUint(s, 10, 64)
	if err != nil {
		return 1
	}
	return n
}

func finish(name string) {
	if n := atomic.LoadInt32(&failed); n != 0 {
		fmt.Printf("FAILED %s (%d failed checks, seed=%d)\n", name, n, seed)
		os.Exit(1)
	}
	fmt.Println("ALL PASS " + name)
}

func saEqual(a, b []int64) bool {
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

// saAppendModel 按“先快照 dst/src，再逐一追加”的语义模拟 append。
func saAppendModel(dst, src []int64) []int64 {
	out := append([]int64(nil), dst...)
	snap := append([]int64(nil), src...)
	return append(out, snap...)
}

func saSeq(n int, salt int64) []int64 {
	a := make([]int64, n)
	for i := range a {
		a[i] = int64((i*29+int(salt)*7)%97) - 48
	}
	return a
}

func main() {
	seed = loadSeed()
	if seed == 0 {
		seed = 1
	}

	for r := 0; r < 3; r++ {
		n := 48 + int((seed+uint64(r)*11)%48)
		base := saSeq(n, int64(seed%31)+int64(r*3))

		grown := append([]int64(nil), base...)
		grown = grown[:len(grown):len(grown)]
		wantGrow := saAppendModel(base, base)
		gotGrow := append(grown, grown...)
		ck(len(gotGrow) == 2*n, "self append grow length")
		ck(saEqual(gotGrow, wantGrow), "self append grow content")
		ck(saEqual(base, saSeq(n, int64(seed%31)+int64(r*3))), "self append grow leaves source")

		inplace := make([]int64, n, 2*n+4)
		copy(inplace, base)
		wantIn := saAppendModel(base, base)
		gotIn := append(inplace, inplace...)
		ck(len(gotIn) == 2*n, "self append in place length")
		ck(saEqual(gotIn, wantIn), "self append in place content")
		ck(saEqual(inplace, base), "self append in place leaves source prefix")

		pat := []int64{3, -7, 11}
		dbl := append([]int64(nil), pat...)
		for k := 0; k < 5; k++ {
			dbl = append(dbl, dbl...)
		}
		ck(len(dbl) == 3<<5, "repeated self append length")
		patOK := true
		for i := range dbl {
			if dbl[i] != pat[i%3] {
				patOK = false
				break
			}
		}
		ck(patOK, "repeated self append pattern")

		full := saSeq(2*n, int64(seed%23)+int64(r*5))
		dst := full[:n]
		src := full[n-4 : n+8]
		wantOver := saAppendModel(dst, src)
		gotOver := append(dst, src...)
		ck(len(gotOver) == n+12, "overlapping append length")
		ck(saEqual(gotOver, wantOver), "overlapping append snapshot semantics")
		ck(saEqual(full[:n-4], saSeq(2*n, int64(seed%23)+int64(r*5))[:n-4]), "overlap prefix intact")

		subBase := saSeq(n, int64(seed%13)-int64(r))
		front := subBase[:2]
		tail := subBase[3:]
		wantSub := saAppendModel(front, tail)
		gotSub := append(front, tail...)
		ck(saEqual(gotSub, wantSub), "sub-slice overlapping append")
		ck(saEqual(subBase[:2], wantSub[:2]), "sub-slice append keeps prefix")

		bytesSrc := []byte{0x00, 0x01, 0xff, 0xfe, 'A', 0x80}
		bytesWant := append([]byte(nil), bytesSrc...)
		bytesWant = append(bytesWant, bytesSrc...)
		bytesGot := append(bytesSrc, bytesSrc...)
		ck(len(bytesGot) == 2*len(bytesSrc), "byte self append length")
		bytesOK := true
		for i := range bytesWant {
			if bytesGot[i] != bytesWant[i] {
				bytesOK = false
				break
			}
		}
		ck(bytesOK, "byte self append content")

		saSnaps = append(saSnaps,
			saSnap{s: append([]int64(nil), gotGrow...), want: append([]int64(nil), wantGrow...)},
			saSnap{s: append([]int64(nil), gotOver...), want: append([]int64(nil), wantOver...)},
			saSnap{s: append([]int64(nil), gotSub...), want: append([]int64(nil), wantSub...)},
		)
		gcStress()
	}

	recs := make([]saRec, 10, 20)
	for i := range recs {
		recs[i] = saRec{v: int64(i), p: &saRec{v: int64(100 + i)}}
	}
	recsOut := append(recs, recs...)
	ck(len(recsOut) == 20, "struct self append length")
	ck(recsOut[0].p == recsOut[10].p, "struct self append shares pointer")
	ck(recsOut[0].v == 0 && recsOut[19].v == 9, "struct self append values")
	recsOut[10].p.v = 777
	ck(recsOut[0].p.v == 777, "struct self append pointer mutation visible")

	gcStress()
	ck(recsOut[0].p == recsOut[10].p, "struct self append pointer identity after GC")
	ck(recsOut[0].p.v == 777 && recsOut[9].p.v == 109, "struct self append pointers after GC")

	for i := range saSnaps {
		if !saEqual(saSnaps[i].s, saSnaps[i].want) {
			ck(false, "retained self append slices after GC")
			break
		}
	}

	finish("l11_slice_copy_selfappend")
}
