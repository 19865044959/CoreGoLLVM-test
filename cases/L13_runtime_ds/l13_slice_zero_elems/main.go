// L13: 零尺寸元素 slice 的 growslice/append/reslice/copy 边界，不分配元素存储但长度容量语义必须正确。
package main

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"sync/atomic"
)

var (
	failed   int32
	gcSink   [][]byte
	curSeed  int64
	zeroSink []struct{}
	zeroArr  [][0]byte
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

func envInt(name string, def int) int {
	s := os.Getenv(name)
	if s == "" {
		return def
	}
	v, err := strconv.Atoi(s)
	if err != nil || v <= 0 {
		return def
	}
	return v
}

func main() {
	curSeed = envSeed()
	rounds := envInt("TORTURE_ROUNDS", 2)

	for round := 0; round < rounds; round++ {
		n := 700000 + int((curSeed+int64(round)*17)%7)*50000

		// 逐个 append 空结构体。
		var zs []struct{}
		for i := 0; i < n; i++ {
			zs = append(zs, struct{}{})
			if i%131071 == 0 && len(zs) != i+1 {
				ckSeed(false, fmt.Sprintf("zero elem: len checkpoint at %d round %d", i, round))
				break
			}
		}
		zeroSink = zs
		ck(len(zeroSink) == n, "zero elem: append len")
		ck(cap(zeroSink) >= len(zeroSink), "zero elem: cap below len")

		// 块 append 到预分配容量。
		chunk := make([]struct{}, 4096)
		pre := make([]struct{}, 0, n/2)
		for len(pre)+len(chunk) <= n/2 {
			pre = append(pre, chunk...)
		}
		ck(len(pre) <= n/2, "zero elem: chunk len overshoot")
		ck(cap(pre) >= n/2, "zero elem: make cap not honored")

		// Reslice 链。
		mid := zs[n/3:]
		sub := mid[:(n/3)/2]
		ck(len(mid) == n-n/3, "zero elem: reslice len")
		ck(len(sub) == (n/3)/2, "zero elem: reslice sub len")

		// copy 只依赖长度。
		dst := make([]struct{}, n)
		if cnt := copy(dst, zs); cnt != n {
			ckSeed(false, fmt.Sprintf("zero elem: copy count %d round %d", cnt, round))
		}
		// 反向 copy，源短于目标。
		short := make([]struct{}, n/4)
		ck(copy(short, zs) == n/4, "zero elem: short copy count")

		// 零尺寸数组元素。
		var za [][0]byte
		for i := 0; i < n/2; i++ {
			za = append(za, [0]byte{})
		}
		zeroArr = za
		ck(len(zeroArr) == n/2, "zero elem: array append len")

		// 零尺寸 slice 自追加：长度不变。
		var self []struct{}
		for i := 0; i < 2000; i++ {
			self = append(self, self...)
		}
		ck(len(self) == 0, "zero elem: empty self append grew len")

		// 从已有元素自追加到空容量 slice。
		capz := make([]struct{}, 0, 1024)
		capz = append(capz, zs[:1024]...)
		ck(len(capz) == 1024, "zero elem: capped self append len")

		gcStress()
		ck(len(zeroSink) == n, "zero elem: len after GC")
		ck(len(zeroArr) == n/2, "zero elem: array len after GC")
		ck(len(dst) == n && len(sub) == (n/3)/2, "zero elem: derived slices after GC")
	}

	runtime.KeepAlive(zeroSink)
	runtime.KeepAlive(zeroArr)
	finish("l13_slice_zero_elems")
}
