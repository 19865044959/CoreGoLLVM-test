// L03: 32 分支字符串 switch 紧循环。每轮按轮次和种子选一个 switch 分支，
// 分支只做堆节点指针偏移选择，不调用函数；循环中周期性显式 GC，另有 GC
// pump 并发。校验分支偏移和、命中计数与节点内容。
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
	bsKeep [][]byte
)

const (
	bsSlots = 64
	bsCases = 32
	bsMin   = 1 << 17
	bsCap   = 1 << 21
	bsGCDiv = 1 << 15
)

type bsNode struct {
	v   int64
	w   int64
	tag string
}

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

func main() {
	seed := seedFromEnv()
	if seed < 0 {
		seed = -seed
	}
	rng := rand.New(rand.NewSource(seed))
	step := int64(2*rng.Intn(31) + 1)
	off := int64(rng.Intn(bsSlots))
	step2 := int64(2*rng.Intn(63) + 1)
	cOff := int64(rng.Intn(bsCases))

	var keys [bsCases]string
	for i := range keys {
		keys[i] = fmt.Sprintf("op%02d", i)
	}

	backing := make([]bsNode, bsSlots)
	for i := range backing {
		backing[i].v = int64(i)*3 + 1
		backing[i].w = int64(i)*5 + 2
		backing[i].tag = "bs"
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
			bsKeep = junk
			runtime.GC()
			epoch.Add(1)
		}
		bsKeep = nil
	}()
	close(start)

	var sum int64
	var done int64
	var badSwitch, mainGC int64
	for (epoch.Load() < 1 || done < bsMin) && done < bsCap {
		idx := int((done*step + off) % bsSlots)
		p := &backing[idx]
		key := keys[int((done*step2+cOff)%bsCases)]
		var c int
		switch key {
		case "op00":
			c = 0
		case "op01":
			c = 1
		case "op02":
			c = 2
		case "op03":
			c = 3
		case "op04":
			c = 4
		case "op05":
			c = 5
		case "op06":
			c = 6
		case "op07":
			c = 7
		case "op08":
			c = 8
		case "op09":
			c = 9
		case "op10":
			c = 10
		case "op11":
			c = 11
		case "op12":
			c = 12
		case "op13":
			c = 13
		case "op14":
			c = 14
		case "op15":
			c = 15
		case "op16":
			c = 16
		case "op17":
			c = 17
		case "op18":
			c = 18
		case "op19":
			c = 19
		case "op20":
			c = 20
		case "op21":
			c = 21
		case "op22":
			c = 22
		case "op23":
			c = 23
		case "op24":
			c = 24
		case "op25":
			c = 25
		case "op26":
			c = 26
		case "op27":
			c = 27
		case "op28":
			c = 28
		case "op29":
			c = 29
		case "op30":
			c = 30
		case "op31":
			c = 31
		default:
			c = -1
			badSwitch++
		}
		sum += p.v + backing[(idx+c)&(bsSlots-1)].w
		if done%bsGCDiv == 0 {
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
		idx := int((i*step + off) % bsSlots)
		c := int((i*step2 + cOff) % bsCases)
		expected += backing[idx].v + backing[(idx+c)&(bsSlots-1)].w
	}

	ck(now >= 1, fmt.Sprintf("GC pump completed during big-switch loop (seed=%d)", seed))
	ck(done >= bsMin, fmt.Sprintf("big switch loop executed (seed=%d)", seed))
	ck(badSwitch == 0, fmt.Sprintf("all switch cases hit labeled branches (seed=%d)", seed))
	ck(sum == expected, fmt.Sprintf("big-switch pointer checksum (seed=%d)", seed))
	ck(mainGC == (done-1)/bsGCDiv+1, fmt.Sprintf("main explicit GC inside loop (seed=%d)", seed))

	corrupt := false
	for i := range backing {
		if backing[i].v != int64(i)*3+1 || backing[i].w != int64(i)*5+2 || backing[i].tag != "bs" {
			corrupt = true
		}
	}
	ck(!corrupt, fmt.Sprintf("switch-loop nodes intact after GC (seed=%d)", seed))
	runtime.KeepAlive(backing)
	runtime.KeepAlive(keys)
	runtime.KeepAlive(bsKeep)
	finish("l03_big_switch_loop")
}
