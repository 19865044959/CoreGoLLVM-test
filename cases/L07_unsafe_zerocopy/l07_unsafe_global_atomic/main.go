// L07: 全局变量与 atomic 的 unsafe.Pointer 存取，在 GC 后仍指向有效对象。
package main

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"sync/atomic"
	"unsafe"
)

type node struct {
	v    int64
	name string
}

type payload struct {
	tag   string
	data  []byte
	items []*node
	pad   [32]byte
}

var (
	failed    int32
	gcSink    [][]byte
	globalPtr unsafe.Pointer
	atomicPtr unsafe.Pointer
	seed      int64
	nItems    int
)

func ck(ok bool, msg string) {
	if !ok {
		atomic.AddInt32(&failed, 1)
		fmt.Printf("FAIL: %s (seed=%d)\n", msg, seed)
	}
}

func gcStress() {
	for r := 0; r < 2; r++ {
		for i := 0; i < 12; i++ {
			b := make([]byte, 8192)
			b[0] = byte(i)
			b[len(b)-1] = byte(255 - i)
			gcSink = append(gcSink[:0], b)
		}
		gcSink = gcSink[:0]
		runtime.GC()
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
func makePayload(tag string, base int64) *payload {
	p := &payload{
		tag:  tag,
		data: []byte(tag + "-data-" + strconv.FormatInt(base, 10)),
	}
	for i := 0; i < nItems; i++ {
		p.items = append(p.items, &node{
			v:    base + int64(i),
			name: tag + "-item-" + strconv.Itoa(i),
		})
	}
	p.pad[0] = byte(base)
	return p
}

//go:noinline
func installAtomic(tag string, base int64) {
	atomic.StorePointer(&atomicPtr, unsafe.Pointer(makePayload(tag, base)))
}

//go:noinline
func installGlobal(tag string, base int64) {
	globalPtr = unsafe.Pointer(makePayload(tag, base))
}

func checkPayload(p *payload, tag string, base int64, stage string) {
	if p == nil {
		ck(false, stage+": nil payload")
		return
	}
	ck(p.tag == tag, stage+": tag")
	ck(string(p.data) == tag+"-data-"+strconv.FormatInt(base, 10), stage+": data")
	ck(len(p.items) == nItems, stage+": item count")
	for i := 0; i < len(p.items) && i < nItems; i++ {
		nd := p.items[i]
		if nd == nil || nd.v != base+int64(i) || nd.name != tag+"-item-"+strconv.Itoa(i) {
			ck(false, fmt.Sprintf("%s: item %d", stage, i))
			break
		}
	}
	ck(p.pad[0] == byte(base), stage+": pad")
}

func main() {
	if s := os.Getenv("UAF_SEED"); s != "" {
		if v, err := strconv.ParseInt(s, 10, 64); err == nil {
			seed = v
		}
	}
	nItems = 4 + int(seed%3)

	installAtomic("alpha", 10)
	installGlobal("beta", 20)
	gcStress()

	a := (*payload)(atomic.LoadPointer(&atomicPtr))
	g := (*payload)(globalPtr)
	checkPayload(a, "alpha", 10, "atomic load after GC")
	checkPayload(g, "beta", 20, "global unsafe.Pointer after GC")

	// CAS 替换：旧对象仅由类型化局部变量持有。
	cand := makePayload("gamma", 30)
	ok := atomic.CompareAndSwapPointer(&atomicPtr, unsafe.Pointer(a), unsafe.Pointer(cand))
	ck(ok, "CAS with expected pointer succeeds")
	ck(atomic.LoadPointer(&atomicPtr) == unsafe.Pointer(cand), "CAS result visible after load")
	ck(!atomic.CompareAndSwapPointer(&atomicPtr, unsafe.Pointer(a), unsafe.Pointer(g)), "stale CAS rejected")
	gcStress()
	checkPayload(a, "alpha", 10, "displaced atomic payload survives GC")
	checkPayload((*payload)(atomic.LoadPointer(&atomicPtr)), "gamma", 30, "CAS-installed payload after GC")

	// Swap 返回旧值，旧值在 GC 后仍可读。
	sw := atomic.SwapPointer(&atomicPtr, unsafe.Pointer(g))
	swp := (*payload)(sw)
	ck(swp == cand, "swap returns previous payload")
	gcStress()
	checkPayload(swp, "gamma", 30, "swapped-out payload survives GC")
	checkPayload((*payload)(atomic.LoadPointer(&atomicPtr)), "beta", 20, "swap-installed payload after GC")

	// 反复改写普通全局 unsafe.Pointer，跨越多次 GC。
	for i := 0; i < 4; i++ {
		tag := "rotate-" + strconv.Itoa(i)
		installGlobal(tag, int64(40+i))
		gcStress()
		checkPayload((*payload)(globalPtr), tag, int64(40+i), "rotated global payload")
	}

	// 清空全局槽位；类型化局部引用必须继续保活对象。
	globalPtr = nil
	atomic.StorePointer(&atomicPtr, nil)
	gcStress()
	checkPayload(a, "alpha", 10, "alpha after globals cleared")
	checkPayload(g, "beta", 20, "beta after globals cleared")
	checkPayload(swp, "gamma", 30, "gamma after globals cleared")
	ck(globalPtr == nil, "global slot cleared")
	ck(atomic.LoadPointer(&atomicPtr) == nil, "atomic slot cleared")

	runtime.KeepAlive(a)
	runtime.KeepAlive(g)
	runtime.KeepAlive(swp)
	finish("l07_unsafe_global_atomic")
}
