// L19: net.Pipe 上的并发写者。4 个 goroutine 各自写入 12 个 48 字节帧，读端
// 按帧全量读取并校验每个写者的序号严格递增、无丢失/重复。读端在中途停顿执行
// 显式 GC，此时写者全部阻塞在 net.Pipe 的 Write（netpoll 阻塞路径）上；恢复后
// 全部帧必须按序到达。TORTURE_ROUNDS 控制轮数，默认 1 轮。
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
	writer int
	seq    int
	base   int64
	name   string
	head   *node
}

var failed int32

const (
	frameLen  = 48
	wireMagic = 0x4c313950 // "P91L" little-endian
	writers   = 4
	frames    = 12
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

func envInt(name string, def, lo, hi int) int {
	s := os.Getenv(name)
	if s == "" {
		return def
	}
	n, err := strconv.Atoi(s)
	if err != nil || n < lo {
		return def
	}
	if n > hi {
		n = hi
	}
	return n
}

func msgBase(w, seq int, seed int64) int64 {
	return int64(w*1000+seq)*59 + seed*43
}

func msgName(w, seq int) string {
	return fmt.Sprintf("w%d-%03d", w, seq)
}

func putFrame(b []byte, w, seq int, seed int64) {
	base := msgBase(w, seq, seed)
	binary.LittleEndian.PutUint32(b[0:4], wireMagic)
	binary.LittleEndian.PutUint32(b[4:8], uint32(w))
	binary.LittleEndian.PutUint32(b[8:12], uint32(seq))
	binary.LittleEndian.PutUint32(b[12:16], 0x5a5a5a5a)
	binary.LittleEndian.PutUint64(b[16:24], uint64(base))
	copy(b[24:40], msgName(w, seq))
	binary.LittleEndian.PutUint64(b[40:48], uint64(base*7+int64(w*100+seq)))
}

func getFrame(b []byte, seed int64) (*message, error) {
	if binary.LittleEndian.Uint32(b[0:4]) != wireMagic {
		return nil, errors.New("bad magic")
	}
	w := int(binary.LittleEndian.Uint32(b[4:8]))
	seq := int(binary.LittleEndian.Uint32(b[8:12]))
	base := int64(binary.LittleEndian.Uint64(b[16:24]))
	if binary.LittleEndian.Uint32(b[12:16]) != 0x5a5a5a5a {
		return nil, errors.New("bad pad")
	}
	if base != msgBase(w, seq, seed) {
		return nil, errors.New("bad base")
	}
	if binary.LittleEndian.Uint64(b[40:48]) != uint64(base*7+int64(w*100+seq)) {
		return nil, errors.New("bad checksum")
	}
	return &message{
		writer: w,
		seq:    seq,
		base:   base,
		name:   string(bytes.TrimRight(b[24:40], "\x00")),
		head:   &node{v: base, next: &node{v: base + 1, next: &node{v: base + 2}}},
	}, nil
}

func chainOK(m *message) bool {
	h := m.head
	base := m.base
	if h == nil || h.v != base || h.next == nil || h.next.v != base+1 {
		return false
	}
	return h.next.next != nil && h.next.next.v == base+2 && h.next.next.next == nil
}

func round(seed int64) {
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()
	if err := server.SetReadDeadline(time.Now().Add(waitLimit)); err != nil {
		ckf(false, "set pipe read deadline: %v", err)
		return
	}

	errCh := make(chan error, writers)
	var wg sync.WaitGroup
	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			buf := make([]byte, frameLen)
			var payload []*node
			for seq := 0; seq < frames; seq++ {
				base := msgBase(w, seq, seed)
				payload = append(payload, &node{v: base, next: &node{v: base + 1, next: &node{v: base + 2}}})
				putFrame(buf, w, seq, seed)
				if _, err := client.Write(buf); err != nil {
					errCh <- fmt.Errorf("writer %d seq %d: %w", w, seq, err)
					return
				}
				if seq%4 == 3 {
					gcStress()
					for k, h := range payload {
						if h == nil || h.v != msgBase(w, k, seed) || h.next == nil || h.next.v != msgBase(w, k, seed)+1 ||
							h.next.next == nil || h.next.next.v != msgBase(w, k, seed)+2 {
							errCh <- fmt.Errorf("writer %d payload %d corrupted after GC", w, k)
							return
						}
					}
				}
			}
			gcStress()
		}(w)
	}

	next := make([]int, writers)
	held := make([]*message, 0, writers*frames)
	var readErr error
	for i := 0; i < writers*frames; i++ {
		buf := make([]byte, frameLen)
		if _, err := io.ReadFull(server, buf); err != nil {
			readErr = err
			break
		}
		m, err := getFrame(buf, seed)
		if err != nil {
			readErr = fmt.Errorf("decode frame %d: %w", i, err)
			break
		}
		if m.writer < 0 || m.writer >= writers {
			readErr = fmt.Errorf("frame %d writer=%d out of range", i, m.writer)
			break
		}
		if m.seq != next[m.writer] {
			readErr = fmt.Errorf("writer %d seq=%d want=%d", m.writer, m.seq, next[m.writer])
			break
		}
		if m.name != msgName(m.writer, m.seq) || !chainOK(m) {
			readErr = fmt.Errorf("writer %d seq=%d payload mismatch", m.writer, m.seq)
			break
		}
		next[m.writer]++
		held = append(held, m)

		// 读端在第 24 帧后停顿：所有写者都阻塞在 net.Pipe 的 Write 上。
		if i == writers*frames/2-1 {
			gcStress()
			for _, hm := range held {
				if !chainOK(hm) {
					readErr = fmt.Errorf("held frame writer %d seq %d corrupted during pause", hm.writer, hm.seq)
					break
				}
			}
		}
	}
	if readErr != nil {
		// 读端提前退出时必须关闭连接，唤醒仍阻塞在 Write 上的写者。
		client.Close()
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		if err != nil {
			ckf(false, "round: %v", err)
		}
	}
	if readErr != nil {
		ckf(false, "round read: %v", readErr)
	}
	ckf(len(held) == writers*frames, "held=%d want=%d", len(held), writers*frames)
	for w := 0; w < writers; w++ {
		ckf(next[w] == frames, "writer %d frames=%d want=%d", w, next[w], frames)
	}
	gcStress()
	for _, m := range held {
		if !chainOK(m) {
			ckf(false, "held frame writer %d seq %d corrupted after final GC", m.writer, m.seq)
			break
		}
	}
}

func main() {
	seed := envSeed()
	rounds := envInt("TORTURE_ROUNDS", 1, 1, 50)
	fmt.Printf("writers=%d frames=%d rounds=%d\n", writers, frames, rounds)
	for r := 0; r < rounds; r++ {
		round(seed + int64(r)*101)
	}
	finish("l19_netpipe_multi_writer")
}
