// L08: 手写 amd64 汇编按固定 stride 遍历结构体值数组，按字段偏移累加
// 数值字段；结构体含 string 指针字段，跨 GC 校验数组内容与求和。
package main

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"sync/atomic"
	"unsafe"
)

type item struct {
	score  int64  // offset 0
	name   string // offset 8（16 字节）
	weight int64  // offset 24
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

func sumItemsAsm(p *item, n int) (sumScore, sumWeight int64)

func mkItems(n int) ([]item, int64, int64) {
	items := make([]item, n)
	var scoreSum, weightSum int64
	for i := 0; i < n; i++ {
		s := int64(i*19 - 250)
		w := int64(-i*7 + 13)
		items[i] = item{score: s, name: "item-" + strconv.Itoa(i), weight: w}
		scoreSum += s
		weightSum += w
	}
	return items, scoreSum, weightSum
}

func sumGo(items []item) (int64, int64) {
	var scoreSum, weightSum int64
	for _, it := range items {
		scoreSum += it.score
		weightSum += it.weight
	}
	return scoreSum, weightSum
}

func verify(items []item) {
	for i := range items {
		want := item{score: int64(i*19 - 250), name: "item-" + strconv.Itoa(i), weight: int64(-i*7 + 13)}
		ck(items[i] == want, fmt.Sprintf("item content i=%d", i))
	}
}

func run(n int) {
	items, wantScore, wantWeight := mkItems(n)
	var p *item
	if n > 0 {
		p = &items[0]
	}
	gotScore, gotWeight := sumItemsAsm(p, n)
	ck(gotScore == wantScore && gotWeight == wantWeight,
		fmt.Sprintf("struct array n=%d got=(%d,%d) want=(%d,%d)", n, gotScore, gotWeight, wantScore, wantWeight))

	gcStress()
	gotScore, gotWeight = sumItemsAsm(p, n)
	ck(gotScore == wantScore && gotWeight == wantWeight, fmt.Sprintf("struct array after GC n=%d", n))
	verify(items)

	if n > 4 {
		subScore, subWeight := sumItemsAsm(&items[1], n-2)
		wScore, wWeight := sumGo(items[1 : n-1])
		ck(subScore == wScore && subWeight == wWeight, fmt.Sprintf("struct array sub-slice n=%d", n))
	}
	gcStress()
	verify(items)
	runtime.KeepAlive(items)
	runtime.KeepAlive(p)
}

func main() {
	ck(unsafe.Offsetof(item{}.score) == 0, "layout score offset")
	ck(unsafe.Offsetof(item{}.name) == 8, "layout name offset")
	ck(unsafe.Offsetof(item{}.weight) == 24, "layout weight offset")
	ck(unsafe.Sizeof(item{}) == 32, "layout item size")

	for _, n := range []int{0, 1, 2, 7, 64, 257} {
		run(n)
	}
	gotScore, gotWeight := sumItemsAsm(nil, 0)
	ck(gotScore == 0 && gotWeight == 0, "nil/0")
	finish("l08_asm_struct_array_sum")
}
