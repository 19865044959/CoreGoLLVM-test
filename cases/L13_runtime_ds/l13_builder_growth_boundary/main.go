// L13: strings.Builder 增长：跨块 WriteString/Write/WriteRune、Grow、Reset 复用与 Copy 独立性。
package main

import (
	"fmt"
	"math/rand"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"unicode/utf8"
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

func pattern(n int, tag byte, seed int64) []byte {
	p := make([]byte, n)
	for i := range p {
		p[i] = byte(int(tag)*37 + i*11 + int(seed&0x3f))
	}
	return p
}

var chunkSizes = []int{1, 2, 3, 7, 31, 63, 64, 65, 127, 128, 129, 255, 256, 257,
	511, 512, 513, 1023, 1024, 1025, 4095, 4096, 4097, 8191, 8192, 8193, 16384, 65535, 65536, 65537}

var runePool = []rune{'a', 'Z', '0', 'é', '中', '🙂', '́', '�'}

func main() {
	curSeed = envSeed()
	r := rand.New(rand.NewSource(curSeed))

	// 1) 混合写入：不同大小/不同方法跨越 Builder 内部缓冲增长边界。
	var b strings.Builder
	var want []byte
	nextRune := r.Intn(len(runePool))
	for i := 0; i < 360; i++ {
		switch i % 6 {
		case 0, 1:
			n := chunkSizes[(i*7+int(curSeed&0x7fffffff))%len(chunkSizes)]
			p := pattern(n, byte(i), curSeed)
			b.Write(p)
			want = append(want, p...)
		case 2:
			s := string(pattern(1+(i*13)%97, byte(i+1), curSeed))
			b.WriteString(s)
			want = append(want, s...)
		case 3:
			ch := byte((i*29 + int(curSeed)) & 0xff)
			b.WriteByte(ch)
			want = append(want, ch)
		case 4:
			ru := runePool[nextRune%len(runePool)]
			nextRune++
			b.WriteRune(ru)
			want = utf8.AppendRune(want, ru)
		case 5:
			n := (i * 61) % 300
			b.Grow(n)
			p := pattern(n, byte(i+2), curSeed)
			b.Write(p)
			want = append(want, p...)
		}
		if i%45 == 44 {
			ckSeed(b.Len() == len(want), fmt.Sprintf("builder: Len mismatch at step %d", i))
			ckSeed(b.String() == string(want), fmt.Sprintf("builder: content mismatch at step %d", i))
			gcStress()
			ckSeed(b.String() == string(want), fmt.Sprintf("builder: content mismatch after GC at step %d", i))
		}
	}
	ck(b.String() == string(want), "builder: final content")
	gcStress()
	ck(b.String() == string(want), "builder: final content after GC")

	// 纯 rune 写入路径：WriteRune 输出必须是合法 UTF-8 且 rune 数精确。
	var u strings.Builder
	nRunes := 0
	for i := 0; i < 10000; i++ {
		u.WriteRune(runePool[i%len(runePool)])
		nRunes++
	}
	ck(utf8.ValidString(u.String()), "builder runes: invalid UTF-8 output")
	ck(utf8.RuneCountInString(u.String()) == nRunes, "builder runes: rune count mismatch")
	gcStress()
	ck(utf8.ValidString(u.String()) && utf8.RuneCountInString(u.String()) == nRunes, "builder runes: corrupted after GC")
	runtime.KeepAlive(u)

	// 2) Reset 复用：同 Builder 反复重置，长度/内容必须完全重置。
	var rb strings.Builder
	for cycle := 0; cycle < 50; cycle++ {
		rb.Reset()
		ck(rb.Len() == 0, "builder reset: Len not zero")
		ck(rb.String() == "", "builder reset: String not empty")
		var rw []byte
		for k := 0; k < 3+cycle%5; k++ {
			p := pattern(1+(cycle*17+k*23)%700, byte(cycle+k), curSeed)
			rb.Write(p)
			rw = append(rw, p...)
		}
		if rb.String() != string(rw) {
			ckSeed(false, fmt.Sprintf("builder reset: content mismatch cycle %d", cycle))
			break
		}
		if cycle%13 == 12 {
			gcStress()
			ck(rb.String() == string(rw), "builder reset: content after GC")
		}
	}
	rb.Reset()

	// 3) 克隆：目标保留已有前缀并复制源内容；源后续被重置不影响目标。
	var src strings.Builder
	for i := 0; i < 40; i++ {
		p := pattern(1+(i*97)%2049, byte(i), curSeed)
		src.Write(p)
	}
	srcWant := src.String()
	var dst strings.Builder
	dst.WriteString("PREFIX:")
	dst.WriteString(src.String())
	ckSeed(dst.String() == "PREFIX:"+srcWant, "builder clone: dst content")
	ck(dst.Cap() >= dst.Len() && dst.Len() == len("PREFIX:")+len(srcWant), "builder clone: cap/len")
	gcStress()
	ckSeed(dst.String() == "PREFIX:"+srcWant, "builder clone: dst after GC")
	src.Reset()
	src.WriteString("rewritten")
	ckSeed(dst.String() == "PREFIX:"+srcWant, "builder clone: dst changed after source reuse")
	var empty strings.Builder
	dst.WriteString(empty.String())
	ckSeed(dst.String() == "PREFIX:"+srcWant, "builder clone: empty source altered dst")
	var dst2 strings.Builder
	dst2.WriteString("X")
	dst2.WriteString(empty.String())
	ck(dst2.String() == "X", "builder clone: empty copy changed dst2")
	gcStress()
	ckSeed(dst.String() == "PREFIX:"+srcWant, "builder clone: dst after final GC")

	// 4) 大块 64KB 附近反复增长，校验跨块字节序不丢。
	var big strings.Builder
	var bigHint []uint32
	var bigWant []byte
	for i := 0; i < 90; i++ {
		n := 65536 + (i%7-3)*17
		p := pattern(n, byte(i*5), curSeed+int64(i))
		big.Write(p)
		bigWant = append(bigWant, p...)
		bigHint = append(bigHint, uint32(len(bigWant)))
		if i%30 == 29 {
			gcStress()
			ckSeed(big.String() == string(bigWant), fmt.Sprintf("builder big: content mismatch at block %d", i))
			for _, l := range bigHint {
				ckSeed(len(big.String()) >= int(l), "builder big: length regression")
			}
		}
	}
	ck(big.String() == string(bigWant), "builder big: final content")
	ck(len(big.String()) == len(bigWant), "builder big: final length")

	runtime.KeepAlive(b)
	runtime.KeepAlive(big)
	runtime.KeepAlive(want)
	runtime.KeepAlive(bigWant)
	finish("l13_builder_growth_boundary")
}
