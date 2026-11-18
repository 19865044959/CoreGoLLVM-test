// L03: 循环内逐轮 panic/recover。每轮在 noinline 函数深处 panic 三种载荷之一
// （堆节点指针、字符串、含指针的结构体值），函数内 defer/recover 取回；
// 每 64 轮在 recover 之后、校验之前显式 GC，使 recover 出的接口载荷跨 GC
// 存活并被再次解引用。另有并发 GC pump。
package main

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"sync"
	"sync/atomic"
)

var (
	pnFailed int32
)

const (
	pnRounds = 4096
	pnGCDiv  = 64
)

type pnNode struct {
	v   int64
	tag string
}

type pnErr struct {
	code int64
	p    *pnNode
}

func ck(ok bool, msg string) {
	if !ok {
		atomic.AddInt32(&pnFailed, 1)
		fmt.Println("FAIL:", msg)
	}
}

func finish(name string) {
	if n := atomic.LoadInt32(&pnFailed); n != 0 {
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

// pnPanicIter 每轮 panic 并仅通过 recover 返回值持有载荷。
//
//go:noinline
func pnPanicIter(r int64) (rec any) {
	defer func() {
		rec = recover()
	}()
	switch r % 3 {
	case 0:
		panic(&pnNode{v: r*5 + 7, tag: "pn"})
	case 1:
		panic(fmt.Sprintf("pn-%d", r&255))
	default:
		panic(pnErr{code: r & 7, p: &pnNode{v: r*9 + 3, tag: "pn"}})
	}
	return nil
}

func pnValidate(rec any, r int64) (kind int, sum int64, ok bool) {
	switch v := rec.(type) {
	case *pnNode:
		if v == nil || v.v != r*5+7 || v.tag != "pn" {
			return 1, 0, false
		}
		return 1, v.v*2 + r, true
	case string:
		if v != fmt.Sprintf("pn-%d", r&255) {
			return 2, 0, false
		}
		return 2, int64(len(v))*3 + r, true
	case pnErr:
		if v.p == nil || v.code != r&7 || v.p.v != r*9+3 || v.p.tag != "pn" {
			return 3, 0, false
		}
		return 3, v.code*100 + v.p.v, true
	default:
		return 0, 0, false
	}
}

func main() {
	seed := seedFromEnv()
	if seed < 0 {
		seed = -seed
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

	var bad, gcCount int64
	var kindCount, kindExp [4]int64
	var kindSum, kindSumExp [4]int64
	for r := int64(0); r < pnRounds; r++ {
		rec := pnPanicIter(r)
		if r%pnGCDiv == 0 {
			junk := make([]byte, 16384)
			junk[0] = byte(r)
			runtime.GC()
			gcCount++
			runtime.KeepAlive(junk)
		}
		kind, sum, ok := pnValidate(rec, r)
		if !ok {
			bad++
		} else {
			kindCount[kind]++
			kindSum[kind] += sum
		}
		switch r % 3 {
		case 0:
			kindExp[1]++
			kindSumExp[1] += (r*5+7)*2 + r
		case 1:
			kindExp[2]++
			kindSumExp[2] += int64(len(fmt.Sprintf("pn-%d", r&255)))*3 + r
		default:
			kindExp[3]++
			kindSumExp[3] += (r&7)*100 + (r*9 + 3)
		}
		rec = nil
	}
	for epoch.Load() < 1 {
		runtime.Gosched()
	}
	now := epoch.Load()
	pumpWG.Wait()

	ck(now >= 1, fmt.Sprintf("GC pump completed during panic loop (seed=%d)", seed))
	ck(gcCount == pnRounds/pnGCDiv, fmt.Sprintf("explicit GC count (seed=%d)", seed))
	ck(bad == 0, fmt.Sprintf("recover payload valid after GC (seed=%d)", seed))
	var countsOK, sumsOK = true, true
	for k := 1; k <= 3; k++ {
		if kindCount[k] != kindExp[k] {
			countsOK = false
		}
		if kindSum[k] != kindSumExp[k] {
			sumsOK = false
		}
	}
	ck(countsOK, fmt.Sprintf("panic payload kind counts (seed=%d)", seed))
	ck(sumsOK, fmt.Sprintf("recovered payload checksums (seed=%d)", seed))
	ck(kindCount[1]+kindCount[2]+kindCount[3] == pnRounds,
		fmt.Sprintf("all rounds recovered (seed=%d)", seed))
	finish("l03_panic_recover_iteration")
}
