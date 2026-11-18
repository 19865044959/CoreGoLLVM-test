// L19: net.Pipe 双 goroutine 请求/应答，payload 为含指针链的结构；每帧在网络
// 路径上传输后，主协程显式 GC 再校验对端解码出的指针内容，最后验证 Close 能
// 唤醒阻塞的 Read。
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
	wireMagic = 0x4c31394e // "N91L" little-endian
	msgCount  = 64
	waitLimit = 5 * time.Second
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
	return int64(id)*73 + seed*19
}

func makeMessage(id int, seed int64) *message {
	base := msgBase(id, seed)
	return &message{
		id:   id,
		base: base,
		name: fmt.Sprintf("msg-%03d", id),
		head: &node{v: base, next: &node{v: base + 1, next: &node{v: base + 2}}},
	}
}

func messageOK(m *message, id int, seed int64) bool {
	if m == nil || m.id != id || m.name != fmt.Sprintf("msg-%03d", id) {
		return false
	}
	base := msgBase(id, seed)
	if m.base != base {
		return false
	}
	h := m.head
	if h == nil || h.v != base || h.next == nil || h.next.v != base+1 {
		return false
	}
	return h.next.next != nil && h.next.next.v == base+2 && h.next.next.next == nil
}

func putFrame(b []byte, m *message) {
	binary.LittleEndian.PutUint32(b[0:4], wireMagic)
	binary.LittleEndian.PutUint32(b[4:8], uint32(m.id))
	binary.LittleEndian.PutUint64(b[8:16], uint64(m.base))
	copy(b[16:32], m.name)
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

func writer(c net.Conn, seed int64, proceed <-chan struct{}, errCh chan<- error) {
	defer c.Close()
	buf := make([]byte, frameLen)
	for i := 0; i < msgCount; i++ {
		m := makeMessage(i, seed)
		putFrame(buf, m)
		if _, err := c.Write(buf); err != nil {
			errCh <- fmt.Errorf("write %d: %w", i, err)
			return
		}
		select {
		case <-proceed:
		case <-time.After(waitLimit):
			errCh <- errors.New("writer proceed timeout")
			return
		}
		// 主协程已在对端 payload 上执行过 GC；本端消息仍须完好。
		if !messageOK(m, i, seed) {
			errCh <- fmt.Errorf("writer payload %d corrupted", i)
			return
		}
	}
	errCh <- nil
}

func reader(c net.Conn, seed int64, msgCh chan<- *message, release <-chan struct{}, ack chan<- struct{}, errCh chan<- error) {
	defer c.Close()
	buf := make([]byte, frameLen)
	for i := 0; i < msgCount; i++ {
		if _, err := io.ReadFull(c, buf); err != nil {
			errCh <- fmt.Errorf("read %d: %w", i, err)
			return
		}
		m, err := getFrame(buf)
		if err != nil {
			errCh <- fmt.Errorf("decode %d: %w", i, err)
			return
		}
		msgCh <- m
		<-release
		ack <- struct{}{}
	}
	errCh <- nil
}

func main() {
	seed := envSeed()
	c1, c2 := net.Pipe()
	msgCh := make(chan *message)
	release := make(chan struct{})
	ack := make(chan struct{})
	proceed := make(chan struct{})
	rerr := make(chan error, 1)
	werr := make(chan error, 1)

	go reader(c2, seed, msgCh, release, ack, rerr)
	go writer(c1, seed, proceed, werr)

	for i := 0; i < msgCount; i++ {
		var m *message
		select {
		case m = <-msgCh:
		case <-time.After(waitLimit):
			ckf(false, "reader did not deliver frame %d", i)
			i = msgCount // 提前结束等待
			continue
		}
		if m == nil {
			continue
		}
		// 帧已离开 net.Pipe，读端对象只由 reader 栈/主协程引用，GC 后校验。
		gcStress()
		ckf(messageOK(m, i, seed), "reader payload %d corrupted after GC", i)
		release <- struct{}{}
		select {
		case <-ack:
		case <-time.After(waitLimit):
			ckf(false, "ack %d timeout", i)
		}
		select {
		case proceed <- struct{}{}:
		case <-time.After(waitLimit):
			ckf(false, "writer proceed %d timeout", i)
		}
	}

	var rErr, wErr error
	select {
	case rErr = <-rerr:
	case <-time.After(waitLimit):
		rErr = errors.New("reader did not finish")
	}
	select {
	case wErr = <-werr:
	case <-time.After(waitLimit):
		wErr = errors.New("writer did not finish")
	}
	ckf(rErr == nil, "reader error: %v", rErr)
	ckf(wErr == nil, "writer error: %v", wErr)

	// Close 必须唤醒阻塞在 Read 上的对端，而不是永久挂起。
	pa, pb := net.Pipe()
	started := make(chan struct{})
	readErr := make(chan error, 1)
	go func() {
		close(started)
		var b [1]byte
		_, err := pa.Read(b[:])
		readErr <- err
	}()
	<-started
	gcStress()
	pb.Close()
	select {
	case err := <-readErr:
		ckf(err != nil, "Read after peer Close returned nil error")
	case <-time.After(waitLimit):
		ck(false, "Close did not unblock blocked Read")
	}
	ck(pa.Close() == nil, "local Close returned error")

	fmt.Printf("messages=%d\n", msgCount)
	finish("l19_netpipe_payload")
}
