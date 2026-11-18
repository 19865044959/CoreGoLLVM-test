// L13: 8KB 长行跨多次缓冲读：ReadSlice 拼接到换行符，CRLF 可跨缓冲边界。
package main

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"math/rand"
	"os"
	"runtime"
	"strconv"
	"sync/atomic"
)

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

type fragReader struct {
	data  []byte
	off   int
	frags []int
	fi    int
}

func (r *fragReader) Read(p []byte) (int, error) {
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

type lineReader struct {
	br *bufio.Reader
}

func newLineReader(r io.Reader, size int) *lineReader {
	return &lineReader{br: bufio.NewReaderSize(r, size)}
}

// readLine 循环 ReadSlice 直到拿到换行符，拼成完整一行（行内可跨多次缓冲）。
func (lr *lineReader) readLine() ([]byte, error) {
	var full []byte
	for {
		frag, err := lr.br.ReadSlice('\n')
		full = append(full, frag...)
		if err == bufio.ErrBufferFull {
			continue
		}
		return full, err
	}
}

func stripEOL(line []byte) []byte {
	if n := len(line); n >= 2 && line[n-2] == '\r' && line[n-1] == '\n' {
		return line[:n-2]
	}
	if n := len(line); n >= 1 && line[n-1] == '\n' {
		return line[:n-1]
	}
	return line
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

func pattern(n int, seed uint64) []byte {
	b := make([]byte, n)
	x := seed*0x9E3779B97F4A7C15 + 0x1234567
	for i := range b {
		x ^= x << 13
		x ^= x >> 7
		x ^= x << 17
		b[i] = byte('a' + (x>>24)%26) // 不引入 CR/LF
	}
	return b
}

func main() {
	curSeed = envSeed()
	r := rand.New(rand.NewSource(curSeed))
	frags := make([]int, 6)
	for i := range frags {
		frags[i] = 1 + r.Intn(1200)
	}
	frags = append(frags, 1, 1024, 4096)

	// A) 8KB 长行 + 若干短行 + 无换行尾行，底层按随机分片投递。
	long := pattern(8192, uint64(curSeed))
	var src []byte
	src = append(src, long...)
	src = append(src, "\r\n"...)
	src = append(src, "short-1\r\n"...)
	src = append(src, "\r\n"...)
	mid := pattern(100, uint64(curSeed)+1)
	src = append(src, mid...)
	src = append(src, "\r\n"...)
	src = append(src, "no-newline"...)

	fr := &fragReader{data: src, frags: frags}
	lr := newLineReader(fr, 1024)

	line, err := lr.readLine()
	ckSeed(err == nil, fmt.Sprintf("A: 8KB line error: %v", err))
	content := stripEOL(line)
	ckSeed(len(line) == 8194, fmt.Sprintf("A: 8KB line length: got %d want 8194", len(line)))
	ckSeed(bytes.Equal(content, long), "A: 8KB line content")
	held := string(content)
	gcStress()
	ckSeed(string(content) == held, "A: long line survives GC")

	line, err = lr.readLine()
	ckSeed(err == nil && string(stripEOL(line)) == "short-1", "A: short line after long line")
	line, err = lr.readLine()
	ckSeed(err == nil && len(stripEOL(line)) == 0, "A: empty line")
	line, err = lr.readLine()
	ckSeed(err == nil && bytes.Equal(stripEOL(line), mid), "A: 100-byte line")
	line, err = lr.readLine()
	ckSeed(err == io.EOF && string(stripEOL(line)) == "no-newline", "A: tail without newline returns data + EOF")
	gcStress()
	ckSeed(string(content) == held, "A: earlier long line still valid after later reads+GC")

	// B) 行长度贴着 1024 缓冲边界，CRLF 恰好跨缓冲。
	for _, n := range []int{1022, 1023, 1024, 1025, 2047, 2048} {
		body := pattern(n, uint64(curSeed)+uint64(n))
		data := make([]byte, 0, n+2)
		data = append(data, body...)
		data = append(data, "\r\n"...)
		fr := &fragReader{data: data, frags: []int{1, 7, 1023}}
		lr := newLineReader(fr, 1024)
		got, err := lr.readLine()
		ckSeed(err == nil, fmt.Sprintf("B: boundary line %d error: %v", n, err))
		ckSeed(len(got) == n+2, fmt.Sprintf("B: boundary line %d length: got %d", n, len(got)))
		ckSeed(bytes.Equal(stripEOL(got), body), fmt.Sprintf("B: boundary line %d content", n))
		if _, err := lr.readLine(); err != io.EOF {
			ckSeed(false, fmt.Sprintf("B: boundary line %d EOF, got %v", n, err))
		}
	}

	// C) 只有 LF 的行；空输入立即 EOF。
	{
		fr := &fragReader{data: []byte("lf-only\nsecond\n"), frags: []int{3}}
		lr := newLineReader(fr, 16)
		got, err := lr.readLine()
		ckSeed(err == nil && string(stripEOL(got)) == "lf-only", "C: LF-only line")
		got, err = lr.readLine()
		ckSeed(err == nil && string(stripEOL(got)) == "second", "C: second LF-only line")
		if _, err := lr.readLine(); err != io.EOF {
			ckSeed(false, "C: LF-only EOF")
		}
	}
	{
		fr := &fragReader{data: nil, frags: []int{3}}
		lr := newLineReader(fr, 16)
		line, err := lr.readLine()
		ckSeed(err == io.EOF && len(line) == 0, "C: empty stream EOF")
	}

	finish("l13_line_reader_long_lines")
}
