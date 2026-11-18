// L07: atomic 指针交换：Store/Swap/CAS 链、被换下对象跨 GC、并发读写不变量。
package main

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"sync"
	"sync/atomic"
)

type pnode struct {
	v   int64
	tag string
}

type payload struct {
	gen  uint64
	name string
	data []byte
	next *pnode
	tail []*pnode
}

var (
	failed int32
	gcSink [][]byte
	seed   int64
)

func ck(ok bool, msg string) {
	if !ok {
		atomic.AddInt32(&failed, 1)
		fmt.Printf("FAIL: %s (seed=%d)\n", msg, seed)
	}
}

func gcStress() {
	for r := 0; r < 2; r++ {
		for i := 0; i < 10; i++ {
			b := make([]byte, 8192)
			b[0] = byte(i ^ r)
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

func newPayload(gen int) *payload {
	g := int64(gen)
	return &payload{
		gen:  uint64(gen),
		name: "p-" + strconv.Itoa(gen),
		data: []byte("d-" + strconv.Itoa(gen)),
		next: &pnode{v: g * 2, tag: "t-" + strconv.Itoa(gen)},
		tail: []*pnode{
			{v: g, tag: "a-" + strconv.Itoa(gen)},
			{v: g + 1, tag: "b-" + strconv.Itoa(gen)},
		},
	}
}

func checkPayload(p *payload, gen int, stage string) {
	if p == nil {
		ck(false, stage+": nil payload")
		return
	}
	ck(p.gen == uint64(gen), stage+": gen")
	ck(p.name == "p-"+strconv.Itoa(gen), stage+": name")
	ck(string(p.data) == "d-"+strconv.Itoa(gen), stage+": data")
	if p.next == nil {
		ck(false, stage+": nil next")
	} else {
		ck(p.next.v == int64(gen)*2 && p.next.tag == "t-"+strconv.Itoa(gen), stage+": next")
	}
	ck(len(p.tail) == 2, stage+": tail len")
	if len(p.tail) == 2 && p.tail[0] != nil && p.tail[1] != nil {
		ck(p.tail[0].v == int64(gen) && p.tail[0].tag == "a-"+strconv.Itoa(gen), stage+": tail0")
		ck(p.tail[1].v == int64(gen)+1 && p.tail[1].tag == "b-"+strconv.Itoa(gen), stage+": tail1")
	} else {
		ck(false, stage+": nil tail node")
	}
}

func tortureRounds(def int) int {
	if s := os.Getenv("TORTURE_ROUNDS"); s != "" {
		if v, err := strconv.Atoi(s); err == nil && v > 0 {
			return v
		}
	}
	return def
}

func main() {
	if s := os.Getenv("UAF_SEED"); s != "" {
		if v, err := strconv.ParseInt(s, 10, 64); err == nil {
			seed = v
		}
	}
	if seed < 0 {
		seed = -seed
	}
	rounds := tortureRounds(20)
	base := int(seed%100) * 1000

	slots := new([4]atomic.Pointer[payload])

	// 1) 逐轮 Store/Load/Swap/CAS，所有换下的对象由类型化局部引用保活。
	for r := 0; r < rounds; r++ {
		g0 := base + r*4
		p0 := newPayload(g0)
		slots[0].Store(p0)
		gcStress()
		got := slots[0].Load()
		ck(got == p0, "load identity")
		checkPayload(got, g0, "after store+GC")

		p1 := newPayload(g0 + 1)
		old := slots[0].Swap(p1)
		ck(old == p0, "swap returns previous")
		ck(slots[0].Load() == p1, "swap installs new")
		garbage := make([]byte, 4096)
		garbage[0] = byte(r)
		gcSink = append(gcSink[:0], garbage)
		runtime.GC()
		checkPayload(old, g0, "swapped-out after GC")
		checkPayload(slots[0].Load(), g0+1, "swapped-in after GC")

		p2 := newPayload(g0 + 2)
		ck(slots[0].CompareAndSwap(p1, p2), "CAS success")
		ck(!slots[0].CompareAndSwap(p1, p2), "stale CAS rejected")
		checkPayload(slots[0].Load(), g0+2, "after CAS")
		checkPayload(p1, g0+1, "CAS loser local")

		// 每轮结束清空槽位，类型化引用仍然有效。
		ck(slots[0].Swap(nil) == p2, "final swap drains slot")
		ck(slots[0].Load() == nil, "slot drained")
		gcStress()
		checkPayload(p2, g0+2, "drained after GC")
	}

	// 2) 槽位链式所有权转移：Swap 串起 0->1->2，所有对象在局部引用下跨 GC。
	gA := base + 9000
	gB := gA + 1
	gC := gA + 2
	a := newPayload(gA)
	b := newPayload(gB)
	c := newPayload(gC)
	slots[0].Store(a)
	slots[1].Store(b)
	slots[2].Store(c)
	x := slots[0].Swap(nil)
	y := slots[1].Swap(x)
	z := slots[2].Swap(y)
	ck(x == a && y == b && z == c, "ownership chain results")
	ck(slots[0].Load() == nil && slots[1].Load() == a && slots[2].Load() == b,
		"ownership chain slots")
	gcStress()
	ck(slots[1].Load() == a && slots[2].Load() == b, "chain slots after GC")
	checkPayload(a, gA, "chain a")
	checkPayload(b, gB, "chain b")
	checkPayload(z, gC, "chain c")
	slots[1].Store(nil)
	slots[2].Store(nil)
	gcStress()
	checkPayload(z, gC, "chain c drained")

	// 3) 并发：写者交替 Store A/B，读者 Load 并校验不可变 payload 的不变量；
	//    reader 周期性触发 GC，槽位写入与并发标记同时发生。
	ca := newPayload(base + 10000)
	cb := newPayload(base + 10001)
	iters := 512 + int(seed%64)
	if rounds > 20 {
		iters += (rounds - 20) * 32
	}
	var mismatches int32
	var wg sync.WaitGroup
	slots[3].Store(ca)
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < iters; i++ {
			if i%2 == 0 {
				slots[3].Store(ca)
			} else {
				slots[3].Store(cb)
			}
			if i%64 == 0 {
				g := make([]byte, 2048)
				g[0] = byte(i)
				gcSink = append(gcSink[:0], g)
			}
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < iters*2; i++ {
			q := slots[3].Load()
			if q != ca && q != cb {
				atomic.AddInt32(&mismatches, 1)
				continue
			}
			if q.gen != uint64(base+10000) && q.gen != uint64(base+10001) {
				atomic.AddInt32(&mismatches, 1)
				continue
			}
			if q.name != "p-"+strconv.FormatUint(q.gen, 10) ||
				string(q.data) != "d-"+strconv.FormatUint(q.gen, 10) {
				atomic.AddInt32(&mismatches, 1)
				continue
			}
			if q.next == nil || q.next.v != int64(q.gen)*2 {
				atomic.AddInt32(&mismatches, 1)
				continue
			}
			if i%128 == 0 {
				runtime.GC()
			}
		}
	}()
	wg.Wait()
	ck(atomic.LoadInt32(&mismatches) == 0, "concurrent atomic swap invariants")
	final := slots[3].Load()
	ck(final == ca || final == cb, "final slot points at one of two payloads")
	checkPayload(ca, base+10000, "concurrent A")
	checkPayload(cb, base+10001, "concurrent B")
	slots[3].Store(nil)
	gcStress()
	checkPayload(ca, base+10000, "concurrent A after drain")
	checkPayload(cb, base+10001, "concurrent B after drain")
	slots[0].Store(nil)
	slots[1].Store(nil)
	slots[2].Store(nil)

	runtime.KeepAlive(a)
	runtime.KeepAlive(b)
	runtime.KeepAlive(c)
	runtime.KeepAlive(x)
	runtime.KeepAlive(y)
	runtime.KeepAlive(z)
	runtime.KeepAlive(ca)
	runtime.KeepAlive(cb)
	finish("l07_atomic_pointer_swap_chain")
}
