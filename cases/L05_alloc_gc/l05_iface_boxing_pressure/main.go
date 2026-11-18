// L05: 接口装箱分配压力：把 int64/string/*node/[]byte/值类型结构体装箱进 []any，
// 每批之间分配垃圾并 GC，随后逐个 type switch 还原并校验内容与身份。
package main

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"sync/atomic"
)

const (
	boxCount  = 20000
	gcEvery   = 4000
	mapBoxNum = 800
)

var (
	failed int32
	gcSink [][]byte
)

type node struct {
	v    int64
	name string
}

type boxed struct {
	k int64
	s string
}

func ck(ok bool, msg string) {
	if !ok {
		atomic.AddInt32(&failed, 1)
		fmt.Println("FAIL:", msg)
	}
}

func ckf(ok bool, format string, args ...interface{}) {
	if !ok {
		atomic.AddInt32(&failed, 1)
		fmt.Printf("FAIL: "+format+"\n", args...)
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

func finish(name string, seed uint64) {
	if n := atomic.LoadInt32(&failed); n != 0 {
		fmt.Printf("FAILED %s (%d failed checks, seed=%d)\n", name, n, seed)
		os.Exit(1)
	}
	fmt.Println("ALL PASS " + name)
}

//go:noinline
func box(v interface{}) interface{} {
	return v
}

func payloadByte(i, j int, seed uint64) byte {
	return byte((uint64(i)*31 + uint64(j)*7 + seed*13 + 1) % 251)
}

func makeBoxed(i int, seed uint64) interface{} {
	switch i % 5 {
	case 0:
		return box(int64(i)*3 + int64(seed%11))
	case 1:
		return box(fmt.Sprintf("str-%05d", i))
	case 2:
		return box(&node{v: int64(i) * 5, name: fmt.Sprintf("nd-%05d", i)})
	case 3:
		n := 4 + (i*7+int(seed))%17
		b := make([]byte, n)
		for j := range b {
			b[j] = payloadByte(i, j, seed)
		}
		return box(b)
	default:
		return box(boxed{k: int64(i) - 9, s: fmt.Sprintf("bx-%05d", i)})
	}
}

func checkBoxed(v interface{}, i int, seed uint64) bool {
	switch i % 5 {
	case 0:
		x, ok := v.(int64)
		return ok && x == int64(i)*3+int64(seed%11)
	case 1:
		x, ok := v.(string)
		return ok && x == fmt.Sprintf("str-%05d", i)
	case 2:
		x, ok := v.(*node)
		return ok && x != nil && x.v == int64(i)*5 && x.name == fmt.Sprintf("nd-%05d", i)
	case 3:
		x, ok := v.([]byte)
		if !ok {
			return false
		}
		n := 4 + (i*7+int(seed))%17
		if len(x) != n {
			return false
		}
		for j := range x {
			if x[j] != payloadByte(i, j, seed) {
				return false
			}
		}
		return true
	default:
		x, ok := v.(boxed)
		return ok && x.k == int64(i)-9 && x.s == fmt.Sprintf("bx-%05d", i)
	}
}

func main() {
	seed := loadSeed()

	items := make([]interface{}, 0, boxCount)
	for i := 0; i < boxCount; i++ {
		items = append(items, makeBoxed(i, seed))
		if i%gcEvery == gcEvery-1 {
			gcStress()
		}
	}
	gcStress()
	for i := 0; i < boxCount; i++ {
		if !checkBoxed(items[i], i, seed) {
			ckf(false, "boxed item=%d corrupted after GC", i)
			break
		}
	}

	// 装箱后的指针身份必须保持：同一 *node 通过两个接口可见。
	n := &node{v: 12345, name: "identity"}
	var a interface{} = n
	var b interface{} = box(n)
	gcStress()
	if pa, ok := a.(*node); !ok || pa != n {
		ck(false, "interface identity lost for a")
	}
	if pb, ok := b.(*node); !ok || pb != n || pb.v != 12345 {
		ck(false, "interface identity lost for b")
	}

	// map[string]any 中的混合装箱值：按确定 key 校验，跨 GC 保持类型与内容。
	m := make(map[string]interface{}, mapBoxNum)
	for i := 0; i < mapBoxNum; i++ {
		key := fmt.Sprintf("k-%05d", i)
		m[key] = makeBoxed(i, seed)
	}
	gcStress()
	for i := 0; i < mapBoxNum; i++ {
		key := fmt.Sprintf("k-%05d", i)
		if !checkBoxed(m[key], i, seed) {
			ckf(false, "map boxed key=%d corrupted after GC", i)
			break
		}
	}

	// 类型断言失败路径同样确定：全部元素类型正确时必须成功。
	okCount := 0
	for _, v := range items {
		if _, ok := v.(int64); ok {
			okCount++
		}
	}
	wantInt := 0
	for i := 0; i < boxCount; i++ {
		if i%5 == 0 {
			wantInt++
		}
	}
	ck(okCount == wantInt, "int64 boxed count")

	// 丢弃大部分接口数组，仅保留少量元素，GC 后保留项仍有效。
	saved := items[0:5]
	items = nil
	gcStress()
	for i := 0; i < 5; i++ {
		if !checkBoxed(saved[i], i, seed) {
			ckf(false, "saved boxed item=%d corrupted after drop", i)
		}
	}
	runtime.KeepAlive(saved)
	runtime.KeepAlive(m)
	runtime.KeepAlive(n)

	finish("l05_iface_boxing_pressure", seed)
}
