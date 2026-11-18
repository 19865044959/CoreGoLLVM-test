// L07: 重叠窗口的 slice header 交换。同一底层数组上两个不相交窗口交换 {data,len,cap}，
// 第三个跨两窗口的观察者必须看到各自 cap 内 append 的落点；恢复 header 与 GC 后仍一致。
package main

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"sync/atomic"
	"unsafe"
)

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
			b := make([]byte, 7168+i*21)
			b[0] = byte(i)
			b[len(b)-1] = byte(215 - i)
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

type exLeaf struct {
	v   int64
	tag string
}

type exRec struct {
	id   int64
	name string
	link *exLeaf
	data []byte
}

type exHdr struct {
	data unsafe.Pointer
	len  int
	cap  int
}

func exHdrOf[T any](s []T) exHdr {
	return *(*exHdr)(unsafe.Pointer(&s))
}

func exSetHdr[T any](sp *[]T, h exHdr) {
	*(*exHdr)(unsafe.Pointer(sp)) = h
}

func newExRec(id int, s int64) exRec {
	n := 4 + id&3
	data := make([]byte, n)
	for k := range data {
		data[k] = byte((id*43 + k*13 + int(s)) & 0xff)
	}
	return exRec{
		id:   int64(id),
		name: "e-" + strconv.Itoa(id),
		link: &exLeaf{v: int64(id)*31 + s, tag: "el-" + strconv.Itoa(id)},
		data: data,
	}
}

func checkExRec(p *exRec, id int, s int64, stage string) {
	ck(p.id == int64(id), stage+": id got="+strconv.FormatInt(p.id, 10))
	ck(p.name == "e-"+strconv.Itoa(id), stage+": name id="+strconv.Itoa(id))
	if p.link == nil {
		ck(false, stage+": nil link id="+strconv.Itoa(id))
	} else {
		ck(p.link.v == int64(id)*31+s && p.link.tag == "el-"+strconv.Itoa(id), stage+": link id="+strconv.Itoa(id))
	}
	n := 4 + id&3
	ck(len(p.data) == n, stage+": data len id="+strconv.Itoa(id))
	for k := 0; k < n && k < len(p.data); k++ {
		ck(p.data[k] == byte((id*43+k*13+int(s))&0xff), stage+": data id="+strconv.Itoa(id)+" k="+strconv.Itoa(k))
	}
}

func main() {
	if env := os.Getenv("UAF_SEED"); env != "" {
		if v, err := strconv.ParseInt(env, 10, 64); err == nil {
			seed = v
		}
	}
	if seed < 0 {
		seed = -seed
	}

	backing := make([]exRec, 64, 64)
	for i := range backing {
		backing[i] = newExRec(i, seed)
	}
	wa := backing[2:10:14]  // len 8, cap 12: append 落在 backing[10..13]
	wb := backing[14:22:26] // len 8, cap 12: append 落在 backing[22..25]
	obs := backing[0:32:64]
	mid := backing[6:26:64] // 跨两个窗口的观察者

	ha := exHdrOf(wa)
	hb := exHdrOf(wb)
	ck(ha.data == unsafe.Pointer(&backing[2]), "A data pointer")
	ck(hb.data == unsafe.Pointer(&backing[14]), "B data pointer")
	ck(ha.len == 8 && ha.cap == 12 && hb.len == 8 && hb.cap == 12, "header shape")
	ck(cap(wa) == 12 && cap(wb) == 12, "slice cap shape")
	wp := unsafe.Pointer(&wa[0])
	wbp := unsafe.Pointer(&wb[0])

	// 1) 交换 header：数据指针、len/cap 全部随头转移，观察者内容暂不变。
	exSetHdr(&wa, hb)
	exSetHdr(&wb, ha)
	ck(exHdrOf(wa) == hb && exHdrOf(wb) == ha, "header values swapped")
	ck(unsafe.Pointer(&wa[0]) == wbp && unsafe.Pointer(&wb[0]) == wp, "data pointers swapped")
	for i := 0; i < 8; i++ {
		checkExRec(&wa[i], 14+i, seed, "A after swap")
		checkExRec(&wb[i], 2+i, seed, "B after swap")
	}
	for j := 0; j < 32; j++ {
		ck(obs[j].id == int64(j), "observer unchanged after header swap j="+strconv.Itoa(j))
	}
	for j := 0; j < 20; j++ {
		ck(mid[j].id == int64(6+j), "mid unchanged after header swap j="+strconv.Itoa(j))
	}

	// 2) 对 A（现指向 backing[14]）在 cap 内追加，落点 22/23；中观察者必须看到。
	wa = append(wa, newExRec(900, seed), newExRec(901, seed))
	ck(len(wa) == 10 && cap(wa) == 12, "A append within cap shape")
	ck(unsafe.Pointer(&wa[0]) == wbp, "A append keeps data pointer")
	for j := 0; j < 32; j++ {
		want := int64(j)
		if j == 22 {
			want = 900
		} else if j == 23 {
			want = 901
		}
		ck(obs[j].id == want, "observer sees A append j="+strconv.Itoa(j))
	}
	checkExRec(&mid[16], 900, seed, "mid A append 16")
	checkExRec(&mid[17], 901, seed, "mid A append 17")

	// 3) 对 B（现指向 backing[2]）追加到满 cap，落点 10..13，与 mid 前段重叠。
	wb = append(wb, newExRec(700, seed), newExRec(701, seed), newExRec(702, seed), newExRec(703, seed))
	ck(len(wb) == 12 && cap(wb) == 12, "B append to cap shape")
	ck(unsafe.Pointer(&wb[0]) == wp, "B append keeps data pointer")
	for k := 0; k < 4; k++ {
		checkExRec(&mid[4+k], 700+k, seed, "mid B append")
		ck(obs[10+k].id == int64(700+k), "observer sees B append k="+strconv.Itoa(k))
	}
	for j := 8; j < 10; j++ {
		ck(obs[j].id == int64(j), "slot below B cap append intact j="+strconv.Itoa(j))
	}
	for j := 14; j < 22; j++ {
		ck(obs[j].id == int64(j), "slot in A original region intact j="+strconv.Itoa(j))
	}

	// 4) 恢复 header：原窗口内容（落在原 len 内）不变，追加区域仍在 cap 之后。
	exSetHdr(&wa, ha)
	exSetHdr(&wb, hb)
	ck(exHdrOf(wa) == ha && exHdrOf(wb) == hb, "header values restored")
	for i := 0; i < 8; i++ {
		checkExRec(&wa[i], 2+i, seed, "A restored")
		checkExRec(&wb[i], 14+i, seed, "B restored")
	}

	// 5) 丢弃底层数组与窗口变量，保留观察者与 header 副本，跨 GC 校验。
	midKeep := mid
	haKeep, hbKeep := ha, hb
	backing = nil
	wa = nil
	wb = nil
	obs = nil
	mid = nil
	gcStress()

	for j := 0; j < 20; j++ {
		id := 6 + j
		switch {
		case j >= 4 && j <= 7:
			id = 700 + (j - 4)
		case j == 16:
			id = 900
		case j == 17:
			id = 901
		}
		checkExRec(&midKeep[j], id, seed, "mid after GC")
	}
	ck(unsafe.Pointer(&midKeep[0]) == unsafe.Pointer(uintptr(haKeep.data)+uintptr(4*unsafe.Sizeof(exRec{}))),
		"mid base vs A header after GC")

	// 6) 只保留 header 副本（unsafe.Pointer 根），重建两个窗口与 cap 内追加区。
	midKeep = nil
	gcStress()
	wa2 := *(*[]exRec)(unsafe.Pointer(&haKeep))
	wb2 := *(*[]exRec)(unsafe.Pointer(&hbKeep))
	ck(len(wa2) == 8 && cap(wa2) == 12, "rebuilt A shape")
	ck(len(wb2) == 8 && cap(wb2) == 12, "rebuilt B shape")
	for i := 0; i < 8; i++ {
		checkExRec(&wa2[i], 2+i, seed, "rebuilt A")
		checkExRec(&wb2[i], 14+i, seed, "rebuilt B")
	}
	waFull := wa2[:cap(wa2)]
	ck(waFull[8].id == 700 && waFull[9].id == 701 && waFull[10].id == 702 && waFull[11].id == 703,
		"rebuilt A cap region holds B append")
	wbFull := wb2[:cap(wb2)]
	ck(wbFull[8].id == 900 && wbFull[9].id == 901, "rebuilt B cap region holds A append")
	ck(haKeep.data != hbKeep.data, "header data pointers distinct")
	ck(uintptr(hbKeep.data)-uintptr(haKeep.data) == uintptr(12*unsafe.Sizeof(exRec{})), "header distance")

	finish("l07_slice_header_overlap_exchange")
}
