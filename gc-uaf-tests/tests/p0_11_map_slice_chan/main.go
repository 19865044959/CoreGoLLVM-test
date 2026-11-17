// P0-11 (报告风险 B14 + C16): map/slice/chan 存指针，append/copy/扩容。
package main

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"sync/atomic"
)

type node struct {
	v    int64
	name string
}

type wrap struct {
	P *node
	S string
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

//go:noinline
func mapsGC(n int) int64 {
	m := make(map[int]*node, 8)
	for i := 0; i < n; i++ {
		m[i] = &node{v: int64(i)}
	}
	gcStress()
	var sum int64
	for i := 0; i < n; i++ {
		sum += m[i].v
	}
	for i := n; i < 200; i++ { // 触发 map 扩容
		m[i] = &node{v: int64(i)}
	}
	gcStress()
	delete(m, 0)
	gcStress()
	if _, ok := m[0]; ok {
		return -1
	}
	return sum
}

//go:noinline
func slicesGC() int64 {
	s := make([]*node, 0, 4)
	for i := 0; i < 12; i++ { // 触发多次扩容
		s = append(s, &node{v: int64(i)})
	}
	gcStress()
	var sum int64
	for _, p := range s {
		sum += p.v
	}
	dst := make([]*node, len(s))
	copy(dst, s)
	gcStress()
	sum += dst[11].v
	sub := s[2:10]
	gcStress()
	sum += sub[7].v
	return sum
}

//go:noinline
func chansGC(n int) int64 {
	ch := make(chan *node, n)
	for i := 0; i < n; i++ {
		ch <- &node{v: int64(i)}
	}
	gcStress()
	var sum int64
	for i := 0; i < n; i++ {
		sum += (<-ch).v
	}
	return sum
}

//go:noinline
func chanStruct(n int) int64 {
	ch := make(chan wrap, n)
	for i := 0; i < n; i++ {
		ch <- wrap{P: &node{v: int64(i)}, S: "s"}
	}
	gcStress()
	var sum int64
	for i := 0; i < n; i++ {
		w := <-ch
		sum += w.P.v + int64(len(w.S))
	}
	return sum
}

func main() {
	ck(mapsGC(100) == 4950, "map values across GC")
	ck(slicesGC() == 86, "slice/append/copy/sub-slice across GC")
	ck(chansGC(64) == 2016, "chan pointers across GC")
	ck(chanStruct(32) == 528, "chan struct values across GC")

	m := map[string]wrap{}
	for i := 0; i < 100; i++ {
		m["k"+strconv.Itoa(i)] = wrap{P: &node{v: int64(i)}, S: "x"}
	}
	gcStress()
	ck(m["k99"].P.v == 99, "map of struct-with-pointer")

	dup := make(map[int][]*node)
	for i := 0; i < 8; i++ {
		dup[i] = []*node{{v: int64(i)}, {v: int64(i * 10)}}
	}
	gcStress()
	ck(dup[7][1].v == 70, "map of pointer slices")
	finish("p0_11_map_slice_chan")
}
