// L19: net.Pipe 多写者 + 分片读边界。4 个写者各并发写 8 条 1KiB 帧；读端只按
// 37 字节的小块读取并重组帧，因此每个 Write 都跨越多次 Read，写者持续阻塞在
// net.Pipe 的同步点。读端在中点停顿并显式 GC，此时未完成写者都在 Write 上。
// 校验每个写者序号严格递增、无串帧/丢失，帧内指针链在 GC 后完好。
// TORTURE_ROUNDS 放大轮数。
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
	frameLen  = 1024
	chunkLen  = 37
	wireMagic = 0x4c394d50 // "PM9L" little-endian
	writers   = 4
	frames    = 8
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
	return int64(w*1000+seq)*61 + seed*47
}

func msgName(w, seq int) string {
	return fmt.Sprintf("pw%d-%03d", w, seq)
}

func patternByte(w, seq, i int) byte {
	return byte((w*131 + seq*17 + i*7 + 3) & 0xff)
}

func putFrame(b []byte, w, seq int, seed int64) {
	base := msgBase(w, seq, seed)
	binary.LittleEndian.PutUint32(b[0:4], wireMagic)
	binary.LittleEndian.PutUint32(b[4:8], uint32(w))
	binary.LittleEndian.PutUint32(b[8:12], uint32(seq))
	binary.LittleEndian.PutUint64(b[16:24], uint64(base))
	copy(b[24:40], msgName(w, seq))
	binary.LittleEndian.PutUint64(b[40:48], uint64(base*7+int64(w*100+seq)))
	for i := 48; i < frameLen; i++ {
		b[i] = patternByte(w, seq, i)
	}
}

func getFrame(b []byte, seed int64) (*message, error) {
	if binary.LittleEndian.Uint32(b[0:4]) != wireMagic {
		return nil, errors.New("bad magic")
	}
	w := int(binary.LittleEndian.Uint32(b[4:8]))
	seq := int(binary.LittleEndian.Uint32(b[8:12]))
	base := int64(binary.LittleEndian.Uint64(b[16:24]))
	if w < 0 || w >= writers || seq < 0 || seq >= frames {
		return nil, errors.New("bad writer/seq")
	}
	if base != msgBase(w, seq, seed) {
		return nil, errors.New("bad base")
	}
	if binary.LittleEndian.Uint64(b[40:48]) != uint64(base*7+int64(w*100+seq)) {
		return nil, errors.New("bad checksum")
	}
	for i := 48; i < frameLen; i++ {
		if b[i] != patternByte(w, seq, i) {
			return nil, fmt.Errorf("bad pattern at %d", i)
		}
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
	if m == nil {
		return false
	}
	base := m.base
	h := m.head
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
			chains := make([]*node, 0, frames)
			for seq := 0; seq < frames; seq++ {
				base := msgBase(w, seq, seed)
				chains = append(chains, &node{v: base, next: &node{v: base + 1, next: &node{v: base + 2}}})
				putFrame(buf, w, seq, seed)
				n, err := client.Write(buf)
				if err != nil || n != frameLen {
					errCh <- fmt.Errorf("writer %d seq %d: n=%d err=%v", w, seq, n, err)
					return
				}
				if seq%3 == 2 {
					gcStress()
					for k, h := range chains {
						b := msgBase(w, k, seed)
						if h == nil || h.v != b || h.next == nil || h.next.v != b+1 || h.next.next == nil || h.next.next.v != b+2 {
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
	for len(held) < writers*frames {
		frame := make([]byte, frameLen)
		for off := 0; off < frameLen; {
			n := chunkLen
			if frameLen-off < n {
				n = frameLen - off
			}
			if _, err := io.ReadFull(server, frame[off:off+n]); err != nil {
				readErr = fmt.Errorf("read frame %d at off %d: %w", len(held), off, err)
				break
			}
			off += n
		}
		if readErr != nil {
			break
		}
		m, err := getFrame(frame, seed)
		if err != nil {
			readErr = fmt.Errorf("decode frame %d: %w", len(held), err)
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

		// 中点停顿：写者尚未完成的 Write 全部阻塞在 net.Pipe 上。
		if len(held) == writers*frames/2 {
			gcStress()
			for _, hm := range held {
				if !chainOK(hm) {
					readErr = fmt.Errorf("held frame writer %d seq %d corrupted during pause", hm.writer, hm.seq)
					break
				}
			}
			if readErr != nil {
				break
			}
		}
		if len(held)%8 == 0 {
			gcStress()
		}
	}
	if readErr != nil {
		// 唤醒仍阻塞在 Write 上的写者，避免 wg.Wait 死锁。
		client.Close()
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		ckf(false, "round: %v", err)
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
	rounds := envInt("TORTURE_ROUNDS", 2, 1, 20)
	fmt.Printf("writers=%d frames=%d frame_len=%d chunk=%d rounds=%d\n", writers, frames, frameLen, chunkLen, rounds)
	for r := 0; r < rounds; r++ {
		round(seed + int64(r)*107)
	}
	finish("l19_netpipe_multi_writer_partial")
}
