// L13: slice 原地过滤压缩：写指针前移、尾部清零、重新切片与 append 复用底层数组。
package main

import (
	"fmt"
	"math/rand"
	"os"
	"runtime"
	"strconv"
	"sync/atomic"
)

type elem struct {
	id      int
	name    string
	payload []byte
	sum     byte
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

func byteSum(p []byte) byte {
	var s byte
	for _, b := range p {
		s = s*31 + b
	}
	return s
}

func newElem(id int, seed int64) *elem {
	n := 8 + id%19
	p := make([]byte, n)
	x := uint64(seed)*0x9E3779B97F4A7C15 + uint64(id)*0x100000001B3 + 0xD1B54A32D192ED03
	for i := range p {
		x = x*6364136223846793005 + 1442695040888963407
		p[i] = byte(x >> 33)
	}
	return &elem{id: id, name: fmt.Sprintf("e%05d", id), payload: p, sum: byteSum(p)}
}

func validElem(e *elem, id int) bool {
	if e == nil || e.id != id || e.name != fmt.Sprintf("e%05d", id) {
		return false
	}
	return len(e.payload) == 8+id%19 && byteSum(e.payload) == e.sum
}

func keepID(id int) bool { return id%3 != 0 && id%7 != 0 }

// filterElems 原地压缩，返回缩短后的切片；尾部旧指针清零以免底层数组继续保活。
func filterElems(in []*elem, keep func(*elem) bool) []*elem {
	w := 0
	for _, e := range in {
		if keep(e) {
			in[w] = e
			w++
		}
	}
	for i := w; i < len(in); i++ {
		in[i] = nil
	}
	return in[:w]
}

func buildElems(n int, seed int64) []*elem {
	out := make([]*elem, n)
	for i := 0; i < n; i++ {
		out[i] = newElem(i, seed)
	}
	return out
}

func refIDs(n int) []int {
	var out []int
	for i := 0; i < n; i++ {
		if keepID(i) {
			out = append(out, i)
		}
	}
	return out
}

func checkElems(got []*elem, want []int, tag string) {
	if len(got) != len(want) {
		ckSeed(false, tag+": filtered length")
		return
	}
	for i := range got {
		if !validElem(got[i], want[i]) {
			ckSeed(false, tag+": filtered element mismatch at "+strconv.Itoa(i))
			return
		}
	}
}

func main() {
	curSeed = envSeed()
	rounds := tortureRounds(3)

	for round := 0; round < rounds; round++ {
		r := rand.New(rand.NewSource(curSeed + int64(round)*7919))
		n := 1400 + r.Intn(400)
		seed := curSeed + int64(round)

		// 1) 指针切片原地过滤：保留 id%3!=0 && id%7!=0。
		all := buildElems(n, seed)
		snapshot := all // 同一底层数组，长度保持 n
		want := refIDs(n)
		gcStress()
		kept := filterElems(all, func(e *elem) bool { return keepID(e.id) })
		checkElems(kept, want, "ptr-filter")
		tailOK := true
		for i := len(kept); i < len(snapshot); i++ {
			if snapshot[i] != nil {
				tailOK = false
				break
			}
		}
		ckSeed(tailOK, "ptr-filter: old tail not zeroed")
		gcStress()
		checkElems(kept, want, "ptr-filter after GC")

		// 过滤后 append 复用（或另分配）底层数组，已丢弃元素不得复活。
		extra := newElem(n+1000, seed)
		kept = append(kept, extra)
		ckSeed(len(kept) == len(want)+1 && validElem(kept[len(want)], n+1000), "append after filter")
		gcStress()
		checkElems(kept[:len(want)], want, "filtered prefix after append+GC")
		ckSeed(validElem(kept[len(want)], n+1000), "appended element after GC")

		// 2) 保留全部：指针必须原样保留（同一 *elem）。
		all2 := buildElems(n, seed+1)
		orig2 := append([]*elem(nil), all2...)
		none := filterElems(all2, func(e *elem) bool { return true })
		same := len(none) == len(orig2)
		if same {
			for i := range none {
				if none[i] != orig2[i] {
					same = false
					break
				}
			}
		}
		ckSeed(same, "keep-all: pointers changed")
		gcStress()
		checkElems(none, refAll(n), "keep-all after GC")

		// 3) 删除全部：长度 0，尾部全部清零。
		all3 := buildElems(n, seed+2)
		full3 := all3
		gcStress()
		gone := filterElems(all3, func(e *elem) bool { return false })
		ckSeed(len(gone) == 0, "drop-all: length")
		allNil := true
		for i := range full3 {
			if full3[i] != nil {
				allNil = false
				break
			}
		}
		ckSeed(allNil, "drop-all: tail not cleared")
		gcStress()
		ckSeed(len(gone) == 0, "drop-all after GC")

		// 4) 带偏移的子切片过滤，共享底层数组。
		all4 := buildElems(n, seed+3)
		part := all4[10:]
		want4 := refIDs(n)
		var wantPart []int
		for _, id := range want4 {
			if id >= 10 {
				wantPart = append(wantPart, id)
			}
		}
		kept4 := filterElems(part, func(e *elem) bool { return keepID(e.id) })
		checkElems(kept4, wantPart, "offset-filter")
		gcStress()
		checkElems(kept4, wantPart, "offset-filter after GC")

		// 5) 不带指针的 int 切片过滤。
		ints := make([]int, n)
		for i := range ints {
			ints[i] = i
		}
		w := 0
		for _, v := range ints {
			if keepID(v) {
				ints[w] = v
				w++
			}
		}
		ints = ints[:w]
		if len(ints) != len(want) {
			ckSeed(false, "int-filter length")
		} else {
			for i := range ints {
				if ints[i] != want[i] {
					ckSeed(false, "int-filter content")
					break
				}
			}
		}
		gcStress()
		ckSeed(len(ints) == len(want), "int-filter after GC")

		// 6) [][]byte 行过滤：同时校验保留行的内容与尾部 nil。
		rowN := 96
		rows := make([][]byte, rowN)
		var wantRows []string
		for i := 0; i < rowN; i++ {
			row := make([]byte, 1+(i*13)%40)
			for j := range row {
				row[j] = byte(i*7 + j*3)
			}
			rows[i] = row
			if row[0]%3 != 0 {
				wantRows = append(wantRows, string(row))
			}
		}
		fullRows := rows
		rw := 0
		for _, row := range rows {
			if row != nil && row[0]%3 != 0 {
				rows[rw] = row
				rw++
			}
		}
		for i := rw; i < len(rows); i++ {
			rows[i] = nil
		}
		rows = rows[:rw]
		tailRows := true
		for i := rw; i < len(fullRows); i++ {
			if fullRows[i] != nil {
				tailRows = false
				break
			}
		}
		ckSeed(tailRows, "rows-filter: tail not cleared")
		gcStress()
		if len(rows) != len(wantRows) {
			ckSeed(false, "rows-filter length")
		} else {
			for i := range rows {
				if string(rows[i]) != wantRows[i] {
					ckSeed(false, "rows-filter content after GC")
					break
				}
			}
		}

		// 7) 去重（保留首次出现），参考实现用 O(n^2) 前向扫描。
		m := 600
		dup := make([]*elem, m)
		for i := 0; i < m; i++ {
			dup[i] = newElem((i*37)%(m/3), seed+4)
		}
		var ref []*elem
		for i := 0; i < m; i++ {
			first := true
			for j := 0; j < i; j++ {
				if dup[j].id == dup[i].id {
					first = false
					break
				}
			}
			if first {
				ref = append(ref, dup[i])
			}
		}
		seen := make(map[int]struct{}, len(ref))
		uw := 0
		for _, e := range dup {
			if _, ok := seen[e.id]; ok {
				continue
			}
			seen[e.id] = struct{}{}
			dup[uw] = e
			uw++
		}
		for i := uw; i < len(dup); i++ {
			dup[i] = nil
		}
		dup = dup[:uw]
		okDup := len(dup) == len(ref)
		if okDup {
			for i := range dup {
				if dup[i] != ref[i] {
					okDup = false
					break
				}
			}
		}
		ckSeed(okDup, "dedupe: first-occurrence pointers")
		gcStress()
		okDup = len(dup) == len(ref)
		if okDup {
			for i := range dup {
				if dup[i] != ref[i] || !validElem(dup[i], ref[i].id) {
					okDup = false
					break
				}
			}
		}
		ckSeed(okDup, "dedupe after GC")

		runtime.KeepAlive(snapshot)
		runtime.KeepAlive(orig2)
		runtime.KeepAlive(full3)
		runtime.KeepAlive(part)
		runtime.KeepAlive(fullRows)
		runtime.KeepAlive(ref)
		runtime.KeepAlive(seen)
	}

	finish("l13_slice_filter_inplace")
}

func refAll(n int) []int {
	out := make([]int, n)
	for i := range out {
		out[i] = i
	}
	return out
}
