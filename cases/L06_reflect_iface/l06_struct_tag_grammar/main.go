// L06: struct tag 文法：Get/Lookup、缺省/空值/畸形 tag、逗号选项、转义引号、重复键、
// 嵌入字段与未导出字段的 tag 读取，跨 GC 保持元数据一致。
package main

import (
	"fmt"
	"os"
	"reflect"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
)

type node struct {
	V     int64
	Label string
}

type tagged struct {
	Name   string `json:"name,omitempty" xml:"name" db:"name"`
	Count  int    `json:"count" db:"count,primary" form:"count"`
	Quote  string `json:"a\"b" xml:",attr"`
	Empty  int    `json:""`
	NoTag  int
	Broken int `json`
	Single int `json:'single'`
	Spaced int `  json:"spaced"  `
	Dupe   int `json:"first" json:"second"`
	Dash   int `json:"-"`
	Store  int `gorm:"column:store;type:varchar(32);index:idx_store,unique"`
}

type Embedded struct {
	Promoted string `json:"promoted"`
	unexp    int    `json:"unexp"`
}

type withEmbed struct {
	Embedded `json:"embedded"`
	Extra    int `json:"extra"`
}

var (
	failed    int32
	gcSink    [][]byte
	fieldSink []reflect.StructField
	tagSink   []string
	anySink   []any
	uafSeed   int64 = 1
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

func splitOptions(v string) []string {
	if v == "" {
		return nil
	}
	return strings.Split(v, ",")
}

func parseSemi(tag string) string {
	opts := make(map[string]string)
	for _, part := range strings.Split(tag, ";") {
		if part == "" {
			continue
		}
		if i := strings.IndexByte(part, ':'); i >= 0 {
			opts[part[:i]] = part[i+1:]
		} else {
			opts[part] = ""
		}
	}
	keys := make([]string, 0, len(opts))
	for k := range opts {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for i, k := range keys {
		if i > 0 {
			b.WriteByte(';')
		}
		b.WriteString(k)
		b.WriteByte('=')
		b.WriteString(opts[k])
	}
	return b.String()
}

func main() {
	uafSeed = envSeed()

	t := reflect.TypeOf(tagged{})
	ck(t.NumField() == 11, "tagged field count")

	names := []string{"Name", "Count", "Quote", "Empty", "NoTag", "Broken", "Single", "Spaced", "Dupe", "Dash", "Store"}
	for i, name := range names {
		f := t.Field(i)
		ck(f.Name == name, "field order "+strconv.Itoa(i))
		ck(len(f.Index) == 1 && f.Index[0] == i, "field index "+name)
		ck(!f.Anonymous && f.IsExported(), "field flags "+name)
		ck(f.PkgPath == "", "exported field pkg path "+name)
		ck(f.Offset%uintptr(f.Type.Align()) == 0, "field offset alignment "+name)
	}

	get := func(name, key string) (string, bool) {
		f, ok := t.FieldByName(name)
		if !ok {
			return "", false
		}
		return f.Tag.Lookup(key)
	}
	wantGet := []struct {
		field string
		key   string
		val   string
		ok    bool
	}{
		{"Name", "json", "name,omitempty", true},
		{"Name", "xml", "name", true},
		{"Name", "db", "name", true},
		{"Count", "json", "count", true},
		{"Count", "db", "count,primary", true},
		{"Count", "form", "count", true},
		{"Quote", "json", `a"b`, true},
		{"Quote", "xml", ",attr", true},
		{"Empty", "json", "", true},
		{"NoTag", "json", "", false},
		{"Broken", "json", "", false},
		{"Single", "json", "", false},
		{"Spaced", "json", "spaced", true},
		{"Dupe", "json", "first", true},
		{"Dash", "json", "-", true},
		{"Store", "gorm", "column:store;type:varchar(32);index:idx_store,unique", true},
		{"Store", "json", "", false},
		{"NoTag", "nosuch", "", false},
	}
	for _, w := range wantGet {
		v, ok := get(w.field, w.key)
		ck(v == w.val && ok == w.ok, "tag lookup "+w.field+"."+w.key)
	}

	ck(splitOptions(mustTag(t, "Name", "json"))[1] == "omitempty", "comma options keep order")
	ck(len(splitOptions(mustTag(t, "Dash", "json"))) == 1, "dash tag single option")
	ck(parseSemi(mustTag(t, "Store", "gorm")) == "column=store;index=idx_store,unique;type=varchar(32)", "semicolon tag options")

	// 独立的 StructTag 字面值：带引号逗号、转义引号、缺失值与未闭合值。
	st := reflect.StructTag(`json:"a,b" xml:"x" my:"v"`)
	v, ok := st.Lookup("json")
	ck(ok && v == "a,b", "quoted comma stays in value")
	ck(st.Get("xml") == "x" && st.Get("my") == "v", "multiple tag keys")
	_, ok = st.Lookup("none")
	ck(!ok, "absent tag key")
	stEsc := reflect.StructTag(`json:"a\"b"`)
	ck(stEsc.Get("json") == `a"b`, "escaped quote decoded")
	stNoValue := reflect.StructTag(`json`)
	_, ok = stNoValue.Lookup("json")
	ck(!ok, "key without value is invalid")
	stOpen := reflect.StructTag(`json:"unterminated`)
	_, ok = stOpen.Lookup("json")
	ck(!ok, "unterminated quoted value is invalid")
	stSingle := reflect.StructTag(`json:'single'`)
	_, ok = stSingle.Lookup("json")
	ck(!ok, "single quoted value is invalid")
	stSpace := reflect.StructTag(`  json:"lead"  `)
	ck(stSpace.Get("json") == "lead", "surrounding spaces tolerated")

	// 嵌入字段：VisibleFields 同时暴露匿名字段自身与提升字段。
	we := reflect.TypeOf(withEmbed{})
	vis := reflect.VisibleFields(we)
	visNames := make([]string, 0, len(vis))
	for _, f := range vis {
		visNames = append(visNames, f.Name)
	}
	ck(strings.Join(visNames, ",") == "Embedded,Promoted,unexp,Extra", "visible field order")
	eff, _ := we.FieldByName("Embedded")
	ck(eff.Anonymous && eff.Tag.Get("json") == "embedded", "embedded field anonymous tag")
	pf, _ := we.FieldByName("Promoted")
	ck(!pf.Anonymous && pf.Tag.Get("json") == "promoted" && sameIndex(pf.Index, []int{0, 0}), "promoted field tag and index")
	uf, _ := we.FieldByName("unexp")
	ck(!uf.IsExported() && uf.PkgPath == "main" && uf.Tag.Get("json") == "unexp", "unexported field tag readable")
	_, missing := we.FieldByName("Missing")
	ck(!missing, "missing field lookup fails")

	plan := make([]string, 0, 4)
	for _, f := range vis {
		if f.Anonymous || !f.IsExported() {
			continue
		}
		tag, ok := f.Tag.Lookup("json")
		if !ok || tag == "" || tag == "-" {
			continue
		}
		plan = append(plan, f.Name+"="+tag)
	}
	ck(strings.Join(plan, "|") == "Promoted=promoted|Extra=extra", "visible tagged exported plan")

	// 值读取与 tag 元数据跨 GC。
	for r := 0; r < 20; r++ {
		base := uafSeed*10 + int64(r)
		w := withEmbed{Embedded: Embedded{Promoted: fmt.Sprintf("p-%d", base), unexp: int(base)}, Extra: int(base + 1)}
		anySink = []any{w, &w}
		fieldSink = append(fieldSink[:0], pf, uf)
		tagSink = append(tagSink[:0], string(pf.Tag), string(uf.Tag))
		gcStress()
		val := reflect.ValueOf(&w).Elem()
		pv := val.FieldByIndex([]int{0, 0})
		uv := val.FieldByIndex([]int{0, 1})
		ck(pv.String() == fmt.Sprintf("p-%d", base), "promoted value after GC")
		ck(uv.Int() == base, "unexported value readable by reflection after GC")
		ck(!uv.CanInterface(), "unexported value cannot be interfaced")
		ck(fieldSink[0].Name == "Promoted" && fieldSink[0].Tag.Get("json") == "promoted", "field metadata after GC")
		ck(fieldSink[1].Name == "unexp" && fieldSink[1].Tag.Get("json") == "unexp", "unexported metadata after GC")
		ck(tagSink[0] == string(fieldSink[0].Tag) && tagSink[1] == string(fieldSink[1].Tag), "tag strings after GC")
		ck(w.Extra == int(base+1), "outer field after GC")
	}
	finish("l06_struct_tag_grammar")
}

func mustTag(t reflect.Type, field, key string) string {
	f, ok := t.FieldByName(field)
	if !ok {
		return ""
	}
	return f.Tag.Get(key)
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
