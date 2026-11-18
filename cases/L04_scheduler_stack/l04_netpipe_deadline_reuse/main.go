// L04: net.Pipe + 超时。每轮在同一对管道上先让 Read 在 netpoll deadline 上超时
// （阻塞期间显式 GC），清除 deadline 后正常收发包；再让 Write 超时，清除
// deadline 后重试成功。验证超时错误、deadline 复位与指针链存活。
package main

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"runtime"
	"strconv"
	"sync/atomic"
	"time"
)

type npNode struct {
	v    int64
	next *npNode
}

var (
	failed int32
	gcSink [][]byte
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
	for i := 0; i < 8; i++ {
		b := make([]byte, 4096)
		b[0] = byte(i)
		gcSink = append(gcSink[:0], b)
	}
	gcSink = gcSink[:0]
	runtime.GC()
	runtime.GC()
}

func finish(name string, seed int64) {
	if n := atomic.LoadInt32(&failed); n != 0 {
		fmt.Printf("FAILED %s (UAF_SEED=%d, %d failed checks)\n", name, seed, n)
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

func expectReadTimeout(c net.Conn, round int) {
	c.SetReadDeadline(time.Now().Add(2 * time.Millisecond))
	errCh := make(chan error, 1)
	go func() {
		var b [8]byte
		_, err := io.ReadFull(c, b[:])
		errCh <- err
	}()
	// 读 goroutine 停在 net.Pipe 的 netpoll deadline 上时显式 GC。
	gcStress()
	err := <-errCh
	ckf(errors.Is(err, os.ErrDeadlineExceeded), "round %d read timeout err=%v", round, err)
	c.SetReadDeadline(time.Time{})
}

func main() {
	seed := envSeed()
	rounds := 8 + int(seed%4)
	c1, c2 := net.Pipe()
	defer c1.Close()
	defer c2.Close()

	chain := &npNode{v: seed*100003 + 7, next: &npNode{v: seed*100003 + 8}}
	big := make([]byte, 4096)
	for i := range big {
		big[i] = byte(seed + int64(i))
	}

	for r := 0; r < rounds; r++ {
		// 1. Read 超时 + GC；清 deadline 后可复用。
		expectReadTimeout(c1, r)

		// 2. 正常收发包，校验 deadline 清除后管道仍可用、指针链未被 GC 破坏。
		req := make([]byte, 16)
		binary.BigEndian.PutUint64(req[0:8], uint64(r))
		binary.BigEndian.PutUint64(req[8:16], uint64(chain.v+chain.next.v))
		werr := make(chan error, 1)
		go func() {
			_, err := c2.Write(req)
			werr <- err
		}()
		var got [16]byte
		_, err := io.ReadFull(c1, got[:])
		ckf(err == nil, "round %d read after deadline clear err=%v", r, err)
		ckf(<-werr == nil, "round %d paired write err", r)
		if err == nil {
			ckf(binary.BigEndian.Uint64(got[0:8]) == uint64(r), "round %d payload id mismatch", r)
			ckf(binary.BigEndian.Uint64(got[8:16]) == uint64(chain.v+chain.next.v), "round %d payload sum mismatch", r)
		}
		ckf(chain.next.v == chain.v+1, "round %d chain corrupted after read-timeout GC", r)

		// 3. Write 超时：无 reader 时 4KB 写阻塞，2ms deadline 触发。
		c1.SetWriteDeadline(time.Now().Add(2 * time.Millisecond))
		n, err := c1.Write(big)
		ckf(errors.Is(err, os.ErrDeadlineExceeded), "round %d write timeout err=%v (n=%d)", r, err, n)
		ckf(n == 0, "round %d timed-out write transferred %d bytes want=0", r, n)
		c1.SetWriteDeadline(time.Time{})

		// 4. 清除 deadline 后重试同一块数据，reader 排空即成功。
		sink := make([]byte, len(big))
		rerr := make(chan error, 1)
		go func() {
			_, err := io.ReadFull(c2, sink)
			rerr <- err
		}()
		n, err = c1.Write(big)
		ckf(err == nil && n == len(big), "round %d write after deadline clear n=%d err=%v", r, n, err)
		ckf(<-rerr == nil, "round %d paired reader err", r)
		if err == nil {
			for i := range big {
				if sink[i] != big[i] {
					ckf(false, "round %d retry payload mismatch at %d", r, i)
					break
				}
			}
		}
	}

	gcStress()
	ck(chain != nil && chain.next != nil && chain.v+1 == chain.next.v &&
		chain.v == seed*100003+7, "retained chain corrupted after final GC")

	finish("l04_netpipe_deadline_reuse", seed)
}
