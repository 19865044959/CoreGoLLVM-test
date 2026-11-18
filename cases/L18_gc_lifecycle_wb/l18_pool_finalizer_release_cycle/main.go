// L18: sync.Pool + finalizer 释放周期。
// 每轮从全局池 Get 一批带 finalizer 的对象并外部持有：churn 与 GC 后它们不得
// 被终结且 payload 完整；轮末放入全局 held。所有轮次结束后同时丢弃池与 held，
// 全部池化对象（含仍留在池中的）必须最终被终结。
package main

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

const (
	pfPerRound    = 64
	pfChurnPerRun = 512
	pfDefault     = 3
	pfMaxRounds   = 16
)

type pfObj struct {
	id   int32
	ver  int64
	data []byte
}

var (
	failed   int32
	pfFlags  []int32
	pfNext   int32
	pfPool   *sync.Pool
	pfHeld   []*pfObj
	pfMaxIDs = 1 << 16
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
	if n > pfMaxRounds {
		n = pfMaxRounds
	}
	return n
}

//go:noinline
func newPFObj() *pfObj {
	id := atomic.AddInt32(&pfNext, 1)
	o := &pfObj{id: id, data: make([]byte, 32)}
	for i := range o.data {
		o.data[i] = byte((int(id)*5 + i) % 251)
	}
	runtime.SetFinalizer(o, func(q *pfObj) {
		idx := int(q.id)
		if idx > 0 && idx < len(pfFlags) {
			atomic.StoreInt32(&pfFlags[idx], 1)
		}
	})
	return o
}

//go:noinline
func acquireHeld(n int, ver int64) []*pfObj {
	out := make([]*pfObj, 0, n)
	for i := 0; i < n; i++ {
		o := pfPool.Get().(*pfObj)
		o.ver = ver
		o.data[0] = byte((int(o.id) + int(ver)) % 251)
		o.data[1] = 0x5a
		out = append(out, o)
	}
	return out
}

//go:noinline
func churnPool(n int) {
	for i := 0; i < n; i++ {
		o := pfPool.Get().(*pfObj)
		o.ver = int64(i)
		o.data[0] = byte(i)
		pfPool.Put(o)
	}
}

func checkHeld(objs []*pfObj, ver int64) int {
	bad := 0
	for _, o := range objs {
		if o == nil || o.ver != ver || o.data[1] != 0x5a || int(o.data[0]) != (int(o.id)+int(ver))%251 {
			bad++
		}
	}
	return bad
}

func countFinalized(objs []*pfObj) int {
	n := 0
	for _, o := range objs {
		if atomic.LoadInt32(&pfFlags[o.id]) != 0 {
			n++
		}
	}
	return n
}

func main() {
	pfFlags = make([]int32, pfMaxIDs)
	pfPool = &sync.Pool{New: func() interface{} { return newPFObj() }}
	rounds := tortureRounds(pfDefault)

	var lastHeld []*pfObj
	for r := 1; r <= rounds; r++ {
		held := acquireHeld(pfPerRound, int64(r))
		churnPool(pfChurnPerRun)
		runtime.GC()
		runtime.GC()

		ck(checkHeld(held, int64(r)) == 0, fmt.Sprintf("held pooled objects corrupted in round %d", r))
		ck(countFinalized(held) == 0, fmt.Sprintf("%d held pooled objects finalized in round %d", countFinalized(held), r))
		pfHeld = append(pfHeld, held...)
		lastHeld = held
	}

	// 所有外部持有的池化对象在多次 GC 后仍不得被终结。
	runtime.GC()
	runtime.GC()
	ck(countFinalized(pfHeld) == 0, fmt.Sprintf("%d externally-held pooled objects finalized before release", countFinalized(pfHeld)))

	// 同时丢弃池与外部引用，所有对象最终必须被终结。
	total := atomic.LoadInt32(&pfNext)
	ck(int(total) < len(pfFlags), "object id overflow")
	pfHeld = nil
	pfPool = nil
	runtime.KeepAlive(lastHeld)

	deadline := time.Now().Add(5 * time.Second)
	pending := func() int {
		n := 0
		for id := int32(1); id <= total; id++ {
			if atomic.LoadInt32(&pfFlags[id]) == 0 {
				n++
			}
		}
		return n
	}
	for pending() != 0 && time.Now().Before(deadline) {
		runtime.GC()
		runtime.Gosched()
		time.Sleep(time.Millisecond)
	}
	ck(pending() == 0, fmt.Sprintf("%d pooled objects not finalized after pool and external refs released", pending()))
	finish("l18_pool_finalizer_release_cycle")
}
