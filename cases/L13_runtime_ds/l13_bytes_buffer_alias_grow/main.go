// L13: bytes.Buffer 的 Bytes 别名、Grow/Truncate/Next/UnreadByte/ReadFrom/WriteTo 语义与 GC。
package main

import (
	"bytes"
	"fmt"
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

	var b bytes.Buffer
	parts := []string{"alpha:", "beta/", "gamma-", "delta"}
	for _, p := range parts {
		b.WriteString(p)
	}
	full := strings.Join(parts, "")
	ck(b.Len() == len(full), "Buffer: len after writes")
	ck(b.String() == full, "Buffer: String content")
	ck(bytes.Equal(b.Bytes(), []byte(full)), "Buffer: Bytes content")

	// 取快照后继续写，快照必须稳定。
	snap := append([]byte(nil), b.Bytes()...)
	b.WriteByte('!')
	ck(bytes.Equal(snap, []byte(full)), "Buffer: snapshot changed by later write")
	ck(b.String() == full+"!", "Buffer: content after WriteByte")

	// Grow 保留内容并提升容量。
	beforeGrow := b.String()
	growN := 512 + int(curSeed%512)
	b.Grow(growN)
	ck(b.String() == beforeGrow, "Buffer: Grow changed content")
	if cap(b.Bytes()) < b.Len()+growN {
		ckSeed(false, "Buffer: Grow capacity insufficient")
	}

	// Truncate 截断；越界必须 panic 且状态仍可用。
	b.Truncate(5)
	ck(b.String() == beforeGrow[:5], "Buffer: Truncate prefix")
	func() {
		defer func() {
			ck(recover() != nil, "Buffer: Truncate beyond len must panic")
		}()
		b.Truncate(6)
	}()
	ck(b.String() == beforeGrow[:5], "Buffer: state after Truncate panic")
	b.Truncate(0)
	ck(b.Len() == 0, "Buffer: Truncate to zero")

	// Next / ReadByte / UnreadByte。
	b.Reset()
	b.WriteString("0123456789")
	nxt := b.Next(4)
	ck(string(nxt) == "0123", "Buffer: Next content")
	ck(b.Len() == 6, "Buffer: Next len")
	c, err := b.ReadByte()
	ck(err == nil && c == '4', "Buffer: ReadByte after Next")
	ck(b.UnreadByte() == nil, "Buffer: UnreadByte")
	c, err = b.ReadByte()
	ck(err == nil && c == '4', "Buffer: ReadByte after UnreadByte")
	if err := b.UnreadByte(); err != nil {
		ckSeed(false, "Buffer: second UnreadByte")
	}
	b.WriteByte('X')
	if err := b.UnreadByte(); err == nil {
		ckSeed(false, "Buffer: UnreadByte after write must fail")
	}

	// ReadFrom 追加，WriteTo 只写未读部分并耗尽。
	b.Reset()
	prefix := "keep=1;"
	rest := "drop=2"
	b.WriteString(prefix + rest)
	if n, err := b.ReadFrom(strings.NewReader("")); n != 0 || err != nil {
		ckSeed(false, "Buffer: empty ReadFrom")
	}
	payload := "READFROM-payload-0123456789"
	b.Reset()
	if n, err := b.ReadFrom(strings.NewReader(payload)); err != nil || n != int64(len(payload)) {
		ckSeed(false, "Buffer: ReadFrom count")
	}
	ck(b.String() == payload, "Buffer: ReadFrom content")
	b.Reset()
	b.WriteString(prefix + rest)
	_ = b.Next(len(prefix))
	var sink bytes.Buffer
	if n, err := b.WriteTo(&sink); err != nil || n != int64(len(rest)) || sink.String() != rest {
		ckSeed(false, "Buffer: WriteTo content")
	}
	ck(b.Len() == 0, "Buffer: WriteTo did not drain")

	// ReadRune / UnreadRune。
	b.Reset()
	b.WriteString("é中z")
	ru, size, err := b.ReadRune()
	ck(err == nil && ru == 'é' && size == 2, "Buffer: ReadRune 2-byte")
	ck(b.UnreadRune() == nil, "Buffer: UnreadRune rune")
	ru, _, err = b.ReadRune()
	ck(err == nil && ru == 'é', "Buffer: ReadRune after UnreadRune")
	ru, size, err = b.ReadRune()
	ck(err == nil && ru == '中' && size == 3, "Buffer: ReadRune 3-byte")
	if err := b.UnreadRune(); err != nil {
		ckSeed(false, "Buffer: UnreadRune after 3-byte rune")
	}
	ru, _, err = b.ReadRune()
	ck(err == nil && ru == '中', "Buffer: ReadRune after UnreadRune")
	b.WriteByte('X')
	if err := b.UnreadByte(); err == nil {
		ckSeed(false, "Buffer: UnreadByte after write must fail")
	}

	// Reset 后重新写入，旧内容不可见。
	b.Reset()
	ck(b.Len() == 0 && b.String() == "", "Buffer: Reset len/content")
	b.WriteString("fresh")
	ck(b.String() == "fresh", "Buffer: write after Reset")

	// GC 后内容仍完整。
	gcStress()
	var gb bytes.Buffer
	gb.WriteString(payload)
	ck(gb.String() == payload, "Buffer: content after GC")
	gcStress()

	runtime.KeepAlive(b)
	runtime.KeepAlive(gb)
	runtime.KeepAlive(snap)
	finish("l13_bytes_buffer_alias_grow")
}
