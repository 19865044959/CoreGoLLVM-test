// L18: finalizer 队列语义——finalizer 只把对象 id 投递到有界 channel。
// 断言四类性质：全局可达对象不得入队；失活对象最终全部入队且只入队一次；
// 被可达父共享的子对象不得提前入队；释放全部根后所有对象最终入队。
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
	fqLiveN     = 192
	fqDeadN     = 192
	fqSharedN   = 64
	fqQueueCap  = 2048
	fqFlagCap   = 2048
	fqWaitLimit = 5 * time.Second
)

type fqObj struct {
	id   int32
	data [32]byte
}

type fqHolder struct {
	child *fqObj
}

var (
	failed         int32
	fqQueue        chan int32
	fqFlags        []int32
	fqNext         int32
	fqDup          int32
	fqOverflow     int32
	fqLive         []*fqObj
	fqSharedParent []*fqHolder
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

func seedValue() int64 {
	s := os.Getenv("UAF_SEED")
	if s == "" {
		return 1
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 1
	}
	return n
}

func fqByte(id int32, i int) byte { return byte((int(id)*37 + i*19) % 251) }

//go:noinline
func fqNew() *fqObj {
	id := atomic.AddInt32(&fqNext, 1)
	o := &fqObj{id: id}
	for i := range o.data {
		o.data[i] = fqByte(id, i)
	}
	runtime.SetFinalizer(o, func(q *fqObj) {
		idx := q.id
		if idx <= 0 || int(idx) >= len(fqFlags) {
			atomic.AddInt32(&fqOverflow, 1)
			return
		}
		if atomic.AddInt32(&fqFlags[idx], 1) > 1 {
			atomic.AddInt32(&fqDup, 1)
		}
		select {
		case fqQueue <- idx:
		default:
			atomic.AddInt32(&fqOverflow, 1)
		}
	})
	return o
}

//go:noinline
func fqMakeLive(n int) (objs []*fqObj, ids []int32) {
	for i := 0; i < n; i++ {
		o := fqNew()
		objs = append(objs, o)
		ids = append(ids, o.id)
	}
	return objs, ids
}

//go:noinline
func fqMakeDead(n int) []int32 {
	ids := make([]int32, 0, n)
	for i := 0; i < n; i++ {
		o := fqNew()
		ids = append(ids, o.id)
	}
	return ids
}

// fqMakeShared 让可达父与立即失活的父引用同一个子；子带 finalizer。
//
//go:noinline
func fqMakeShared(n int) (parents []*fqHolder, childIDs []int32) {
	for i := 0; i < n; i++ {
		ch := fqNew()
		parents = append(parents, &fqHolder{child: ch})
		dead := &fqHolder{child: ch}
		childIDs = append(childIDs, ch.id)
		_ = dead
	}
	return parents, childIDs
}

func fqDrain(seen map[int32]bool) int {
	n := 0
	for {
		select {
		case id := <-fqQueue:
			seen[id] = true
			n++
		default:
			return n
		}
	}
}

func fqObserve(pause time.Duration) {
	for i := 0; i < 20; i++ {
		runtime.GC()
		runtime.Gosched()
		time.Sleep(pause)
	}
}

func fqToSet(ids []int32) map[int32]bool {
	out := make(map[int32]bool, len(ids))
	for _, id := range ids {
		out[id] = true
	}
	return out
}

func main() {
	seed := seedValue()
	pause := time.Duration(1+seed%3) * time.Millisecond
	fqQueue = make(chan int32, fqQueueCap)
	fqFlags = make([]int32, fqFlagCap)

	// 阶段一：全局可达对象在多个 GC 窗口内不得入队。
	var liveIDs []int32
	fqLive, liveIDs = fqMakeLive(fqLiveN)
	fqObserve(pause)
	seen := make(map[int32]bool)
	fqDrain(seen)
	if len(seen) != 0 {
		ck(false, fmt.Sprintf("reachable objects were queued: %d (seed=%d)", len(seen), seed))
	}

	// 阶段二：失活对象最终全部入队，且没有非预期 id。
	deadIDs := fqMakeDead(fqDeadN)
	wantDead := fqToSet(deadIDs)
	runtime.GC()
	runtime.GC()
	deadline := time.Now().Add(fqWaitLimit)
	for len(seen) < len(wantDead) && time.Now().Before(deadline) {
		runtime.GC()
		fqDrain(seen)
		if len(seen) >= len(wantDead) {
			break
		}
		runtime.Gosched()
		time.Sleep(pause)
	}
	missingDead := 0
	for id := range wantDead {
		if !seen[id] {
			missingDead++
		}
	}
	ck(missingDead == 0, fmt.Sprintf("dead objects not queued: %d/%d (seed=%d)", missingDead, len(wantDead), seed))
	unexpectedDead := 0
	for id := range seen {
		if !wantDead[id] {
			unexpectedDead++
		}
	}
	ck(unexpectedDead == 0, fmt.Sprintf("unexpected ids queued in dead phase: %d (seed=%d)", unexpectedDead, seed))

	// 阶段三：失活父与可达父共享的子不得入队。
	seen = make(map[int32]bool)
	var sharedIDs []int32
	fqSharedParent, sharedIDs = fqMakeShared(fqSharedN)
	fqObserve(pause)
	fqDrain(seen)
	ck(len(seen) == 0, fmt.Sprintf("shared child queued while parent reachable: %d (seed=%d)", len(seen), seed))

	// 阶段四：释放全部根，所有对象最终入队且不重复。
	releaseIDs := make([]int32, 0, len(liveIDs)+len(sharedIDs))
	releaseIDs = append(releaseIDs, liveIDs...)
	releaseIDs = append(releaseIDs, sharedIDs...)
	wantAll := fqToSet(releaseIDs)
	seen = make(map[int32]bool)
	fqLive = nil
	fqSharedParent = nil
	runtime.GC()
	runtime.GC()
	deadline = time.Now().Add(fqWaitLimit)
	for len(seen) < len(wantAll) && time.Now().Before(deadline) {
		runtime.GC()
		fqDrain(seen)
		if len(seen) >= len(wantAll) {
			break
		}
		runtime.Gosched()
		time.Sleep(pause)
	}
	missing := 0
	for id := range wantAll {
		if !seen[id] {
			missing++
		}
	}
	ck(missing == 0, fmt.Sprintf("released objects not queued: %d/%d (seed=%d)", missing, len(wantAll), seed))
	unexpected := 0
	for id := range seen {
		if !wantAll[id] {
			unexpected++
		}
	}
	ck(unexpected == 0, fmt.Sprintf("unexpected ids queued after release: %d (seed=%d)", unexpected, seed))
	ck(atomic.LoadInt32(&fqDup) == 0, fmt.Sprintf("duplicate finalizer deliveries=%d (seed=%d)", atomic.LoadInt32(&fqDup), seed))
	ck(atomic.LoadInt32(&fqOverflow) == 0, fmt.Sprintf("finalizer queue overflow/invalid id=%d (seed=%d)", atomic.LoadInt32(&fqOverflow), seed))

	finish("l18_finalizer_queue_drain")
}
