// L13: UTF-8 处理：合法性、rune 计数、rune 边界截断、非法序列与多字节 map key 跨 GC。
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

type uentry struct {
	runes int
	bytes int
}

type ucase struct {
	s       string
	valid   bool
	runes   int
	invalid int
}

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

// decodeStats 手工按 DecodeRuneInString 逐 rune 前进，size==1 的 RuneError 计为非法。
func decodeStats(s string) (runes, invalid int) {
	for len(s) > 0 {
		r, size := utf8.DecodeRuneInString(s)
		if r == utf8.RuneError && size == 1 {
			invalid++
		}
		runes++
		s = s[size:]
	}
	return runes, invalid
}

func main() {
	curSeed = envSeed()

	fixed := []ucase{
		{"", true, 0, 0},
		{"hello", true, 5, 0},
		{"héllo", true, 5, 0},
		{"你好世界", true, 4, 0},
		{"🙂🚀", true, 2, 0},
		{"áb", true, 3, 0},
		{"\x00", true, 1, 0},
		{"�", true, 1, 0},
		{"Καλημέρα", true, 8, 0},
		{"日本語テキスト", true, 7, 0},
		{"\xff", false, 1, 1},
		{"\x80", false, 1, 1},
		{"\xc3\x28", false, 2, 1},
		{"\xe2\x82", false, 2, 2},
		{"\xed\xa0\x80", false, 3, 3},
		{"\xf4\x90\x80\x80", false, 4, 4},
		{"a\xffb", false, 3, 1},
		{"\xc0\x80", false, 2, 2},
		{"\xf8\x88\x80\x80\x80", false, 5, 5},
	}

	for _, tc := range fixed {
		runes, invalid := decodeStats(tc.s)
		ck(utf8.ValidString(tc.s) == tc.valid, fmt.Sprintf("valid flag for %d-byte case", len(tc.s)))
		ck(utf8.Valid([]byte(tc.s)) == tc.valid, fmt.Sprintf("Valid([]byte) for %d-byte case", len(tc.s)))
		ck(runes == tc.runes && invalid == tc.invalid, fmt.Sprintf("decode stats for %d-byte case", len(tc.s)))
		ck(utf8.RuneCountInString(tc.s) == tc.runes, fmt.Sprintf("RuneCount for %d-byte case", len(tc.s)))
		if tc.valid {
			ck(string([]rune(tc.s)) == tc.s, fmt.Sprintf("rune roundtrip for %d-byte case", len(tc.s)))
			// 每个 rune 边界截断都必须仍是合法 UTF-8 且等于对应 rune 前缀。
			rs := []rune(tc.s)
			idx := 0
			for k := 0; k <= len(rs); k++ {
				prefix := tc.s[:idx]
				ck(utf8.ValidString(prefix), fmt.Sprintf("truncated prefix valid at rune %d", k))
				ck(prefix == string(rs[:k]), fmt.Sprintf("truncated prefix content at rune %d", k))
				if k < len(rs) {
					_, size := utf8.DecodeRuneInString(tc.s[idx:])
					idx += size
				}
			}
		} else {
			ck(invalid > 0, fmt.Sprintf("invalid case has no invalid rune (len=%d)", len(tc.s)))
			// 任意字节截断都满足 valid <=> 无非法 rune。
			for cut := 0; cut <= len(tc.s); cut++ {
				p := tc.s[:cut]
				pr, pi := decodeStats(p)
				ck(utf8.ValidString(p) == (pi == 0), fmt.Sprintf("byte-cut validity at %d", cut))
				ck(utf8.RuneCountInString(p) == pr, fmt.Sprintf("byte-cut rune count at %d", cut))
			}
		}
	}
	// 多字节字符从中间截断必须非法且按单字节 RuneError 计。
	s := "你"
	ck(!utf8.ValidString(s[:1]) && !utf8.ValidString(s[:2]), "mid-rune truncation must be invalid")
	if r, size := utf8.DecodeRuneInString(s[:1]); r != utf8.RuneError || size != 1 {
		ck(false, "mid-rune decode must be RuneError/size1")
	}

	// 合法/非法字符串作为 map key，GC 后仍可查。
	validMap := make(map[string]*uentry)
	invalidMap := make(map[string]*uentry)
	for _, tc := range fixed {
		e := &uentry{runes: tc.runes, bytes: len(tc.s)}
		if tc.valid {
			validMap[tc.s] = e
		} else {
			invalidMap[tc.s] = e
		}
	}
	gcStress()
	vkeys := make([]string, 0, len(validMap))
	for k := range validMap {
		vkeys = append(vkeys, k)
	}
	sort.Strings(vkeys)
	for _, k := range vkeys {
		e := validMap[k]
		if e == nil || e.runes != utf8.RuneCountInString(k) || e.bytes != len(k) || !utf8.ValidString(k) {
			ckSeed(false, "valid UTF-8 map key corrupted after GC")
			break
		}
	}
	for _, tc := range fixed {
		if tc.valid {
			continue
		}
		k2 := string([]byte(tc.s))
		e, ok := invalidMap[k2]
		if !ok || e == nil || e.bytes != len(tc.s) || utf8.ValidString(k2) {
			ckSeed(false, "invalid UTF-8 map key corrupted after GC")
			break
		}
	}
	gcStress()

	// 大字符串 rune 计数 map（固定顺序校验，不依赖遍历顺序）。
	big := strings.Repeat("aé你🙂", 250)
	counts := make(map[rune]int)
	for _, r := range big {
		counts[r]++
	}
	gcStress()
	for _, r := range []rune{'a', 'é', '你', '🙂'} {
		ck(counts[r] == 250, "rune count in map")
	}
	ck(len(counts) == 4, "distinct rune count")
	ck(utf8.RuneCountInString(big) == 1000, "big string rune count")
	ck(len(big) == 250*(1+2+3+4), "big string byte length")

	// 随机路径：任意字节串 / 合法 rune 串，与 stdlib 的 Valid/RuneCount 对照。
	r := rand.New(rand.NewSource(curSeed))
	validPool := []rune{'a', 'Z', '0', 'é', '你', '🙂', '́', '�', '\x00'}
	randomValid := make(map[string]*uentry)
	randomBytes := 0
	invalidBytes := 0
	for i := 0; i < 2000; i++ {
		var str string
		if i%2 == 0 {
			n := r.Intn(48)
			b := make([]byte, n)
			for j := range b {
				b[j] = byte(r.Intn(256))
			}
			str = string(b)
			randomBytes++
		} else {
			n := r.Intn(20)
			rs := make([]rune, 0, n)
			for j := 0; j < n; j++ {
				rs = append(rs, validPool[r.Intn(len(validPool))])
			}
			str = string(rs)
		}
		runes, invalid := decodeStats(str)
		gotValid := utf8.ValidString(str)
		if gotValid == (invalid > 0) {
			ckSeed(false, fmt.Sprintf("random %d: valid flag vs invalid count", i))
		}
		if utf8.RuneCountInString(str) != runes {
			ckSeed(false, fmt.Sprintf("random %d: rune count", i))
		}
		if gotValid {
			if string([]rune(str)) != str {
				ckSeed(false, fmt.Sprintf("random %d: rune roundtrip", i))
			}
			if _, ok := randomValid[str]; !ok {
				randomValid[str] = &uentry{runes: runes, bytes: len(str)}
			}
		} else {
			invalidBytes++
		}
		if i%17 == 0 {
			for cut := 0; cut <= len(str); cut++ {
				p := str[:cut]
				pr, pi := decodeStats(p)
				if utf8.ValidString(p) != (pi == 0) || utf8.RuneCountInString(p) != pr {
					ckSeed(false, fmt.Sprintf("random %d: byte-cut property", i))
					break
				}
			}
		}
		if i%256 == 0 {
			gcStress()
		}
	}
	gcStress()
	ckSeed(randomBytes == 1000, "random byte-string count")
	ckSeed(invalidBytes > 0, "random invalid sample coverage")
	rkeys := make([]string, 0, len(randomValid))
	for k := range randomValid {
		rkeys = append(rkeys, k)
	}
	sort.Strings(rkeys)
	for _, k := range rkeys {
		e := randomValid[k]
		if e == nil || e.bytes != len(k) || e.runes != utf8.RuneCountInString(k) || !utf8.ValidString(k) {
			ckSeed(false, "random valid UTF-8 map entry corrupted after GC")
			break
		}
	}
	ckSeed(len(rkeys) > 0, "random valid samples empty")

	runtime.KeepAlive(validMap)
	runtime.KeepAlive(invalidMap)
	runtime.KeepAlive(counts)
	runtime.KeepAlive(randomValid)
	runtime.KeepAlive(big)
	finish("l13_utf8_process")
}
