// L13: 字符串 intern 表：map[string]*string 规范化、指针复用、清理/重加与 GC 存活。
package main

import (
	"fmt"
	"math/rand"
	"os"
	"runtime"
	"sort"
	"strconv"
	"sync/atomic"
)

type interner struct {
	tab  map[string]*string
	hits int
	adds int
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

func tortureRounds(def int) int {
	s := os.Getenv("TORTURE_ROUNDS")
	if s == "" {
		return def
	}
	n, err := strconv.Atoi(s)
	if err != nil || n <= 0 {
		return def
	}
	if n > 200 {
		n = 200
	}
	return n
}

func newInterner() *interner {
	return &interner{tab: make(map[string]*string)}
}

// intern 返回表内规范指针；同内容字符串必须返回同一指针。
func (in *interner) intern(s string) *string {
	if p, ok := in.tab[s]; ok {
		in.hits++
		return p
	}
	cp := s
	p := &cp
	in.tab[cp] = p
	in.adds++
	return p
}

func (in *interner) purge(pred func(string) bool) int {
	n := 0
	for k := range in.tab {
		if pred(k) {
			delete(in.tab, k)
			n++
		}
	}
	return n
}

func sortedKeys(m map[string]*string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func main() {
	curSeed = envSeed()
	rounds := tortureRounds(3)

	for round := 0; round < rounds; round++ {
		r := rand.New(rand.NewSource(curSeed + int64(round)*104729))
		in := newInterner()
		contents := make([]string, 0, 3006)

		// 小字母表 + 短长度制造大量重复内容；另外加入多字节/空串样本。
		const alphabet = "abcde"
		for i := 0; i < 3000; i++ {
			n := r.Intn(13)
			b := make([]byte, n)
			for j := range b {
				b[j] = alphabet[r.Intn(len(alphabet))]
			}
			contents = append(contents, string(b))
		}
		contents = append(contents, "", "中文", "🙂🚀", "a中🙂", "中文")

		uniq := make(map[string]struct{})
		ptrs := make([]*string, len(contents))
		for i, s := range contents {
			p := in.intern(s)
			ptrs[i] = p
			uniq[s] = struct{}{}
			if p == nil || *p != s {
				ckSeed(false, fmt.Sprintf("round=%d: intern content mismatch at %d", round, i))
				break
			}
		}
		ckSeed(len(in.tab) == len(uniq), fmt.Sprintf("round=%d: intern table size", round))
		ckSeed(in.adds == len(uniq), fmt.Sprintf("round=%d: intern add count", round))

		// 同内容再次 intern 必须返回同一规范指针。
		samePtr := true
		for i, s := range contents {
			if in.intern(s) != ptrs[i] {
				samePtr = false
				break
			}
		}
		ckSeed(samePtr, fmt.Sprintf("round=%d: intern did not return canonical pointer", round))
		ckSeed(in.hits == 2*len(contents)-len(uniq), fmt.Sprintf("round=%d: intern hit count", round))

		// 源 []byte 被改写后，规范串内容不受影响（转换时已拷贝）。
		sourceIndependent := true
		for i := 0; i < len(contents) && i < 64; i++ {
			raw := []byte(contents[i])
			s := string(raw)
			for j := range raw {
				raw[j] = 'Z'
			}
			p := in.intern(s)
			if p == nil || *p != contents[i] {
				sourceIndependent = false
				break
			}
		}
		ckSeed(sourceIndependent, fmt.Sprintf("round=%d: intern aliased mutable source", round))

		livenessOK := true
		for i, p := range ptrs {
			if p == nil || *p != contents[i] {
				livenessOK = false
				break
			}
		}
		ckSeed(livenessOK, fmt.Sprintf("round=%d: interned values corrupted before GC", round))

		gcStress()
		for i, p := range ptrs {
			if p == nil || *p != contents[i] || in.intern(contents[i]) != p {
				ckSeed(false, fmt.Sprintf("round=%d: interned pointer/value lost after GC at %d", round, i))
				break
			}
		}

		// 清理 len(k)%4==0 的条目（含空串），剩余条目及其指针必须仍有效。
		purgedWant := 0
		for s := range uniq {
			if len(s)%4 == 0 {
				purgedWant++
			}
		}
		purged := in.purge(func(s string) bool { return len(s)%4 == 0 })
		ckSeed(purged == purgedWant, fmt.Sprintf("round=%d: purge count", round))
		ckSeed(len(in.tab) == len(uniq)-purgedWant, fmt.Sprintf("round=%d: table size after purge", round))
		gcStress()
		for s, p := range in.tab {
			if p == nil || *p != s {
				ckSeed(false, fmt.Sprintf("round=%d: retained intern entry broken after purge+GC", round))
				break
			}
		}
		for s := range uniq {
			if len(s)%4 != 0 {
				continue
			}
			if _, ok := in.tab[s]; ok {
				ckSeed(false, fmt.Sprintf("round=%d: purged key still present", round))
				break
			}
		}

		// 重新 intern 被清理的内容：表规模恢复，内容正确。
		for i, s := range contents {
			p := in.intern(s)
			if p == nil || *p != s {
				ckSeed(false, fmt.Sprintf("round=%d: re-intern after purge at %d", round, i))
				break
			}
		}
		ckSeed(len(in.tab) == len(uniq), fmt.Sprintf("round=%d: table size after re-intern", round))

		// 全表一致性：排序后逐项校验 key 与解引用值相等。
		gcStress()
		keys := sortedKeys(in.tab)
		if len(keys) != len(uniq) {
			ckSeed(false, fmt.Sprintf("round=%d: sorted key count", round))
		} else {
			for _, k := range keys {
				p := in.tab[k]
				if p == nil || *p != k {
					ckSeed(false, fmt.Sprintf("round=%d: canonical entry mismatch after GC", round))
					break
				}
			}
		}

		// 大字符串与空串的规范化。
		big := make([]byte, 8192)
		for i := range big {
			big[i] = byte('A' + (i*17+round)%26)
		}
		b1 := in.intern(string(big))
		big2 := append([]byte(nil), big...)
		b2 := in.intern(string(big2))
		ckSeed(b1 == b2 && *b1 == string(big), fmt.Sprintf("round=%d: large string canonicalization", round))
		e1 := in.intern(string([]byte{}))
		e2 := in.intern("")
		ckSeed(e1 == e2 && *e1 == "", fmt.Sprintf("round=%d: empty string canonicalization", round))
		gcStress()
		ckSeed(*b1 == string(big) && *e1 == "", fmt.Sprintf("round=%d: large/empty interned values after GC", round))

		runtime.KeepAlive(in)
		runtime.KeepAlive(ptrs)
		runtime.KeepAlive(contents)
		runtime.KeepAlive(b1)
		runtime.KeepAlive(e1)
	}

	finish("l13_string_intern_table")
}
