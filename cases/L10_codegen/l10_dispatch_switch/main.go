// L10: 生成式 dispatch switch：1024 个 opcode 的大 switch、payload 指针访问、
// 默认分支拒绝越界、GC 后分发结果稳定、TORTURE_ROUNDS 放大全量扫描。
package main

import (
	"fmt"
	"math/rand"
	"os"
	"runtime"
	"strconv"
	"sync/atomic"
)

const (
	dispOps     = 1024
	dispDataLen = 4
)

type dispPayload struct {
	Base int64
	Data []int64
}

var (
	failed  int32
	gcSink  [][]byte
	curSeed int64
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

func envRounds() int {
	n := 1
	if s := os.Getenv("TORTURE_ROUNDS"); s != "" {
		if v, err := strconv.Atoi(s); err == nil && v > 0 {
			n = v
		}
	}
	if n > 200 {
		n = 200
	}
	return n
}

// expected 是测试侧独立公式，用来核对生成 switch 的每个常量分支。
func expected(p *dispPayload, op int, b int64) int64 {
	return p.Base*int64(op%17+1) + b + p.Data[op%dispDataLen] + int64(op)
}

func newPayload(r *rand.Rand) *dispPayload {
	p := &dispPayload{Base: r.Int63n(2001) - 1000, Data: make([]int64, dispDataLen+r.Intn(4))}
	for i := range p.Data {
		p.Data[i] = r.Int63n(2001) - 1000
	}
	return p
}

func main() {
	curSeed = 1
	if s := os.Getenv("UAF_SEED"); s != "" {
		if v, err := strconv.ParseInt(s, 10, 64); err == nil {
			curSeed = v
		}
	}
	tRounds := envRounds()

	// 1. 固定 payload 全量扫描：每个 opcode 的返回值与独立公式一致。
	p := &dispPayload{Base: 7, Data: []int64{1, -2, 3, -4}}
	var sum, wantSum int64
	for op := 0; op < dispOps; op++ {
		got, ok := dispatch(uint16(op), p, 11)
		if !ok || got != expected(p, op, 11) {
			ck(false, "fixed dispatch op")
			break
		}
		sum ^= got * int64(op+1)
		wantSum ^= expected(p, op, 11) * int64(op+1)
	}
	ck(sum == wantSum, "fixed dispatch checksum")

	// 2. 负值/边界 payload。
	neg := &dispPayload{Base: -13, Data: []int64{-1, 0, 1, -1 << 20}}
	for _, op := range []int{0, 1, 2, 3, 16, 17, 511, 1023} {
		got, ok := dispatch(uint16(op), neg, -9)
		ck(ok && got == expected(neg, op, -9), "negative payload dispatch")
	}

	// 3. nil/短 payload/越界 opcode 走默认分支。
	if _, ok := dispatch(0, nil, 1); ok {
		ck(false, "nil payload rejected")
	}
	if _, ok := dispatch(0, &dispPayload{Base: 1, Data: []int64{1, 2}}, 1); ok {
		ck(false, "short payload rejected")
	}
	for _, op := range []int{dispOps, dispOps + 1, 5000, 65535} {
		if _, ok := dispatch(uint16(op), p, 1); ok {
			ck(false, "out-of-range opcode rejected")
		}
	}

	// 4. GC 后保留 payload 继续分发，公式不变。
	payloads := make([]*dispPayload, 16)
	r := rand.New(rand.NewSource(curSeed))
	for i := range payloads {
		payloads[i] = newPayload(r)
	}
	gcSink = append(gcSink, []byte("dispatch"))
	gcStress()
	for i, pl := range payloads {
		for op := 0; op < dispOps; op++ {
			got, ok := dispatch(uint16(op), pl, int64(i)-8)
			if !ok || got != expected(pl, op, int64(i)-8) {
				ckSeed(false, fmt.Sprintf("dispatch after GC payload %d op %d", i, op))
				break
			}
		}
	}

	// 5. UAF_SEED 随机 rounds × 全量 opcode；TORTURE_ROUNDS 放大。
	rounds := 24 * tRounds
	for round := 0; round < rounds; round++ {
		pl := newPayload(r)
		b := r.Int63n(2001) - 1000
		start := r.Intn(dispOps)
		var h, wh uint64
		bad := false
		for k := 0; k < dispOps; k++ {
			op := (start + k) % dispOps
			got, ok := dispatch(uint16(op), pl, b)
			want := expected(pl, op, b)
			if !ok || got != want {
				ckSeed(false, fmt.Sprintf("random dispatch round %d op %d", round, op))
				bad = true
				break
			}
			h = h*1099511628211 + uint64(got)
			wh = wh*1099511628211 + uint64(want)
		}
		if !bad {
			ckSeed(h == wh, "random dispatch checksum")
		}
		if round%8 == 0 {
			gcStress()
			got, ok := dispatch(uint16(round%dispOps), pl, b)
			ckSeed(ok && got == expected(pl, round%dispOps, b), "random dispatch after GC")
		}
	}

	// 6. 全量扫完再次确认总数与默认分支。
	valid := 0
	for op := 0; op < 65536; op++ {
		if _, ok := dispatch(uint16(op), p, 0); ok {
			valid++
		}
	}
	ck(valid == dispOps, "exactly 1024 valid opcodes")

	finish("l10_dispatch_switch")
}
