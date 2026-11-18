// L19: 连接 deadline exceeded 路径。TCP 回环连接先设已过期的读 deadline，
// Read 必须返回 net.Error 且 Timeout()=true、errors.Is(os.ErrDeadlineExceeded)；
// 清空 deadline 后服务器下发的帧必须正常读回。随后验证过期写 deadline 与
// net.Pipe 上的读写 deadline。只用绝对 deadline，不做耗时断言。
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

type message struct {
	id   int
	base int64
	head *node
}

var failed int32

const (
	frameLen  = 32
	wireMagic = 0x4c313944 // "D91L" little-endian
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
	return int64(id)*71 + seed*53
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

func putFrame(b []byte, id int, seed int64) {
	base := baseOf(id, seed)
	binary.LittleEndian.PutUint32(b[0:4], wireMagic)
	binary.LittleEndian.PutUint32(b[4:8], uint32(id))
	binary.LittleEndian.PutUint64(b[8:16], uint64(base))
}

func getFrame(b []byte, id int, seed int64) (*message, error) {
	if binary.LittleEndian.Uint32(b[0:4]) != wireMagic {
		return nil, errors.New("bad magic")
	}
	gotID := int(binary.LittleEndian.Uint32(b[4:8]))
	base := int64(binary.LittleEndian.Uint64(b[8:16]))
	if gotID != id || base != baseOf(id, seed) {
		return nil, fmt.Errorf("frame mismatch id=%d base=%d", gotID, base)
	}
	return &message{id: gotID, base: base, head: makeChain(base)}, nil
}

func messageOK(m *message, id int, seed int64) bool {
	return m != nil && m.id == id && m.base == baseOf(id, seed) && chainOK(m.head, m.base)
}

func isTimeout(err error) bool {
	var ne net.Error
	return errors.As(err, &ne) && ne.Timeout()
}

func checkDeadlineErr(err error, label string) {
	ckf(err != nil, "%s: nil error", label)
	ckf(isTimeout(err), "%s: err=%v not a timeout", label, err)
	ckf(errors.Is(err, os.ErrDeadlineExceeded), "%s: err=%v not ErrDeadlineExceeded", label, err)
	ckf(!errors.Is(err, io.EOF), "%s: deadline error reported as EOF", label)
}

func main() {
	seed := envSeed()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		ckf(false, "listen: %v", err)
		finish("l19_conn_deadline_exceeded")
		return
	}
	defer ln.Close()

	accepted := make(chan net.Conn, 1)
	acceptErr := make(chan error, 1)
	go func() {
		c, err := ln.Accept()
		if err != nil {
			acceptErr <- err
			return
		}
		accepted <- c
	}()
	cli, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		ckf(false, "dial: %v", err)
		finish("l19_conn_deadline_exceeded")
		return
	}
	defer cli.Close()
	var srv net.Conn
	select {
	case srv = <-accepted:
	case err := <-acceptErr:
		ckf(false, "accept: %v", err)
		finish("l19_conn_deadline_exceeded")
		return
	case <-time.After(waitLimit):
		ck(false, "accept did not return")
		finish("l19_conn_deadline_exceeded")
		return
	}
	defer srv.Close()

	buf := make([]byte, frameLen)

	// 1) TCP 读 deadline 过期：连续两次读都必须超时。
	if err := cli.SetReadDeadline(time.Now().Add(-time.Second)); err != nil {
		ckf(false, "set expired read deadline: %v", err)
	}
	n, err := cli.Read(buf)
	ckf(n == 0, "expired read returned %d bytes", n)
	checkDeadlineErr(err, "tcp read #1")
	_, err = cli.Read(buf)
	checkDeadlineErr(err, "tcp read #2")
	gcStress()

	// 2) 清空 deadline 后同一连接可正常读取。
	if err := cli.SetReadDeadline(time.Time{}); err != nil {
		ckf(false, "clear read deadline: %v", err)
	}
	putFrame(buf, 0, seed)
	if _, err := srv.Write(buf); err != nil {
		ckf(false, "server write: %v", err)
	} else if _, err := io.ReadFull(cli, buf); err != nil {
		ckf(false, "read after clearing deadline: %v", err)
	} else if m, err := getFrame(buf, 0, seed); err != nil {
		ckf(false, "decode: %v", err)
	} else {
		gcStress()
		ckf(messageOK(m, 0, seed), "payload corrupted after GC")
	}

	// 3) TCP 写 deadline 过期：Write 必须超时而不是阻塞。
	if err := cli.SetWriteDeadline(time.Now().Add(-time.Second)); err != nil {
		ckf(false, "set expired write deadline: %v", err)
	}
	putFrame(buf, 1, seed)
	_, err = cli.Write(buf)
	checkDeadlineErr(err, "tcp write")
	if err := cli.SetWriteDeadline(time.Time{}); err != nil {
		ckf(false, "clear write deadline: %v", err)
	}
	// 清空后写 ACK，服务器读满。
	putFrame(buf, 2, seed)
	if _, err := cli.Write(buf); err != nil {
		ckf(false, "write ack after clearing deadline: %v", err)
	} else if _, err := io.ReadFull(srv, buf); err != nil {
		ckf(false, "server read ack: %v", err)
	}
	gcStress()

	// 4) net.Pipe 上的读/写 deadline。
	pa, pb := net.Pipe()
	if err := pa.SetReadDeadline(time.Now().Add(-time.Second)); err != nil {
		ckf(false, "pipe set read deadline: %v", err)
	}
	_, err = pa.Read(buf)
	checkDeadlineErr(err, "pipe read")
	if err := pa.SetReadDeadline(time.Time{}); err != nil {
		ckf(false, "pipe clear read deadline: %v", err)
	}

	base := baseOf(9, seed)
	head := makeChain(base)
	wrErr := make(chan error, 1)
	go func() {
		fb := make([]byte, frameLen)
		putFrame(fb, 9, seed)
		_, err := pb.Write(fb)
		wrErr <- err
	}()
	if _, err := io.ReadFull(pa, buf); err != nil {
		ckf(false, "pipe read after clearing deadline: %v", err)
	} else if _, err := getFrame(buf, 9, seed); err != nil {
		ckf(false, "pipe decode: %v", err)
	} else {
		gcStress()
		ck(chainOK(head, base), "pipe payload corrupted after GC")
	}
	select {
	case err := <-wrErr:
		ckf(err == nil, "pipe write: %v", err)
	case <-time.After(waitLimit):
		ck(false, "pipe write did not finish")
	}

	if err := pa.SetWriteDeadline(time.Now().Add(-time.Second)); err != nil {
		ckf(false, "pipe set write deadline: %v", err)
	}
	_, err = pa.Write(buf)
	checkDeadlineErr(err, "pipe write")
	if err := pa.SetWriteDeadline(time.Time{}); err != nil {
		ckf(false, "pipe clear write deadline: %v", err)
	}
	pa.Close()
	pb.Close()
	gcStress()

	fmt.Println("tcp_read_timeouts=2 tcp_write_timeouts=1 pipe_read_timeouts=1 pipe_write_timeouts=1")
	finish("l19_conn_deadline_exceeded")
}
