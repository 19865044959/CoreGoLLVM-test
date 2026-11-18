// L15: GOOS 选择的结构体布局。linux 版 eventHeader 比通用版多一个 Seq
// 字段，unsafe.Sizeof/Offsetof 与 reflect 视图随构建标签切换；TORTURE_ROUNDS
// 轮在 GC 压力下反复分配、读取 tag 选中的布局对象并做闭式校验。
package main

import (
	"fmt"
	"os"
	"reflect"
	"runtime"
	"strconv"
	"sync/atomic"
	"unsafe"
)

type payloadNode struct {
	id   int64
	name string
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

func envInt(key string, def int) int {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 1 {
		return def
	}
	return n
}

func main() {
	wantName := "other"
	wantFields := 2
	wantSize := uintptr(16)
	wantSeq := uint64(0)
	if runtime.GOOS == "linux" {
		wantName = "linux"
		wantFields = 3
		wantSize = 24
		wantSeq = 9
	}

	p := &payloadNode{id: 43, name: "tag-struct-layout"}

	ck(platformLayout == wantName, fmt.Sprintf("tag-selected layout name got=%s want=%s", platformLayout, wantName))
	ck(unsafe.Sizeof(eventHeader{}) == wantSize,
		fmt.Sprintf("header size got=%d want=%d", unsafe.Sizeof(eventHeader{}), wantSize))
	ck(unsafe.Alignof(eventHeader{}) == 8, "header alignment")

	typ := reflect.TypeOf(eventHeader{})
	ck(typ.NumField() == wantFields, fmt.Sprintf("field count got=%d want=%d", typ.NumField(), wantFields))
	ck(typ.Field(0).Name == "ID" && typ.Field(0).Offset == 0, "field 0 ID offset")
	ck(typ.Field(1).Name == "Flags" && typ.Field(1).Offset == 8, "field 1 Flags offset")
	if runtime.GOOS == "linux" {
		ck(typ.Field(2).Name == "Seq" && typ.Field(2).Offset == 16, "linux Seq field offset")
	}

	h := newEvent(7, 0x11, 9)
	id, flags, seq, seqOff, size := inspectEvent(h)
	ck(id == 7 && flags == 0x11 && seq == wantSeq, "tag-selected fields round-trip")
	ck(size == wantSize, "inspect size")
	ck(seqOff == 0 || (runtime.GOOS == "linux" && seqOff == 16), "Seq offset from tagged view")

	ring := make([]*eventHeader, 32)
	for i := range ring {
		ring[i] = newEvent(uint64(i+1), uint64(i*3), uint64(i*5))
	}
	gcStress()
	for i := range ring {
		id, flags, seq, _, _ = inspectEvent(ring[i])
		ck(id == uint64(i+1) && flags == uint64(i*3) && seq == uint64(i*5),
			fmt.Sprintf("ring[%d] after GC", i))
	}
	ck(p.id == 43 && p.name == "tag-struct-layout", "payload after GC")

	rounds := envInt("TORTURE_ROUNDS", 20000)
	seed := envInt("UAF_SEED", 1)
	gcEvery := rounds / 16
	if gcEvery < 1 {
		gcEvery = 1
	}
	var sum uint64
	for r := 0; r < rounds; r++ {
		e := newEvent(uint64(r), uint64(seed), uint64(r+seed))
		id, flags, seq, _, _ = inspectEvent(e)
		if id != uint64(r) || flags != uint64(seed) || seq != uint64(r+seed) {
			ck(false, fmt.Sprintf("torture round=%d seed=%d", r, seed))
			break
		}
		sum += id + flags + seq
		if r%256 == 0 {
			buf := make([]byte, 1024)
			buf[0] = byte(r)
			gcSink = append(gcSink[:0], buf)
		}
		if r%gcEvery == 0 {
			runtime.GC()
		}
	}
	idSum := uint64(rounds) * uint64(rounds-1) / 2
	wantSum := 2*idSum + 2*uint64(rounds)*uint64(seed)
	ck(sum == wantSum, fmt.Sprintf("torture checksum got=%d want=%d seed=%d rounds=%d", sum, wantSum, seed, rounds))

	runtime.GC()
	runtime.GC()
	for i := range ring {
		id, flags, seq, _, _ = inspectEvent(ring[i])
		ck(id == uint64(i+1) && flags == uint64(i*3) && seq == uint64(i*5),
			fmt.Sprintf("ring[%d] after final GC", i))
	}
	finish("l15_tag_struct_layout")
}
