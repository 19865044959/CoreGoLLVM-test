// L13: slice 收缩/重切：容量保留、尾部重切可见、满切片表达式复制、尾部清零与 s[:0] 复用。
package main

import (
	"fmt"
	"math/rand"
	"os"
	"runtime"
	"strconv"
	"sync/atomic"
)

type rec struct {
	id   int
	name string
	pad  []byte
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

func newRec(id int, salt int64) *rec {
	r := &rec{id: id, name: fmt.Sprintf("r-%05d", id), pad: make([]byte, 12)}
	for i := range r.pad {
		r.pad[i] = byte(id*5 + i*3 + int(salt))
	}
	return r
}

func validRec(r *rec, id int, salt int64) bool {
	if r == nil || r.id != id || r.name != fmt.Sprintf("r-%05d", id) || len(r.pad) != 12 {
		return false
	}
	for i := range r.pad {
		if r.pad[i] != byte(id*5+i*3+int(salt)) {
			return false
		}
	}
	return true
}

func main() {
	curSeed = envSeed()
	rnd := rand.New(rand.NewSource(curSeed))

	const total = 400
	master := make([]*rec, total)
	for i := range master {
		master[i] = newRec(i, curSeed)
	}
	orig := append([]*rec(nil), master...)
	gcStress()

	// 1) 收缩后再重切回原长：旧尾部元素完整可见。
	shr := master[:150]
	ck(len(shr) == 150 && cap(shr) == cap(master), "reslice: shrink len/cap")
	back := shr[:total]
	ck(len(back) == total, "reslice: regrow len")
	for i := 0; i < total; i++ {
		if back[i] != orig[i] {
			ckSeed(false, fmt.Sprintf("reslice: regrow pointer mismatch at %d", i))
			break
		}
	}
	gcStress()
	back = shr[:total]
	for i := 0; i < total; i++ {
		if back[i] != orig[i] {
			ckSeed(false, fmt.Sprintf("reslice: regrow pointer mismatch after GC at %d", i))
			break
		}
	}

	// 2) 容量内 append 写入共享底层数组，父切片立即可见。
	shr = master[:150]
	np := newRec(9001, curSeed)
	shr = append(shr, np)
	ck(len(shr) == 151 && cap(shr) == total, "reslice: append len/cap")
	ck(master[150] == np, "reslice: append did not write shared backing array")
	ck(master[151] == orig[151], "reslice: append damaged neighbor")
	gcStress()
	grown := shr[:total]
	ck(grown[150] == np, "reslice: appended element lost after GC")
	ck(grown[151] == orig[151], "reslice: neighbor changed after GC")

	// 3) 三索引切片 cap 收紧，append 必须分配新数组。
	full := master[:150:150]
	ck(cap(full) == 150, "reslice: full-slice cap")
	nf := newRec(7777, curSeed)
	full2 := append(full, nf)
	ck(len(full2) == 151 && full2[150] == nf, "reslice: full-slice append result")
	ck(master[150] == np, "reslice: full-slice append wrote into retained array")
	ck(master[151] == orig[151], "reslice: full-slice append damaged neighbor")

	// 4) 通过满重切清除不再使用的尾部指针，避免底层数组继续保活。
	tail := master[:cap(master)]
	for i := 150; i < len(tail); i++ {
		tail[i] = nil
	}
	gcStress()
	for i := 150; i < total; i++ {
		if master[i] != nil {
			ckSeed(false, fmt.Sprintf("reslice: tail not cleared at %d", i))
			break
		}
	}
	for i := 0; i < 150; i++ {
		if master[i] != orig[i] {
			ckSeed(false, fmt.Sprintf("reslice: cleared past retained element at %d", i))
			break
		}
	}

	// 5) copy 只复制切片头：元素对象共享，替换元素互不影响。
	snap := append([]*rec(nil), master[:50]...)
	gcStress()
	ck(snap[0] == master[0], "reslice: shallow copy pointer mismatch")
	snap[0].name = "mutated"
	ck(master[0].name == "mutated", "reslice: shallow copy did not share object")
	snap[1] = newRec(5555, curSeed)
	ck(master[1] == orig[1], "reslice: element replacement leaked through copy")
	master[0].name = fmt.Sprintf("r-%05d", master[0].id)
	ck(validRec(master[0], 0, curSeed), "reslice: restore mutated object")

	// 6) s[:0] 复用：同一底层数组反复填满，跨 GC 指针有效。
	var reuse []*rec
	for cycle := 0; cycle < 24; cycle++ {
		reuse = reuse[:0]
		if len(reuse) != 0 {
			ck(false, "reslice: truncate to zero")
			break
		}
		baseID := 10000 + cycle*256
		for i := 0; i < 256; i++ {
			reuse = append(reuse, newRec(baseID+i, curSeed))
		}
		if cycle%6 == 5 {
			gcStress()
			for i := 0; i < 256; i++ {
				if !validRec(reuse[i], baseID+i, curSeed) {
					ckSeed(false, fmt.Sprintf("reslice: reuse cycle %d element %d", cycle, i))
					break
				}
			}
		}
	}
	ck(len(reuse) == 256, "reslice: reuse final len")
	reuse = reuse[:cap(reuse)]
	for i := 256; i < len(reuse); i++ {
		reuse[i] = newRec(700000+i, curSeed)
	}
	oldLen := len(reuse)
	oldCap := cap(reuse)
	reuse = append(reuse, newRec(888888, curSeed))
	ck(cap(reuse) > oldCap && len(reuse) == oldLen+1, "reslice: grow past capacity")
	keep := reuse[oldLen]
	reuse = reuse[:oldLen]
	gcStress()
	ck(reuse[oldLen-1] != nil && keep.id == 888888 && keep.name == "r-888888", "reslice: appended element after GC")
	reuse = append(reuse, keep)

	// 7) []string 收缩/重切，字符串元素不被改写。
	ss := make([]string, 300)
	for i := range ss {
		ss[i] = fmt.Sprintf("s-%04d-%d", i, curSeed%31)
	}
	sshr := ss[:40]
	gcStress()
	sback := sshr[:300]
	for i := 0; i < 300; i++ {
		if sback[i] != fmt.Sprintf("s-%04d-%d", i, curSeed%31) {
			ckSeed(false, fmt.Sprintf("reslice: string value mismatch at %d", i))
			break
		}
	}
	app := ss[:40:40]
	app = append(app, "extra")
	ck(len(app) == 41 && ss[40] != "extra" && len(ss) == 300, "reslice: string full-slice append leaked")

	// 8) UAF_SEED 驱动的收缩点序列。
	for step := 0; step < 300; step++ {
		n := 1 + rnd.Intn(total)
		sub := master[:n]
		if len(sub) != n || cap(sub) != total {
			ckSeed(false, fmt.Sprintf("reslice: random shrink len/cap at step %d", step))
			break
		}
		if sub[0] != master[0] {
			ckSeed(false, fmt.Sprintf("reslice: random shrink start pointer at step %d", step))
			break
		}
		if step%60 == 59 {
			gcStress()
			if sub[0] != master[0] {
				ckSeed(false, fmt.Sprintf("reslice: random shrink pointer after GC at step %d", step))
				break
			}
		}
		runtime.KeepAlive(sub)
	}

	gcStress()
	runtime.KeepAlive(master)
	runtime.KeepAlive(reuse)
	runtime.KeepAlive(ss)
	finish("l13_slice_reslice_retention")
}
