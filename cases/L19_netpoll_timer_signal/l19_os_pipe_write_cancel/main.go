// L19: 阻塞在 os.Pipe 上的写端在读者消失后必须被 netpoll 唤醒并返回错误，
// 不能永久挂起。分别在“写满管道后关闭读端”和“读走部分数据后关闭读端”两条
// 路径上验证：写端返回非零已写字节 + EPIPE/ErrClosed 类错误，payload 指针链
// 与部分数据在显式 GC 前后保持正确。最后验证已关闭读端的读/写错误。
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

const (
	bigLen    = 4 << 20 // 远大于 Linux 默认管道容量，保证写端阻塞
	partLen   = 4096
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

func makeChain(base int64) *node {
	return &node{v: base, next: &node{v: base + 1, next: &node{v: base + 2}}}
}

func chainOK(h *node, base int64) bool {
	if h == nil || h.v != base || h.next == nil || h.next.v != base+1 {
		return false
	}
	return h.next.next != nil && h.next.next.v == base+2 && h.next.next.next == nil
}

type writeResult struct {
	n   int
	err error
	ok  bool
}

// blockedWrite 启动一个写入 bigLen 字节的 goroutine，写端必然因为管道容量
// 不足而阻塞。返回 started/result 供调用方在阻塞窗口内做 GC 与关闭读端。
func blockedWrite(w *os.File, payload []byte, base int64) (<-chan struct{}, <-chan writeResult) {
	started := make(chan struct{})
	result := make(chan writeResult, 1)
	go func() {
		head := makeChain(base)
		close(started)
		n, err := w.Write(payload)
		result <- writeResult{n: n, err: err, ok: chainOK(head, base)}
	}()
	return started, result
}

func main() {
	seed := envSeed()
	payload := make([]byte, bigLen)
	for i := range payload {
		payload[i] = byte(i*7 + 3)
	}

	// 路径 1：写端阻塞时直接关闭读端。
	r, w, err := os.Pipe()
	if err != nil {
		ckf(false, "pipe1: %v", err)
		finish("l19_os_pipe_write_cancel")
		return
	}
	advertised, res := blockedWrite(w, payload, seed*13+1)
	<-advertised
	gcStress()
	r.Close()

	var got writeResult
	select {
	case got = <-res:
	case <-time.After(waitLimit):
		ck(false, "blocked writer not woken by reader close")
	}
	if got.err == nil {
		ck(false, "write to closed pipe must fail")
	}
	ckf(got.n >= 0 && got.n < bigLen, "blocked write n=%d, want 0<=n<%d", got.n, bigLen)
	ck(got.ok, "writer payload chain corrupted after GC")
	for i := 0; i < got.n; i++ {
		if payload[i] != byte(i*7+3) {
			ckf(false, "written byte %d corrupted", i)
			break
		}
	}
	if _, err := w.Write([]byte("x")); err == nil {
		ck(false, "write after reader close must fail")
	}
	if _, err := r.Read(make([]byte, 1)); !errors.Is(err, os.ErrClosed) {
		ckf(false, "read after local close: %v", err)
	}
	w.Close()

	// 路径 2：读端先消费 4KiB，再关闭；写端已写出的字节必须至少包含这 4KiB。
	r2, w2, err := os.Pipe()
	if err != nil {
		ckf(false, "pipe2: %v", err)
		finish("l19_os_pipe_write_cancel")
		return
	}
	advertised2, res2 := blockedWrite(w2, payload, seed*17+5)
	<-advertised2
	head := make(chan struct{})
	var partialN int
	var partialErr error
	go func() {
		defer close(head)
		buf := make([]byte, partLen)
		n, err := io.ReadFull(r2, buf)
		partialN, partialErr = n, err
		if err == nil {
			for i := 0; i < n; i++ {
				if buf[i] != byte(i*7+3) {
					ckf(false, "partial read byte %d corrupted", i)
					break
				}
			}
		}
	}()
	<-head
	ckf(partialErr == nil && partialN == partLen, "partial read: n=%d err=%v", partialN, partialErr)
	gcStress()
	r2.Close()

	var got2 writeResult
	select {
	case got2 = <-res2:
	case <-time.After(waitLimit):
		ck(false, "writer not woken after partial-read reader close")
	}
	if got2.err == nil {
		ck(false, "writer must fail after reader closes mid-stream")
	}
	ckf(got2.n >= partLen && got2.n <= bigLen, "partial write n=%d, want %d<=n<=%d", got2.n, partLen, bigLen)
	ck(got2.ok, "partial writer payload chain corrupted after GC")
	w2.Close()

	// 路径 3：读端先关闭，随后写入必须返回 EPIPE/ErrClosed 而不是 SIGPIPE 终止。
	r3, w3, err := os.Pipe()
	if err != nil {
		ckf(false, "pipe3: %v", err)
		finish("l19_os_pipe_write_cancel")
		return
	}
	r3.Close()
	if _, err := w3.Write(payload[:partLen]); err == nil {
		ck(false, "write after peer close must fail")
	}
	gcStress()
	w3.Close()

	finish("l19_os_pipe_write_cancel")
}
