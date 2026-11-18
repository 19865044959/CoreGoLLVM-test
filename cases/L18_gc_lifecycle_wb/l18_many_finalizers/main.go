// L18: 大量 finalizer 对象——可达对象不得被终结，失活对象最终必须被终结。
// 只断言这两类属性，不依赖 finalizer 的具体触发时机。
package main

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"sync/atomic"
	"time"
)

const (
	maxIDs    = 1 << 17
	perRound  = 1024
	maxRounds = 16
)

type fobj struct {
	id  int32
	pad [8]int64
}

var (
	failed    int32
	finFlags  []int32
	droppedID []int32
	kept      []*fobj
	nextID    int32
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

func tortureRounds(def int) int {
	s := os.Getenv("TORTURE_ROUNDS")
	if s == "" {
		return def
	}
	n, err := strconv.Atoi(s)
	if err != nil || n <= 0 {
		return def
	}
	if n > maxRounds {
		n = maxRounds
	}
	return n
}

func newFObj() *fobj {
	id := atomic.AddInt32(&nextID, 1)
	o := &fobj{id: id}
	runtime.SetFinalizer(o, func(q *fobj) {
		if q.id > 0 && int(q.id) < len(finFlags) {
			atomic.StoreInt32(&finFlags[q.id], 1)
		}
	})
	return o
}

// installKept 创建 n 个全局可达对象；它们的 finalizer 绝不能运行。
//
//go:noinline
func installKept(n int) {
	for i := 0; i < n; i++ {
		kept = append(kept, newFObj())
	}
}

// installDropped 创建 n 个立即失活的对象；返回后仅保留 id 用于断言。
//
//go:noinline
func installDropped(n int) {
	for i := 0; i < n; i++ {
		o := newFObj()
		droppedID = append(droppedID, o.id)
	}
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

func finalizedCount(objs []*fobj) int {
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
	rounds := tortureRounds(2)

	for r := 0; r < rounds; r++ {
		installKept(perRound)
		installDropped(perRound)
		runtime.GC()
		runtime.GC()
	}
	runtime.KeepAlive(kept)

	// 失活对象必须最终全部被终结。
	deadline := time.Now().Add(10 * time.Second)
	for pendingFinalizers(droppedID) != 0 && time.Now().Before(deadline) {
		runtime.GC()
		runtime.Gosched()
		time.Sleep(time.Millisecond)
	}
	ck(pendingFinalizers(droppedID) == 0, fmt.Sprintf("%d unreachable objects were not finalized", pendingFinalizers(droppedID)))

	// 多轮 GC 后，仍被全局切片持有的对象绝不能已被终结。
	runtime.GC()
	runtime.GC()
	ck(finalizedCount(kept) == 0, fmt.Sprintf("%d reachable objects were finalized", finalizedCount(kept)))
	runtime.GC()
	ck(finalizedCount(kept) == 0, fmt.Sprintf("%d reachable objects finalized on later GC", finalizedCount(kept)))

	finish("l18_many_finalizers")
}
