// L07: struct 数组指针算术：用 uintptr 单表达式按 Sizeof 步长走数组，
// 保留字段指针（*uint64/*string/**leaf）跨 GC，并从字段指针反推元素与邻居。
package main

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"sync/atomic"
	"unsafe"
)

type wleaf struct {
	v    int64
	name string
}

type rec struct {
	id   int32
	word uint64
	name string
	link *wleaf
	flag uint8
	val  int64
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

func newRec(i int, s int64) rec {
	return rec{
		id:   int32(i),
		word: uint64(i)*97 + uint64(s%13),
		name: "r-" + strconv.Itoa(i),
		link: &wleaf{v: int64(i)*31 + s, name: "l-" + strconv.Itoa(i)},
		flag: byte(i*7 + int(s)),
		val:  int64(i) - s,
	}
}

func checkRec(p *rec, i int, s int64, stage string) {
	ck(p != nil, stage+": nil rec i="+strconv.Itoa(i))
	if p == nil {
		return
	}
	ck(p.id == int32(i), stage+": id i="+strconv.Itoa(i))
	ck(p.word == uint64(i)*97+uint64(s%13), stage+": word i="+strconv.Itoa(i))
	ck(p.name == "r-"+strconv.Itoa(i), stage+": name i="+strconv.Itoa(i))
	ck(p.flag == byte(i*7+int(s)), stage+": flag i="+strconv.Itoa(i))
	ck(p.val == int64(i)-s, stage+": val i="+strconv.Itoa(i))
	if p.link == nil {
		ck(false, stage+": nil link i="+strconv.Itoa(i))
		return
	}
	ck(p.link.v == int64(i)*31+s && p.link.name == "l-"+strconv.Itoa(i), stage+": link i="+strconv.Itoa(i))
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

	n := 18 + int(seed%5)
	arr := new([24]rec)
	for i := 0; i < n; i++ {
		arr[i] = newRec(i, seed)
	}
	sz := unsafe.Sizeof(rec{})
	ck(sz == 56, "rec stride")
	ck(unsafe.Offsetof(rec{}.word) == 8, "word offset")
	ck(unsafe.Offsetof(rec{}.name) == 16, "name offset")
	ck(unsafe.Offsetof(rec{}.link) == 32, "link offset")
	ck(unsafe.Offsetof(rec{}.val) == 48, "val offset")

	base := unsafe.Pointer(&arr[0])

	// 1) 前向：uintptr 只在单个表达式中出现，立即转回 unsafe.Pointer。
	for k := 0; k < n; k++ {
		p := (*rec)(unsafe.Pointer(uintptr(base) + uintptr(k)*sz))
		ck(p == &arr[k], "forward GEP identity k="+strconv.Itoa(k))
		checkRec(p, k, seed, "forward")
		ck(uintptr(unsafe.Pointer(&arr[k]))-uintptr(base) == uintptr(k)*sz, "forward stride delta k="+strconv.Itoa(k))
	}

	// 2) 后向：从末元素减去步长。
	last := unsafe.Pointer(&arr[n-1])
	for k := n - 1; k >= 0; k-- {
		p := (*rec)(unsafe.Pointer(uintptr(last) - uintptr(n-1-k)*sz))
		ck(p == &arr[k], "backward GEP identity k="+strconv.Itoa(k))
		checkRec(p, k, seed, "backward")
	}

	// 3) 以中间元素为基址双向走，单表达式正负偏移。
	mid := n / 2
	mbase := unsafe.Pointer(&arr[mid])
	for k := 0; k < n; k++ {
		var p *rec
		if k >= mid {
			p = (*rec)(unsafe.Pointer(uintptr(mbase) + uintptr(k-mid)*sz))
		} else {
			p = (*rec)(unsafe.Pointer(uintptr(mbase) - uintptr(mid-k)*sz))
		}
		ck(p == &arr[k], "mid-base GEP identity k="+strconv.Itoa(k))
	}

	// 4) 字段指针：unsafe.Add + Offsetof 与类型化字段地址一致。
	k := int(seed) % n
	wp := &arr[k].word
	np := &arr[k].name
	lp := &arr[k].link
	fp := (*uint8)(unsafe.Add(unsafe.Pointer(&arr[k]), unsafe.Offsetof(rec{}.flag)))
	upWord := (*uint64)(unsafe.Add(unsafe.Pointer(&arr[k]), unsafe.Offsetof(rec{}.word)))
	upName := (*string)(unsafe.Add(unsafe.Pointer(&arr[k]), unsafe.Offsetof(rec{}.name)))
	upLink := (**wleaf)(unsafe.Add(unsafe.Pointer(&arr[k]), unsafe.Offsetof(rec{}.link)))
	ck(fp == &arr[k].flag, "flag field pointer identity")
	ck(upWord == wp, "word field pointer identity")
	ck(upName == np, "name field pointer identity")
	ck(upLink == lp, "link field pointer identity")

	// 只保留字段指针与派生值，丢弃数组变量。
	wantName := arr[k].name
	wantLeaf := *lp
	arr = nil
	gcStress()

	ck(*wp == uint64(k)*97+uint64(seed%13), "word via field pointer after GC")
	ck(*np == wantName, "string via field pointer after GC")
	ck(*fp == byte(k*7+int(seed)), "flag via field pointer after GC")
	ck(*lp != nil && (*lp).v == wantLeaf.v && (*lp).name == wantLeaf.name, "leaf via link slot after GC")

	// 5) 从字段指针反推元素地址与邻居（单表达式 uintptr 算术）。
	re := (*rec)(unsafe.Pointer(uintptr(unsafe.Pointer(np)) - unsafe.Offsetof(rec{}.name)))
	ck(re.id == int32(k), "reconstructed element id")
	ck(re.name == wantName, "reconstructed element name")
	if k+1 < n {
		nxt := (*rec)(unsafe.Pointer(uintptr(unsafe.Pointer(np)) - unsafe.Offsetof(rec{}.name) + sz))
		checkRec(nxt, k+1, seed, "neighbor via field pointer")
	}
	if k > 0 {
		prv := (*rec)(unsafe.Pointer(uintptr(unsafe.Pointer(np)) - unsafe.Offsetof(rec{}.name) - sz))
		checkRec(prv, k-1, seed, "prev via field pointer")
	}
	gcStress()
	ck(*wp == uint64(k)*97+uint64(seed%13), "word after second GC")
	ck((*lp).name == wantLeaf.name, "leaf after second GC")

	finish("l07_struct_array_uintptr_walk")
}
