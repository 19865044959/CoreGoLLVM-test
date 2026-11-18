// L03: 长循环内对 []interface 做动态方法调用，接口值持有堆节点指针；
// 每 64K 轮分配垃圾并显式 GC，另有 GC pump 并发，校验 itab 分发与指针存活。
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
	gcKeep [][]byte
	ifKeep []byte
)

const (
	ifSlots   = 128
	ifCount   = 256
	ifMin     = 1 << 19
	ifCapLoop = 1 << 24
	ifGCDiv   = 1 << 16
)

type iNode struct {
	v   int64
	tag string
}

type summer interface {
	add(x int64) int64
	id() int
}

type implA struct {
	p *iNode
	k int64
}

type implB struct {
	p *iNode
	k int64
}

func (a *implA) add(x int64) int64 { return a.p.v + x + a.k }
func (a *implA) id() int           { return 1 }

func (b *implB) add(x int64) int64 { return b.p.v*2 + x + b.k*2 }
func (b *implB) id() int           { return 2 }

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

func ifIdxAt(i, step, off int64) int {
	return int((i*step + off) & (ifCount - 1))
}

func ifNodeOf(k, seedOff int) int {
	return (k*3 + seedOff) % ifSlots
}

func ifExpected(i int64, k, seedOff int) int64 {
	nv := int64(ifNodeOf(k, seedOff))*4 + 9
	var v int64
	if k&1 == 0 {
		v = nv + i + int64(k)
	} else {
		v = nv*2 + i + int64(k)*2
	}
	if i%128 == 0 {
		if k&1 == 0 {
			v++
		} else {
			v += 2
		}
	}
	return v
}

func main() {
	seed := seedFromEnv()
	if seed < 0 {
		seed = -seed
	}
	rng := rand.New(rand.NewSource(seed))
	step := int64(2*rng.Intn(16) + 1)
	off := int64(rng.Intn(ifCount))
	seedOff := int(seed % int64(ifSlots))

	backing := make([]iNode, ifSlots)
	for i := range backing {
		backing[i].v = int64(i)*4 + 9
		backing[i].tag = "i"
	}
	ifaces := make([]summer, ifCount)
	for k := 0; k < ifCount; k++ {
		p := &backing[ifNodeOf(k, seedOff)]
		if k&1 == 0 {
			ifaces[k] = &implA{p: p, k: int64(k)}
		} else {
			ifaces[k] = &implB{p: p, k: int64(k)}
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
			gcKeep = junk
			runtime.GC()
			epoch.Add(1)
		}
		gcKeep = nil
	}()
	close(start)

	var sum int64
	var done int64
	for (epoch.Load() < 1 || done < ifMin) && done < ifCapLoop {
		k := ifIdxAt(done, step, off)
		s := ifaces[k]
		sum += s.add(done)
		if done%128 == 0 {
			sum += int64(s.id())
		}
		if done%ifGCDiv == 0 {
			ifKeep = make([]byte, 16384)
			ifKeep[0] = byte(done)
			runtime.GC()
		}
		done++
	}
	now := epoch.Load()
	wg.Wait()

	var expected int64
	for i := int64(0); i < done; i++ {
		expected += ifExpected(i, ifIdxAt(i, step, off), seedOff)
	}

	ck(now >= 1, fmt.Sprintf("GC pump completed during iface loop (seed=%d)", seed))
	ck(done >= ifMin, fmt.Sprintf("iface loop executed (seed=%d)", seed))
	ck(sum == expected, fmt.Sprintf("interface dispatch checksum (seed=%d)", seed))

	badType := int32(0)
	for k := 0; k < ifCount; k++ {
		switch v := ifaces[k].(type) {
		case *implA:
			if v.p.v != int64(ifNodeOf(k, seedOff))*4+9 || v.p.tag != "i" {
				atomic.AddInt32(&badType, 1)
			}
		case *implB:
			if v.p.v != int64(ifNodeOf(k, seedOff))*4+9 || v.p.tag != "i" {
				atomic.AddInt32(&badType, 1)
			}
		default:
			atomic.AddInt32(&badType, 1)
		}
	}
	ck(atomic.LoadInt32(&badType) == 0, fmt.Sprintf("iface-held nodes intact after GC (seed=%d)", seed))
	runtime.KeepAlive(backing)
	runtime.KeepAlive(ifaces)
	runtime.KeepAlive(ifKeep)
	finish("l03_iface_call_loop")
}
