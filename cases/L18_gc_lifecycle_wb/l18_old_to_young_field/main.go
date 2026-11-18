// L18: 老对象→新对象写屏障：字段/slice/map/interface/数组/全局槽。
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

type cell struct {
	p   *node
	sl  []*node
	m   map[int]*node
	ifv interface{}
	arr [4]*node
}

var (
	failed      int32
	gcSink      [][]byte
	globalYoung *node
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
func promote() *cell {
	c := &cell{sl: make([]*node, 4), m: make(map[int]*node)}
	mk := func(v int64) *node { return &node{v: v, name: "y"} }
	c.p = mk(7)
	for i := range c.sl {
		c.sl[i] = mk(7)
	}
	for k := 0; k < 4; k++ {
		c.m[k] = mk(7)
	}
	c.ifv = mk(7)
	for i := range c.arr {
		c.arr[i] = mk(7)
	}
	gcStress()
	gcStress() // 晋升为老对象
	return c
}

// 以下 setter 全部 noinline：返回后年轻对象只被老对象引用。

//go:noinline
func setP(c *cell, v int64) { c.p = &node{v: v, name: "y"} }

//go:noinline
func setSL(c *cell, i int, v int64) { c.sl[i] = &node{v: v, name: "y"} }

//go:noinline
func setM(c *cell, k int, v int64) { c.m[k] = &node{v: v, name: "y"} }

//go:noinline
func setIF(c *cell, v int64) { c.ifv = &node{v: v, name: "y"} }

//go:noinline
func setArr(c *cell, i int, v int64) { c.arr[i] = &node{v: v, name: "y"} }

//go:noinline
func setGlobal(v int64) { globalYoung = &node{v: v, name: "y"} }

func check(q *node) bool { return q != nil && q.name == "y" && q.v%16 == 7 }

func verify(c *cell) {
	ck(check(c.p), "cell.p corrupted")
	for _, q := range c.sl {
		ck(check(q), "cell.sl corrupted")
	}
	for k := 0; k < 4; k++ {
		ck(check(c.m[k]), "cell.m corrupted")
	}
	q, ok := c.ifv.(*node)
	ck(ok && check(q), "cell.ifv corrupted")
	for _, q := range c.arr {
		ck(check(q), "cell.arr corrupted")
	}
}

func main() {
	c := promote()
	for i := 0; i < 30; i++ {
		v := int64(i*16 + 7)
		setP(c, v)
		gcStress()
		verify(c)

		setSL(c, i%4, v)
		gcStress()
		verify(c)

		setM(c, i%4, v)
		gcStress()
		verify(c)

		setIF(c, v)
		gcStress()
		verify(c)

		setArr(c, i%4, v)
		gcStress()
		verify(c)

		setGlobal(v)
		gcStress()
		ck(check(globalYoung), "global young pointer corrupted")
	}
	runtime.KeepAlive(c)
	finish("l18_old_to_young_field")
}
