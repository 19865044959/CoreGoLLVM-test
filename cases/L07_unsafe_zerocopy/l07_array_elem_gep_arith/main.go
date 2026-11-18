// L07: 数组元素指针 + GEP 算术：unsafe.Add 步长、前后向遍历、多维数组、指针字段槽位。
package main

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"sync/atomic"
	"unsafe"
)

type gnode struct {
	v   int64
	tag string
}

type gelem struct {
	id   int64
	name string
	link *gnode
	flag byte
}

var (
	failed int32
	gcSink [][]byte
	seed   int64
)

func ck(ok bool, msg string) {
	if !ok {
		atomic.AddInt32(&failed, 1)
		fmt.Printf("FAIL: %s (seed=%d)\n", msg, seed)
	}
}

func gcStress() {
	for r := 0; r < 2; r++ {
		for i := 0; i < 12; i++ {
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

func newGElem(i int) gelem {
	return gelem{
		id:   int64(i),
		name: "e-" + strconv.Itoa(i),
		link: &gnode{v: int64(i * 7), tag: "n-" + strconv.Itoa(i)},
		flag: byte(i),
	}
}

func checkGElem(p *gelem, i int, stage string) {
	if p == nil {
		ck(false, stage+": nil elem")
		return
	}
	ck(p.id == int64(i) && p.name == "e-"+strconv.Itoa(i), stage+": id/name i="+strconv.Itoa(i))
	ck(p.flag == byte(i), stage+": flag i="+strconv.Itoa(i))
	if p.link == nil {
		ck(false, stage+": nil link i="+strconv.Itoa(i))
		return
	}
	ck(p.link.v == int64(i*7) && p.link.tag == "n-"+strconv.Itoa(i), stage+": link i="+strconv.Itoa(i))
}

type gepRef struct {
	idx int
	p   *gelem
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

	stride := int(unsafe.Sizeof(gelem{}))
	n := 16 + int(seed%8) // 16..23，数组长度 24
	arr := new([24]gelem)
	for i := range arr {
		arr[i] = newGElem(i)
	}
	ck(stride > 0 && stride%int(unsafe.Alignof(gelem{})) == 0, "stride alignment")

	// 前向 GEP：从基址按元素步长定位每个元素。
	var derived []gepRef
	for i := 0; i < n; i += 3 {
		p := (*gelem)(unsafe.Add(unsafe.Pointer(&arr[0]), i*stride))
		ck(p == &arr[i], "forward GEP identity i="+strconv.Itoa(i))
		checkGElem(p, i, "forward GEP")
		derived = append(derived, gepRef{idx: i, p: p})
	}

	// 后向 GEP：从末元素减去 k 个步长。
	last := n - 1
	back := (*gelem)(unsafe.Add(unsafe.Pointer(&arr[last]), -3*stride))
	ck(back == &arr[last-3], "backward GEP identity")
	checkGElem(back, last-3, "backward GEP")

	// 单表达式 uintptr 算术：不把 uintptr 保存到安全点之后。
	k := int(seed+1) % n
	pm := (*gelem)(unsafe.Pointer(uintptr(unsafe.Pointer(&arr[0])) + uintptr(k*stride)))
	ck(pm == &arr[k], "single-expression uintptr GEP")
	checkGElem(pm, k, "uintptr GEP")

	// 元素内指针字段的槽位指针：&elem.link，跨 GC 后解引用仍有效。
	fi := int(seed) % n
	slot := (**gnode)(unsafe.Add(unsafe.Pointer(&arr[fi]), unsafe.Offsetof(arr[fi].link)))
	ck(*slot == arr[fi].link, "field slot identity")
	checkGElem(&arr[fi], fi, "field slot owner")

	// 多维数组：展平下标 r*cols+c 的 GEP。
	grid := new([6][5]gelem)
	for r := range grid {
		for c := range grid[r] {
			grid[r][c] = newGElem(100 + r*5 + c)
		}
	}
	var diag []gepRef
	for d := 0; d < 5; d++ {
		gr := (d + int(seed)) % 6
		gc := d
		flat := gr*5 + gc
		p := (*gelem)(unsafe.Add(unsafe.Pointer(&grid[0][0]), flat*stride))
		ck(p == &grid[gr][gc], "grid GEP identity d="+strconv.Itoa(d))
		checkGElem(p, 100+flat, "grid GEP")
		diag = append(diag, gepRef{idx: 100 + flat, p: p})
	}

	// 丢弃基址数组，只保留派生元素指针与槽位指针，强制 GC。
	arr = nil
	grid = nil
	gcStress()

	for _, ref := range derived {
		checkGElem(ref.p, ref.idx, "derived after GC")
	}
	for _, ref := range diag {
		checkGElem(ref.p, ref.idx, "diag after GC")
	}
	ck(*slot != nil, "field slot after GC not nil")
	if *slot != nil {
		ck((*slot).v == int64(fi*7) && (*slot).tag == "n-"+strconv.Itoa(fi), "field slot after GC")
	}

	// 反复建立/丢弃数组，验证每轮的派生指针都指向各自的数组。
	for round := 0; round < 24; round++ {
		ra := new([16]gelem)
		for i := range ra {
			ra[i] = newGElem(200 + round*16 + i)
		}
		var refs [5]gepRef
		for j := 0; j < 5; j++ {
			idx := (j*3 + round) % 16
			p := (*gelem)(unsafe.Add(unsafe.Pointer(&ra[0]), idx*stride))
			ck(p == &ra[idx], "round GEP identity")
			refs[j] = gepRef{idx: 200 + round*16 + idx, p: p}
		}
		ra = nil
		if round%8 == 7 {
			gcStress()
		}
		for _, ref := range refs {
			checkGElem(ref.p, ref.idx, "round derived after GC")
		}
	}
	gcStress()

	runtime.KeepAlive(derived)
	runtime.KeepAlive(diag)
	runtime.KeepAlive(slot)
	finish("l07_array_elem_gep_arith")
}
