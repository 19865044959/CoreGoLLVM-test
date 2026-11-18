// L13: 固定窗口缓冲读取器：已缓冲数据不重复读底层、跨窗口紧凑、Next(0) 重置。
package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"os"
	"runtime"
	"strconv"
	"sync/atomic"
)

var errNegative = errors.New("chunkreader: negative size")

var (
	failed  int32
	gcSink  [][]byte
	curSeed int64
)

func ck(ok bool, msg string) {
	if !ok {
		atomic.AddInt32(&failed, 1)
		fmt.Println("FAIL:", msg)
	}
}

func ckSeed(ok bool, msg string) {
	if !ok {
		atomic.AddInt32(&failed, 1)
		fmt.Printf("FAIL: %s (seed=%d)\n", msg, curSeed)
	}
}

func gcStress() {
	for i := 0; i < 16; i++ {
		b := make([]byte, 4096)
		b[0] = byte(i)
		gcSink = append(gcSink[:0], b)
	}
	gcSink = gcSink[:0]
	runtime.GC()
	runtime.GC()
}

func finish(name string) {
	if n := atomic.LoadInt32(&failed); n != 0 {
		fmt.Printf("FAILED %s (%d failed checks)\n", name, n)
		os.Exit(1)
	}
	fmt.Println("ALL PASS " + name)
}

// fragReader 每次最多返回 frags 中指定数量的字节，并在 Read 中计数。
type fragReader struct {
	data  []byte
	off   int
	frags []int
	fi    int
	calls int
}

func (r *fragReader) Read(p []byte) (int, error) {
	r.calls++
	if r.off >= len(r.data) {
		return 0, io.EOF
	}
	n := len(p)
	if f := r.frags[r.fi%len(r.frags)]; f < n {
		n = f
	}
	r.fi++
	if n > len(r.data)-r.off {
		n = len(r.data) - r.off
	}
	copy(p, r.data[r.off:r.off+n])
	r.off += n
	return n, nil
}

// chunkReader 维护固定窗口 [rp,wp)，小请求走窗口，大请求直接分配。
type chunkReader struct {
	r        *fragReader
	buf      []byte
	rp, wp   int
	eof      bool
	srcReads int
}

func newChunkReader(r *fragReader, size int) *chunkReader {
	return &chunkReader{r: r, buf: make([]byte, size)}
}

func (cr *chunkReader) Next(n int) ([]byte, error) {
	if n < 0 {
		return nil, errNegative
	}
	if n == 0 {
		cr.rp, cr.wp = 0, 0
		return cr.buf[:0], nil
	}
	if cr.wp-cr.rp >= n {
		out := cr.buf[cr.rp : cr.rp+n]
		cr.rp += n
		return out, nil
	}
	if n <= len(cr.buf) {
		avail := copy(cr.buf, cr.buf[cr.rp:cr.wp])
		cr.rp, cr.wp = 0, avail
		for cr.wp < n {
			if cr.eof {
				break
			}
			m, err := cr.r.Read(cr.buf[cr.wp:])
			cr.srcReads++
			if m > 0 {
				cr.wp += m
			}
			if err == io.EOF {
				cr.eof = true
				break
			}
			if err != nil {
				return nil, err
			}
			if m == 0 {
				break
			}
		}
		if cr.wp < n {
			out := cr.buf[:cr.wp]
			cr.rp = cr.wp
			if len(out) == 0 {
				return nil, io.EOF
			}
			return out, io.ErrUnexpectedEOF
		}
		out := cr.buf[:n]
		cr.rp = n
		return out, nil
	}
	// 请求超过窗口：用独立分配的大缓冲承接已缓存数据并继续读取。
	out := make([]byte, 0, n)
	out = append(out, cr.buf[cr.rp:cr.wp]...)
	cr.rp, cr.wp = 0, 0
	for len(out) < n {
		if cr.eof {
			break
		}
		need := n - len(out)
		tmp := make([]byte, need)
		m, err := cr.r.Read(tmp)
		cr.srcReads++
		if m > 0 {
			out = append(out, tmp[:m]...)
		}
		if err == io.EOF {
			cr.eof = true
			break
		}
		if err != nil {
			return nil, err
		}
		if m == 0 {
			break
		}
	}
	if len(out) < n {
		if len(out) == 0 {
			return nil, io.EOF
		}
		return out, io.ErrUnexpectedEOF
	}
	return out, nil
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

func makeData(n int, seed uint64) []byte {
	b := make([]byte, n)
	x := seed*0x9E3779B97F4A7C15 + 1
	for i := range b {
		x ^= x << 13
		x ^= x >> 7
		x ^= x << 17
		b[i] = byte(x >> 24)
	}
	return b
}

func main() {
	curSeed = envSeed()

	// A) 已缓冲数据不重复读底层；Next(0) 重置读写指针。
	{
		data := makeData(100, uint64(curSeed))
		fr := &fragReader{data: data, frags: []int{7}}
		cr := newChunkReader(fr, 32)
		got, err := cr.Next(16)
		ckSeed(err == nil && bytes.Equal(got, data[0:16]), "A: first buffered read")
		ckSeed(fr.calls == 3, fmt.Sprintf("A: read calls after Next(16): got %d want 3", fr.calls))
		got, err = cr.Next(5)
		ckSeed(err == nil && bytes.Equal(got, data[16:21]), "A: served from buffered data")
		ckSeed(fr.calls == 3, "A: no extra underlying read while buffered")
		if z, err := cr.Next(0); err != nil || len(z) != 0 {
			ckSeed(false, "A: Next(0) resets without error")
		}
		ckSeed(cr.rp == 0 && cr.wp == 0, "A: Next(0) resets rp/wp")
		got, err = cr.Next(1)
		ckSeed(err == nil && bytes.Equal(got, data[21:22]), "A: read after reset")
		ckSeed(fr.calls == 4, "A: reset forces refill")
	}

	// B) 跨窗口：耗尽后把剩余数据紧凑到窗口前部再补齐。
	{
		data := makeData(500, uint64(curSeed)+1)
		fr := &fragReader{data: data, frags: []int{3, 5, 11}}
		cr := newChunkReader(fr, 32)
		off := 0
		for _, n := range []int{30, 30, 30, 30} {
			got, err := cr.Next(n)
			ckSeed(err == nil && bytes.Equal(got, data[off:off+n]), fmt.Sprintf("B: cross-window read at %d", off))
			off += n
		}
		ckSeed(off == 120, "B: consumed length")
	}

	// C) 超过窗口的请求直接分配；返回的大切片跨 GC 仍有效。
	{
		data := makeData(600, uint64(curSeed)+2)
		fr := &fragReader{data: data, frags: []int{13}}
		cr := newChunkReader(fr, 32)
		head, err := cr.Next(10)
		ckSeed(err == nil && bytes.Equal(head, data[:10]), "C: head read")
		big, err := cr.Next(200)
		ckSeed(err == nil && len(big) == 200 && bytes.Equal(big, data[10:210]), "C: large read across window")
		gcStress()
		ckSeed(bytes.Equal(big, data[10:210]), "C: large read survives GC")
	}

	// D) EOF 语义：精确耗尽返回 io.EOF，半截请求返回 io.ErrUnexpectedEOF。
	{
		data := makeData(10, uint64(curSeed)+3)
		fr := &fragReader{data: data, frags: []int{4}}
		cr := newChunkReader(fr, 8)
		a, err := cr.Next(4)
		ckSeed(err == nil && bytes.Equal(a, data[:4]), "D: first exact read")
		b, err := cr.Next(6)
		ckSeed(err == nil && bytes.Equal(b, data[4:10]), "D: second exact read")
		_, err = cr.Next(1)
		ckSeed(err == io.EOF, fmt.Sprintf("D: exact EOF, got %v", err))
	}
	{
		data := makeData(10, uint64(curSeed)+4)
		fr := &fragReader{data: data, frags: []int{4}}
		cr := newChunkReader(fr, 8)
		a, err := cr.Next(9)
		ckSeed(err == nil && bytes.Equal(a, data[:9]), "D: large exact read")
		b, err := cr.Next(3)
		ckSeed(err == io.ErrUnexpectedEOF, fmt.Sprintf("D: partial EOF error, got %v", err))
		ckSeed(bytes.Equal(b, data[9:10]), "D: partial EOF returns remaining bytes")
	}
	{
		fr := &fragReader{data: nil, frags: []int{4}}
		cr := newChunkReader(fr, 8)
		_, err := cr.Next(1)
		ckSeed(err == io.EOF, "D: empty stream EOF")
		_, err = cr.Next(-1)
		ckSeed(err == errNegative, "D: negative size rejected")
	}

	// E) 随机窗口/随机取块，逐字节对照。
	{
		r := rand.New(rand.NewSource(curSeed))
		data := makeData(4096, uint64(curSeed)+5)
		frags := make([]int, 8)
		for i := range frags {
			frags[i] = 1 + r.Intn(23)
		}
		fr := &fragReader{data: data, frags: frags}
		cr := newChunkReader(fr, 64)
		off := 0
		chunks := 0
		for off < len(data) {
			n := 1 + r.Intn(100)
			if n > len(data)-off {
				n = len(data) - off
			}
			got, err := cr.Next(n)
			if err != nil || !bytes.Equal(got, data[off:off+n]) {
				ckSeed(false, fmt.Sprintf("E: random chunk at %d n=%d err=%v", off, n, err))
				break
			}
			off += n
			chunks++
			if chunks%50 == 0 {
				gcStress()
			}
		}
		ckSeed(off == len(data), "E: random read consumed all bytes")
		if _, err := cr.Next(1); err != io.EOF {
			ckSeed(false, fmt.Sprintf("E: final EOF got %v", err))
		}
		ckSeed(fr.calls > 0, "E: underlying reader used")
	}

	finish("l13_chunkreader_buffered")
}
