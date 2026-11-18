// L05: 可复用 scratch（直方图表 + 输出 slice）连续处理 255 个 128KB 块；
// 首块分配输出缓冲，其后复用同一底层数组；期间制造垃圾并 GC，输出不得串块。
package main

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"os"
	"runtime"
	"strconv"
	"sync/atomic"
)

const (
	blockSize   = 128 << 10
	blockCount  = 255
	snapshotGap = 16
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

// fillBlock 生成带长游程的确定性块数据，便于 RLE 编码。
func fillBlock(dst []byte, block int, seed uint64) {
	x := uint64(block)*0x9E3779B97F4A7C15 + seed*0x2545F4914F6CDD1D + 1
	for i := 0; i < len(dst); {
		x ^= x >> 12
		x ^= x << 25
		x ^= x >> 27
		run := int(x%600) + 1
		v := byte((x >> 40) % 251)
		for j := 0; j < run && i < len(dst); j++ {
			dst[i] = v
			i++
		}
	}
}

type scratch struct {
	hist [256]uint32
	out  []byte
}

func histSig(hist *[256]uint32) uint32 {
	var sig uint32
	for b, c := range hist {
		sig = sig*131 + c*uint32(b+1)
	}
	return sig
}

// encode 使用 scratch 的直方图表与输出缓冲编码一个块。
// 头部：blockID | crc32 | histSig | 原始长度，正文为 literal/repeat 两种 tag。
func (s *scratch) encode(block int, data []byte) []byte {
	for i := range s.hist {
		s.hist[i] = 0
	}
	for _, b := range data {
		s.hist[b]++
	}
	s.out = s.out[:0]
	s.out = binary.LittleEndian.AppendUint32(s.out, uint32(block))
	s.out = binary.LittleEndian.AppendUint32(s.out, crc32.ChecksumIEEE(data))
	s.out = binary.LittleEndian.AppendUint32(s.out, histSig(&s.hist))
	s.out = binary.LittleEndian.AppendUint32(s.out, uint32(len(data)))

	for i := 0; i < len(data); {
		j := i + 1
		for j < len(data) && data[j] == data[i] && j-i < 1<<20 {
			j++
		}
		if j-i >= 4 {
			s.out = append(s.out, 1)
			s.out = append(s.out, data[i])
			s.out = binary.AppendUvarint(s.out, uint64(j-i))
			i = j
			continue
		}
		start := i
		i++
		for i < len(data) && i-start < 4096 {
			k := i + 1
			for k < len(data) && data[k] == data[i] && k-i < 4 {
				k++
			}
			if k-i >= 4 {
				break
			}
			i = k
		}
		s.out = append(s.out, 0)
		s.out = binary.AppendUvarint(s.out, uint64(i-start))
		s.out = append(s.out, data[start:i]...)
	}
	return s.out
}

// decodeBlock 校验头部（blockID/长度/CRC/直方图签名）并重建数据。
func decodeBlock(src, dst []byte, wantBlock int) ([]byte, bool) {
	if len(src) < 16 {
		return nil, false
	}
	block := int(binary.LittleEndian.Uint32(src[0:4]))
	wantCRC := binary.LittleEndian.Uint32(src[4:8])
	wantSig := binary.LittleEndian.Uint32(src[8:12])
	n := int(binary.LittleEndian.Uint32(src[12:16]))
	if block != wantBlock || n != blockSize || n > cap(dst) {
		return nil, false
	}
	dst = dst[:0]
	i := 16
	for i < len(src) && len(dst) < n {
		tag := src[i]
		i++
		switch tag {
		case 1:
			if i >= len(src) {
				return nil, false
			}
			v := src[i]
			i++
			cnt, m := binary.Uvarint(src[i:])
			if m <= 0 {
				return nil, false
			}
			i += m
			if cnt == 0 || len(dst)+int(cnt) > n {
				return nil, false
			}
			for k := uint64(0); k < cnt; k++ {
				dst = append(dst, v)
			}
		case 0:
			ln, m := binary.Uvarint(src[i:])
			if m <= 0 {
				return nil, false
			}
			i += m
			if ln == 0 || i+int(ln) > len(src) || len(dst)+int(ln) > n {
				return nil, false
			}
			dst = append(dst, src[i:i+int(ln)]...)
			i += int(ln)
		default:
			return nil, false
		}
	}
	if len(dst) != n || i != len(src) {
		return nil, false
	}
	var hist [256]uint32
	for _, b := range dst {
		hist[b]++
	}
	if histSig(&hist) != wantSig {
		return nil, false
	}
	if crc32.ChecksumIEEE(dst) != wantCRC {
		return nil, false
	}
	return dst, true
}

func main() {
	seed := loadSeed()

	data := make([]byte, blockSize)
	dec := make([]byte, 0, blockSize)
	var s scratch
	var firstPtr *byte
	snaps := make([][]byte, 0, blockCount/snapshotGap+1)
	snapBlocks := make([]int, 0, cap(snaps))

	for bi := 0; bi < blockCount; bi++ {
		fillBlock(data, bi, seed)

		// 首块一次性分配到最坏编码上界；后续必须复用同一底层数组。
		if cap(s.out) < blockSize+256 {
			if bi != 0 {
				ckf(false, "scratch out reallocated at block=%d cap=%d", bi, cap(s.out))
			}
			s.out = make([]byte, 0, blockSize+256)
		}
		enc := s.encode(bi, data)
		if bi == 0 {
			firstPtr = &s.out[0]
		} else if &s.out[0] != firstPtr {
			ckf(false, "scratch out backing array changed at block=%d", bi)
		}
		ckf(cap(s.out) == blockSize+256, "scratch out cap grew at block=%d cap=%d", bi, cap(s.out))
		ckf(len(enc) <= blockSize+256, "encoded block=%d too large: %d", bi, len(enc))

		got, ok := decodeBlock(enc, dec, bi)
		if !ok {
			ckf(false, "block=%d decode failed", bi)
		} else {
			ckf(bytes.Equal(got, data), "block=%d content mismatch", bi)
			dec = got[:0]
		}

		// 定期保留编码副本，稍后跨 GC 再解，检测 scratch 复用造成的串块。
		if bi%snapshotGap == 0 {
			snaps = append(snaps, append([]byte(nil), enc...))
			snapBlocks = append(snapBlocks, bi)
		}

		junk := make([]byte, 8<<10)
		junk[0] = byte(bi)
		gcSink = append(gcSink[:0], junk)
		if bi%8 == 0 {
			runtime.GC()
		}
	}
	gcSink = gcSink[:0]
	runtime.GC()
	runtime.GC()

	for k, enc := range snaps {
		bi := snapBlocks[k]
		fillBlock(data, bi, seed)
		got, ok := decodeBlock(enc, dec, bi)
		if !ok {
			ckf(false, "snapshot block=%d decode after GC failed", bi)
			continue
		}
		ckf(bytes.Equal(got, data), "snapshot block=%d content after GC mismatch", bi)
		dec = got[:0]
	}
	runtime.KeepAlive(s.out)
	runtime.KeepAlive(firstPtr)

	finish("l05_scratch_reuse_blocks", seed)
}
