// L10: 由 reflect 构建生成风格 descriptor 表：struct tag 解析、字段号/JSON 名、
// 按字段号动态读写、presence/oneof/容器、构建校验、注册表与 GC 后读写。
package main

import (
	"errors"
	"fmt"
	"math/rand"
	"os"
	"reflect"
	"runtime"
	"sort"
	"strconv"
	"strings"
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

// ---- 被生成的类型 ----

type profile struct {
	Bio string `proto:"1,string"`
}

type unionText struct {
	V string
}

type unionCount struct {
	V int32
}

type user struct {
	ID      int64            `proto:"1,int64"`
	Name    string           `proto:"2,string"`
	Active  bool             `proto:"3,bool"`
	Score   *float64         `proto:"4,double,opt"`
	Tags    []string         `proto:"5,repeated,string"`
	Labels  map[string]int32 `proto:"6,map,string,int32"`
	Profile *profile         `proto:"7,message,main.profile"`
	Union   any              `proto:"8,oneof,union"`
	Ignored string           `proto:"-"`
	hidden  string
}

// ---- descriptor ----

type fieldDesc struct {
	Number    int32
	Name      string
	JSONName  string
	GoKind    reflect.Kind
	Type      reflect.Type
	Index     int
	Cardinal  string // scalar|optional|repeated|map|message|oneof
	MapKey    reflect.Kind
	MapElem   reflect.Kind
	Msg       *messageDesc
	OneofName string
}

type messageDesc struct {
	Name     string
	Type     reflect.Type
	Fields   []*fieldDesc
	ByNumber map[int32]*fieldDesc
	ByName   map[string]*fieldDesc
	ByJSON   map[string]*fieldDesc
}

type descRegistry struct {
	m      map[string]*messageDesc
	frozen bool
}

func newDescRegistry() *descRegistry {
	return &descRegistry{m: make(map[string]*messageDesc)}
}

func (r *descRegistry) Register(d *messageDesc) error {
	if r.frozen {
		return errors.New("descriptor registry frozen")
	}
	if d == nil || d.Name == "" {
		return errors.New("nil descriptor or empty name")
	}
	if _, ok := r.m[d.Name]; ok {
		return fmt.Errorf("duplicate message descriptor %q", d.Name)
	}
	r.m[d.Name] = d
	return nil
}

func (r *descRegistry) Get(name string) (*messageDesc, bool) {
	d, ok := r.m[name]
	return d, ok
}

func (r *descRegistry) Freeze() { r.frozen = true }

func jsonNameFromGo(name string) string { return strings.ToLower(name) }

func parseUint32(s string) (int32, error) {
	n, err := strconv.ParseInt(s, 10, 32)
	if err != nil || n <= 0 || n > 0x7fffffff {
		return 0, fmt.Errorf("bad field number %q", s)
	}
	return int32(n), nil
}

func scalarKind(name string) (reflect.Kind, bool) {
	switch name {
	case "int32":
		return reflect.Int32, true
	case "int64":
		return reflect.Int64, true
	case "string":
		return reflect.String, true
	case "bool":
		return reflect.Bool, true
	case "float":
		return reflect.Float32, true
	case "double":
		return reflect.Float64, true
	}
	return reflect.Invalid, false
}

func buildMessage(t reflect.Type, reg *descRegistry) (*messageDesc, error) {
	if t == nil || t.Kind() != reflect.Struct {
		return nil, errors.New("descriptor target must be struct")
	}
	d := &messageDesc{
		Name:     t.String(),
		Type:     t,
		ByNumber: make(map[int32]*fieldDesc),
		ByName:   make(map[string]*fieldDesc),
		ByJSON:   make(map[string]*fieldDesc),
	}
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if f.PkgPath != "" {
			continue // 未导出字段（生成消息的 state 字段）跳过
		}
		tag := f.Tag.Get("proto")
		if tag == "" {
			return nil, fmt.Errorf("field %s missing proto tag", f.Name)
		}
		if tag == "-" {
			continue
		}
		parts := strings.Split(tag, ",")
		num, err := parseUint32(parts[0])
		if err != nil {
			return nil, err
		}
		if len(parts) < 2 {
			return nil, fmt.Errorf("field %s: missing kind", f.Name)
		}
		fd := &fieldDesc{
			Number:   num,
			Name:     f.Name,
			JSONName: jsonNameFromGo(f.Name),
			Type:     f.Type,
			Index:    i,
			GoKind:   f.Type.Kind(),
		}
		switch parts[1] {
		case "repeated":
			if f.Type.Kind() != reflect.Slice || len(parts) != 3 {
				return nil, fmt.Errorf("field %s: bad repeated tag", f.Name)
			}
			k, ok := scalarKind(parts[2])
			if !ok || f.Type.Elem().Kind() != k {
				return nil, fmt.Errorf("field %s: repeated kind mismatch", f.Name)
			}
			fd.Cardinal = "repeated"
			fd.GoKind = k
		case "map":
			if f.Type.Kind() != reflect.Map || len(parts) != 4 {
				return nil, fmt.Errorf("field %s: bad map tag", f.Name)
			}
			kk, ok1 := scalarKind(parts[2])
			vk, ok2 := scalarKind(parts[3])
			if !ok1 || !ok2 || f.Type.Key().Kind() != kk || f.Type.Elem().Kind() != vk {
				return nil, fmt.Errorf("field %s: map kind mismatch", f.Name)
			}
			fd.Cardinal = "map"
			fd.MapKey, fd.MapElem = kk, vk
		case "message":
			if f.Type.Kind() != reflect.Ptr || f.Type.Elem().Kind() != reflect.Struct || len(parts) != 3 {
				return nil, fmt.Errorf("field %s: bad message tag", f.Name)
			}
			md, ok := reg.Get(parts[2])
			if !ok || md.Type != f.Type.Elem() {
				return nil, fmt.Errorf("field %s: unknown message %q", f.Name, parts[2])
			}
			fd.Cardinal = "message"
			fd.Msg = md
		case "oneof":
			if f.Type.Kind() != reflect.Interface || len(parts) != 3 {
				return nil, fmt.Errorf("field %s: oneof must be interface", f.Name)
			}
			fd.Cardinal = "oneof"
			fd.OneofName = parts[2]
		default:
			k, ok := scalarKind(parts[1])
			if !ok {
				return nil, fmt.Errorf("field %s: unknown kind %q", f.Name, parts[1])
			}
			if len(parts) == 3 && parts[2] == "opt" {
				if f.Type.Kind() != reflect.Ptr || f.Type.Elem().Kind() != k {
					return nil, fmt.Errorf("field %s: optional pointer mismatch", f.Name)
				}
				fd.Cardinal = "optional"
				fd.GoKind = k
			} else if len(parts) == 2 {
				if f.Type.Kind() != k {
					return nil, fmt.Errorf("field %s: scalar kind mismatch", f.Name)
				}
				fd.Cardinal = "scalar"
			} else {
				return nil, fmt.Errorf("field %s: bad scalar tag", f.Name)
			}
		}
		if _, ok := d.ByNumber[fd.Number]; ok {
			return nil, fmt.Errorf("duplicate field number %d", fd.Number)
		}
		d.ByNumber[fd.Number] = fd
		d.ByName[fd.Name] = fd
		d.ByJSON[fd.JSONName] = fd
		d.Fields = append(d.Fields, fd)
	}
	sort.Slice(d.Fields, func(i, j int) bool { return d.Fields[i].Number < d.Fields[j].Number })
	return d, nil
}

// ---- 动态读写 ----

func formatScalar(v reflect.Value, kind reflect.Kind) string {
	switch kind {
	case reflect.Int32, reflect.Int64:
		return strconv.FormatInt(v.Int(), 10)
	case reflect.Uint32, reflect.Uint64:
		return strconv.FormatUint(v.Uint(), 10)
	case reflect.Bool:
		return strconv.FormatBool(v.Bool())
	case reflect.Float32, reflect.Float64:
		return strconv.FormatFloat(v.Float(), 'g', -1, 64)
	case reflect.String:
		return strconv.Quote(v.String())
	}
	return "<unsupported>"
}

func parseScalar(raw string, kind reflect.Kind, dst reflect.Value) error {
	switch kind {
	case reflect.Int32:
		n, err := strconv.ParseInt(raw, 10, 32)
		if err != nil {
			return err
		}
		dst.SetInt(n)
	case reflect.Int64:
		n, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			return err
		}
		dst.SetInt(n)
	case reflect.Bool:
		b, err := strconv.ParseBool(raw)
		if err != nil {
			return err
		}
		dst.SetBool(b)
	case reflect.Float32, reflect.Float64:
		f, err := strconv.ParseFloat(raw, 64)
		if err != nil {
			return err
		}
		dst.SetFloat(f)
	case reflect.String:
		dst.SetString(raw)
	default:
		return errors.New("unsupported scalar")
	}
	return nil
}

func unionParts(v any) (kind string, formatted string, present bool) {
	if v == nil {
		return "", "", false
	}
	switch u := v.(type) {
	case *unionText:
		if u == nil {
			return "", "", false
		}
		return "text", strconv.Quote(u.V), true
	case *unionCount:
		if u == nil {
			return "", "", false
		}
		return "count", strconv.FormatInt(int64(u.V), 10), true
	}
	rv := reflect.ValueOf(v)
	if rv.Kind() == reflect.Ptr && rv.IsNil() {
		return "", "", false
	}
	return "", "", false
}

func getField(v reflect.Value, fd *fieldDesc) (string, bool, error) {
	fv := v.Field(fd.Index)
	switch fd.Cardinal {
	case "scalar":
		return formatScalar(fv, fd.GoKind), true, nil
	case "optional":
		if fv.IsNil() {
			return "", false, nil
		}
		return formatScalar(fv.Elem(), fd.GoKind), true, nil
	case "repeated":
		if fv.IsNil() {
			return "", false, nil
		}
		parts := make([]string, fv.Len())
		for i := 0; i < fv.Len(); i++ {
			parts[i] = formatScalar(fv.Index(i), fd.GoKind)
		}
		return strings.Join(parts, "|"), true, nil
	case "map":
		if fv.IsNil() {
			return "", false, nil
		}
		keys := fv.MapKeys()
		sort.Slice(keys, func(i, j int) bool {
			return formatScalar(keys[i], fd.MapKey) < formatScalar(keys[j], fd.MapKey)
		})
		parts := make([]string, 0, len(keys))
		for _, k := range keys {
			parts = append(parts, strings.Trim(formatScalar(k, fd.MapKey), `"`)+"="+formatScalar(fv.MapIndex(k), fd.MapElem))
		}
		return strings.Join(parts, ","), true, nil
	case "message":
		if fv.IsNil() {
			return "", false, nil
		}
		var parts []string
		for _, sub := range fd.Msg.Fields {
			s, ok, err := getField(fv.Elem(), sub)
			if err != nil {
				return "", false, err
			}
			if ok {
				parts = append(parts, sub.JSONName+"="+s)
			}
		}
		return strings.Join(parts, ";"), true, nil
	case "oneof":
		kind, formatted, ok := unionParts(fv.Interface())
		if !ok {
			return "", false, nil
		}
		return kind + "=" + formatted, true, nil
	}
	return "", false, errors.New("unknown cardinality")
}

func getByNumber(m any, d *messageDesc, num int32) (string, bool, error) {
	fd, ok := d.ByNumber[num]
	if !ok {
		return "", false, fmt.Errorf("unknown field number %d", num)
	}
	rv := reflect.ValueOf(m)
	if rv.Kind() != reflect.Ptr || rv.IsNil() || rv.Elem().Type() != d.Type {
		return "", false, errors.New("message type mismatch")
	}
	return getField(rv.Elem(), fd)
}

func setByNumber(m any, d *messageDesc, num int32, raw string) error {
	fd, ok := d.ByNumber[num]
	if !ok {
		return fmt.Errorf("unknown field number %d", num)
	}
	rv := reflect.ValueOf(m)
	if rv.Kind() != reflect.Ptr || rv.IsNil() || rv.Elem().Type() != d.Type {
		return errors.New("message type mismatch")
	}
	fv := rv.Elem().Field(fd.Index)
	switch fd.Cardinal {
	case "scalar":
		return parseScalar(raw, fd.GoKind, fv)
	case "optional":
		p := reflect.New(fv.Type().Elem())
		if err := parseScalar(raw, fd.GoKind, p.Elem()); err != nil {
			return err
		}
		fv.Set(p)
		return nil
	case "repeated":
		var elems []string
		if raw != "" {
			elems = strings.Split(raw, "|")
		}
		slice := reflect.MakeSlice(fv.Type(), len(elems), len(elems))
		for i, s := range elems {
			if err := parseScalar(s, fd.GoKind, slice.Index(i)); err != nil {
				return err
			}
		}
		fv.Set(slice)
		return nil
	case "map":
		mv := reflect.MakeMap(fv.Type())
		if raw != "" {
			for _, pair := range strings.Split(raw, ",") {
				kv := strings.SplitN(pair, "=", 2)
				if len(kv) != 2 {
					return fmt.Errorf("bad map pair %q", pair)
				}
				k := reflect.New(fv.Type().Key()).Elem()
				if err := parseScalar(kv[0], fd.MapKey, k); err != nil {
					return err
				}
				val := reflect.New(fv.Type().Elem()).Elem()
				if err := parseScalar(kv[1], fd.MapElem, val); err != nil {
					return err
				}
				mv.SetMapIndex(k, val)
			}
		}
		fv.Set(mv)
		return nil
	case "message":
		return errors.New("message field set not supported")
	case "oneof":
		kindVal := strings.SplitN(raw, "=", 2)
		if len(kindVal) != 2 {
			return fmt.Errorf("bad oneof %q", raw)
		}
		switch kindVal[0] {
		case "text":
			fv.Set(reflect.ValueOf(&unionText{V: kindVal[1]}))
		case "count":
			n, err := strconv.ParseInt(kindVal[1], 10, 32)
			if err != nil {
				return err
			}
			fv.Set(reflect.ValueOf(&unionCount{V: int32(n)}))
		default:
			return fmt.Errorf("unknown oneof branch %q", kindVal[0])
		}
		return nil
	}
	return errors.New("unknown cardinality")
}

// ---- 测试 ----

func buildRegistry() (*descRegistry, *messageDesc, *messageDesc, error) {
	reg := newDescRegistry()
	pd, err := buildMessage(reflect.TypeOf(profile{}), reg)
	if err != nil {
		return nil, nil, nil, err
	}
	if err := reg.Register(pd); err != nil {
		return nil, nil, nil, err
	}
	ud, err := buildMessage(reflect.TypeOf(user{}), reg)
	if err != nil {
		return nil, nil, nil, err
	}
	if err := reg.Register(ud); err != nil {
		return nil, nil, nil, err
	}
	return reg, pd, ud, nil
}

func expectBuildError(name string, t reflect.Type, reg *descRegistry) {
	_, err := buildMessage(t, reg)
	ck(err != nil, "expected build error: "+name)
}

type badDup struct {
	A string `proto:"1,string"`
	B string `proto:"1,string"`
}

type badKind struct {
	A string `proto:"1,bogus"`
}

type badTag struct {
	A string `proto:"x,string"`
}

type badNoTag struct {
	A string
}

type badMap struct {
	A map[struct{}]string `proto:"1,map,string,string"`
}

type badOneof struct {
	A string `proto:"1,oneof,u"`
}

type unknownMsg struct{}

type badMsg struct {
	A *unknownMsg `proto:"1,message,unknownMsg"`
}

func main() {
	seed := int64(1)
	if v := os.Getenv("UAF_SEED"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			seed = n
		}
	}
	curSeed = seed

	reg, pd, ud, err := buildRegistry()
	ck(err == nil, "build registry")
	ck(pd != nil && pd.Name == "main.profile" && len(pd.Fields) == 1, "profile descriptor")
	ck(ud != nil && len(ud.Fields) == 8, "user descriptor field count")
	ck(ud != nil && ud.Fields[0].Number == 1 && ud.Fields[7].Number == 8, "fields sorted by number")
	if fd, ok := ud.ByNumber[4]; !ok || fd.Cardinal != "optional" || fd.JSONName != "score" {
		ck(false, "optional field descriptor")
	}
	if fd, ok := ud.ByJSON["tags"]; !ok || fd.Cardinal != "repeated" || fd.GoKind != reflect.String {
		ck(false, "repeated field descriptor by json name")
	}
	if fd, ok := ud.ByName["Labels"]; !ok || fd.MapKey != reflect.String || fd.MapElem != reflect.Int32 {
		ck(false, "map field descriptor")
	}
	if fd, ok := ud.ByNumber[8]; !ok || fd.Cardinal != "oneof" || fd.OneofName != "union" {
		ck(false, "oneof field descriptor")
	}
	if _, ok := ud.ByName["Ignored"]; ok {
		ck(false, "ignored field excluded")
	}
	if _, ok := ud.ByName["hidden"]; ok {
		ck(false, "unexported field excluded")
	}
	if _, ok := reg.Get("main.user"); !ok {
		ck(false, "registry lookup")
	}
	ck(reg.Register(pd) != nil, "duplicate descriptor rejected")
	reg.Freeze()
	late, _ := buildMessage(reflect.TypeOf(profile{}), reg)
	ck(reg.Register(late) != nil, "frozen registry rejects register")

	// 构建校验。
	expectBuildError("duplicate number", reflect.TypeOf(badDup{}), reg)
	expectBuildError("unknown kind", reflect.TypeOf(badKind{}), reg)
	expectBuildError("bad number", reflect.TypeOf(badTag{}), reg)
	expectBuildError("missing tag", reflect.TypeOf(badNoTag{}), reg)
	expectBuildError("unsupported map key", reflect.TypeOf(badMap{}), reg)
	expectBuildError("oneof must be interface", reflect.TypeOf(badOneof{}), reg)
	expectBuildError("unknown message", reflect.TypeOf(badMsg{}), reg)

	// 动态读写与 presence。
	u := &user{}
	if _, present, err := getByNumber(u, ud, 4); err != nil || present {
		ck(false, "optional absent initially")
	}
	ck(setByNumber(u, ud, 1, "-42") == nil, "set int64")
	ck(setByNumber(u, ud, 2, `he said "hi"`) == nil, "set string")
	ck(setByNumber(u, ud, 3, "true") == nil, "set bool")
	ck(setByNumber(u, ud, 4, "1.5") == nil, "set optional double")
	ck(setByNumber(u, ud, 5, `a|b|`) == nil, "set repeated string")
	ck(setByNumber(u, ud, 6, "z=9,a=1") == nil, "set map")
	ck(setByNumber(u, ud, 8, "text=hello") == nil, "set oneof text")
	ck(u.ID == -42 && u.Name == `he said "hi"` && u.Active && u.Score != nil && *u.Score == 1.5, "reflect writes visible")
	ck(len(u.Tags) == 3 && u.Tags[2] == "" && u.Labels["a"] == 1 && u.Labels["z"] == 9, "container writes visible")
	if s, ok, err := getByNumber(u, ud, 1); err != nil || !ok || s != "-42" {
		ck(false, "get int64")
	}
	if s, ok, err := getByNumber(u, ud, 2); err != nil || !ok || s != `"he said \"hi\""` {
		ck(false, "get quoted string")
	}
	if s, ok, err := getByNumber(u, ud, 3); err != nil || !ok || s != "true" {
		ck(false, "get bool")
	}
	if s, ok, err := getByNumber(u, ud, 4); err != nil || !ok || s != "1.5" {
		ck(false, "get optional present")
	}
	if s, ok, err := getByNumber(u, ud, 5); err != nil || !ok || s != `"a"|"b"|""` {
		ck(false, "get repeated formatted")
	}
	if s, ok, err := getByNumber(u, ud, 6); err != nil || !ok || s != "a=1,z=9" {
		ck(false, "get map sorted")
	}
	if s, ok, err := getByNumber(u, ud, 8); err != nil || !ok || s != `text="hello"` {
		ck(false, "get oneof text")
	}
	ck(setByNumber(u, ud, 8, "count=7") == nil, "set oneof count")
	if s, ok, err := getByNumber(u, ud, 8); err != nil || !ok || s != "count=7" {
		ck(false, "get oneof count")
	}
	u.Union = (*unionCount)(nil)
	if _, ok, err := getByNumber(u, ud, 8); err != nil || ok {
		ck(false, "typed-nil oneof absent")
	}
	if err := setByNumber(u, ud, 8, "bogus=x"); err == nil {
		ck(false, "unknown oneof branch rejected")
	}
	if err := setByNumber(u, ud, 9, "x"); err == nil {
		ck(false, "unknown field number rejected")
	}
	if _, _, err := getByNumber(u, ud, 9); err == nil {
		ck(false, "get unknown number rejected")
	}
	if err := setByNumber(&profile{}, ud, 1, "1"); err == nil {
		ck(false, "wrong message type rejected")
	}

	// 空容器与 nil 区分。
	u2 := &user{}
	ck(setByNumber(u2, ud, 5, "") == nil && u2.Tags != nil && len(u2.Tags) == 0, "empty repeated is non-nil")
	ck(setByNumber(u2, ud, 6, "") == nil && u2.Labels != nil && len(u2.Labels) == 0, "empty map is non-nil")
	if s, ok, err := getByNumber(u2, ud, 5); err != nil || !ok || s != "" {
		ck(false, "empty repeated present")
	}
	if s, ok, err := getByNumber(u2, ud, 6); err != nil || !ok || s != "" {
		ck(false, "empty map present")
	}
	u2.Score = nil
	if _, ok, _ := getByNumber(u2, ud, 4); ok {
		ck(false, "nil optional absent")
	}
	u2.Profile = &profile{Bio: "bio"}
	if s, ok, err := getByNumber(u2, ud, 7); err != nil || !ok || s != `bio="bio"` {
		ck(false, "nested message reflection")
	}
	if err := setByNumber(u2, ud, 7, "x"); err == nil {
		ck(false, "message set unsupported")
	}

	// 描述符不持有消息值；消息保留后跨 GC 可读写。
	keptUser := &user{ID: 7, Name: "keep", Tags: []string{"x"}}
	u = nil
	gcStress()
	if s, ok, err := getByNumber(keptUser, ud, 1); err != nil || !ok || s != "7" {
		ck(false, "descriptor read after GC")
	}
	ck(setByNumber(keptUser, ud, 2, "after-gc") == nil && keptUser.Name == "after-gc", "write after GC")
	keptDesc := ud
	ud = nil
	gcStress()
	if len(keptDesc.Fields) != 8 || keptDesc.ByNumber[5].Cardinal != "repeated" {
		ck(false, "descriptor table survives GC")
	}

	// 随机动态读写与模型对账。
	r := rand.New(rand.NewSource(seed))
	var mm userModel
	rm := &user{}
	for i := 0; i < 300; i++ {
		switch r.Intn(7) {
		case 0:
			v := int64(r.Intn(1<<40) - 1<<39)
			if err := setByNumber(rm, keptDesc, 1, strconv.FormatInt(v, 10)); err != nil {
				ckSeed(false, "random set id")
			}
			mm.id = v
		case 1:
			s := "n" + strconv.Itoa(r.Intn(1000))
			if err := setByNumber(rm, keptDesc, 2, s); err != nil {
				ckSeed(false, "random set name")
			}
			mm.name = s
		case 2:
			b := r.Intn(2) == 1
			if err := setByNumber(rm, keptDesc, 3, strconv.FormatBool(b)); err != nil {
				ckSeed(false, "random set bool")
			}
			mm.active = b
		case 3:
			f := float64(r.Intn(10000)) / 100
			if err := setByNumber(rm, keptDesc, 4, strconv.FormatFloat(f, 'g', -1, 64)); err != nil {
				ckSeed(false, "random set score")
			}
			mm.score = &f
		case 4:
			var elems []string
			for j := 0; j < r.Intn(4); j++ {
				elems = append(elems, "t"+strconv.Itoa(r.Intn(100)))
			}
			raw := strings.Join(elems, "|")
			if err := setByNumber(rm, keptDesc, 5, raw); err != nil {
				ckSeed(false, "random set tags")
			}
			mm.tags = elems
		case 5:
			var pairs []string
			m := make(map[string]int32)
			for j := 0; j < r.Intn(4); j++ {
				k := "k" + strconv.Itoa(r.Intn(20))
				v := int32(r.Intn(200) - 100)
				pairs = append(pairs, k+"="+strconv.Itoa(int(v)))
				m[k] = v
			}
			if err := setByNumber(rm, keptDesc, 6, strings.Join(pairs, ",")); err != nil {
				ckSeed(false, "random set labels")
			}
			mm.labels = m
		case 6:
			if r.Intn(2) == 0 {
				s := "u" + strconv.Itoa(r.Intn(100))
				if err := setByNumber(rm, keptDesc, 8, "text="+s); err != nil {
					ckSeed(false, "random set oneof text")
				}
				mm.union = "text=" + strconv.Quote(s)
			} else {
				n := int32(r.Intn(200) - 100)
				if err := setByNumber(rm, keptDesc, 8, "count="+strconv.Itoa(int(n))); err != nil {
					ckSeed(false, "random set oneof count")
				}
				mm.union = "count=" + strconv.Itoa(int(n))
			}
		}
		if i%50 == 49 {
			gcStress()
			checkModel(rm, keptDesc, &mm)
		}
	}
	checkModel(rm, keptDesc, &mm)

	finish("l10_descriptor_reflect")
}

type userModel struct {
	id     int64
	name   string
	active bool
	score  *float64
	tags   []string
	labels map[string]int32
	union  string
}

func checkModel(u *user, d *messageDesc, mm *userModel) {
	if s, ok, err := getByNumber(u, d, 1); err != nil || !ok || s != strconv.FormatInt(mm.id, 10) {
		ckSeed(false, "model id")
	}
	if s, ok, err := getByNumber(u, d, 2); err != nil || !ok || s != strconv.Quote(mm.name) {
		ckSeed(false, "model name")
	}
	if s, ok, err := getByNumber(u, d, 3); err != nil || !ok || s != strconv.FormatBool(mm.active) {
		ckSeed(false, "model active")
	}
	if mm.score == nil {
		if _, ok, _ := getByNumber(u, d, 4); ok {
			ckSeed(false, "model score absent")
		}
	} else if s, ok, err := getByNumber(u, d, 4); err != nil || !ok || s != strconv.FormatFloat(*mm.score, 'g', -1, 64) {
		ckSeed(false, "model score")
	}
	var tagParts []string
	for _, t := range mm.tags {
		tagParts = append(tagParts, strconv.Quote(t))
	}
	if s, ok, err := getByNumber(u, d, 5); err != nil || !ok || s != strings.Join(tagParts, "|") {
		ckSeed(false, "model tags")
	}
	var keys []string
	for k := range mm.labels {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var labelParts []string
	for _, k := range keys {
		labelParts = append(labelParts, k+"="+strconv.Itoa(int(mm.labels[k])))
	}
	if s, ok, err := getByNumber(u, d, 6); err != nil || !ok || s != strings.Join(labelParts, ",") {
		ckSeed(false, "model labels")
	}
	if s, ok, err := getByNumber(u, d, 8); err != nil || !ok || s != mm.union {
		ckSeed(false, "model oneof")
	}
}
