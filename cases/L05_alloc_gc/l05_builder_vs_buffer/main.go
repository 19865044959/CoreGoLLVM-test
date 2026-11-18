// L05: strings.Builder 与 bytes.Buffer 在同一批变长内容上分块追加、Reset 复用；
// 每轮缓存 Builder.String()/Buffer.Bytes() 副本，跨多轮 GC 后逐字节与 CRC 校验。
package main

import (
	"bytes"
	"fmt"
	"hash/crc32"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
)

const (
	roundCount = 12
	chunkCount = 700
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

func makeChunk(round, idx int, seed uint64) []byte {
	n := 24 + int((uint64(round)*17+uint64(idx)*29+seed)%160)
	b := make([]byte, n)
	base := uint64(round)*101 + uint64(idx)*37 + seed*13 + 3
	for i := range b {
		b[i] = byte((base + uint64(i)*11) % 251)
	}
	return b
}

func main() {
	seed := loadSeed()

	var sb strings.Builder
	var bb bytes.Buffer
	built := make([]string, 0, roundCount)
	copied := make([][]byte, 0, roundCount)
	wantCRC := make([]uint32, 0, roundCount)
	wantLen := make([]int, 0, roundCount)

	for r := 0; r < roundCount; r++ {
		sb.Reset()
		bb.Reset()
		var crc uint32
		total := 0
		for idx := 0; idx < chunkCount; idx++ {
			chunk := makeChunk(r, idx, seed)
			if _, err := sb.Write(chunk); err != nil {
				ckf(false, "round=%d builder write: %v", r, err)
				break
			}
			if _, err := bb.Write(chunk); err != nil {
				ckf(false, "round=%d buffer write: %v", r, err)
				break
			}
			crc = crc32.Update(crc, crc32.IEEETable, chunk)
			total += len(chunk)
		}
		ckf(sb.Len() == total, "round=%d builder len=%d want=%d", r, sb.Len(), total)
		ckf(bb.Len() == total, "round=%d buffer len=%d want=%d", r, bb.Len(), total)

		s := sb.String()
		b := bb.Bytes()
		if len(s) != len(b) {
			ckf(false, "round=%d builder/buffer length differ", r)
		} else {
			for i := 0; i < len(s); i += 1024 {
				if s[i] != b[i] {
					ckf(false, "round=%d builder/buffer differ at=%d", r, i)
					break
				}
			}
		}

		built = append(built, s)
		copied = append(copied, append([]byte(nil), b...))
		wantCRC = append(wantCRC, crc32.ChecksumIEEE(b))
		wantLen = append(wantLen, total)
		ckf(crc == wantCRC[r], "round=%d incremental crc mismatch", r)

		// Reset 后同一对象继续复用；旧 String()/Bytes() 副本已写入 built/copied。
		sb.Reset()
		bb.Reset()
		ck(sb.Len() == 0 && bb.Len() == 0, "reset did not clear length")

		// 轮间制造垃圾并 GC，迫使 Builder/Buffer 的后备数组经历回收周期。
		if r%2 == 1 {
			gcStress()
		}
	}

	gcStress()
	for r := range built {
		ckf(len(built[r]) == wantLen[r], "round=%d builder snapshot len after GC", r)
		ckf(len(copied[r]) == wantLen[r], "round=%d buffer snapshot len after GC", r)
		ckf(crc32.ChecksumIEEE([]byte(built[r])) == wantCRC[r], "round=%d builder snapshot checksum after GC", r)
		ckf(crc32.ChecksumIEEE(copied[r]) == wantCRC[r], "round=%d buffer snapshot checksum after GC", r)
	}

	// Reset 后重新写入完全不同内容，确认旧快照不与之共享可写缓冲。
	sb.Reset()
	bb.Reset()
	tail := "tail-after-reset-builder-buffer"
	sb.WriteString(tail)
	bb.WriteString(tail)
	gcStress()
	ck(sb.String() == tail && bb.String() == tail, "tail roundtrip after reset")
	ck(built[0] != tail && copied[0] != nil, "old snapshots must not alias tail")

	runtime.KeepAlive(built)
	runtime.KeepAlive(copied)
	finish("l05_builder_vs_buffer", seed)
}
