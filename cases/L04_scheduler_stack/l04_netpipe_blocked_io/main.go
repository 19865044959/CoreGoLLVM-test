// L04: net.Pipe 阻塞 I/O：每会话一个 writer + 一个 reader；writer 写完头部后
// 停在协调点上，reader 阻塞在第二次 Read。主协程此时 GC，随后放行写尾部；
// 另用过期 deadline 验证 net.Pipe 的 netpoll 超时唤醒路径。
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
	"sync"
	"sync/atomic"
	"time"
)

type pnode struct {
	v    int64
	next *pnode
}

type pipeResult struct {
	id int
	ok bool
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

func pipeSession(sid int, seed int64, halfReady chan<- int, release <-chan struct{}, out chan<- pipeResult, keep []*pnode) {
	c1, c2 := net.Pipe()
	b := seed*1009 + int64(sid)*53
	head := &pnode{v: b, next: &pnode{v: b + 1, next: &pnode{v: b + 2}}}
	keep[sid] = head
	var bad int32
	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		var hdr [24]byte
		if _, err := io.ReadFull(c2, hdr[:]); err != nil {
			atomic.AddInt32(&bad, 1)
			return
		}
		id := binary.BigEndian.Uint64(hdr[0:8])
		base := int64(binary.BigEndian.Uint64(hdr[8:16]))
		sum := int64(binary.BigEndian.Uint64(hdr[16:24]))
		if id != uint64(sid) || base != b || sum != 3*b+3 {
			atomic.AddInt32(&bad, 1)
		}
		var body [8]byte
		if _, err := io.ReadFull(c2, body[:]); err != nil {
			atomic.AddInt32(&bad, 1)
			return
		}
		if int64(binary.BigEndian.Uint64(body[:])) != 3*b+3 {
			atomic.AddInt32(&bad, 1)
		}
	}()

	go func() {
		defer wg.Done()
		var hdr [24]byte
		binary.BigEndian.PutUint64(hdr[0:8], uint64(sid))
		binary.BigEndian.PutUint64(hdr[8:16], uint64(b))
		binary.BigEndian.PutUint64(hdr[16:24], uint64(3*b+3))
		if _, err := c1.Write(hdr[:]); err != nil {
			atomic.AddInt32(&bad, 1)
			c1.Close()
			c2.Close()
			halfReady <- sid
			return
		}
		halfReady <- sid
		<-release
		// writer 阻塞期间 GC，唤醒后复验栈上指针链。
		if head.next == nil || head.next.next == nil ||
			head.next.v != b+1 || head.next.next.v != b+2 {
			atomic.AddInt32(&bad, 1)
		}
		var body [8]byte
		binary.BigEndian.PutUint64(body[:], uint64(head.v+head.next.v+head.next.next.v))
		if _, err := c1.Write(body[:]); err != nil {
			atomic.AddInt32(&bad, 1)
		}
	}()

	wg.Wait()
	c1.Close()
	c2.Close()
	out <- pipeResult{id: sid, ok: atomic.LoadInt32(&bad) == 0}
}

func main() {
	seed := envSeed()
	const sessions = 8
	halfReady := make(chan int, sessions)
	release := make(chan struct{})
	out := make(chan pipeResult, sessions)
	keep := make([]*pnode, sessions)

	for sid := 0; sid < sessions; sid++ {
		go pipeSession(sid, seed, halfReady, release, out, keep)
	}
	for i := 0; i < sessions; i++ {
		<-halfReady
	}
	// reader 阻塞在 pipe 的第二次 Read、writer 阻塞在 release 上时 GC。
	gcStress()
	close(release)

	for i := 0; i < sessions; i++ {
		res := <-out
		ckf(res.ok, "session %d pipe payload corrupted", res.id)
	}

	gcStress()
	for sid := 0; sid < sessions; sid++ {
		h := keep[sid]
		if h == nil || h.next == nil || h.next.next == nil {
			ckf(false, "session %d chain missing after pipe GC", sid)
			continue
		}
		b := seed*1009 + int64(sid)*53
		ckf(h.v == b && h.next.v == b+1 && h.next.next.v == b+2,
			"session %d chain corrupted after pipe GC", sid)
	}

	// 过期 deadline：net.Pipe 读必须在 netpoll 超时路径上立即唤醒。
	pc1, pc2 := net.Pipe()
	if err := pc1.SetReadDeadline(time.Now().Add(-time.Second)); err != nil {
		ckf(false, "SetReadDeadline failed: %v", err)
	}
	var one [1]byte
	_, err := pc1.Read(one[:])
	ckf(err != nil && errors.Is(err, os.ErrDeadlineExceeded), "net.Pipe expired deadline did not wake with timeout error")
	pc1.Close()
	pc2.Close()

	finish("l04_netpipe_blocked_io")
}
