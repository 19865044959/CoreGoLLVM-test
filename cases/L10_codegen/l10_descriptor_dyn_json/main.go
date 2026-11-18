// l10_descriptor_dyn_json: a descriptor built by reflection from generated
// struct tags drives a dynamic JSON codec (optional presence, repeated, maps,
// enums by name, nested messages, unknown-field policy) for arbitrary values.
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"math/rand"
	"os"
	"reflect"
	"runtime"
	"sort"
	"strconv"
	"strings"
)

var (
	failed int
	seed   = int64(1)
	gcSink [][]byte
)

func ck(ok bool, msg string) {
	if !ok {
		failed++
		fmt.Println("FAIL:", msg)
	}
}

func ckSeed(ok bool, msg string) {
	if !ok {
		failed++
		fmt.Printf("FAIL: %s (UAF_SEED=%d)\n", msg, seed)
	}
}

func gcStress() {
	for i := 0; i < 16; i++ {
		b := make([]byte, 4096)
		b[0] = byte(i)
		b[len(b)-1] = byte(i * 9)
		gcSink = append(gcSink, b)
	}
	for i := range gcSink {
		gcSink[i] = nil
	}
	gcSink = gcSink[:0]
	runtime.GC()
	runtime.GC()
}

func finish(name string) {
	if failed != 0 {
		fmt.Printf("FAILED %s (%d failed checks)\n", name, failed)
		os.Exit(1)
	}
	fmt.Println("ALL PASS " + name)
}

func readSeed() int64 {
	s := os.Getenv("UAF_SEED")
	if s == "" {
		return 1
	}
	v, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 1
	}
	return v
}

func envInt(name string, def int) int {
	s := os.Getenv(name)
	if s == "" {
		return def
	}
	v, err := strconv.Atoi(s)
	if err != nil || v < 0 {
		return def
	}
	return v
}

// ---------- generated-style types ----------

type mode int32

const (
	modeOff   mode = 0
	modeFast  mode = 1
	modeSafe  mode = 2
	modeAlias mode = 2 // alias of MODE_SAFE
)

type enumInfo struct {
	canonical map[int32]string
	byName    map[string]int32
}

func newModeEnum() *enumInfo {
	decl := []struct {
		v int32
		n string
	}{
		{int32(modeOff), "MODE_OFF"},
		{int32(modeFast), "MODE_FAST"},
		{int32(modeSafe), "MODE_SAFE"},
		{int32(modeAlias), "MODE_QUICK"},
	}
	e := &enumInfo{canonical: map[int32]string{}, byName: map[string]int32{}}
	for _, d := range decl {
		if _, ok := e.canonical[d.v]; !ok {
			e.canonical[d.v] = d.n
		}
		e.byName[d.n] = d.v
	}
	return e
}

type dynChild struct {
	Note *string  `pb:"1,note,opt"`
	Data []string `pb:"2,data,rep"`
}

type dynMsg struct {
	Name   *string           `pb:"1,name,opt"`
	Count  *int64            `pb:"2,count,opt"`
	Ratio  *float64          `pb:"3,ratio,opt"`
	Active *bool             `pb:"4,active,opt"`
	Tags   []string          `pb:"5,tags,rep"`
	Attrs  map[string]string `pb:"6,attrs,map"`
	Mode   *mode             `pb:"7,mode,opt,enum=mode"`
	Child  *dynChild         `pb:"8,child,msg"`
}

// ---------- descriptor ----------

type fieldKind int

const (
	kString fieldKind = iota
	kInt64
	kFloat64
	kBool
	kEnum
	kStrings
	kMapSS
	kMessage
)

type fieldDesc struct {
	num      int32
	jsonName string
	index    []int
	kind     fieldKind
	enum     *enumInfo
	child    *msgDesc
}

type msgDesc struct {
	name   string
	typ    reflect.Type
	fields []*fieldDesc
	byName map[string]*fieldDesc
}

func parsePBField(f reflect.StructField, enums map[string]*enumInfo) (*fieldDesc, error) {
	if f.PkgPath != "" {
		return nil, fmt.Errorf("field %s: unexported", f.Name)
	}
	tag := f.Tag.Get("pb")
	if tag == "" {
		return nil, fmt.Errorf("field %s: missing pb tag", f.Name)
	}
	parts := strings.Split(tag, ",")
	if len(parts) < 3 || len(parts) > 4 {
		return nil, fmt.Errorf("field %s: malformed pb tag %q", f.Name, tag)
	}
	num64, err := strconv.ParseInt(parts[0], 10, 32)
	if err != nil || num64 <= 0 {
		return nil, fmt.Errorf("field %s: bad field number %q", f.Name, parts[0])
	}
	if parts[1] == "" {
		return nil, fmt.Errorf("field %s: empty json name", f.Name)
	}
	card := parts[2]
	var enumTag string
	if len(parts) == 4 {
		if !strings.HasPrefix(parts[3], "enum=") {
			return nil, fmt.Errorf("field %s: unknown option %q", f.Name, parts[3])
		}
		enumTag = strings.TrimPrefix(parts[3], "enum=")
		if enumTag == "" {
			return nil, fmt.Errorf("field %s: empty enum name", f.Name)
		}
	}
	fd := &fieldDesc{num: int32(num64), jsonName: parts[1], index: f.Index}
	t := f.Type
	switch card {
	case "opt":
		if t.Kind() != reflect.Ptr {
			return nil, fmt.Errorf("field %s: opt requires pointer", f.Name)
		}
		switch t.Elem().Kind() {
		case reflect.String:
			fd.kind = kString
		case reflect.Int64:
			fd.kind = kInt64
		case reflect.Float64:
			fd.kind = kFloat64
		case reflect.Bool:
			fd.kind = kBool
		case reflect.Int32:
			if enumTag == "" {
				return nil, fmt.Errorf("field %s: int32 requires enum option", f.Name)
			}
			e, ok := enums[enumTag]
			if !ok {
				return nil, fmt.Errorf("field %s: unknown enum %q", f.Name, enumTag)
			}
			fd.kind = kEnum
			fd.enum = e
		default:
			return nil, fmt.Errorf("field %s: unsupported opt elem %s", f.Name, t.Elem())
		}
		if enumTag != "" && fd.kind != kEnum {
			return nil, fmt.Errorf("field %s: enum option on non-enum", f.Name)
		}
	case "rep":
		if t.Kind() != reflect.Slice || t.Elem().Kind() != reflect.String {
			return nil, fmt.Errorf("field %s: rep requires []string", f.Name)
		}
		if enumTag != "" {
			return nil, fmt.Errorf("field %s: enum option on repeated", f.Name)
		}
		fd.kind = kStrings
	case "map":
		if t.Kind() != reflect.Map || t.Key().Kind() != reflect.String || t.Elem().Kind() != reflect.String {
			return nil, fmt.Errorf("field %s: map requires map[string]string", f.Name)
		}
		if enumTag != "" {
			return nil, fmt.Errorf("field %s: enum option on map", f.Name)
		}
		fd.kind = kMapSS
	case "msg":
		if t.Kind() != reflect.Ptr || t.Elem().Kind() != reflect.Struct {
			return nil, fmt.Errorf("field %s: msg requires pointer to struct", f.Name)
		}
		if enumTag != "" {
			return nil, fmt.Errorf("field %s: enum option on message", f.Name)
		}
		fd.kind = kMessage
	default:
		return nil, fmt.Errorf("field %s: unknown cardinality %q", f.Name, card)
	}
	return fd, nil
}

func buildMessage(t reflect.Type, enums map[string]*enumInfo, reg map[reflect.Type]*msgDesc, building map[reflect.Type]bool) (*msgDesc, error) {
	if t.Kind() != reflect.Struct {
		return nil, fmt.Errorf("%s is not a struct", t)
	}
	if d, ok := reg[t]; ok {
		return d, nil
	}
	if building[t] {
		return nil, fmt.Errorf("recursive message type %s", t)
	}
	building[t] = true
	defer delete(building, t)
	d := &msgDesc{name: t.Name(), typ: t, byName: map[string]*fieldDesc{}}
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		fd, err := parsePBField(f, enums)
		if err != nil {
			return nil, err
		}
		if fd.kind == kMessage {
			child, err := buildMessage(f.Type.Elem(), enums, reg, building)
			if err != nil {
				return nil, err
			}
			fd.child = child
		}
		if old, ok := d.byName[fd.jsonName]; ok {
			return nil, fmt.Errorf("duplicate json name %q (fields %d and %d)", fd.jsonName, old.num, fd.num)
		}
		for _, prev := range d.fields {
			if prev.num == fd.num {
				return nil, fmt.Errorf("duplicate field number %d", fd.num)
			}
		}
		d.fields = append(d.fields, fd)
		d.byName[fd.jsonName] = fd
	}
	sort.Slice(d.fields, func(i, j int) bool { return d.fields[i].num < d.fields[j].num })
	reg[t] = d
	return d, nil
}

// ---------- dynamic encode ----------

func jsonString(s string) []byte {
	b, _ := json.Marshal(s)
	return b
}

type encoder struct {
	buf    bytes.Buffer
	first  bool
	failed error
}

func (e *encoder) field(name string, raw []byte) {
	if !e.first {
		e.buf.WriteByte(',')
	}
	e.first = false
	e.buf.Write(jsonString(name))
	e.buf.WriteByte(':')
	e.buf.Write(raw)
}

func (e *encoder) encodeMessage(d *msgDesc, v reflect.Value) {
	e.buf.WriteByte('{')
	e.first = true
	for _, fd := range d.fields {
		fv := v.FieldByIndex(fd.index)
		switch fd.kind {
		case kString:
			if !fv.IsNil() {
				e.field(fd.jsonName, jsonString(fv.Elem().String()))
			}
		case kInt64:
			if !fv.IsNil() {
				e.field(fd.jsonName, []byte(strconv.FormatInt(fv.Elem().Int(), 10)))
			}
		case kFloat64:
			if !fv.IsNil() {
				f := fv.Elem().Float()
				if math.IsNaN(f) || math.IsInf(f, 0) {
					e.failed = fmt.Errorf("field %s: non-finite float", fd.jsonName)
					return
				}
				e.field(fd.jsonName, []byte(strconv.FormatFloat(f, 'g', -1, 64)))
			}
		case kBool:
			if !fv.IsNil() {
				if fv.Elem().Bool() {
					e.field(fd.jsonName, []byte("true"))
				} else {
					e.field(fd.jsonName, []byte("false"))
				}
			}
		case kEnum:
			if !fv.IsNil() {
				e.field(fd.jsonName, encodeEnum(fd.enum, int32(fv.Elem().Int())))
			}
		case kStrings:
			if !fv.IsNil() {
				var b bytes.Buffer
				b.WriteByte('[')
				for i := 0; i < fv.Len(); i++ {
					if i > 0 {
						b.WriteByte(',')
					}
					b.Write(jsonString(fv.Index(i).String()))
				}
				b.WriteByte(']')
				e.field(fd.jsonName, b.Bytes())
			}
		case kMapSS:
			if !fv.IsNil() {
				var b bytes.Buffer
				b.WriteByte('{')
				keys := fv.MapKeys()
				sort.Slice(keys, func(i, j int) bool { return keys[i].String() < keys[j].String() })
				for i, k := range keys {
					if i > 0 {
						b.WriteByte(',')
					}
					b.Write(jsonString(k.String()))
					b.WriteByte(':')
					b.Write(jsonString(fv.MapIndex(k).String()))
				}
				b.WriteByte('}')
				e.field(fd.jsonName, b.Bytes())
			}
		case kMessage:
			if !fv.IsNil() {
				sub := &encoder{}
				sub.encodeMessage(fd.child, fv.Elem())
				if sub.failed != nil {
					e.failed = sub.failed
					return
				}
				e.field(fd.jsonName, sub.buf.Bytes())
			}
		}
		if e.failed != nil {
			return
		}
	}
	e.buf.WriteByte('}')
}

func encodeEnum(e *enumInfo, v int32) []byte {
	if s, ok := e.canonical[v]; ok {
		return jsonString(s)
	}
	return []byte(strconv.FormatInt(int64(v), 10))
}

func encodeDynamic(d *msgDesc, v any) ([]byte, error) {
	rv := reflect.ValueOf(v)
	if !rv.IsValid() || rv.Kind() != reflect.Ptr || rv.IsNil() || rv.Elem().Type() != d.typ {
		return nil, fmt.Errorf("encode: value does not match descriptor %s", d.name)
	}
	e := &encoder{}
	e.encodeMessage(d, rv.Elem())
	if e.failed != nil {
		return nil, e.failed
	}
	return e.buf.Bytes(), nil
}

// ---------- dynamic decode ----------

func parseObject(raw []byte) (map[string]json.RawMessage, error) {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil {
		return nil, err
	}
	if obj == nil {
		return nil, fmt.Errorf("expected JSON object")
	}
	return obj, nil
}

func decodeEnum(e *enumInfo, raw []byte) (int32, error) {
	s := strings.TrimSpace(string(raw))
	if s == "" || s == "null" {
		return 0, fmt.Errorf("enum: null")
	}
	if s[0] == '"' {
		var name string
		if err := json.Unmarshal(raw, &name); err != nil {
			return 0, err
		}
		v, ok := e.byName[name]
		if !ok {
			return 0, fmt.Errorf("enum: unknown name %q", name)
		}
		return v, nil
	}
	var n int64
	if err := json.Unmarshal(raw, &n); err != nil {
		return 0, err
	}
	if n < math.MinInt32 || n > math.MaxInt32 {
		return 0, fmt.Errorf("enum: out of int32 range")
	}
	return int32(n), nil
}

func fillMessage(d *msgDesc, raw []byte, v reflect.Value, strict bool) error {
	obj, err := parseObject(raw)
	if err != nil {
		return err
	}
	keys := make([]string, 0, len(obj))
	for k := range obj {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		fd := d.byName[k]
		if fd == nil {
			if strict {
				return fmt.Errorf("%s: unknown field %q", d.name, k)
			}
			continue
		}
		if err := fillField(fd, obj[k], v.FieldByIndex(fd.index), strict); err != nil {
			return fmt.Errorf("%s.%s: %w", d.name, k, err)
		}
	}
	return nil
}

func fillField(fd *fieldDesc, raw []byte, fv reflect.Value, strict bool) error {
	if string(raw) == "null" {
		fv.Set(reflect.Zero(fv.Type()))
		return nil
	}
	switch fd.kind {
	case kString:
		var s string
		if err := json.Unmarshal(raw, &s); err != nil {
			return err
		}
		p := reflect.New(fv.Type().Elem())
		p.Elem().SetString(s)
		fv.Set(p)
	case kInt64:
		var n int64
		if err := json.Unmarshal(raw, &n); err != nil {
			return err
		}
		p := reflect.New(fv.Type().Elem())
		p.Elem().SetInt(n)
		fv.Set(p)
	case kFloat64:
		var f float64
		if err := json.Unmarshal(raw, &f); err != nil {
			return err
		}
		p := reflect.New(fv.Type().Elem())
		p.Elem().SetFloat(f)
		fv.Set(p)
	case kBool:
		var b bool
		if err := json.Unmarshal(raw, &b); err != nil {
			return err
		}
		p := reflect.New(fv.Type().Elem())
		p.Elem().SetBool(b)
		fv.Set(p)
	case kEnum:
		n, err := decodeEnum(fd.enum, raw)
		if err != nil {
			return err
		}
		p := reflect.New(fv.Type().Elem())
		p.Elem().SetInt(int64(n))
		fv.Set(p)
	case kStrings:
		var elems []json.RawMessage
		if err := json.Unmarshal(raw, &elems); err != nil {
			return err
		}
		out := make([]string, len(elems))
		for i, el := range elems {
			if string(el) == "null" {
				return fmt.Errorf("element %d is null", i)
			}
			if err := json.Unmarshal(el, &out[i]); err != nil {
				return fmt.Errorf("element %d: %w", i, err)
			}
		}
		fv.Set(reflect.ValueOf(out))
	case kMapSS:
		obj, err := parseObject(raw)
		if err != nil {
			return err
		}
		out := make(map[string]string, len(obj))
		for k, val := range obj {
			if string(val) == "null" {
				return fmt.Errorf("value %q is null", k)
			}
			var s string
			if err := json.Unmarshal(val, &s); err != nil {
				return fmt.Errorf("value %q: %w", k, err)
			}
			out[k] = s
		}
		fv.Set(reflect.ValueOf(out))
	case kMessage:
		p := reflect.New(fv.Type().Elem())
		if err := fillMessage(fd.child, raw, p.Elem(), strict); err != nil {
			return err
		}
		fv.Set(p)
	default:
		return fmt.Errorf("unsupported kind")
	}
	return nil
}

func decodeDynamic(d *msgDesc, raw []byte, strict bool) (any, error) {
	pv := reflect.New(d.typ)
	if err := fillMessage(d, raw, pv.Elem(), strict); err != nil {
		return nil, err
	}
	return pv.Interface(), nil
}

// ---------- main ----------

func main() {
	seed = readSeed()

	enums := map[string]*enumInfo{"mode": newModeEnum()}
	reg := map[reflect.Type]*msgDesc{}
	childDesc, err := buildMessage(reflect.TypeOf(dynChild{}), enums, reg, map[reflect.Type]bool{})
	ck(err == nil, "build child descriptor")
	if err != nil {
		finish("l10_descriptor_dyn_json")
		return
	}
	mainDesc, err := buildMessage(reflect.TypeOf(dynMsg{}), enums, reg, map[reflect.Type]bool{})
	ck(err == nil, fmt.Sprintf("build message descriptor: %v", err))
	if err != nil {
		finish("l10_descriptor_dyn_json")
		return
	}
	ck(len(mainDesc.fields) == 8 && len(childDesc.fields) == 2, "descriptor field counts")
	ck(mainDesc.fields[0].num == 1 && mainDesc.fields[7].num == 8, "fields sorted by number")
	ck(mainDesc.byName["attrs"].kind == kMapSS && mainDesc.byName["mode"].kind == kEnum && mainDesc.byName["child"].child == childDesc, "descriptor kinds")

	// 1. Golden deterministic encode.
	name := "alice"
	count := int64(0)
	ratio := 1.25
	active := true
	md := modeAlias
	note := "n"
	m := &dynMsg{
		Name: &name, Count: &count, Ratio: &ratio, Active: &active,
		Tags: []string{"b", "a"}, Attrs: map[string]string{"z": "1", "a": "2"},
		Mode: &md, Child: &dynChild{Note: &note, Data: []string{}},
	}
	enc, err := encodeDynamic(mainDesc, m)
	ck(err == nil, "golden encode succeeds")
	golden := `{"name":"alice","count":0,"ratio":1.25,"active":true,"tags":["b","a"],"attrs":{"a":"2","z":"1"},"mode":"MODE_SAFE","child":{"note":"n","data":[]}}`
	ck(string(enc) == golden, "golden dynamic JSON")

	// 2. Encode -> GC -> decode -> encode is byte-stable.
	gcStress()
	decAny, err := decodeDynamic(mainDesc, enc, true)
	ck(err == nil, "golden decode")
	if err == nil {
		dec := decAny.(*dynMsg)
		ck(dec.Count != nil && *dec.Count == 0, "explicit zero presence survives codec")
		ck(dec.Mode != nil && *dec.Mode == modeSafe, "alias decodes to canonical numeric value")
		ck(dec.Child != nil && dec.Child.Data != nil && len(dec.Child.Data) == 0, "empty child slice non-nil")
		enc2, err := encodeDynamic(mainDesc, dec)
		ck(err == nil && bytes.Equal(enc, enc2), "encode/decode byte stable")
		// Poison the encoded buffer and GC; decoded strings must be independent.
		for i := range enc {
			enc[i] = 0xEE
		}
		gcStress()
		enc3, err := encodeDynamic(mainDesc, dec)
		ck(err == nil && bytes.Equal(enc2, enc3), "decoded payload independent of input buffer")
	}

	// 3. Absent vs explicit zero vs null vs empty containers.
	absentAny, err := decodeDynamic(mainDesc, []byte(`{}`), true)
	ck(err == nil, "empty object decodes")
	if err == nil {
		a := absentAny.(*dynMsg)
		ck(a.Name == nil && a.Count == nil && a.Tags == nil && a.Attrs == nil && a.Child == nil, "absent fields remain nil")
		enc, _ := encodeDynamic(mainDesc, a)
		ck(string(enc) == `{}`, "absent fields omitted")
	}
	zeroAny, err := decodeDynamic(mainDesc, []byte(`{"name":"","count":0,"ratio":0,"active":false,"mode":0}`), true)
	ck(err == nil, "explicit zero decodes")
	if err == nil {
		z := zeroAny.(*dynMsg)
		ck(z.Name != nil && *z.Name == "" && z.Count != nil && *z.Count == 0 && z.Active != nil && !*z.Active && z.Mode != nil && *z.Mode == modeOff, "explicit zero pointers allocated")
		enc, _ := encodeDynamic(mainDesc, z)
		ck(string(enc) == `{"name":"","count":0,"ratio":0,"active":false,"mode":"MODE_OFF"}`, "explicit zero re-encoded")
	}
	nullAny, err := decodeDynamic(mainDesc, []byte(`{"name":null,"count":null,"tags":null,"attrs":null,"child":null}`), true)
	ck(err == nil, "null resets fields")
	if err == nil {
		n := nullAny.(*dynMsg)
		ck(n.Name == nil && n.Count == nil && n.Tags == nil && n.Attrs == nil && n.Child == nil, "null means absent")
	}
	emptyAny, err := decodeDynamic(mainDesc, []byte(`{"tags":[],"attrs":{},"child":{"data":[]}}`), true)
	ck(err == nil, "empty containers decode")
	if err == nil {
		e := emptyAny.(*dynMsg)
		ck(e.Tags != nil && len(e.Tags) == 0 && e.Attrs != nil && len(e.Attrs) == 0, "empty containers non-nil")
		ck(e.Child != nil && e.Child.Data != nil && len(e.Child.Data) == 0, "nested empty container non-nil")
	}

	// 4. Unknown-field policy: strict vs lenient, including nested objects.
	_, err = decodeDynamic(mainDesc, []byte(`{"unknown":1}`), true)
	ck(err != nil && strings.Contains(err.Error(), `unknown field "unknown"`), "strict unknown rejected")
	lenient, err := decodeDynamic(mainDesc, []byte(`{"unknown":1,"child":{"x":2}}`), false)
	ck(err == nil, "lenient unknown decodes")
	if err == nil {
		enc, _ := encodeDynamic(mainDesc, lenient)
		ck(string(enc) == `{"child":{}}`, "lenient unknown dropped")
	}
	_, err = decodeDynamic(mainDesc, []byte(`{"child":{"x":2}}`), true)
	ck(err != nil && strings.Contains(err.Error(), `unknown field "x"`), "strict nested unknown rejected")

	// 5. Decode error paths.
	bad := []string{
		`{"count":"x"}`,
		`{"count":999999999999999999999999}`,
		`{"tags":[1]}`,
		`{"tags":[null]}`,
		`{"attrs":{"k":null}}`,
		`{"mode":"NOPE"}`,
		`{"mode":2147483648}`,
		`{"child":[]}`,
		`{"active":"yes"}`,
		`{"name":1}`,
		`{"ratio":true}`,
		`{"tags":{}}`,
		`{"attrs":[]}`,
		`[]`,
		`null`,
		`{"name":"x"} trailing`,
	}
	for i, s := range bad {
		_, err := decodeDynamic(mainDesc, []byte(s), true)
		ck(err != nil, fmt.Sprintf("bad decode %d rejected", i))
	}

	// 6. Encode errors: non-finite floats.
	nan := math.NaN()
	_, err = encodeDynamic(mainDesc, &dynMsg{Ratio: &nan})
	ck(err != nil, "NaN encode rejected")
	inf := math.Inf(1)
	_, err = encodeDynamic(mainDesc, &dynMsg{Ratio: &inf})
	ck(err != nil, "Inf encode rejected")

	// 7. Descriptor build errors.
	type badDup struct {
		A *string `pb:"1,a,opt"`
		B *string `pb:"1,b,opt"`
	}
	type badJSONName struct {
		A *string `pb:"1,x,opt"`
		B *string `pb:"2,x,opt"`
	}
	type badNumber struct {
		A *string `pb:"x,a,opt"`
	}
	type badMissing struct {
		A *string
	}
	type badCard struct {
		A *string `pb:"1,a,rep"`
	}
	type badEnumRef struct {
		A *int32 `pb:"1,a,opt,enum=nope"`
	}
	type badInt32 struct {
		A *int32 `pb:"1,a,opt"`
	}
	type badSlice struct {
		A []int32 `pb:"1,a,rep"`
	}
	type badMap struct {
		A map[int32]string `pb:"1,a,map"`
	}
	type badUnexported struct {
		a *string `pb:"1,a,opt"`
	}
	type badMsg struct {
		A *int `pb:"1,a,msg"`
	}
	type badEnumOnString struct {
		A *string `pb:"1,a,opt,enum=mode"`
	}
	type badOption struct {
		A *string `pb:"1,a,opt,nope=1"`
	}
	type badRecursive struct {
		A *badRecursive `pb:"1,a,msg"`
	}
	buildErrs := []struct {
		name string
		typ  reflect.Type
	}{
		{"duplicate number", reflect.TypeOf(badDup{})},
		{"duplicate json", reflect.TypeOf(badJSONName{})},
		{"bad number", reflect.TypeOf(badNumber{})},
		{"missing tag", reflect.TypeOf(badMissing{})},
		{"wrong cardinality", reflect.TypeOf(badCard{})},
		{"unknown enum", reflect.TypeOf(badEnumRef{})},
		{"int32 without enum", reflect.TypeOf(badInt32{})},
		{"unsupported slice", reflect.TypeOf(badSlice{})},
		{"unsupported map", reflect.TypeOf(badMap{})},
		{"unexported", reflect.TypeOf(badUnexported{})},
		{"bad msg", reflect.TypeOf(badMsg{})},
		{"enum on string", reflect.TypeOf(badEnumOnString{})},
		{"unknown option", reflect.TypeOf(badOption{})},
		{"recursive", reflect.TypeOf(badRecursive{})},
	}
	for _, be := range buildErrs {
		_, err := buildMessage(be.typ, enums, map[reflect.Type]*msgDesc{}, map[reflect.Type]bool{})
		ck(err != nil, "descriptor reject: "+be.name)
	}

	// 8. Seeded torture: random messages -> dynamic encode -> decode -> compare.
	rounds := envInt("TORTURE_ROUNDS", 250)
	r := rand.New(rand.NewSource(seed))
	okRounds := 0
	for i := 0; i < rounds; i++ {
		msg := &dynMsg{}
		if r.Intn(2) == 0 {
			s := fmt.Sprintf("name%d", r.Intn(1000))
			msg.Name = &s
		}
		if r.Intn(2) == 0 {
			v := r.Int63() - (1 << 40)
			msg.Count = &v
		}
		if r.Intn(2) == 0 {
			v := float64(r.Intn(20000)) / 8
			msg.Ratio = &v
		}
		if r.Intn(2) == 0 {
			v := r.Intn(2) == 0
			msg.Active = &v
		}
		if r.Intn(2) == 0 {
			msg.Tags = make([]string, r.Intn(4))
			for j := range msg.Tags {
				msg.Tags[j] = fmt.Sprintf("t%d_%d", i%7, r.Intn(100))
			}
		}
		if r.Intn(2) == 0 {
			msg.Attrs = map[string]string{}
			for j := 0; j < r.Intn(4); j++ {
				msg.Attrs[fmt.Sprintf("k%d", r.Intn(10))] = fmt.Sprintf("v%d", r.Intn(100))
			}
		}
		if r.Intn(2) == 0 {
			v := mode(r.Intn(5))
			msg.Mode = &v
		}
		if r.Intn(2) == 0 {
			c := &dynChild{}
			if r.Intn(2) == 0 {
				s := fmt.Sprintf("note%d", r.Intn(100))
				c.Note = &s
			}
			if r.Intn(2) == 0 {
				c.Data = make([]string, r.Intn(3))
				for j := range c.Data {
					c.Data[j] = fmt.Sprintf("d%d", r.Intn(50))
				}
			}
			msg.Child = c
		}
		enc, err := encodeDynamic(mainDesc, msg)
		if err != nil {
			ckSeed(false, fmt.Sprintf("torture %d encode: %v", i, err))
			continue
		}
		decAny, err := decodeDynamic(mainDesc, enc, true)
		if err != nil {
			ckSeed(false, fmt.Sprintf("torture %d decode: %v", i, err))
			continue
		}
		enc2, err := encodeDynamic(mainDesc, decAny)
		if err != nil || !bytes.Equal(enc, enc2) {
			ckSeed(false, fmt.Sprintf("torture %d unstable", i))
			continue
		}
		for j := range enc {
			enc[j] = 0x7F
		}
		if i%29 == 0 {
			gcStress()
		}
		enc3, err := encodeDynamic(mainDesc, decAny)
		if err != nil || !bytes.Equal(enc2, enc3) {
			ckSeed(false, fmt.Sprintf("torture %d input alias", i))
			continue
		}
		okRounds++
	}
	ck(okRounds == rounds, "torture: all dynamic rounds stable")

	// 9. Descriptor retention across GC.
	gcStress()
	again, err := decodeDynamic(mainDesc, []byte(`{"name":"post-gc","mode":2,"child":{"data":["x"]}}`), true)
	ck(err == nil, "decode after GC")
	if err == nil {
		enc, _ := encodeDynamic(mainDesc, again)
		ck(string(enc) == `{"name":"post-gc","mode":"MODE_SAFE","child":{"data":["x"]}}`, "descriptor survives GC")
	}

	finish("l10_descriptor_dyn_json")
}
