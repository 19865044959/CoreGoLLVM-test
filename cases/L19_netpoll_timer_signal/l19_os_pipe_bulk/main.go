// L19: os.Pipe 大数据传输。每轮把 256 个 4128 字节的块（约 1MiB）写入
// os.Pipe，读端逐块校验 magic/索引/pattern，并在首块后阻塞以让写端填满
// 内核管道，主协程此时显式 GC。读端持有指针链跨多次 GC 校验。
package main

import (
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

var failed int32

const (
	bulkHeader    = 32
	bulkChunkSize = 4096
	bulkFrameSize = bulkHeader + bulkChunkSize
	bulkMagic     = 0x50495045 // "PIPE"
	waitLimit     = 5 * time.Second
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

func envRounds(def int) int {
	s := os.Getenv("TORTURE_ROUNDS")
	if s == "" {
		return def
	}
	v, err := strconv.Atoi(s)
	if err != nil || v < 1 {
		return def
	}
	if v > 20 {
		v = 20
	}
	return v
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

func patternByte(chunk, off int, seed int64) byte {
	return byte((int64(chunk)*31 + int64(off)*7 + seed) & 0xff)
}

func bulkWriter(w *os.File, chunks int, seed, base int64, res chan<- error) {
	buf := make([]byte, bulkFrameSize)
	for c := 0; c < chunks; c++ {
		binary.LittleEndian.PutUint32(buf[0:4], bulkMagic)
		binary.LittleEndian.PutUint32(buf[4:8], uint32(c))
		binary.LittleEndian.PutUint32(buf[8:12], uint32(chunks))
		binary.LittleEndian.PutUint32(buf[12:16], 0)
		binary.LittleEndian.PutUint64(buf[16:24], uint64(base))
		binary.LittleEndian.PutUint64(buf[24:32], uint64(seed))
		for i := 0; i < bulkChunkSize; i++ {
			buf[bulkHeader+i] = patternByte(c, i, seed)
		}
		if _, err := w.Write(buf); err != nil {
			res <- err
			return
		}
	}
	res <- w.Close()
}

type bulkResult struct {
	chunks int
	bad    int
	err    error
}

func bulkReader(r *os.File, chunks int, seed, base int64, first chan<- struct{}, release <-chan struct{}) bulkResult {
	buf := make([]byte, bulkFrameSize)
	holder := makeChain(base + 77)
	bad := 0
	for c := 0; c < chunks; c++ {
		if _, err := io.ReadFull(r, buf); err != nil {
			return bulkResult{chunks: c, bad: bad, err: err}
		}
		if binary.LittleEndian.Uint32(buf[0:4]) != bulkMagic ||
			int(binary.LittleEndian.Uint32(buf[4:8])) != c ||
			int(binary.LittleEndian.Uint32(buf[8:12])) != chunks ||
			int64(binary.LittleEndian.Uint64(buf[16:24])) != base ||
			int64(binary.LittleEndian.Uint64(buf[24:32])) != seed {
			bad++
		}
		for i := 0; i < bulkChunkSize; i++ {
			if buf[bulkHeader+i] != patternByte(c, i, seed) {
				bad++
				break
			}
		}
		if c == 0 {
			first <- struct{}{}
			<-release
		}
		if c%64 == 63 {
			gcStress()
			if !chainOK(holder, base+77) {
				bad++
			}
		}
	}
	var tail [1]byte
	n, err := r.Read(tail[:])
	if n != 0 || !errors.Is(err, io.EOF) {
		bad++
	}
	gcStress()
	if !chainOK(holder, base+77) {
		bad++
	}
	return bulkResult{chunks: chunks, bad: bad}
}

func main() {
	seed := envSeed()
	rounds := envRounds(1)
	const chunks = 256

	for r := 0; r < rounds; r++ {
		pr, pw, err := os.Pipe()
		ckf(err == nil, "os.Pipe: %v (seed=%d round=%d)", err, seed, r)
		if err != nil {
			break
		}
		base := seed*100000 + int64(r)*10000 + 11
		first := make(chan struct{}, 1)
		release := make(chan struct{})
		wres := make(chan error, 1)
		rres := make(chan bulkResult, 1)
		go bulkWriter(pw, chunks, seed, base, wres)
		go func() {
			rres <- bulkReader(pr, chunks, seed, base, first, release)
		}()

		select {
		case <-first:
		case <-time.After(waitLimit):
			ckf(false, "reader did not read first chunk (seed=%d round=%d)", seed, r)
		}
		// reader 阻塞在 release 上，writer 正在填满内核管道，此时 GC。
		gcStress()
		close(release)

		select {
		case rr := <-rres:
			ckf(rr.err == nil, "reader error: %v (seed=%d round=%d)", rr.err, seed, r)
			ckf(rr.chunks == chunks, "reader chunks=%d want=%d (seed=%d round=%d)",
				rr.chunks, chunks, seed, r)
			ckf(rr.bad == 0, "reader bad chunks=%d (seed=%d round=%d)", rr.bad, seed, r)
		case <-time.After(waitLimit):
			ckf(false, "reader did not finish (seed=%d round=%d)", seed, r)
		}
		select {
		case err := <-wres:
			ckf(err == nil, "writer error: %v (seed=%d round=%d)", err, seed, r)
		case <-time.After(waitLimit):
			ckf(false, "writer did not finish (seed=%d round=%d)", seed, r)
		}
		pr.Close()
	}

	fmt.Printf("rounds=%d chunks_per_round=%d bytes_per_round=%d\n", rounds, chunks, chunks*bulkChunkSize)
	finish("l19_os_pipe_bulk")
}
