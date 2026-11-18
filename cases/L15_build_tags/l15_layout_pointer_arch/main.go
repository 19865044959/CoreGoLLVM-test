// L15: GOARCH 选中的结构体布局。64 位平台把 ID 放前、指针 Ref 放后，
// 32 位平台反过来；布局随构建标签切换，unsafe/reflect 视图与 GC 指针偏移
// 都必须同步。TORTURE_ROUNDS 轮在 GC 压力下反复分配、读取该结构体。
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

type blob struct {
	id   int64
	name string
	pad  [4]int64
}

type payloadNode struct {
	id   int
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

// newPacked / inspect 由共享文件提供，字段顺序由 tag 选中的 packed 决定。
func newPacked(id int) *packed {
	return &packed{ID: id, Ref: &blob{id: int64(id) * 3, name: "blob", pad: [4]int64{1, 2, 3, 4}}}
}

func inspect(p *packed) (int, *blob) { return p.ID, p.Ref }

func main() {
	ptrSize := int(unsafe.Sizeof(uintptr(0)))
	wantName := "ptr-second"
	wantFirst := "ID"
	wantRefOff := uintptr(8)
	wantIDOff := uintptr(0)
	if ptrSize == 4 {
		wantName = "ptr-first"
		wantFirst = "Ref"
		wantRefOff = 0
		wantIDOff = 4
	}
	p := &payloadNode{id: 42, name: "layout-pointer-arch"}

	ck(layoutName == wantName, fmt.Sprintf("tag-selected layout name got=%s want=%s", layoutName, wantName))
	ck(unsafe.Sizeof(packed{}) == uintptr(2*ptrSize),
		fmt.Sprintf("packed size got=%d want=%d", unsafe.Sizeof(packed{}), 2*ptrSize))
	ck(unsafe.Alignof(packed{}) == uintptr(ptrSize),
		fmt.Sprintf("packed align got=%d want=%d", unsafe.Alignof(packed{}), ptrSize))
	ck(unsafe.Offsetof(packed{}.Ref) == wantRefOff,
		fmt.Sprintf("Ref offset got=%d want=%d", unsafe.Offsetof(packed{}.Ref), wantRefOff))
	ck(unsafe.Offsetof(packed{}.ID) == wantIDOff,
		fmt.Sprintf("ID offset got=%d want=%d", unsafe.Offsetof(packed{}.ID), wantIDOff))

	typ := reflect.TypeOf(packed{})
	ck(typ.NumField() == 2, fmt.Sprintf("field count got=%d want=2", typ.NumField()))
	ck(typ.Field(0).Name == wantFirst, fmt.Sprintf("field 0 got=%s want=%s", typ.Field(0).Name, wantFirst))
	if f, ok := typ.FieldByName("Ref"); ok {
		ck(f.Offset == wantRefOff, fmt.Sprintf("reflect Ref offset got=%d want=%d", f.Offset, wantRefOff))
	} else {
		ck(false, "reflect missing Ref field")
	}

	ring := make([]*packed, 64)
	for i := range ring {
		ring[i] = &packed{ID: i + 1, Ref: &blob{id: int64(i*7 + 1), name: "ring", pad: [4]int64{1, 2, 3, 4}}}
	}
	gcStress()
	for i := range ring {
		id, ref := inspect(ring[i])
		ck(id == i+1 && ref != nil && ref.id == int64(i*7+1) && ref.name == "ring" && ref.pad[3] == 4,
			fmt.Sprintf("ring[%d] after GC", i))
	}
	ck(p.id == 42 && p.name == "layout-pointer-arch", "payload after GC")

	rounds := envInt("TORTURE_ROUNDS", 20000)
	seed := envInt("UAF_SEED", 1)
	gcEvery := rounds / 16
	if gcEvery < 1 {
		gcEvery = 1
	}
	var sum int64
	for r := 0; r < rounds; r++ {
		id := r + seed
		pp := &packed{ID: id, Ref: &blob{id: int64(r*2 + seed), name: "torture"}}
		gotID, ref := inspect(pp)
		if gotID != id || ref == nil || ref.id != int64(r*2+seed) || ref.name != "torture" {
			ck(false, fmt.Sprintf("torture round=%d seed=%d", r, seed))
			break
		}
		sum += int64(gotID) + ref.id
		if r%256 == 0 {
			buf := make([]byte, 1024)
			buf[0] = byte(r)
			gcSink = append(gcSink[:0], buf)
		}
		if r%gcEvery == 0 {
			runtime.GC()
		}
	}
	wantSum := int64(3)*int64(rounds)*int64(rounds-1)/2 + 2*int64(rounds)*int64(seed)
	ck(sum == wantSum, fmt.Sprintf("torture checksum got=%d want=%d seed=%d rounds=%d", sum, wantSum, seed, rounds))

	runtime.GC()
	runtime.GC()
	for i := range ring {
		id, ref := inspect(ring[i])
		ck(id == i+1 && ref != nil && ref.id == int64(i*7+1) && ref.pad[3] == 4,
			fmt.Sprintf("ring[%d] after final GC", i))
	}
	finish("l15_layout_pointer_arch")
}
