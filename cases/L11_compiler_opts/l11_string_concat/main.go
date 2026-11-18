// L11: 循环字符串拼接（+=）、strings.Builder、[]byte append 三种路径
// 内容/字节长度/rune 数必须一致，GC 后保留的字符串内容不变。
package main

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"unicode/utf8"
)

var (
	failed int32
	gcSink [][]byte
	seed   uint64 = 1
)

func ck(ok bool, msg string) {
	if !ok {
		atomic.AddInt32(&failed, 1)
		fmt.Printf("FAIL: %s (UAF_SEED=%d)\n", msg, seed)
	}
}

func gcStress() {
	for i := 0; i < 16; i++ {
		b := make([]byte, 4096)
		b[0] = byte(i)
		b[len(b)-1] = byte(255 - i)
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

func tortureMultiplier() int {
	s := os.Getenv("TORTURE_ROUNDS")
	if s == "" {
		return 1
	}
	n, err := strconv.Atoi(s)
	if err != nil || n <= 0 {
		return 1
	}
	if n > 100 {
		n = 100
	}
	return n
}

func finish(name string) {
	if n := atomic.LoadInt32(&failed); n != 0 {
		fmt.Printf("FAILED %s (%d failed checks, seed=%d)\n", name, n, seed)
		os.Exit(1)
	}
	fmt.Println("ALL PASS " + name)
}

func concatPlus(parts []string) string {
	s := ""
	for _, p := range parts {
		s += p
	}
	return s
}

func concatBuilder(parts []string) string {
	total := 0
	for _, p := range parts {
		total += len(p)
	}
	var b strings.Builder
	b.Grow(total)
	for _, p := range parts {
		b.WriteString(p)
	}
	return b.String()
}

func concatBytes(parts []string) string {
	total := 0
	for _, p := range parts {
		total += len(p)
	}
	buf := make([]byte, 0, total)
	for _, p := range parts {
		buf = append(buf, p...)
	}
	return string(buf)
}

func makeParts(n int) ([]string, int, int) {
	parts := make([]string, n)
	byteLen, runeLen := 0, 0
	for i := range parts {
		switch i % 5 {
		case 0:
			parts[i] = strconv.Itoa(i)
		case 1:
			parts[i] = "ab"
		case 2:
			parts[i] = "世界"
		case 3:
			parts[i] = "z"
		default:
			parts[i] = "-" + strconv.Itoa(i%97)
		}
		byteLen += len(parts[i])
		runeLen += utf8.RuneCountInString(parts[i])
	}
	return parts, byteLen, runeLen
}

func main() {
	seed = loadSeed()
	rounds := 12 * tortureMultiplier()
	base := 4096 + int(seed%2048)
	kept := make([]string, 0, rounds)
	keptRunes := make([]int, 0, rounds)
	keptLens := make([]int, 0, rounds)

	for r := 0; r < rounds; r++ {
		n := base + r%4
		parts, byteLen, runeLen := makeParts(n)
		plus := concatPlus(parts)
		builder := concatBuilder(parts)
		bytes := concatBytes(parts)

		ck(plus == builder, "plus vs builder mismatch")
		ck(builder == bytes, "builder vs bytes mismatch")
		ck(len(plus) == byteLen, "byte length mismatch")
		ck(utf8.RuneCountInString(plus) == runeLen, "rune count mismatch")
		ck(strings.Count(plus, "世界") == (n+2)/5, "unicode fragment count mismatch")
		ck(strings.HasPrefix(plus, strings.Join(parts[:5], "")), "prefix mismatch")
		ck(strings.HasSuffix(plus, parts[n-1]), "suffix mismatch")

		off := 0
		for i := 0; i < 1000; i++ {
			off += len(parts[i])
		}
		mid := parts[1000]
		ck(plus[off:off+len(mid)] == mid, "interior byte offset mismatch")

		kept = append(kept, plus)
		keptRunes = append(keptRunes, runeLen)
		keptLens = append(keptLens, byteLen)
		gcStress()
	}

	gcStress()
	for i := range kept {
		ck(len(kept[i]) == keptLens[i], "kept string length corrupted after GC")
		ck(utf8.RuneCountInString(kept[i]) == keptRunes[i], "kept string rune count corrupted after GC")
		ck(strings.Contains(kept[i], "世界"), "kept string content corrupted after GC")
	}

	finish("l11_string_concat")
}
