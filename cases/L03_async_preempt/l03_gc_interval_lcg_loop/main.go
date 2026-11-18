// L03: 循环内按可变周期显式 GC。每轮解引用环上的节点并在无调用块中累加，
// 当轮次命中由 LCG 计划出的下一 GC 点时，调用 noinline gcTick 分配垃圾并
// runtime.GC()，随后继续解引用同一节点。GC 间隔在 8..200 轮之间按种子变化，
// 结束前用独立的 LCG 模拟核对 GC 次数与各次 GC 的垃圾校验和。
package main

import (
	"fmt"
	"math/rand"
	"os"
	"runtime"
	"strconv"
	"sync/atomic"
)

var (
	glFailed int32
)

const (
	glRounds = 32768
	glSlots  = 512
	glMinGap = 8
	glMaxGap = 200
)

type glNode struct {
	v   int64
	tag string
}

func ck(ok bool, msg string) {
	if !ok {
		atomic.AddInt32(&glFailed, 1)
		fmt.Println("FAIL:", msg)
	}
}

func finish(name string) {
	if n := atomic.LoadInt32(&glFailed); n != 0 {
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

func roundsFromEnv(def int) int {
	s := os.Getenv("TORTURE_ROUNDS")
	if s == "" {
		return def
	}
	v, err := strconv.Atoi(s)
	if err != nil || v < 1 {
		return def
	}
	if v > 1<<20 {
		v = 1 << 20
	}
	return v
}

func glInit(seed int64) uint64 {
	return uint64(seed)*2654435761 + 17
}

// glPlan 返回推进后的 LCG 状态与下一次 GC 的间隔。
func glPlan(state uint64) (uint64, int) {
	state = state*6364136223846793005 + 1442695040888963407
	return state, glMinGap + int((state>>33)%uint64(glMaxGap-glMinGap+1))
}

func glFirstGap(state uint64) int {
	return glMinGap + int((state>>40)%uint64(glMaxGap-glMinGap+1))
}

func glTickValue(r int64) int64 {
	var s int64
	for i := 0; i < 1024; i++ {
		s += int64(byte(r + int64(i)))
	}
	return s
}

//go:noinline
func glTick(r int64) int64 {
	junk := make([]byte, 8192)
	for i := 0; i < 1024; i++ {
		junk[i] = byte(r + int64(i))
	}
	runtime.GC()
	var s int64
	for i := 0; i < 1024; i++ {
		s += int64(junk[i])
	}
	runtime.KeepAlive(junk)
	return s
}

func main() {
	seed := seedFromEnv()
	if seed < 0 {
		seed = -seed
	}
	rng := rand.New(rand.NewSource(seed))
	step := int64(2*rng.Intn(1023) + 1)
	off := int64(rng.Intn(glSlots))
	rounds := roundsFromEnv(glRounds)

	backing := make([]glNode, glSlots)
	for i := range backing {
		backing[i].v = int64(i)*5 + 2
		backing[i].tag = "gl"
	}

	// 独立计划：与实际循环使用同一 LCG，计算 GC 次数与 gcTick 垃圾校验和。
	pState := glInit(seed)
	pNext := glFirstGap(pState)
	var planGCs, planTickSum int64
	for r := 0; r < rounds; r++ {
		if r == pNext {
			planGCs++
			planTickSum += glTickValue(int64(r))
			var gap int
			pState, gap = glPlan(pState)
			pNext = r + gap
		}
	}

	state := glInit(seed)
	nextGC := glFirstGap(state)
	var sum, expBase, gcCount, bad int64
	for r := 0; r < rounds; r++ {
		n := &backing[(int64(r)*step+off)%glSlots]
		acc := n.v
		for k := 0; k < 8; k++ {
			acc += n.v ^ int64(k)
		}
		sum += acc
		expBase += acc
		if r == nextGC {
			want := n.v
			tick := glTick(int64(r))
			sum += tick
			gcCount++
			var gap int
			state, gap = glPlan(state)
			nextGC = r + gap
			if n.v != want || n.tag != "gl" {
				bad++
			}
		}
	}

	var nodeBad int64
	for i := range backing {
		if backing[i].v != int64(i)*5+2 || backing[i].tag != "gl" {
			nodeBad++
		}
	}

	ck(planGCs > 0 || rounds < glMinGap, fmt.Sprintf("LCG plan scheduled GC points (seed=%d)", seed))
	ck(gcCount == planGCs, fmt.Sprintf("variable-period GC count (seed=%d)", seed))
	ck(sum == expBase+planTickSum, fmt.Sprintf("GC tick and loop checksum (seed=%d)", seed))
	ck(bad == 0, fmt.Sprintf("node readable after in-loop GC (seed=%d)", seed))
	ck(nodeBad == 0, fmt.Sprintf("ring nodes intact after loop (seed=%d)", seed))
	runtime.KeepAlive(backing)
	finish("l03_gc_interval_lcg_loop")
}
