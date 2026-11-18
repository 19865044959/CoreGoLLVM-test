// L13: strings.Reader 的 Read/ReadAt/Seek/UnreadByte/ReadRune/WriteTo/Reset 状态机与 GC。
package main

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"runtime"
	"strconv"
	"strings"
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

func main() {
	curSeed = envSeed()

	const size = 4096
	payload := make([]byte, size)
	for i := range payload {
		payload[i] = byte(i*37 + 11 + int(curSeed%7))
	}
	text := string(payload)
	r := strings.NewReader(text)
	ck(r.Len() == size && r.Size() == int64(size), "strings.Reader: initial len/size")

	buf100 := make([]byte, 100)
	if n, err := r.Read(buf100); n != 100 || err != nil || !bytes.Equal(buf100, payload[:100]) {
		ckSeed(false, "strings.Reader: first read")
	}
	ck(r.Len() == size-100, "strings.Reader: len after read")

	// ReadAt 不改变当前位置。
	rbuf := make([]byte, 64)
	if n, err := r.ReadAt(rbuf, 1000); n != 64 || err != nil || !bytes.Equal(rbuf, payload[1000:1064]) {
		ckSeed(false, "strings.Reader: ReadAt content")
	}
	next := make([]byte, 10)
	if n, err := r.Read(next); n != 10 || err != nil || !bytes.Equal(next, payload[100:110]) {
		ckSeed(false, "strings.Reader: ReadAt changed offset")
	}

	// Seek from end，读最后 128 字节。
	if pos, err := r.Seek(-128, io.SeekEnd); err != nil || pos != int64(size-128) {
		ckSeed(false, "strings.Reader: Seek from end")
	}
	tail := make([]byte, 128)
	if n, err := r.Read(tail); n != 128 || (err != nil && err != io.EOF) || !bytes.Equal(tail, payload[size-128:]) {
		ckSeed(false, "strings.Reader: tail read")
	}
	ck(r.Len() == 0, "strings.Reader: len at end")
	if n, err := r.Read(tail[:1]); n != 0 || err != io.EOF {
		ckSeed(false, "strings.Reader: read at end")
	}

	// UnreadByte / ReadByte 状态。
	if pos, err := r.Seek(0, io.SeekStart); err != nil || pos != 0 {
		ckSeed(false, "strings.Reader: Seek start")
	}
	b1, err := r.ReadByte()
	ck(err == nil && b1 == payload[0], "strings.Reader: ReadByte")
	ck(r.UnreadByte() == nil, "strings.Reader: UnreadByte")
	b1, err = r.ReadByte()
	ck(err == nil && b1 == payload[0], "strings.Reader: ReadByte after unread")
	ck(r.UnreadByte() == nil, "strings.Reader: second UnreadByte setup")
	if err := r.UnreadByte(); err == nil {
		ckSeed(false, "strings.Reader: double UnreadByte must fail")
	}
	b1, err = r.ReadByte()
	ck(err == nil && b1 == payload[0], "strings.Reader: state after failed UnreadByte")

	// 非法 Seek 不改变位置。
	if _, err := r.Seek(-1, io.SeekStart); err == nil {
		ckSeed(false, "strings.Reader: negative Seek must fail")
	}
	b1, err = r.ReadByte()
	ck(err == nil && b1 == payload[1], "strings.Reader: Seek error changed offset")

	// ReadAt 越过末尾。
	short := make([]byte, 16)
	if n, err := r.ReadAt(short, int64(size-6)); n != 6 || err != io.EOF || !bytes.Equal(short[:6], payload[size-6:]) {
		ckSeed(false, "strings.Reader: ReadAt past end")
	}

	// WriteTo 从当前位置写到末尾并耗尽 reader。
	if _, err := r.Seek(0, io.SeekStart); err != nil {
		ckSeed(false, "strings.Reader: seek for WriteTo")
	}
	var sink bytes.Buffer
	wn, werr := r.WriteTo(&sink)
	if werr != nil || wn != int64(size) || !bytes.Equal(sink.Bytes(), payload) {
		ckSeed(false, "strings.Reader: WriteTo content")
	}
	ck(r.Len() == 0, "strings.Reader: WriteTo did not drain")
	if n, err := r.Read(buf100[:1]); n != 0 || err != io.EOF {
		ckSeed(false, "strings.Reader: read after WriteTo")
	}

	// ReadRune / UnreadRune。
	r.Reset("é中z")
	if r.Len() != 6 || r.Size() != 6 {
		ckSeed(false, "strings.Reader: Reset size")
	}
	ru, sz, err := r.ReadRune()
	ck(err == nil && ru == 'é' && sz == 2, "strings.Reader: ReadRune 2-byte")
	ck(r.UnreadRune() == nil, "strings.Reader: UnreadRune")
	ru, sz, err = r.ReadRune()
	ck(err == nil && ru == 'é' && sz == 2, "strings.Reader: ReadRune after unread")
	ru, sz, err = r.ReadRune()
	ck(err == nil && ru == '中' && sz == 3, "strings.Reader: ReadRune 3-byte")
	ck(r.UnreadRune() == nil, "strings.Reader: UnreadRune cjk")
	ru, _, err = r.ReadRune()
	ck(err == nil && ru == '中', "strings.Reader: ReadRune cjk after unread")
	ru, sz, err = r.ReadRune()
	ck(err == nil && ru == 'z' && sz == 1, "strings.Reader: ReadRune ascii")
	if _, _, err := r.ReadRune(); err != io.EOF {
		ckSeed(false, "strings.Reader: ReadRune at EOF")
	}

	// 空串 reader 边界。
	empty := strings.NewReader("")
	if n, err := empty.Read(buf100); n != 0 || err != io.EOF {
		ckSeed(false, "strings.Reader: empty read")
	}
	if n, err := empty.ReadAt(buf100, 0); n != 0 || err != io.EOF {
		ckSeed(false, "strings.Reader: empty ReadAt")
	}
	if pos, err := empty.Seek(0, io.SeekEnd); err != nil || pos != 0 {
		ckSeed(false, "strings.Reader: empty Seek end")
	}
	if err := empty.UnreadByte(); err == nil {
		ckSeed(false, "strings.Reader: empty UnreadByte must fail")
	}

	// GC 后仍可完整读取。
	gcStress()
	r.Reset(text)
	all, err := io.ReadAll(r)
	ck(err == nil && bytes.Equal(all, payload), "strings.Reader: content after GC")
	gcStress()
	runtime.KeepAlive(text)
	runtime.KeepAlive(payload)
	finish("l13_strings_reader_statemachine")
}
