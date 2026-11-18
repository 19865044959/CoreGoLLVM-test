// L05: cap 增长 + 复制/重叠 copy。含指针字段的 struct 切片从 cap=4 起步，
// 多次扩容时保留旧底层数组快照；随后做三段重叠 copy（按 memmove 语义）、
// 复制到新切片、slices.Clone，再触发扩容。每阶段用独立 id 模型对照，并在
// 垃圾分配 + 显式 GC 后校验指针身份与数据。
package main

import (
	"fmt"
	"os"
	"runtime"
	"slices"
	"strconv"
	"sync/atomic"
)

const (
	cgInitial = 512
	cgExtra   = 128
)

var (
	failed int32
	gcSink [][]byte
)

type cgNode struct {
	id  int
	tag string
}

type cgItem struct {
	id   int
	name string
	data []byte
	ptr  *cgNode
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

func gcStress() {
	for i := 0; i < 20; i++ {
		b := make([]byte, 16<<10)
		b[0] = byte(i)
		gcSink = append(gcSink[:0], b)
	}
	gcSink = gcSink[:0]
	runtime.GC()
	runtime.GC()
}

func cgByte(id, j int, seed uint64) byte {
	return byte((seed*7 + uint64(id)*11 + uint64(j)*3) % 251)
}

func cgMake(id int, seed uint64) cgItem {
	n := 4 + id%13
	data := make([]byte, n)
	for j := range data {
		data[j] = cgByte(id, j, seed)
	}
	return cgItem{
		id:   id,
		name: fmt.Sprintf("i-%d", id),
		data: data,
		ptr:  &cgNode{id: id*7 + 1, tag: fmt.Sprintf("p-%d", id)},
	}
}

func cgAppendTo(s *[]cgItem, want *[]int, nextID *int, n int, seed uint64) int {
	changes := 0
	for k := 0; k < n; k++ {
		oldCap := cap(*s)
		*s = append(*s, cgMake(*nextID, seed))
		if cap(*s) != oldCap {
			changes++
		}
		*want = append(*want, *nextID)
		(*nextID)++
	}
	return changes
}

func cgCheck(what string, s []cgItem, want []int, seed uint64) {
	if len(s) != len(want) {
		ckf(false, "%s len=%d want=%d", what, len(s), len(want))
		return
	}
	bad := 0
	for i := range s {
		if s[i].id != want[i] {
			ckf(false, "%s index=%d id=%d want=%d", what, i, s[i].id, want[i])
			bad++
		}
		if s[i].ptr == nil || s[i].ptr.id != s[i].id*7+1 {
			ckf(false, "%s index=%d id=%d pointer identity lost", what, i, s[i].id)
			bad++
		}
		if s[i].name != fmt.Sprintf("i-%d", s[i].id) {
			ckf(false, "%s index=%d id=%d name=%q", what, i, s[i].id, s[i].name)
			bad++
		}
		if len(s[i].data) != 4+s[i].id%13 {
			ckf(false, "%s index=%d id=%d data len=%d", what, i, s[i].id, len(s[i].data))
			bad++
		} else if len(s[i].data) > 0 {
			if s[i].data[0] != cgByte(s[i].id, 0, seed) {
				ckf(false, "%s index=%d id=%d data first byte", what, i, s[i].id)
				bad++
			}
			if s[i].data[len(s[i].data)-1] != cgByte(s[i].id, len(s[i].data)-1, seed) {
				ckf(false, "%s index=%d id=%d data last byte", what, i, s[i].id)
				bad++
			}
		}
		if bad > 8 {
			ckf(false, "%s too many element failures", what)
			return
		}
	}
}

func main() {
	seed := loadSeed()
	s := make([]cgItem, 0, 4)
	want := make([]int, 0, cgInitial+cgExtra*4)
	nextID := 0

	capChanges := cgAppendTo(&s, &want, &nextID, cgInitial, seed)
	ckf(capChanges >= 5, "capacity changes=%d want>=5", capChanges)

	gcStress()
	cgCheck("initial+GC", s, want, seed)

	// 在扩容前保留旧底层数组（长度取 cap），append 超过 cap 后旧数组必须不变。
	oldLen := len(s)
	oldCap := cap(s)
	oldBacking := s[:cap(s)]
	extra := cap(s) - len(s) + 1
	cgAppendTo(&s, &want, &nextID, extra, seed)
	ckf(&s[0] != &oldBacking[0], "append beyond cap did not reallocate")
	ckf(len(oldBacking) == oldCap, "old backing capacity changed unexpectedly")
	cgCheck("old-backing prefix", oldBacking[:oldLen], want[:oldLen], seed)
	gcStress()
	cgCheck("after-grow+GC", s, want, seed)
	cgCheck("old-backing after-grow+GC", oldBacking[:oldLen], want[:oldLen], seed)

	// 继续增长到足够长度，再做三段重叠 copy（copy 必须等价 memmove）。
	cgAppendTo(&s, &want, &nextID, cgInitial, seed)
	gcStress()
	cgCheck("pre-copy+GC", s, want, seed)

	n := copy(s[100:], s[40:240])
	wn := copy(want[100:], want[40:240])
	ckf(n == 200, "overlap copy 1 n=%d want=200", n)
	ckf(n == wn, "model copy 1 n=%d want=%d", n, wn)
	cgCheck("overlap-1", s, want, seed)

	n = copy(s[50:150], s[0:200])
	wn = copy(want[50:150], want[0:200])
	ckf(n == 100, "overlap copy 2 n=%d want=100", n)
	ckf(n == wn, "model copy 2 n=%d want=%d", n, wn)
	cgCheck("overlap-2", s, want, seed)

	n = copy(s[0:], s[200:])
	wn = copy(want[0:], want[200:])
	ckf(n == len(s)-200, "overlap copy 3 n=%d want=%d", n, len(s)-200)
	ckf(n == wn, "model copy 3 n=%d want=%d", n, wn)
	gcStress()
	cgCheck("overlap-3+GC", s, want, seed)

	// 复制到新切片：元素为按值拷贝，但指针身份保留。
	dstWant := append([]int(nil), want...)
	dst := make([]cgItem, len(s), len(s)+32)
	n = copy(dst, s)
	ckf(n == len(s), "copy to dst n=%d want=%d", n, len(s))
	for i := range s {
		ckf(dst[i].ptr == s[i].ptr, "dst index=%d pointer identity changed", i)
	}
	dst = append(dst, cgMake(nextID, seed))
	nextID++
	ckf(cap(dst) >= len(dst), "dst cap < len")
	gcStress()
	cgCheck("dst+GC", dst[:len(dst)-1], dstWant, seed)
	ckf(dst[len(dst)-1].id == nextID-1, "dst appended element lost")

	// slices.Clone 后再 GC，克隆与原件互不影响。
	cl := slices.Clone(s)
	ckf(len(cl) == len(s) && cap(cl) >= len(cl), "clone shape")
	for i := range s {
		ckf(cl[i].ptr == s[i].ptr, "clone index=%d pointer identity changed", i)
	}
	cloneWant := append([]int(nil), want...)
	oldName := cl[0].name
	s[0].name = "original-mutated"
	ckf(cl[0].name == oldName, "clone shares mutable struct field")
	gcStress()
	cgCheck("clone+GC", cl, cloneWant, seed)
	ckf(cl[0].name == oldName, "clone name changed after GC")
	s[0].name = fmt.Sprintf("i-%d", s[0].id)

	// 再触发一次扩容，保留旧的 s 快照并跨 GC 校验。
	oldLen = len(s)
	oldBacking = s[:cap(s)]
	extra = cap(s) - len(s) + 1
	cgAppendTo(&s, &want, &nextID, extra, seed)
	ckf(&s[0] != &oldBacking[0], "second append beyond cap did not reallocate")
	gcStress()
	cgCheck("second-grow+GC", s, want, seed)
	cgCheck("second old-backing+GC", oldBacking[:oldLen], want[:oldLen], seed)

	// 丢弃原切片，dst/clone 仍有效。
	s = nil
	gcStress()
	cgCheck("final dst", dst[:len(dst)-1], dstWant, seed)
	cgCheck("final clone", cl, cloneWant, seed)
	runtime.KeepAlive(oldBacking)

	finish("l05_copy_growth_overlap", seed)
}
