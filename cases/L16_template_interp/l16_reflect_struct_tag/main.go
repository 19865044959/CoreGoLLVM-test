// L16: reflect struct tag 解析（json/db/validate、转义、非法 tag、嵌入提升字段）与 GC 后类型元数据稳定。
package main

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
)

type Embedded struct {
	Extra string `json:"extra" xml:"extra"`
}

type meta struct {
	Ver int `json:"ver" db:"ver"`
}

type record struct {
	ID     int    `json:"id" db:"id,primary" validate:"required,min=1"`
	Name   string `json:"name,omitempty" db:"name"`
	Hidden string `json:"-"`
	Meta   meta   `json:"meta"`
	inner  string `json:"inner"`
	Embedded
}

const jsonWant = `{"id":1,"name":"n","meta":{"ver":7},"extra":"e"}`
const jsonWantEmpty = `{"id":1,"meta":{"ver":7},"extra":"e"}`

var (
	failed  int32
	gcSink  [][]byte
	live    []*record
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
	for i := 0; i < 64; i++ {
		_ = &record{ID: i, Name: "trash", Meta: meta{Ver: i}, Embedded: Embedded{Extra: "g"}}
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

func splitOptions(tag string) []string {
	return strings.Split(tag, ",")
}

func verifyType(t reflect.Type, label string) {
	ck(t.NumField() == 6, fmt.Sprintf("%s NumField=%d want 6", label, t.NumField()))
	names := make([]string, 0, 6)
	lastOff := -1
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		names = append(names, f.Name)
		ck(int(f.Offset) > lastOff, fmt.Sprintf("%s field %s offset not increasing", label, f.Name))
		lastOff = int(f.Offset)
		ck(len(f.Index) >= 1 && f.Index[len(f.Index)-1] == i,
			fmt.Sprintf("%s field %s index=%v", label, f.Name, f.Index))
	}
	ck(strings.Join(names, ",") == "ID,Name,Hidden,Meta,inner,Embedded",
		fmt.Sprintf("%s field names=%v", label, names))

	id, ok := t.FieldByName("ID")
	ck(ok, label+": ID field missing")
	if ok {
		ck(id.Tag.Get("json") == "id", fmt.Sprintf("%s ID json=%q", label, id.Tag.Get("json")))
		ck(id.Tag.Get("validate") == "required,min=1", fmt.Sprintf("%s ID validate=%q", label, id.Tag.Get("validate")))
		db, found := id.Tag.Lookup("db")
		ck(found && db == "id,primary", fmt.Sprintf("%s ID db=%q found=%v", label, db, found))
		opts := splitOptions(db)
		ck(len(opts) == 2 && opts[0] == "id" && opts[1] == "primary", fmt.Sprintf("%s ID db options=%v", label, opts))
	}
	name, ok := t.FieldByName("Name")
	ck(ok, label+": Name field missing")
	if ok {
		opts := splitOptions(name.Tag.Get("json"))
		ck(len(opts) == 2 && opts[0] == "name" && opts[1] == "omitempty",
			fmt.Sprintf("%s Name json options=%v", label, opts))
	}
	hidden, ok := t.FieldByName("Hidden")
	ck(ok && hidden.Tag.Get("json") == "-", label+": Hidden json tag")
	inner, ok := t.FieldByName("inner")
	ck(ok, label+": unexported inner field missing")
	if ok {
		ck(inner.PkgPath != "", label+": inner PkgPath empty")
		ck(inner.Tag.Get("json") == "inner", fmt.Sprintf("%s inner tag=%q", label, inner.Tag.Get("json")))
		ck(!inner.IsExported(), label+": inner reported exported")
	}
	extra, ok := t.FieldByName("Extra")
	ck(ok, label+": promoted Extra missing")
	if ok {
		ck(len(extra.Index) == 2 && extra.Index[0] == 5 && extra.Index[1] == 0,
			fmt.Sprintf("%s Extra index=%v", label, extra.Index))
		ck(extra.Tag.Get("json") == "extra" && extra.Tag.Get("xml") == "extra",
			fmt.Sprintf("%s Extra tag=%q", label, extra.Tag))
	}
	emb := t.Field(5)
	ck(emb.Anonymous && emb.Name == "Embedded", fmt.Sprintf("%s embedded field=%+v", label, emb))
	ver := t.FieldByIndex([]int{3, 0})
	ck(ver.Name == "Ver" && ver.Tag.Get("json") == "ver" && ver.Tag.Get("db") == "ver",
		fmt.Sprintf("%s Meta.Ver field=%+v", label, ver))

	// StructTag 解析边界。
	esc := reflect.StructTag(`json:"a\"b"`)
	ev, eok := esc.Lookup("json")
	ck(eok && ev == `a"b`, fmt.Sprintf("%s escaped tag=%q ok=%v", label, ev, eok))
	bad := reflect.StructTag(`bad tag`)
	if v, found := bad.Lookup("bad"); found || v != "" {
		ck(false, fmt.Sprintf("%s malformed tag lookup=%q,%v", label, v, found))
	}
	multi := reflect.StructTag(`json:"x,omitempty" db:"y"`)
	ck(multi.Get("json") == "x,omitempty" && multi.Get("db") == "y", label+": multi-key tag")
	if _, found := t.FieldByName("NodeSuch"); found {
		ck(false, label+": nonexistent field found")
	}
}

func main() {
	uafSeed = envSeed()
	rounds := 3 + int(uafSeed%3)
	rng := rand.New(rand.NewSource(uafSeed))
	t := reflect.TypeOf(record{})

	for r := 0; r < rounds; r++ {
		verifyType(t, fmt.Sprintf("round %d", r))

		// 反射构造对象：导出字段可写，非导出字段不可写。
		v := reflect.New(t).Elem()
		ck(v.FieldByName("ID").CanSet(), fmt.Sprintf("round %d ID not settable", r))
		v.FieldByName("ID").SetInt(1)
		v.FieldByName("Name").SetString("n")
		v.FieldByName("Hidden").SetString("h")
		v.FieldByName("Meta").FieldByName("Ver").SetInt(7)
		ck(v.FieldByName("Extra").CanSet(), fmt.Sprintf("round %d promoted Extra not settable", r))
		v.FieldByName("Extra").SetString("e")
		ck(!v.FieldByName("inner").CanSet(), fmt.Sprintf("round %d unexported inner settable", r))

		rec := v.Addr().Interface().(*record)
		live = append(live[:0], rec)
		b, err := json.Marshal(rec)
		ck(err == nil, fmt.Sprintf("round %d marshal: %v", r, err))
		ck(string(b) == jsonWant, fmt.Sprintf("round %d json\n got: %s\nwant: %s", r, b, jsonWant))

		// 打乱字段访问顺序，确保 Type 的并发/顺序无关读取。
		perm := rng.Perm(t.NumField())
		for _, i := range perm {
			f := t.Field(i)
			ck(f.Name == t.Field(i).Name, fmt.Sprintf("round %d Field(%d) unstable", r, i))
			ck(f.Type.Kind() != reflect.Invalid, fmt.Sprintf("round %d field %s invalid type", r, f.Name))
		}

		// omitempty 与 json:"-" 行为。
		rec.Name = ""
		rec.Hidden = "still here"
		b2, err := json.Marshal(rec)
		ck(err == nil, fmt.Sprintf("round %d marshal empty: %v", r, err))
		ck(string(b2) == jsonWantEmpty, fmt.Sprintf("round %d json omitempty\n got: %s\nwant: %s", r, b2, jsonWantEmpty))
		ck(rec.Hidden == "still here", fmt.Sprintf("round %d hidden field lost", r))

		gcStress()

		ck(reflect.TypeOf(*rec) == t, fmt.Sprintf("round %d type identity changed after GC", r))
		ck(rec.ID == 1 && rec.Name == "" && rec.Meta.Ver == 7 && rec.Embedded.Extra == "e",
			fmt.Sprintf("round %d record fields after GC: %+v", r, rec))
		verifyType(t, fmt.Sprintf("round %d after GC", r))
		b3, err := json.Marshal(rec)
		ck(err == nil, fmt.Sprintf("round %d marshal after GC: %v", r, err))
		ck(string(b3) == jsonWantEmpty, fmt.Sprintf("round %d json after GC\n got: %s\nwant: %s", r, b3, jsonWantEmpty))
	}

	finish("l16_reflect_struct_tag")
}
