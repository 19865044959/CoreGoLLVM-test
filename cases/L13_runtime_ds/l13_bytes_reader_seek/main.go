// L13: bytes.Reader 定位语义：Seek/ReadAt/ReadByte/UnreadByte/WriteTo 与底层缓冲跨 GC 存活。
package main

import (
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

func digest(p []byte) uint64 {
	var s uint64
	for _, c := range p {
		s = s*131 + uint64(c)
	}
	return s
}

type digestWriter struct {
	n   int
	sum uint64
}

func (w *digestWriter) Write(p []byte) (int, error) {
	for _, c := range p {
		w.sum = w.sum*131 + uint64(c)
	}
	w.n += len(p)
	return len(p), nil
}

func main() {
	curSeed = envSeed()
	rnd := rand.New(rand.NewSource(curSeed))

	n := 180000 + int(curSeed%997)*17
	payload := make([]byte, n)
	for i := range payload {
		payload[i] = byte(i*17 + int(curSeed) + i/251)
	}
	rd := bytes.NewReader(payload)
	ck(rd.Size() == int64(n) && rd.Len() == n, "reader: size/len")

	// 1) 顺序读取前 4000 字节。
	head := make([]byte, 4000)
	if got, err := io.ReadFull(rd, head); err != nil || got != len(head) {
		ck(false, "reader: sequential head read")
	}
	ck(bytes.Equal(head, payload[:4000]), "reader: sequential head data")
	ck(rd.Len() == n-4000, "reader: len after head")

	// 2) Seek/ReadByte/UnreadByte。
	if off, err := rd.Seek(12345, io.SeekStart); err != nil || off != 12345 {
		ck(false, "reader: seek start")
	}
	b1, err := rd.ReadByte()
	ck(err == nil && b1 == payload[12345], "reader: read byte after seek")
	ck(rd.UnreadByte() == nil, "reader: unread byte")
	b2, err := rd.ReadByte()
	ck(err == nil && b2 == b1, "reader: read byte after unread")

	// 3) ReadAt 使用绝对偏移，不改变当前 Seek 位置。
	at := 54321
	rb := make([]byte, 777)
	if got, err := rd.ReadAt(rb, int64(at)); err != nil || got != len(rb) {
		ck(false, "reader: ReadAt")
	}
	ck(bytes.Equal(rb, payload[at:at+len(rb)]), "reader: ReadAt data")
	cur, _ := rd.Seek(0, io.SeekCurrent)
	ck(cur == 12346, "reader: ReadAt changed seek position")
	b3, err := rd.ReadByte()
	ck(err == nil && b3 == payload[12346], "reader: position after ReadAt")

	// 4) SeekEnd 负偏移读取尾部。
	tailLen := 300
	if off, err := rd.Seek(int64(-tailLen), io.SeekEnd); err != nil || off != int64(n-tailLen) {
		ck(false, "reader: seek end")
	}
	tail := make([]byte, tailLen)
	if got, err := io.ReadFull(rd, tail); err != nil || got != tailLen {
		ck(false, "reader: tail read")
	}
	ck(bytes.Equal(tail, payload[n-tailLen:]), "reader: tail data")

	// 5) SeekCurrent 相对定位。
	rd.Seek(1000, io.SeekStart)
	rd.Seek(250, io.SeekCurrent)
	b4, err := rd.ReadByte()
	ck(err == nil && b4 == payload[1250], "reader: seek current")

	// 6) 非法位置与 EOF。
	if _, err := rd.Seek(-1, io.SeekStart); err == nil {
		ck(false, "reader: negative seek accepted")
	}
	rd.Seek(int64(n), io.SeekStart)
	if _, err := rd.ReadByte(); err != io.EOF {
		ck(false, "reader: read byte at EOF")
	}
	one := make([]byte, 1)
	if got, err := rd.Read(one); got != 0 || err != io.EOF {
		ck(false, "reader: read at EOF")
	}
	if _, err := rd.Seek(int64(n+100), io.SeekStart); err != nil {
		ck(false, "reader: seek beyond end rejected")
	}
	if _, err := rd.Read(one); err != io.EOF {
		ck(false, "reader: read beyond end")
	}
	short := make([]byte, 4)
	if got, err := rd.ReadAt(short, int64(n-2)); got != 2 || err != io.EOF {
		ck(false, "reader: partial ReadAt")
	}
	ck(bytes.Equal(short[:2], payload[n-2:]), "reader: partial ReadAt data")

	// 7) 全量顺序读取与内容校验。
	rd.Seek(0, io.SeekStart)
	full := make([]byte, 0, n)
	buf := make([]byte, 8192)
	for {
		got, err := rd.Read(buf)
		full = append(full, buf[:got]...)
		if err == io.EOF {
			break
		}
		if err != nil {
			ck(false, "reader: full read error")
			break
		}
	}
	ck(len(full) == n && bytes.Equal(full, payload), "reader: full content")

	// 8) WriteTo 从指定位置写出剩余内容。
	rd.Seek(777, io.SeekStart)
	w := &digestWriter{}
	nw, err := rd.WriteTo(w)
	ck(err == nil && nw == int64(n-777), "reader: WriteTo count")
	ck(w.n == n-777 && w.sum == digest(payload[777:]), "reader: WriteTo digest")
	ck(rd.Len() == 0, "reader: WriteTo did not drain")

	// 9) Reader 持有底层 payload 引用：GC 后修改 payload 应立即可见。
	gcStress()
	payload[0] ^= 0xff
	if off, err := rd.Seek(0, io.SeekStart); err != nil || off != 0 {
		ck(false, "reader: reseek after GC")
	}
	c, err := rd.ReadByte()
	ck(err == nil && c == payload[0], "reader: payload reference lost after GC")
	payload[0] ^= 0xff

	// 10) UAF_SEED 驱动的随机定位读取。
	for round := 0; round < 400; round++ {
		off := rnd.Intn(n)
		if _, err := rd.Seek(int64(off), io.SeekStart); err != nil {
			ckSeed(false, fmt.Sprintf("reader: random seek at %d", off))
			break
		}
		c, err := rd.ReadByte()
		if err != nil || c != payload[off] {
			ckSeed(false, fmt.Sprintf("reader: random read mismatch at %d", off))
			break
		}
		if round%100 == 99 {
			gcStress()
			if rd.Len() != n-off-1 {
				ckSeed(false, fmt.Sprintf("reader: len after random GC at %d", off))
				break
			}
		}
	}

	runtime.KeepAlive(rd)
	runtime.KeepAlive(payload)
	finish("l13_bytes_reader_seek")
}
