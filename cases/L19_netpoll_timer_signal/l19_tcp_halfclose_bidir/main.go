// L19: 回环 TCP 上的双向同时半关闭。客户端与服务端各自先写满固定帧数，
// 随后双方在未等待对端的情况下各自 CloseWrite（两条 FIN 交错在途），
// 再分别读取对端数据直到 EOF。之后本地写方向必须返回错误，EOF 粘滞。
// 帧解码为含指针链的消息，在写入后、半关闭后与 GC 后逐段校验。
// 全程固定帧数与 deadline 防死锁，不做耗时阈值断言。
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
	wireMagic = 0x4c394842 // "BH9L" little-endian
	frames    = 16
	srvIDBase = 200
	cliIDBase = 100
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
	return int64(id)*73 + seed*37
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
	binary.LittleEndian.PutUint64(b[32:40], uint64(base*5+int64(id)))
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
	if binary.LittleEndian.Uint64(b[32:40]) != uint64(base*5+int64(id)) {
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

// server 写完自己的帧后立刻 CloseWrite，不等客户端；随后读到对端 FIN 为止。
func serve(ln net.Listener, seed int64, done chan<- error) {
	c, err := ln.Accept()
	if err != nil {
		done <- fmt.Errorf("accept: %w", err)
		return
	}
	tc, ok := c.(*net.TCPConn)
	if !ok {
		c.Close()
		done <- errors.New("accepted conn is not *net.TCPConn")
		return
	}
	defer tc.Close()
	if err := tc.SetDeadline(time.Now().Add(waitLimit)); err != nil {
		done <- fmt.Errorf("server set deadline: %w", err)
		return
	}

	buf := make([]byte, frameLen)
	own := make([]*message, 0, frames)
	for i := 0; i < frames; i++ {
		id := srvIDBase + i
		putFrame(buf, id, seed)
		if _, err := tc.Write(buf); err != nil {
			done <- fmt.Errorf("server write %d: %w", i, err)
			return
		}
		m, err := getFrame(buf, seed)
		if err != nil || !messageOK(m, id, seed) {
			done <- fmt.Errorf("server own frame %d corrupted: %v", i, err)
			return
		}
		own = append(own, m)
	}
	gcStress()
	for i, m := range own {
		if !messageOK(m, srvIDBase+i, seed) {
			done <- fmt.Errorf("server own frame %d corrupted after GC", i)
			return
		}
	}

	// 双向同时半关闭：本端不等待客户端先关。
	if err := tc.CloseWrite(); err != nil {
		done <- fmt.Errorf("server CloseWrite: %w", err)
		return
	}

	peer := make([]*message, 0, frames)
	for i := 0; i < frames; i++ {
		if _, err := io.ReadFull(tc, buf); err != nil {
			done <- fmt.Errorf("server read peer %d: %w", i, err)
			return
		}
		id := cliIDBase + i
		m, err := getFrame(buf, seed)
		if err != nil || !messageOK(m, id, seed) {
			done <- fmt.Errorf("server peer frame %d corrupted: %v", i, err)
			return
		}
		peer = append(peer, m)
		if i == frames/2-1 {
			gcStress()
			for k, pm := range peer {
				if !messageOK(pm, cliIDBase+k, seed) {
					done <- fmt.Errorf("server peer frame %d corrupted during GC", k)
					return
				}
			}
		}
	}
	n, rerr := tc.Read(buf)
	if n != 0 || !errors.Is(rerr, io.EOF) {
		done <- fmt.Errorf("server final read: n=%d err=%v, want EOF", n, rerr)
		return
	}
	if n, rerr := tc.Read(buf); n != 0 || !errors.Is(rerr, io.EOF) {
		done <- fmt.Errorf("server EOF not sticky: n=%d err=%v", n, rerr)
		return
	}
	putFrame(buf, srvIDBase, seed)
	if _, werr := tc.Write(buf); werr == nil {
		done <- errors.New("server write after local CloseWrite succeeded")
		return
	}
	gcStress()
	for i, pm := range peer {
		if !messageOK(pm, cliIDBase+i, seed) {
			done <- fmt.Errorf("server peer frame %d corrupted after GC", i)
			return
		}
	}
	done <- nil
}

func main() {
	seed := envSeed()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		ckf(false, "listen: %v", err)
		finish("l19_tcp_halfclose_bidir")
		return
	}
	defer ln.Close()

	srvDone := make(chan error, 1)
	go serve(ln, seed, srvDone)

	c, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		ckf(false, "dial: %v", err)
		finish("l19_tcp_halfclose_bidir")
		return
	}
	tc, ok := c.(*net.TCPConn)
	if !ok {
		ck(false, "dialed conn is not *net.TCPConn")
		c.Close()
		finish("l19_tcp_halfclose_bidir")
		return
	}
	defer tc.Close()
	if err := tc.SetDeadline(time.Now().Add(waitLimit)); err != nil {
		ckf(false, "client set deadline: %v", err)
	}

	buf := make([]byte, frameLen)
	own := make([]*message, 0, frames)
	for i := 0; i < frames; i++ {
		id := cliIDBase + i
		putFrame(buf, id, seed)
		if _, err := tc.Write(buf); err != nil {
			ckf(false, "client write %d: %v", i, err)
			break
		}
		m, err := getFrame(buf, seed)
		if err != nil || !messageOK(m, id, seed) {
			ckf(false, "client own frame %d corrupted: %v", i, err)
			break
		}
		own = append(own, m)
	}
	gcStress()

	// 不等服务端 FIN，直接半关闭本端写方向。
	if err := tc.CloseWrite(); err != nil {
		ckf(false, "client CloseWrite: %v", err)
	}

	peer := make([]*message, 0, frames)
	for i := 0; i < frames; i++ {
		if _, err := io.ReadFull(tc, buf); err != nil {
			ckf(false, "client read peer %d: %v", i, err)
			break
		}
		id := srvIDBase + i
		m, err := getFrame(buf, seed)
		if err != nil || !messageOK(m, id, seed) {
			ckf(false, "client peer frame %d corrupted: %v", i, err)
			break
		}
		peer = append(peer, m)
		if i == frames/2-1 {
			gcStress()
			for k, pm := range peer {
				if !messageOK(pm, srvIDBase+k, seed) {
					ckf(false, "client peer frame %d corrupted during GC", k)
				}
			}
		}
	}
	n, rerr := tc.Read(buf)
	ckf(n == 0 && errors.Is(rerr, io.EOF), "client final read: n=%d err=%v, want EOF", n, rerr)
	if n, rerr := tc.Read(buf); n != 0 || !errors.Is(rerr, io.EOF) {
		ckf(false, "client EOF not sticky: n=%d err=%v", n, rerr)
	}
	putFrame(buf, cliIDBase, seed)
	if _, werr := tc.Write(buf); werr == nil {
		ck(false, "client write after local CloseWrite succeeded")
	}

	select {
	case err := <-srvDone:
		if err != nil {
			ckf(false, "server: %v", err)
		}
	case <-time.After(waitLimit):
		ck(false, "server goroutine did not finish")
	}

	gcStress()
	for i, m := range own {
		if !messageOK(m, cliIDBase+i, seed) {
			ckf(false, "client own frame %d corrupted after GC", i)
		}
	}
	for i, m := range peer {
		if !messageOK(m, srvIDBase+i, seed) {
			ckf(false, "client peer frame %d corrupted after GC", i)
		}
	}
	fmt.Printf("frames_per_dir=%d\n", frames)
	finish("l19_tcp_halfclose_bidir")
}
