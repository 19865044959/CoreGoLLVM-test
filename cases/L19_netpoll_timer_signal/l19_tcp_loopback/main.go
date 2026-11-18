// L19: 127.0.0.1:0 回环 TCP 并发回显。客户端与服务器都把帧解码成含指针链的
// 消息结构，传输/回显过程中穿插显式 GC，最终按计数与内容校验，不依赖任何
// 耗时阈值（仅用 10s 超时防死锁）。
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
	frameLen      = 48
	wireMagic     = 0x4c313954 // "T91L" little-endian
	clientCount   = 4
	msgsPerClient = 16
	waitLimit     = 10 * time.Second
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
	return int64(id)*67 + seed*29
}

func msgName(id int) string {
	return fmt.Sprintf("echo-%04d", id)
}

func putFrame(b []byte, id int, seed int64) {
	base := msgBase(id, seed)
	binary.LittleEndian.PutUint32(b[0:4], wireMagic)
	binary.LittleEndian.PutUint32(b[4:8], uint32(id))
	binary.LittleEndian.PutUint64(b[8:16], uint64(base))
	copy(b[16:32], msgName(id))
}

func getFrame(b []byte) (*message, error) {
	if binary.LittleEndian.Uint32(b[0:4]) != wireMagic {
		return nil, errors.New("bad magic")
	}
	id := int(binary.LittleEndian.Uint32(b[4:8]))
	base := int64(binary.LittleEndian.Uint64(b[8:16]))
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

func handleConn(c net.Conn, seed int64, wg *sync.WaitGroup, errCh chan<- error, echoed *int32) {
	defer wg.Done()
	defer c.Close()
	buf := make([]byte, frameLen)
	for {
		_, err := io.ReadFull(c, buf)
		if err != nil {
			if errors.Is(err, io.EOF) || errors.Is(err, net.ErrClosed) {
				return
			}
			errCh <- fmt.Errorf("server read: %w", err)
			return
		}
		m, err := getFrame(buf)
		if err != nil {
			errCh <- fmt.Errorf("server decode: %w", err)
			return
		}
		if !messageOK(m, m.id, seed) {
			errCh <- fmt.Errorf("server decoded payload corrupted: id=%d", m.id)
			return
		}
		if m.id%8 == 0 {
			// 回显前 GC：解码出的指针链仍在服务器 goroutine 栈上。
			gcStress()
			if !messageOK(m, m.id, seed) {
				errCh <- fmt.Errorf("server payload corrupted after GC: id=%d", m.id)
				return
			}
		}
		putFrame(buf, m.id, seed)
		if _, err := c.Write(buf); err != nil {
			errCh <- fmt.Errorf("server write: %w", err)
			return
		}
		atomic.AddInt32(echoed, 1)
	}
}

func runClient(addr string, cid int, seed int64, wg *sync.WaitGroup, errCh chan<- error, sum *int64) {
	defer wg.Done()
	c, err := net.Dial("tcp", addr)
	if err != nil {
		errCh <- fmt.Errorf("client %d dial: %w", cid, err)
		return
	}
	defer c.Close()

	buf := make([]byte, frameLen)
	held := make([]*message, 0, msgsPerClient)
	for m := 0; m < msgsPerClient; m++ {
		id := cid*1000 + m
		putFrame(buf, id, seed)
		if _, err := c.Write(buf); err != nil {
			errCh <- fmt.Errorf("client %d write %d: %w", cid, m, err)
			return
		}
		if _, err := io.ReadFull(c, buf); err != nil {
			errCh <- fmt.Errorf("client %d read %d: %w", cid, m, err)
			return
		}
		em, err := getFrame(buf)
		if err != nil {
			errCh <- fmt.Errorf("client %d decode %d: %w", cid, m, err)
			return
		}
		if !messageOK(em, id, seed) {
			errCh <- fmt.Errorf("client %d echo %d corrupted", cid, m)
			return
		}
		held = append(held, em)
		if m%4 == 3 {
			gcStress()
		}
	}
	gcStress()
	for m := 0; m < len(held); m++ {
		if !messageOK(held[m], cid*1000+m, seed) {
			errCh <- fmt.Errorf("client %d held echo %d corrupted after GC", cid, m)
			return
		}
	}
	for m := 0; m < msgsPerClient; m++ {
		atomic.AddInt64(sum, msgBase(cid*1000+m, seed)*3+3)
	}
}

func main() {
	seed := envSeed()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		ckf(false, "listen: %v", err)
		finish("l19_tcp_loopback")
		return
	}
	addr := ln.Addr().String()

	errCh := make(chan error, 256)
	var echoed int32
	var srvWG sync.WaitGroup
	srvDone := make(chan struct{})
	go func() {
		defer close(srvDone)
		for i := 0; i < clientCount; i++ {
			c, err := ln.Accept()
			if err != nil {
				errCh <- fmt.Errorf("accept %d: %w", i, err)
				break
			}
			srvWG.Add(1)
			go handleConn(c, seed, &srvWG, errCh, &echoed)
		}
		ln.Close()
		srvWG.Wait()
	}()

	var cliWG sync.WaitGroup
	var sum int64
	for i := 0; i < clientCount; i++ {
		cliWG.Add(1)
		go runClient(addr, i, seed, &cliWG, errCh, &sum)
	}
	clientsDone := make(chan struct{})
	go func() {
		cliWG.Wait()
		close(clientsDone)
	}()

	select {
	case <-clientsDone:
	case <-time.After(waitLimit):
		ck(false, "clients did not finish")
	}
	select {
	case <-srvDone:
	case <-time.After(waitLimit):
		ck(false, "server did not finish")
	}

	for {
		select {
		case err := <-errCh:
			ckf(false, "%v", err)
		default:
			goto drained
		}
	}
drained:

	var wantSum int64
	for cid := 0; cid < clientCount; cid++ {
		for m := 0; m < msgsPerClient; m++ {
			wantSum += msgBase(cid*1000+m, seed)*3 + 3
		}
	}
	total := int32(clientCount * msgsPerClient)
	ckf(atomic.LoadInt32(&echoed) == total, "echoed=%d want=%d", atomic.LoadInt32(&echoed), total)
	ckf(sum == wantSum, "client sum=%d want=%d", sum, wantSum)

	fmt.Printf("clients=%d messages=%d\n", clientCount, clientCount*msgsPerClient)
	finish("l19_tcp_loopback")
}
