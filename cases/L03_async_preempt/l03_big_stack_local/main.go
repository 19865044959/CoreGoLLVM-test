// L03: 循环每轮调用 noinline 大栈帧函数（16KiB 局部数组 + noinline 消费者），
// 每 16 轮在浅栈上显式 GC 使栈收缩，下一轮重新增长；GC pump 并发打断调用链。
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
	failed  int32
	gcKeep  [][]byte
	bigKeep []byte
)

const (
	bsSlots  = 64
	bsRounds = 512
	bsGCDiv  = 16
)

type bsNode struct {
	v    int64
	name string
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

func tortureRounds(def int) int {
	if s := os.Getenv("TORTURE_ROUNDS"); s != "" {
		if v, err := strconv.Atoi(s); err == nil && v > 0 {
			return v
		}
	}
	return def
}

//go:noinline
func consumeBig(buf *[2048]int64) int64 {
	var s int64
	for i := range buf {
		s += buf[i]
	}
	return s
}

//go:noinline
func bigStackFrame(round int, salt int64, p *bsNode) int64 {
	var buf [2048]int64
	base := p.v + int64(round) + salt
	for i := range buf {
		buf[i] = base + int64(i)
	}
	return consumeBig(&buf) + buf[int(base)&2047]
}

func bsIdxAt(i, step, off int64) int {
	return int((i*step + off) % bsSlots)
}

func bsExpected(i int64, idx int, salt int64) int64 {
	base := int64(idx)*3 + 5 + i + salt
	sum := int64(2048)*base + int64(2048*2047/2)
	return sum + base + (base & 2047)
}

func main() {
	seed := seedFromEnv()
	if seed < 0 {
		seed = -seed
	}
	rng := rand.New(rand.NewSource(seed))
	step := int64(2*rng.Intn(16) + 1)
	off := int64(rng.Intn(bsSlots))
	salt := seed % 97
	rounds := tortureRounds(bsRounds)

	backing := make([]bsNode, bsSlots)
	for i := range backing {
		backing[i].v = int64(i)*3 + 5
		backing[i].name = "b"
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
			gcKeep = junk
			runtime.GC()
			epoch.Add(1)
		}
		gcKeep = nil
	}()
	close(start)

	var sum int64
	var done int64
	limit := int64(rounds) * 4
	for (epoch.Load() < 1 || done < int64(rounds)) && done < limit {
		idx := bsIdxAt(done, step, off)
		p := &backing[idx]
		sum += bigStackFrame(int(done), salt, p)
		if done%bsGCDiv == 0 {
			bigKeep = make([]byte, 16384)
			bigKeep[0] = byte(done)
			runtime.GC()
		}
		done++
	}
	now := epoch.Load()
	wg.Wait()

	var expected int64
	for i := int64(0); i < done; i++ {
		expected += bsExpected(i, bsIdxAt(i, step, off), salt)
	}

	ck(now >= 1, fmt.Sprintf("GC pump completed during big-stack loop (seed=%d)", seed))
	ck(done >= int64(rounds), fmt.Sprintf("big-stack rounds executed (seed=%d)", seed))
	ck(sum == expected, fmt.Sprintf("big-stack checksum (seed=%d)", seed))

	corrupt := false
	for i := range backing {
		if backing[i].v != int64(i)*3+5 || backing[i].name != "b" {
			corrupt = true
		}
	}
	ck(!corrupt, fmt.Sprintf("big-stack nodes intact after GC (seed=%d)", seed))
	runtime.KeepAlive(backing)
	runtime.KeepAlive(bigKeep)
	finish("l03_big_stack_local")
}
