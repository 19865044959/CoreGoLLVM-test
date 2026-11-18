// L05: 变尺寸 byte 缓冲池：同一 sync.Pool 服务 1B..1MB+ 的请求，
// 每轮同时持有多个不同尺寸缓冲，跨显式 GC 校验内容与容量契约，
// 释放后按不同尺寸再取，确认不残留旧内容、不重复返回同一对象。
package main

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"sync"
	"sync/atomic"
)

const (
	poolRounds = 192
	liveCount  = 8
)

var (
	failed int32
	gcSink [][]byte

	sizeClasses = [...]int{
		1, 2, 7, 16, 63, 64, 65, 127, 128, 255, 256, 257,
		511, 1023, 1024, 4096, 65535, 65536, 1<<20 + 7,
	}
)

type byteBuf struct {
	data []byte
}

var bufPool = sync.Pool{New: func() interface{} {
	return &byteBuf{data: make([]byte, 0, 64)}
}}

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

func sizeAt(k int, seed uint64) int {
	return sizeClasses[(k*7+int(seed))%len(sizeClasses)]
}

func acquire(n int) *byteBuf {
	b := bufPool.Get().(*byteBuf)
	if cap(b.data) < n {
		b.data = make([]byte, n)
	} else {
		b.data = b.data[:n]
	}
	return b
}

func bufByte(gen, i int, seed uint64) byte {
	base := uint64(gen)*31 + seed*17 + 5
	return byte((base + uint64(i)*7) % 251)
}

func fillBuf(b *byteBuf, gen int, seed uint64) {
	for i := range b.data {
		b.data[i] = bufByte(gen, i, seed)
	}
}

func checkBuf(b *byteBuf, gen int, seed uint64) bool {
	for i := range b.data {
		if b.data[i] != bufByte(gen, i, seed) {
			return false
		}
	}
	return true
}

func release(b *byteBuf) {
	b.data = b.data[:0]
	bufPool.Put(b)
}

func main() {
	seed := loadSeed()

	for round := 0; round < poolRounds; round++ {
		live := make([]*byteBuf, 0, liveCount+4)
		gens := make([]int, 0, liveCount+4)
		for k := 0; k < liveCount; k++ {
			n := sizeAt(round*13+k, seed)
			b := acquire(n)
			if cap(b.data) < n || len(b.data) != n {
				ckf(false, "round=%d slot=%d n=%d len=%d cap=%d", round, k, n, len(b.data), cap(b.data))
			}
			gen := round*100 + k
			fillBuf(b, gen, seed)
			live = append(live, b)
			gens = append(gens, gen)
		}

		// 同时存活的缓冲必须是不同对象，写一个不得影响另一个。
		for i := 0; i < len(live); i++ {
			for j := i + 1; j < len(live); j++ {
				if live[i] == live[j] {
					ckf(false, "round=%d duplicate live buffer %d/%d", round, i, j)
				}
			}
		}
		if len(live) >= 2 && len(live[0].data) > 0 && len(live[1].data) > 0 {
			saved := live[1].data[0]
			live[0].data[0] ^= 0xff
			if live[1].data[0] != saved {
				ckf(false, "round=%d live buffers alias", round)
			}
			live[0].data[0] ^= 0xff
		}

		if round%16 == 15 {
			gcStress()
		}
		for k := range live {
			if !checkBuf(live[k], gens[k], seed) {
				ckf(false, "round=%d slot=%d content corrupted", round, k)
			}
		}

		// 释放一半，再按完全不同的尺寸取回，验证容量契约与内容独立性。
		kept := live[liveCount/2:]
		keptGens := gens[liveCount/2:]
		for k := 0; k < liveCount/2; k++ {
			release(live[k])
			live[k] = nil
		}
		newLive := make([]*byteBuf, 0, liveCount/2)
		newGens := make([]int, 0, liveCount/2)
		for k := 0; k < liveCount/2; k++ {
			n := sizeAt(round*29+k+liveCount, seed)
			b := acquire(n)
			if len(b.data) != n || cap(b.data) < n {
				ckf(false, "round=%d reacquire slot=%d n=%d len=%d cap=%d", round, k, n, len(b.data), cap(b.data))
			}
			gen := round*100 + liveCount + k
			fillBuf(b, gen, seed)
			newLive = append(newLive, b)
			newGens = append(newGens, gen)
		}
		all := append(kept, newLive...)
		allGens := append(keptGens, newGens...)
		for i := 0; i < len(all); i++ {
			for j := i + 1; j < len(all); j++ {
				if all[i] == all[j] {
					ckf(false, "round=%d duplicate live buffer after refill %d/%d", round, i, j)
				}
			}
		}
		for k := range all {
			if !checkBuf(all[k], allGens[k], seed) {
				ckf(false, "round=%d slot=%d corrupted after refill", round, k)
			}
		}
		for k := range all {
			release(all[k])
		}
	}

	// 池经多轮 GC 后可能已清空；重新请求全部尺寸档，契约不变。
	gcStress()
	for k, n := range sizeClasses {
		b := acquire(n)
		if len(b.data) != n || cap(b.data) < n {
			ckf(false, "final class=%d n=%d len=%d cap=%d", k, n, len(b.data), cap(b.data))
		}
		gen := 90000 + k
		fillBuf(b, gen, seed)
		if !checkBuf(b, gen, seed) {
			ckf(false, "final class=%d content mismatch", k)
		}
		release(b)
	}

	finish("l05_varisize_byte_pool", seed)
}
