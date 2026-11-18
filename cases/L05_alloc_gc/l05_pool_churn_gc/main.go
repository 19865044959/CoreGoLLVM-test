// L05: sync.Pool 复用 + GC：池中对象可能被 GC 清空，但取出的对象必须可安全复用。
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
	pool   = sync.Pool{New: func() interface{} { return &buf{p: &node{v: -1}} }}
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
	for i := 0; i < 300; i++ {
		b := pool.Get().(*buf)
		n := 17 + i%13
		b.data = b.data[:0]
		for k := 0; k < n; k++ {
			b.data = append(b.data, byte(k+i))
		}
		b.p = &node{v: int64(i), name: "pool"}

		gcStress() // 对象仍被局部变量持有，绝不能丢
		ck(b.p != nil && b.p.v == int64(i) && b.p.name == "pool", "pool object lost in GC")
		ck(len(b.data) == n && b.data[n-1] == byte(n-1+i), "pool buffer content corrupted")

		pool.Put(b)
		if i%50 == 0 {
			runtime.GC()
		}
	}

	// 池可能被 GC 清空；无论拿到旧对象还是 New 的对象，内部不变量必须成立。
	for k := 0; k < 20; k++ {
		b := pool.Get().(*buf)
		if b.p == nil {
			ck(false, "pool object has nil pointer field")
		} else if b.p.v >= 0 && len(b.data) > 0 {
			ck(b.data[0] == byte(b.p.v), "reused pool object inconsistent")
		}
		pool.Put(b)
	}
	finish("l05_pool_churn_gc")
}
