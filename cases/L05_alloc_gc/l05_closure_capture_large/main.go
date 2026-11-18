// L05: 闭包捕获超大对象（256KB~320KB slab 与 4MB slab）；创建后只由闭包持有，
// 两个闭包共享同一 slab 的不同视图，显式 GC + 垃圾分配后调用闭包校验
// 捕获对象存活、修改互相可见；释放部分闭包后剩余闭包仍完整。
package main

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"sync/atomic"
)

const (
	capCount = 20
	baseSlab = 256 << 10
)

var (
	failed int32
	gcSink [][]byte
)

type capFns struct {
	id        int
	n         int
	pos       int
	orig      byte
	want      uint64
	wantAfter uint64
	sumA      func() uint64
	sumB      func() uint64
	mutate    func()
	peek      func(int) byte
}

func ck(ok bool, msg string) {
	if !ok {
		atomic.AddInt32(&failed, 1)
		fmt.Println("FAIL:", msg)
	}
}

func ckf(ok bool, format string, args ...interface{}) {
	if !ok {
		atomic.AddInt32(&failed, 1)
		fmt.Printf("FAIL: "+format+"\n", args...)
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

func loadSeed() uint64 {
	s := os.Getenv("UAF_SEED")
	if s == "" {
		return 1
	}
	n, err := strconv.ParseUint(s, 10, 64)
	if err != nil {
		return 1
	}
	return n
}

func finish(name string, seed uint64) {
	if n := atomic.LoadInt32(&failed); n != 0 {
		fmt.Printf("FAILED %s (%d failed checks, seed=%d)\n", name, n, seed)
		os.Exit(1)
	}
	fmt.Println("ALL PASS " + name)
}

func sumBytes(b []byte) uint64 {
	h := uint64(1469598103934665603)
	for i := range b {
		h ^= uint64(b[i])
		h *= 1099511628211
	}
	return h
}

func slabByte(id, i int, seed uint64) byte {
	base := uint64(id)*101 + seed*13 + 7
	return byte((base + uint64(i)*7 + uint64(i>>9)*11) % 251)
}

// makeCap 只通过返回的闭包暴露 slab；创建者拿不到底层指针。
//
//go:noinline
func makeCap(id, n int, seed uint64) capFns {
	data := make([]byte, n)
	for i := range data {
		data[i] = slabByte(id, i, seed)
	}
	pos := 1 + (id*97+int(seed))%(n-2)
	orig := data[pos]
	want := sumBytes(data)
	data[pos] ^= 0x5a
	wantAfter := sumBytes(data)
	data[pos] = orig

	return capFns{
		id:        id,
		n:         n,
		pos:       pos,
		orig:      orig,
		want:      want,
		wantAfter: wantAfter,
		sumA:      func() uint64 { return sumBytes(data) },
		sumB:      func() uint64 { return sumBytes(data) },
		mutate:    func() { data[pos] ^= 0x5a },
		peek:      func(off int) byte { return data[off] },
	}
}

//go:noinline
func makeBigCap(seed uint64) capFns {
	n := 4 << 20
	data := make([]byte, n)
	for i := range data {
		data[i] = slabByte(999, i, seed)
	}
	pos := 3 + int(seed%1021)
	orig := data[pos]
	want := sumBytes(data)
	data[pos] ^= 0x5a
	wantAfter := sumBytes(data)
	data[pos] = orig
	return capFns{
		id:        999,
		n:         n,
		pos:       pos,
		orig:      orig,
		want:      want,
		wantAfter: wantAfter,
		sumA:      func() uint64 { return sumBytes(data) },
		sumB:      func() uint64 { return sumBytes(data) },
		mutate:    func() { data[pos] ^= 0x5a },
		peek:      func(off int) byte { return data[off] },
	}
}

func main() {
	seed := loadSeed()

	caps := make([]capFns, 0, capCount+1)
	for i := 0; i < capCount; i++ {
		n := baseSlab + (i*8192)%(96<<10)
		caps = append(caps, makeCap(i, n, seed))
		if i%4 == 3 {
			gcStress()
		}
	}
	bigCap := makeBigCap(seed)
	caps = append(caps, bigCap)

	gcStress()
	for i := 0; i < 12; i++ {
		junk := make([]byte, 1<<20)
		junk[0] = byte(i)
		gcSink = append(gcSink[:0], junk)
	}
	gcSink = gcSink[:0]
	runtime.GC()
	runtime.GC()

	for i := range caps {
		c := &caps[i]
		if c.sumA() != c.want {
			ckf(false, "cap id=%d sumA before mutate", c.id)
		}
		if c.sumB() != c.want {
			ckf(false, "cap id=%d sumB before mutate", c.id)
		}
		if c.peek(c.pos) != c.orig {
			ckf(false, "cap id=%d captured byte before mutate", c.id)
		}
		c.mutate()
		if c.sumA() != c.wantAfter {
			ckf(false, "cap id=%d sumA after mutate", c.id)
		}
		// 第二个闭包共享同一 slab，必须观察到第一个闭包的修改。
		if c.sumB() != c.wantAfter {
			ckf(false, "cap id=%d sumB did not observe shared mutation", c.id)
		}
		if c.peek(c.pos) != c.orig^0x5a {
			ckf(false, "cap id=%d peek after mutate", c.id)
		}
	}

	// 释放一半闭包（仅置零函数字段，捕获对象应可回收），剩余项仍可调用。
	for i := 0; i < len(caps); i += 2 {
		caps[i] = capFns{}
	}
	for as := 0; as < 8; as++ {
		junk := make([]byte, 512<<10)
		junk[0] = byte(as)
		gcSink = append(gcSink[:0], junk)
	}
	gcSink = gcSink[:0]
	runtime.GC()
	runtime.GC()
	for i := 1; i < len(caps); i += 2 {
		c := &caps[i]
		if c.sumA() != c.wantAfter || c.sumB() != c.wantAfter || c.peek(c.pos) != c.orig^0x5a {
			ckf(false, "cap id=%d corrupted after dropping half closures", c.id)
		}
	}

	// 只保留 4MB 闭包，其它全部释放，再 GC；捕获的大对象仍必须有效。
	last := bigCap
	bigCap = capFns{}
	caps = nil
	gcStress()
	if last.sumA() != last.wantAfter || last.n != 4<<20 || last.peek(last.pos) != last.orig^0x5a {
		ck(false, "4MB captured slab corrupted when only closure remains")
	}
	runtime.KeepAlive(last)

	finish("l05_closure_capture_large", seed)
}
