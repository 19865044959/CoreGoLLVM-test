// L19: os.Pipe 上的阻塞读写（netpoll/文件轮询路径）。reader 先阻塞在 Read 上，
// 主协程显式 GC 后再由 writer 写入含指针的帧；reader 解码后再次 GC 并校验
// 全部 payload，最后验证写端 Close 后读到 EOF。
package main

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
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
	wireMagic = 0x4c31394f // "O91L" little-endian
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
	return int64(id)*61 + seed*23
}

func putFrame(b []byte, id int, seed int64) string {
	base := msgBase(id, seed)
	name := fmt.Sprintf("poll-%03d", id)
	binary.LittleEndian.PutUint32(b[0:4], wireMagic)
	binary.LittleEndian.PutUint32(b[4:8], uint32(id))
	binary.LittleEndian.PutUint64(b[8:16], uint64(base))
	copy(b[16:32], name)
	return name
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
	if m == nil || m.id != id || m.name != fmt.Sprintf("poll-%03d", id) || m.base != msgBase(id, seed) {
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
	r, w, err := os.Pipe()
	if err != nil {
		ckf(false, "os.Pipe: %v", err)
		finish("l19_os_pipe_poll")
		return
	}
	defer r.Close()
	defer w.Close()

	ready := make(chan struct{})
	readDone := make(chan error, 1)
	writeDone := make(chan error, 1)
	var msgs []*message

	go func() {
		close(ready)
		buf := make([]byte, frameLen)
		for i := 0; i < msgCount; i++ {
			if _, err := io.ReadFull(r, buf); err != nil {
				readDone <- fmt.Errorf("read %d: %w", i, err)
				return
			}
			m, err := getFrame(buf)
			if err != nil {
				readDone <- fmt.Errorf("decode %d: %w", i, err)
				return
			}
			msgs = append(msgs, m)
			if i%16 == 15 {
				// 在持有已解码指针对象的读 goroutine 上显式 GC。
				gcStress()
			}
		}
		readDone <- nil
	}()

	// 等 reader 就位后先 GC：此时读端阻塞在空管道上（netpoll 等待）。
	<-ready
	gcStress()

	go func() {
		defer w.Close()
		buf := make([]byte, frameLen)
		for i := 0; i < msgCount; i++ {
			putFrame(buf, i, seed)
			if _, err := w.Write(buf); err != nil {
				writeDone <- fmt.Errorf("write %d: %w", i, err)
				return
			}
		}
		writeDone <- nil
	}()

	select {
	case err := <-readDone:
		ckf(err == nil, "reader error: %v", err)
	case <-time.After(waitLimit):
		ck(false, "os.Pipe reader timeout")
	}
	select {
	case err := <-writeDone:
		ckf(err == nil, "writer error: %v", err)
	case <-time.After(waitLimit):
		ck(false, "os.Pipe writer timeout")
	}

	ckf(len(msgs) == msgCount, "decoded %d messages want %d", len(msgs), msgCount)

	// 全部数据落盘后再 GC，逐个校验指针内容。
	gcStress()
	for i := 0; i < len(msgs); i++ {
		ckf(messageOK(msgs[i], i, seed), "message %d corrupted after GC", i)
	}

	// 写端已 Close：再读应得到 EOF，而不是挂起。
	w.Close()
	buf := make([]byte, 1)
	_, err = r.Read(buf)
	ckf(err == io.EOF, "read after writer Close: err=%v want EOF", err)

	fmt.Printf("frames=%d\n", msgCount)
	finish("l19_os_pipe_poll")
}
