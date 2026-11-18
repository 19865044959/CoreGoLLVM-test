// L10: 生成风格 descriptor 表：扁平 message/field/enum/value 表、parent 链、
// 偏移表、路径/字段号查找、描述符构建校验与 GC 稳定性。
package main

import (
	"fmt"
	"math/rand"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
)

var (
	failed  int32
	gcSink  [][]byte
	curSeed int64
)

func ck(ok bool, msg string) {
	if !ok {
		atomic.AddInt32(&failed, 1)
		fmt.Println("FAIL:", msg)
	}
}

func ckSeed(ok bool, msg string) {
	if !ok {
		atomic.AddInt32(&failed, 1)
		fmt.Printf("FAIL: %s (seed=%d)\n", msg, curSeed)
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

// ---- 生成器原始描述（模拟生成文件中的紧凑字面量表） ----

type fieldSpec struct {
	Name     string
	JSONName string
	Number   int32
	Kind     string
	TypeName string
	Repeated bool
	Oneof    int32
}

type msgSpec struct {
	Name   string
	Parent string
	Fields []fieldSpec
}

type enumValueSpec struct {
	Name   string
	Number int32
}

type enumSpec struct {
	Name   string
	Parent string
	Values []enumValueSpec
}

type fileSpec struct {
	Path     string
	Package  string
	Messages []msgSpec
	Enums    []enumSpec
}

var rawFile = fileSpec{
	Path:    "gen/v1.proto",
	Package: "gen.v1",
	Messages: []msgSpec{
		{Name: "Root", Fields: []fieldSpec{
			{Name: "id", JSONName: "id", Number: 1, Kind: "int64", Oneof: -1},
			{Name: "name", JSONName: "name", Number: 2, Kind: "string", Oneof: -1},
			{Name: "kind", JSONName: "kind", Number: 3, Kind: "enum", TypeName: "gen.v1.Kind", Oneof: -1},
			{Name: "child", JSONName: "child", Number: 4, Kind: "message", TypeName: "gen.v1.Root.Child", Oneof: -1},
			{Name: "tags", JSONName: "tags", Number: 5, Kind: "string", Repeated: true, Oneof: -1},
		}},
		{Name: "Child", Parent: "Root", Fields: []fieldSpec{
			{Name: "id", JSONName: "id", Number: 1, Kind: "int32", Oneof: -1},
			{Name: "leaf", JSONName: "leaf", Number: 2, Kind: "message", TypeName: "gen.v1.Root.Child.Leaf", Oneof: -1},
			{Name: "list", JSONName: "list", Number: 3, Kind: "int32", Repeated: true, Oneof: -1},
		}},
		{Name: "Leaf", Parent: "Root.Child", Fields: []fieldSpec{
			{Name: "value", JSONName: "value", Number: 1, Kind: "string", Oneof: -1},
			{Name: "blob", JSONName: "blob", Number: 2, Kind: "bytes", Oneof: -1},
		}},
		{Name: "Outer", Fields: []fieldSpec{
			{Name: "root", JSONName: "root", Number: 1, Kind: "message", TypeName: "gen.v1.Root", Oneof: -1},
			{Name: "flag", JSONName: "flag", Number: 2, Kind: "bool", Oneof: -1},
			{Name: "text", JSONName: "text", Number: 3, Kind: "string", Oneof: 0},
			{Name: "num", JSONName: "num", Number: 4, Kind: "int32", Oneof: 0},
		}},
	},
	Enums: []enumSpec{
		{Name: "Kind", Values: []enumValueSpec{
			{Name: "KIND_UNKNOWN", Number: 0},
			{Name: "KIND_ACTIVE", Number: 1},
			{Name: "KIND_PAUSED", Number: 2},
		}},
		{Name: "State", Parent: "Root", Values: []enumValueSpec{
			{Name: "STATE_UNSET", Number: 0},
			{Name: "STATE_READY", Number: 1},
		}},
	},
}

// ---- 扁平 descriptor 表 ----

type fieldDesc struct {
	Name     string
	JSONName string
	Kind     string
	TypeName string
	Number   int32
	Repeated bool
	Oneof    int32
	Parent   int32
}

type messageDesc struct {
	Name       string
	FullName   string
	Parent     int32
	FieldStart int32
	FieldEnd   int32
}

type enumValueDesc struct {
	Name   string
	Number int32
}

type enumDesc struct {
	Name       string
	FullName   string
	Parent     int32
	ValueStart int32
	ValueEnd   int32
}

type fileDesc struct {
	Path     string
	Package  string
	Messages []messageDesc
	Fields   []fieldDesc
	Enums    []enumDesc
	Values   []enumValueDesc

	msgByName   map[string]int32
	enumByName  map[string]int32
	fieldByNum  []map[int32]int32 // 与 Messages 平行
	allBySymbol map[string]string
}

var validKinds = map[string]bool{
	"bool": true, "int32": true, "int64": true, "uint32": true, "uint64": true,
	"string": true, "bytes": true, "message": true, "enum": true,
}

func buildFile(s fileSpec) (*fileDesc, error) {
	if s.Path == "" {
		return nil, fmt.Errorf("file: path is empty")
	}
	if s.Package == "" {
		return nil, fmt.Errorf("file: package is empty")
	}
	f := &fileDesc{Path: s.Path, Package: s.Package}
	msgRel := make(map[string]int32, len(s.Messages))
	symbols := make(map[string]string, len(s.Messages)+len(s.Enums))

	// 1. message 按声明序展开；parent 必须已声明（保证 parent 链无环且索引递增）。
	for _, m := range s.Messages {
		if m.Name == "" {
			return nil, fmt.Errorf("message name is empty")
		}
		rel := m.Name
		parent := int32(-1)
		if m.Parent != "" {
			pi, ok := msgRel[m.Parent]
			if !ok {
				return nil, fmt.Errorf("message %q: unknown parent %q", m.Name, m.Parent)
			}
			parent = pi
			rel = m.Parent + "." + m.Name
		}
		full := s.Package + "." + rel
		if _, dup := msgRel[rel]; dup {
			return nil, fmt.Errorf("duplicate symbol %q", full)
		}
		if kind, dup := symbols[full]; dup {
			return nil, fmt.Errorf("duplicate symbol %q (%s)", full, kind)
		}
		idx := int32(len(f.Messages))
		msgRel[rel] = idx
		symbols[full] = "message"
		f.Messages = append(f.Messages, messageDesc{Name: m.Name, FullName: full, Parent: parent})
	}

	// 2. enum 与 message 共享符号命名空间。
	for _, e := range s.Enums {
		if e.Name == "" {
			return nil, fmt.Errorf("enum name is empty")
		}
		if len(e.Values) == 0 {
			return nil, fmt.Errorf("enum %q: no values", e.Name)
		}
		parent := int32(-1)
		rel := e.Name
		if e.Parent != "" {
			pi, ok := msgRel[e.Parent]
			if !ok {
				return nil, fmt.Errorf("enum %q: unknown parent %q", e.Name, e.Parent)
			}
			parent = pi
			rel = e.Parent + "." + e.Name
		}
		full := s.Package + "." + rel
		if _, dup := symbols[full]; dup {
			return nil, fmt.Errorf("duplicate symbol %q", full)
		}
		symbols[full] = "enum"
		start := int32(len(f.Values))
		seen := make(map[string]bool, len(e.Values))
		for _, v := range e.Values {
			if v.Name == "" {
				return nil, fmt.Errorf("enum %q: value name is empty", e.Name)
			}
			if seen[v.Name] {
				return nil, fmt.Errorf("duplicate enum value name %q in %q", v.Name, e.Name)
			}
			seen[v.Name] = true
			f.Values = append(f.Values, enumValueDesc{Name: v.Name, Number: v.Number})
		}
		f.Enums = append(f.Enums, enumDesc{
			Name: e.Name, FullName: full, Parent: parent,
			ValueStart: start, ValueEnd: int32(len(f.Values)),
		})
	}

	// 3. 字段：编号/JSON 名/kind/oneof/类型引用。
	for mi, m := range s.Messages {
		f.Messages[mi].FieldStart = int32(len(f.Fields))
		nums := make(map[int32]bool, len(m.Fields))
		jsonNames := make(map[string]bool, len(m.Fields))
		names := make(map[string]bool, len(m.Fields))
		for _, fd := range m.Fields {
			if fd.Name == "" {
				return nil, fmt.Errorf("message %q: field name is empty", m.Name)
			}
			if names[fd.Name] {
				return nil, fmt.Errorf("duplicate field name %q in %q", fd.Name, m.Name)
			}
			names[fd.Name] = true
			if fd.JSONName == "" {
				return nil, fmt.Errorf("field %s.%s: json name is empty", m.Name, fd.Name)
			}
			if jsonNames[fd.JSONName] {
				return nil, fmt.Errorf("duplicate json name %q in %s", fd.JSONName, m.Name)
			}
			jsonNames[fd.JSONName] = true
			if fd.Number <= 0 {
				return nil, fmt.Errorf("field %s.%s: number must be > 0", m.Name, fd.Name)
			}
			if nums[fd.Number] {
				return nil, fmt.Errorf("duplicate field number %d in %s", fd.Number, m.Name)
			}
			nums[fd.Number] = true
			if !validKinds[fd.Kind] {
				return nil, fmt.Errorf("unknown field kind %q", fd.Kind)
			}
			if fd.Oneof < -1 || fd.Oneof > 7 {
				return nil, fmt.Errorf("field %s.%s: oneof index %d out of range", m.Name, fd.Name, fd.Oneof)
			}
			if fd.Kind == "message" || fd.Kind == "enum" {
				kind, ok := symbols[fd.TypeName]
				if !ok {
					return nil, fmt.Errorf("field %s.%s: type %q not found", m.Name, fd.Name, fd.TypeName)
				}
				if kind != fd.Kind {
					return nil, fmt.Errorf("field %s.%s: type %q is not a %s", m.Name, fd.Name, fd.TypeName, fd.Kind)
				}
			}
			f.Fields = append(f.Fields, fieldDesc{
				Name: fd.Name, JSONName: fd.JSONName, Kind: fd.Kind, TypeName: fd.TypeName,
				Number: fd.Number, Repeated: fd.Repeated, Oneof: fd.Oneof, Parent: int32(mi),
			})
		}
		f.Messages[mi].FieldEnd = int32(len(f.Fields))
	}

	// 4. 一次性提交索引表。
	f.msgByName = make(map[string]int32, len(f.Messages))
	for i, m := range f.Messages {
		f.msgByName[m.FullName] = int32(i)
	}
	f.enumByName = make(map[string]int32, len(f.Enums))
	for i, e := range f.Enums {
		f.enumByName[e.FullName] = int32(i)
	}
	f.fieldByNum = make([]map[int32]int32, len(f.Messages))
	for i, m := range f.Messages {
		m2 := make(map[int32]int32, m.FieldEnd-m.FieldStart)
		for j := m.FieldStart; j < m.FieldEnd; j++ {
			m2[f.Fields[j].Number] = j
		}
		f.fieldByNum[i] = m2
	}
	f.allBySymbol = symbols
	return f, nil
}

func (f *fileDesc) lookupMessage(full string) (int32, bool) {
	i, ok := f.msgByName[full]
	return i, ok
}

func (f *fileDesc) lookupEnum(full string) (int32, bool) {
	i, ok := f.enumByName[full]
	return i, ok
}

func (f *fileDesc) lookupField(msgIdx int32, number int32) (fieldDesc, bool) {
	if msgIdx < 0 || int(msgIdx) >= len(f.fieldByNum) {
		return fieldDesc{}, false
	}
	fi, ok := f.fieldByNum[msgIdx][number]
	if !ok {
		return fieldDesc{}, false
	}
	return f.Fields[fi], true
}

func (f *fileDesc) enumsOfMessage(msgIdx int32) []int32 {
	var out []int32
	for i, e := range f.Enums {
		if e.Parent == msgIdx {
			out = append(out, int32(i))
		}
	}
	return out
}

func (f *fileDesc) path(i int32) string {
	if i < 0 || int(i) >= len(f.Messages) {
		return ""
	}
	m := f.Messages[i]
	if m.Parent < 0 {
		return f.Package + "." + m.Name
	}
	return f.path(m.Parent) + "." + m.Name
}

func (f *fileDesc) render() string {
	var b strings.Builder
	fmt.Fprintf(&b, "file %s pkg=%s messages=%d fields=%d enums=%d values=%d\n",
		f.Path, f.Package, len(f.Messages), len(f.Fields), len(f.Enums), len(f.Values))
	for i, m := range f.Messages {
		fmt.Fprintf(&b, "m[%d] %s parent=%d fields=[%d,%d)\n",
			i, m.FullName, m.Parent, m.FieldStart, m.FieldEnd)
		for j := m.FieldStart; j < m.FieldEnd; j++ {
			fd := f.Fields[j]
			fmt.Fprintf(&b, "  f[%d] %s #%d kind=%s json=%s rep=%v oneof=%d type=%q\n",
				j, fd.Name, fd.Number, fd.Kind, fd.JSONName, fd.Repeated, fd.Oneof, fd.TypeName)
		}
	}
	for i, e := range f.Enums {
		fmt.Fprintf(&b, "e[%d] %s parent=%d values=[%d,%d)\n", i, e.FullName, e.Parent, e.ValueStart, e.ValueEnd)
		for j := e.ValueStart; j < e.ValueEnd; j++ {
			fmt.Fprintf(&b, "  v[%d] %s=%d\n", j, f.Values[j].Name, f.Values[j].Number)
		}
	}
	return b.String()
}

var (
	fileOnce sync.Once
	filePtr  *fileDesc
)

func fileDescriptor() *fileDesc {
	fileOnce.Do(func() {
		d, err := buildFile(rawFile)
		if err != nil {
			panic(err)
		}
		filePtr = d
	})
	return filePtr
}

func cloneSpec(s fileSpec) fileSpec {
	out := fileSpec{Path: s.Path, Package: s.Package}
	out.Messages = make([]msgSpec, len(s.Messages))
	for i, m := range s.Messages {
		out.Messages[i] = msgSpec{Name: m.Name, Parent: m.Parent}
		out.Messages[i].Fields = append([]fieldSpec{}, m.Fields...)
	}
	out.Enums = make([]enumSpec, len(s.Enums))
	for i, e := range s.Enums {
		out.Enums[i] = enumSpec{Name: e.Name, Parent: e.Parent}
		out.Enums[i].Values = append([]enumValueSpec{}, e.Values...)
	}
	return out
}

func expectErr(s fileSpec, substr string) {
	_, err := buildFile(s)
	ck(err != nil && strings.Contains(err.Error(), substr), "invalid descriptor rejected: "+substr)
}

func main() {
	curSeed = 1
	if s := os.Getenv("UAF_SEED"); s != "" {
		if v, err := strconv.ParseInt(s, 10, 64); err == nil {
			curSeed = v
		}
	}

	// 1. 合法表构建：计数、偏移、parent 链、路径。
	f, err := buildFile(rawFile)
	ck(err == nil, "valid descriptor builds")
	if err != nil {
		finish("l10_descriptor_table")
		return
	}
	ck(len(f.Messages) == 4 && len(f.Fields) == 14 && len(f.Enums) == 2 && len(f.Values) == 5,
		"descriptor table counts")
	ck(f.Messages[0].FieldStart == 0 && f.Messages[0].FieldEnd == 5 &&
		f.Messages[1].FieldStart == 5 && f.Messages[1].FieldEnd == 8 &&
		f.Messages[2].FieldStart == 8 && f.Messages[2].FieldEnd == 10 &&
		f.Messages[3].FieldStart == 10 && f.Messages[3].FieldEnd == 14,
		"field offset table")
	ck(f.Enums[0].ValueStart == 0 && f.Enums[0].ValueEnd == 3 &&
		f.Enums[1].ValueStart == 3 && f.Enums[1].ValueEnd == 5, "enum value offset table")
	ck(f.Messages[1].Parent == 0 && f.Messages[2].Parent == 1 && f.Messages[0].Parent == -1 &&
		f.Messages[3].Parent == -1, "parent indices")
	ck(f.Enums[0].Parent == -1 && f.Enums[1].Parent == 0, "enum parent indices")
	for i := range f.Messages {
		ck(f.path(int32(i)) == f.Messages[i].FullName, "path reconstruction")
		if f.Messages[i].Parent >= 0 {
			ck(f.Messages[i].Parent < int32(i), "parent declared earlier")
		}
	}
	ck(f.path(2) == "gen.v1.Root.Child.Leaf", "nested path")
	ck(len(f.enumsOfMessage(0)) == 1 && f.Enums[f.enumsOfMessage(0)[0]].Name == "State",
		"nested enum of message")
	ck(len(f.enumsOfMessage(1)) == 0 && len(f.enumsOfMessage(2)) == 0, "messages without nested enums")

	// 2. 按 full name / 字段号查找。
	if i, ok := f.lookupMessage("gen.v1.Root.Child"); !ok || i != 1 {
		ck(false, "lookup nested message")
	}
	if i, ok := f.lookupMessage("gen.v1.Root.Child.Leaf"); !ok || i != 2 {
		ck(false, "lookup leaf message")
	}
	if _, ok := f.lookupMessage("gen.v1.Nope"); ok {
		ck(false, "unknown message lookup")
	}
	if i, ok := f.lookupEnum("gen.v1.Root.State"); !ok || i != 1 {
		ck(false, "lookup nested enum")
	}
	if _, ok := f.lookupEnum("gen.v1.Root"); ok {
		ck(false, "message name not in enum lookup")
	}
	if fd, ok := f.lookupField(0, 4); !ok || fd.Name != "child" || fd.Kind != "message" ||
		fd.TypeName != "gen.v1.Root.Child" || fd.Parent != 0 {
		ck(false, "lookup message field by number")
	}
	if fd, ok := f.lookupField(1, 2); !ok || fd.Name != "leaf" || fd.TypeName != "gen.v1.Root.Child.Leaf" {
		ck(false, "lookup nested field by number")
	}
	if fd, ok := f.lookupField(3, 3); !ok || fd.Name != "text" || fd.Oneof != 0 {
		ck(false, "lookup oneof field")
	}
	if _, ok := f.lookupField(0, 999); ok {
		ck(false, "unknown field number rejected")
	}
	if _, ok := f.lookupField(-1, 1); ok {
		ck(false, "negative message index rejected")
	}
	ck(f.Fields[4].Repeated && f.Fields[4].Name == "tags" && f.Fields[9].Kind == "bytes",
		"repeated/bytes flags")

	// 3. 确定性渲染与 init 单例。
	rd := f.render()
	ck(strings.HasPrefix(rd, "file gen/v1.proto pkg=gen.v1 messages=4 fields=14 enums=2 values=5\n"),
		"render header")
	ck(strings.Contains(rd, "m[2] gen.v1.Root.Child.Leaf parent=1 fields=[8,10)\n"),
		"render nested message")
	ck(strings.Contains(rd, "  f[10] root #1 kind=message json=root rep=false oneof=-1 type=\"gen.v1.Root\"\n"),
		"render field line")
	ck(strings.Contains(rd, "e[1] gen.v1.Root.State parent=0 values=[3,5)\n"), "render nested enum")
	ck(f.render() == rd, "render deterministic")
	p1 := fileDescriptor()
	p2 := fileDescriptor()
	ck(p1 == p2 && p1 != nil, "descriptor singleton returns same pointer")
	ck(p1.render() == rd, "singleton render same")

	// 4. 构建后修改原始 spec 不影响已建表（生成代码按值拷贝）。
	spec := cloneSpec(rawFile)
	f2, err := buildFile(spec)
	ck(err == nil, "cloned spec builds")
	spec.Path = "mut.proto"
	spec.Messages[0].Name = "Mut"
	spec.Messages[0].Fields[0].Name = "mut"
	spec.Enums[0].Values[0].Name = "MUT"
	ck(f2.Path == "gen/v1.proto" && f2.Messages[0].Name == "Root" &&
		f2.Fields[0].Name == "id" && f2.Values[0].Name == "KIND_UNKNOWN",
		"descriptor owns copy of spec")

	// 5. 非法描述符：校验先于提交。
	expectErr(func() fileSpec { s := cloneSpec(rawFile); s.Path = ""; return s }(), "path is empty")
	expectErr(func() fileSpec { s := cloneSpec(rawFile); s.Package = ""; return s }(), "package is empty")
	expectErr(func() fileSpec { s := cloneSpec(rawFile); s.Messages[2].Name = ""; return s }(), "message name is empty")
	expectErr(func() fileSpec {
		s := cloneSpec(rawFile)
		s.Messages = append(s.Messages, msgSpec{Name: "Root"})
		return s
	}(), "duplicate symbol")
	expectErr(func() fileSpec {
		s := cloneSpec(rawFile)
		s.Messages = append(s.Messages, msgSpec{Name: "Orphan", Parent: "Nope"})
		return s
	}(), "unknown parent")
	expectErr(func() fileSpec {
		s := cloneSpec(rawFile)
		s.Enums = append(s.Enums, enumSpec{Name: "Root", Values: []enumValueSpec{{Name: "X", Number: 0}}})
		return s
	}(), "duplicate symbol")
	expectErr(func() fileSpec { s := cloneSpec(rawFile); s.Messages[0].Fields[0].Number = 0; return s }(), "number must be > 0")
	expectErr(func() fileSpec { s := cloneSpec(rawFile); s.Messages[0].Fields[1].Number = 1; return s }(), "duplicate field number")
	expectErr(func() fileSpec { s := cloneSpec(rawFile); s.Messages[0].Fields[1].JSONName = "id"; return s }(), "duplicate json name")
	expectErr(func() fileSpec { s := cloneSpec(rawFile); s.Messages[0].Fields[0].Kind = "float128"; return s }(), "unknown field kind")
	expectErr(func() fileSpec { s := cloneSpec(rawFile); s.Messages[0].Fields[3].TypeName = "gen.v1.Missing"; return s }(), "not found")
	expectErr(func() fileSpec { s := cloneSpec(rawFile); s.Messages[0].Fields[3].TypeName = "gen.v1.Kind"; return s }(), "is not a message")
	expectErr(func() fileSpec { s := cloneSpec(rawFile); s.Enums[1].Parent = "Nope"; return s }(), "unknown parent")
	expectErr(func() fileSpec { s := cloneSpec(rawFile); s.Enums[0].Values[1].Name = "KIND_UNKNOWN"; return s }(), "duplicate enum value name")
	expectErr(func() fileSpec {
		s := cloneSpec(rawFile)
		s.Enums = append(s.Enums, enumSpec{Name: "Empty"})
		return s
	}(), "no values")
	expectErr(func() fileSpec {
		s := cloneSpec(rawFile)
		s.Enums = append(s.Enums, enumSpec{Name: "State", Parent: "Root", Values: []enumValueSpec{{Name: "X", Number: 0}}})
		return s
	}(), "duplicate symbol")
	expectErr(func() fileSpec { s := cloneSpec(rawFile); s.Messages[0].Fields[0].Oneof = 9; return s }(), "oneof index")
	expectErr(func() fileSpec { s := cloneSpec(rawFile); s.Messages[0].Fields[0].Name = ""; return s }(), "field name is empty")
	expectErr(func() fileSpec { s := cloneSpec(rawFile); s.Enums[0].Values[0].Name = ""; return s }(), "value name is empty")

	// 6. GC：保留渲染串、full name、字段描述副本与单例指针。
	want := f.render()
	names := []string{f.Messages[2].FullName, f.Enums[1].FullName, f.Fields[13].Name}
	fdCopy, _ := f.lookupField(3, 4)
	gcSink = append(gcSink, []byte(want), []byte(names[0]))
	gcStress()
	ck(fileDescriptor() == p1, "singleton pointer stable across GC")
	ck(f.render() == want, "render stable across GC")
	ck(names[0] == "gen.v1.Root.Child.Leaf" && names[1] == "gen.v1.Root.State" && names[2] == "num",
		"retained names survive GC")
	ck(fdCopy.Name == "num" && fdCopy.Number == 4 && fdCopy.Oneof == 0, "field desc copy survives GC")
	if idx, ok := f.lookupMessage(names[0]); !ok || f.Messages[idx].FullName != names[0] {
		ck(false, "lookup after GC")
	}

	// 7. UAF_SEED 随机查找：消息、字段号、枚举、路径。
	r := rand.New(rand.NewSource(curSeed))
	for i := 0; i < 700; i++ {
		mi := r.Intn(len(f.Messages))
		msg := f.Messages[mi]
		if idx, ok := f.lookupMessage(msg.FullName); !ok || idx != int32(mi) {
			ckSeed(false, fmt.Sprintf("round %d message lookup %s", i, msg.FullName))
		}
		if f.path(int32(mi)) != msg.FullName {
			ckSeed(false, fmt.Sprintf("round %d path %d", i, mi))
		}
		count := int(msg.FieldEnd - msg.FieldStart)
		if count > 0 {
			fi := msg.FieldStart + int32(r.Intn(count))
			fd := f.Fields[fi]
			got, ok := f.lookupField(int32(mi), fd.Number)
			ckSeed(ok && got.Name == fd.Name && got.Kind == fd.Kind && got.Parent == int32(mi),
				fmt.Sprintf("round %d field lookup %s#%d", i, msg.Name, fd.Number))
		}
		if _, ok := f.lookupField(int32(mi), 1000+int32(r.Intn(100))); ok {
			ckSeed(false, fmt.Sprintf("round %d phantom field found", i))
		}
		ei := r.Intn(len(f.Enums))
		e := f.Enums[ei]
		if idx, ok := f.lookupEnum(e.FullName); !ok || idx != int32(ei) {
			ckSeed(false, fmt.Sprintf("round %d enum lookup %s", i, e.FullName))
		}
		if r.Intn(5) == 0 {
			gcStress()
			ckSeed(f.render() == want, fmt.Sprintf("round %d render after GC", i))
		}
	}

	finish("l10_descriptor_table")
}
