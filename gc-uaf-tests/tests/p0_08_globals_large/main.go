// P0-08 (报告风险 B14/B15): 全局大对象/gcprog/ptrdata。
package main

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"sync/atomic"
)

type node struct{ v int64 }

type kv struct {
	k, v string
	p    *node
	n    int64
}

type deepT struct {
	A [4]*node
	B struct {
		C [4]string
		D *node
	}
	E []*node
}

var (
	failed int32
	gcSink [][]byte

	gArr   [1000]kv
	gPtrs  [512]*node
	gSlice []*node
	gMap   map[string]*node
	gIface interface{}
	gStr   [256]string
	gDeep  deepT
	gStrI  interface{}
	gMapIf interface{}
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

func main() {
	for i := 0; i < 1000; i++ {
		gArr[i] = kv{k: "k" + strconv.Itoa(i), v: "v", p: &node{v: int64(i)}, n: int64(i) * 2}
	}
	for i := 0; i < 512; i++ {
		gPtrs[i] = &node{v: int64(1000 + i)}
	}
	gSlice = make([]*node, 1000)
	for i := range gSlice {
		gSlice[i] = &node{v: int64(2000 + i)}
	}
	gMap = make(map[string]*node)
	for i := 0; i < 200; i++ {
		gMap["key-"+strconv.Itoa(i)] = &node{v: int64(3000 + i)}
	}
	gIface = [4]*node{{v: 1}, {v: 2}, {v: 3}, {v: 4}}
	for i := 0; i < 256; i++ {
		gStr[i] = "s-" + strconv.Itoa(i)
	}
	gDeep.A[0] = &node{v: 41}
	gDeep.A[3] = &node{v: 44}
	for i := range gDeep.B.C {
		gDeep.B.C[i] = "deep-" + strconv.Itoa(i)
	}
	gDeep.B.D = &node{v: 45}
	gDeep.E = []*node{{v: 46}, {v: 47}, {v: 48}}
	gStrI = "global-string-interface"
	gMapIf = map[string]*node{"im": {v: 9000}}

	gcStress()

	ck(gArr[999].p.v == 999 && gArr[0].k == "k0" && gArr[500].n == 1000, "gArr")
	ck(gPtrs[511].v == 1511 && gPtrs[0].v == 1000, "gPtrs")
	ck(gSlice[999].v == 2999, "gSlice")
	ck(gMap["key-199"].v == 3199, "gMap")
	ck(gIface.([4]*node)[3].v == 4, "gIface array")
	ck(gStr[255] == "s-255" && gStr[0] == "s-0", "gStr")
	ck(gDeep.A[3].v == 44 && gDeep.B.C[3] == "deep-3" && gDeep.B.D.v == 45 && gDeep.E[2].v == 48, "gDeep")
	ck(gStrI.(string) == "global-string-interface", "gStrI")
	ck(gMapIf.(map[string]*node)["im"].v == 9000, "gMapIf")

	// 全局指针改写后再 GC。
	gIface = []*node{{v: 9001}, {v: 9002}}
	gMapIf = &node{v: 9003}
	old := gPtrs[100]
	gPtrs[100] = &node{v: 9100}
	gcStress()
	ck(gIface.([]*node)[1].v == 9002, "gIface rewritten")
	ck(gMapIf.(*node).v == 9003, "gMapIf rewritten")
	ck(gPtrs[100].v == 9100, "gPtrs rewritten")
	ck(old.v == 1100, "old global pointer still alive")
	finish("p0_08_globals_large")
}
