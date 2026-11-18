// L03: 大结构体按值拷贝长循环。每轮经 noinline 函数把含 [384]int64、指针和
// string 头的大结构体整体传入/传出，拷贝结果跨显式 GC 后继续解引用 p 字段。
// 每 8 轮显式 GC，另有 GC pump 并发；轮数可由 TORTURE_ROUNDS 放大。
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
	bcSlots   = 64
	bcField   = 384
	bcGCDiv   = 8
	bcDefault = 4096
)

type bcNode struct {
	v   int64
	tag string
}

type bcBig struct {
	a   [bcField]int64
	p   *bcNode
	tag string
	id  int64
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
func bcCopy(in bcBig) (out bcBig) {
	out = in
	out.a[0]++
	return
}

func main() {
	seed := seedFromEnv()
	if seed < 0 {
		seed = -seed
	}
	rng := rand.New(rand.NewSource(seed))
	step := int64(2*rng.Intn(32) + 1)
	off := int64(rng.Intn(bcSlots))
	rounds := tortureRounds(bcDefault)

	nodes := make([]bcNode, bcSlots)
	ring := make([]bcBig, bcSlots)
	for i := range nodes {
		nodes[i].v = int64(i)*97 + 13
		nodes[i].tag = "bcn"
	}
	for i := range ring {
		ring[i].id = int64(i)
		ring[i].p = &nodes[i]
		ring[i].tag = "big"
		for j := 0; j < bcField; j++ {
			ring[i].a[j] = int64(i*1000 + j)
		}
	}

	var epoch atomic.Int64
	var wg sync.WaitGroup
	start := make(chan struct{})
	wg.Add(1)
	go func() {
		defer wg.Done()
		<-start
		for r := 0; r < 8; r++ {
			junk := make([]byte, 16384)
			for i := 0; i < len(junk); i += 512 {
				junk[i] = byte(r + i)
			}
			runtime.GC()
			epoch.Add(1)
			runtime.KeepAlive(junk)
		}
	}()
	close(start)

	var sum, expected int64
	var bad, mainGC int
	var last bcBig
	var sink []byte

	for r := 0; r < rounds; r++ {
		idx := int((int64(r)*step + off) % bcSlots)
		src := &ring[idx]
		cp := bcCopy(*src)

		if r%bcGCDiv == 0 {
			sink = make([]byte, 16384)
			sink[0] = byte(r)
			runtime.GC()
			mainGC++
		}

		if cp.a[0] != src.a[0]+1 || cp.p != src.p || cp.tag != src.tag || cp.id != src.id {
			bad++
		}
		if cp.p.v != int64(idx)*97+13 || cp.p.tag != "bcn" {
			bad++
		}
		pos := int((int64(r)*step + off) % bcField)
		wantField := int64(idx*1000 + pos)
		if pos == 0 {
			wantField++
		}
		if cp.a[pos] != wantField {
			bad++
		}
		sum += cp.a[0] + cp.p.v + int64(len(cp.tag)) + cp.id
		expected += src.a[0] + 1 + src.p.v + int64(len(src.tag)) + src.id
		last = cp
	}
	for epoch.Load() < 1 {
		runtime.Gosched()
	}
	now := epoch.Load()
	wg.Wait()

	ck(now >= 1, fmt.Sprintf("concurrent GC ran during big-struct copy loop (seed=%d)", seed))
	ck(bad == 0, fmt.Sprintf("copied struct fields and pointer (seed=%d)", seed))
	ck(sum == expected, fmt.Sprintf("big-struct copy checksum after GC (seed=%d)", seed))
	ck(mainGC == (rounds+bcGCDiv-1)/bcGCDiv, fmt.Sprintf("explicit GC every %d rounds (seed=%d)", bcGCDiv, seed))
	lastIdx := int((int64(rounds-1)*step + off) % bcSlots)
	ck(last.p != nil && last.p == ring[lastIdx].p && last.p.v == int64(lastIdx)*97+13,
		fmt.Sprintf("last copy keeps valid pointer after final GC (seed=%d)", seed))
	ck(last.tag == "big" && last.a[0] == ring[lastIdx].a[0]+1,
		fmt.Sprintf("last copy fields intact after final GC (seed=%d)", seed))

	corrupt := false
	for i := range ring {
		if ring[i].p != &nodes[i] || ring[i].id != int64(i) || ring[i].tag != "big" ||
			ring[i].a[0] != int64(i*1000) || ring[i].a[bcField-1] != int64(i*1000+bcField-1) {
			corrupt = true
		}
	}
	ck(!corrupt, fmt.Sprintf("copy ring intact after GC (seed=%d)", seed))
	runtime.KeepAlive(ring)
	runtime.KeepAlive(nodes)
	runtime.KeepAlive(last)
	runtime.KeepAlive(sink)
	finish("l03_big_struct_copy_loop")
}
