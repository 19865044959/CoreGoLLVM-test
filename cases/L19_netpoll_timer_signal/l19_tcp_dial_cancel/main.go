// L19: 拨号阶段的取消/超时路径。已取消的 context、已过期的 context deadline
// 以及已过期的 Dialer.Deadline 都必须在建连前失败并保持错误可判别；监听器
// 侧用已过期 Accept deadline 证明没有残留半开连接。随后正常拨号完成一次带
// 指针 payload 的请求/应答，最后验证关闭监听器后 Accept 返回 ErrClosed。
package main

import (
	"bytes"
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

type message struct {
	id   int
	base int64
	name string
	head *node
}

var failed int32

const (
	frameLen  = 48
	wireMagic = 0x4c313944 // "D91L" little-endian
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
	return int64(id)*83 + seed*37
}

func msgName(id int) string {
	return fmt.Sprintf("dial-%04d", id)
}

func putFrame(b []byte, id int, seed int64) {
	base := msgBase(id, seed)
	binary.LittleEndian.PutUint32(b[0:4], wireMagic)
	binary.LittleEndian.PutUint32(b[4:8], uint32(id))
	binary.LittleEndian.PutUint64(b[8:16], uint64(base))
	copy(b[16:32], msgName(id))
	binary.LittleEndian.PutUint64(b[32:40], uint64(base*5+int64(id)))
}

func getFrame(b []byte) (*message, error) {
	if binary.LittleEndian.Uint32(b[0:4]) != wireMagic {
		return nil, errors.New("bad magic")
	}
	id := int(binary.LittleEndian.Uint32(b[4:8]))
	base := int64(binary.LittleEndian.Uint64(b[8:16]))
	if binary.LittleEndian.Uint64(b[32:40]) != uint64(base*5+int64(id)) {
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
	base := msgBase(id, seed)
	h := m.head
	if h == nil || h.v != base || h.next == nil || h.next.v != base+1 {
		return false
	}
	return h.next.next != nil && h.next.next.v == base+2 && h.next.next.next == nil
}

func isTimeout(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, os.ErrDeadlineExceeded) {
		return true
	}
	var ne net.Error
	return errors.As(err, &ne) && ne.Timeout()
}

func main() {
	seed := envSeed()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		ckf(false, "listen: %v", err)
		finish("l19_tcp_dial_cancel")
		return
	}
	addr := ln.Addr().String()
	tl := ln.(*net.TCPListener)
	defer ln.Close()

	// 1) 已取消 context：建连前返回 context.Canceled。
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	var d net.Dialer
	c0, err := d.DialContext(canceled, "tcp", addr)
	ckf(c0 == nil && errors.Is(err, context.Canceled), "canceled ctx dial: conn=%v err=%v", c0, err)
	if c0 != nil {
		c0.Close()
	}

	// 2) 已过期 context deadline：建连前返回 DeadlineExceeded。
	expired, cancelExp := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	c1, err := d.DialContext(expired, "tcp", addr)
	ckf(c1 == nil && errors.Is(err, context.DeadlineExceeded), "expired ctx dial: conn=%v err=%v", c1, err)
	if c1 != nil {
		c1.Close()
	}
	cancelExp()
	gcStress()

	// 3) Dialer 自带已过期 Deadline：立即返回 timeout 类错误。
	dd := net.Dialer{Deadline: time.Now().Add(-time.Second)}
	c2, err := dd.Dial("tcp", addr)
	ckf(c2 == nil && isTimeout(err), "expired dialer deadline: conn=%v err=%v", c2, err)
	if c2 != nil {
		c2.Close()
	}

	// 4) 三次失败拨号都没有建立连接：监听器上的 Accept 必须超时而不是接住连接。
	if err := tl.SetDeadline(time.Now().Add(-time.Second)); err != nil {
		ckf(false, "set past accept deadline: %v", err)
	}
	ac, aerr := tl.Accept()
	ckf(ac == nil && errors.Is(aerr, os.ErrDeadlineExceeded), "accept after canceled dials: conn=%v err=%v", ac, aerr)
	if ac != nil {
		ac.Close()
	}
	if err := tl.SetDeadline(time.Time{}); err != nil {
		ckf(false, "clear accept deadline: %v", err)
	}
	gcStress()

	// 5) 正常拨号仍可用：完成一次带指针 payload 的请求/应答。
	client, err := net.Dial("tcp", addr)
	ckf(err == nil, "normal dial: %v", err)
	if err != nil {
		finish("l19_tcp_dial_cancel")
		return
	}
	server, err := tl.Accept()
	ckf(err == nil, "normal accept: %v", err)
	if err != nil {
		client.Close()
		finish("l19_tcp_dial_cancel")
		return
	}

	buf := make([]byte, frameLen)
	putFrame(buf, 1, seed)
	if _, err := client.Write(buf); err != nil {
		ckf(false, "client write: %v", err)
	}
	if _, err := io.ReadFull(server, buf); err != nil {
		ckf(false, "server read: %v", err)
	}
	m, derr := getFrame(buf)
	ckf(derr == nil && messageOK(m, 1, seed), "server decoded payload: err=%v", derr)
	gcStress()
	ckf(messageOK(m, 1, seed), "server payload corrupted after GC")

	putFrame(buf, 2, seed)
	if _, err := server.Write(buf); err != nil {
		ckf(false, "server write: %v", err)
	}
	if _, err := io.ReadFull(client, buf); err != nil {
		ckf(false, "client read: %v", err)
	}
	em, derr := getFrame(buf)
	ckf(derr == nil && messageOK(em, 2, seed), "client decoded payload: err=%v", derr)
	gcStress()
	ckf(messageOK(em, 2, seed), "client payload corrupted after GC")

	client.Close()
	server.Close()
	ln.Close()

	// 6) 监听器关闭后阻塞中的 Accept 必须返回 ErrClosed。
	_, cerr := tl.Accept()
	ckf(errors.Is(cerr, net.ErrClosed), "accept after listener close: %v", cerr)

	gcStress()
	finish("l19_tcp_dial_cancel")
}
