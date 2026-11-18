// L08: 手写 amd64 汇编填充 Go 结构体字段（含 string 头与指针字段）
// 并把入参指针原样返回；验证 ABI0 参数指针映射、汇编写指针字段后
// 对象跨 GC 存活，以及结构体布局偏移稳定。
package main

import (
	"fmt"
	"os"
	"runtime"
	"sync/atomic"
	"unsafe"
)

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

type rec struct {
	A int64
	B uint32
	C uint32
	S string
	P *int64
}

// fillRecAsm 写入 A/B/C/S/P 并返回 p。
func fillRecAsm(p *rec, a int64, b, c uint32, sp *byte, sn int, ip *int64) *rec

const payload = "asm-struct-payload-0123456789"

func main() {
	ck(unsafe.Offsetof(rec{}.B) == 8, "offset B")
	ck(unsafe.Offsetof(rec{}.C) == 12, "offset C")
	ck(unsafe.Offsetof(rec{}.S) == 16, "offset S")
	ck(unsafe.Offsetof(rec{}.P) == 32, "offset P")
	ck(unsafe.Sizeof(rec{}) == 40, "sizeof rec")

	ival := int64(0x5eed5eed)
	sp := unsafe.StringData(payload)

	recs := make([]*rec, 32)
	for i := range recs {
		p := &rec{}
		got := fillRecAsm(p, int64(i)*1000-500, uint32(i), uint32(i*3+1), sp, len(payload), &ival)
		ck(got == p, fmt.Sprintf("returned pointer i=%d", i))
		ck(p.A == int64(i)*1000-500 && p.B == uint32(i) && p.C == uint32(i*3+1),
			fmt.Sprintf("fields i=%d", i))
		ck(p.S == payload, fmt.Sprintf("string field i=%d", i))
		ck(p.P == &ival && *p.P == ival, fmt.Sprintf("pointer field i=%d", i))
		recs[i] = p
	}

	gcStress()
	var sum int64
	for i, p := range recs {
		ck(p.A == int64(i)*1000-500, fmt.Sprintf("A after GC i=%d", i))
		ck(p.S == payload, fmt.Sprintf("S after GC i=%d", i))
		ck(p.P == &ival, fmt.Sprintf("P after GC i=%d", i))
		sum += int64(p.B) + int64(p.C)
	}
	ck(sum == int64(31*32/2)+int64(3*31*32/2+32), "all field sums after GC")

	// 同一对象反复覆写（对象已年老），指针仍指向静态串与老对象。
	for i := 0; i < 64; i++ {
		p := recs[i%len(recs)]
		fillRecAsm(p, int64(i), uint32(i+1), uint32(i+2), sp, len(payload), &ival)
		ck(p.S == payload && p.P == &ival, fmt.Sprintf("rewrite i=%d", i))
		if i%16 == 15 {
			gcStress()
		}
	}
	gcStress()
	for i, p := range recs {
		ck(p.S == payload, fmt.Sprintf("final S i=%d", i))
		ck(*p.P == ival, fmt.Sprintf("final P i=%d", i))
	}
	ck(len(payload) == 29, "payload length check")

	finish("l08_asm_struct_fill")
}
