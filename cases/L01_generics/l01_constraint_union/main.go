// L01: 类型约束（~int64/union）与指针 payload：多种数值/键实例化 + GC。
package main

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"sync/atomic"
)

type Intish interface {
	~int64 | ~int32 | ~uint32 | ~uint16
}

type Keyish interface {
	~int64 | ~string
}

// 自定义底层类型，靠 ~int64 进入约束。
type myInt int64
type myKey int64

type NumBox[T Intish] struct {
	v   *T
	gen uint64
}

func NewNumBox[T Intish](v *T) *NumBox[T] { return &NumBox[T]{v: v} }

func (b *NumBox[T]) Add(d T) { *b.v += d }

func (b *NumBox[T]) Value() T { return *b.v }

func Sum[T Intish](xs []*T) T {
	var s T
	for _, p := range xs {
		if p != nil {
			s += *p
		}
	}
	return s
}

func CountKeys[K Keyish, V any](m map[K]*V) int { return len(m) }

type entry struct {
	name string
	hits int64
}

var (
	failed  int32
	gcSink  [][]byte
	keySink []*entry
	uafSeed int64 = 1
)

func ck(ok bool, msg string) {
	if !ok {
		atomic.AddInt32(&failed, 1)
		fmt.Println("FAIL:", msg)
	}
}

func gcStress() {
	for i := 0; i < 8; i++ {
		b := make([]byte, 2048)
		b[0] = byte(i)
		gcSink = append(gcSink[:0], b)
	}
	gcSink = gcSink[:0]
	runtime.GC()
	runtime.GC()
}

func finish(name string) {
	if n := atomic.LoadInt32(&failed); n != 0 {
		fmt.Printf("FAILED %s seed=%d (%d failed checks)\n", name, uafSeed, n)
		os.Exit(1)
	}
	fmt.Println("ALL PASS " + name)
}

func envSeed() int64 {
	s := os.Getenv("UAF_SEED")
	if s == "" {
		return 1
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil || n <= 0 {
		return 1
	}
	return n
}

func main() {
	uafSeed = envSeed()
	n := 40 + int(uafSeed%9)

	// int64 实例化：值指针 + 加法 + Sum。
	var i64s []*int64
	var b64 []*NumBox[int64]
	var want64 int64
	for i := 0; i < n; i++ {
		v := int64(10 + i)
		i64s = append(i64s, &v)
		b64 = append(b64, NewNumBox(&v))
		want64 += v
	}
	gcStress()
	ck(int64(Sum(i64s)) == want64, "Sum[int64] mismatch after GC")
	for i := range b64 {
		ck(b64[i].v == i64s[i], "NumBox[int64] pointer identity changed")
		b64[i].Add(1)
		ck(*b64[i].v == int64(11+i), "NumBox[int64] Add write-through lost")
	}

	// int32 / uint32 / uint16 实例化，各自独立。
	var i32s []*int32
	var u32s []*uint32
	var u16s []*uint16
	var want32, wantU32, wantU16 int64
	for i := 0; i < n; i++ {
		a := int32(i)
		b := uint32(i * 2)
		c := uint16(i * 3)
		i32s = append(i32s, &a)
		u32s = append(u32s, &b)
		u16s = append(u16s, &c)
		want32 += int64(a)
		wantU32 += int64(b)
		wantU16 += int64(c)
	}
	gcStress()
	ck(int64(Sum(i32s)) == want32, "Sum[int32] mismatch after GC")
	ck(int64(Sum(u32s)) == wantU32, "Sum[uint32] mismatch after GC")
	ck(int64(Sum(u16s)) == wantU16, "Sum[uint16] mismatch after GC")

	// 自定义 ~int64 类型。
	var mine []*myInt
	var wantMy int64
	for i := 0; i < n; i++ {
		m := myInt(1000 + i)
		mine = append(mine, &m)
		wantMy += int64(m)
	}
	gcStress()
	ck(int64(Sum(mine)) == wantMy, "Sum[myInt] mismatch after GC (~int64 not honored)")

	// nil 指针元素必须被 Sum 跳过且不影响结果。
	nilCheck := []*int64{nil, i64s[0], nil}
	ck(int64(Sum(nilCheck)) == *i64s[0], "Sum with nil element mismatch")

	// Keyish：int64 / string / 自定义 ~int64 键三种实例化共享同一 payload。
	m64 := make(map[int64]*entry)
	mstr := make(map[string]*entry)
	mkey := make(map[myKey]*entry)
	for i := 0; i < n; i++ {
		e := &entry{name: fmt.Sprintf("e%d", i), hits: int64(i * 5)}
		keySink = append(keySink, e)
		m64[int64(i)] = e
		mstr[fmt.Sprintf("k%d", i)] = e
		mkey[myKey(i)] = e
	}
	gcStress()
	ck(CountKeys(m64) == n, "CountKeys[int64] mismatch")
	ck(CountKeys(mstr) == n, "CountKeys[string] mismatch")
	ck(CountKeys(mkey) == n, "CountKeys[myKey] mismatch")
	for i := 0; i < n; i++ {
		e := m64[int64(i)]
		ck(e != nil && e.name == fmt.Sprintf("e%d", i) && e.hits == int64(i*5), "int64-key payload corrupted")
		e2, ok := mstr[fmt.Sprintf("k%d", i)]
		ck(ok && e2 == e, "string-key map lost pointer identity")
		e3, ok := mkey[myKey(i)]
		ck(ok && e3 == e, "myKey map lost pointer identity")
	}

	finish("l01_constraint_union")
}
