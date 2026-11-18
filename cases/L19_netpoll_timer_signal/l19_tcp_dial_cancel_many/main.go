// L19: TCP 拨号取消的批量边界。320 次已取消 ctx / 已过期 deadline 的拨号必须
// 全部快速失败，且不得在监听器上留下半开连接；随后一次真实拨号/接受必须拿到
// 自己新建的连接（若前面泄漏了连接，accept 会先取到旧连接导致握手失败）。
// 失败路径之间穿插显式 GC，真实连接帧携带指针链并在 GC 后校验。
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
	frameLen   = 48
	wireMagic  = 0x4c394443 // "CD9L" little-endian
	seqDials   = 256
	concDials  = 64
	deadlineOn = 64
	waitLimit  = 10 * time.Second
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
	return fmt.Sprintf("dial-%04d", id)
}

func putFrame(b []byte, id int, seed int64) {
	base := msgBase(id, seed)
	binary.LittleEndian.PutUint32(b[0:4], wireMagic)
	binary.LittleEndian.PutUint32(b[4:8], uint32(id))
	binary.LittleEndian.PutUint64(b[8:16], uint64(base))
	copy(b[16:32], msgName(id))
	binary.LittleEndian.PutUint64(b[32:40], uint64(base*3+int64(id)))
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
	if binary.LittleEndian.Uint64(b[32:40]) != uint64(base*3+int64(id)) {
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
		finish("l19_tcp_dial_cancel_many")
		return
	}
	defer ln.Close()
	addr := ln.Addr().String()
	tl := ln.(*net.TCPListener)
	dialer := &net.Dialer{}

	// 1) 已取消 ctx 的顺序拨号：全部立即 Canceled，无连接对象。
	canceledCtx, cancel := context.WithCancel(context.Background())
	cancel()
	canceled := 0
	for i := 0; i < seqDials; i++ {
		c, err := dialer.DialContext(canceledCtx, "tcp", addr)
		if c != nil {
			c.Close()
			ckf(false, "seq canceled dial %d returned a conn", i)
		}
		if errors.Is(err, context.Canceled) {
			canceled++
		} else {
			ckf(false, "seq canceled dial %d err=%v, want context.Canceled", i, err)
		}
	}
	ckf(canceled == seqDials, "canceled=%d want=%d", canceled, seqDials)

	// 2) 已过期 deadline 的拨号：全部 DeadlineExceeded。
	expired := 0
	for i := 0; i < deadlineOn; i++ {
		dctx, dcancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
		c, err := dialer.DialContext(dctx, "tcp", addr)
		if c != nil {
			c.Close()
			ckf(false, "expired-deadline dial %d returned a conn", i)
		}
		if errors.Is(err, context.DeadlineExceeded) {
			expired++
		} else {
			ckf(false, "expired-deadline dial %d err=%v, want context.DeadlineExceeded", i, err)
		}
		dcancel()
	}
	ckf(expired == deadlineOn, "expired=%d want=%d", expired, deadlineOn)
	gcStress()

	// 3) 并发取消拨号：所有 goroutine 都必须拿回 Canceled。
	var wg sync.WaitGroup
	var badConc int32
	for i := 0; i < concDials; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			c, err := dialer.DialContext(canceledCtx, "tcp", addr)
			if c != nil {
				c.Close()
				atomic.AddInt32(&badConc, 1)
				return
			}
			if !errors.Is(err, context.Canceled) {
				atomic.AddInt32(&badConc, 1)
				ckf(false, "concurrent canceled dial %d err=%v", i, err)
			}
		}(i)
	}
	wg.Wait()
	ckf(atomic.LoadInt32(&badConc) == 0, "concurrent canceled dials bad=%d", atomic.LoadInt32(&badConc))
	gcStress()

	// 4) 监听器 deadline 语义：已过期 Accept 立即 timeout。
	if err := tl.SetDeadline(time.Now().Add(-time.Second)); err != nil {
		ckf(false, "set past accept deadline: %v", err)
	}
	c, aerr := tl.Accept()
	if c != nil {
		c.Close()
		ckf(false, "past-deadline Accept returned a conn")
	}
	ckf(aerr != nil && errors.Is(aerr, os.ErrDeadlineExceeded), "past-deadline Accept err=%v", aerr)

	// 5) 健康路径：清空 deadline 后真实拨号必须握手成功；若有取消拨号泄漏成
	// 已排队连接，这里会先 accept 到旧连接并以帧校验失败暴露。
	if err := tl.SetDeadline(time.Now().Add(waitLimit)); err != nil {
		ckf(false, "clear accept deadline: %v", err)
	}
	srvCh := make(chan error, 1)
	go func() {
		c, err := tl.Accept()
		if err != nil {
			srvCh <- fmt.Errorf("healthy accept: %w", err)
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
		if err != nil || !messageOK(m, 7, seed) {
			srvCh <- fmt.Errorf("server payload corrupted: %v", err)
			return
		}
		gcStress()
		if !messageOK(m, 7, seed) {
			srvCh <- errors.New("server payload corrupted after GC")
			return
		}
		putFrame(buf, 8, seed)
		if _, err := tc.Write(buf); err != nil {
			srvCh <- fmt.Errorf("server ack write: %w", err)
			return
		}
		srvCh <- nil
	}()

	cl, err := net.Dial("tcp", addr)
	if err != nil {
		ckf(false, "healthy dial: %v", err)
		finish("l19_tcp_dial_cancel_many")
		return
	}
	ctc := cl.(*net.TCPConn)
	if err := ctc.SetDeadline(time.Now().Add(waitLimit)); err != nil {
		ckf(false, "client set deadline: %v", err)
	}
	buf := make([]byte, frameLen)
	putFrame(buf, 7, seed)
	if _, err := ctc.Write(buf); err != nil {
		ckf(false, "client write: %v", err)
	}
	gcStress()
	if _, err := io.ReadFull(ctc, buf); err != nil {
		ckf(false, "client read ack: %v", err)
	} else {
		m, err := getFrame(buf, seed)
		if err != nil || !messageOK(m, 8, seed) {
			ckf(false, "client ack corrupted: %v", err)
		}
	}
	ctc.Close()
	select {
	case err := <-srvCh:
		if err != nil {
			ckf(false, "server: %v", err)
		}
	case <-time.After(waitLimit):
		ck(false, "healthy server goroutine did not finish")
	}

	gcStress()
	fmt.Printf("seq=%d deadline=%d concurrent=%d\n", seqDials, deadlineOn, concDials)
	finish("l19_tcp_dial_cancel_many")
}
