// L03: 每 N 轮 GC。主 goroutine 在无函数调用的持针循环里推进轮次计数；
// 控制 goroutine 观察计数，每当累计到 N 的整数倍就分配垃圾并 runtime.GC()，
// 恰好完成 want 次。主循环只有回边安全点，GC 可能在任意一轮之间打断它。
// 默认 262144 轮 / 每 16384 轮 GC；TORTURE_ROUNDS 可放大总轮数。
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

var failed int32

const (
	geSlotsDefault = 1 << 20
	geEvery        = 16384
	geSlots        = 2048
	geInner        = 8
)

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

func tortureRounds(def int) int {
	if s := os.Getenv("TORTURE_ROUNDS"); s != "" {
		if v, err := strconv.Atoi(s); err == nil && v > 0 {
			return v
		}
	}
	return def
}

func main() {
	seed := seedFromEnv()
	if seed < 0 {
		seed = -seed
	}
	rng := rand.New(rand.NewSource(seed))
	step := int64(2*rng.Intn(1024) + 1)
	off := int64(rng.Intn(geSlots))
	total := tortureRounds(geSlotsDefault)
	every := geEvery
	if total < every {
		every = total
	}
	want := total / every
	if want < 1 {
		want = 1
	}

	ring := make([]int64, geSlots)
	for i := range ring {
		ring[i] = int64(i)*17 + 3
	}

	var rounds atomic.Int64
	var gcs atomic.Int64
	var controllerWG sync.WaitGroup
	start := make(chan struct{})
	controllerWG.Add(1)
	go func() {
		defer controllerWG.Done()
		<-start
		target := int64(every)
		for {
			for rounds.Load() < target {
				runtime.Gosched()
			}
			junk := make([]byte, 16384)
			for i := 0; i < len(junk); i += 512 {
				junk[i] = byte(target + int64(i))
			}
			runtime.GC()
			end := gcs.Add(1)
			runtime.KeepAlive(junk)
			if end >= int64(want) {
				return
			}
			target += int64(every)
		}
	}()
	close(start)

	limit := int64(total) * 4
	var sum int64
	done := int64(0)
	for (done < int64(total) || gcs.Load() < int64(want)) && done < limit {
		idx := int((done*step + off) % geSlots)
		q := &ring[idx]
		var local int64
		for k := 0; k < geInner; k++ {
			local += *q + int64(k)
		}
		sum += local
		done++
		rounds.Store(done)
	}
	controllerWG.Wait()

	var expected int64
	for i := int64(0); i < done; i++ {
		idx := int((i*step + off) % geSlots)
		for k := 0; k < geInner; k++ {
			expected += ring[idx] + int64(k)
		}
	}

	ck(gcs.Load() == int64(want), fmt.Sprintf("GC fired exactly every N rounds (seed=%d)", seed))
	ck(done >= int64(total), fmt.Sprintf("loop reached target rounds (seed=%d)", seed))
	ck(done <= limit, fmt.Sprintf("loop completed before bounded limit (seed=%d)", seed))
	ck(sum == expected, fmt.Sprintf("gc-every-n checksum after GC (seed=%d)", seed))
	corrupt := false
	for i := range ring {
		if ring[i] != int64(i)*17+3 {
			corrupt = true
		}
	}
	ck(!corrupt, fmt.Sprintf("ring intact after %d GCs (seed=%d)", want, seed))
	runtime.KeepAlive(ring)
	finish("l03_gc_every_n_rounds")
}
