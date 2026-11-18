// L13: 小目标容量 (nil/20/64) 下多种输入长度的 compress/flate 往返，
// 覆盖 append/扩容路径与编码输出对源切片的独立性。
package main

import (
	"bytes"
	"compress/flate"
	"fmt"
	"io"
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

type sliceWriter struct {
	buf []byte
}

func (w *sliceWriter) Write(p []byte) (int, error) {
	w.buf = append(w.buf, p...)
	return len(p), nil
}

func flateAppend(dst, src []byte) ([]byte, error) {
	w := &sliceWriter{buf: dst}
	fw, err := flate.NewWriter(w, flate.BestSpeed)
	if err != nil {
		return nil, err
	}
	if _, err := fw.Write(src); err != nil {
		return nil, err
	}
	if err := fw.Close(); err != nil {
		return nil, err
	}
	return w.buf, nil
}

func flateInflate(dst, comp []byte) ([]byte, error) {
	fr := flate.NewReader(bytes.NewReader(comp))
	w := &sliceWriter{buf: dst}
	_, err := io.Copy(w, fr)
	cerr := fr.Close()
	if err != nil {
		return w.buf, err
	}
	return w.buf, cerr
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

func tortureRounds(def int) int {
	s := os.Getenv("TORTURE_ROUNDS")
	if s == "" {
		return def
	}
	n, err := strconv.Atoi(s)
	if err != nil || n <= 0 {
		return def
	}
	if n > 1000 {
		n = 1000
	}
	return n
}

func makePayload(n int, seed uint64) []byte {
	b := make([]byte, n)
	x := seed
	for i := range b {
		x += 0x9E3779B97F4A7C15
		z := x
		z = (z ^ (z >> 30)) * 0xBF58476D1CE4E5B9
		z = (z ^ (z >> 27)) * 0x94D049BB133111EB
		z ^= z >> 31
		b[i] = byte(z >> 56)
	}
	if n%3 == 0 {
		// 三分之一的长度使用高度可压缩数据，覆盖小块输出路径。
		for i := range b {
			b[i] = byte('a' + (n+i)%26)
		}
	}
	return b
}

func main() {
	curSeed = envSeed()
	lengths := []int{
		0, 1, 2, 3, 4, 5, 7, 8, 15, 16, 17, 31, 32, 33, 63, 64,
		65, 127, 128, 129, 255, 256, 257, 511, 512, 513, 1023, 1024, 1025,
		2048, 4095, 8192,
	}
	caps := []int{0, 20, 64}
	rounds := tortureRounds(3)
	grew := false

	type record struct {
		want []byte
		enc  []byte
		desc string
	}

	for round := 0; round < rounds; round++ {
		recs := make([]record, 0, len(lengths)*len(caps))
		for _, L := range lengths {
			for ci, c := range caps {
				var dst []byte
				if c > 0 {
					dst = make([]byte, 0, c)
				}
				seed := uint64(curSeed) + uint64(round*1000+L*13+ci)
				src := makePayload(L, seed)
				want := append([]byte(nil), src...)
				enc, err := flateAppend(dst, src)
				if err != nil {
					ckSeed(false, fmt.Sprintf("flateAppend round=%d len=%d cap=%d: %v", round, L, c, err))
					continue
				}
				ckSeed(len(enc) > 0, fmt.Sprintf("empty encoded output round=%d len=%d cap=%d", round, L, c))
				ckSeed(cap(enc) >= len(enc), "encoded cap covers len")
				if len(enc) > c {
					grew = true
				}
				// 编码结束后改写源切片，验证输出不引用输入。
				for i := range src {
					src[i] = 0x5A
				}
				desc := fmt.Sprintf("round=%d len=%d cap=%d", round, L, c)
				recs = append(recs, record{want: want, enc: enc, desc: desc})
			}
		}

		// 关键位置：全部编码完成、解码之前 GC，已分配的输出缓冲必须存活。
		gcStress()
		for _, r := range recs {
			var decDst []byte
			if len(r.enc)%2 == 0 {
				decDst = make([]byte, 0, 20)
			} else {
				decDst = make([]byte, 0, 64)
			}
			dec, err := flateInflate(decDst, r.enc)
			if err != nil {
				ckSeed(false, fmt.Sprintf("inflate %s: %v", r.desc, err))
				continue
			}
			ckSeed(bytes.Equal(dec, r.want), fmt.Sprintf("roundtrip mismatch %s", r.desc))
		}
		gcStress()
	}

	// 同输入同目标容量重复编码，输出必须逐字节一致。
	for i, c := range caps {
		src := makePayload(333, uint64(curSeed)+uint64(i))
		var dst []byte
		if c > 0 {
			dst = make([]byte, 0, c)
		}
		e1, err1 := flateAppend(dst, src)
		e2, err2 := flateAppend(dst, src)
		ckSeed(err1 == nil && err2 == nil, "deterministic encode errors")
		ckSeed(bytes.Equal(e1, e2), fmt.Sprintf("deterministic encode cap=%d", c))
	}

	ckSeed(grew, "at least one encoded output grew beyond the small target capacity")
	runtime.KeepAlive(gcSink)
	finish("l13_small_capacity_roundtrip")
}
