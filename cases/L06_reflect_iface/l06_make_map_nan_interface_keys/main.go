// L06: MakeMap/MakeSlice 边界：NaN key 每次插入独立、查找恒失败、删除无效，±0 相等，
// 接口 key 动态类型区分，typed nil key，切片元素作 key，零尺寸元素 Append，全部跨 GC。
package main

import (
	"fmt"
	"math"
	"os"
	"reflect"
	"runtime"
	"strconv"
	"sync/atomic"
)

type node struct {
	V     int64
	Label string
}

type myInt int64

var (
	failed  int32
	gcSink  [][]byte
	anySink any
	uafSeed int64 = 1
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
		fmt.Printf("FAILED %s seed=%d (%d failed checks)\n", name, uafSeed, n)
		os.Exit(1)
	}
	fmt.Println("ALL PASS " + name)
}

func envSeed() int64 {
	s := os.Getenv("UAF_SEED")
	if s == "" {
		return 1
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil || n <= 0 {
		return 1
	}
	return n
}

// sumNodes 通过 MapRange 汇总，避免依赖 map 遍历顺序。
func sumNodes(m reflect.Value) (int, int64) {
	count := 0
	var sum int64
	it := m.MapRange()
	for it.Next() {
		v, ok := it.Value().Interface().(*node)
		if !ok || v == nil {
			continue
		}
		count++
		sum += v.V
	}
	return count, sum
}

func main() {
	uafSeed = envSeed()
	for r := int64(0); r < 6; r++ {
		base := uafSeed*100 + r*7

		// float key：每次 NaN 插入都是独立条目；NaN 查找恒失败；Delete 无效。
		ft := reflect.TypeOf(map[float64]*node{})
		fm := reflect.MakeMapWithSize(ft, 8)
		for i := int64(0); i < 3; i++ {
			fm.SetMapIndex(reflect.ValueOf(math.NaN()), reflect.ValueOf(&node{V: base + i + 1, Label: "nan"}))
		}
		ck(fm.Len() == 3, "each NaN key is a distinct entry")
		ck(!fm.MapIndex(reflect.ValueOf(math.NaN())).IsValid(), "NaN key lookup always fails")
		fm.SetMapIndex(reflect.ValueOf(0.0), reflect.ValueOf(&node{V: base + 10, Label: "poszero"}))
		fm.SetMapIndex(reflect.ValueOf(math.Copysign(0, -1)), reflect.ValueOf(&node{V: base + 11, Label: "negzero"}))
		ck(fm.Len() == 4, "+0 and -0 are the same key")
		zero := fm.MapIndex(reflect.ValueOf(0.0))
		ck(zero.IsValid() && zero.Interface().(*node).V == base+11, "-0 insertion overwrites +0 value")
		fm.SetMapIndex(reflect.ValueOf(math.NaN()), reflect.Value{})
		ck(fm.Len() == 4, "deleting NaN key is a no-op")
		count, sum := sumNodes(fm)
		ck(count == 4 && sum == 3*base+6+base+11, "float map range sum")
		keys := fm.MapKeys()
		nanKeys := 0
		for _, k := range keys {
			if k.Kind() == reflect.Float64 && math.IsNaN(k.Float()) {
				nanKeys++
			}
		}
		ck(len(keys) == 4 && nanKeys == 3, "MapKeys returns all NaN entries")

		// 接口 key：动态类型参与相等；typed nil 指针可作 key；NaN 仍不可查找。
		it := reflect.TypeOf(map[any]*node{})
		im := reflect.MakeMap(it)
		im.SetMapIndex(reflect.ValueOf(int64(1)), reflect.ValueOf(&node{V: base + 1, Label: "int64"}))
		im.SetMapIndex(reflect.ValueOf(int(1)), reflect.ValueOf(&node{V: base + 2, Label: "int"}))
		im.SetMapIndex(reflect.ValueOf("1"), reflect.ValueOf(&node{V: base + 3, Label: "string"}))
		im.SetMapIndex(reflect.ValueOf(myInt(1)), reflect.ValueOf(&node{V: base + 4, Label: "myInt"}))
		im.SetMapIndex(reflect.ValueOf(math.NaN()), reflect.ValueOf(&node{V: base + 5, Label: "nan1"}))
		im.SetMapIndex(reflect.ValueOf(math.NaN()), reflect.ValueOf(&node{V: base + 6, Label: "nan2"}))
		im.SetMapIndex(reflect.ValueOf((*node)(nil)), reflect.ValueOf(&node{V: base + 7, Label: "typednil"}))
		ck(im.Len() == 7, "interface keys distinguish dynamic types")
		im.SetMapIndex(reflect.ValueOf((*node)(nil)), reflect.ValueOf(&node{V: base + 8, Label: "typednil2"}))
		ck(im.Len() == 7, "typed nil pointer keys are equal")
		v := im.MapIndex(reflect.ValueOf(int64(1)))
		ck(v.IsValid() && v.Interface().(*node).V == base+1, "int64 interface key lookup")
		v = im.MapIndex(reflect.ValueOf(int(1)))
		ck(v.IsValid() && v.Interface().(*node).V == base+2, "int interface key lookup")
		ck(!im.MapIndex(reflect.ValueOf(any(math.NaN()))).IsValid(), "interface NaN key lookup fails")
		v = im.MapIndex(reflect.ValueOf((*node)(nil)))
		ck(v.IsValid() && v.Interface().(*node).V == base+8, "typed nil key lookup")
		count, sum = sumNodes(im)
		ck(count == 7 && sum == 7*base+29, "interface map range sum")

		// 切片元素作 map key：NaN 元素独立，±0 合并。
		st := reflect.TypeOf([]float64(nil))
		sl := reflect.MakeSlice(st, 4, 4)
		sl.Index(0).SetFloat(math.NaN())
		sl.Index(1).SetFloat(0)
		sl.Index(2).SetFloat(math.Copysign(0, -1))
		sl.Index(3).SetFloat(math.NaN())
		ck(sl.Len() == 4 && sl.Cap() == 4, "MakeSlice len/cap")
		sm := reflect.MakeMap(ft)
		for i := 0; i < sl.Len(); i++ {
			sm.SetMapIndex(sl.Index(i), reflect.ValueOf(&node{V: base + int64(20+i), Label: "slicekey"}))
		}
		ck(sm.Len() == 3, "slice NaN/zero keys collapse as expected")
		zv := sm.MapIndex(reflect.ValueOf(0.0))
		ck(zv.IsValid() && zv.Interface().(*node).V == base+22, "last zero slice key overwrites")
		ck(!sm.MapIndex(reflect.ValueOf(math.NaN())).IsValid(), "slice NaN key not lookupable")

		// Append 超过 cap 后 NaN 与 -0 值仍保持。
		sl2 := reflect.MakeSlice(st, 0, 1)
		sl2 = reflect.Append(sl2, reflect.ValueOf(math.NaN()))
		sl2 = reflect.Append(sl2, reflect.ValueOf(math.Copysign(0, -1)))
		ck(sl2.Len() == 2 && sl2.Cap() >= 2, "Append grows slice")
		ck(math.IsNaN(sl2.Index(0).Float()) && math.Signbit(sl2.Index(1).Float()), "appended NaN/-0 values")

		// 零尺寸元素切片：大 cap 合法，Append 在 cap 内不增长。
		zt := reflect.TypeOf([]struct{}{})
		zs := reflect.MakeSlice(zt, 0, 1024)
		zs = reflect.Append(zs, reflect.Zero(reflect.TypeOf(struct{}{})))
		zs = reflect.Append(zs, reflect.Zero(reflect.TypeOf(struct{}{})))
		ck(zs.Len() == 2 && zs.Cap() == 1024, "zero-size element slice append within cap")

		// 错误 key 类型查找必须 panic。
		func() {
			defer func() { ck(recover() != nil, "MapIndex with wrong key type panics") }()
			fm.MapIndex(reflect.ValueOf("not-a-float"))
		}()

		// 只保留容器槽，GC 后 NaN 条目与指针值仍完整。
		anySink = []any{fm, im, sl, sl2, zs}
		gcStress()
		fm2 := anySink.([]any)[0].(reflect.Value)
		count, sum = sumNodes(fm2)
		ck(count == 4 && sum == 4*base+17, "float map after GC")
		im2 := anySink.([]any)[1].(reflect.Value)
		count, sum = sumNodes(im2)
		ck(count == 7 && sum == 7*base+29, "interface map after GC")
		sl2v := anySink.([]any)[3].(reflect.Value)
		ck(math.IsNaN(sl2v.Index(0).Float()) && math.Signbit(sl2v.Index(1).Float()), "appended NaN/-0 after GC")
	}

	finish("l06_make_map_nan_interface_keys")
}
