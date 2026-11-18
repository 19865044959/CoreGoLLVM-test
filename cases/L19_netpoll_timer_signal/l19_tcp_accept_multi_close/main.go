// L19: 同一监听器上多个阻塞 Accept 的关闭唤醒。8 个 goroutine 同时挂在
// Accept 上，Close listener 必须唤醒全部并返回 net.ErrClosed；之后拨号必须
// 失败。第二个监听器验证关闭风暴后 netpoll 仍可正常 accept/dial 并完成一次
// 带指针链的帧交换，期间穿插显式 GC。
package main

import (
	"bytes"
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

type message struct {
	id   int
	base int64
	name string
	head *node
}

type acceptResult struct {
	c   net.Conn
	err error
}

var failed int32

const (
	frameLen  = 48
	wireMagic = 0x4c394d41 // "AM9L" little-endian
	acceptors = 8
	dials     = 8
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

func msgBase(id int, seed int64) int64 {
	return int64(id)*79 + seed*23
}

func msgName(id int) string {
	return fmt.Sprintf("acc-%04d", id)
}

func putFrame(b []byte, id int, seed int64) {
	base := msgBase(id, seed)
	binary.LittleEndian.PutUint32(b[0:4], wireMagic)
	binary.LittleEndian.PutUint32(b[4:8], uint32(id))
	binary.LittleEndian.PutUint64(b[8:16], uint64(base))
	copy(b[16:32], msgName(id))
	binary.LittleEndian.PutUint64(b[32:40], uint64(base*11+int64(id)))
}

func getFrame(b []byte, seed int64) (*message, error) {
	if binary.LittleEndian.Uint32(b[0:4]) != wireMagic {
		return nil, errors.New("bad magic")
	}
	id := int(binary.LittleEndian.Uint32(b[4:8]))
	base := int64(binary.LittleEndian.Uint64(b[8:16]))
	if base != msgBase(id, seed) {
		return nil, errors.New("bad base")
	}
	if binary.LittleEndian.Uint64(b[32:40]) != uint64(base*11+int64(id)) {
		return nil, errors.New("bad checksum")
	}
	name := string(bytes.TrimRight(b[16:32], "\x00"))
	return &message{
		id:   id,
		base: base,
		name: name,
		head: &node{v: base, next: &node{v: base + 1, next: &node{v: base + 2}}},
	}, nil
}

func messageOK(m *message, id int, seed int64) bool {
	if m == nil || m.id != id || m.name != msgName(id) || m.base != msgBase(id, seed) {
		return false
	}
	base := msgBase(id, seed)
	h := m.head
	if h == nil || h.v != base || h.next == nil || h.next.v != base+1 {
		return false
	}
	return h.next.next != nil && h.next.next.v == base+2 && h.next.next.next == nil
}

func main() {
	seed := envSeed()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		ckf(false, "listen: %v", err)
		finish("l19_tcp_accept_multi_close")
		return
	}
	addr := ln.Addr().String()

	started := make(chan struct{}, acceptors)
	results := make(chan acceptResult, acceptors)
	for i := 0; i < acceptors; i++ {
		go func(i int) {
			started <- struct{}{}
			c, err := ln.Accept()
			results <- acceptResult{c: c, err: err}
		}(i)
	}
	for i := 0; i < acceptors; i++ {
		select {
		case <-started:
		case <-time.After(waitLimit):
			ckf(false, "accept goroutine %d did not start", i)
		}
	}
	gcStress()
	if err := ln.Close(); err != nil {
		ckf(false, "close listener with blocked accepts: %v", err)
	}
	for i := 0; i < acceptors; i++ {
		select {
		case r := <-results:
			if r.c != nil {
				r.c.Close()
				ckf(false, "accept %d returned a conn after close", i)
			}
			ckf(errors.Is(r.err, net.ErrClosed), "accept %d err=%v, want net.ErrClosed", i, r.err)
		case <-time.After(waitLimit):
			ckf(false, "accept %d not woken by listener close", i)
		}
	}
	// 关闭后新拨号必须失败，且不产生连接对象。
	for i := 0; i < dials; i++ {
		c, derr := net.DialTimeout("tcp", addr, waitLimit)
		if c != nil {
			c.Close()
			ckf(false, "dial %d after close succeeded", i)
		}
		ckf(derr != nil, "dial %d after close returned nil error", i)
	}
	gcStress()

	// 健康路径：关闭风暴后新监听器仍可 accept/dial 并完成帧交换。
	ln2, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		ckf(false, "second listen: %v", err)
		finish("l19_tcp_accept_multi_close")
		return
	}
	defer ln2.Close()
	tl2 := ln2.(*net.TCPListener)
	if err := tl2.SetDeadline(time.Now().Add(waitLimit)); err != nil {
		ckf(false, "second listener deadline: %v", err)
	}
	srvCh := make(chan error, 1)
	go func() {
		c, err := tl2.Accept()
		if err != nil {
			srvCh <- fmt.Errorf("second accept: %w", err)
			return
		}
		tc, ok := c.(*net.TCPConn)
		if !ok {
			c.Close()
			srvCh <- errors.New("accepted conn is not *net.TCPConn")
			return
		}
		defer tc.Close()
		if err := tc.SetDeadline(time.Now().Add(waitLimit)); err != nil {
			srvCh <- fmt.Errorf("server set deadline: %w", err)
			return
		}
		buf := make([]byte, frameLen)
		if _, err := io.ReadFull(tc, buf); err != nil {
			srvCh <- fmt.Errorf("server read: %w", err)
			return
		}
		m, err := getFrame(buf, seed)
		if err != nil || !messageOK(m, 3, seed) {
			srvCh <- fmt.Errorf("server payload corrupted: %v", err)
			return
		}
		gcStress()
		if !messageOK(m, 3, seed) {
			srvCh <- errors.New("server payload corrupted after GC")
			return
		}
		putFrame(buf, 4, seed)
		if _, err := tc.Write(buf); err != nil {
			srvCh <- fmt.Errorf("server ack: %w", err)
			return
		}
		srvCh <- nil
	}()

	cl, err := net.Dial("tcp", ln2.Addr().String())
	if err != nil {
		ckf(false, "second dial: %v", err)
		finish("l19_tcp_accept_multi_close")
		return
	}
	cl.(*net.TCPConn).SetDeadline(time.Now().Add(waitLimit))
	buf := make([]byte, frameLen)
	putFrame(buf, 3, seed)
	if _, err := cl.Write(buf); err != nil {
		ckf(false, "client write: %v", err)
	}
	gcStress()
	if _, err := io.ReadFull(cl, buf); err != nil {
		ckf(false, "client read ack: %v", err)
	} else {
		m, err := getFrame(buf, seed)
		if err != nil || !messageOK(m, 4, seed) {
			ckf(false, "client ack corrupted: %v", err)
		}
	}
	cl.Close()
	select {
	case err := <-srvCh:
		if err != nil {
			ckf(false, "server: %v", err)
		}
	case <-time.After(waitLimit):
		ck(false, "second server goroutine did not finish")
	}
	gcStress()
	fmt.Printf("acceptors=%d dials=%d\n", acceptors, dials)
	finish("l19_tcp_accept_multi_close")
}
