// L19: IO 阻塞期间的 context 取消。reader 阻塞在 net.Pipe 的 Read 上，
// context.AfterFunc 在 ctx 取消时关闭连接唤醒 Read；验证 Err 为 Canceled、
// Read 返回关闭类错误、回调恰好执行一次。对照路径在取消前停掉 AfterFunc，
// 连接保持可用并把带指针链的帧完整读回。
package main

import (
	"context"
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

type node struct {
	v    int64
	next *node
}

var failed int32

const (
	frameLen  = 32
	wireMagic = 0x4c313943 // "C91L" little-endian
	waitLimit = 10 * time.Second
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

func baseOf(id int, seed int64) int64 {
	return int64(id)*61 + seed*47
}

func makeChain(base int64) *node {
	return &node{v: base, next: &node{v: base + 1, next: &node{v: base + 2}}}
}

func chainOK(head *node, base int64) bool {
	if head == nil || head.v != base || head.next == nil || head.next.v != base+1 {
		return false
	}
	return head.next.next != nil && head.next.next.v == base+2 && head.next.next.next == nil
}

func isCloseErr(err error) bool {
	return errors.Is(err, io.ErrClosedPipe) || errors.Is(err, net.ErrClosed)
}

func main() {
	seed := envSeed()

	// 1) 取消路径：reader 阻塞在 Read 上，AfterFunc 关闭连接。
	c1, c2 := net.Pipe()
	ctx, cancel := context.WithCancel(context.Background())
	base := seed*1000 + 1
	p := makeChain(base)
	var callbacks int32
	stop := context.AfterFunc(ctx, func() {
		atomic.AddInt32(&callbacks, 1)
		c2.Close()
	})

	type readResult struct {
		n   int
		err error
		ok  bool
	}
	ready := make(chan struct{}, 1)
	resCh := make(chan readResult, 1)
	go func() {
		buf := make([]byte, frameLen)
		ready <- struct{}{}
		n, err := c2.Read(buf)
		resCh <- readResult{n: n, err: err, ok: chainOK(p, base)}
	}()
	<-ready
	gcStress()
	cancel()

	var res readResult
	select {
	case res = <-resCh:
	case <-time.After(waitLimit):
		ck(false, "blocked Read was not unblocked by ctx cancel")
	}
	ckf(res.err != nil, "Read completed with nil error after ctx cancel")
	ckf(isCloseErr(res.err), "Read err=%v want closed-pipe/net-closed", res.err)
	ckf(ctx.Err() == context.Canceled, "ctx.Err()=%v want Canceled", ctx.Err())
	ckf(atomic.LoadInt32(&callbacks) == 1, "AfterFunc ran %d times want 1", atomic.LoadInt32(&callbacks))
	ckf(!stop(), "stop after callback ran returned true")
	ck(res.ok, "reader payload corrupted after cancel + GC")
	c1.Close()
	gcStress()

	// 2) 对照路径：取消前停掉 AfterFunc，连接不被关闭，IO 正常完成。
	c3, c4 := net.Pipe()
	ctx2, cancel2 := context.WithCancel(context.Background())
	var callbacks2 int32
	stop2 := context.AfterFunc(ctx2, func() {
		atomic.AddInt32(&callbacks2, 1)
		c4.Close()
	})
	ck(stop2(), "stop before cancel returned false")
	cancel2()
	gcStress()
	ckf(atomic.LoadInt32(&callbacks2) == 0, "stopped AfterFunc ran %d times", atomic.LoadInt32(&callbacks2))
	ckf(ctx2.Err() == context.Canceled, "stopped path ctx.Err()=%v", ctx2.Err())

	buf := make([]byte, frameLen)
	base2 := baseOf(3, seed)
	head2 := makeChain(base2)
	binary.LittleEndian.PutUint32(buf[0:4], wireMagic)
	binary.LittleEndian.PutUint32(buf[4:8], 3)
	binary.LittleEndian.PutUint64(buf[8:16], uint64(base2))

	wrErr := make(chan error, 1)
	go func() {
		_, err := c4.Write(buf)
		wrErr <- err
	}()
	rbuf := make([]byte, frameLen)
	if _, err := io.ReadFull(c3, rbuf); err != nil {
		ckf(false, "control path read after stop: %v", err)
	} else if binary.LittleEndian.Uint32(rbuf[0:4]) != wireMagic ||
		binary.LittleEndian.Uint64(rbuf[8:16]) != uint64(base2) {
		ck(false, "control path frame corrupted")
	} else {
		gcStress()
		ck(chainOK(head2, base2), "control path payload corrupted after GC")
	}
	select {
	case err := <-wrErr:
		ckf(err == nil, "control path write: %v", err)
	case <-time.After(waitLimit):
		ck(false, "control path write did not finish")
	}
	c3.Close()
	c4.Close()
	gcStress()

	fmt.Println("cancel_unblocks_io=1 afterfunc_runs=1 stopped_afterfunc_runs=0")
	finish("l19_io_context_cancel")
}
