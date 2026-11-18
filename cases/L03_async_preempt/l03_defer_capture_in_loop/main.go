// L03: 循环内登记 defer 闭包。每批在 noinline 函数的 16 轮循环里登记
// 捕获 per-iteration 变量与堆节点指针的 defer；登记后原地修改节点，每 8 批
// 在 defer 记录尚未执行时显式 GC，函数返回时闭包按 LIFO 读出被修改后的值。
// 校验执行顺序、逐迭代捕获语义和指针跨 GC 存活；另有并发 GC pump。
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
	dcFailed int32
)

const (
	dcBatches  = 2048
	dcPerBatch = 16
	dcGCDiv    = 8
)

type dcNode struct {
	v   int64
	tag string
}

func ck(ok bool, msg string) {
	if !ok {
		atomic.AddInt32(&dcFailed, 1)
		fmt.Println("FAIL:", msg)
	}
}

func finish(name string) {
	if n := atomic.LoadInt32(&dcFailed); n != 0 {
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

//go:noinline
func dcBatch(round, seed int64, order *[]int, vals *[]int64) (gcs int64) {
	nodes := make([]*dcNode, dcPerBatch)
	base := round*1000 + seed%97
	for i := 0; i < dcPerBatch; i++ {
		i := i
		nd := &dcNode{v: base + int64(i)*7, tag: "dc"}
		nodes[i] = nd
		defer func() {
			// LIFO 执行时读取 per-iteration i 与已被原地修改的节点。
			*order = append(*order, i)
			*vals = append(*vals, nd.v*100+int64(i))
		}()
	}
	for i := range nodes {
		nodes[i].v += 3
	}
	if round%dcGCDiv == 0 {
		junk := make([]byte, 16384)
		junk[0] = byte(round)
		runtime.GC()
		runtime.KeepAlive(junk)
		gcs = 1
	}
	// 函数返回时 defer 闭包按 LIFO 执行。
	return gcs
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

	order := make([]int, 0, dcPerBatch)
	vals := make([]int64, 0, dcPerBatch)
	var badLen, badOrder, badVal, gcCount int64
	for r := 0; r < dcBatches; r++ {
		order = order[:0]
		vals = vals[:0]
		gcCount += dcBatch(int64(r), seed, &order, &vals)
		if len(order) != dcPerBatch || len(vals) != dcPerBatch {
			badLen++
			continue
		}
		base := int64(r)*1000 + seed%97 + 3
		for k := 0; k < dcPerBatch; k++ {
			wantI := dcPerBatch - 1 - k
			if order[k] != wantI {
				badOrder++
			}
			wantV := (base+int64(wantI)*7)*100 + int64(wantI)
			if vals[k] != wantV {
				badVal++
			}
		}
	}
	for epoch.Load() < 1 {
		runtime.Gosched()
	}
	now := epoch.Load()
	pumpWG.Wait()

	ck(now >= 1, fmt.Sprintf("GC pump completed during defer loop (seed=%d)", seed))
	ck(gcCount == (dcBatches+dcGCDiv-1)/dcGCDiv,
		fmt.Sprintf("GC with pending defer records (seed=%d)", seed))
	ck(badLen == 0, fmt.Sprintf("deferred closures all executed (seed=%d)", seed))
	ck(badOrder == 0, fmt.Sprintf("defer LIFO and per-iteration capture (seed=%d)", seed))
	ck(badVal == 0, fmt.Sprintf("captured node mutation visible after GC (seed=%d)", seed))
	finish("l03_defer_capture_in_loop")
}
