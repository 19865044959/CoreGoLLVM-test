// L11: nil map 读取——len/lookup/comma-ok/range/delete/clear/链式读取均为
// 零值语义且不 panic；仅写入 nil map 按规范 panic，GC 后保持 nil。
package main

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
)

var (
	failed int32
	gcSink [][]byte
	seed   uint64 = 1
)

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

type nmHolder struct {
	m map[string]int64
}

//go:noinline
func nmMaybeNil(flag bool) map[string]int64 {
	if flag {
		return map[string]int64{"a": 1, "b": 2}
	}
	return nil
}

func expectNilMapWritePanic(name string, fn func()) {
	defer func() {
		r := recover()
		if r == nil {
			ck(false, name+": expected panic")
			return
		}
		if !strings.Contains(fmt.Sprint(r), "assignment to entry in nil map") {
			ck(false, name+": unexpected panic text")
		}
	}()
	fn()
}

func main() {
	seed = loadSeed()

	var nilStr map[string]int64
	var nilInt map[int]string
	var nilStruct map[struct{ X int }]int64

	// 基本读取语义。
	ck(len(nilStr) == 0, "nil map len mismatch")
	ck(nilStr["missing"] == 0, "nil map lookup zero value mismatch")
	ck(nilStr[""] == 0, "nil map empty key lookup mismatch")
	if v, ok := nilStr["missing"]; ok || v != 0 {
		ck(false, "nil map comma-ok mismatch")
	}
	ck(len(nilInt) == 0, "nil int-key map len mismatch")
	ck(nilInt[42] == "", "nil int-key map lookup mismatch")
	ck(nilStruct[struct{ X int }{X: 1}] == 0, "nil struct-key map lookup mismatch")

	// 遍历、删除与 clear 都是安全的零操作。
	rangeCnt := 0
	for range nilStr {
		rangeCnt++
	}
	ck(rangeCnt == 0, "nil map range count mismatch")
	delete(nilStr, "missing")
	delete(nilInt, 0)
	delete(nilStruct, struct{ X int }{X: 2})
	ck(len(nilStr) == 0 && len(nilInt) == 0, "nil map delete changed length")
	clear(nilStr)
	clear(nilInt)
	ck(len(nilStr) == 0 && len(nilInt) == 0, "nil map clear changed length")

	// 复制 nil map 仍是 nil map。
	var copied map[string]int64 = nilStr
	ck(len(copied) == 0 && copied["x"] == 0, "copied nil map lookup mismatch")
	if _, ok := copied["x"]; ok {
		ck(false, "copied nil map comma-ok mismatch")
	}

	// 结构体字段中的 nil map。
	var h nmHolder
	ck(len(h.m) == 0 && h.m["x"] == 0, "nil map field lookup mismatch")
	h2 := nmHolder{m: map[string]int64{"x": 9}}
	ck(h2.m["x"] == 9 && h.m["x"] == 0, "nil map field isolation mismatch")

	// 函数返回 nil map 的读取与遍历。
	m := nmMaybeNil(false)
	if m != nil {
		ck(false, "nmMaybeNil(false) returned non-nil map")
	}
	ck(len(m) == 0 && m["a"] == 0, "returned nil map lookup mismatch")
	cnt := 0
	for range m {
		cnt++
	}
	ck(cnt == 0, "returned nil map range mismatch")
	nonNil := nmMaybeNil(true)
	ck(nonNil["a"] == 1 && nonNil["b"] == 2, "non-nil returned map lookup mismatch")

	// 链式索引：缺失与 nil 内层 map 的读取都安全。
	nested := map[string]map[string]int64{"a": {"x": 3}}
	ck(nested["missing"]["x"] == 0, "missing nested map read panic/mismatch")
	ck(nested["a"]["missing"] == 0, "nil inner map read mismatch")
	if v, ok := nested["a"]["x"]; !ok || v != 3 {
		ck(false, "nested map comma-ok mismatch")
	}
	if v, ok := nested["a"]["missing"]; ok || v != 0 {
		ck(false, "nested missing key comma-ok mismatch")
	}
	var nilNested map[string]map[string]int64
	ck(nilNested["a"]["b"] == 0, "chained nil map read mismatch")

	// 指针值 map 的读取。
	var pm map[string]*int64
	ck(pm["x"] == nil, "nil map pointer lookup mismatch")
	pv := pm["x"]
	if pv != nil {
		ck(false, "nil map pointer lookup returned non-nil")
	}

	// 非 nil map 中的 nil value 与缺失键的 comma-ok 差异。
	anym := map[string]any{"nil": nil}
	if v, ok := anym["nil"]; !ok || v != nil {
		ck(false, "nil value entry comma-ok mismatch")
	}
	if v, ok := anym["missing"]; ok || v != nil {
		ck(false, "missing nil value entry comma-ok mismatch")
	}

	// 零值与缺失键的区分。
	known := map[int]int64{0: 42}
	if v, ok := known[0]; !ok || v != 42 {
		ck(false, "zero key present lookup mismatch")
	}
	if v, ok := known[1]; ok || v != 0 {
		ck(false, "zero key missing lookup mismatch")
	}

	// 接口中的 typed nil map。
	var i any = nilStr
	if i == nil {
		ck(false, "typed nil map in interface equals nil")
	}
	im, ok := i.(map[string]int64)
	ck(ok, "typed nil map assertion failed")
	ck(im == nil && len(im) == 0 && im["x"] == 0, "typed nil map read mismatch")
	if _, ok := im["x"]; ok {
		ck(false, "typed nil map comma-ok mismatch")
	}

	// 零大小 value 与 nil map 的 comma-ok。
	zset := map[string]struct{}{"a": {}, "b": {}}
	if v, ok := zset["a"]; !ok || v != struct{}{} {
		ck(false, "zero-size value present lookup mismatch")
	}
	if v, ok := zset["missing"]; ok || v != struct{}{} {
		ck(false, "zero-size value missing lookup mismatch")
	}
	var nmz map[string]struct{}
	if v, ok := nmz["x"]; ok || v != struct{}{} {
		ck(false, "nil zero-size value map lookup mismatch")
	}

	// 大量读取循环，全部返回零值。
	reads := 1500 + int(seed%500)
	var sum int64
	okCount := 0
	for i := 0; i < reads; i++ {
		v, ok := nilStr["fixed-key"]
		sum += v
		if ok {
			okCount++
		}
	}
	ck(sum == 0 && okCount == 0, "nil map read loop produced non-zero result")

	// GC 后仍为 nil map，读取语义不变。
	gcStress()
	ck(len(nilStr) == 0, "nil map len changed after GC")
	ck(nilStr["after-gc"] == 0, "nil map lookup changed after GC")
	if _, ok := nilStr["after-gc"]; ok {
		ck(false, "nil map comma-ok changed after GC")
	}
	ck(i != nil, "typed nil map interface corrupted after GC")
	ck(len(zset) == 2, "zero-size value map corrupted after GC")

	// 只有写入 nil map 才 panic。
	expectNilMapWritePanic("string-map-write", func() { nilStr["x"] = 1 })
	expectNilMapWritePanic("int-map-write", func() { nilInt[1] = "x" })
	ck(len(nilStr) == 0 && nilStr["x"] == 0, "nil map changed after recovered write panic")

	finish("l11_nil_map_read")
}
