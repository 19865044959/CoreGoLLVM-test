// L13: slice of slices：锯齿 [][]*T 的行内/行间别名、插入/删除/过滤与 [][]byte 共享底层数组。
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
	data []byte
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
	data := make([]byte, 8)
	for i := range data {
		data[i] = byte(id*11 + i*7)
	}
	return &cell{id: id, name: fmt.Sprintf("c%05d", id), data: data}
}

func validCell(c *cell, id int) bool {
	if c == nil || c.id != id || c.name != fmt.Sprintf("c%05d", id) || len(c.data) != 8 {
		return false
	}
	for i := range c.data {
		if c.data[i] != byte(id*11+i*7) {
			return false
		}
	}
	return true
}

func checkRows(rows [][]*cell, model [][]int, tag string) {
	if len(rows) != len(model) {
		ckSeed(false, tag+": row count differs from model")
		return
	}
	for i := range rows {
		if len(rows[i]) != len(model[i]) {
			ckSeed(false, tag+": row length differs at "+strconv.Itoa(i))
			return
		}
		for j := range rows[i] {
			if !validCell(rows[i][j], model[i][j]) {
				ckSeed(false, tag+": cell mismatch at "+strconv.Itoa(i)+"/"+strconv.Itoa(j))
				return
			}
		}
	}
}

func buildRow(base, n int) []*cell {
	row := make([]*cell, n)
	for j := range row {
		row[j] = newCell(base + j)
	}
	return row
}

func buildModel(base, n int) []int {
	row := make([]int, n)
	for j := range row {
		row[j] = base + j
	}
	return row
}

func main() {
	curSeed = envSeed()
	r := rand.New(rand.NewSource(curSeed))
	seed := curSeed

	const rowCount = 64
	rows := make([][]*cell, rowCount)
	model := make([][]int, rowCount)
	for i := 0; i < rowCount; i++ {
		ln := (i*7 + r.Intn(4)) % 10
		if i == 10 {
			// 额外容量，用于验证 append 子切片与父切片长度独立。
			row := make([]*cell, 0, 20)
			for j := 0; j < ln; j++ {
				row = append(row, newCell(i*100+j))
			}
			rows[i] = row
			model[i] = buildModel(i*100, ln)
			continue
		}
		rows[i] = buildRow(i*100, ln)
		model[i] = buildModel(i*100, ln)
	}
	gcStress()
	checkRows(rows, model, "build")

	// 行尾追加：只影响该行。
	before13 := len(rows[13])
	for j := 0; j < 3; j++ {
		id := 13000 + j
		rows[13] = append(rows[13], newCell(id))
		model[13] = append(model[13], id)
	}
	ckSeed(len(rows[13]) == before13+3, "append row length")
	gcStress()
	checkRows(rows, model, "row-append")

	// 子切片别名：sub 与 rows[10] 共享底层数组。
	if len(rows[10]) < 3 {
		for j := len(rows[10]); j < 3; j++ {
			id := 10000 + j
			rows[10] = append(rows[10], newCell(id))
			model[10] = append(model[10], id)
		}
	}
	parentLen := len(rows[10])
	sub := rows[10][1:]
	sub[0] = newCell(9999)
	model[10][1] = 9999
	ckSeed(rows[10][1] != nil && rows[10][1].id == 9999, "sub-slice write not visible in parent")
	ckSeed(sub[0] == rows[10][1], "sub-slice does not share backing array")
	reuse := cap(sub) > len(sub)
	appended := append(sub, newCell(8888))
	ckSeed(len(rows[10]) == parentLen, "append through sub-slice changed parent length")
	ckSeed(appended[len(appended)-1].id == 8888, "append through sub-slice content")
	if reuse {
		ckSeed(rows[10][parentLen-1] != appended[len(appended)-1], "unexpected parent overwrite")
	}
	gcStress()
	checkRows(rows, model, "sub-slice")
	ckSeed(appended[len(appended)-1].id == 8888, "appended cell lost after GC")

	// 中间插入一行（重叠 copy 搬移 slice header）。
	rows = append(rows, nil)
	model = append(model, nil)
	copy(rows[6:], rows[5:])
	copy(model[6:], model[5:])
	rows[5] = buildRow(5000, 3)
	model[5] = buildModel(5000, 3)
	gcStress()
	checkRows(rows, model, "insert-row")

	// 删除一行（重叠 copy 前移 + 尾部置 nil）。
	copy(rows[7:], rows[8:])
	copy(model[7:], model[8:])
	rows[len(rows)-1] = nil
	model[len(model)-1] = nil
	rows = rows[:len(rows)-1]
	model = model[:len(model)-1]
	gcStress()
	checkRows(rows, model, "delete-row")

	// 行级过滤压缩：只保留奇数长度的行。
	var wantModel [][]int
	for _, mr := range model {
		if len(mr)%2 == 1 {
			wantModel = append(wantModel, append([]int(nil), mr...))
		}
	}
	oldLen := len(rows)
	w := 0
	for i := 0; i < oldLen; i++ {
		if len(rows[i])%2 == 1 {
			rows[w] = rows[i]
			model[w] = model[i]
			w++
		}
	}
	for i := w; i < oldLen; i++ {
		rows[i] = nil
		model[i] = nil
	}
	rows = rows[:w]
	model = model[:w]
	gcStress()
	checkRows(rows, model, "row-filter")
	if len(model) != len(wantModel) {
		ckSeed(false, "row-filter output size")
	} else {
		for i := range model {
			if len(model[i]) != len(wantModel[i]) {
				ckSeed(false, "row-filter model size")
				break
			}
			for j := range model[i] {
				if model[i][j] != wantModel[i][j] {
					ckSeed(false, "row-filter model content")
					break
				}
			}
		}
	}

	// 浅拷贝共享行底层数组；深拷贝只复制 slice header 与元素指针。
	shallow := append([][]*cell(nil), rows...)
	if len(shallow) > 0 && len(shallow[0]) > 0 {
		shallow[0][0] = newCell(424242)
		model[0][0] = 424242
		ckSeed(rows[0][0] != nil && rows[0][0].id == 424242, "shallow clone did not share row backing")
	}
	deepClone := make([][]*cell, len(rows))
	for i := range rows {
		deepClone[i] = append([]*cell(nil), rows[i]...)
	}
	if len(deepClone) > 0 && len(deepClone[0]) > 0 {
		deepClone[0][0] = newCell(555555)
		ckSeed(rows[0][0].id != 555555, "deep clone row slot aliased parent")
		ckSeed(deepClone[0][0].id == 555555, "deep clone slot content")
	}
	gcStress()
	checkRows(rows, model, "clones")
	if len(deepClone[0]) > 0 {
		ckSeed(deepClone[0][0].id == 555555, "deep clone slot lost after GC")
	}

	// [][]byte：8 个互不重叠的 512B 块共享同一个大底层数组。
	const chunk = 512
	buf := make([]byte, 8*chunk)
	for i := range buf {
		buf[i] = byte(i*13 + int(seed)*7)
	}
	chunks := make([][]byte, 8)
	wantChunks := make([]string, 8)
	for i := range chunks {
		chunks[i] = buf[i*chunk : (i+1)*chunk : (i+1)*chunk]
		wantChunks[i] = string(chunks[i])
	}
	saved1 := chunks[1][0]
	saved3 := chunks[3][0]
	chunks[2][0] ^= 0xFF
	ckSeed(chunks[1][0] == saved1 && chunks[3][0] == saved3, "adjacent byte chunks overlap")
	chunks[2][0] ^= 0xFF // 恢复，供最终内容校验
	rowCopy := append([]byte(nil), chunks[2]...)
	savedRowCopy := rowCopy[1]
	rowCopy[1] ^= 0xFF
	ckSeed(chunks[2][1] == savedRowCopy, "byte row copy aliases source")
	appB := append(chunks[2], 0x77)
	ckSeed(len(chunks[2]) == chunk, "append to full byte row changed length")
	ckSeed(appB[len(appB)-1] == 0x77, "append to byte row content")
	gcStress()
	for i := range chunks {
		if string(chunks[i]) != wantChunks[i] {
			ckSeed(false, fmt.Sprintf("byte chunk %d corrupted after GC", i))
			break
		}
	}

	runtime.KeepAlive(rows)
	runtime.KeepAlive(model)
	runtime.KeepAlive(shallow)
	runtime.KeepAlive(deepClone)
	runtime.KeepAlive(buf)
	runtime.KeepAlive(chunks)
	runtime.KeepAlive(rowCopy)
	runtime.KeepAlive(appended)
	runtime.KeepAlive(appB)
	finish("l13_slice_of_slices")
}
