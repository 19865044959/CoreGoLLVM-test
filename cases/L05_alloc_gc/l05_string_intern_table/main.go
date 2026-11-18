// L05: 字符串 intern 表：大量内容重复但分别分配的字符串归一到同一底层存储，
// 校验去重计数与 unsafe.StringData 身份；丢弃 intern 表后仅由 token 持有，
// 显式 GC + 垃圾分配后共享关系与内容不变（含 4KB 大字符串）。
package main

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"sync/atomic"
	"unsafe"
)

const (
	uniqueCount = 1536
	tokenCount  = 16384
	largeUnique = 8
	largeTokens = 512
	largeSize   = 4096
)

var (
	failed int32
	gcSink [][]byte
)

func ck(ok bool, msg string) {
	if !ok {
		atomic.AddInt32(&failed, 1)
		fmt.Println("FAIL:", msg)
	}
}

func ckf(ok bool, format string, args ...interface{}) {
	if !ok {
		atomic.AddInt32(&failed, 1)
		fmt.Printf("FAIL: "+format+"\n", args...)
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

func finish(name string, seed uint64) {
	if n := atomic.LoadInt32(&failed); n != 0 {
		fmt.Printf("FAILED %s (%d failed checks, seed=%d)\n", name, n, seed)
		os.Exit(1)
	}
	fmt.Println("ALL PASS " + name)
}

// cloneString 保证每次调用都产生独立的字符串底层存储。
//
//go:noinline
func cloneString(b []byte) string {
	return string(b)
}

//go:noinline
func intern(table map[string]string, s string) string {
	if canon, ok := table[s]; ok {
		return canon
	}
	table[s] = s
	return s
}

func main() {
	seed := loadSeed()

	vocab := make([]string, uniqueCount)
	for i := range vocab {
		vocab[i] = fmt.Sprintf("tenant%02d/metric%05d", i%4, i/4)
	}
	table := make(map[string]string, uniqueCount)
	scratch := make([]byte, 0, 64)
	interned := make([]string, tokenCount)
	idxOf := make([]int, tokenCount)

	for t := 0; t < tokenCount; t++ {
		idx := (t*37 + int(seed%101)) % uniqueCount
		scratch = append(scratch[:0], vocab[idx]...)
		interned[t] = intern(table, cloneString(scratch))
		idxOf[t] = idx
	}
	ckf(len(table) == uniqueCount, "intern table size=%d want=%d", len(table), uniqueCount)

	gcStress()

	// 每个词条应有唯一且与其它词条不同的底层存储。
	canon := make([]*byte, uniqueCount)
	seen := make(map[*byte]int, uniqueCount)
	for i, v := range vocab {
		s := table[v]
		if s != v {
			ckf(false, "canonical value mismatch idx=%d", i)
		}
		p := unsafe.StringData(s)
		canon[i] = p
		if prev, ok := seen[p]; ok {
			ckf(false, "distinct vocab idx=%d shares data with idx=%d", i, prev)
		}
		seen[p] = i
	}
	for t := 0; t < tokenCount; t++ {
		idx := idxOf[t]
		if interned[t] != vocab[idx] {
			ckf(false, "token=%d content mismatch", t)
			break
		}
		if unsafe.StringData(interned[t]) != canon[idx] {
			ckf(false, "token=%d did not intern to canonical storage", t)
			break
		}
	}

	// 丢弃 intern 表：共享关系必须仍由 token 自身维持。
	table = nil
	for i := 0; i < 16; i++ {
		junk := make([]byte, 256<<10)
		junk[0] = byte(i)
		gcSink = append(gcSink[:0], junk)
	}
	gcSink = gcSink[:0]
	runtime.GC()
	runtime.GC()

	rep := make([]string, uniqueCount)
	have := make([]bool, uniqueCount)
	for t := 0; t < tokenCount; t++ {
		idx := idxOf[t]
		if !have[idx] {
			have[idx] = true
			rep[idx] = interned[t]
		}
	}
	for t := 0; t < tokenCount; t++ {
		idx := idxOf[t]
		if !have[idx] || interned[t] != vocab[idx] {
			ckf(false, "token=%d content lost after table drop", t)
			break
		}
		if unsafe.StringData(interned[t]) != unsafe.StringData(rep[idx]) {
			ckf(false, "token=%d sharing lost after table drop", t)
			break
		}
	}

	// 4KB 大字符串同样 intern，跨 GC 共享且内容完整。
	largeVocab := make([]string, largeUnique)
	for i := range largeVocab {
		b := make([]byte, largeSize)
		base := uint64(i)*101 + seed*13 + 7
		for j := range b {
			b[j] = byte((base + uint64(j)*7 + uint64(j>>8)*11) % 251)
		}
		largeVocab[i] = cloneString(b)
	}
	largeTable := make(map[string]string, largeUnique)
	largeTok := make([]string, largeTokens)
	largeIdx := make([]int, largeTokens)
	for t := 0; t < largeTokens; t++ {
		idx := (t*5 + int(seed%3)) % largeUnique
		largeTok[t] = intern(largeTable, cloneString([]byte(largeVocab[idx])))
		largeIdx[t] = idx
	}
	ckf(len(largeTable) == largeUnique, "large intern table size=%d want=%d", len(largeTable), largeUnique)
	gcStress()
	for i, v := range largeVocab {
		if largeTable[v] != v {
			ckf(false, "large canonical idx=%d mismatch", i)
		}
	}
	for t := 0; t < largeTokens; t++ {
		idx := largeIdx[t]
		if largeTok[t] != largeVocab[idx] {
			ckf(false, "large token=%d content mismatch", t)
			break
		}
		if unsafe.StringData(largeTok[t]) != unsafe.StringData(largeTable[largeVocab[idx]]) {
			ckf(false, "large token=%d sharing mismatch", t)
			break
		}
	}
	largeTable = nil
	gcStress()
	for t := 0; t < largeTokens; t += 37 {
		if largeTok[t] != largeVocab[largeIdx[t]] {
			ckf(false, "large token=%d content lost after table drop", t)
		}
	}

	// 只保留少量 token，其余字符串存储应可回收；保留项必须完好且仍共享。
	var kept []string
	var keptIdx []int
	for t := 0; t < tokenCount; t += 128 {
		kept = append(kept, interned[t])
		keptIdx = append(keptIdx, idxOf[t])
	}
	interned = nil
	rep = nil
	for i := 0; i < 8; i++ {
		junk := make([]byte, 1<<20)
		junk[0] = byte(i)
		gcSink = append(gcSink[:0], junk)
	}
	gcSink = gcSink[:0]
	runtime.GC()
	runtime.GC()
	for k := range kept {
		if kept[k] != vocab[keptIdx[k]] {
			ckf(false, "kept token=%d content corrupted", k)
		}
	}
	for k := 1; k < len(kept); k++ {
		if keptIdx[k] == keptIdx[0] {
			if unsafe.StringData(kept[k]) != unsafe.StringData(kept[0]) {
				ckf(false, "kept token=%d sharing corrupted", k)
			}
		}
	}
	runtime.KeepAlive(kept)
	runtime.KeepAlive(largeTok)

	finish("l05_string_intern_table", seed)
}
