// L06: VisibleFields 驱动字段物化：按 Index 路径分配 nil 嵌入指针、按 tag 选择叶子字段并写入，
// FieldByIndexErr 错误路径与分配后回读，跨 GC 保持嵌套指针链。
package main

import (
	"fmt"
	"os"
	"reflect"
	"runtime"
	"strconv"
	"sync/atomic"
)

type Base struct {
	ID   int64  `bind:"id"`
	Note string `bind:"note,omitempty"`
}

type Detail struct {
	*Base
	Owner  string `bind:"owner"`
	hidden int64
}

type Doc struct {
	*Detail
	Title string `bind:"title"`
	Skip  int64  `bind:"-"`
	Extra string
}

type bindField struct {
	name  string
	index []int
	tag   string
}

var (
	failed  int32
	gcSink  [][]byte
	plan    []bindField
	anySink []any
	uafSeed int64 = 1
)

func ck(ok bool, msg string) {
	if !ok {
		atomic.AddInt32(&failed, 1)
		fmt.Println("FAIL:", msg)
	}
}

func mustPanic(name string, fn func()) {
	defer func() {
		if recover() == nil {
			ck(false, name+" did not panic")
		}
	}()
	fn()
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

func sameIndex(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// ensureField 沿 Index 路径下钻，遇到 nil 的 *struct 嵌入指针时分配新对象。
func ensureField(root reflect.Value, idx []int) (reflect.Value, bool) {
	cur := root
	for _, j := range idx {
		for cur.Kind() == reflect.Ptr {
			if cur.IsNil() {
				if cur.Type().Elem().Kind() != reflect.Struct {
					return reflect.Value{}, false
				}
				cur.Set(reflect.New(cur.Type().Elem()))
			}
			cur = cur.Elem()
		}
		if cur.Kind() != reflect.Struct || j < 0 || j >= cur.NumField() {
			return reflect.Value{}, false
		}
		cur = cur.Field(j)
	}
	if !cur.IsValid() || !cur.CanSet() {
		return reflect.Value{}, false
	}
	return cur, true
}

func main() {
	uafSeed = envSeed()

	dt := reflect.TypeOf(Doc{})
	vis := reflect.VisibleFields(dt)
	names := make([]string, 0, len(vis))
	for _, f := range vis {
		names = append(names, f.Name)
	}
	ck(fmt.Sprint(names) == "[Detail Base ID Note Owner hidden Title Skip Extra]", "visible fields of Doc")

	wantIdx := map[string][]int{
		"Detail": {0},
		"Base":   {0, 0},
		"ID":     {0, 0, 0},
		"Note":   {0, 0, 1},
		"Owner":  {0, 1},
		"hidden": {0, 2},
		"Title":  {1},
		"Skip":   {2},
		"Extra":  {3},
	}
	for _, f := range vis {
		ck(sameIndex(f.Index, wantIdx[f.Name]), "visible index path "+f.Name)
		if f.Name == "Detail" || f.Name == "Base" {
			ck(f.Anonymous, "anonymous embedded field "+f.Name)
		} else {
			ck(!f.Anonymous, "leaf field not anonymous "+f.Name)
		}
	}
	ck(vis[0].Tag.Get("bind") == "", "embedded Detail has no bind tag")
	ck(vis[1].Tag.Get("bind") == "", "embedded Base has no bind tag")
	ck(vis[2].Tag.Get("bind") == "id", "ID bind tag")
	ck(vis[3].Tag.Get("bind") == "note,omitempty", "Note bind tag")
	ck(vis[4].Tag.Get("bind") == "owner", "Owner bind tag")
	ck(!vis[5].IsExported() && vis[5].PkgPath == "main", "hidden unexported metadata")
	ck(vis[6].Tag.Get("bind") == "title", "Title bind tag")
	ck(vis[7].Tag.Get("bind") == "-", "Skip ignored tag")
	ck(vis[8].Tag.Get("bind") == "", "Extra has no bind tag")

	plan = plan[:0]
	for _, f := range vis {
		if f.Anonymous || !f.IsExported() {
			continue
		}
		tag, ok := f.Tag.Lookup("bind")
		if !ok || tag == "" || tag == "-" {
			continue
		}
		plan = append(plan, bindField{name: f.Name, index: append([]int(nil), f.Index...), tag: tag})
	}
	ck(len(plan) == 4, "bind plan size")
	ck(plan[0].name == "ID" && plan[1].name == "Note" && plan[2].name == "Owner" && plan[3].name == "Title", "bind plan order")
	gcStress()
	ck(len(reflect.VisibleFields(dt)) == 9, "visible fields after GC")

	mustPanic("VisibleFields on non-struct", func() { reflect.VisibleFields(reflect.TypeOf(0)) })

	for r := 0; r < 30; r++ {
		base := uafSeed*100 + int64(r)
		doc := &Doc{}
		root := reflect.ValueOf(doc).Elem()

		// 物化前：nil 嵌入指针使深层访问失败，浅层路径正常。
		if _, err := root.FieldByIndexErr([]int{0, 0, 0}); err == nil {
			ck(false, "deep field before materialization should error")
		}
		mustPanic("FieldByIndex through nil embedded pointer", func() { root.FieldByIndex([]int{0, 0, 0}) })
		mustPanic("FieldByName through nil embedded pointer", func() { root.FieldByName("ID") })
		if _, err := root.FieldByIndexErr([]int{1}); err != nil {
			ck(false, "top-level field before materialization should succeed")
		}

		for i, bf := range plan {
			fv, ok := ensureField(root, bf.index)
			if !ok {
				ck(false, "ensure field "+bf.name)
				continue
			}
			switch fv.Kind() {
			case reflect.Int64:
				fv.SetInt(base + int64(10+i))
			case reflect.String:
				fv.SetString(fmt.Sprintf("%s-%d", bf.name, base))
			default:
				ck(false, "unexpected bind field kind "+bf.name)
			}
		}

		ck(doc.Detail != nil && doc.Detail.Base != nil, "nested embedded pointers allocated")
		ck(doc.ID == base+10, "ID bound")
		ck(doc.Note == fmt.Sprintf("Note-%d", base), "Note bound")
		ck(doc.Owner == fmt.Sprintf("Owner-%d", base), "Owner bound")
		ck(doc.Title == fmt.Sprintf("Title-%d", base), "Title bound")
		ck(doc.Skip == 0 && doc.Extra == "", "skipped and untagged fields untouched")
		ck(doc.hidden == 0, "unexported field untouched")

		// 物化后：FieldByIndexErr 与 FieldByName 均可读取。
		for _, bf := range plan {
			v, err := root.FieldByIndexErr(bf.index)
			ck(err == nil && v.IsValid(), "FieldByIndexErr after materialization "+bf.name)
			byName := root.FieldByName(bf.name)
			ck(byName.IsValid() && byName.Type() == v.Type(), "FieldByName after materialization "+bf.name)
		}
		idv, err := root.FieldByIndexErr([]int{0, 0, 0})
		ck(err == nil && idv.Int() == base+10, "ID via FieldByIndexErr")
		nv, err := root.FieldByIndexErr([]int{0, 0, 1})
		ck(err == nil && nv.String() == fmt.Sprintf("Note-%d", base), "Note via FieldByIndexErr")

		anySink = []any{doc, &doc.Detail.Base.ID, doc.Detail.Base}
		gcStress()
		ck(doc.Detail != nil && doc.Detail.Base != nil, "embedded pointer chain after GC")
		ck(doc.Detail.Base.ID == base+10 && doc.Detail.Base.Note == fmt.Sprintf("Note-%d", base), "Base fields after GC")
		ck(doc.Detail.Owner == fmt.Sprintf("Owner-%d", base) && doc.Title == fmt.Sprintf("Title-%d", base), "leaf strings after GC")
		root2 := reflect.ValueOf(doc).Elem()
		v2, err := root2.FieldByIndexErr([]int{0, 0, 1})
		ck(err == nil && v2.String() == fmt.Sprintf("Note-%d", base), "visible path after GC")
		back := v2.Addr().Elem()
		ck(back.String() == v2.String(), "addressable visible field after GC")

		// 清空中间指针后深层路径重新失败，浅层路径仍可读。
		doc.Detail.Base = nil
		if _, err := root2.FieldByIndexErr([]int{0, 0, 0}); err == nil {
			ck(false, "deep field after clearing Base should error")
		}
		if _, err := root2.FieldByIndexErr([]int{0, 1}); err != nil {
			ck(false, "Owner after clearing Base should succeed")
		}
	}
	finish("l06_visiblefields_materialize")
}
