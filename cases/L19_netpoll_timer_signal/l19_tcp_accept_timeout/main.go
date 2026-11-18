// L19: TCP listener 的 Accept 超时路径。对 *net.TCPListener 设置已过期 deadline，
// Accept 必须返回 net.Error 且 Timeout()=true；清空 deadline 后下一次 Accept 必须
// 正常接住受控拨号。固定 8 轮，最后验证 Close listener 让阻塞 Accept 返回
// net.ErrClosed，而不是超时。全程穿插 GC 与帧 payload 校验。
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

type node struct {
	v    int64
	next *node
}

var failed int32

const (
	frameLen     = 32
	wireMagic    = 0x4c313941 // "A91L" little-endian
	acceptRounds = 8
	waitLimit    = 10 * time.Second
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

func baseOf(round int, seed int64) int64 {
	return int64(round)*53 + seed*29
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

func putFrame(b []byte, round int, seed int64) {
	base := baseOf(round, seed)
	binary.LittleEndian.PutUint32(b[0:4], wireMagic)
	binary.LittleEndian.PutUint32(b[4:8], uint32(round))
	binary.LittleEndian.PutUint64(b[8:16], uint64(base))
}

func getFrame(b []byte, round int, seed int64) (*message, error) {
	if binary.LittleEndian.Uint32(b[0:4]) != wireMagic {
		return nil, errors.New("bad magic")
	}
	id := int(binary.LittleEndian.Uint32(b[4:8]))
	base := int64(binary.LittleEndian.Uint64(b[8:16]))
	if id != round || base != baseOf(round, seed) {
		return nil, fmt.Errorf("frame id/base mismatch id=%d base=%d", id, base)
	}
	return &message{id: id, base: base, head: makeChain(base)}, nil
}

type message struct {
	id   int
	base int64
	head *node
}

func messageOK(m *message, round int, seed int64) bool {
	return m != nil && m.id == round && m.base == baseOf(round, seed) && chainOK(m.head, m.base)
}

func isTimeout(err error) bool {
	var ne net.Error
	return errors.As(err, &ne) && ne.Timeout()
}

type dialResult struct {
	c   net.Conn
	err error
}

func main() {
	seed := envSeed()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		ckf(false, "listen: %v", err)
		finish("l19_tcp_accept_timeout")
		return
	}
	tln := ln.(*net.TCPListener)
	addr := ln.Addr().String()

	for round := 0; round < acceptRounds; round++ {
		// 1) 已过期 deadline：Accept 立即超时，且不得返回连接。
		if err := tln.SetDeadline(time.Now().Add(-time.Second)); err != nil {
			ckf(false, "set expired deadline: %v", err)
			break
		}
		conn, aerr := tln.Accept()
		if conn != nil {
			conn.Close()
			ck(false, "Accept returned a conn with expired deadline")
		}
		ckf(aerr != nil, "Accept with expired deadline returned nil error")
		ckf(isTimeout(aerr), "Accept err=%v want timeout", aerr)

		// 2) 清空 deadline：受控拨号必须被接住。
		if err := tln.SetDeadline(time.Time{}); err != nil {
			ckf(false, "clear deadline: %v", err)
			break
		}
		dch := make(chan dialResult, 1)
		go func() {
			c, derr := net.Dial("tcp", addr)
			dch <- dialResult{c: c, err: derr}
		}()
		ac, aerr := tln.Accept()
		if aerr != nil {
			ckf(false, "round %d accept after clear: %v", round, aerr)
			if dr := <-dch; dr.c != nil {
				dr.c.Close()
			}
			break
		}
		var dr dialResult
		select {
		case dr = <-dch:
		case <-time.After(waitLimit):
			ckf(false, "round %d dial did not return", round)
			ac.Close()
			break
		}
		if dr.err != nil || dr.c == nil {
			ckf(false, "round %d dial: %v", round, dr.err)
			ac.Close()
			break
		}

		// 3) 收发一帧：数据经真实 netpoll 路径，GC 后校验指针链。
		buf := make([]byte, frameLen)
		putFrame(buf, round, seed)
		if _, err := ac.Write(buf); err != nil {
			ckf(false, "round %d server write: %v", round, err)
		} else if _, err := io.ReadFull(dr.c, buf); err != nil {
			ckf(false, "round %d client read: %v", round, err)
		} else if m, err := getFrame(buf, round, seed); err != nil {
			ckf(false, "round %d decode: %v", round, err)
		} else {
			gcStress()
			ckf(messageOK(m, round, seed), "round %d payload corrupted after GC", round)
			// 客户端回 ACK，服务器读满后关闭。
			putFrame(buf, round, seed)
			if _, err := dr.c.Write(buf); err != nil {
				ckf(false, "round %d client ack write: %v", round, err)
			} else if _, err := io.ReadFull(ac, buf); err != nil {
				ckf(false, "round %d server ack read: %v", round, err)
			}
		}
		dr.c.Close()
		ac.Close()
		gcStress()
	}

	// 4) listener.Close 唤醒阻塞 Accept：必须是 net.ErrClosed，而非 timeout。
	if err := tln.SetDeadline(time.Time{}); err != nil {
		ckf(false, "final clear deadline: %v", err)
	}
	started := make(chan struct{})
	accErr := make(chan error, 1)
	go func() {
		close(started)
		c, err := tln.Accept()
		if c != nil {
			c.Close()
		}
		accErr <- err
	}()
	<-started
	gcStress()
	tln.Close()
	select {
	case err := <-accErr:
		ckf(err != nil, "Accept after listener Close returned nil error")
		ckf(errors.Is(err, net.ErrClosed), "Accept after Close err=%v want net.ErrClosed", err)
		ckf(!isTimeout(err), "Accept after Close reported timeout")
	case <-time.After(waitLimit):
		ck(false, "listener Close did not unblock Accept")
	}
	ln.Close()
	gcStress()

	fmt.Printf("accept_timeouts=%d successful_accepts=%d\n", acceptRounds, acceptRounds)
	finish("l19_tcp_accept_timeout")
}
