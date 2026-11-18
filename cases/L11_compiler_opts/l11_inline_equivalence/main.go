// L11: 内联候选函数与 //go:noinline 版本必须给出完全一致的算术结果；
// 两种版本返回的指针、指向切片内部的指针在 GC 后仍指向有效对象。
package main

import (
	"fmt"
	"os"
	"runtime"
	"sync/atomic"
)

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
		b[len(b)-1] = byte(255 - i)
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

type pair struct {
	a, b int64
	tag  string
}

// mix 是内联候选：分支 + 位移 + 取负，覆盖有符号溢出回绕。
func mix(x, y int64) int64 {
	z := x*31 + y*17
	if z&1 == 0 {
		z = (z >> 1) ^ (x << 3)
	} else {
		z = z*3 + (y >> 2)
	}
	if z < 0 {
		z = -z
	}
	return z ^ (x & 0xFF)
}

//go:noinline
func mixNoInline(x, y int64) int64 {
	z := x*31 + y*17
	if z&1 == 0 {
		z = (z >> 1) ^ (x << 3)
	} else {
		z = z*3 + (y >> 2)
	}
	if z < 0 {
		z = -z
	}
	return z ^ (x & 0xFF)
}

func sliceSum(s []int64) int64 {
	var t int64
	for _, v := range s {
		t += v
	}
	return t
}

//go:noinline
func sliceSumNoInline(s []int64) int64 {
	var t int64
	for _, v := range s {
		t += v
	}
	return t
}

func newPair(v int64) *pair {
	return &pair{a: v, b: mix(v, v+3), tag: "inline"}
}

//go:noinline
func newPairNoInline(v int64) *pair {
	return &pair{a: v, b: mix(v, v+3), tag: "inline"}
}

func pick(s []pair, i int) *pair { return &s[i] }

//go:noinline
func pickNoInline(s []pair, i int) *pair { return &s[i] }

func localPickInline(v int64) *pair {
	arr := []pair{{a: v, b: v + 1, tag: "lo"}, {a: v + 2, b: v + 3, tag: "hi"}}
	return &arr[1]
}

//go:noinline
func localPickNoInline(v int64) *pair {
	arr := []pair{{a: v, b: v + 1, tag: "lo"}, {a: v + 2, b: v + 3, tag: "hi"}}
	return &arr[1]
}

func main() {
	inputs := []int64{0, 1, -1, 2, 63, -64, 1 << 20, -(1 << 20), 1<<62 - 1, -(1 << 62), 7, -9, 123456789, -987654321}
	for _, x := range inputs {
		for _, y := range inputs {
			ck(mix(x, y) == mixNoInline(x, y), "mix/mixNoInline result mismatch")
			ck(mix(x, y) == mix(x, y), "inline call unstable")
		}
	}

	vals := make([]int64, 128)
	for i := range vals {
		vals[i] = int64(i)*3 - 40
	}
	ck(sliceSum(vals) == sliceSumNoInline(vals), "sliceSum variants disagree")
	rev := append([]int64(nil), vals...)
	for i, j := 0, len(rev)-1; i < j; i, j = i+1, j-1 {
		rev[i], rev[j] = rev[j], rev[i]
	}
	ck(sliceSum(rev) == sliceSum(vals), "reverse must preserve sum")
	ck(sliceSumNoInline(rev) == sliceSumNoInline(vals), "noinline reverse must preserve sum")

	pairs := make([]*pair, 0, 48)
	for i := 0; i < 48; i++ {
		v := int64(i)*7 - 100
		var p *pair
		if i%2 == 0 {
			p = newPair(v)
		} else {
			p = newPairNoInline(v)
		}
		pairs = append(pairs, p)
	}
	gcStress()
	for i, p := range pairs {
		ck(p != nil, "constructor returned nil")
		if p == nil {
			continue
		}
		v := int64(i)*7 - 100
		ck(p.a == v, "constructor a corrupted after GC")
		ck(p.b == mix(v, v+3), "constructor b corrupted after GC")
		ck(p.tag == "inline", "constructor tag corrupted after GC")
	}
	for i := range pairs {
		for j := i + 1; j < len(pairs); j++ {
			if pairs[i] == pairs[j] {
				ck(false, "distinct constructor calls must allocate distinct pairs")
			}
		}
	}

	pairVals := make([]pair, len(pairs))
	for i, p := range pairs {
		pairVals[i] = *p
	}
	ptrs := make([]*pair, 0, len(pairVals))
	for i := range pairVals {
		if i%2 == 0 {
			ptrs = append(ptrs, pick(pairVals, i))
		} else {
			ptrs = append(ptrs, pickNoInline(pairVals, i))
		}
	}
	gcStress()
	for i := range ptrs {
		ck(ptrs[i] == &pairVals[i], "pointer into slice changed identity after GC")
		if ptrs[i] != nil {
			ck(ptrs[i].a == int64(i)*7-100, "pointer into slice target corrupted")
		}
	}

	locals := []*pair{localPickInline(11), localPickNoInline(22)}
	gcStress()
	ck(locals[0] != nil && locals[0].a == 13 && locals[0].b == 14 && locals[0].tag == "hi", "inlined local array element corrupted after GC")
	ck(locals[1] != nil && locals[1].a == 24 && locals[1].b == 25 && locals[1].tag == "hi", "noinline local array element corrupted after GC")
	ck(locals[0] != locals[1], "distinct local escapes must be distinct objects")

	finish("l11_inline_equivalence")
}
