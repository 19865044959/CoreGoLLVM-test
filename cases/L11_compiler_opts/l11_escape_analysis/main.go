// L11: 逃逸与不逃逸两种分配语义结果必须一致——纯局部切片计算、
// 返回指针、闭包捕获、接口装箱的值在 GC 后全部保持正确。
package main

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"sync/atomic"
)

var (
	failed    int32
	gcSink    [][]byte
	escSink   []*payload
	boxSink   []any
	sliceSink [][]int64
	adderSink []func(int64) int64
	seed      uint64 = 1
)

type payload struct {
	v    int64
	name string
	arr  [4]int64
}

func ck(ok bool, msg string) {
	if !ok {
		atomic.AddInt32(&failed, 1)
		fmt.Printf("FAIL: %s (UAF_SEED=%d)\n", msg, seed)
	}
}

func gcStress() {
	for i := 0; i < 16; i++ {
		b := make([]byte, 4096)
		b[0] = byte(i)
		b[len(b)-1] = byte(255 - i)
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

func finish(name string) {
	if n := atomic.LoadInt32(&failed); n != 0 {
		fmt.Printf("FAILED %s (%d failed checks, seed=%d)\n", name, n, seed)
		os.Exit(1)
	}
	fmt.Println("ALL PASS " + name)
}

// localSum 只在栈/局部使用，结果值可完全由公式预测。
func localSum(n int) int64 {
	arr := make([]int64, n)
	for i := range arr {
		arr[i] = int64(i)*2 + 1
	}
	var t int64
	for _, v := range arr {
		t += v
	}
	return t
}

// makePayload 返回指向局部对象的指针，必须逃逸到堆。
func makePayload(v int64) *payload {
	p := &payload{v: v, name: "esc"}
	for i := range p.arr {
		p.arr[i] = v + int64(i)
	}
	return p
}

// makeAdder 返回捕获 base 的闭包，闭包环境逃逸。
func makeAdder(base int64) func(int64) int64 {
	return func(x int64) int64 { return base + x*3 }
}

// boxPayload 把值装箱进 interface，接口数据逃逸。
func boxPayload(v int64) any {
	return payload{v: v, name: "boxed", arr: [4]int64{v, v + 1, v + 2, v + 3}}
}

func makeLocalSlice(n int) []int64 {
	out := make([]int64, n)
	for i := range out {
		out[i] = int64(i * i)
	}
	return out
}

func main() {
	seed = loadSeed()
	n := 16 + int(seed%81)
	want := int64(n) * int64(n) // 前 n 个奇数之和 = n^2
	ck(localSum(n) == want, "non-escaping local sum mismatch")

	for i := 0; i < 40; i++ {
		v := int64(i)*5 - 100
		escSink = append(escSink, makePayload(v))
		boxSink = append(boxSink, boxPayload(v))
		sliceSink = append(sliceSink, makeLocalSlice(8+i%5))
		adderSink = append(adderSink, makeAdder(v))
		if i%10 == 9 {
			gcStress()
		}
	}

	// 闭包捕获局部切片：局部切片随闭包逃逸。
	func() {
		local := make([]int64, 32)
		for i := range local {
			local[i] = int64(i)*2 + 1
		}
		adderSink = append(adderSink, func(x int64) int64 { return x + local[3] })
	}()

	pp := &payload{v: 777, name: "ptrboxed", arr: [4]int64{1, 2, 3, 4}}
	boxSink = append(boxSink, pp)

	gcStress()
	ck(localSum(n) == want, "non-escaping sum changed after GC")

	for i := 0; i < 40; i++ {
		v := int64(i)*5 - 100
		p := escSink[i]
		ck(p != nil, "escaped payload is nil")
		if p != nil {
			ck(p.v == v && p.name == "esc", "escaped payload scalar corrupted")
			for j := 0; j < 4; j++ {
				ck(p.arr[j] == v+int64(j), "escaped payload array corrupted")
			}
		}
		ck(adderSink[i](10) == v+30, "escaping closure result mismatch")

		bp, ok := boxSink[i].(payload)
		ck(ok, "boxed value type assertion failed")
		if ok {
			ck(bp.v == v && bp.name == "boxed" && bp.arr[3] == v+3, "boxed value corrupted after GC")
		}

		sl := sliceSink[i]
		ln := 8 + i%5
		ck(len(sl) == ln, "escaped slice length mismatch")
		if len(sl) == ln {
			ck(sl[ln-1] == int64((ln-1)*(ln-1)), "escaped slice tail corrupted")
		}
	}

	ck(adderSink[40](5) == 5+7, "closure capturing local slice corrupted after GC")
	got, ok := boxSink[len(boxSink)-1].(*payload)
	ck(ok && got == pp && got.v == 777 && got.arr[2] == 3, "pointer boxed in interface corrupted after GC")

	finish("l11_escape_analysis")
}
