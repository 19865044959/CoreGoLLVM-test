// L04: netpoll 唤醒路径：回环 TCP listener + accept 循环；6 个连接在阻塞 Read
// 上等待数据，4 个用已过期 deadline 走 netpoll 超时唤醒。阻塞期间 GC，
// 随后写入数据唤醒 reader；最后关闭 listener 让 accept 从 netpoll 返回。
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

type npnode struct {
	v    int64
	next *npnode
}

type npSession struct {
	id       int
	client   net.Conn
	server   net.Conn
	deadline bool
}

type npResult struct {
	id    int
	read  int
	timed int
	bad   int32
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

func runNetpoll(seed int64) {
	const sessions = 10
	const deadlineSessions = 4

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		ckf(false, "listen: %v", err)
		finish("l04_netpoll_accept_close")
		return
	}
	accepted := make(chan net.Conn, sessions)
	acceptErr := make(chan error, 1)
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				acceptErr <- err
				return
			}
			accepted <- c
		}
	}()

	sess := make([]npSession, sessions)
	for i := 0; i < sessions; i++ {
		cl, err := net.Dial("tcp", ln.Addr().String())
		if err != nil {
			ckf(false, "dial %d: %v", i, err)
			ln.Close()
			finish("l04_netpoll_accept_close")
			return
		}
		sv := <-accepted
		sess[i] = npSession{id: i, client: cl, server: sv, deadline: i < deadlineSessions}
	}

	ready := make(chan int, sessions)
	results := make(chan npResult, sessions)
	kept := make([]*npnode, sessions)
	var wg sync.WaitGroup
	normal := 0
	for i := range sess {
		s := &sess[i]
		b := seed*100003 + int64(s.id)*97
		head := &npnode{v: b, next: &npnode{v: b + 1}}
		kept[s.id] = head
		if s.deadline {
			wg.Add(1)
			go func(s *npSession, head *npnode, b int64) {
				defer wg.Done()
				_ = s.server.SetReadDeadline(time.Now().Add(-time.Second))
				var buf [8]byte
				_, err := s.server.Read(buf[:])
				res := npResult{id: s.id}
				if err == nil {
					res.bad = 1
				} else {
					var ne net.Error
					if errors.As(err, &ne) && ne.Timeout() {
						res.timed = 1
					} else if errors.Is(err, os.ErrDeadlineExceeded) {
						res.timed = 1
					} else {
						res.bad = 1
					}
				}
				if head == nil || head.next == nil || head.next.v != b+1 {
					res.bad = 1
				}
				results <- res
				s.server.Close()
				s.client.Close()
			}(s, head, b)
		} else {
			normal++
			wg.Add(1)
			go func(s *npSession, head *npnode, b int64) {
				defer wg.Done()
				var buf [8]byte
				ready <- s.id
				if _, err := io.ReadFull(s.server, buf[:]); err != nil {
					results <- npResult{id: s.id, bad: 1}
					return
				}
				got := int64(binary.BigEndian.Uint64(buf[:]))
				res := npResult{id: s.id, read: 1}
				if got != b {
					res.bad = 1
				}
				if head == nil || head.next == nil || head.next.v != b+1 {
					res.bad = 1
				}
				if _, err := s.server.Write(buf[:]); err != nil {
					res.bad = 1
				}
				results <- res
			}(s, head, b)
		}
	}

	for i := 0; i < normal; i++ {
		<-ready
	}
	// normal reader 全部阻塞在 Read（netpoll 上），deadline reader 走超时唤醒。
	gcStress()

	for i := range sess {
		s := &sess[i]
		if s.deadline {
			continue
		}
		b := seed*100003 + int64(s.id)*97
		var buf [8]byte
		binary.BigEndian.PutUint64(buf[:], uint64(b))
		if _, err := s.client.Write(buf[:]); err != nil {
			ckf(false, "client %d write: %v", s.id, err)
			continue
		}
		var ack [8]byte
		if _, err := io.ReadFull(s.client, ack[:]); err != nil {
			ckf(false, "client %d read ack: %v", s.id, err)
			continue
		}
		if int64(binary.BigEndian.Uint64(ack[:])) != b {
			ckf(false, "client %d ack mismatch", s.id)
		}
	}
	wg.Wait()
	close(results)

	timed := 0
	reads := 0
	for r := range results {
		ckf(r.bad == 0, "session %d bad=%d timed=%d read=%d", r.id, r.bad, r.timed, r.read)
		timed += r.timed
		reads += r.read
	}
	ckf(timed == deadlineSessions, "timed out sessions=%d want=%d", timed, deadlineSessions)
	ckf(reads == normal, "read sessions=%d want=%d", reads, normal)

	for i := range sess {
		sess[i].client.Close()
		sess[i].server.Close()
	}
	// accept 循环此时阻塞在 netpoll；Close listener 必须立刻唤醒它。
	ln.Close()
	if err := <-acceptErr; !errors.Is(err, net.ErrClosed) {
		ckf(false, "accept err=%v want=net.ErrClosed", err)
	}
	gcStress()
	for i := range kept {
		n := kept[i]
		b := seed*100003 + int64(i)*97
		if n == nil || n.next == nil || n.v != b || n.next.v != b+1 {
			ckf(false, "kept[%d] chain corrupted after netpoll GC", i)
		}
	}
}

func main() {
	seed := envSeed()
	runNetpoll(seed)
	finish("l04_netpoll_accept_close")
}
