// L18: sync.Pool 与 finalizer 组合——池化对象被外部持有时不得被终结；
// 池随作用域失活后的对象必须最终被终结。只断言这两类属性。
package main

import (
	"fmt"
	"os"
	"runtime"
	"sync"
	"sync/atomic"
	"time"
)

const (
	maxIDs   = 1 << 17
	liveN    = 256
	deadN    = 256
	deadPass = 3
	churnN   = 1024
)

type pobj struct {
	id   int32
	ver  int64
	data []byte
}

var (
	failed   int32
	finFlags []int32
	nextID   int32
	live     []*pobj

	pool = sync.Pool{New: func() interface{} { return newPObj() }}
)

func ck(ok bool, msg string) {
	if !ok {
		atomic.AddInt32(&failed, 1)
		fmt.Println("FAIL:", msg)
	}
}

func finish(name string) {
	if n := atomic.LoadInt32(&failed); n != 0 {
		fmt.Printf("FAILED %s (%d failed checks)\n", name, n)
		os.Exit(1)
	}
	fmt.Println("ALL PASS " + name)
}

func newPObj() *pobj {
	id := atomic.AddInt32(&nextID, 1)
	o := &pobj{id: id, data: make([]byte, 32)}
	runtime.SetFinalizer(o, func(q *pobj) {
		if q.id > 0 && int(q.id) < len(finFlags) {
			atomic.StoreInt32(&finFlags[q.id], 1)
		}
	})
	return o
}

// acquireLive 从全局池取出对象并外部持有；这些对象在整个进程中必须保持存活。
//
//go:noinline
func acquireLive(n int) {
	for i := 0; i < n; i++ {
		o := pool.Get().(*pobj)
		live = append(live, o)
	}
}

// churnPool 反复 Get/Put（含改写），模拟池化对象复用。
//
//go:noinline
func churnPool(n int) {
	for i := 0; i < n; i++ {
		o := pool.Get().(*pobj)
		o.ver = int64(i)
		o.data[0] = byte(i)
		pool.Put(o)
	}
}

// deadBatch 在函数内建局部池，Put 后返回；池与对象在返回后不可达，必须最终被终结。
//
//go:noinline
func deadBatch(n int) []int32 {
	var p sync.Pool
	p.New = func() interface{} { return newPObj() }
	ids := make([]int32, 0, n)
	for i := 0; i < n; i++ {
		o := p.Get().(*pobj)
		o.ver = int64(i)
		p.Put(o)
		ids = append(ids, o.id)
	}
	return ids
}

func pendingFinalizers(ids []int32) int {
	n := 0
	for _, id := range ids {
		if atomic.LoadInt32(&finFlags[id]) == 0 {
			n++
		}
	}
	return n
}

func finalizedCount(objs []*pobj) int {
	n := 0
	for _, o := range objs {
		if atomic.LoadInt32(&finFlags[o.id]) != 0 {
			n++
		}
	}
	return n
}

func main() {
	finFlags = make([]int32, maxIDs)

	acquireLive(liveN)
	churnPool(churnN)

	var deadIDs []int32
	for i := 0; i < deadPass; i++ {
		deadIDs = append(deadIDs, deadBatch(deadN)...)
		runtime.GC()
		runtime.GC()
	}

	// 池/作用域失活的对象必须最终全部被终结。
	deadline := time.Now().Add(10 * time.Second)
	for pendingFinalizers(deadIDs) != 0 && time.Now().Before(deadline) {
		runtime.GC()
		runtime.Gosched()
		time.Sleep(time.Millisecond)
	}
	ck(pendingFinalizers(deadIDs) == 0, fmt.Sprintf("%d pooled objects not finalized after pool became unreachable", pendingFinalizers(deadIDs)))

	// 外部仍持有的池化对象绝不能已被终结。
	runtime.GC()
	runtime.GC()
	churnPool(churnN)
	runtime.GC()
	ck(finalizedCount(live) == 0, fmt.Sprintf("%d externally-held pooled objects were finalized", finalizedCount(live)))

	runtime.KeepAlive(live)
	finish("l18_pool_finalizer_combo")
}
