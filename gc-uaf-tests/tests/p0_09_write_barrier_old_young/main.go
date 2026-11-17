// P0-09 (报告风险 C16): 老对象→新对象写屏障（字段/数组/切片/map/接口/chan/全局）。
package main

import (
	"fmt"
	"os"
	"runtime"
	"sync/atomic"
)

type node struct {
	v    int64
	name string
}

type wb struct {
	p   *node
	arr [4]*node
	sl  []*node
	m   map[string]*node
	i   interface{}
	ch  chan *node
}

var (
	failed     int32
	gcSink     [][]byte
	globalRoot *wb
	globalNode *node
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
func promote() *wb {
	w := &wb{
		sl: make([]*node, 4),
		m:  make(map[string]*node),
		ch: make(chan *node, 4),
	}
	gcStress()
	gcStress()
	return w
}

// 每个 setter 都是 noinline：让年轻对象在返回后立刻成为"只有老对象引用"的状态。

//go:noinline
func setField(w *wb, k int64) { w.p = &node{v: k} }

//go:noinline
func setArr(w *wb, i int, k int64) { w.arr[i] = &node{v: k} }

//go:noinline
func setSlice(w *wb, i int, k int64) { w.sl[i] = &node{v: k} }

//go:noinline
func setMap(w *wb, k int64) { w.m["x"] = &node{v: k} }

//go:noinline
func setIface(w *wb, k int64) { w.i = &node{v: k, name: "young"} }

//go:noinline
func sendChan(w *wb, k int64) { w.ch <- &node{v: k} }

//go:noinline
func appendSlice(w *wb, k int64) { w.sl = append(w.sl, &node{v: k}) }

//go:noinline
func setGlobal(k int64) { globalNode = &node{v: k} }

func main() {
	w := promote()
	globalRoot = w
	gcStress() // 让 w 进入老年代

	for k := int64(0); k < 10; k++ {
		setField(w, k)
		gcStress()
		ck(w.p != nil && w.p.v == k, "old->young field")

		setArr(w, int(k%4), k)
		gcStress()
		ck(w.arr[k%4].v == k, "old->young array element")

		setSlice(w, int(k%4), k)
		gcStress()
		ck(w.sl[k%4].v == k, "old->young slice element")

		setMap(w, k)
		gcStress()
		ck(w.m["x"].v == k, "old->young map value")

		setIface(w, k)
		gcStress()
		ck(w.i.(*node).v == k && w.i.(*node).name == "young", "old->young interface")

		sendChan(w, k)
		gcStress()
		got := <-w.ch
		ck(got.v == k, "old->young chan send")

		setGlobal(k)
		gcStress()
		ck(globalNode.v == k, "global->young")

		appendSlice(w, k)
		gcStress()
		ck(w.sl[len(w.sl)-1].v == k, "old->young append")
	}
	runtime.KeepAlive(w)
	finish("p0_09_write_barrier_old_young")
}
