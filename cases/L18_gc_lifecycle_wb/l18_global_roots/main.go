// L18: 全局根（map/slice/interface/单指针）持续写入年轻对象后的 GC。
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

var (
	failed int32
	gcSink [][]byte

	gm map[string]*node
	gs []*node
	gi interface{}
	gn *node
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

func mk(i int64) *node { return &node{v: i*16 + 7, name: "y"} }
func key(i int) string { return "k" + strconv.Itoa(i) }
func check(q *node) bool { return q != nil && q.name == "y" && q.v%16 == 7 }

func main() {
	gm = make(map[string]*node, 64)
	for i := 0; i < 64; i++ {
		gm[key(i)] = mk(int64(i))
	}
	gs = make([]*node, 64)
	for i := range gs {
		gs[i] = mk(int64(i))
	}
	gi = []*node{mk(1), mk(2)}
	gn = mk(3)

	gcStress()
	for i := 0; i < 64; i++ {
		ck(check(gm[key(i)]), "global map value after GC")
		ck(check(gs[i]), "global slice element after GC")
	}
	ck(check(gi.([]*node)[1]), "global interface slice after GC")
	ck(check(gn), "global pointer after GC")

	// 改写全局根 + 扩容，然后再 GC。
	gm["k0"] = mk(10)
	gs = append(gs, mk(100))
	gi = []*node{mk(11), mk(12), mk(13)}
	gn = mk(14)
	for i := 64; i < 200; i++ {
		gm[key(i)] = mk(int64(i))
	}
	gcStress()
	ck(check(gm["k0"]) && check(gm[key(199)]), "rewritten global map entries")
	ck(check(gs[0]) && check(gs[len(gs)-1]), "appended global slice")
	ck(check(gi.([]*node)[2]), "rewritten global interface")
	ck(check(gn), "rewritten global pointer")

	var sum int64
	for _, q := range gs {
		if !check(q) {
			ck(false, "global slice element corrupted")
			break
		}
		sum += q.v
	}
	ck(sum > 0, "global slice sum")
	finish("l18_global_roots")
}
