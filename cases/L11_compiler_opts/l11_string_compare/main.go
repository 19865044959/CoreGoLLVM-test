// L11: 字符串比较优化——长度/公共前缀/尾部差异/Unicode/空串的 ==、<、>
// 必须与逐字节参考比较一致；排序与 map 键查找跨 GC 保持正确。
package main

import (
	"fmt"
	"os"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
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
	if err != nil || n < 1 {
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

func cmpRef(a, b string) int {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	for i := 0; i < n; i++ {
		if a[i] < b[i] {
			return -1
		}
		if a[i] > b[i] {
			return 1
		}
	}
	switch {
	case len(a) < len(b):
		return -1
	case len(a) > len(b):
		return 1
	}
	return 0
}

func fixedCorpus() []string {
	long := strings.Repeat("a", 4096)
	return []string{
		"", "a", "b", "aa", "ab", "ba", "abc", "abd", "ab\x00", "ab\x00c",
		"世界", "世", "世界x", "世界y", long, long + "b", long[:2048], long[:2047],
		"A", "Z", "0", "9", "request-id-000", "request-id-001", "request-id-0010",
	}
}

func nextRand(x *uint64) uint64 {
	*x ^= *x << 13
	*x ^= *x >> 7
	*x ^= *x << 17
	return *x
}

func randomString(rng *uint64, maxLen int) string {
	alphabet := []byte("abcXYZ019-世界")
	n := int(nextRand(rng) % uint64(maxLen+1))
	b := make([]byte, n)
	for i := range b {
		b[i] = alphabet[nextRand(rng)%uint64(len(alphabet))]
	}
	return string(b)
}

func checkPair(a, b string) {
	r := cmpRef(a, b)
	ck((a == b) == (r == 0), "string equality")
	ck((a < b) == (r < 0), "string less-than")
	ck((a > b) == (r > 0), "string greater-than")
	ck(strings.Compare(a, b) == r, "strings.Compare reference")
}

func main() {
	seed = loadSeed()
	if seed == 0 {
		seed = 1
	}
	rounds := 6 * tortureMultiplier()

	var kept []string
	for r := 0; r < rounds; r++ {
		corpus := fixedCorpus()
		rng := seed + uint64(r)*0x9e3779b97f4a7c15
		for i := 0; i < 24; i++ {
			corpus = append(corpus, randomString(&rng, 64))
		}
		for i := 0; i < len(corpus); i++ {
			for j := 0; j < len(corpus); j++ {
				checkPair(corpus[i], corpus[j])
			}
		}

		// 排序结果必须与逐字节参考顺序一致。
		sorted := append([]string(nil), corpus...)
		sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
		for i := 1; i < len(sorted); i++ {
			ck(cmpRef(sorted[i-1], sorted[i]) <= 0, "sorted order")
		}
		counts := make(map[string]int, len(corpus))
		for _, s := range corpus {
			counts[s]++
		}
		for _, s := range sorted {
			counts[s]--
		}
		permOK := true
		for _, s := range corpus {
			if counts[s] != 0 {
				permOK = false
			}
		}
		ck(permOK, "sort permutation")

		// map 键查找：长公共前缀键必须精确命中（重复键取最后一次写入下标）。
		m := make(map[string]int, len(corpus))
		final := make(map[string]int, len(corpus))
		for i, s := range corpus {
			m[s] = i
			final[s] = i
		}
		for _, s := range corpus {
			v, ok := m[s]
			ck(ok && v == final[s], "map key lookup")
		}

		// 转换/切片后的字符串与原串相等。
		for _, s := range corpus {
			ck(s == string([]byte(s)), "string conversion equality")
			ck(s == s[:len(s)], "string self-slice equality")
		}

		if r == 0 {
			kept = append([]string(nil), corpus...)
		}
		gcStress()
	}

	gcStress()
	for i := 0; i < len(kept); i += 3 {
		for j := 0; j < len(kept); j += 5 {
			checkPair(kept[i], kept[j])
		}
	}
	for _, s := range kept {
		ck(s == string([]byte(s)), "kept string after GC")
	}
	runtime.KeepAlive(kept)

	finish("l11_string_compare")
}
