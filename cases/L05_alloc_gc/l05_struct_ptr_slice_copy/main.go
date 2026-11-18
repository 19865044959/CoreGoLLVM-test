// L05: []struct{指针字段} 通过 append 多次扩容，再取子切片、整体 copy、append 超容量；
// 每个阶段穿插垃圾分配与 runtime.GC，校验结构体内的 string/slice/指针字段全部完好。
package main

import (
	"fmt"
	"hash/crc32"
	"os"
	"runtime"
	"strconv"
	"sync/atomic"
)

const rowCount = 1200

var (
	failed int32
	gcSink [][]byte
)

type item struct {
	v   int64
	tag string
}

type row struct {
	id    int
	name  string
	data  []byte
	link  *item
	extra []string
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

func makeRow(id int, seed uint64) row {
	n := 24 + (id*37+int(seed))%80
	data := make([]byte, n)
	base := uint64(id)*37 + seed*13 + 7
	for i := range data {
		data[i] = byte((base + uint64(i)*11) % 251)
	}
	tag := fmt.Sprintf("it-%05d", id)
	return row{
		id:    id,
		name:  fmt.Sprintf("row-%05d", id),
		data:  data,
		link:  &item{v: int64(id) * 7, tag: tag},
		extra: []string{fmt.Sprintf("a-%05d", id), fmt.Sprintf("b-%05d", id)},
	}
}

func checkRow(r *row, id int, seed uint64) bool {
	if r.id != id || r.name != fmt.Sprintf("row-%05d", id) {
		return false
	}
	if r.link == nil || r.link.v != int64(id)*7 || r.link.tag != fmt.Sprintf("it-%05d", id) {
		return false
	}
	if len(r.extra) != 2 || r.extra[0] != fmt.Sprintf("a-%05d", id) || r.extra[1] != fmt.Sprintf("b-%05d", id) {
		return false
	}
	n := 24 + (id*37+int(seed))%80
	if len(r.data) != n {
		return false
	}
	want := make([]byte, n)
	base := uint64(id)*37 + seed*13 + 7
	for i := range want {
		want[i] = byte((base + uint64(i)*11) % 251)
	}
	return crc32.ChecksumIEEE(r.data) == crc32.ChecksumIEEE(want)
}

func main() {
	seed := loadSeed()

	var rows []row
	for i := 0; i < rowCount; i++ {
		rows = append(rows, makeRow(i, seed))
	}
	ck(len(rows) == rowCount, "append growth length")
	// 容量内 append 不得更换底层数组。
	pre := make([]row, 0, 8)
	pre = append(pre, makeRow(0, seed))
	prePtr := &pre[0]
	pre = append(pre, makeRow(1, seed), makeRow(2, seed))
	ck(prePtr == &pre[0], "append within capacity replaced backing array")
	gcStress()
	for i := range rows {
		if !checkRow(&rows[i], i, seed) {
			ckf(false, "row=%d corrupted after growth+GC", i)
			break
		}
	}

	// 子切片与两种拷贝方式：copy 只复制结构体值，但指针/slice/string 必须保持有效。
	sub := rows[100:900]
	cp := append([]row(nil), sub...)
	dst := make([]row, len(sub))
	copy(dst, sub)
	gcStress()
	for i := range sub {
		if !checkRow(&sub[i], 100+i, seed) {
			ckf(false, "sub row=%d after GC", i)
			break
		}
		if !checkRow(&cp[i], 100+i, seed) || !checkRow(&dst[i], 100+i, seed) {
			ckf(false, "copied row=%d after GC", i)
			break
		}
		// copy 只复制结构体值，link 指针必须仍指向同一对象。
		if cp[i].link != sub[i].link || dst[i].link != sub[i].link {
			ckf(false, "copy row=%d dropped link pointer identity", i)
			break
		}
	}

	// 满容量切片表达式强制下一次 append 重新分配；旧数组内容不得被改写。
	old := rows[:len(rows):len(rows)]
	newRow := makeRow(rowCount, seed)
	grown := append(old, newRow, makeRow(rowCount+1, seed))
	gcStress()
	ck(len(old) == rowCount && cap(old) == rowCount, "full-slice cap clamp")
	ck(len(grown) == rowCount+2, "append beyond cap length")
	if &old[0] == &grown[0] {
		ck(false, "append beyond cap reused old backing array")
	}
	for i := 0; i < rowCount; i += 71 {
		if !checkRow(&old[i], i, seed) {
			ckf(false, "old backing row=%d changed by append realloc", i)
			break
		}
	}
	if !checkRow(&grown[rowCount], rowCount, seed) || !checkRow(&grown[rowCount+1], rowCount+1, seed) {
		ck(false, "new rows after realloc corrupted")
	}

	// 丢弃原始切片，仅保留拷贝结果，GC 后拷贝中的指针目标必须仍存活。
	rows = nil
	sub = nil
	old = nil
	gcStress()
	for i := range cp {
		if !checkRow(&cp[i], 100+i, seed) || !checkRow(&dst[i], 100+i, seed) {
			ckf(false, "retained copy row=%d corrupted after origin drop", i)
			break
		}
	}
	runtime.KeepAlive(cp)
	runtime.KeepAlive(dst)

	finish("l05_struct_ptr_slice_copy", seed)
}
