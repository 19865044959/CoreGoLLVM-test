// L03: 接口表 + 闭包环境的长循环。注册表由 256 个接口值组成，一半是
// 持有堆节点的结构体实现，一半是捕获堆节点的闭包（func 类型实现接口）。
// 长循环通过 itab 动态分派调用，每 4096 轮显式 GC 并再次调用同一接口值，
// 验证闭包环境里的节点指针跨 GC 存活；另有并发 GC pump。
package main

import (
	"fmt"
	"math/rand"
	"os"
	"runtime"
	"strconv"
	"sync"
	"sync/atomic"
)

var (
	ifFailed int32
)

const (
	ifSlots = 256
	ifIters = 65536
	ifGCDiv = 4096
)

type ifNode struct {
	v   int64
	tag string
}

type ifProc interface {
	run(*ifNode) int64
}

// ifScale 是持有捕获节点的普通实现。
type ifScale struct {
	k int64
	p *ifNode
}

func (s *ifScale) run(n *ifNode) int64 { return n.v*s.k + s.p.v }

// ifClosure 让闭包值满足 ifProc 接口。
type ifClosure func(*ifNode) int64

func (f ifClosure) run(n *ifNode) int64 { return f(n) }

type ifDesc struct {
	kind  int8
	k     int64
	capv  int64
	extra int64
}

func ck(ok bool, msg string) {
	if !ok {
		atomic.AddInt32(&ifFailed, 1)
		fmt.Println("FAIL:", msg)
	}
}

func finish(name string) {
	if n := atomic.LoadInt32(&ifFailed); n != 0 {
		fmt.Printf("FAILED %s (%d failed checks)\n", name, n)
		os.Exit(1)
	}
	fmt.Println("ALL PASS " + name)
}

func seedFromEnv() int64 {
	if s := os.Getenv("UAF_SEED"); s != "" {
		if v, err := strconv.ParseInt(s, 10, 64); err == nil {
			return v
		}
	}
	return 1
}

func main() {
	seed := seedFromEnv()
	if seed < 0 {
		seed = -seed
	}
	rng := rand.New(rand.NewSource(seed))
	step := int64(2*rng.Intn(127) + 1)
	off := int64(rng.Intn(ifSlots))

	backing := make([]ifNode, ifSlots)
	for i := range backing {
		backing[i].v = int64(i)*3 + 1
		backing[i].tag = "if"
	}

	reg := make([]ifProc, ifSlots)
	desc := make([]ifDesc, ifSlots)
	for i := 0; i < ifSlots; i++ {
		capv := int64(i)*11 + 5
		capNode := &ifNode{v: capv, tag: "cap"}
		if i%2 == 0 {
			k := int64(i%7) + 2
			reg[i] = &ifScale{k: k, p: capNode}
			desc[i] = ifDesc{kind: 0, k: k, capv: capv}
		} else {
			extra := int64(i & 7)
			idx := i
			reg[i] = ifClosure(func(n *ifNode) int64 {
				return n.v*3 + capNode.v + int64(idx&7)
			})
			desc[i] = ifDesc{kind: 1, k: 3, capv: capv, extra: extra}
		}
	}

	start := make(chan struct{})
	var epoch atomic.Int64
	var pumpWG sync.WaitGroup
	pumpWG.Add(1)
	go func() {
		defer pumpWG.Done()
		<-start
		for r := 0; r < 8; r++ {
			junk := make([][]byte, 8)
			for j := range junk {
				junk[j] = make([]byte, 8192)
				junk[j][0] = byte(r + j)
			}
			runtime.GC()
			runtime.KeepAlive(junk)
			epoch.Add(1)
		}
	}()
	close(start)

	var sum, expSum, badAfterGC, gcCount int64
	for i := int64(0); i < ifIters; i++ {
		idx := int((i*step + off) % ifSlots)
		n := &backing[(i*7+off)%ifSlots]
		d := desc[idx]
		exp := n.v*d.k + d.capv + d.extra
		got := reg[idx].run(n)
		sum += got
		expSum += exp
		if got != exp {
			badAfterGC++
		}
		if i%ifGCDiv == 0 {
			junk := make([]byte, 16384)
			junk[0] = byte(i)
			runtime.GC()
			// 同一个闭包环境/结构体实现跨 GC 再调用一次。
			if reg[idx].run(n) != exp {
				badAfterGC++
			}
			gcCount++
			runtime.KeepAlive(junk)
		}
	}
	for epoch.Load() < 1 {
		runtime.Gosched()
	}
	now := epoch.Load()
	pumpWG.Wait()

	// 结束前按固定顺序逐个调用注册表项，复核闭包捕获节点。
	probe := &ifNode{v: 1000, tag: "probe"}
	var tableBad, nScale, nClosure int64
	for i := 0; i < ifSlots; i++ {
		d := desc[i]
		want := probe.v*d.k + d.capv + d.extra
		if reg[i].run(probe) != want {
			tableBad++
		}
		switch reg[i].(type) {
		case *ifScale:
			nScale++
		case ifClosure:
			nClosure++
		}
	}

	ck(now >= 1, fmt.Sprintf("GC pump completed during interface loop (seed=%d)", seed))
	ck(gcCount == ifIters/ifGCDiv, fmt.Sprintf("explicit GC count in loop (seed=%d)", seed))
	ck(badAfterGC == 0, fmt.Sprintf("interface dispatch result across GC (seed=%d)", seed))
	ck(sum == expSum, fmt.Sprintf("interface/closure checksum (seed=%d)", seed))
	ck(tableBad == 0, fmt.Sprintf("closure environments intact after final GC (seed=%d)", seed))
	ck(nScale == ifSlots/2 && nClosure == ifSlots/2,
		fmt.Sprintf("registry implementation mix stable (seed=%d)", seed))
	runtime.KeepAlive(reg)
	runtime.KeepAlive(backing)
	runtime.KeepAlive(probe)
	finish("l03_iface_closure_registry")
}
