// L18: runtime.KeepAlive 边界处的对象存活。finalizer 仅作存活探针：
// KeepAlive 之前的 GC 不得终结对象；越过边界失活后对象最终必须被终结。
package main

import (
	"fmt"
	"os"
	"runtime"
	"sync/atomic"
	"time"
)

const kaDataN = 64

type payload struct {
	v    int64
	name string
	data [kaDataN]byte
}

var (
	failed  int32
	kaFin   int32
	sinkInt int64
	gcSink  [][]byte
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
func newPayload(v int64) *payload {
	p := &payload{v: v, name: "ka"}
	for i := range p.data {
		p.data[i] = byte((int(v) + i*5) % 251)
	}
	runtime.SetFinalizer(p, func(*payload) {
		atomic.StoreInt32(&kaFin, 1)
	})
	return p
}

// keepBoundary 的字段读取发生在 GC 之前；runtime.KeepAlive 位于函数末尾。
// 正确实现下对象必须活到 KeepAlive 处，GC 与 finalizer 不得在边界前终结它。
//
//go:noinline
func keepBoundary(v int64) {
	p := newPayload(v)
	sinkInt += p.v + int64(p.data[0]) + int64(len(p.name))

	gcStress()
	// 给 finalizer 充分的运行机会：若对象被提前判死，这里应观察到 kaFin==1。
	for i := 0; i < 20 && atomic.LoadInt32(&kaFin) == 0; i++ {
		runtime.GC()
		runtime.Gosched()
		time.Sleep(time.Millisecond)
	}
	ck(atomic.LoadInt32(&kaFin) == 0, "object finalized before KeepAlive boundary")

	runtime.KeepAlive(p)
}

func main() {
	keepBoundary(7)

	// 越过 KeepAlive 边界后对象已失活，最终必须被终结。
	deadline := time.Now().Add(10 * time.Second)
	for atomic.LoadInt32(&kaFin) == 0 && time.Now().Before(deadline) {
		runtime.GC()
		runtime.Gosched()
		time.Sleep(time.Millisecond)
	}
	ck(atomic.LoadInt32(&kaFin) == 1, "object was not finalized after KeepAlive boundary")

	finish("l18_keepalive_boundary")
}
