// L18: 大 struct 数组（>32KB 大对象）零化——新分配必须全零，clear() 旧数组必须
// 彻底清零；零化后再写入年轻对象（noinline setter），GC 后校验老/零/新混合状态。
package main

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"sync/atomic"
)

const (
	bigN     = 8192
	recDataN = 24
)

type node struct {
	v    int64
	name string
}

type bigrec struct {
	id   int64
	ver  int
	name string
	p    *node
	data [recDataN]byte
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

func tortureRounds(def int) int {
	s := os.Getenv("TORTURE_ROUNDS")
	if s == "" {
		return def
	}
	n, err := strconv.Atoi(s)
	if err != nil || n <= 0 {
		return def
	}
	if n > 16 {
		n = 16
	}
	return n
}

func recByte(slot, gen, i int) byte { return byte((slot*13 + gen*7 + i*3) % 251) }

//go:noinline
func allocBig(n int) []bigrec {
	return make([]bigrec, n)
}

// fillBig 在 noinline 内分配年轻 node 写入给定槽位；返回后只由数组引用。
//
//go:noinline
func fillBig(dst []bigrec, order []int, gen int) {
	for _, i := range order {
		var r bigrec
		r.id = int64(i)
		r.ver = gen
		r.name = "br"
		r.p = &node{v: int64(i*100 + gen), name: "bn"}
		for j := range r.data {
			r.data[j] = recByte(i, gen, j)
		}
		dst[i] = r
	}
}

func recZero(r *bigrec) bool {
	if r.id != 0 || r.ver != 0 || r.name != "" || r.p != nil {
		return false
	}
	for _, b := range r.data {
		if b != 0 {
			return false
		}
	}
	return true
}

func checkRec(r *bigrec, slot, gen int) bool {
	if r.id != int64(slot) || r.ver != gen || r.name != "br" {
		return false
	}
	if r.p == nil || r.p.v != int64(slot*100+gen) || r.p.name != "bn" {
		return false
	}
	for i := range r.data {
		if r.data[i] != recByte(slot, gen, i) {
			return false
		}
	}
	return true
}

// verifyBig: want[i]==0 表示该槽位必须全零，否则必须匹配对应代次。
func verifyBig(dst []bigrec, want []int) {
	for i := range dst {
		if want[i] == 0 {
			if !recZero(&dst[i]) {
				ck(false, fmt.Sprintf("slot %d not zeroed", i))
				return
			}
			continue
		}
		if !checkRec(&dst[i], i, want[i]) {
			ck(false, fmt.Sprintf("slot %d gen %d corrupted", i, want[i]))
			return
		}
	}
}

func main() {
	rounds := tortureRounds(2)
	full := make([]int, bigN)
	half := make([]int, 0, bigN/2)
	for i := 0; i < bigN; i++ {
		full[i] = i
		if i%2 == 0 {
			half = append(half, i)
		}
	}
	want := make([]int, bigN)

	var prev []bigrec
	for r := 1; r <= rounds; r++ {
		arr := allocBig(bigN)

		// 1) 大对象新分配必须全零。
		for i := range arr {
			if !recZero(&arr[i]) {
				ck(false, fmt.Sprintf("fresh large array slot %d not zeroed", i))
				break
			}
		}

		// 2) 全量写入年轻对象后 GC（数组此时已被多轮 GC 晋升为老对象）。
		fillBig(arr, full, r)
		for i := range want {
			want[i] = r
		}
		gcStress()
		gcStress()
		verifyBig(arr, want)

		// 3) clear() 大数组必须彻底零化，GC 后不得残留旧指针。
		clear(arr)
		for i := range want {
			want[i] = 0
		}
		gcStress()
		verifyBig(arr, want)

		// 4) 半量槽位重新写入年轻对象，未写槽位保持全零。
		fillBig(arr, half, r+100)
		for _, i := range half {
			want[i] = r + 100
		}
		gcStress()
		verifyBig(arr, want)

		prev = arr
	}
	gcStress()
	runtime.KeepAlive(prev)
	finish("l18_big_struct_array_zero")
}
