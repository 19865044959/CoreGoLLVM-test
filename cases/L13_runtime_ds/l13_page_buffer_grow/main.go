// L13: 多页缓冲跨页写、Truncate 后重写、容量逐轮翻倍，与 bytes.Buffer 对照。
package main

import (
	"bytes"
	"fmt"
	"os"
	"runtime"
	"sync/atomic"
)

const pageSize = 512

var (
	failed int32
	gcSink [][]byte
)

func ck(ok bool, msg string) {
	if !ok {
		atomic.AddInt32(&failed, 1)
		fmt.Println("FAIL:", msg)
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

// pageBuf 以固定 512B 页追加写入，模拟 badger y 包的多页缓冲。
type pageBuf struct {
	pages [][]byte
	n     int
}

func (b *pageBuf) Write(p []byte) (int, error) {
	total := len(p)
	for len(p) > 0 {
		off := b.n % pageSize
		if off == 0 {
			b.pages = append(b.pages, make([]byte, pageSize))
		}
		page := b.pages[len(b.pages)-1]
		k := copy(page[off:], p)
		b.n += k
		p = p[k:]
	}
	return total, nil
}

func (b *pageBuf) Truncate(n int) {
	if n < 0 {
		n = 0
	}
	if n > b.n {
		return
	}
	b.n = n
	keep := (n + pageSize - 1) / pageSize
	for i := keep; i < len(b.pages); i++ {
		b.pages[i] = nil
	}
	b.pages = b.pages[:keep]
	if n > 0 && n%pageSize != 0 {
		page := b.pages[len(b.pages)-1]
		for i := n % pageSize; i < pageSize; i++ {
			page[i] = 0
		}
	}
}

func (b *pageBuf) Bytes() []byte {
	out := make([]byte, b.n)
	for i := 0; i < b.n; i++ {
		out[i] = b.pages[i/pageSize][i%pageSize]
	}
	return out
}

func fillPattern(b []byte, salt int64) {
	x := uint64(salt)*0x9E3779B97F4A7C15 + 0xD1B54A32D192ED03
	for i := range b {
		x ^= x << 13
		x ^= x >> 7
		x ^= x << 17
		b[i] = byte(x >> 24)
	}
}

func main() {
	// 跨页写：256B 起始，随后三条跨越页边界的写入。
	var pb pageBuf
	var bb bytes.Buffer
	var expected []byte
	sizes := []int{256, 1024, 511, 513}
	for i, sz := range sizes {
		chunk := make([]byte, sz)
		fillPattern(chunk, int64(i+1))
		if _, err := pb.Write(chunk); err != nil {
			ck(false, "page buffer write error")
		}
		bb.Write(chunk)
		expected = append(expected, chunk...)
	}
	gcStress()
	ck(pb.n == len(expected), fmt.Sprintf("page buffer length: got %d want %d", pb.n, len(expected)))
	ck(bytes.Equal(pb.Bytes(), expected), "page buffer content across page boundaries")
	ck(bytes.Equal(bb.Bytes(), expected), "bytes.Buffer reference content")
	ck(len(pb.pages) == (len(expected)+pageSize-1)/pageSize, "page count after cross-page writes")
	ck(len(expected) == 2304, "cross-page total length")
	for i, p := range pb.pages {
		ck(len(p) == pageSize, fmt.Sprintf("page %d fixed size", i))
	}

	// Truncate 到非页边界后重写，跨页数据必须重新对齐。
	pb.Truncate(700)
	bb.Truncate(700)
	expected = expected[:700]
	gcStress()
	tail := make([]byte, 900)
	fillPattern(tail, 99)
	pb.Write(tail)
	bb.Write(tail)
	expected = append(expected, tail...)
	gcStress()
	ck(bytes.Equal(pb.Bytes(), expected), "page buffer after truncate+rewrite")
	ck(bytes.Equal(bb.Bytes(), expected), "bytes.Buffer after truncate+rewrite")
	ck(len(pb.pages) == (len(expected)+pageSize-1)/pageSize, "page count after truncate+rewrite")

	// Truncate 到 0 后重写。
	pb.Truncate(0)
	bb.Truncate(0)
	expected = nil
	again := make([]byte, 600)
	fillPattern(again, 7)
	pb.Write(again)
	bb.Write(again)
	expected = append(expected, again...)
	ck(bytes.Equal(pb.Bytes(), expected), "page buffer after reset to zero")
	ck(bytes.Equal(bb.Bytes(), expected), "bytes.Buffer after reset to zero")
	ck(len(pb.pages) == 2, "page count after reset+write")

	// 容量逐轮翻倍：32B 起，每轮写入当前容量大小的数据。
	var pb2 pageBuf
	var bb2 bytes.Buffer
	bb2.Grow(32)
	var all []byte
	size := 32
	for round := 0; round < 10; round++ {
		chunk := make([]byte, size)
		fillPattern(chunk, int64(round+100))
		pb2.Write(chunk)
		bb2.Write(chunk)
		all = append(all, chunk...)
		size *= 2
		if round%3 == 0 {
			gcStress()
		}
	}
	ck(len(all) == 32736, "doubling total length")
	ck(pb2.n == len(all), "doubling page buffer length")
	ck(bytes.Equal(pb2.Bytes(), all), "doubling page buffer content")
	ck(bytes.Equal(bb2.Bytes(), all), "bytes.Buffer doubling content")
	ck(bb2.Cap() >= len(all), "bytes.Buffer capacity covers length")
	ck(len(pb2.pages) == (len(all)+pageSize-1)/pageSize, "doubling page count")

	// GC 后再读一次，页数据必须仍然有效。
	gcStress()
	ck(bytes.Equal(pb.Bytes(), expected), "first page buffer survives GC")
	ck(bytes.Equal(pb2.Bytes(), all), "doubling page buffer survives GC")

	runtime.KeepAlive(pb)
	runtime.KeepAlive(bb)
	runtime.KeepAlive(pb2)
	runtime.KeepAlive(bb2)
	finish("l13_page_buffer_grow")
}
