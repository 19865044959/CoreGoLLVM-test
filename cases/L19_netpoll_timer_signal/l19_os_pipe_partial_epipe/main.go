// L19: os.Pipe 写端取消的边界组合。写者一次 Write 4MiB 必然阻塞；读者只读取
// 固定前缀后关闭读端，阻塞写必须返回“部分已写字节 + 错误”，后续写继续失败，
// 已读前缀 pattern 必须完好。每轮在写者仍阻塞于 Write 时显式 GC，校验写者
// 栈/闭包上的指针链。TORTURE_ROUNDS 放大轮数。
package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"runtime"
	"strconv"
	"sync/atomic"
	"syscall"
	"time"
)

type node struct {
	v    int64
	next *node
}

var failed int32

const (
	cyclesPerRound = 8
	payloadLen     = 4 << 20
	prefixLen      = 12 << 10
	waitLimit      = 10 * time.Second
)

func ck(ok bool, msg string) {
	if !ok {
		atomic.AddInt32(&failed, 1)
		fmt.Println("FAIL:", msg)
	}
}

func ckf(ok bool, format string, args ...any) {
	if !ok {
		atomic.AddInt32(&failed, 1)
		fmt.Printf("FAIL: "+format+"\n", args...)
	}
}

func gcStress() {
	bufs := make([][]byte, 0, 16)
	for i := 0; i < 16; i++ {
		b := make([]byte, 4096)
		b[len(b)-1] = byte(i)
		bufs = append(bufs, b)
	}
	runtime.GC()
	runtime.GC()
	runtime.KeepAlive(bufs)
}

func finish(name string) {
	if n := atomic.LoadInt32(&failed); n != 0 {
		fmt.Printf("FAILED %s (%d failed checks)\n", name, n)
		os.Exit(1)
	}
	fmt.Println("ALL PASS " + name)
}

func envSeed() int64 {
	s := os.Getenv("UAF_SEED")
	if s == "" {
		return 1
	}
	v, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 1
	}
	return v
}

func envInt(name string, def, lo, hi int) int {
	s := os.Getenv(name)
	if s == "" {
		return def
	}
	n, err := strconv.Atoi(s)
	if err != nil || n < lo {
		return def
	}
	if n > hi {
		n = hi
	}
	return n
}

func makeChain(base int64) *node {
	return &node{v: base, next: &node{v: base + 1, next: &node{v: base + 2}}}
}

func chainOK(h *node, base int64) bool {
	if h == nil || h.v != base || h.next == nil || h.next.v != base+1 {
		return false
	}
	return h.next.next != nil && h.next.next.v == base+2 && h.next.next.next == nil
}

func patternByte(i int, seed int64, cycle int) byte {
	return byte((int64(i)*31 + seed*17 + int64(cycle)*7) & 0xff)
}

type writeResult struct {
	n       int
	err     error
	chainOK bool
}

func round(seed int64, roundIdx int) {
	for c := 0; c < cyclesPerRound; c++ {
		r, w, err := os.Pipe()
		if err != nil {
			ckf(false, "round %d cycle %d: os.Pipe: %v", roundIdx, c, err)
			return
		}

		payload := make([]byte, payloadLen)
		for i := range payload {
			payload[i] = patternByte(i, seed, c)
		}
		base := seed*59 + int64(roundIdx*100+c)*37 + 5
		chain := makeChain(base)
		started := make(chan struct{})
		res := make(chan writeResult, 1)
		go func() {
			close(started)
			n, werr := w.Write(payload)
			res <- writeResult{n: n, err: werr, chainOK: chainOK(chain, base)}
		}()
		<-started

		// 写者已经进入 Write 且 4MiB 远大于管道容量；此时 GC 扫描其阻塞栈。
		gcStress()

		got := make([]byte, prefixLen)
		if _, err := io.ReadFull(r, got); err != nil {
			ckf(false, "round %d cycle %d: read prefix: %v", roundIdx, c, err)
			r.Close()
			w.Close()
			select {
			case <-res:
			case <-time.After(waitLimit):
			}
			continue
		}
		bad := -1
		for i := range got {
			if got[i] != patternByte(i, seed, c) {
				bad = i
				break
			}
		}
		ckf(bad < 0, "round %d cycle %d: prefix pattern mismatch at %d", roundIdx, c, bad)

		// 只消费了前缀，payload 仍远超容量，写者必然仍阻塞或刚被唤醒。
		gcStress()
		if err := r.Close(); err != nil {
			ckf(false, "round %d cycle %d: close reader: %v", roundIdx, c, err)
		}

		var wr writeResult
		select {
		case wr = <-res:
		case <-time.After(waitLimit):
			ckf(false, "round %d cycle %d: blocked write not woken by reader close", roundIdx, c)
			w.Close()
			continue
		}
		ckf(wr.err != nil, "round %d cycle %d: blocked write returned nil error", roundIdx, c)
		ckf(wr.n >= prefixLen && wr.n < payloadLen, "round %d cycle %d: partial n=%d want [%d,%d)", roundIdx, c, wr.n, prefixLen, payloadLen)
		ckf(errors.Is(wr.err, syscall.EPIPE) || errors.Is(wr.err, os.ErrClosed),
			"round %d cycle %d: write err=%v, want EPIPE/ErrClosed", roundIdx, c, wr.err)
		ckf(wr.chainOK, "round %d cycle %d: writer payload chain corrupted", roundIdx, c)

		var one [1]byte
		n2, err2 := w.Write(one[:])
		ckf(n2 == 0 && err2 != nil, "round %d cycle %d: write after EPIPE n=%d err=%v", roundIdx, c, n2, err2)
		w.Close()

		var tmp [1]byte
		if _, rerr := r.Read(tmp[:]); !errors.Is(rerr, os.ErrClosed) {
			ckf(false, "round %d cycle %d: read on closed pipe err=%v, want ErrClosed", roundIdx, c, rerr)
		}
		runtime.KeepAlive(payload)
		gcStress()
	}
}

func main() {
	seed := envSeed()
	rounds := envInt("TORTURE_ROUNDS", 2, 1, 20)
	fmt.Printf("cycles_per_round=%d payload=%d rounds=%d\n", cyclesPerRound, payloadLen, rounds)
	for r := 0; r < rounds; r++ {
		round(seed+int64(r)*131, r)
	}
	gcStress()
	finish("l19_os_pipe_partial_epipe")
}
