// L05: 模拟 badger y.Allocator：sync.Pool 复用 byte slice，Reset 后编码记录，
// 循环中穿插垃圾分配与 runtime.GC，校验池对象长度/内容与编码结果。
package main

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"os"
	"runtime"
	"strconv"
	"sync"
	"sync/atomic"
)

var (
	failed int32
	gcSink [][]byte
	alloc  = sync.Pool{New: func() interface{} { return &buffer{b: make([]byte, 0, 4096)} }}
)

type buffer struct {
	b []byte
}

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

func appendRecord(dst []byte, id int, key, val []byte) []byte {
	dst = binary.AppendUvarint(dst, uint64(id))
	dst = binary.AppendUvarint(dst, uint64(len(key)))
	dst = append(dst, key...)
	dst = binary.AppendUvarint(dst, uint64(len(val)))
	dst = append(dst, val...)
	return binary.LittleEndian.AppendUint32(dst, crc32.ChecksumIEEE(dst))
}

func decodeRecord(src []byte, wantID int) (key, val []byte, ok bool) {
	if len(src) < 4 {
		return nil, nil, false
	}
	body := src[:len(src)-4]
	if crc32.ChecksumIEEE(body) != binary.LittleEndian.Uint32(src[len(src)-4:]) {
		return nil, nil, false
	}
	p := body
	id, n := binary.Uvarint(p)
	if n <= 0 || int(id) != wantID {
		return nil, nil, false
	}
	p = p[n:]
	kl, n := binary.Uvarint(p)
	if n <= 0 || kl > uint64(len(p)-n) {
		return nil, nil, false
	}
	p = p[n:]
	key = p[:kl]
	p = p[kl:]
	vl, n := binary.Uvarint(p)
	if n <= 0 || vl > uint64(len(p)-n) {
		return nil, nil, false
	}
	p = p[n:]
	val = p[:vl]
	p = p[vl:]
	if len(p) != 0 {
		return nil, nil, false
	}
	return key, val, true
}

func main() {
	seed := loadSeed()
	r := newRNG(seed)

	const records = 600
	snap := make([][]byte, 0, records/50+1)
	snapID := make([]int, 0, cap(snap))
	snapCRC := make([]uint32, 0, cap(snap))
	snapLen := make([]int, 0, cap(snap))
	snapKey := make([]string, 0, cap(snap))

	for id := 0; id < records; id++ {
		bufp := alloc.Get().(*buffer)
		buf := bufp.b
		ckf(len(buf) == 0, "pooled buffer not reset before reuse: id=%d len=%d", id, len(buf))

		key := []byte(fmt.Sprintf("key-%04d", id))
		val := make([]byte, 64+r.intn(960))
		base := byte(seed + uint64(id)*13)
		for i := range val {
			val[i] = byte((int(base) + i*7) % 251)
		}
		valCRC := crc32.ChecksumIEEE(val)

		buf = buf[:0]
		buf = appendRecord(buf, id, key, val)
		key2, val2, ok := decodeRecord(buf, id)
		if !ok {
			ckf(false, "record=%d decode failed", id)
		} else {
			ckf(bytes.Equal(key2, key), "record=%d key mismatch", id)
			ckf(bytes.Equal(val2, val), "record=%d value mismatch", id)
			ckf(crc32.ChecksumIEEE(val2) == valCRC, "record=%d checksum mismatch", id)
		}

		// 定期缓存编码结果，之后跨 GC 再解，确认池缓冲复用没有改写副本。
		if id%50 == 0 {
			snap = append(snap, append([]byte(nil), buf...))
			snapID = append(snapID, id)
			snapCRC = append(snapCRC, valCRC)
			snapLen = append(snapLen, len(val))
			snapKey = append(snapKey, string(key))
		}

		bufp.b = buf[:0] // Reset 语义：归还前长度归零，容量保留
		alloc.Put(bufp)

		if id%20 == 0 {
			gcStress()
		}
	}

	runtime.GC()
	for k, enc := range snap {
		key, val, ok := decodeRecord(enc, snapID[k])
		if !ok {
			ckf(false, "snapshot record=%d decode after GC failed", snapID[k])
			continue
		}
		ckf(string(key) == snapKey[k], "snapshot record=%d key after GC mismatch", snapID[k])
		ckf(len(val) == snapLen[k], "snapshot record=%d len after GC got=%d want=%d", snapID[k], len(val), snapLen[k])
		ckf(crc32.ChecksumIEEE(val) == snapCRC[k], "snapshot record=%d checksum after GC mismatch", snapID[k])
	}

	// 池被 GC 清空后再次借出：New 对象或旧对象都必须 len=0、cap 可用。
	gcStress()
	for k := 0; k < 16; k++ {
		bufp := alloc.Get().(*buffer)
		ck(len(bufp.b) == 0, "post-GC pooled buffer len != 0")
		ckf(cap(bufp.b) >= 64, "post-GC pooled buffer cap too small: %d", cap(bufp.b))
		bufp.b = append(bufp.b[:0], byte(k), byte(k+1), byte(k+2))
		if k%4 == 0 {
			runtime.GC()
		}
		ck(len(bufp.b) == 3 && bufp.b[0] == byte(k) && bufp.b[2] == byte(k+2), "post-GC buffer write")
		bufp.b = bufp.b[:0]
		alloc.Put(bufp)
	}

	finish("l05_allocator_reset_pool", seed)
}
