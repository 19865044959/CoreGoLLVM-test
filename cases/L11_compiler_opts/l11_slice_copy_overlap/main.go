// L11: slice copy 语义——重叠区间前后向拷贝的 memmove 行为、返回长度、
// 目标尾部哨兵、string→[]byte、浅拷贝指针字段与 GC 后数据稳定。
package main

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"sync/atomic"
)

type cpNode struct {
	v int64
	p *cpNode
}

type cpSnap struct {
	live []int64
	want []int64
}

var (
	failed  int32
	gcSink  [][]byte
	seed    uint64 = 1
	cpSnaps []cpSnap
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

func tortureMultiplier() int {
	s := os.Getenv("TORTURE_ROUNDS")
	if s == "" {
		return 1
	}
	n, err := strconv.Atoi(s)
	if err != nil || n < 1 {
		return 1
	}
	if n > 100 {
		n = 100
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

func cpEqual(a, b []int64) bool {
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

// cpModel 模拟 copy：先快照 src，再按 dst 长度逐元素写入。
func cpModel(dst, src []int64) []int64 {
	snap := append([]int64(nil), src...)
	for i := range dst {
		if i < len(snap) {
			dst[i] = snap[i]
		}
	}
	return dst
}

func cpSeq(n int, seed uint64, salt int64) []int64 {
	a := make([]int64, n)
	for i := range a {
		a[i] = int64((i*37+int(seed)+int(salt)*17)%101) - 50
	}
	return a
}

func main() {
	seed = loadSeed()
	if seed == 0 {
		seed = 1
	}
	rounds := 6 * tortureMultiplier()

	for r := 0; r < rounds; r++ {
		n := 24 + int((seed+uint64(r)*11)%40)
		base := cpSeq(n, seed, int64(r))

		fwd := append([]int64(nil), base...)
		wantF := append([]int64(nil), base...)
		cpModel(wantF[2:], wantF[:n-2])
		cnt := copy(fwd[2:], fwd[:n-2])
		ck(cnt == n-2 && cpEqual(fwd, wantF), "forward overlapping copy")

		bwd := append([]int64(nil), base...)
		wantB := append([]int64(nil), base...)
		cpModel(wantB[:n-2], wantB[2:])
		cnt = copy(bwd[:n-2], bwd[2:])
		ck(cnt == n-2 && cpEqual(bwd, wantB), "backward overlapping copy")

		dst := make([]int64, n+6)
		for i := range dst {
			dst[i] = -777
		}
		cnt = copy(dst[2:2+n], base)
		sepOK := cnt == n && dst[0] == -777 && dst[1] == -777 && dst[n+2] == -777 && dst[n+5] == -777
		if sepOK {
			for i := 0; i < n; i++ {
				if dst[2+i] != base[i] {
					sepOK = false
					break
				}
			}
		}
		ck(sepOK, "separate copy and destination tail")

		short := make([]int64, 5)
		cnt = copy(short, base)
		ck(cnt == 5 && cpEqual(short, base[:5]), "copy returns destination length")

		smallDst := make([]int64, 8)
		cnt = copy(smallDst, base[:3])
		ck(cnt == 3 && smallDst[3] == 0 && smallDst[7] == 0, "copy returns source length when shorter")

		text := "copy世界abc"
		bb := make([]byte, len(text))
		cnt = copy(bb, text)
		ck(cnt == len(text) && string(bb) == text, "string to byte copy")

		var nilSlice []int64
		ck(copy(nilSlice, base) == 0, "copy to nil destination")
		ck(copy(make([]int64, 4), nilSlice) == 0, "copy from nil source")
		ck(copy(nilSlice, nilSlice) == 0, "copy nil to nil")

		raw := make([]byte, n, n+8)
		for i := 0; i < n; i++ {
			raw[i] = 0x11
		}
		full := raw[:cap(raw)]
		for i := n; i < len(full); i++ {
			full[i] = 0x99
		}
		d3 := raw[: n : n+3]
		cnt = copy(d3, make([]byte, n))
		ck(cnt == n && full[n] == 0x99 && full[n+1] == 0x99 && full[n+2] == 0x99 && full[n+3] == 0x99, "three-index destination sentinels")

		k := 1 + int((seed+uint64(r))%uint64(n-1))
		rot := append([]int64(nil), base...)
		tail := append([]int64(nil), rot[n-k:]...)
		copy(rot[k:], rot[:n-k])
		copy(rot[:k], tail)
		wantRot := make([]int64, n)
		for i := 0; i < n; i++ {
			wantRot[(i+k)%n] = base[i]
		}
		ck(cpEqual(rot, wantRot), "overlap rotation")

		cpSnaps = append(cpSnaps, cpSnap{live: fwd, want: wantF}, cpSnap{live: rot, want: wantRot}, cpSnap{live: dst, want: append([]int64(nil), dst...)})
	}

	srcNodes := make([]cpNode, 8)
	for i := range srcNodes {
		srcNodes[i] = cpNode{v: int64(i), p: &cpNode{v: int64(i * 10)}}
	}
	dstNodes := make([]cpNode, len(srcNodes))
	cpCopy := copy(dstNodes, srcNodes)
	ck(cpCopy == len(srcNodes), "struct slice copy count")
	ck(dstNodes[0].p == srcNodes[0].p, "struct copy shares pointer field")
	dstNodes[0].p.v = 999
	ck(srcNodes[0].p.v == 999, "shared pointer target mutation visible")
	gcStress()
	ck(dstNodes[0].p.v == 999 && srcNodes[0].p.v == 999 && dstNodes[7].p.v == 70, "struct pointer fields after GC")

	gcStress()
	for _, s := range cpSnaps {
		if !cpEqual(s.live, s.want) {
			ck(false, "retained copy result after GC")
			break
		}
	}

	finish("l11_slice_copy_overlap")
}
