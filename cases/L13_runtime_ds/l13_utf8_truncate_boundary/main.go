// L13: UTF-8 截断：按字节上限截到 rune 边界、非法序列前缀语义、幂等性与截断 key 跨 GC。
package main

import (
	"fmt"
	"math/rand"
	"os"
	"runtime"
	"sort"
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

// truncateUTF8 返回不超过 max 字节的最长前缀；若 max 落在 rune 中间，
// 回退到该 rune 的起始位置。
func truncateUTF8(s string, max int) string {
	if max <= 0 {
		return ""
	}
	if max >= len(s) {
		return s
	}
	i := max
	for i > 0 && !utf8.RuneStart(s[i]) {
		i--
	}
	return s[:i]
}

// runeEnds 返回每个 rune 结束后的字节偏移（仅对合法串有意义）。
func runeEnds(s string) []int {
	ends := make([]int, 0, len(s))
	for i := 0; i < len(s); {
		_, sz := utf8.DecodeRuneInString(s[i:])
		i += sz
		ends = append(ends, i)
	}
	return ends
}

func expectedEnd(ends []int, limit int) int {
	i := sort.Search(len(ends), func(i int) bool { return ends[i] > limit })
	if i == 0 {
		return 0
	}
	return ends[i-1]
}

func checkValid(s string, limits []int, tag string) {
	ends := runeEnds(s)
	for _, lim := range limits {
		out := truncateUTF8(s, lim)
		ckSeed(len(out) <= lim || lim < 0, fmt.Sprintf("%s: byte limit violated lim=%d", tag, lim))
		if !strings.HasPrefix(s, out) {
			ckSeed(false, fmt.Sprintf("%s: output not prefix lim=%d", tag, lim))
			continue
		}
		if lim <= 0 && out != "" {
			ckSeed(false, fmt.Sprintf("%s: non-empty output for lim=%d", tag, lim))
		}
		if !utf8.ValidString(out) {
			ckSeed(false, fmt.Sprintf("%s: output invalid UTF-8 lim=%d", tag, lim))
		}
		want := expectedEnd(ends, lim)
		ckSeed(len(out) == want, fmt.Sprintf("%s: boundary wrong lim=%d got=%d want=%d", tag, lim, len(out), want))
		ckSeed(utf8.RuneCountInString(out) == sort.Search(len(ends), func(i int) bool { return ends[i] > lim }),
			fmt.Sprintf("%s: rune count wrong lim=%d", tag, lim))
		ckSeed(truncateUTF8(out, lim) == out, fmt.Sprintf("%s: not idempotent lim=%d", tag, lim))
		if len(out) < len(s) {
			_, sz := utf8.DecodeRuneInString(s[len(out):])
			ckSeed(len(out)+sz > lim, fmt.Sprintf("%s: cut before rune fit lim=%d", tag, lim))
		}
	}
}

func checkInvalid(s string, limits []int, tag string) {
	for _, lim := range limits {
		out := truncateUTF8(s, lim)
		ckSeed(len(out) <= lim || lim < 0, fmt.Sprintf("%s: byte limit violated lim=%d", tag, lim))
		ckSeed(strings.HasPrefix(s, out), fmt.Sprintf("%s: output not prefix lim=%d", tag, lim))
		ckSeed(truncateUTF8(out, lim) == out, fmt.Sprintf("%s: not idempotent lim=%d", tag, lim))
	}
}

func main() {
	curSeed = envSeed()
	r := rand.New(rand.NewSource(curSeed))

	validFixed := []string{
		"", "a", "abc", "é", "中", "🙂", "aé中🙂", "é", "한국어", "Привет",
		strings.Repeat("aé中🙂", 200), strings.Repeat("x", 4096), strings.Repeat("中", 600),
	}
	for si, s := range validFixed {
		limits := make([]int, 0, len(s)+3)
		for i := 0; i <= len(s)+2; i++ {
			limits = append(limits, i)
		}
		checkValid(s, limits, fmt.Sprintf("valid-fixed-%d", si))
		gcStress()
	}

	invalidFixed := [][]byte{
		{0x80}, {0xbf}, {0xc3}, {0xe4, 0xb8}, {0xf0, 0x9f, 0x99}, {0xff, 0xfe},
		{0xc0, 0xaf}, {0xed, 0xa0, 0x80}, {0xf4, 0x90, 0x80, 0x80}, {0x61, 0x80, 0x62},
	}
	for si, raw := range invalidFixed {
		limits := make([]int, 0, len(raw)+1)
		for i := 0; i <= len(raw); i++ {
			limits = append(limits, i)
		}
		checkInvalid(string(raw), limits, fmt.Sprintf("invalid-fixed-%d", si))
		gcStress()
	}

	// 随机合法串：所有字节截断点都必须落在 rune 边界。
	alphabet := []rune{'a', 'z', '0', 'é', '中', '한', '🙂', '𝄞', '́', '�'}
	keyMap := make(map[string]int)
	keyWant := make(map[string]int)
	for si := 0; si < 180; si++ {
		var b strings.Builder
		nRunes := r.Intn(90)
		for k := 0; k < nRunes; k++ {
			b.WriteRune(alphabet[r.Intn(len(alphabet))])
		}
		s := b.String()
		limits := make([]int, 0, len(s)+1)
		for i := 0; i <= len(s); i++ {
			limits = append(limits, i)
		}
		checkValid(s, limits, fmt.Sprintf("valid-rand-%d", si))
		if si%9 == 0 {
			gcStress()
		}
		if si < 100 {
			for _, lim := range []int{0, 1, 3, 7, 16, 64, 255, 256, 1024} {
				out := truncateUTF8(s, lim)
				keyMap[out] = utf8.RuneCountInString(out)
				keyWant[out] = utf8.RuneCountInString(out)
			}
		}
		if si%30 == 29 {
			ckSeed(len(keyMap) > 0, "truncate keys: empty map")
		}
	}

	// 随机非法字节串：前缀 + 幂等，不得越界。
	for si := 0; si < 120; si++ {
		n := r.Intn(200)
		raw := make([]byte, n)
		for i := range raw {
			raw[i] = byte(r.Intn(256))
		}
		limits := make([]int, 0, n+1)
		for i := 0; i <= n; i++ {
			limits = append(limits, i)
		}
		checkInvalid(string(raw), limits, fmt.Sprintf("invalid-rand-%d", si))
		if si%17 == 16 {
			gcStress()
		}
	}

	// 截断结果作为 map key 跨 GC：同内容新字符串查询、rune 数一致。
	gcStress()
	for k, want := range keyWant {
		got, ok := keyMap[k]
		ckSeed(ok && got == want, "truncate keys: entry lost after GC")
		fresh := string(append([]byte(nil), []byte(k)...))
		got2, ok2 := keyMap[fresh]
		ckSeed(ok2 && got2 == want, "truncate keys: fresh lookup miss")
		ckSeed(utf8.ValidString(fresh) && utf8.RuneCountInString(fresh) == want, "truncate keys: rune count changed")
	}
	for _, budget := range []int{64, 255, 256, 1024} {
		for k := range keyWant {
			out := truncateUTF8(k, budget)
			ckSeed(len(out) <= budget && utf8.ValidString(out), "truncate keys: budget/validity violated")
			ckSeed(strings.HasPrefix(k, out), "truncate keys: result not prefix")
		}
	}

	runtime.KeepAlive(keyMap)
	runtime.KeepAlive(keyWant)
	finish("l13_utf8_truncate_boundary")
}
