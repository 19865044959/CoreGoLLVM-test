// L18: 老 slice 的底层数组批量改写——逐元素年轻化、copy 年轻切片到老 slice；
// setter noinline 返回后年轻对象只被老 slice 引用，再 GC 校验。
package main

import (
	"fmt"
	"math/rand"
	"os"
	"runtime"
	"strconv"
	"sync/atomic"
)

const (
	sliceN  = 64
	dataLen = 24
)

type item struct {
	slot int
	ver  int
	name string
	data []byte
}

type bucket struct {
	elems []*item
}

var (
	failed int32
	seed   int64 = 1
	gcSink [][]byte
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

func patternByte(slot, ver, i int) byte { return byte((slot*29 + ver*11 + i*5) % 251) }

func newItem(slot, ver int) *item {
	it := &item{slot: slot, ver: ver, name: "rw", data: make([]byte, dataLen)}
	for i := range it.data {
		it.data[i] = patternByte(slot, ver, i)
	}
	return it
}

func checkItem(it *item, slot, ver int) bool {
	if it == nil || it.slot != slot || it.ver != ver || it.name != "rw" || len(it.data) != dataLen {
		return false
	}
	for i := range it.data {
		if it.data[i] != patternByte(slot, ver, i) {
			return false
		}
	}
	return true
}

// promoteBucket 建立老 bucket + 老底层数组。
//
//go:noinline
func promoteBucket(n int) *bucket {
	b := &bucket{elems: make([]*item, n)}
	for i := range b.elems {
		b.elems[i] = newItem(i, 0)
	}
	gcStress()
	gcStress()
	return b
}

// rewriteRange 在 noinline 内批量改写老 slice 的指定下标。
//
//go:noinline
func rewriteRange(dst []*item, order []int, ver int) {
	for _, i := range order {
		dst[i] = newItem(i, ver)
	}
}

// copyRewrite 在 noinline 内构造年轻切片，再 copy 进老 slice 的底层数组。
//
//go:noinline
func copyRewrite(dst []*item, order []int, ver int) {
	tmp := make([]*item, len(order))
	for j, i := range order {
		tmp[j] = newItem(i, ver)
	}
	for j, i := range order {
		dst[i] = tmp[j]
	}
}

func verify(b *bucket, want []int) {
	for i := 0; i < sliceN; i++ {
		if !checkItem(b.elems[i], i, want[i]) {
			ck(false, fmt.Sprintf("old slice element %d expected ver %d corrupted", i, want[i]))
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

	b := promoteBucket(sliceN)
	want := make([]int, sliceN)
	gcStress()
	verify(b, want)

	const rounds = 16
	for r := 1; r <= rounds; r++ {
		perm := rng.Perm(sliceN)
		// 每轮改写 3/4 的元素：老/新指针混合存活，再 GC 校验。
		n := sliceN * 3 / 4
		order := perm[:n]
		if r%4 == 3 {
			copyRewrite(b.elems, order, r)
		} else {
			rewriteRange(b.elems, order, r)
		}
		for _, i := range order {
			want[i] = r
		}
		gcStress()
		verify(b, want)
	}

	// 最后一轮全量改写，确保每个槽位都由年轻对象覆盖。
	full := make([]int, sliceN)
	for i := range full {
		full[i] = i
	}
	copyRewrite(b.elems, full, rounds+1)
	for i := range want {
		want[i] = rounds + 1
	}
	gcStress()
	gcStress()
	verify(b, want)

	runtime.KeepAlive(b)
	finish("l18_old_slice_bulk_rewrite")
}
