// L05: append 驱动的多级扩容：byte/int64/string/指针元素四类切片从 nil 逐项增长，
// 记录容量阶梯与旧底层数组上的满容量快照；扩容、GC、二次 append 后
// 快照不得改变，新老元素（含指针目标）全部完整。另做一组预分配 cap 对照。
package main

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"sync/atomic"
)

const (
	appendN   = 2048
	appendAdd = 512
)

var (
	failed int32
	gcSink [][]byte
)

type ginner struct {
	v int64
}

type gnode struct {
	id   int
	name string
	p    *ginner
}

type gmix struct {
	id int
	b  []byte
	p  *ginner
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

func byteVal(i int, seed uint64) byte {
	return byte((uint64(i)*7 + seed*13 + 3) % 251)
}

func intVal(i int, seed uint64) int64 {
	return int64(i)*17 + int64(seed%23) - 11
}

func strVal(i int, seed uint64) string {
	return fmt.Sprintf("s-%04d-%d", i, seed%7)
}

func main() {
	seed := loadSeed()
	snapAt := []int{16, 64, 256, 1024}

	// 1) []byte：从 nil 逐项 append。
	var bs []byte
	bsCapChanges := 0
	prevCap := 0
	byteSnaps := make([][]byte, 0, len(snapAt))
	for i := 0; i < appendN; i++ {
		bs = append(bs, byteVal(i, seed))
		if cap(bs) != prevCap {
			bsCapChanges++
			prevCap = cap(bs)
		}
		for _, at := range snapAt {
			if len(bs) == at {
				byteSnaps = append(byteSnaps, bs[:at:at])
			}
		}
	}
	ckf(len(bs) == appendN, "byte append len=%d want=%d", len(bs), appendN)
	ckf(cap(bs) >= len(bs), "byte append cap=%d < len=%d", cap(bs), len(bs))
	ckf(bsCapChanges >= 5, "byte append only %d capacity transitions", bsCapChanges)
	gcStress()
	for i := 0; i < appendN; i++ {
		if bs[i] != byteVal(i, seed) {
			ckf(false, "byte element i=%d corrupted after GC", i)
			break
		}
	}
	for k, at := range snapAt {
		if len(byteSnaps[k]) != at {
			ckf(false, "byte snapshot k=%d len=%d", k, len(byteSnaps[k]))
			continue
		}
		for i := 0; i < at; i++ {
			if byteSnaps[k][i] != byteVal(i, seed) {
				ckf(false, "byte snapshot k=%d i=%d changed by later growth", k, i)
				break
			}
		}
	}
	for i := 0; i < appendAdd; i++ {
		bs = append(bs, byte(0xE0+i%16))
	}
	gcStress()
	for i := 0; i < appendN; i++ {
		if bs[i] != byteVal(i, seed) {
			ckf(false, "byte element i=%d corrupted after second append", i)
			break
		}
	}
	for k, at := range snapAt {
		for i := 0; i < at; i++ {
			if byteSnaps[k][i] != byteVal(i, seed) {
				ckf(false, "byte snapshot k=%d i=%d changed by realloc/GC", k, i)
				break
			}
		}
	}

	// 2) []int64。
	var is []int64
	intCapChanges := 0
	prevCap = 0
	var intSnaps [][]int64
	for i := 0; i < appendN; i++ {
		is = append(is, intVal(i, seed))
		if cap(is) != prevCap {
			intCapChanges++
			prevCap = cap(is)
		}
		for _, at := range snapAt {
			if len(is) == at {
				intSnaps = append(intSnaps, is[:at:at])
			}
		}
	}
	ckf(cap(is) >= len(is) && intCapChanges >= 5, "int64 append cap growth")
	gcStress()
	for i := 0; i < appendN; i++ {
		if is[i] != intVal(i, seed) {
			ckf(false, "int64 element i=%d corrupted", i)
			break
		}
	}
	for k, at := range snapAt {
		for i := 0; i < at; i++ {
			if intSnaps[k][i] != intVal(i, seed) {
				ckf(false, "int64 snapshot k=%d i=%d changed", k, i)
				break
			}
		}
	}

	// 3) []string：快照共享旧底层数组，字符串内容不得随扩容改变。
	var ss []string
	var strSnaps [][]string
	for i := 0; i < appendN; i++ {
		ss = append(ss, strVal(i, seed))
		for _, at := range snapAt {
			if len(ss) == at {
				strSnaps = append(strSnaps, ss[:at:at])
			}
		}
	}
	gcStress()
	for i := 0; i < appendN; i++ {
		if ss[i] != strVal(i, seed) {
			ckf(false, "string element i=%d corrupted", i)
			break
		}
	}
	for k, at := range snapAt {
		for i := 0; i < at; i++ {
			if strSnaps[k][i] != strVal(i, seed) {
				ckf(false, "string snapshot k=%d i=%d changed", k, i)
				break
			}
		}
	}

	// 4) []*gnode：每次 append 新分配节点；快照持有旧元素，扩容后逐项校验指针目标。
	var ps []*gnode
	var ptrSnaps [][]*gnode
	for i := 0; i < appendN; i++ {
		ps = append(ps, &gnode{id: i, name: fmt.Sprintf("gn-%04d", i), p: &ginner{v: int64(i) * 3}})
		for _, at := range snapAt {
			if len(ps) == at {
				ptrSnaps = append(ptrSnaps, ps[:at:at])
			}
		}
	}
	gcStress()
	for i := 0; i < appendN; i++ {
		e := ps[i]
		if e == nil || e.id != i || e.name != fmt.Sprintf("gn-%04d", i) || e.p == nil || e.p.v != int64(i)*3 {
			ckf(false, "pointer element i=%d corrupted", i)
			break
		}
	}
	for k, at := range snapAt {
		for i := 0; i < at; i++ {
			e := ptrSnaps[k][i]
			if e == nil || e.id != i || e.p == nil || e.p.v != int64(i)*3 {
				ckf(false, "pointer snapshot k=%d i=%d corrupted", k, i)
				break
			}
		}
	}
	for i := 0; i < appendAdd; i++ {
		id := appendN + i
		ps = append(ps, &gnode{id: id, name: fmt.Sprintf("gn-%04d", id), p: &ginner{v: int64(id) * 3}})
	}
	gcStress()
	for i := 0; i < appendN+appendAdd; i++ {
		e := ps[i]
		if e == nil || e.id != i || e.p == nil || e.p.v != int64(i)*3 {
			ckf(false, "pointer element i=%d corrupted after second append", i)
			break
		}
	}

	// 5) 预分配 cap 对照：append 至满容量时 cap 不应变化。
	pre := make([]gmix, 0, appendN)
	preCap := cap(pre)
	for i := 0; i < appendN; i++ {
		b := make([]byte, 4+i%13)
		b[0] = byte(i)
		pre = append(pre, gmix{id: i, b: b, p: &ginner{v: int64(i) + 1}})
		if cap(pre) != preCap {
			ckf(false, "prealloc slice grew at i=%d cap=%d want=%d", i, cap(pre), preCap)
			break
		}
	}
	ckf(len(pre) == appendN && cap(pre) == appendN, "prealloc len/cap=%d/%d", len(pre), cap(pre))
	gcStress()
	for i := 0; i < appendN; i++ {
		if pre[i].id != i || len(pre[i].b) != 4+i%13 || pre[i].p == nil || pre[i].p.v != int64(i)+1 {
			ckf(false, "prealloc element i=%d corrupted", i)
			break
		}
	}
	runtime.KeepAlive(bs)
	runtime.KeepAlive(is)
	runtime.KeepAlive(ss)
	runtime.KeepAlive(ps)
	runtime.KeepAlive(pre)

	finish("l05_append_growth_ladder", seed)
}
