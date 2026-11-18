// L19: 回环 TCP 上的真实半关闭。服务端读完正向帧后 CloseWrite（发 FIN），
// 客户端读到 EOF 后仍可继续写入 ACK（本端写方向保持开放），服务端在
// CloseWrite 之后仍能读取 ACK，最后读到 EOF。帧解码为含指针链的消息，
// 传输与半关闭前后穿插显式 GC。全程只用 channel/顺序 I/O 协调，不依赖耗时阈值。
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

var failed int32

const (
	frameLen  = 48
	wireMagic = 0x4c313948 // "H91L" little-endian
	fwdFrames = 24
	ackFrames = 4
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
	return int64(id)*71 + seed*31
}

func msgName(id int) string {
	return fmt.Sprintf("half-%04d", id)
}

func putFrame(b []byte, id int, seed int64) {
	base := msgBase(id, seed)
	binary.LittleEndian.PutUint32(b[0:4], wireMagic)
	binary.LittleEndian.PutUint32(b[4:8], uint32(id))
	binary.LittleEndian.PutUint64(b[8:16], uint64(base))
	copy(b[16:32], msgName(id))
	binary.LittleEndian.PutUint64(b[32:40], uint64(base*3+int64(id)))
}

func getFrame(b []byte) (*message, error) {
	if binary.LittleEndian.Uint32(b[0:4]) != wireMagic {
		return nil, errors.New("bad magic")
	}
	id := int(binary.LittleEndian.Uint32(b[4:8]))
	base := int64(binary.LittleEndian.Uint64(b[8:16]))
	wantSum := uint64(base*3 + int64(id))
	if binary.LittleEndian.Uint64(b[32:40]) != wantSum {
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
		finish("l19_tcp_halfclose")
		return
	}
	addr := ln.Addr().String()
	defer ln.Close()

	ready := make(chan struct{})
	srvDone := make(chan struct{})
	go func() {
		defer close(srvDone)
		c, err := ln.Accept()
		if err != nil {
			ckf(false, "accept: %v", err)
			return
		}
		defer c.Close()
		tc, ok := c.(*net.TCPConn)
		if !ok {
			ck(false, "accepted conn is not *net.TCPConn")
			return
		}
		buf := make([]byte, frameLen)
		held := make([]*message, 0, fwdFrames)
		for i := 0; i < fwdFrames; i++ {
			if _, err := io.ReadFull(tc, buf); err != nil {
				ckf(false, "server read forward %d: %v", i, err)
				return
			}
			m, err := getFrame(buf)
			if err != nil {
				ckf(false, "server decode forward %d: %v", i, err)
				return
			}
			if !messageOK(m, i, seed) {
				ckf(false, "server forward payload corrupted at %d", i)
				return
			}
			held = append(held, m)
			if i == 7 {
				gcStress()
			}
		}
		gcStress()
		for i := range held {
			if !messageOK(held[i], i, seed) {
				ckf(false, "server held forward %d corrupted after GC", i)
				return
			}
		}
		close(ready)

		// 半关闭：只关闭写方向，读方向必须继续可用。
		if err := tc.CloseWrite(); err != nil {
			ckf(false, "server CloseWrite: %v", err)
			return
		}
		for i := 0; i < ackFrames; i++ {
			if _, err := io.ReadFull(tc, buf); err != nil {
				ckf(false, "server read ack %d after CloseWrite: %v", i, err)
				return
			}
			id := 5000 + i
			m, err := getFrame(buf)
			if err != nil {
				ckf(false, "server decode ack %d: %v", i, err)
				return
			}
			if !messageOK(m, id, seed) {
				ckf(false, "server ack payload corrupted at %d", i)
			}
		}
		if n, err := tc.Read(buf); n != 0 || !errors.Is(err, io.EOF) {
			ckf(false, "server final read: n=%d err=%v, want EOF", n, err)
		}
	}()

	c, err := net.Dial("tcp", addr)
	if err != nil {
		ckf(false, "dial: %v", err)
		finish("l19_tcp_halfclose")
		return
	}
	tc, ok := c.(*net.TCPConn)
	if !ok {
		ck(false, "dialed conn is not *net.TCPConn")
		finish("l19_tcp_halfclose")
		return
	}
	buf := make([]byte, frameLen)
	for i := 0; i < fwdFrames; i++ {
		putFrame(buf, i, seed)
		if _, err := tc.Write(buf); err != nil {
			ckf(false, "client write forward %d: %v", i, err)
			break
		}
	}
	<-ready
	gcStress()

	// 服务端只发送 FIN，没有数据帧；客户端必须先看到 EOF。
	n, rerr := tc.Read(buf)
	ckf(n == 0 && errors.Is(rerr, io.EOF), "client read after server CloseWrite: n=%d err=%v, want EOF", n, rerr)
	gcStress()

	// 收到 FIN 只关闭了服务端写方向，本端写方向仍然开放。
	for i := 0; i < ackFrames; i++ {
		id := 5000 + i
		putFrame(buf, id, seed)
		if _, err := tc.Write(buf); err != nil {
			ckf(false, "client write ack %d after peer FIN: %v", i, err)
			break
		}
	}
	ckf(errors.Is(rerr, io.EOF), "EOF must be sticky")

	if err := tc.CloseWrite(); err != nil {
		ckf(false, "client CloseWrite: %v", err)
	}
	if _, err := tc.Write(buf); err == nil {
		ck(false, "write after local CloseWrite must fail")
	}
	tc.Close()

	select {
	case <-srvDone:
	case <-time.After(waitLimit):
		ck(false, "server goroutine did not finish")
	}
	gcStress()
	finish("l19_tcp_halfclose")
}
