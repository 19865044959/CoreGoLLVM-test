// L13: byte 池：size-class 复用、GC 掉池、清零隔离、直接大分配与错误尺寸归还。
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

type pooledBuf struct {
	b     []byte
	class int
}

var sizeClasses = [...]int{256, 1024, 4096, 16384, 65536, 262144, 1048576}

var (
	failed  int32
	gcSink  [][]byte
	curSeed int64
)

func ck(ok bool, msg string) {
	if !ok {
		atomic.AddInt32(&failed, 1)
		fmt.Println("FAIL:", msg)
	}
}

func ckSeed(ok bool, msg string) {
	if !ok {
		atomic.AddInt32(&failed, 1)
		fmt.Printf("FAIL: %s (seed=%d)\n", msg, curSeed)
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
		fmt.Printf("FAILED %s (%d failed checks)\n", name, n)
		os.Exit(1)
	}
	fmt.Println("ALL PASS " + name)
}

func envSeed() int64 {
	s := os.Getenv("UAF_SEED")
	if s == "" {
		return 1
	}
	v, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 1
	}
	return v
}

func tortureRounds(def int) int {
	s := os.Getenv("TORTURE_ROUNDS")
	if s == "" {
		return def
	}
	n, err := strconv.Atoi(s)
	if err != nil || n <= 0 {
		return def
	}
	if n > 20 {
		n = 20
	}
	return n
}

type bytePool struct {
	pools  [len(sizeClasses)]sync.Pool
	gets   int
	reuses int
	allocs int
	puts   int
	drops  int
}

func classFor(n int) int {
	for i, c := range sizeClasses {
		if n <= c {
			return i
		}
	}
	return -1
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func (bp *bytePool) get(n int) *pooledBuf {
	bp.gets++
	if n <= 0 {
		n = 1
	}
	c := classFor(n)
	if c < 0 {
		bp.allocs++
		return &pooledBuf{b: make([]byte, n), class: -1}
	}
	if v := bp.pools[c].Get(); v != nil {
		pb := v.(*pooledBuf)
		bp.reuses++
		ck(pb.class == c, "bytepool: pooled class mismatch")
		if cap(pb.b) < sizeClasses[c] {
			ck(false, "bytepool: pooled buffer capacity too small")
			pb.b = make([]byte, sizeClasses[c])
		}
		pb.b = pb.b[:sizeClasses[c]]
		for i := 0; i < minInt(64, sizeClasses[c]); i++ {
			if pb.b[i] != 0 {
				ck(false, "bytepool: pooled buffer not zeroed")
				break
			}
		}
		pb.b = pb.b[:n]
		return pb
	}
	bp.allocs++
	return &pooledBuf{b: make([]byte, n, sizeClasses[c]), class: c}
}

func (bp *bytePool) put(pb *pooledBuf) {
	bp.puts++
	if pb == nil {
		bp.drops++
		return
	}
	if pb.class < 0 {
		bp.drops++
		return
	}
	if pb.class >= len(sizeClasses) || cap(pb.b) != sizeClasses[pb.class] {
		bp.drops++
		return
	}
	full := pb.b[:sizeClasses[pb.class]]
	for i := range full {
		full[i] = 0
	}
	pb.b = full
	bp.pools[pb.class].Put(pb)
}

func mark(p []byte, tag byte) byte {
	var s byte
	for i := range p {
		p[i] = byte(int(tag)*31 + i*7)
		s = s*131 + p[i]
	}
	return s
}

func checkMark(p []byte, tag byte, sum byte) bool {
	h := byte(0)
	for i := range p {
		if p[i] != byte(int(tag)*31+i*7) {
			return false
		}
		h = h*131 + p[i]
	}
	return h == sum
}

func main() {
	curSeed = envSeed()
	rounds := tortureRounds(3)

	for round := 0; round < rounds; round++ {
		r := rand.New(rand.NewSource(curSeed + int64(round)*7919))
		var bp bytePool
		const live = 48
		bufs := make([]*pooledBuf, live)
		sums := make([]byte, live)
		tags := make([]byte, live)
		sizes := make([]int, live)

		// 1) 租用混合 size class 缓冲并写满模式。
		for i := 0; i < live; i++ {
			c := r.Intn(len(sizeClasses))
			n := sizeClasses[c] - r.Intn(minInt(sizeClasses[c]-1, 200))
			pb := bp.get(n)
			ck(len(pb.b) == n, "bytepool: Get length")
			ck(cap(pb.b) >= n, "bytepool: Get capacity")
			for j := 0; j < minInt(64, n); j++ {
				if pb.b[j] != 0 {
					ck(false, "bytepool: dirty byte on Get")
					break
				}
			}
			tag := byte(i*13 + round*7 + 1)
			tags[i] = tag
			sizes[i] = n
			sums[i] = mark(pb.b, tag)
			bufs[i] = pb
			if i%17 == 16 {
				gcStress()
			}
		}
		// 存活缓冲跨 GC 内容不变。
		gcStress()
		for i := 0; i < live; i++ {
			pb := bufs[i]
			ckSeed(pb != nil && len(pb.b) == sizes[i] && cap(pb.b) >= sizes[i], fmt.Sprintf("bytepool: rented buffer shape changed i=%d", i))
			ckSeed(checkMark(pb.b, tags[i], sums[i]), fmt.Sprintf("bytepool: rented buffer corrupted after GC i=%d", i))
		}
		// 归还一半（清零），保留一半。
		for i := 0; i < live; i += 2 {
			bp.put(bufs[i])
			bufs[i] = nil
		}
		gcStress()
		for i := 1; i < live; i += 2 {
			ckSeed(checkMark(bufs[i].b, tags[i], sums[i]), fmt.Sprintf("bytepool: retained buffer corrupted after Put+GC i=%d", i))
		}

		// 2) 再租一批：池中返回的缓冲必须已清零；写新模式。
		refill := make([]*pooledBuf, 24)
		refillSums := make([]byte, 24)
		refillTags := make([]byte, 24)
		for i := range refill {
			c := r.Intn(len(sizeClasses))
			n := sizeClasses[c] - r.Intn(minInt(sizeClasses[c]-1, 300))
			pb := bp.get(n)
			refill[i] = pb
			for j := 0; j < minInt(64, n); j++ {
				if pb.b[j] != 0 {
					ckSeed(false, fmt.Sprintf("bytepool: reused buffer leaked data i=%d", i))
					break
				}
			}
			refillTags[i] = byte(100 + i)
			refillSums[i] = mark(pb.b, refillTags[i])
		}
		gcStress()
		for i := range refill {
			ckSeed(checkMark(refill[i].b, refillTags[i], refillSums[i]), fmt.Sprintf("bytepool: refill corrupted after GC i=%d", i))
			bp.put(refill[i])
		}

		// 3) 小类高 churn：反复 Get/Put，穿插 GC；每次 Get 内容从零开始。
		for i := 0; i < 240; i++ {
			pb := bp.get(4000 + i%97)
			for j := 0; j < 64; j++ {
				if pb.b[j] != 0 {
					ckSeed(false, fmt.Sprintf("bytepool: churn dirty buffer op=%d", i))
					break
				}
			}
			s := mark(pb.b, byte(i%251))
			ckSeed(checkMark(pb.b, byte(i%251), s), fmt.Sprintf("bytepool: churn mark mismatch op=%d", i))
			bp.put(pb)
			if i%40 == 39 {
				gcStress()
			}
		}

		// 4) 超最大 class 直接精确分配；错误尺寸/直接缓冲归还全部丢弃。
		bigN := 2*1024*1024 + 123
		big := bp.get(bigN)
		ck(big.class == -1 && len(big.b) == bigN && cap(big.b) >= bigN, "bytepool: direct large allocation")
		bs := mark(big.b, 0x5A)
		gcStress()
		ckSeed(checkMark(big.b, 0x5A, bs), "bytepool: direct large buffer corrupted after GC")
		beforeDrops := bp.drops
		bp.put(big)
		bp.put(&pooledBuf{b: make([]byte, 100), class: 0})
		bp.put(nil)
		ck(bp.drops == beforeDrops+3, "bytepool: wrong-size/nil/direct puts not dropped")

		ck(bp.gets == bp.allocs+bp.reuses, "bytepool: get accounting")
		if intra := atomic.LoadInt32(&failed); intra != 0 {
			break
		}
		runtime.KeepAlive(bufs)
		runtime.KeepAlive(refill)
		runtime.KeepAlive(big)
	}

	finish("l13_byte_pool_reuse")
}
