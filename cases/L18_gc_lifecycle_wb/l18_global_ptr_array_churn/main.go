// L18: 全局固定指针数组（含指向数组的全局指针）持续 churn：
// 整组年轻化 + 随机槽位年轻化，setter noinline，返回后对象只被全局数组引用。
package main

import (
	"fmt"
	"math/rand"
	"os"
	"runtime"
	"strconv"
	"sync/atomic"
)

const arrN = 256

type node struct {
	v    int64
	name string
}

var (
	failed  int32
	seed    int64 = 1
	gcSink  [][]byte
	gArr    [arrN]*node
	gArrPtr = &gArr
)

func ck(ok bool, msg string) {
	if !ok {
		atomic.AddInt32(&failed, 1)
		fmt.Printf("FAIL: %s (seed=%d)\n", msg, seed)
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
		fmt.Printf("FAILED %s (%d failed checks, seed=%d)\n", name, n, seed)
		os.Exit(1)
	}
	fmt.Println("ALL PASS " + name)
}

// fillGlobal 一次性把整个全局数组替换为年轻对象。
//
//go:noinline
func fillGlobal(pa *[arrN]*node, gen int64) {
	for i := 0; i < arrN; i++ {
		pa[i] = &node{v: gen*1000 + int64(i), name: "ga"}
	}
}

// setGlobalSlot 单槽写入年轻对象。
//
//go:noinline
func setGlobalSlot(pa *[arrN]*node, i int, gen int64) {
	pa[i] = &node{v: gen*1000 + int64(i), name: "ga"}
}

func verifyArray(want []int64) {
	for i := 0; i < arrN; i++ {
		q := gArr[i]
		if q == nil || q.v != want[i] || q.name != "ga" {
			ck(false, fmt.Sprintf("global array slot %d corrupted", i))
			continue
		}
		if (*gArrPtr)[i] != q {
			ck(false, fmt.Sprintf("array pointer view diverged at slot %d", i))
		}
	}
}

func parseSeed() int64 {
	v := os.Getenv("UAF_SEED")
	if v == "" {
		return 1
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil || n <= 0 {
		return 1
	}
	return n
}

func main() {
	seed = parseSeed()
	rng := rand.New(rand.NewSource(seed))

	fillGlobal(gArrPtr, 0)
	gcStress()
	gcStress() // 数组槽位中的初始对象晋升为老对象

	want := make([]int64, arrN)
	// 整组年轻化多轮。
	for gen := int64(1); gen <= 12; gen++ {
		fillGlobal(gArrPtr, gen)
		for i := 0; i < arrN; i++ {
			want[i] = gen*1000 + int64(i)
		}
		gcStress()
		verifyArray(want)
	}

	// 随机槽位 churn：老/新对象混合存活。
	for r := 0; r < 10; r++ {
		perm := rng.Perm(arrN)
		n := 80 + rng.Intn(48)
		gen := int64(100 + r)
		for _, i := range perm[:n] {
			setGlobalSlot(gArrPtr, i, gen)
			want[i] = gen*1000 + int64(i)
		}
		gcStress()
		verifyArray(want)

		if r%3 == 2 {
			gen++
			fillGlobal(gArrPtr, gen)
			for i := 0; i < arrN; i++ {
				want[i] = gen*1000 + int64(i)
			}
			gcStress()
			verifyArray(want)
		}
	}

	gcStress()
	verifyArray(want)
	runtime.KeepAlive(gArrPtr)
	finish("l18_global_ptr_array_churn")
}
