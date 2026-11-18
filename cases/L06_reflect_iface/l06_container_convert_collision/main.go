// L06: 容器转换冲突：map key 解析后碰撞、NaN 格式化为同一字符串、接口 key 动态类型碰撞、
// 转换失败原子性、nil vs 空容器与成功路径指针跨 GC。
package main

import (
	"fmt"
	"math"
	"os"
	"runtime"
	"sort"
	"strconv"
	"sync/atomic"
)

type node struct {
	V     int64
	Label string
}

type myString string

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

func envInt(name string, def, min, max int) int {
	s := os.Getenv(name)
	if s == "" {
		return def
	}
	n, err := strconv.Atoi(s)
	if err != nil || n < min {
		return def
	}
	if n > max {
		return max
	}
	return n
}

func sortedStringKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// convertStringKeyMap 解析 key 为 int64；冲突时整体失败，不返回部分结果。
func convertStringKeyMap(src map[string]string) (map[int64]*node, error) {
	if src == nil {
		return nil, nil
	}
	out := make(map[int64]*node, len(src))
	for _, k := range sortedStringKeys(src) {
		n, err := strconv.ParseInt(k, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("key %q: cannot parse int64", k)
		}
		if _, exists := out[n]; exists {
			return nil, fmt.Errorf("duplicate converted key %q -> %d", k, n)
		}
		out[n] = &node{V: n, Label: src[k] + "-" + strconv.FormatInt(uafSeed, 10)}
	}
	return out, nil
}

func convertAnySlice(src []any) ([]int64, error) {
	if src == nil {
		return nil, nil
	}
	out := make([]int64, 0, len(src))
	for i, v := range src {
		x, ok := v.(int64)
		if !ok {
			return nil, fmt.Errorf("element %d: cannot convert %T to int64", i, v)
		}
		out = append(out, x)
	}
	return out, nil
}

func convertIfaceKeyMap(src map[any]string) (map[string]*node, error) {
	type pair struct {
		typ string
		key string
		val string
	}
	pairs := make([]pair, 0, len(src))
	for k, v := range src {
		var key string
		switch x := k.(type) {
		case string:
			key = x
		case myString:
			key = string(x)
		default:
			return nil, fmt.Errorf("key %T: unsupported", k)
		}
		pairs = append(pairs, pair{typ: fmt.Sprintf("%T", k), key: key, val: v})
	}
	sort.Slice(pairs, func(i, j int) bool {
		if pairs[i].key != pairs[j].key {
			return pairs[i].key < pairs[j].key
		}
		return pairs[i].typ < pairs[j].typ
	})
	out := make(map[string]*node, len(pairs))
	for _, p := range pairs {
		if _, exists := out[p.key]; exists {
			return nil, fmt.Errorf("duplicate converted key %q", p.key)
		}
		out[p.key] = &node{V: int64(len(p.typ)), Label: p.val}
	}
	return out, nil
}

// convertFloatKeyMap 把 float key 格式化成文本；NaN 会全部塌缩成 "NaN" 并触发冲突。
func convertFloatKeyMap(src map[float64]string) (map[string]*node, error) {
	type pair struct {
		key string
		val string
	}
	pairs := make([]pair, 0, len(src))
	for k, v := range src {
		pairs = append(pairs, pair{key: strconv.FormatFloat(k, 'g', -1, 64), val: v})
	}
	sort.Slice(pairs, func(i, j int) bool {
		if pairs[i].key != pairs[j].key {
			return pairs[i].key < pairs[j].key
		}
		return pairs[i].val < pairs[j].val
	})
	out := make(map[string]*node, len(pairs))
	for _, p := range pairs {
		if _, exists := out[p.key]; exists {
			return nil, fmt.Errorf("duplicate converted key %q", p.key)
		}
		out[p.key] = &node{V: int64(len(p.val)), Label: p.val}
	}
	return out, nil
}

func convertNested(src map[string]any) (map[string][]int64, error) {
	keys := make([]string, 0, len(src))
	for k := range src {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make(map[string][]int64, len(src))
	for _, k := range keys {
		raw, ok := src[k].([]any)
		if !ok {
			return nil, fmt.Errorf("field %q: want []any, got %T", k, src[k])
		}
		vals, err := convertAnySlice(raw)
		if err != nil {
			return nil, fmt.Errorf("field %q: %w", k, err)
		}
		out[k] = vals
	}
	return out, nil
}

func main() {
	uafSeed = envSeed()
	rounds := envInt("TORTURE_ROUNDS", 10, 1, 2000)
	items := envInt("TORTURE_ITEMS", 32, 1, 20000)

	// key 解析碰撞："01" 与 "1" 都转成 1；错误按排序后第一处冲突报告。
	collide := map[string]string{"1": "a", "01": "b"}
	out, err := convertStringKeyMap(collide)
	ck(err != nil && out == nil, "key collision fails with no partial map")
	ck(err != nil && err.Error() == `duplicate converted key "1" -> 1`, "key collision error is deterministic")

	// 失败原子性：调用方保留原目标。
	target := map[int64]*node{7: {V: 7, Label: "sentinel"}}
	res, err := convertStringKeyMap(collide)
	if err == nil {
		target = res
	}
	ck(err != nil && len(target) == 1 && target[7].V == 7, "failed conversion leaves target unchanged")

	// 成功路径：节点只由转换结果保活，GC 后仍完整。
	okSrc := map[string]string{"3": "c", "-1": "a", "10": "b"}
	res, err = convertStringKeyMap(okSrc)
	ck(err == nil && len(res) == 3, "string key conversion succeeds")
	if err == nil {
		anySink = res
		okSrc = nil
		gcStress()
		got, ok := anySink.(map[int64]*node)
		ck(ok && got[3].V == 3 && got[-1].V == -1 && got[10].V == 10, "converted pointer values after GC")
		ck(got[3].Label == "c-"+strconv.FormatInt(uafSeed, 10), "converted label after GC")
	}
	nilRes, err := convertStringKeyMap(nil)
	ck(err == nil && nilRes == nil, "nil source map preserves nil")
	emptyRes, err := convertStringKeyMap(map[string]string{})
	ck(err == nil && emptyRes != nil && len(emptyRes) == 0, "empty source map yields empty map")

	// 切片逐元素转换：错误元素导致整体失败，nil 与空切片语义不同。
	vals, err := convertAnySlice([]any{int64(4), int64(5), int64(6)})
	ck(err == nil && len(vals) == 3 && vals[2] == 6, "any slice conversion")
	badVals, err := convertAnySlice([]any{int64(1), "2"})
	ck(err != nil && badVals == nil, "invalid element fails slice conversion")
	ck(err != nil && err.Error() == "element 1: cannot convert string to int64", "slice error is deterministic")
	nilVals, err := convertAnySlice(nil)
	ck(err == nil && nilVals == nil, "nil slice stays nil")
	emptyVals, err := convertAnySlice([]any{})
	ck(err == nil && emptyVals != nil && len(emptyVals) == 0, "empty slice yields empty slice")

	// 接口 key 的动态类型不同但转换后同名，必须检测冲突。
	ifaceSrc := map[any]string{"k": "plain", myString("k"): "named"}
	iout, err := convertIfaceKeyMap(ifaceSrc)
	ck(err != nil && iout == nil, "interface key collision fails")
	ck(err != nil && err.Error() == `duplicate converted key "k"`, "interface collision error is deterministic")
	ifaceOK := map[any]string{"b": "second", myString("a"): "first"}
	iout, err = convertIfaceKeyMap(ifaceOK)
	ck(err == nil && iout["a"].Label == "first" && iout["b"].Label == "second", "interface key conversion success")
	_, err = convertIfaceKeyMap(map[any]string{1: "x"})
	ck(err != nil && err.Error() == "key int: unsupported", "unsupported interface key")

	// NaN key 格式化后全部变成 "NaN"，触发冲突。
	nanSrc := map[float64]string{}
	nanSrc[math.NaN()] = "a"
	nanSrc[math.NaN()] = "b"
	ck(len(nanSrc) == 2, "two NaN keys are distinct map entries")
	nout, err := convertFloatKeyMap(nanSrc)
	ck(err != nil && nout == nil, "NaN keys collide after formatting")
	ck(err != nil && err.Error() == `duplicate converted key "NaN"`, "NaN collision error is deterministic")
	fout, err := convertFloatKeyMap(map[float64]string{1.5: "x", -2: "y"})
	ck(err == nil && fout["1.5"].V == 1 && fout["-2"].V == 1, "finite float keys convert")

	// 嵌套容器：字段错误包装后返回，nil 字段与非法元素都失败。
	nested := map[string]any{
		"b": []any{int64(2), int64(3)},
		"a": []any{int64(1)},
	}
	nout2, err := convertNested(nested)
	ck(err == nil && nout2["a"][0] == 1 && nout2["b"][1] == 3, "nested conversion success")
	_, err = convertNested(map[string]any{"x": nil})
	ck(err != nil && err.Error() == `field "x": want []any, got <nil>`, "nested nil field error")
	_, err = convertNested(map[string]any{"x": []any{int64(1), "bad"}})
	ck(err != nil && err.Error() == `field "x": element 1: cannot convert string to int64`, "nested element error wraps field")

	// TORTURE：每轮构造冲突与成功转换，结果与错误稳定。
	for r := 0; r < rounds; r++ {
		collide := make(map[string]string, 2*items)
		okMap := make(map[string]string, items)
		for i := 0; i < items; i++ {
			collide[strconv.Itoa(i)] = "a"
			collide["0"+strconv.Itoa(i)] = "b"
			okMap[strconv.Itoa(i*3+1)] = strconv.Itoa(i)
		}
		c, cerr := convertStringKeyMap(collide)
		ck(cerr != nil && c == nil, "torture collision detected")
		okRes, oerr := convertStringKeyMap(okMap)
		ck(oerr == nil && len(okRes) == items, "torture success conversion")
		if r%4 == 0 {
			anySink = okRes
			gcStress()
			got, ok := anySink.(map[int64]*node)
			ck(ok && len(got) == items, "torture converted map after GC")
			if ok && items > 0 {
				ck(got[1].V == 1 && got[1].Label == "0-"+strconv.FormatInt(uafSeed, 10), "torture pointer value after GC")
			}
		}
	}

	finish("l06_container_convert_collision")
}
