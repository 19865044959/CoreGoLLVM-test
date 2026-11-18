// L05: 多 goroutine 从共享 1MB 源按随机长度切片，各自用 stdlib gzip 压缩/解压，
// 过程中分配垃圾并显式 GC，校验每轮往返内容。
package main

import (
	"bytes"
	"compress/gzip"
	"fmt"
	"hash/crc32"
	"io"
	"os"
	"runtime"
	"strconv"
	"sync"
	"sync/atomic"
)

const (
	srcSize    = 1 << 20
	numWorkers = 6
	numRounds  = 10
	minSeg     = 16 << 10
	maxSeg     = 128 << 10
	gcEveryN   = 3
)

var (
	failed int32
	gcSink [][]byte
	ops    int64
)

func ck(ok bool, msg string) {
	if !ok {
		atomic.AddInt32(&failed, 1)
		fmt.Println("FAIL:", msg)
	}
}

func ckf(ok bool, format string, args ...interface{}) {
	if !ok {
		atomic.AddInt32(&failed, 1)
		fmt.Printf("FAIL: "+format+"\n", args...)
	}
}

func loadSeed() uint64 {
	s := os.Getenv("UAF_SEED")
	if s == "" {
		return 1
	}
	n, err := strconv.ParseUint(s, 10, 64)
	if err != nil {
		return 1
	}
	return n
}

func finish(name string, seed uint64) {
	if n := atomic.LoadInt32(&failed); n != 0 {
		fmt.Printf("FAILED %s (%d failed checks, seed=%d)\n", name, n, seed)
		os.Exit(1)
	}
	fmt.Println("ALL PASS " + name)
}

type rng struct {
	s uint64
}

func newRNG(seed uint64) *rng {
	if seed == 0 {
		seed = 1
	}
	return &rng{s: seed*0x9E3779B97F4A7C15 + 1}
}

func (r *rng) next() uint64 {
	x := r.s
	x ^= x >> 12
	x ^= x << 25
	x ^= x >> 27
	r.s = x
	return x * 2685821657736338717
}

func (r *rng) intn(n int) int {
	if n <= 0 {
		return 0
	}
	return int(r.next() % uint64(n))
}

//go:noinline
func makeJunk(n int, tag byte) []byte {
	b := make([]byte, n)
	b[0] = tag
	return b
}

// localGC 只使用本 goroutine 的垃圾，避免并发用例共享全局 sink 造成数据竞争。
func localGC(round int) {
	junk := makeJunk(16<<10, byte(round))
	runtime.KeepAlive(junk)
	runtime.GC()
}

func worker(w int, src []byte, seed uint64, wg *sync.WaitGroup) {
	defer wg.Done()
	r := newRNG(seed*1000 + uint64(w)*97 + 7)
	var buf bytes.Buffer
	wtr, err := gzip.NewWriterLevel(&buf, gzip.DefaultCompression)
	if err != nil {
		ckf(false, "worker=%d new gzip writer: %v", w, err)
		return
	}
	span := maxSeg - minSeg
	for round := 0; round < numRounds; round++ {
		start := r.intn(srcSize - maxSeg)
		n := minSeg + r.intn(span+1)
		seg := src[start : start+n]

		buf.Reset()
		wtr.Reset(&buf)
		step := (n + 2) / 3
		ok := true
		for off := 0; off < n; off += step {
			end := off + step
			if end > n {
				end = n
			}
			if _, err := wtr.Write(seg[off:end]); err != nil {
				ckf(false, "worker=%d round=%d write: %v", w, round, err)
				ok = false
				break
			}
		}
		if !ok {
			continue
		}
		if err := wtr.Close(); err != nil {
			ckf(false, "worker=%d round=%d close: %v", w, round, err)
			continue
		}
		if buf.Len() == 0 {
			ckf(false, "worker=%d round=%d empty compressed stream", w, round)
			continue
		}

		zr, err := gzip.NewReader(bytes.NewReader(buf.Bytes()))
		if err != nil {
			ckf(false, "worker=%d round=%d new reader: %v", w, round, err)
			continue
		}
		got, rerr := io.ReadAll(zr)
		cerr := zr.Close()
		if rerr != nil {
			ckf(false, "worker=%d round=%d decompress: %v", w, round, rerr)
			continue
		}
		if cerr != nil {
			ckf(false, "worker=%d round=%d reader close: %v", w, round, cerr)
		}
		if len(got) != n || crc32.ChecksumIEEE(got) != crc32.ChecksumIEEE(seg) || !bytes.Equal(got, seg) {
			ckf(false, "worker=%d round=%d roundtrip mismatch start=%d len=%d", w, round, start, n)
		}
		atomic.AddInt64(&ops, 1)

		if round%gcEveryN == gcEveryN-1 {
			localGC(round)
		}
	}
}

func main() {
	seed := loadSeed()

	src := make([]byte, srcSize)
	x := seed*0x9E3779B97F4A7C15 + 1
	for i := 0; i < len(src); {
		x ^= x >> 12
		x ^= x << 25
		x ^= x >> 27
		run := int(x%512) + 1
		v := byte((x >> 40) % 251)
		for j := 0; j < run && i < len(src); j++ {
			src[i] = v
			i++
		}
	}

	// 启动前压一次堆，随后并发压缩期间由各 worker 与主协程穿插 GC。
	for i := 0; i < 16; i++ {
		b := make([]byte, 4096)
		b[0] = byte(i)
		gcSink = append(gcSink[:0], b)
	}
	gcSink = gcSink[:0]
	runtime.GC()

	var wg sync.WaitGroup
	for w := 0; w < numWorkers; w++ {
		wg.Add(1)
		go worker(w, src, seed, &wg)
	}
	wg.Wait()

	runtime.GC()
	for i := 0; i < 64; i++ {
		junk := make([]byte, 32<<10)
		junk[0] = byte(i)
		gcSink = append(gcSink[:0], junk)
	}
	gcSink = gcSink[:0]
	runtime.GC()

	ckf(atomic.LoadInt64(&ops) == numWorkers*numRounds,
		"completed rounds=%d want=%d", atomic.LoadInt64(&ops), numWorkers*numRounds)

	finish("l05_concurrent_compress_gc", seed)
}
