// L05: make(cap) 预分配与 append 增长路径对照：容量足够时不换底层数组，
// 超出 cap 后重分配且旧数组不变；各阶段穿插垃圾分配与 GC。
package main

import (
	"fmt"
	"hash/crc32"
	"os"
	"runtime"
	"strconv"
	"sync/atomic"
)

const recCount = 3000

var (
	failed int32
	gcSink [][]byte
)

type item struct {
	v int64
}

type rec struct {
	id    int64
	kind  int32
	label string
	ptr   *item
}

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

func makeRec(i int, seed uint64) rec {
	return rec{
		id:    int64(i),
		kind:  int32((i*7 + int(seed)) % 13),
		label: fmt.Sprintf("rec-%05d", i),
		ptr:   &item{v: int64(i) * 11},
	}
}

func checkRec(r *rec, i int, seed uint64) bool {
	if r.id != int64(i) || r.kind != int32((i*7+int(seed))%13) || r.label != fmt.Sprintf("rec-%05d", i) {
		return false
	}
	return r.ptr != nil && r.ptr.v == int64(i)*11
}

func main() {
	seed := loadSeed()

	// 1) 预分配恰好 N 的容量：N 次 append 不换底层数组。
	s := make([]rec, 0, recCount)
	s = append(s, makeRec(0, seed))
	first := &s[0]
	for i := 1; i < recCount; i++ {
		s = append(s, makeRec(i, seed))
	}
	ck(len(s) == recCount, "prealloc length")
	ck(cap(s) == recCount, "prealloc cap should stay at initial capacity")
	ck(first == &s[0], "prealloc backing array changed before reaching cap")
	gcStress()
	for i := 0; i < recCount; i += 37 {
		if !checkRec(&s[i], i, seed) {
			ckf(false, "prealloc rec=%d corrupted after GC", i)
			break
		}
	}

	// 2) 满容量切片表达式 + append 一次：必须分配新数组，旧数组内容保持不变。
	old := s[:len(s):len(s)]
	grown := append(old, makeRec(recCount, seed))
	gcStress()
	ck(len(grown) == recCount+1 && cap(old) == recCount, "append beyond cap shape")
	if &old[0] == &grown[0] {
		ck(false, "append beyond cap did not reallocate")
	}
	for i := 0; i < recCount; i += 113 {
		if !checkRec(&old[i], i, seed) {
			ckf(false, "old backing rec=%d changed after realloc", i)
			break
		}
	}
	if !checkRec(&grown[recCount], recCount, seed) {
		ck(false, "new element after realloc corrupted")
	}

	// 3) 从 nil 开始 append 的增长路径：只约束内容与 len/cap 关系，不假设具体增长率。
	var g []rec
	for i := 0; i < recCount; i++ {
		g = append(g, makeRec(i, seed))
		if i%700 == 0 {
			junk := make([]byte, 8<<10)
			junk[0] = byte(i)
			gcSink = append(gcSink[:0], junk)
		}
	}
	gcSink = gcSink[:0]
	ck(len(g) == recCount, "grown length")
	ck(cap(g) >= len(g), "grown cap < len")
	gcStress()
	for i := 0; i < recCount; i += 41 {
		if !checkRec(&g[i], i, seed) {
			ckf(false, "grown rec=%d corrupted after GC", i)
			break
		}
	}

	// 4) 预留一半容量再分两批 append：第一批不扩容，第二批触发一次增长。
	half := recCount / 2
	h := make([]rec, 0, half)
	for i := 0; i < half; i++ {
		h = append(h, makeRec(i, seed))
	}
	ck(cap(h) == half && len(h) == half, "half prealloc shape")
	h = append(h, makeRec(half, seed))
	gcStress()
	ck(len(h) == half+1, "half append length")
	if !checkRec(&h[0], 0, seed) || !checkRec(&h[half], half, seed) {
		ck(false, "half append content")
	}

	// 5) 对带 string/pointer 的 slice 计算整体 CRC，GC 前后必须一致。
	var crc uint32
	for i := range s {
		crc = crc32.Update(crc, crc32.IEEETable, []byte(s[i].label))
	}
	gcStress()
	var crc2 uint32
	for i := range s {
		crc2 = crc32.Update(crc2, crc32.IEEETable, []byte(s[i].label))
	}
	ck(crc == crc2 && crc != 0, "label checksum changed across GC")

	runtime.KeepAlive(grown)
	runtime.KeepAlive(g)
	runtime.KeepAlive(h)
	finish("l05_prealloc_cap_append", seed)
}
