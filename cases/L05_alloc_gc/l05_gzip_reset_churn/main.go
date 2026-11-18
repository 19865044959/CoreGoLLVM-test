// L05: stdlib gzip.Writer.Reset 反复复用内部窗口/哈希表/写缓冲，20 轮递减载荷，
// 每轮分配垃圾并 GC，最后统一回解校验全部结果。
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
	"sync/atomic"
)

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

func ckf(ok bool, format string, args ...interface{}) {
	if !ok {
		atomic.AddInt32(&failed, 1)
		fmt.Printf("FAIL: "+format+"\n", args...)
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

func fillPayload(p []byte, seed uint64) {
	x := seed*0x9E3779B97F4A7C15 + 1
	for i := 0; i < len(p); {
		x ^= x >> 12
		x ^= x << 25
		x ^= x >> 27
		run := int(x%300) + 1
		v := byte((x >> 40) % 251)
		for j := 0; j < run && i < len(p); j++ {
			p[i] = v
			i++
		}
	}
}

func main() {
	seed := loadSeed()

	const (
		rounds   = 20
		baseSize = 256 << 10
		step     = 8 << 10
	)
	payload := make([]byte, baseSize)
	fillPayload(payload, seed)

	var buf bytes.Buffer
	w, err := gzip.NewWriterLevel(&buf, gzip.DefaultCompression)
	ck(err == nil, "new gzip writer")
	if err != nil {
		finish("l05_gzip_reset_churn", seed)
		return
	}

	outs := make([][]byte, 0, rounds)
	wantLen := make([]int, 0, rounds)
	wantCRC := make([]uint32, 0, rounds)

	for r := 0; r < rounds; r++ {
		n := baseSize - r*step
		src := payload[:n]

		buf.Reset()
		w.Reset(&buf)
		ok := true
		for off := 0; off < n; off += step {
			end := off + step
			if end > n {
				end = n
			}
			if _, err := w.Write(src[off:end]); err != nil {
				ckf(false, "round=%d write off=%d: %v", r, off, err)
				ok = false
				break
			}
		}
		if ok {
			if err := w.Close(); err != nil {
				ckf(false, "round=%d close: %v", r, err)
				ok = false
			}
		}
		if !ok {
			continue
		}
		// 缓存本轮输出，Writer 在下一轮 Reset 后不得改写它。
		comp := append([]byte(nil), buf.Bytes()...)
		outs = append(outs, comp)
		wantLen = append(wantLen, n)
		wantCRC = append(wantCRC, crc32.ChecksumIEEE(src))

		ckf(len(comp) > 16, "round=%d compressed too short: %d", r, len(comp))
		gcStress() // Reset 循环之间制造垃圾并强制 GC
	}

	// 再制造一次垃圾与 GC，确保所有缓存输出与 Writer 内部状态都经历回收周期。
	payload = nil
	gcSink = nil
	runtime.GC()
	for i := 0; i < 64; i++ {
		junk := make([]byte, 16<<10)
		junk[0] = byte(i)
		gcSink = append(gcSink[:0], junk)
	}
	gcSink = gcSink[:0]
	runtime.GC()

	for r, out := range outs {
		zr, err := gzip.NewReader(bytes.NewReader(out))
		if err != nil {
			ckf(false, "round=%d new reader: %v", r, err)
			continue
		}
		got, err := io.ReadAll(zr)
		cerr := zr.Close()
		if err != nil {
			ckf(false, "round=%d decompress: %v", r, err)
			continue
		}
		if cerr != nil {
			ckf(false, "round=%d reader close: %v", r, cerr)
		}
		ckf(len(got) == wantLen[r], "round=%d len got=%d want=%d", r, len(got), wantLen[r])
		ckf(crc32.ChecksumIEEE(got) == wantCRC[r], "round=%d payload checksum mismatch", r)
	}

	// 最后再 Reset 一次并产生一份新输出，确认前面 20 轮的复用未损坏 Writer。
	var tail bytes.Buffer
	w.Reset(&tail)
	if _, err := w.Write([]byte("tail-after-reset")); err != nil {
		ckf(false, "tail write: %v", err)
	}
	if err := w.Close(); err != nil {
		ckf(false, "tail close: %v", err)
	}
	tr, err := gzip.NewReader(bytes.NewReader(tail.Bytes()))
	if err != nil {
		ckf(false, "tail reader: %v", err)
	} else {
		got, rerr := io.ReadAll(tr)
		tr.Close()
		ck(rerr == nil && string(got) == "tail-after-reset", "tail roundtrip")
	}

	finish("l05_gzip_reset_churn", seed)
}
