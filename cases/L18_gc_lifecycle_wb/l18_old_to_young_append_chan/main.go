// L18: 老对象的 slice append 扩容 / chan 发送年轻对象后的写屏障。
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
	sl []*node
	ch chan *node
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
func promote() *wb {
	w := &wb{sl: make([]*node, 4), ch: make(chan *node, 8)}
	for i := range w.sl {
		w.sl[i] = &node{v: 7, name: "y"}
	}
	gcStress()
	gcStress()
	return w
}

//go:noinline
func appendYoung(w *wb, v int64) {
	w.sl = append(w.sl, &node{v: v, name: "y"})
}

//go:noinline
func sendYoung(w *wb, v int64) {
	w.ch <- &node{v: v, name: "y"}
}

func check(q *node) bool { return q != nil && q.name == "y" && q.v%16 == 7 }

func main() {
	w := promote()
	for i := 0; i < 20; i++ {
		v := int64(i*16 + 7)

		appendYoung(w, v)
		gcStress()
		ck(check(w.sl[len(w.sl)-1]), "old slice append lost young object")
		for _, q := range w.sl {
			if !check(q) {
				ck(false, "old slice element corrupted")
				break
			}
		}

		sendYoung(w, v)
		gcStress()
		got := <-w.ch
		ck(check(got), "old chan buffer lost young object")
		ck(len(w.sl) == 4+i+1, "slice length drift")
	}
	runtime.KeepAlive(w)
	finish("l18_old_to_young_append_chan")
}
