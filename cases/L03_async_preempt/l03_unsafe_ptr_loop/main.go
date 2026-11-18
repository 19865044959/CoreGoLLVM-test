// L03: 长循环通过 unsafe.Pointer + unsafe.Add 逐次定位记录并读取字段，
// 每 4096 轮分配垃圾并显式 GC；GC pump 并发，验证 unsafe 基址在 GC 中始终有效。
package main

import (
	"fmt"
	"math/rand"
	"os"
	"runtime"
	"strconv"
	"sync"
	"sync/atomic"
	"unsafe"
)

var (
	failed int32
	gcKeep [][]byte
	uKeep  []byte
)

const (
	uSlots = 4096
	uMin   = 1 << 18
	uCap   = 1 << 24
	uGCDiv = 4096
)

type uRec struct {
	a uint64
	b uint64
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

func uIdxAt(i, step, off int64) int {
	return int((i*step + off) % uSlots)
}

func uExpected(i int64, idx int) int64 {
	return int64(uint64(idx)*3+1+uint64(idx)*5+2) + int64(idx)
}

func main() {
	seed := seedFromEnv()
	if seed < 0 {
		seed = -seed
	}
	rng := rand.New(rand.NewSource(seed))
	step := int64(2*rng.Intn(16) + 1)
	off := int64(rng.Intn(uSlots))

	recs := make([]uRec, uSlots)
	for i := range recs {
		recs[i].a = uint64(i)*3 + 1
		recs[i].b = uint64(i)*5 + 2
	}
	base := unsafe.Pointer(&recs[0])
	stride := unsafe.Sizeof(uRec{})

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
	for (epoch.Load() < 1 || done < uMin) && done < uCap {
		idx := uIdxAt(done, step, off)
		p := (*uRec)(unsafe.Add(base, uintptr(idx)*stride))
		sum += int64(p.a+p.b) + int64(idx)
		if done%uGCDiv == 0 {
			uKeep = make([]byte, 16384)
			uKeep[0] = byte(done)
			runtime.GC()
		}
		done++
	}
	now := epoch.Load()
	wg.Wait()

	var expected int64
	for i := int64(0); i < done; i++ {
		expected += uExpected(i, uIdxAt(i, step, off))
	}

	ck(now >= 1, fmt.Sprintf("GC pump completed during unsafe loop (seed=%d)", seed))
	ck(done >= uMin, fmt.Sprintf("unsafe loop executed (seed=%d)", seed))
	ck(sum == expected, fmt.Sprintf("unsafe pointer read checksum (seed=%d)", seed))

	corrupt := false
	for i := range recs {
		if recs[i].a != uint64(i)*3+1 || recs[i].b != uint64(i)*5+2 {
			corrupt = true
		}
	}
	ck(!corrupt, fmt.Sprintf("unsafe-read records intact after GC (seed=%d)", seed))
	runtime.KeepAlive(recs)
	runtime.KeepAlive(base)
	runtime.KeepAlive(uKeep)
	finish("l03_unsafe_ptr_loop")
}
