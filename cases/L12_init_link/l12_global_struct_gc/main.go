// L12: 包级 struct/map/slice/interface 初始化时含指针，GC 后校验指针与内容。
package main

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"sync/atomic"
)

var (
	failed  int32
	gcSink  [][]byte
	seedVal int64
	seedStr string
	mutID   int
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
		fmt.Printf("FAILED %s (%d failed checks) seed=%s\n", name, n, seedStr)
		os.Exit(1)
	}
	fmt.Println("ALL PASS " + name)
}

func readSeed() int64 {
	s := os.Getenv("UAF_SEED")
	if s == "" {
		return 1
	}
	v, err := strconv.ParseInt(s, 10, 64)
	if err != nil || v < 0 {
		return 1
	}
	return v
}

func checkBase() {
	ck(globalStore != nil, "global store non-nil")
	if globalStore == nil {
		return
	}
	ck(globalStore.self == globalStore, "global struct self pointer")
	ck(globalStore.label == "root/init", "init mutated global label")
	ck(len(globalStore.list) == 32 && len(globalStore.byID) == 32, "global list/map size")
	ck(globalStore.head == globalStore.list[0], "head points at first list element")
	ck(globalStore.counter == &globalCounter && *globalStore.counter == 41, "global pointer to scalar")

	n := globalStore.head
	for i := 0; i < 32; i++ {
		cur := globalStore.list[i]
		if cur == nil {
			ck(false, "nil list element")
			return
		}
		ck(cur.id == i && cur.label == "n", "node content")
		ck(globalStore.byID[i] == cur, "map entry aliases list element")
		if i < 31 {
			if cur.next != globalStore.list[i+1] {
				ck(false, "chain link")
			}
		} else if cur.next != nil {
			ck(false, "chain tail not nil")
		}
		if n != cur {
			ck(false, "chain walk identity")
		}
		n = n.next
	}
	ck(n == nil, "chain terminates")

	ck(len(globalBoxes) == 3, "global interface slice size")
	if len(globalBoxes) == 3 {
		s, ok0 := globalBoxes[0].(*gstore)
		n0, ok1 := globalBoxes[1].(*gnode)
		p0, ok2 := globalBoxes[2].(*int64)
		ck(ok0 && s == globalStore, "boxed store identity")
		ck(ok1 && n0 == globalStore.list[0], "boxed node identity")
		ck(ok2 && p0 == globalStore.counter && *p0 == 41, "boxed scalar pointer")
	}
	ck(globalStore.boxes != nil && len(globalStore.boxes) == 3, "store boxes attached in init")
}

func checkMutated() {
	ck(len(globalStore.list) == 33 && len(globalStore.byID) == 33, "mutated list/map size")
	ck(globalStore.counter == &globalCounter && globalCounter == 42, "mutated counter")
	ck(globalStore.label == "root/init/mut", "mutated label")
	head := globalStore.head
	ck(head != nil && head.id == mutID && head.label == "m", "new head content")
	if head == nil {
		return
	}
	ck(head.next == globalStore.list[0], "new head links old head")
	ck(globalStore.list[32] == head, "appended list element identity")
	ck(globalStore.byID[mutID] == head, "map holds new node")

	seen := 0
	for n := head; n != nil; n = n.next {
		seen++
		if seen > 64 {
			ck(false, "cycle in mutated chain")
			return
		}
	}
	ck(seen == 33, "mutated chain length")

	for i := 0; i < 32; i++ {
		cur := globalStore.list[i]
		ck(cur != nil && cur.id == i && cur.label == "n", "old node intact")
		ck(globalStore.byID[i] == cur, "old map entry intact")
	}

	if len(globalBoxes) == 3 {
		n1, ok1 := globalBoxes[1].(*gnode)
		ck(ok1 && n1 == head, "replaced boxed node")
		s0, ok0 := globalBoxes[0].(*gstore)
		ck(ok0 && s0 == globalStore, "boxed store unchanged")
	}
}

func main() {
	seedVal = readSeed()
	seedStr = strconv.FormatInt(seedVal, 10)

	checkBase()
	gcStress()
	checkBase()

	mutID = 100 + int(seedVal%8)
	mutateStore(mutID)
	gcStress()
	checkMutated()

	// 再制造一轮垃圾与 GC，确认改动后的全局根仍被正确扫描。
	for i := 0; i < 32; i++ {
		gcSink = append(gcSink, make([]byte, 2048))
	}
	gcSink = nil
	runtime.GC()
	checkMutated()

	finish("l12_global_struct_gc")
}
