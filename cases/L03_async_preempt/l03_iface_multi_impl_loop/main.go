// L03: 6 种接口实现的方法分派长循环。每轮通过接口调用 apply/weight/kind，
// 实现体持有堆节点指针；循环内周期性显式 GC，另有 GC pump 并发。
// 校验闭式分派和，并在结束后用 type switch 复核每个实现持有的节点。
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
	failed int32
	miKeep [][]byte
)

const (
	miSlots  = 192
	miImpls  = 6
	miOplist = 384
	miMin    = 1 << 17
	miCap    = 1 << 21
	miGCDiv  = 1 << 15
)

type miNode struct {
	v   int64
	tag string
}

type miOp interface {
	kind() int
	apply(x int64) int64
	weight() int64
}

type miA struct {
	p *miNode
	k int64
}

func (o *miA) kind() int           { return 0 }
func (o *miA) apply(x int64) int64 { return o.p.v + x + o.k }
func (o *miA) weight() int64       { return 3 }

type miB struct {
	p *miNode
	k int64
}

func (o *miB) kind() int           { return 1 }
func (o *miB) apply(x int64) int64 { return o.p.v*2 + x + o.k*2 }
func (o *miB) weight() int64       { return 5 }

type miC struct {
	p *miNode
	k int64
}

func (o *miC) kind() int           { return 2 }
func (o *miC) apply(x int64) int64 { return o.p.v - x + o.k*3 }
func (o *miC) weight() int64       { return 7 }

type miD struct {
	p *miNode
	k int64
}

func (o *miD) kind() int           { return 3 }
func (o *miD) apply(x int64) int64 { return (o.p.v+x)*2 + o.k }
func (o *miD) weight() int64       { return 11 }

type miE struct {
	p *miNode
	k int64
}

func (o *miE) kind() int           { return 4 }
func (o *miE) apply(x int64) int64 { return o.p.v + 2*x - o.k }
func (o *miE) weight() int64       { return 13 }

type miF struct {
	p *miNode
	k int64
}

func (o *miF) kind() int           { return 5 }
func (o *miF) apply(x int64) int64 { return o.p.v*3 + x>>1 }
func (o *miF) weight() int64       { return 17 }

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

func seedFromEnv() int64 {
	if s := os.Getenv("UAF_SEED"); s != "" {
		if v, err := strconv.ParseInt(s, 10, 64); err == nil {
			return v
		}
	}
	return 1
}

func miNodeVal(idx int) int64 { return int64(idx)*4 + 1 }

func miNodeAt(op int) int { return (op*5 + 3) % miSlots }

func miExpected(kind int, nodeV, k, x int64) int64 {
	switch kind {
	case 0:
		return nodeV + x + k
	case 1:
		return nodeV*2 + x + k*2
	case 2:
		return nodeV - x + k*3
	case 3:
		return (nodeV+x)*2 + k
	case 4:
		return nodeV + 2*x - k
	default:
		return nodeV*3 + x>>1
	}
}

func miWeight(kind int) int64 {
	switch kind {
	case 0:
		return 3
	case 1:
		return 5
	case 2:
		return 7
	case 3:
		return 11
	case 4:
		return 13
	default:
		return 17
	}
}

func main() {
	seed := seedFromEnv()
	if seed < 0 {
		seed = -seed
	}
	rng := rand.New(rand.NewSource(seed))
	step := int64(2*rng.Intn(15) + 1)
	off := int64(rng.Intn(miOplist))

	backing := make([]miNode, miSlots)
	for i := range backing {
		backing[i].v = miNodeVal(i)
		backing[i].tag = "mi"
	}
	ops := make([]miOp, miOplist)
	for j := 0; j < miOplist; j++ {
		p := &backing[miNodeAt(j)]
		k := int64(j)
		switch j % miImpls {
		case 0:
			ops[j] = &miA{p: p, k: k}
		case 1:
			ops[j] = &miB{p: p, k: k}
		case 2:
			ops[j] = &miC{p: p, k: k}
		case 3:
			ops[j] = &miD{p: p, k: k}
		case 4:
			ops[j] = &miE{p: p, k: k}
		default:
			ops[j] = &miF{p: p, k: k}
		}
	}

	var epoch atomic.Int64
	var wg sync.WaitGroup
	start := make(chan struct{})
	wg.Add(1)
	go func() {
		defer wg.Done()
		<-start
		for r := 0; r < 8; r++ {
			junk := make([][]byte, 8)
			for i := range junk {
				junk[i] = make([]byte, 8192)
				junk[i][0] = byte(r + i)
			}
			miKeep = junk
			runtime.GC()
			epoch.Add(1)
		}
		miKeep = nil
	}()
	close(start)

	var sum int64
	var done int64
	var mainGC int64
	for (epoch.Load() < 1 || done < miMin) && done < miCap {
		idx := int((done*step + off) % miOplist)
		o := ops[idx]
		sum += o.apply(done)
		if done&63 == 0 {
			sum += o.weight()
		}
		if done&255 == 0 {
			sum += int64(o.kind())
		}
		if done%miGCDiv == 0 {
			junk := make([]byte, 16384)
			junk[0] = byte(done)
			runtime.GC()
			mainGC++
			runtime.KeepAlive(junk)
		}
		done++
	}
	for epoch.Load() < 1 {
		runtime.Gosched()
	}
	now := epoch.Load()
	wg.Wait()

	var expected int64
	for i := int64(0); i < done; i++ {
		idx := int((i*step + off) % miOplist)
		kind := idx % miImpls
		nodeV := miNodeVal(miNodeAt(idx))
		expected += miExpected(kind, nodeV, int64(idx), i)
		if i&63 == 0 {
			expected += miWeight(kind)
		}
		if i&255 == 0 {
			expected += int64(kind)
		}
	}

	ck(now >= 1, fmt.Sprintf("GC pump completed during iface loop (seed=%d)", seed))
	ck(done >= miMin, fmt.Sprintf("interface loop executed (seed=%d)", seed))
	ck(sum == expected, fmt.Sprintf("multi-impl dispatch checksum (seed=%d)", seed))
	ck(mainGC == (done-1)/miGCDiv+1, fmt.Sprintf("main explicit GC inside loop (seed=%d)", seed))

	badImpl := 0
	for j, o := range ops {
		if o == nil {
			badImpl++
			continue
		}
		if o.kind() != j%miImpls || o.weight() != miWeight(j%miImpls) {
			badImpl++
		}
		want := miNodeVal(miNodeAt(j))
		switch v := o.(type) {
		case *miA:
			if v.p == nil || v.p.v != want || v.k != int64(j) {
				badImpl++
			}
		case *miB:
			if v.p == nil || v.p.v != want || v.k != int64(j) {
				badImpl++
			}
		case *miC:
			if v.p == nil || v.p.v != want || v.k != int64(j) {
				badImpl++
			}
		case *miD:
			if v.p == nil || v.p.v != want || v.k != int64(j) {
				badImpl++
			}
		case *miE:
			if v.p == nil || v.p.v != want || v.k != int64(j) {
				badImpl++
			}
		case *miF:
			if v.p == nil || v.p.v != want || v.k != int64(j) {
				badImpl++
			}
		default:
			badImpl++
		}
	}
	ck(badImpl == 0, fmt.Sprintf("interface implementations and held nodes intact (seed=%d)", seed))

	corrupt := false
	for i := range backing {
		if backing[i].v != miNodeVal(i) || backing[i].tag != "mi" {
			corrupt = true
		}
	}
	ck(!corrupt, fmt.Sprintf("iface backing nodes intact after GC (seed=%d)", seed))
	runtime.KeepAlive(backing)
	runtime.KeepAlive(ops)
	runtime.KeepAlive(miKeep)
	finish("l03_iface_multi_impl_loop")
}
