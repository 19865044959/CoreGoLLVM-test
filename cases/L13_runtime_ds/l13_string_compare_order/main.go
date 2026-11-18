// L13: 字符串比较与排序：长公共前缀、字节序比较、二分查找、map key 等值与跨 GC。
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
)

type scRec struct {
	id  int
	key string
	pad [8]byte
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

// refCmp 逐字节（无符号）字典序参考实现。
func refCmp(a, b string) int {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	for i := 0; i < n; i++ {
		if a[i] != b[i] {
			if a[i] < b[i] {
				return -1
			}
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

func checkPair(a, b string, tag string) {
	want := refCmp(a, b)
	got := strings.Compare(a, b)
	ckSeed(got == want, fmt.Sprintf("%s: strings.Compare got=%d want=%d", tag, got, want))
	ckSeed((a < b) == (want < 0), tag+": < operator mismatch")
	ckSeed((a == b) == (want == 0), tag+": == operator mismatch")
	ckSeed((a > b) == (want > 0), tag+": > operator mismatch")
}

func newScRec(id int, prefix string) *scRec {
	key := prefix + fmt.Sprintf("%05d", id)
	r := &scRec{id: id, key: key}
	for i := range r.pad {
		r.pad[i] = byte(id*17 + i*5)
	}
	return r
}

func validScRec(r *scRec, id int, prefix string) bool {
	if r == nil || r.id != id || r.key != prefix+fmt.Sprintf("%05d", id) {
		return false
	}
	for i := range r.pad {
		if r.pad[i] != byte(id*17+i*5) {
			return false
		}
	}
	return true
}

func main() {
	curSeed = envSeed()
	r := rand.New(rand.NewSource(curSeed))

	// 1) 长公共前缀下仅尾字节/首字节不同的比较。
	for _, n := range []int{1, 2, 3, 7, 31, 255, 1024, 4096, 16384} {
		prefix := strings.Repeat("q", n)
		a := prefix + "A"
		b := prefix + "B"
		checkPair(a, b, fmt.Sprintf("prefix-tail-%d", n))
		checkPair(a, a, fmt.Sprintf("equal-%d", n))
		checkPair("A"+prefix, "B"+prefix, fmt.Sprintf("first-byte-%d", n))
		checkPair(prefix, prefix+"A", fmt.Sprintf("prefix-short-%d", n))
	}
	// 同内容、不同分配来源。
	longA := strings.Repeat("z", 8192)
	longB := string(append([]byte(nil), []byte(longA)...))
	checkPair(longA, longB, "equal-different-allocation")
	ckSeed(longA == longB, "equal long strings differ")
	gcStress()
	checkPair(longA, longB, "equal-different-allocation-after-GC")

	// 2) 单字节 key 全覆盖：0x00..0xFF 排序后必须严格递增。
	singles := make([]string, 256)
	for i := 0; i < 256; i++ {
		singles[i] = string([]byte{byte(i)})
	}
	sort.Strings(singles)
	for i := 0; i < 256; i++ {
		if singles[i] != string([]byte{byte(i)}) {
			ckSeed(false, fmt.Sprintf("singles: wrong order at %d", i))
			break
		}
		if i > 0 && !(singles[i-1] < singles[i]) {
			ckSeed(false, fmt.Sprintf("singles: not strictly increasing at %d", i))
			break
		}
		idx := sort.SearchStrings(singles, string([]byte{byte(i)}))
		ckSeed(idx >= 0 && idx < 256 && singles[idx] == string([]byte{byte(i)}), fmt.Sprintf("singles: binary search miss byte=%d", i))
	}
	gcStress()
	ck(len(singles) == 256 && singles[0] == "\x00" && singles[255] == "\xff", "singles: endpoints after GC")

	// 3) UAF_SEED 随机字节对：比较符号必须与逐字节参考一致。
	alphabet := []byte{0x00, 0x01, 0x41, 0x5a, 0x61, 0x7a, 0x7f, 0x80, 0xc3, 0xfe, 0xff}
	for i := 0; i < 3000; i++ {
		mk := func() string {
			n := r.Intn(130)
			b := make([]byte, n)
			for j := range b {
				if j%5 == 0 {
					b[j] = alphabet[r.Intn(len(alphabet))]
				} else {
					b[j] = byte(r.Intn(256))
				}
			}
			return string(b)
		}
		a := mk()
		b := mk()
		if i%3 == 0 {
			b = a
		}
		checkPair(a, b, fmt.Sprintf("random-pair-%d", i))
		if i%257 == 256 {
			gcStress()
			checkPair(a, b, fmt.Sprintf("random-pair-%d-after-GC", i))
		}
	}

	// 4) 240 个长公共前缀 key：sort + 二分 + map 等值 + 跨 GC。
	keyPrefix := strings.Repeat("k", 2048)
	keys := make([]string, 240)
	for id := 0; id < 240; id++ {
		keys[id] = fmt.Sprintf("%s%05d", keyPrefix, id)
	}
	order := r.Perm(240)
	sortedKeys := append([]string(nil), keys...)
	sort.Strings(sortedKeys)
	for i := 1; i < len(sortedKeys); i++ {
		if !(sortedKeys[i-1] < sortedKeys[i]) {
			ckSeed(false, fmt.Sprintf("sorted keys: order broken at %d", i))
			break
		}
	}
	m := make(map[string]*scRec, 240)
	for _, id := range order {
		m[keys[id]] = newScRec(id, keyPrefix)
	}
	ck(len(m) == 240, "compare map: initial size")
	for _, k := range sortedKeys {
		idx := sort.SearchStrings(sortedKeys, k)
		ckSeed(idx < len(sortedKeys) && sortedKeys[idx] == k, "compare map: binary search miss")
		id := 0
		fmt.Sscanf(k[len(k)-5:], "%05d", &id)
		rec, ok := m[k]
		ckSeed(ok && validScRec(rec, id, keyPrefix), fmt.Sprintf("compare map: lookup bad id=%d", id))
	}
	gcStress()
	for id := 0; id < 240; id++ {
		fresh := keyPrefix + fmt.Sprintf("%05d", id)
		rec, ok := m[fresh]
		ckSeed(ok && validScRec(rec, id, keyPrefix), fmt.Sprintf("compare map: after GC bad id=%d", id))
	}
	// 不存在 key 的插入点。
	mid := keyPrefix + "00119x"
	idx := sort.SearchStrings(sortedKeys, mid)
	ckSeed(idx == 120, "compare map: insertion point for mid key")
	ckSeed(sort.SearchStrings(sortedKeys, keyPrefix) == 0, "compare map: prefix insertion point")
	ckSeed(sort.SearchStrings(sortedKeys, keyPrefix+"z") == len(sortedKeys), "compare map: high insertion point")

	// 5) 前缀关系与空串边界。
	checkPair("", "", "empty-equal")
	checkPair("", "a", "empty-vs-a")
	checkPair("\x00", "", "nul-vs-empty")
	checkPair("\x00", "\x00\x00", "nul-prefix")
	checkPair(strings.Repeat("x", 4096), strings.Repeat("x", 4096)+"\x00", "long-prefix-nul")
	checkPair("é", "中", "utf8-first-byte-order")
	gcStress()

	runtime.KeepAlive(longA)
	runtime.KeepAlive(longB)
	runtime.KeepAlive(singles)
	runtime.KeepAlive(m)
	runtime.KeepAlive(sortedKeys)
	finish("l13_string_compare_order")
}
