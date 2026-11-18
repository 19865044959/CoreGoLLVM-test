// L13: 重叠 copy：同底层数组前移/后移、指针切片与 []byte 的 memmove 语义及 GC 保活。
package main

import (
	"fmt"
	"math/rand"
	"os"
	"runtime"
	"strconv"
	"sync/atomic"
)

type cell struct {
	id   int
	name string
	pad  [16]byte
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

func newCell(id int) *cell {
	c := &cell{id: id, name: fmt.Sprintf("c%05d", id)}
	for i := range c.pad {
		c.pad[i] = byte(id*17 + i*5)
	}
	return c
}

func validCell(c *cell, id int) bool {
	if c == nil || c.id != id || c.name != fmt.Sprintf("c%05d", id) {
		return false
	}
	for i := range c.pad {
		if c.pad[i] != byte(id*17+i*5) {
			return false
		}
	}
	return true
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// refOverlapInts 用临时副本 + 逐元素赋值，独立于 copy 的重叠语义。
func refOverlapInts(cur []int, dstOff, srcOff int) []int {
	n := minInt(len(cur)-dstOff, len(cur)-srcOff)
	tmp := append([]int(nil), cur[srcOff:srcOff+n]...)
	out := append([]int(nil), cur...)
	for i := 0; i < n; i++ {
		out[dstOff+i] = tmp[i]
	}
	return out
}

func refOverlapBytes(cur []byte, dstOff, srcOff int) []byte {
	n := minInt(len(cur)-dstOff, len(cur)-srcOff)
	tmp := append([]byte(nil), cur[srcOff:srcOff+n]...)
	out := append([]byte(nil), cur...)
	for i := 0; i < n; i++ {
		out[dstOff+i] = tmp[i]
	}
	return out
}

func refOverlapStrings(cur []string, dstOff, srcOff int) []string {
	n := minInt(len(cur)-dstOff, len(cur)-srcOff)
	tmp := append([]string(nil), cur[srcOff:srcOff+n]...)
	out := append([]string(nil), cur...)
	for i := 0; i < n; i++ {
		out[dstOff+i] = tmp[i]
	}
	return out
}

func equalInts(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func equalBytes(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func checkPtrs(ps []*cell, ids []int) bool {
	if len(ps) != len(ids) {
		return false
	}
	for i := range ps {
		if !validCell(ps[i], ids[i]) {
			return false
		}
	}
	return true
}

func main() {
	curSeed = envSeed()
	r := rand.New(rand.NewSource(curSeed))

	// 1) int 切片：前移/后移/相交/全等/贴边。
	const n = 256
	base := make([]int, n)
	for i := range base {
		base[i] = i*7 + 3
	}
	shifts := [][2]int{{3, 0}, {0, 3}, {10, 50}, {50, 10}, {0, 1}, {1, 0}, {100, 100}, {255, 0}, {0, 255}, {200, 20}}
	for _, sh := range shifts {
		s := append([]int(nil), base...)
		want := refOverlapInts(s, sh[0], sh[1])
		got := copy(s[sh[0]:], s[sh[1]:])
		exp := minInt(n-sh[0], n-sh[1])
		ck(got == exp, "int copy return count")
		ck(equalInts(s, want), "int overlap copy content")
		gcStress()
		ck(equalInts(s, want), "int overlap copy content after GC")
	}
	same := append([]int(nil), base...)
	if copy(same, same) != n || !equalInts(same, base) {
		ck(false, "int self copy changed content")
	}
	var nilInts []int
	ck(copy(nilInts, base) == 0, "copy to nil dst")
	ck(copy(base, nilInts) == 0, "copy from nil src")
	unchanged := append([]int(nil), base...)
	if copy(unchanged[:0], unchanged[3:]) != 0 || !equalInts(unchanged, base) {
		ck(false, "zero-length dst copy")
	}

	// 2) []*cell：重叠搬移指针，GC 后每个对象内容仍有效。
	pn := 128
	ps := make([]*cell, pn)
	ids := make([]int, pn)
	for i := 0; i < pn; i++ {
		ps[i] = newCell(i)
		ids[i] = i
	}
	for _, sh := range [][2]int{{7, 0}, {0, 7}, {20, 90}, {90, 20}, {64, 64}} {
		wantIDs := refOverlapInts(ids, sh[0], sh[1])
		got := copy(ps[sh[0]:], ps[sh[1]:])
		ck(got == minInt(pn-sh[0], pn-sh[1]), "ptr copy return count")
		ids = wantIDs
		gcStress()
		if !checkPtrs(ps, ids) {
			ckSeed(false, "pointer overlap copy corrupted after GC")
			break
		}
	}
	// 全等自拷贝保持指针身份。
	psBefore := append([]*cell(nil), ps...)
	copy(ps, ps)
	samePtrs := true
	for i := range ps {
		if ps[i] != psBefore[i] {
			samePtrs = false
			break
		}
	}
	ck(samePtrs, "pointer self copy changed identities")
	gcStress()
	ck(checkPtrs(ps, ids), "pointer self copy after GC")

	// 非重叠的不同切片之间拷贝。
	src := make([]*cell, 64)
	dst := make([]*cell, 64)
	for i := range src {
		src[i] = newCell(1000 + i)
	}
	copy(dst[3:], src[:61])
	for i := 0; i < 61; i++ {
		if !validCell(dst[3+i], 1000+i) {
			ck(false, "disjoint pointer copy content")
			break
		}
	}
	gcStress()
	ck(validCell(dst[3+60], 1060), "disjoint pointer copy after GC")

	// 3) []string：重叠搬移 string 头。
	sn := 96
	ss := make([]string, sn)
	for i := range ss {
		ss[i] = fmt.Sprintf("s%05d", i)
	}
	for _, sh := range [][2]int{{5, 0}, {0, 5}, {40, 70}, {70, 40}} {
		want := refOverlapStrings(ss, sh[0], sh[1])
		copy(ss[sh[0]:], ss[sh[1]:])
		if !equalStrings(ss, want) {
			ck(false, "string overlap copy content")
			break
		}
		gcStress()
		if !equalStrings(ss, want) {
			ck(false, "string overlap copy content after GC")
			break
		}
	}

	// 4) 大 []byte 重叠 copy（含 256KB 级搬移）。
	bn := 1 << 18
	bytes := make([]byte, bn)
	for i := range bytes {
		bytes[i] = byte(i*29 + int(curSeed)*13)
	}
	byteShifts := [][2]int{{1024, 0}, {0, 1024}, {100000, 50000}, {50000, 100000}, {bn - 100, 0}, {0, bn - 100}, {bn / 2, bn / 2}}
	for _, sh := range byteShifts {
		b := append([]byte(nil), bytes...)
		want := refOverlapBytes(b, sh[0], sh[1])
		got := copy(b[sh[0]:], b[sh[1]:])
		ck(got == minInt(bn-sh[0], bn-sh[1]), "byte copy return count")
		ck(equalBytes(b, want), "byte overlap copy content")
		gcStress()
		ck(equalBytes(b, want), "byte overlap copy content after GC")
	}

	// 5) 随机重叠 copy：随机 dst/src 偏移，GC 穿插并按模型校验全部指针。
	rn := 96
	rps := make([]*cell, rn)
	rids := make([]int, rn)
	for i := 0; i < rn; i++ {
		rps[i] = newCell(2000 + i)
		rids[i] = 2000 + i
	}
	overlaps := 0
	for op := 0; op < 300; op++ {
		dstOff := r.Intn(rn)
		srcOff := r.Intn(rn)
		wantIDs := refOverlapInts(rids, dstOff, srcOff)
		got := copy(rps[dstOff:], rps[srcOff:])
		if got != minInt(rn-dstOff, rn-srcOff) {
			ckSeed(false, "random copy return count")
		}
		rids = wantIDs
		if dstOff != srcOff {
			lo1, hi1 := dstOff, dstOff+got
			lo2, hi2 := srcOff, srcOff+got
			if lo1 < hi2 && lo2 < hi1 {
				overlaps++
			}
		}
		if op%50 == 0 {
			gcStress()
			if !checkPtrs(rps, rids) {
				ckSeed(false, fmt.Sprintf("random copy corrupted at op %d", op))
				break
			}
		}
	}
	ckSeed(overlaps > 0, "random path produced no overlapping copy")
	gcStress()
	ckSeed(checkPtrs(rps, rids), "random overlap copy after final GC")

	runtime.KeepAlive(ps)
	runtime.KeepAlive(ids)
	runtime.KeepAlive(rps)
	runtime.KeepAlive(rids)
	runtime.KeepAlive(bytes)
	runtime.KeepAlive(ss)
	runtime.KeepAlive(src)
	runtime.KeepAlive(dst)
	finish("l13_overlap_copy")
}
