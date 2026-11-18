// L19: pipe 读取消路径。io.Pipe 的 reader 阻塞时 CloseWithError 必须让它带着
// 指定错误返回，且错误粘滞、二次 CloseWithError 不覆盖；os.Pipe 上分别验证
// 写端关闭得到 io.EOF、读端被另一 goroutine 关闭得到 os.ErrClosed。每次取消
// 前后显式 GC 并校验读缓冲/栈上的指针 payload。
package main

import (
	"errors"
	"fmt"
	"io"
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

const waitLimit = 5 * time.Second

var errCanceled = errors.New("l19 pipe read canceled")

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

func makeChain(base int64) *node {
	return &node{v: base, next: &node{v: base + 1, next: &node{v: base + 2}}}
}

func chainOK(head *node, base int64) bool {
	if head == nil || head.v != base || head.next == nil || head.next.v != base+1 {
		return false
	}
	return head.next.next != nil && head.next.next.v == base+2 && head.next.next.next == nil
}

type readResult struct {
	n   int
	err error
}

func main() {
	seed := envSeed()
	base := seed*1000 + 11

	// 1) io.Pipe：先读到部分数据，再 CloseWithError -> 错误粘滞。
	pr, pw := io.Pipe()
	p := makeChain(base)
	writeDone := make(chan error, 1)
	reads := make(chan readResult, 2)
	ready := make(chan struct{}, 1)
	go func() {
		buf := make([]byte, 32)
		ready <- struct{}{}
		n, err := pr.Read(buf)
		reads <- readResult{n: n, err: err}
		_, err = pr.Read(buf)
		reads <- readResult{n: 0, err: err}
	}()
	<-ready
	go func() {
		n, err := pw.Write([]byte("half-payload"))
		if err != nil {
			writeDone <- err
			return
		}
		if n != 12 {
			writeDone <- fmt.Errorf("short write %d", n)
			return
		}
		writeDone <- nil
	}()

	select {
	case r := <-reads:
		ckf(r.n == 12, "io.Pipe first read n=%d want=12", r.n)
		ckf(r.err == nil, "io.Pipe first read err=%v want nil", r.err)
	case <-time.After(waitLimit):
		ck(false, "io.Pipe first read did not return")
	}
	select {
	case err := <-writeDone:
		ckf(err == nil, "io.Pipe write: %v", err)
	case <-time.After(waitLimit):
		ck(false, "io.Pipe write did not finish")
	}
	gcStress()
	ck(chainOK(p, base), "io.Pipe payload corrupted before cancel")

	if err := pw.CloseWithError(errCanceled); err != nil {
		ckf(false, "CloseWithError returned %v", err)
	}
	if err := pw.CloseWithError(errors.New("second error")); err != nil {
		ckf(false, "second CloseWithError returned %v", err)
	}
	select {
	case r := <-reads:
		ckf(errors.Is(r.err, errCanceled), "io.Pipe read after cancel err=%v want sentinel", r.err)
	case <-time.After(waitLimit):
		ck(false, "io.Pipe blocked read not released by CloseWithError")
	}
	buf := make([]byte, 32)
	_, err := pr.Read(buf)
	ckf(errors.Is(err, errCanceled), "io.Pipe sticky error=%v want sentinel", err)
	ck(pr.Close() == nil, "io.Pipe reader Close")
	gcStress()
	ck(chainOK(p, base), "io.Pipe payload corrupted after cancel + GC")

	// 2) os.Pipe：关闭写端 -> 阻塞读返回 io.EOF。
	r1, w1, err := os.Pipe()
	if err != nil {
		ckf(false, "os.Pipe #1: %v", err)
		finish("l19_pipe_read_cancel")
		return
	}
	ready1 := make(chan struct{}, 1)
	eofCh := make(chan error, 1)
	go func() {
		var b [8]byte
		ready1 <- struct{}{}
		_, rerr := r1.Read(b[:])
		eofCh <- rerr
	}()
	<-ready1
	gcStress()
	ck(w1.Close() == nil, "os.Pipe write close")
	select {
	case err := <-eofCh:
		ckf(errors.Is(err, io.EOF), "os.Pipe read after write close err=%v want EOF", err)
	case <-time.After(waitLimit):
		ck(false, "os.Pipe write close did not unblock read")
	}
	ck(r1.Close() == nil, "os.Pipe read close")
	gcStress()

	// 3) os.Pipe：另一个 goroutine 关闭读端 -> 阻塞读返回 os.ErrClosed。
	r2, w2, err := os.Pipe()
	if err != nil {
		ckf(false, "os.Pipe #2: %v", err)
		finish("l19_pipe_read_cancel")
		return
	}
	ready2 := make(chan struct{}, 1)
	closeCh := make(chan error, 1)
	go func() {
		var b [8]byte
		ready2 <- struct{}{}
		_, rerr := r2.Read(b[:])
		closeCh <- rerr
	}()
	<-ready2
	gcStress()
	ck(r2.Close() == nil, "os.Pipe concurrent read close")
	select {
	case err := <-closeCh:
		ckf(err != nil, "os.Pipe read after read-end close returned nil error")
		ckf(errors.Is(err, os.ErrClosed), "os.Pipe read err=%v want os.ErrClosed", err)
	case <-time.After(waitLimit):
		ck(false, "os.Pipe read-end close did not unblock read")
	}
	ck(w2.Close() == nil, "os.Pipe write close after read cancel")
	gcStress()

	fmt.Println("io_pipe_cancel=1 os_pipe_eof=1 os_pipe_read_close=1")
	finish("l19_pipe_read_cancel")
}
