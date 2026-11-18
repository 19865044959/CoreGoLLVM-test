// L06: StructTag 病态文法：制表/换行分隔、未加引号、未闭合、空 key、非法转义、DEL 字符、
// 逗号/引号/反斜杠/tab 值，以及 StructOf 原样保存 tag 后跨 GC 读取。
package main

import (
	"fmt"
	"os"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
)

type tagMatrix struct {
	Good      string `json:"good,omitempty" db:"good_col"`
	Quoted    string `json:"a,b" db:"quoted"`
	Escaped   string `json:"a\"b" db:"escaped"`
	Backslash string `json:"a\\b" db:"backslash"`
	TabValue  string `json:"a\tb" db:"tab_value"`
	PunctKey  string `a-b_c9:"v" db:"punct"`
	DotKey    string `a.b:"v" db:"dot"`
	Empty     string `json:"" db:""`
	EmptyTag  string
	Spaced    string "   json:\"spaced\"   db:\"spaced_db\"   "
	Trailing  string `json:"junk" trailing`
	BadEscape string `json:"bad\q" db:"after_bad"`
	Unquoted  string `json:bad db:"after_unquoted"`
	Unclosed  string `json:"unclosed`
	EmptyKey  string `:"" db:"after_empty"`
	TabSep    string "json:\"tab\"\tdb:\"not_seen\""
	NLsep     string "json:\"nl\"\ndb:\"not_seen_nl\""
	DelChar   string "js\x7fon:\"v\" db:\"not_seen_del\""
	SpaceKey  string "bad key:\"v\" db:\"after_space\""
}

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

func fieldTag(t reflect.Type, name string) reflect.StructTag {
	f, _ := t.FieldByName(name)
	return f.Tag
}

func main() {
	uafSeed = envSeed()
	t := reflect.TypeOf(tagMatrix{})

	// 合法文法：选项、转义引号/反斜杠、值内 tab、带点/连字符/数字的 key。
	v, ok := fieldTag(t, "Good").Lookup("json")
	ck(ok && v == "good,omitempty", "json tag options retained")
	parts := strings.Split(v, ",")
	ck(len(parts) == 2 && parts[0] == "good" && parts[1] == "omitempty", "json tag option split")
	v, ok = fieldTag(t, "Quoted").Lookup("json")
	ck(ok && v == "a,b", "quoted comma stays in tag value")
	v, ok = fieldTag(t, "Escaped").Lookup("json")
	ck(ok && v == `a"b`, "escaped quote decoded")
	v, ok = fieldTag(t, "Backslash").Lookup("json")
	ck(ok && v == `a\b`, "escaped backslash decoded")
	v, ok = fieldTag(t, "TabValue").Lookup("json")
	ck(ok && v == "a\tb", "escaped tab decoded")
	v, ok = fieldTag(t, "PunctKey").Lookup("a-b_c9")
	ck(ok && v == "v", "dash/underscore/digit key parsed")
	v, ok = fieldTag(t, "PunctKey").Lookup("db")
	ck(ok && v == "punct", "key after punctuation key parsed")
	v, ok = fieldTag(t, "DotKey").Lookup("a.b")
	ck(ok && v == "v", "dot in key parsed")

	// 空值 vs 缺失。
	v, ok = fieldTag(t, "Empty").Lookup("json")
	ck(ok && v == "", "empty quoted value is present")
	v, ok = fieldTag(t, "Empty").Lookup("db")
	ck(ok && v == "", "second empty value is present")
	ck(fieldTag(t, "EmptyTag") == "", "absent tag is empty string")
	_, ok = fieldTag(t, "EmptyTag").Lookup("json")
	ck(!ok, "lookup on empty tag fails")

	// 前导/尾随空格合法。
	v, ok = fieldTag(t, "Spaced").Lookup("json")
	ck(ok && v == "spaced", "leading spaces skipped")
	v, ok = fieldTag(t, "Spaced").Lookup("db")
	ck(ok && v == "spaced_db", "multiple spaces between pairs")
	_, ok = fieldTag(t, "Spaced").Lookup("nope")
	ck(!ok, "missing key in spaced tag")

	// 尾部垃圾不影响已解析键，但垃圾本身不可见。
	v, ok = fieldTag(t, "Trailing").Lookup("json")
	ck(ok && v == "junk", "valid key before trailing junk")
	_, ok = fieldTag(t, "Trailing").Lookup("trailing")
	ck(!ok, "trailing junk is not a key")

	// 非法转义：该 key 失败，后面的 well-formed key 仍可读。
	_, ok = fieldTag(t, "BadEscape").Lookup("json")
	ck(!ok, "invalid escape makes key invisible")
	v, ok = fieldTag(t, "BadEscape").Lookup("db")
	ck(ok && v == "after_bad", "later key after invalid escape parsed")

	// 未加引号/未闭合/空 key/非法分隔符会破坏整个 tag 的解析。
	for _, name := range []string{"Unquoted", "Unclosed", "EmptyKey"} {
		_, jok := fieldTag(t, name).Lookup("json")
		_, dok := fieldTag(t, name).Lookup("db")
		ck(!jok && !dok, "malformed tag hides all keys: "+name)
	}
	_, ok = fieldTag(t, "TabSep").Lookup("json")
	ck(ok, "key before tab separator parsed")
	_, ok = fieldTag(t, "TabSep").Lookup("db")
	ck(!ok, "tab is not a tag separator")
	_, ok = fieldTag(t, "NLsep").Lookup("json")
	ck(ok, "key before newline separator parsed")
	_, ok = fieldTag(t, "NLsep").Lookup("db")
	ck(!ok, "newline is not a tag separator")
	_, ok = fieldTag(t, "DelChar").Lookup("json")
	ck(!ok, "DEL character in key stops parsing")
	_, ok = fieldTag(t, "SpaceKey").Lookup("bad")
	ck(!ok, "space in key stops parsing")

	// 字段元数据与 tag 原文。
	f, ok := t.FieldByName("Quoted")
	ck(ok && f.Index[0] == 1 && f.Type == reflect.TypeOf(""), "field index and type")
	ck(f.Tag == `json:"a,b" db:"quoted"`, "raw tag string preserved")

	// 基于 tag 的确定性绑定计划（仅导出、非匿名、json 名非空且非 "-"）。
	plan := make([]string, 0, 12)
	for i := 0; i < t.NumField(); i++ {
		sf := t.Field(i)
		if sf.PkgPath != "" || sf.Anonymous {
			continue
		}
		jv, jok := sf.Tag.Lookup("json")
		if !jok || jv == "" || jv == "-" {
			continue
		}
		jsonName := strings.Split(jv, ",")[0]
		if jsonName == "" || jsonName == "-" {
			continue
		}
		plan = append(plan, sf.Name+":"+jsonName)
	}
	wantPlan := []string{
		"Good:good", "Quoted:a", `Escaped:a"b`, `Backslash:a\b`, "TabValue:a\tb",
		"Spaced:spaced", "Trailing:junk", "TabSep:tab", "NLsep:nl",
	}
	ck(len(plan) == len(wantPlan), "binding plan length")
	if len(plan) == len(wantPlan) {
		for i := range wantPlan {
			ck(plan[i] == wantPlan[i], "binding plan entry: "+wantPlan[i])
		}
	}

	// StructOf 原样保存 tag，并跨 GC 可 Lookup。
	quoted, _ := t.FieldByName("Quoted")
	so := reflect.StructOf([]reflect.StructField{{Name: "Q", Type: reflect.TypeOf(""), Tag: quoted.Tag}})
	sv, sok := so.Field(0).Tag.Lookup("json")
	ck(sok && sv == "a,b", "StructOf preserves quoted tag")
	badTag := reflect.StructTag("json:\"x\\q\" db:\"ok\"")
	so2 := reflect.StructOf([]reflect.StructField{{Name: "B", Type: reflect.TypeOf(""), Tag: badTag}})
	_, bok := so2.Field(0).Tag.Lookup("json")
	dv2, dok2 := so2.Field(0).Tag.Lookup("db")
	ck(!bok && dok2 && dv2 == "ok", "StructOf preserves malformed tag semantics")
	anySink = []any{so, so2, t}

	// 反射写入所有导出字段，GC 后值与 tag 都保持。
	val := reflect.New(t).Elem()
	suffix := fmt.Sprintf("-%d", uafSeed)
	for i := 0; i < t.NumField(); i++ {
		if t.Field(i).PkgPath != "" {
			continue
		}
		val.Field(i).SetString(t.Field(i).Name + suffix)
	}
	gcStress()
	for i := 0; i < t.NumField(); i++ {
		if t.Field(i).PkgPath != "" {
			continue
		}
		ck(val.Field(i).String() == t.Field(i).Name+suffix, "field value after GC: "+t.Field(i).Name)
	}
	ck(fieldTag(t, "Good").Get("json") == "good,omitempty", "tag after GC")
	sv, sok = anySink.([]any)[0].(reflect.Type).Field(0).Tag.Lookup("json")
	ck(sok && sv == "a,b", "StructOf tag after GC")

	finish("l06_struct_tag_pathological_parse")
}
