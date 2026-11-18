// L18: finalizer 组合场景——父/子对象都带 finalizer，分成三类：
//  1. 全局可达的父+子（含被失活父共享的子）不得被终结；
//  2. noinline 内创建后立即失活的父+子必须最终被终结；
//  3. 被失活父与可达父共同引用的子，只能随可达父存活，不得被终结。
//
// finalizer 只写按 id 索引的原子标记，不读取对象内容；只断言“可达不终结 /
// 失活最终终结”两类属性。
package main

import (
	"fmt"
	"os"
	"runtime"
	"sync/atomic"
	"time"
)

const (
	livePairN   = 48
	sharedN     = 24
	deadPairN   = 64
	finalizerID = 1 << 15
)

type fchild struct {
	id   int32
	data [32]byte
}

type fparent struct {
	id    int32
	child *fchild
	tag   string
}

var (
	failed        int32
	parentFlags   []int32
	childFlags    []int32
	nextParentID  int32
	nextChildID   int32
	liveParents   []*fparent
	sharedParents []*fparent
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

//go:noinline
func newChild() *fchild {
	id := atomic.AddInt32(&nextChildID, 1)
	c := &fchild{id: id}
	for i := range c.data {
		c.data[i] = byte((int(id)*7 + i*3) % 251)
	}
	runtime.SetFinalizer(c, func(q *fchild) {
		idx := int(q.id)
		if idx > 0 && idx < len(childFlags) {
			atomic.StoreInt32(&childFlags[idx], 1)
		}
	})
	return c
}

//go:noinline
func newParent(ch *fchild) *fparent {
	id := atomic.AddInt32(&nextParentID, 1)
	p := &fparent{id: id, child: ch, tag: "fp"}
	if p.child == nil {
		p.child = newChild()
	}
	runtime.SetFinalizer(p, func(q *fparent) {
		idx := int(q.id)
		if idx > 0 && idx < len(parentFlags) {
			atomic.StoreInt32(&parentFlags[idx], 1)
		}
	})
	return p
}

//go:noinline
func makeLivePairs(n int) []*fparent {
	out := make([]*fparent, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, newParent(nil))
	}
	return out
}

// makeShared 返回可达父、失活父 id 及共享子；失活父与可达父引用同一个子。
//
//go:noinline
func makeShared(n int) (live []*fparent, deadParents []int32, sharedKids []*fchild) {
	for i := 0; i < n; i++ {
		ch := newChild()
		live = append(live, newParent(ch))
		dead := newParent(ch)
		deadParents = append(deadParents, dead.id)
		sharedKids = append(sharedKids, ch)
	}
	return live, deadParents, sharedKids
}

//go:noinline
func makeDeadPairs(n int) (parentIDs, childIDs []int32) {
	for i := 0; i < n; i++ {
		p := newParent(nil)
		parentIDs = append(parentIDs, p.id)
		childIDs = append(childIDs, p.child.id)
	}
	return parentIDs, childIDs
}

func countUnfinalized(flags []int32, ids []int32) int {
	n := 0
	for _, id := range ids {
		if atomic.LoadInt32(&flags[id]) == 0 {
			n++
		}
	}
	return n
}

func parentIDsOf(ps []*fparent) []int32 {
	ids := make([]int32, 0, len(ps))
	for _, p := range ps {
		ids = append(ids, p.id)
	}
	return ids
}

func childIDsOf(is []*fchild) []int32 {
	ids := make([]int32, 0, len(is))
	for _, c := range is {
		ids = append(ids, c.id)
	}
	return ids
}

func waitFinalized(flags []int32, ids []int32, timeout time.Duration) int {
	deadline := time.Now().Add(timeout)
	for {
		pending := countUnfinalized(flags, ids)
		if pending == 0 {
			return 0
		}
		if time.Now().After(deadline) {
			return pending
		}
		runtime.GC()
		runtime.Gosched()
		time.Sleep(time.Millisecond)
	}
}

// observeReachable 给误终结一个暴露窗口：多次 GC 后仍应保持未终结。
func observeReachable(flags []int32, ids []int32) int {
	for i := 0; i < 20; i++ {
		runtime.GC()
		runtime.Gosched()
		time.Sleep(time.Millisecond)
	}
	return countUnfinalized(flags, ids)
}

func main() {
	parentFlags = make([]int32, finalizerID)
	childFlags = make([]int32, finalizerID)

	liveParents = append(liveParents, makeLivePairs(livePairN)...)
	shared, deadSharedParents, sharedKids := makeShared(sharedN)
	sharedParents = append(sharedParents, shared...)

	liveIDs := parentIDsOf(liveParents)
	sharedParentIDs := parentIDsOf(sharedParents)
	sharedChildIDs := childIDsOf(sharedKids)
	liveChildIDs := make([]int32, 0, livePairN)
	for _, p := range liveParents {
		liveChildIDs = append(liveChildIDs, p.child.id)
	}

	runtime.GC()
	runtime.GC()

	// 可达父/子即使在最终 GC 窗口内也不得被终结。
	ck(observeReachable(parentFlags, liveIDs) == len(liveIDs), "reachable parent finalized prematurely")
	ck(observeReachable(childFlags, liveChildIDs) == len(liveChildIDs), "reachable child finalized prematurely")
	ck(observeReachable(parentFlags, sharedParentIDs) == len(sharedParentIDs), "reachable shared parent finalized prematurely")
	ck(observeReachable(childFlags, sharedChildIDs) == len(sharedChildIDs), "shared child finalized while still reachable via live parent")

	// 失活父+子必须最终全部被终结。
	deadParentIDs, deadChildIDs := makeDeadPairs(deadPairN)
	deadParentIDs = append(deadParentIDs, deadSharedParents...)
	ck(waitFinalized(parentFlags, deadParentIDs, 5*time.Second) == 0, "unreachable parents not finalized")
	ck(waitFinalized(childFlags, deadChildIDs, 5*time.Second) == 0, "unreachable children not finalized")

	// 共享子仍被可达父持有，必须保持未终结。
	ck(countUnfinalized(childFlags, sharedChildIDs) == len(sharedChildIDs), "shared child finalized after dead parent was collected")
	ck(countUnfinalized(parentFlags, liveIDs) == len(liveIDs), "live parent finalized during dead-cohort collection")
	ck(countUnfinalized(parentFlags, sharedParentIDs) == len(sharedParentIDs), "live shared parent finalized during dead-cohort collection")

	runtime.KeepAlive(liveParents)
	runtime.KeepAlive(sharedParents)
	finish("l18_finalizer_graph_cohorts")
}
