// L13: 单 goroutine 下边 range map 边删除当前键/插入新键，验证迭代语义与指针保活。
package main

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"sync/atomic"
)

type item struct {
	id   int
	name string
	data []byte
}

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

func newItem(id int) *item {
	return &item{id: id, name: "item-" + strconv.Itoa(id), data: []byte{byte(id), byte(id >> 8), 0x5A}}
}

func validItem(v *item, id int) bool {
	if v == nil || v.id != id || v.name != "item-"+strconv.Itoa(id) || len(v.data) != 3 {
		return false
	}
	return v.data[0] == byte(id) && v.data[1] == byte(id>>8) && v.data[2] == 0x5A
}

func main() {
	const n = 1500
	m := make(map[int]*item, n)
	for i := 0; i < n; i++ {
		m[i] = newItem(i)
	}
	gcStress()

	// 阶段 1：range 中删除当前键（k%3==0），并插入 k+n（k%5==0）。
	// 只删除“已到达”的键，因此每个原始键都必须恰好被访问一次；新插入键可访问也可跳过。
	visits := 0
	deleted := 0
	inserted := 0
	for k, v := range m {
		if k < n {
			visits++
			if !validItem(v, k) {
				ck(false, "phase1: original value corrupted during iteration")
				break
			}
			if k%3 == 0 {
				delete(m, k)
				deleted++
			}
			if k%5 == 0 {
				m[k+n] = newItem(k + n)
				inserted++
			}
		}
		if visits%256 == 0 {
			gcStress()
		}
	}
	ck(visits == n, "phase1: not every original key was visited")
	ck(deleted == (n+2)/3, "phase1: wrong delete count")
	ck(inserted == (n+4)/5, "phase1: wrong insert count")

	gcStress()
	wantLen := n - deleted + inserted
	ck(len(m) == wantLen, "phase1: map size after mutate-while-range")
	kept := 0
	for i := 0; i < n; i++ {
		v, ok := m[i]
		if i%3 == 0 {
			ck(!ok, "phase1: deleted key reappeared")
			continue
		}
		ck(ok && validItem(v, i), "phase1: kept key missing or corrupted")
		kept++
	}
	insertedSeen := 0
	for i := 0; i < n; i++ {
		if i%5 != 0 {
			continue
		}
		v, ok := m[i+n]
		ck(ok && validItem(v, i+n), "phase1: inserted key missing or corrupted")
		insertedSeen++
	}
	ck(kept+insertedSeen == wantLen, "phase1: accounting mismatch")

	// range 全量遍历校验（不打印顺序）。
	seen := 0
	for k, v := range m {
		seen++
		if !validItem(v, k) {
			ck(false, "phase1: final range found corrupted value")
			break
		}
	}
	ck(seen == len(m), "phase1: final range count mismatch")

	// 阶段 2：range 中删除每个当前键，最终 map 必须为空。
	before := len(m)
	visits2 := 0
	for k := range m {
		delete(m, k)
		visits2++
		if visits2%300 == 0 {
			gcStress()
		}
	}
	ck(visits2 == before, "phase2: delete-all did not visit every key")
	gcStress()
	ck(len(m) == 0, "phase2: map not empty after delete-all")

	// 阶段 3：range 中只插入（不删除），所有原始键必须访问到，插入键在结束后全部存在。
	base := 800
	for i := 0; i < base; i++ {
		m[i] = newItem(i)
	}
	gcStress()
	seenOriginal := 0
	added := 0
	for k, v := range m {
		if k >= base {
			continue
		}
		seenOriginal++
		if !validItem(v, k) {
			ck(false, "phase3: original value corrupted during insert-while-range")
			break
		}
		if k%32 == 0 && added < 25 {
			nk := 1000000 + added
			m[nk] = newItem(nk)
			added++
		}
		if seenOriginal%200 == 0 {
			gcStress()
		}
	}
	ck(seenOriginal == base, "phase3: not every original key was visited")
	ck(added == 25, "phase3: expected 25 insertions")
	ck(len(m) == base+added, "phase3: unexpected extra entries")
	for i := 0; i < added; i++ {
		v, ok := m[1000000+i]
		ck(ok && validItem(v, 1000000+i), "phase3: inserted key missing or corrupted")
	}
	gcStress()
	for k, v := range m {
		if !validItem(v, k) {
			ck(false, "phase3: final range found corrupted value")
			break
		}
	}

	runtime.KeepAlive(m)
	finish("l13_map_iterate_mutate")
}
