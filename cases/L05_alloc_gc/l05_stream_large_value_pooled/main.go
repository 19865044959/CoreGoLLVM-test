// L05: 模拟 badger stream writer：2–4MB 大 value 经 1MB 池化缓冲分块写入内存 sink，
// GC 后再经池化缓冲分块读回并逐字节校验。
package main

import (
	"fmt"
	"hash/crc32"
	"os"
	"runtime"
	"strconv"
	"sync"
	"sync/atomic"
)

const (
	chunkSize  = 1 << 20
	valueCount = 3
	valueSize  = 3 << 20
)

var (
	failed int32
	gcSink [][]byte
	vpool  = sync.Pool{New: func() interface{} {
		b := make([]byte, chunkSize)
		return &b
	}}
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

func valueByte(v, i int, seed uint64) byte {
	return byte((uint64(v)*17 + uint64(i)*7 + seed*13 + 11) % 251)
}

// writeValue 从池中借一块缓冲，分块生成并写入 sink，返回 sink 与整值 CRC。
func writeValue(sink []byte, v int, seed uint64) ([]byte, uint32) {
	bp := vpool.Get().(*[]byte)
	buf := (*bp)[:0]
	ckf(len(buf) == 0, "pooled write buffer not reset: value=%d len=%d", v, len(buf))
	ckf(cap(buf) >= chunkSize, "pooled write buffer too small: value=%d cap=%d", v, cap(buf))

	var crc uint32
	for off := 0; off < valueSize; off += chunkSize {
		end := off + chunkSize
		if end > valueSize {
			end = valueSize
		}
		buf = buf[:0]
		for i := off; i < end; i++ {
			buf = append(buf, valueByte(v, i, seed))
		}
		crc = crc32.Update(crc, crc32.IEEETable, buf)
		sink = append(sink, buf...)
	}
	*bp = buf[:0]
	vpool.Put(bp)
	return sink, crc
}

// readValue 从 sink 分块读回池化缓冲，校验长度、CRC 与逐字节内容。
func readValue(src []byte, v int, seed uint64, wantCRC uint32) bool {
	if len(src) != valueSize {
		ckf(false, "value=%d read len=%d want=%d", v, len(src), valueSize)
		return false
	}
	bp := vpool.Get().(*[]byte)
	buf := (*bp)[:0]
	if len(buf) != 0 || cap(buf) < chunkSize {
		ckf(false, "pooled read buffer shape value=%d len=%d cap=%d", v, len(buf), cap(buf))
	}
	ok := true
	var crc uint32
	for off := 0; off < valueSize; off += chunkSize {
		end := off + chunkSize
		if end > valueSize {
			end = valueSize
		}
		buf = buf[:0]
		buf = append(buf, src[off:end]...)
		crc = crc32.Update(crc, crc32.IEEETable, buf)
		for i := off; i < end; i++ {
			if buf[i-off] != valueByte(v, i, seed) {
				ckf(false, "value=%d byte mismatch at=%d", v, i)
				ok = false
				break
			}
		}
	}
	ckf(crc == wantCRC, "value=%d crc got=%08x want=%08x", v, crc, wantCRC)
	*bp = buf[:0]
	vpool.Put(bp)
	return ok
}

func main() {
	seed := loadSeed()

	sink := make([]byte, 0, valueCount*valueSize)
	offsets := make([]int, valueCount+1)
	crcref := make([]uint32, valueCount)

	for v := 0; v < valueCount; v++ {
		offsets[v] = len(sink)
		var crc uint32
		sink, crc = writeValue(sink, v, seed)
		crcref[v] = crc
		// 每写完一个大 value 就制造垃圾并 GC；sink 仍被持有，内容不得丢。
		gcStress()
		ckf(len(sink) == (v+1)*valueSize, "sink length after value=%d got=%d", v, len(sink))
	}
	offsets[valueCount] = len(sink)

	// 读回前再触发一次全量 GC，池可能被清空。
	runtime.GC()
	for v := 0; v < valueCount; v++ {
		readValue(sink[offsets[v]:offsets[v+1]], v, seed, crcref[v])
	}

	// 池化缓冲跨 GC 复用：借出后立即写入标记，GC 后仍可读。
	bp := vpool.Get().(*[]byte)
	buf := (*bp)[:0]
	buf = append(buf, "pooled-buffer-after-gc"...)
	gcStress()
	ck(string(buf) == "pooled-buffer-after-gc", "pooled buffer content after GC")
	*bp = buf[:0]
	vpool.Put(bp)

	runtime.KeepAlive(sink)
	finish("l05_stream_large_value_pooled", seed)
}
