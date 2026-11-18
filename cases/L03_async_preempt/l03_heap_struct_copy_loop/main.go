// L03: 循环内大对象按值复制。每轮在 noinline 函数里执行 `*dst = *src`，
// 把一个约 64KiB、含指针与字符串头的堆结构体整体 typedmemmove 到另一个
// 堆对象；复制后立即清掉 src 侧的新指针，使 dst 成为唯一引用，随后每 8 轮
// 显式 GC 并解引用 dst.p/dst.tag。并发 GC pump 加剧写屏障压力。
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
	hcFailed int32
)

const (
	hcArr    = 8192
	hcGCDiv  = 8
	hcRounds = 512
)

type hcNode struct {
	v   int64
	tag string
}

type hcBig struct {
	arr [hcArr]int64
	p   *hcNode
	tag string
	id  int64
}

func ck(ok bool, msg string) {
	if !ok {
		atomic.AddInt32(&hcFailed, 1)
		fmt.Println("FAIL:", msg)
	}
}

func finish(name string) {
	if n := atomic.LoadInt32(&hcFailed); n != 0 {
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
	if v > 1<<16 {
		v = 1 << 16
	}
	return v
}

// hcPrepare 让 dst 成为 fresh 节点与 fresh 字符串的唯一引用，并完成大对象拷贝。
//
//go:noinline
func hcPrepare(dst, src *hcBig, r int64) (wantNode int64) {
	wantNode = r*2 + 1
	src.p = &hcNode{v: wantNode, tag: "hc"}
	src.tag = fmt.Sprintf("hc-%d", r&255)
	src.id = r
	*dst = *src
	src.p = nil
	src.tag = ""
	return wantNode
}

func main() {
	seed := seedFromEnv()
	if seed < 0 {
		seed = -seed
	}
	rng := rand.New(rand.NewSource(seed))
	step := int64(2*rng.Intn(1023) + 1)
	off := int64(rng.Intn(hcArr))
	rounds := roundsFromEnv(hcRounds)

	src := &hcBig{}
	dst := new(hcBig)
	baseByte := seed % 11

	start := make(chan struct{})
	var epoch atomic.Int64
	var pumpWG sync.WaitGroup
	pumpWG.Add(1)
	go func() {
		defer pumpWG.Done()
		<-start
		for r := 0; r < 8; r++ {
			junk := make([][]byte, 4)
			for j := range junk {
				junk[j] = make([]byte, 16384)
				junk[j][0] = byte(r + j)
			}
			runtime.GC()
			runtime.KeepAlive(junk)
			epoch.Add(1)
		}
	}()
	close(start)

	var badPtr, badTag, badID, badArr, gcCount int64
	for r := int64(0); r < int64(rounds); r++ {
		for i := 0; i < hcArr; i++ {
			src.arr[i] = int64(i)*3 + baseByte
		}
		pos := (r*step + off) % hcArr
		src.arr[pos] = r*7 + 1
		if r%32 == 0 {
			// 周期性丢弃旧大对象，制造堆分配/回收。
			dst = new(hcBig)
		}
		wantNode := hcPrepare(dst, src, r)
		if r%hcGCDiv == 0 {
			junk := make([]byte, 32768)
			junk[0] = byte(r)
			runtime.GC()
			gcCount++
			runtime.KeepAlive(junk)
		}
		if dst.p == nil || dst.p.v != wantNode || dst.p.tag != "hc" {
			badPtr++
		}
		if dst.tag != fmt.Sprintf("hc-%d", r&255) {
			badTag++
		}
		if dst.id != r {
			badID++
		}
		if dst.arr[pos] != r*7+1 {
			badArr++
		}
		for _, j := range [5]int64{0, 101, 1000, 4096, 8000} {
			if j == pos {
				continue
			}
			if dst.arr[j] != j*3+baseByte {
				badArr++
			}
		}
	}
	for epoch.Load() < 1 {
		runtime.Gosched()
	}
	now := epoch.Load()
	pumpWG.Wait()

	ck(now >= 1, fmt.Sprintf("GC pump completed during large copy loop (seed=%d)", seed))
	ck(gcCount == int64((rounds+hcGCDiv-1)/hcGCDiv),
		fmt.Sprintf("explicit GC count (seed=%d)", seed))
	ck(badPtr == 0, fmt.Sprintf("copied pointer survives GC (seed=%d)", seed))
	ck(badTag == 0, fmt.Sprintf("copied string header survives GC (seed=%d)", seed))
	ck(badID == 0, fmt.Sprintf("copied scalar field intact (seed=%d)", seed))
	ck(badArr == 0, fmt.Sprintf("large array copy checksum (seed=%d)", seed))
	runtime.KeepAlive(src)
	runtime.KeepAlive(dst)
	finish("l03_heap_struct_copy_loop")
}
