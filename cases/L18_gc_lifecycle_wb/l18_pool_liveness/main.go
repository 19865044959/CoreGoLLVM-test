// L18: 池中对象同时被外部引用时，GC 不得回收；取出复用后内部不变量成立。
package main

import (
	"fmt"
	"os"
	"runtime"
	"sync"
	"sync/atomic"
)

type node struct {
	v    int64
	name string
}

type buf struct {
	data []byte
	p    *node
}

var (
	failed int32
	gcSink [][]byte
	keep   []*buf
	pool   = sync.Pool{New: func() interface{} { return &buf{p: &node{v: 7, name: "y"}} }}
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
	for i := 0; i < 100; i++ {
		b := &buf{data: make([]byte, 64), p: &node{v: int64(i)*16 + 7, name: "y"}}
		for j := range b.data {
			b.data[j] = byte(i + j)
		}
		pool.Put(b)
		keep = append(keep, b)
		if len(keep) > 32 {
			keep = keep[1:]
		}

		gcStress()
		for _, k := range keep {
			if k.p == nil || k.p.name != "y" || k.p.v%16 != 7 {
				ck(false, "external reference to pooled object corrupted")
				break
			}
			if len(k.data) != 64 || k.data[0] != byte(k.p.v/16) {
				ck(false, "pooled buffer content corrupted")
				break
			}
		}
		if i%10 == 0 && len(keep) > 0 {
			keep[len(keep)-1] = nil
			keep = keep[:0]
		}
	}

	for k := 0; k < 20; k++ {
		q := pool.Get().(*buf)
		if q.p == nil || q.p.name != "y" {
			ck(false, "pool New object invalid")
		}
		pool.Put(q)
	}
	finish("l18_pool_liveness")
}
