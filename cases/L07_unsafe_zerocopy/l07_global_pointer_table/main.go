// L07: unsafe.Pointer 全局表：包级指针数组、map[string]unsafe.Pointer、全局 slice 与 GC 根。
package main

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"sync/atomic"
	"unsafe"
)

type gobj struct {
	id   int64
	name string
	tags []string
	link *gobj
}

var (
	failed int32
	gcSink [][]byte
	seed   int64

	gTable [32]unsafe.Pointer
	gGen   [32]uint64
	gUsed  [32]bool

	gMap   map[string]unsafe.Pointer
	gSlice []unsafe.Pointer
)

func ck(ok bool, msg string) {
	if !ok {
		atomic.AddInt32(&failed, 1)
		fmt.Printf("FAIL: %s (seed=%d)\n", msg, seed)
	}
}

func gcStress() {
	for r := 0; r < 2; r++ {
		for i := 0; i < 10; i++ {
			b := make([]byte, 8192)
			b[0] = byte(i)
			b[len(b)-1] = byte(255 - i)
			gcSink = append(gcSink[:0], b)
		}
		gcSink = gcSink[:0]
		runtime.GC()
	}
}

func finish(name string) {
	if n := atomic.LoadInt32(&failed); n != 0 {
		fmt.Printf("FAILED %s (%d failed checks)\n", name, n)
		os.Exit(1)
	}
	fmt.Println("ALL PASS " + name)
}

func newGObj(id int64, tag string, link *gobj) *gobj {
	return &gobj{
		id:   id,
		name: tag + "-" + strconv.FormatInt(id, 10),
		tags: []string{tag, strconv.FormatInt(id%7, 10)},
		link: link,
	}
}

func checkGObj(o *gobj, id int64, tag string, linkID int64, stage string) {
	if o == nil {
		ck(false, stage+": nil obj")
		return
	}
	ck(o.id == id && o.name == tag+"-"+strconv.FormatInt(id, 10), stage+": id/name")
	ck(len(o.tags) == 2 && o.tags[0] == tag, stage+": tags")
	if linkID < 0 {
		ck(o.link == nil, stage+": expected nil link")
		return
	}
	if o.link == nil {
		ck(false, stage+": nil link")
		return
	}
	ck(o.link.id == linkID, stage+": link id")
}

func gPut(i int, o *gobj) {
	gGen[i]++
	gUsed[i] = true
	gTable[i] = unsafe.Pointer(o)
}

func gGet(i int) *gobj { return (*gobj)(gTable[i]) }

func gRelease(i int) {
	gGen[i]++
	gUsed[i] = false
	gTable[i] = nil
}

func mapPut(k string, o *gobj) { gMap[k] = unsafe.Pointer(o) }
func mapGet(k string) *gobj    { return (*gobj)(gMap[k]) }
func mapDel(k string)          { delete(gMap, k) }
func tableKey(prefix string, i int) string {
	return prefix + "-" + strconv.Itoa(i)
}

func main() {
	if s := os.Getenv("UAF_SEED"); s != "" {
		if v, err := strconv.ParseInt(s, 10, 64); err == nil {
			seed = v
		}
	}
	if seed < 0 {
		seed = -seed
	}
	prefix := "k" + strconv.Itoa(int(seed%97))
	n := 12 + int(seed%9) // 12..20

	// 1) 包级 [N]unsafe.Pointer 表：对象只经全局槽位可达，GC 后逐一校验。
	for i := 0; i < n; i++ {
		linkID := int64((i + 1) % n)
		o := newGObj(int64(i), "tab", newGObj(linkID, "lnk", nil))
		gPut(i, o)
		ck(gGen[i] == 1 && gUsed[i], "table put generation i="+strconv.Itoa(i))
	}
	gcStress()
	for i := 0; i < n; i++ {
		checkGObj(gGet(i), int64(i), "tab", int64((i+1)%n), "table after GC i="+strconv.Itoa(i))
	}
	ck(gTable[0] != nil && gTable[n-1] != nil, "table boundary slots")

	// 2) map[string]unsafe.Pointer：小容量起步并多次触发扩容；值必须被扫描。
	gMap = make(map[string]unsafe.Pointer, 4)
	m := 96 + int(seed%33)
	expTag := make([]string, m)
	for i := 0; i < m; i++ {
		tag := "m" + strconv.Itoa(i%11)
		expTag[i] = tag
		mapPut(tableKey(prefix, i), newGObj(int64(1000+i), tag, nil))
		if i%16 == 15 {
			// 扩容/搬迁过程中穿插显式 GC。
			garbage := make([]byte, 4096)
			garbage[0] = byte(i)
			gcSink = append(gcSink[:0], garbage)
			runtime.GC()
		}
	}
	ck(len(gMap) == m, "map length after inserts")
	gcStress()
	for i := 0; i < m; i++ {
		o := mapGet(tableKey(prefix, i))
		checkGObj(o, int64(1000+i), expTag[i], -1, "map after GC i="+strconv.Itoa(i))
	}

	// 3) 删除部分 key：被删对象仅由类型化局部引用保活，GC 后仍可读。
	var keepDeleted []*gobj
	deleted := 0
	for i := 0; i < m; i++ {
		if i%3 != 0 {
			continue
		}
		o := mapGet(tableKey(prefix, i))
		keepDeleted = append(keepDeleted, o)
		mapDel(tableKey(prefix, i))
		deleted++
	}
	ck(len(gMap) == m-deleted, "map length after deletes")
	ck(mapGet(tableKey(prefix, 0)) == nil, "deleted key lookup nil")
	gcStress()
	for i := 0; i < m; i++ {
		if i%3 == 0 {
			ck(mapGet(tableKey(prefix, i)) == nil, "deleted key after GC i="+strconv.Itoa(i))
		} else {
			checkGObj(mapGet(tableKey(prefix, i)), int64(1000+i), expTag[i], -1, "survivor after GC i="+strconv.Itoa(i))
		}
	}
	ck(len(keepDeleted) > 0, "deleted typed refs non-empty")
	for k, o := range keepDeleted {
		idx := (k * 3) % m
		checkGObj(o, int64(1000+idx), expTag[idx], -1, "deleted object typed ref "+strconv.Itoa(k))
	}

	// 4) 全局表释放/复用：旧对象由 typed ref 保活，槽位代际递增。
	var keepTable []*gobj
	var releasedIdx []int
	for i := 0; i < n; i++ {
		if i%4 != 0 {
			continue
		}
		keepTable = append(keepTable, gGet(i))
		releasedIdx = append(releasedIdx, i)
		gRelease(i)
	}
	ck(len(releasedIdx) > 0, "table released non-empty")
	for _, i := range releasedIdx {
		ck(gGet(i) == nil && !gUsed[i], "table released nil i="+strconv.Itoa(i))
	}
	gcStress()
	for k, o := range keepTable {
		idx := releasedIdx[k]
		checkGObj(o, int64(idx), "tab", int64((idx+1)%n), "table released typed ref "+strconv.Itoa(k))
	}
	for k, i := range releasedIdx {
		o := newGObj(int64(2000+k), "new", nil)
		gPut(i, o)
		ck(gGen[i] == 3, "table reuse generation i="+strconv.Itoa(i))
	}
	gcStress()
	for k, i := range releasedIdx {
		checkGObj(gGet(i), int64(2000+k), "new", -1, "table reused after GC i="+strconv.Itoa(i))
	}
	for k, o := range keepTable {
		idx := releasedIdx[k]
		checkGObj(o, int64(idx), "tab", int64((idx+1)%n), "old table object after reuse "+strconv.Itoa(k))
	}

	// 5) 全局 []unsafe.Pointer 切片：append 扩容后 GC，尾部截断后再 GC。
	gSlice = make([]unsafe.Pointer, 0, 8)
	sn := 64
	for i := 0; i < sn; i++ {
		gSlice = append(gSlice, unsafe.Pointer(newGObj(int64(3000+i), "sl", nil)))
		if i%21 == 20 {
			runtime.GC()
		}
	}
	ck(len(gSlice) == sn && cap(gSlice) >= sn, "global slice shape")
	gcStress()
	for i := 0; i < sn; i++ {
		checkGObj((*gobj)(gSlice[i]), int64(3000+i), "sl", -1, "global slice after GC i="+strconv.Itoa(i))
	}
	var keepSlice []*gobj
	for i := sn - 1; i >= 10; i-- {
		keepSlice = append(keepSlice, (*gobj)(gSlice[i]))
	}
	gSlice = gSlice[:10]
	gcStress()
	for i := 0; i < 10; i++ {
		checkGObj((*gobj)(gSlice[i]), int64(3000+i), "sl", -1, "global slice truncated i="+strconv.Itoa(i))
	}
	for k, o := range keepSlice {
		id := int64(3000 + sn - 1 - k)
		checkGObj(o, id, "sl", -1, "slice truncated typed ref "+strconv.Itoa(k))
	}

	// 6) 全部清空：全局根断开后不再崩溃；typed refs 仍有效。
	for i := 0; i < 32; i++ {
		if gUsed[i] {
			gRelease(i)
		}
	}
	for k := range gMap {
		delete(gMap, k)
	}
	ck(len(gMap) == 0, "map cleared")
	gSlice = nil
	gcStress()
	for k, o := range keepDeleted {
		idx := (k * 3) % m
		checkGObj(o, int64(1000+idx), expTag[idx], -1, "deleted typed ref final "+strconv.Itoa(k))
	}
	for k, o := range keepTable {
		idx := releasedIdx[k]
		checkGObj(o, int64(idx), "tab", int64((idx+1)%n), "table typed ref final "+strconv.Itoa(k))
	}
	for k, o := range keepSlice {
		id := int64(3000 + sn - 1 - k)
		checkGObj(o, id, "sl", -1, "slice typed ref final "+strconv.Itoa(k))
	}

	runtime.KeepAlive(keepDeleted)
	runtime.KeepAlive(keepTable)
	runtime.KeepAlive(keepSlice)
	finish("l07_global_pointer_table")
}
