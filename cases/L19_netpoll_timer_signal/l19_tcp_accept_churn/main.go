// L19: TCP 监听器的 accept 状态切换 churn。每轮先设置已过期 Accept deadline
// 验证超时错误，再清空 deadline 接受 4 个并发客户端；随后 accept 循环阻塞在
// 第 5 次 Accept 上，主协程关闭 listener，要求阻塞中的 Accept 被唤醒并返回
// ErrClosed。每轮穿插显式 GC 与带指针帧的回显校验。TORTURE_ROUNDS 放大轮数。
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
	"sync"
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

var failed int32

const (
	frameLen    = 48
	wireMagic   = 0x4c313941 // "A91L" little-endian
	clients     = 4
	msgsPerConn = 8
	waitLimit   = 10 * time.Second
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

func msgBase(id int, seed int64) int64 {
	return int64(id)*89 + seed*47
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

func getFrame(b []byte) (*message, error) {
	if binary.LittleEndian.Uint32(b[0:4]) != wireMagic {
		return nil, errors.New("bad magic")
	}
	id := int(binary.LittleEndian.Uint32(b[4:8]))
	base := int64(binary.LittleEndian.Uint64(b[8:16]))
	if binary.LittleEndian.Uint64(b[32:40]) != uint64(base*11+int64(id)) {
		return nil, errors.New("bad checksum")
	}
	return &message{
		id:   id,
		base: base,
		name: string(bytes.TrimRight(b[16:32], "\x00")),
		head: &node{v: base, next: &node{v: base + 1, next: &node{v: base + 2}}},
	}, nil
}

func messageOK(m *message, id int, seed int64) bool {
	if m == nil || m.id != id || m.name != msgName(id) || m.base != msgBase(id, seed) {
		return false
	}
	b := msgBase(id, seed)
	h := m.head
	if h == nil || h.v != b || h.next == nil || h.next.v != b+1 {
		return false
	}
	return h.next.next != nil && h.next.next.v == b+2 && h.next.next.next == nil
}

func serve(c net.Conn, seed int64, handled chan<- struct{}, errCh chan<- error) {
	defer c.Close()
	defer func() { handled <- struct{}{} }()
	c.SetDeadline(time.Now().Add(waitLimit))
	buf := make([]byte, frameLen)
	var held []*message
	for i := 0; i < msgsPerConn; i++ {
		if _, err := io.ReadFull(c, buf); err != nil {
			errCh <- fmt.Errorf("server read %d: %w", i, err)
			return
		}
		m, err := getFrame(buf)
		if err != nil {
			errCh <- fmt.Errorf("server decode %d: %w", i, err)
			return
		}
		if !messageOK(m, m.id, seed) {
			errCh <- fmt.Errorf("server payload corrupted at %d", i)
			return
		}
		held = append(held, m)
		if i == msgsPerConn/2-1 {
			gcStress()
			for k, hm := range held {
				if !messageOK(hm, hm.id, seed) {
					errCh <- fmt.Errorf("server held %d corrupted after GC", k)
					return
				}
			}
		}
		putFrame(buf, m.id, seed)
		if _, err := c.Write(buf); err != nil {
			errCh <- fmt.Errorf("server echo %d: %w", i, err)
			return
		}
	}
	gcStress()
}

func runClient(addr string, cid int, seed int64, wg *sync.WaitGroup, errCh chan<- error) {
	defer wg.Done()
	c, err := net.Dial("tcp", addr)
	if err != nil {
		errCh <- fmt.Errorf("client %d dial: %w", cid, err)
		return
	}
	defer c.Close()
	c.SetDeadline(time.Now().Add(waitLimit))
	buf := make([]byte, frameLen)
	held := make([]*message, 0, msgsPerConn)
	for i := 0; i < msgsPerConn; i++ {
		id := cid*100 + i
		putFrame(buf, id, seed)
		if _, err := c.Write(buf); err != nil {
			errCh <- fmt.Errorf("client %d write %d: %w", cid, i, err)
			return
		}
		if _, err := io.ReadFull(c, buf); err != nil {
			errCh <- fmt.Errorf("client %d read %d: %w", cid, i, err)
			return
		}
		m, err := getFrame(buf)
		if err != nil {
			errCh <- fmt.Errorf("client %d decode %d: %w", cid, i, err)
			return
		}
		if !messageOK(m, id, seed) {
			errCh <- fmt.Errorf("client %d echo %d corrupted", cid, i)
			return
		}
		held = append(held, m)
	}
	gcStress()
	for i, m := range held {
		if !messageOK(m, cid*100+i, seed) {
			errCh <- fmt.Errorf("client %d held echo %d corrupted after GC", cid, i)
			return
		}
	}
}

func round(seed int64) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		ckf(false, "listen: %v", err)
		return
	}
	tl := ln.(*net.TCPListener)
	addr := ln.Addr().String()

	// 过期 deadline：Accept 立即返回 timeout。
	if err := tl.SetDeadline(time.Now().Add(-time.Second)); err != nil {
		ckf(false, "set past accept deadline: %v", err)
	}
	c0, err := tl.Accept()
	ckf(c0 == nil && errors.Is(err, os.ErrDeadlineExceeded), "past-deadline accept: conn=%v err=%v", c0, err)
	if c0 != nil {
		c0.Close()
	}
	if err := tl.SetDeadline(time.Time{}); err != nil {
		ckf(false, "clear accept deadline: %v", err)
	}
	gcStress()

	handled := make(chan struct{}, clients)
	acceptDone := make(chan error, 1)
	errCh := make(chan error, clients*2+clients)
	go func() {
		var wg sync.WaitGroup
		for i := 0; i < clients; i++ {
			c, err := tl.Accept()
			if err != nil {
				acceptDone <- fmt.Errorf("accept %d: %w", i, err)
				wg.Wait()
				return
			}
			wg.Add(1)
			go func(c net.Conn) {
				defer wg.Done()
				serve(c, seed, handled, errCh)
			}(c)
		}
		// 第 5 次 Accept 应当阻塞，直到主协程关闭 listener。
		c, err := tl.Accept()
		if err == nil {
			c.Close()
			acceptDone <- errors.New("extra Accept succeeded unexpectedly")
		} else {
			acceptDone <- err
		}
		wg.Wait()
	}()

	var wg sync.WaitGroup
	for i := 0; i < clients; i++ {
		wg.Add(1)
		go runClient(addr, i, seed, &wg, errCh)
	}
	cliDone := make(chan struct{})
	go func() {
		wg.Wait()
		close(cliDone)
	}()
	select {
	case <-cliDone:
	case <-time.After(2 * waitLimit):
		ck(false, "clients did not finish")
	}
	for i := 0; i < clients; i++ {
		select {
		case <-handled:
		case <-time.After(waitLimit):
			ck(false, "handler did not finish")
			i = clients
		}
	}

	// 关闭 listener，唤醒阻塞中的第 5 次 Accept。
	ln.Close()
	select {
	case err := <-acceptDone:
		ckf(errors.Is(err, net.ErrClosed), "final accept: %v, want net.ErrClosed", err)
	case <-time.After(waitLimit):
		ck(false, "blocked Accept not woken by listener close")
	}

	close(errCh)
	for e := range errCh {
		ckf(false, "round: %v", e)
	}
	gcStress()
}

func main() {
	seed := envSeed()
	rounds := envInt("TORTURE_ROUNDS", 2, 1, 50)
	fmt.Printf("clients=%d msgs=%d rounds=%d\n", clients, msgsPerConn, rounds)
	for r := 0; r < rounds; r++ {
		round(seed + int64(r)*131)
	}
	finish("l19_tcp_accept_churn")
}
