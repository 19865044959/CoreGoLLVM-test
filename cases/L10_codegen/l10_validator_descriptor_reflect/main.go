// l10_validator_descriptor_reflect: go/parser reads a generated validation
// schema (struct tags), then a reflection-driven validator applies the plan to
// arbitrary values: required/presence, rune length, UTF-8, float finiteness,
// enum/oneof sets, slice/map bounds, nested recursion with depth and cycle cuts.
package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"math"
	"math/rand"
	"os"
	"reflect"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"
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
		b[len(b)-1] = byte(i * 23)
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

// ---------- runtime message types ----------

type Child struct {
	Code int64
	Name string
	Next *Child
}

type Account struct {
	ID      int64
	Name    string
	Balance float64
	Role    int32
	Tags    []string
	Limits  map[string]int64
	Child   *Child
	Note    *string
	Scores  []float64
}

// ---------- schema source ----------

func schemaSource() string {
	lines := []string{
		"package gen",
		"",
		"type Account struct {",
		"\tID      int64            `json:\"id\" validate:\"required,min=1,max=1000000\"`",
		"\tName    string           `json:\"name\" validate:\"required,minlen=2,maxlen=8,utf8\"`",
		"\tBalance float64          `json:\"balance\" validate:\"finite,min=-1000,max=1000\"`",
		"\tRole    int32            `json:\"role\" validate:\"enum=1|2|3\"`",
		"\tTags    []string         `json:\"tags\" validate:\"maxitems=3,eachminlen=1\"`",
		"\tLimits  map[string]int64 `json:\"limits\" validate:\"maxitems=3,keyprefix=lim_,valmin=0,valmax=10\"`",
		"\tChild   *Child           `json:\"child\" validate:\"nested\"`",
		"\tNote    *string          `json:\"note\" validate:\"minlen=1,maxlen=4\"`",
		"\tScores  []float64        `json:\"scores\" validate:\"maxitems=2,eachfinite,eachmax=100\"`",
		"}",
		"",
		"type Child struct {",
		"\tCode int64  `json:\"code\" validate:\"required,min=1\"`",
		"\tName string `json:\"name\" validate:\"oneof=a|b\"`",
		"\tNext *Child `json:\"next\" validate:\"nested\"`",
		"}",
	}
	return strings.Join(lines, "\n") + "\n"
}

// ---------- plan ----------

type fieldKind int

const (
	fkInt64 fieldKind = iota
	fkInt32
	fkFloat64
	fkBool
	fkString
	fkPtrInt64
	fkPtrFloat64
	fkPtrString
	fkStrings
	fkFloats
	fkMapInt64
	fkNested
)

type rule struct {
	op   string
	i1   int64
	i2   int64
	f1   float64
	f2   float64
	list []string
	nums []int64
}

type fieldPlan struct {
	goName   string
	jsonName string
	index    int
	kind     fieldKind
	rules    []rule
	nested   *msgPlan
}

type msgPlan struct {
	name   string
	typ    reflect.Type
	fields []*fieldPlan
	byJSON map[string]*fieldPlan
}

type rawField struct {
	goName    string
	jsonName  string
	kind      fieldKind
	rules     []rule
	nestedTyp string
}

type rawPlan struct {
	name   string
	fields []rawField
}

func isScalar(name string) (fieldKind, bool) {
	switch name {
	case "int64":
		return fkInt64, true
	case "int32":
		return fkInt32, true
	case "float64":
		return fkFloat64, true
	case "bool":
		return fkBool, true
	case "string":
		return fkString, true
	}
	return 0, false
}

func classify(expr ast.Expr) (kind fieldKind, nested string, err error) {
	switch x := expr.(type) {
	case *ast.Ident:
		if k, ok := isScalar(x.Name); ok {
			return k, "", nil
		}
		return 0, "", fmt.Errorf("unsupported type %s", x.Name)
	case *ast.StarExpr:
		id, ok := x.X.(*ast.Ident)
		if !ok {
			return 0, "", fmt.Errorf("unsupported pointer type")
		}
		if k, ok := isScalar(id.Name); ok {
			switch k {
			case fkInt64:
				return fkPtrInt64, "", nil
			case fkFloat64:
				return fkPtrFloat64, "", nil
			case fkString:
				return fkPtrString, "", nil
			default:
				return 0, "", fmt.Errorf("unsupported pointer scalar %s", id.Name)
			}
		}
		return fkNested, id.Name, nil
	case *ast.ArrayType:
		id, ok := x.Elt.(*ast.Ident)
		if !ok {
			return 0, "", fmt.Errorf("unsupported slice element")
		}
		switch id.Name {
		case "string":
			return fkStrings, "", nil
		case "float64":
			return fkFloats, "", nil
		}
		return 0, "", fmt.Errorf("unsupported slice element %s", id.Name)
	case *ast.MapType:
		key, ok1 := x.Key.(*ast.Ident)
		val, ok2 := x.Value.(*ast.Ident)
		if !ok1 || !ok2 || key.Name != "string" || val.Name != "int64" {
			return 0, "", fmt.Errorf("unsupported map type")
		}
		return fkMapInt64, "", nil
	}
	return 0, "", fmt.Errorf("unsupported field type")
}

func opAllowed(kind fieldKind, op string) bool {
	switch kind {
	case fkInt64, fkInt32, fkPtrInt64:
		return op == "required" || op == "min" || op == "max" || op == "enum"
	case fkFloat64, fkPtrFloat64:
		return op == "required" || op == "finite" || op == "min" || op == "max"
	case fkString, fkPtrString:
		return op == "required" || op == "minlen" || op == "maxlen" || op == "utf8" || op == "oneof"
	case fkBool:
		return op == "required"
	case fkStrings:
		return op == "required" || op == "maxitems" || op == "eachminlen"
	case fkFloats:
		return op == "required" || op == "maxitems" || op == "eachfinite" || op == "eachmax"
	case fkMapInt64:
		return op == "required" || op == "maxitems" || op == "keyprefix" || op == "valmin" || op == "valmax"
	case fkNested:
		return op == "required" || op == "nested"
	}
	return false
}

func parseRules(kind fieldKind, raw string) ([]rule, error) {
	var out []rule
	if raw == "" {
		return out, nil
	}
	seen := map[string]bool{}
	for _, part := range strings.Split(raw, ",") {
		if part == "" {
			return nil, fmt.Errorf("empty rule")
		}
		kv := strings.SplitN(part, "=", 2)
		op := kv[0]
		if !opAllowed(kind, op) {
			return nil, fmt.Errorf("rule %q not allowed for kind", op)
		}
		if seen[op] {
			return nil, fmt.Errorf("duplicate rule %q", op)
		}
		seen[op] = true
		r := rule{op: op}
		if op == "required" || op == "finite" || op == "utf8" || op == "nested" || op == "eachfinite" {
			if len(kv) != 1 {
				return nil, fmt.Errorf("rule %q takes no parameter", op)
			}
			out = append(out, r)
			continue
		}
		if len(kv) != 2 || kv[1] == "" {
			return nil, fmt.Errorf("rule %q requires a parameter", op)
		}
		val := kv[1]
		switch op {
		case "min", "max":
			if kind == fkFloat64 || kind == fkPtrFloat64 {
				f, err := strconv.ParseFloat(val, 64)
				if err != nil || math.IsNaN(f) || math.IsInf(f, 0) {
					return nil, fmt.Errorf("bad float parameter %q", val)
				}
				if op == "min" {
					r.f1 = f
				} else {
					r.f2 = f
				}
			} else {
				n, err := strconv.ParseInt(val, 10, 64)
				if err != nil {
					return nil, fmt.Errorf("bad integer parameter %q", val)
				}
				if op == "min" {
					r.i1 = n
				} else {
					r.i2 = n
				}
			}
		case "minlen", "maxlen", "maxitems", "eachminlen", "valmin", "valmax":
			n, err := strconv.ParseInt(val, 10, 64)
			if err != nil {
				return nil, fmt.Errorf("bad integer parameter %q", val)
			}
			if (op == "maxitems" || op == "minlen" || op == "maxlen" || op == "eachminlen") && n < 0 {
				return nil, fmt.Errorf("negative %s", op)
			}
			if op == "minlen" || op == "eachminlen" || op == "valmin" {
				r.i1 = n
			} else {
				r.i2 = n
			}
		case "eachmax":
			f, err := strconv.ParseFloat(val, 64)
			if err != nil || math.IsNaN(f) || math.IsInf(f, 0) {
				return nil, fmt.Errorf("bad float parameter %q", val)
			}
			r.f1 = f
		case "enum":
			for _, s := range strings.Split(val, "|") {
				n, err := strconv.ParseInt(s, 10, 64)
				if err != nil {
					return nil, fmt.Errorf("bad enum value %q", s)
				}
				r.nums = append(r.nums, n)
			}
			if len(r.nums) == 0 {
				return nil, fmt.Errorf("empty enum set")
			}
		case "oneof":
			for _, s := range strings.Split(val, "|") {
				if s == "" {
					return nil, fmt.Errorf("empty oneof value")
				}
				r.list = append(r.list, s)
			}
		case "keyprefix":
			r.list = []string{val}
		default:
			return nil, fmt.Errorf("unknown rule %q", op)
		}
		out = append(out, r)
	}
	// Cross-parameter checks.
	var minInt, maxInt *int64
	var minF, maxF *float64
	for i := range out {
		switch out[i].op {
		case "min":
			if kind == fkFloat64 || kind == fkPtrFloat64 {
				v := out[i].f1
				minF = &v
			} else {
				v := out[i].i1
				minInt = &v
			}
		case "max":
			if kind == fkFloat64 || kind == fkPtrFloat64 {
				v := out[i].f2
				maxF = &v
			} else {
				v := out[i].i2
				maxInt = &v
			}
		}
	}
	if minInt != nil && maxInt != nil && *minInt > *maxInt {
		return nil, fmt.Errorf("min > max")
	}
	if minF != nil && maxF != nil && *minF > *maxF {
		return nil, fmt.Errorf("min > max")
	}
	for i := range out {
		if out[i].op == "minlen" {
			for j := range out {
				if out[j].op == "maxlen" && out[i].i1 > out[j].i2 {
					return nil, fmt.Errorf("minlen > maxlen")
				}
			}
		}
	}
	return out, nil
}

func tagJSON(f *ast.Field) (string, *reflect.StructTag, error) {
	if f.Tag == nil {
		return "", nil, fmt.Errorf("missing struct tag")
	}
	s, err := strconv.Unquote(f.Tag.Value)
	if err != nil {
		return "", nil, fmt.Errorf("bad struct tag: %w", err)
	}
	st := reflect.StructTag(s)
	jsonName := st.Get("json")
	if i := strings.IndexByte(jsonName, ','); i >= 0 {
		jsonName = jsonName[:i]
	}
	if jsonName == "" {
		return "", nil, fmt.Errorf("missing json name")
	}
	return jsonName, &st, nil
}

func parseSchema(src string) (map[string]*rawPlan, error) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "schema.go", src, 0)
	if err != nil {
		return nil, err
	}
	raws := map[string]*rawPlan{}
	for _, decl := range f.Decls {
		gd, ok := decl.(*ast.GenDecl)
		if !ok || gd.Tok != token.TYPE {
			continue
		}
		for _, spec := range gd.Specs {
			ts, ok := spec.(*ast.TypeSpec)
			if !ok {
				continue
			}
			st, ok := ts.Type.(*ast.StructType)
			if !ok {
				continue
			}
			rp := &rawPlan{name: ts.Name.Name}
			seenJSON := map[string]bool{}
			for _, field := range st.Fields.List {
				if len(field.Names) != 1 {
					return nil, fmt.Errorf("%s: embedded or multi-name field", rp.name)
				}
				goName := field.Names[0].Name
				if !ast.IsExported(goName) {
					return nil, fmt.Errorf("%s: unexported field %s", rp.name, goName)
				}
				jsonName, stTag, err := tagJSON(field)
				if err != nil {
					return nil, fmt.Errorf("%s.%s: %w", rp.name, goName, err)
				}
				if seenJSON[jsonName] {
					return nil, fmt.Errorf("%s: duplicate json name %q", rp.name, jsonName)
				}
				seenJSON[jsonName] = true
				kind, nested, err := classify(field.Type)
				if err != nil {
					return nil, fmt.Errorf("%s.%s: %w", rp.name, goName, err)
				}
				rules, err := parseRules(kind, stTag.Get("validate"))
				if err != nil {
					return nil, fmt.Errorf("%s.%s: %w", rp.name, goName, err)
				}
				rp.fields = append(rp.fields, rawField{
					goName: goName, jsonName: jsonName, kind: kind, rules: rules, nestedTyp: nested,
				})
			}
			raws[rp.name] = rp
		}
	}
	if len(raws) == 0 {
		return nil, fmt.Errorf("no message structs found")
	}
	return raws, nil
}

func hasRule(rules []rule, op string) bool {
	for _, r := range rules {
		if r.op == op {
			return true
		}
	}
	return false
}

func resolvePlan(name string, raws map[string]*rawPlan, types map[string]reflect.Type, cache map[string]*msgPlan) (*msgPlan, error) {
	if p, ok := cache[name]; ok {
		return p, nil
	}
	rp := raws[name]
	if rp == nil {
		return nil, fmt.Errorf("unknown message type %s", name)
	}
	rt, ok := types[name]
	if !ok {
		return nil, fmt.Errorf("no runtime type for %s", name)
	}
	if rt.Kind() != reflect.Struct {
		return nil, fmt.Errorf("%s runtime type is not a struct", name)
	}
	p := &msgPlan{name: name, typ: rt, byJSON: map[string]*fieldPlan{}}
	cache[name] = p
	for _, rf := range rp.fields {
		sf, ok := rt.FieldByName(rf.goName)
		if !ok {
			return nil, fmt.Errorf("%s: field %s missing from runtime type", name, rf.goName)
		}
		fp := &fieldPlan{goName: rf.goName, jsonName: rf.jsonName, index: sf.Index[0], kind: rf.kind, rules: rf.rules}
		if rf.kind == fkNested {
			if !hasRule(rf.rules, "nested") {
				return nil, fmt.Errorf("%s.%s: pointer message requires nested rule", name, rf.goName)
			}
			child, err := resolvePlan(rf.nestedTyp, raws, types, cache)
			if err != nil {
				return nil, err
			}
			fp.nested = child
		}
		p.fields = append(p.fields, fp)
		p.byJSON[fp.jsonName] = fp
	}
	if len(p.fields) != rt.NumField() {
		return nil, fmt.Errorf("%s: schema covers %d of %d fields", name, len(p.fields), rt.NumField())
	}
	return p, nil
}

func buildPlan(src string, types map[string]reflect.Type) (map[string]*msgPlan, error) {
	raws, err := parseSchema(src)
	if err != nil {
		return nil, err
	}
	cache := map[string]*msgPlan{}
	out := map[string]*msgPlan{}
	for name := range raws {
		p, err := resolvePlan(name, raws, types, cache)
		if err != nil {
			return nil, err
		}
		out[name] = p
	}
	return out, nil
}

// ---------- validator ----------

type violation struct {
	path string
	code string
}

const maxDepth = 8

func add(out *[]violation, path, code string) {
	*out = append(*out, violation{path: path, code: code})
}

func joinPath(prefix, name string) string {
	if prefix == "" {
		return name
	}
	return prefix + "." + name
}

func requiredMissing(fv reflect.Value, kind fieldKind) bool {
	switch kind {
	case fkString:
		return fv.String() == ""
	case fkInt64, fkInt32, fkPtrInt64:
		return fv.Int() == 0
	case fkFloat64:
		return fv.Float() == 0
	case fkBool:
		return !fv.Bool()
	case fkStrings, fkFloats, fkMapInt64:
		return fv.IsNil()
	case fkNested:
		return fv.IsNil()
	}
	return false
}

func containsInt(set []int64, v int64) bool {
	for _, x := range set {
		if x == v {
			return true
		}
	}
	return false
}

func containsStr(set []string, v string) bool {
	for _, x := range set {
		if x == v {
			return true
		}
	}
	return false
}

func applyField(fv reflect.Value, fp *fieldPlan, path string, depth int, seen map[uintptr]bool, out *[]violation) {
	if fp.kind == fkNested {
		if fv.IsNil() {
			if hasRule(fp.rules, "required") {
				add(out, path, "required")
			}
			return
		}
		if depth >= maxDepth {
			add(out, path, "depth")
			return
		}
		ptr := fv.Pointer()
		if seen[ptr] {
			add(out, path, "cycle")
			return
		}
		seen[ptr] = true
		validateMessage(fv.Elem(), fp.nested, path, depth+1, seen, out)
		delete(seen, ptr)
		return
	}

	optional := fp.kind == fkPtrInt64 || fp.kind == fkPtrFloat64 || fp.kind == fkPtrString
	if optional {
		if fv.IsNil() {
			if hasRule(fp.rules, "required") {
				add(out, path, "required")
			}
			return
		}
		fv = fv.Elem()
	}

	isInt := fp.kind == fkInt64 || fp.kind == fkInt32 || fp.kind == fkPtrInt64
	isFloat := fp.kind == fkFloat64 || fp.kind == fkPtrFloat64

	for _, r := range fp.rules {
		switch r.op {
		case "required":
			if optional {
				// A non-nil optional pointer satisfies required regardless of value.
				continue
			}
			if requiredMissing(fv, fp.kind) {
				add(out, path, "required")
				return
			}
		case "min":
			if isFloat {
				if fv.Float() < r.f1 {
					add(out, path, "min")
				}
			} else if isInt {
				if fv.Int() < r.i1 {
					add(out, path, "min")
				}
			}
		case "max":
			if isFloat {
				if fv.Float() > r.f2 {
					add(out, path, "max")
				}
			} else if isInt {
				if fv.Int() > r.i2 {
					add(out, path, "max")
				}
			}
		case "finite":
			f := fv.Float()
			if math.IsNaN(f) || math.IsInf(f, 0) {
				add(out, path, "finite")
			}
		case "minlen":
			if utf8.RuneCountInString(fv.String()) < int(r.i1) {
				add(out, path, "minlen")
			}
		case "maxlen":
			if utf8.RuneCountInString(fv.String()) > int(r.i2) {
				add(out, path, "maxlen")
			}
		case "utf8":
			if !utf8.ValidString(fv.String()) {
				add(out, path, "utf8")
			}
		case "oneof":
			if !containsStr(r.list, fv.String()) {
				add(out, path, "oneof")
			}
		case "enum":
			if !containsInt(r.nums, fv.Int()) {
				add(out, path, "enum")
			}
		case "maxitems":
			if int64(fv.Len()) > r.i2 {
				add(out, path, "maxitems")
			}
		case "eachminlen":
			for i := 0; i < fv.Len(); i++ {
				if int64(utf8.RuneCountInString(fv.Index(i).String())) < r.i1 {
					add(out, fmt.Sprintf("%s[%d]", path, i), "eachminlen")
				}
			}
		case "eachfinite":
			for i := 0; i < fv.Len(); i++ {
				f := fv.Index(i).Float()
				if math.IsNaN(f) || math.IsInf(f, 0) {
					add(out, fmt.Sprintf("%s[%d]", path, i), "eachfinite")
				}
			}
		case "eachmax":
			for i := 0; i < fv.Len(); i++ {
				if fv.Index(i).Float() > r.f1 {
					add(out, fmt.Sprintf("%s[%d]", path, i), "eachmax")
				}
			}
		case "keyprefix":
			keys := fv.MapKeys()
			sort.Slice(keys, func(i, j int) bool { return keys[i].String() < keys[j].String() })
			for _, k := range keys {
				if !strings.HasPrefix(k.String(), r.list[0]) {
					add(out, fmt.Sprintf("%s[%s]", path, k.String()), "keyprefix")
				}
			}
		case "valmin":
			keys := fv.MapKeys()
			sort.Slice(keys, func(i, j int) bool { return keys[i].String() < keys[j].String() })
			for _, k := range keys {
				if fv.MapIndex(k).Int() < r.i1 {
					add(out, fmt.Sprintf("%s[%s]", path, k.String()), "valmin")
				}
			}
		case "valmax":
			keys := fv.MapKeys()
			sort.Slice(keys, func(i, j int) bool { return keys[i].String() < keys[j].String() })
			for _, k := range keys {
				if fv.MapIndex(k).Int() > r.i2 {
					add(out, fmt.Sprintf("%s[%s]", path, k.String()), "valmax")
				}
			}
		}
	}
}

func validateMessage(v reflect.Value, p *msgPlan, path string, depth int, seen map[uintptr]bool, out *[]violation) {
	for _, fp := range p.fields {
		fv := v.Field(fp.index)
		applyField(fv, fp, joinPath(path, fp.jsonName), depth, seen, out)
	}
}

func validateAccount(a *Account, p *msgPlan) []violation {
	out := []violation{}
	if a == nil {
		return []violation{{path: "", code: "nil"}}
	}
	validateMessage(reflect.ValueOf(a).Elem(), p, "", 0, map[uintptr]bool{}, &out)
	return out
}

func dump(vs []violation) string {
	var parts []string
	for _, v := range vs {
		parts = append(parts, v.path+":"+v.code)
	}
	return strings.Join(parts, ",")
}

func equalViolations(a, b []violation) bool {
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

func v(path, code string) violation { return violation{path: path, code: code} }

func validAccount() *Account {
	note := "ok"
	return &Account{
		ID: 5, Name: "ab", Balance: 0.5, Role: 2,
		Tags:   []string{"x"},
		Limits: map[string]int64{"lim_a": 1},
		Child:  &Child{Code: 1, Name: "a"},
		Note:   &note,
		Scores: []float64{1},
	}
}

// ---------- main ----------

func main() {
	seed = readSeed()

	rt := map[string]reflect.Type{
		"Account": reflect.TypeOf(Account{}),
		"Child":   reflect.TypeOf(Child{}),
	}
	plans, err := buildPlan(schemaSource(), rt)
	ck(err == nil, "build schema plan")
	if err != nil {
		fmt.Println("FAIL: build schema plan:", err)
		finish("l10_validator_descriptor_reflect")
		return
	}
	ap := plans["Account"]
	cp := plans["Child"]
	ck(ap != nil && cp != nil, "both plans built")
	ck(len(ap.fields) == 9 && len(cp.fields) == 3, "plan field counts")
	ck(ap.fields[0].jsonName == "id" && ap.fields[5].kind == fkMapInt64 && ap.fields[6].nested == cp, "plan kinds and nesting")
	ck(ap.byJSON["note"].kind == fkPtrString && cp.byJSON["next"].nested == cp, "pointer and recursive plans")

	// 1. Valid account produces no violations.
	va := validAccount()
	ck(len(validateAccount(va, ap)) == 0, "valid account passes")

	// 2. A message with one violation per rule family, in deterministic order.
	nan := math.NaN()
	note := ""
	bad := &Account{
		ID: 0, Name: "a", Balance: nan, Role: 9,
		Tags:   []string{"", "b", "c", "d"},
		Limits: map[string]int64{"lim_ok": 20, "lim_x": 5, "lim_y": 5, "zzz": -1},
		Child:  &Child{Code: 0, Name: "c"},
		Note:   &note,
		Scores: []float64{nan, 200},
	}
	want := []violation{
		v("id", "required"),
		v("name", "minlen"),
		v("balance", "finite"),
		v("role", "enum"),
		v("tags", "maxitems"),
		v("tags[0]", "eachminlen"),
		v("limits", "maxitems"),
		v("limits[zzz]", "keyprefix"),
		v("limits[zzz]", "valmin"),
		v("limits[lim_ok]", "valmax"),
		v("child.code", "required"),
		v("child.name", "oneof"),
		v("note", "minlen"),
		v("scores[0]", "eachfinite"),
		v("scores[1]", "eachmax"),
	}
	got := validateAccount(bad, ap)
	ck(equalViolations(got, want), "deterministic violation order: "+dump(got))

	// 3. required short-circuits the remaining rules of the same field.
	req := validAccount()
	req.ID = 0
	got = validateAccount(req, ap)
	ck(equalViolations(got, []violation{v("id", "required")}), "required skips min: "+dump(got))
	req = validAccount()
	req.Name = ""
	got = validateAccount(req, ap)
	ck(equalViolations(got, []violation{v("name", "required")}), "required skips minlen: "+dump(got))

	// 4. Rune-based lengths and explicit UTF-8 validation.
	runeAcc := validAccount()
	s1 := "ééé"
	runeAcc.Note = &s1
	ck(len(validateAccount(runeAcc, ap)) == 0, "multibyte runes counted as runes")
	s2 := "🙂🙂🙂🙂"
	runeAcc.Note = &s2
	ck(len(validateAccount(runeAcc, ap)) == 0, "4-byte rune counted once")
	s3 := "🙂🙂🙂🙂🙂"
	runeAcc.Note = &s3
	got = validateAccount(runeAcc, ap)
	ck(equalViolations(got, []violation{v("note", "maxlen")}), "five runes exceed maxlen")
	utfAcc := validAccount()
	utfAcc.Name = "\xff\xff"
	got = validateAccount(utfAcc, ap)
	ck(equalViolations(got, []violation{v("name", "utf8")}), "invalid UTF-8 flagged: "+dump(got))

	// 5. Optional pointer nil skips rules; required optional fails when missing.
	opt := validAccount()
	opt.Note = nil
	ck(len(validateAccount(opt, ap)) == 0, "nil optional pointer skips rules")

	// 6. Nested cycle and depth guards.
	cyc := &Child{Code: 1, Name: "a"}
	cyc.Next = cyc
	cycAcc := validAccount()
	cycAcc.Child = cyc
	got = validateAccount(cycAcc, ap)
	ck(equalViolations(got, []violation{v("child.next", "cycle")}), "cycle detected: "+dump(got))
	var chain *Child
	for i := 0; i < 12; i++ {
		chain = &Child{Code: 1, Name: "a", Next: chain}
	}
	deep := validAccount()
	deep.Child = chain
	got = validateAccount(deep, ap)
	ck(len(got) == 1 && got[0].code == "depth" && strings.HasPrefix(got[0].path, "child.next"), "depth guard: "+dump(got))

	// 7. Map key/value violation paths are sorted by key.
	m := validAccount()
	m.Limits = map[string]int64{"zzz": 1, "lim_b": 99, "aaa": 2}
	got = validateAccount(m, ap)
	ck(len(got) == 3 && got[0].path == "limits[aaa]" && got[0].code == "keyprefix" && got[1].path == "limits[zzz]" && got[1].code == "keyprefix" && got[2].path == "limits[lim_b]" && got[2].code == "valmax", "map sorted order: "+dump(got))

	// 8. Build-time schema errors.
	typeSrc := func(decls ...string) string {
		return "package gen\n\ntype Account struct {\n" + strings.Join(decls, "\n") + "\n}\n"
	}
	badSchemas := []struct {
		name string
		src  string
	}{
		{"parse error", "package gen\ntype Account struct {"},
		{"missing tag", typeSrc("\tID int64")},
		{"missing json", typeSrc("\tID int64 `validate:\"required\"`")},
		{"duplicate json", typeSrc("\tID int64 `json:\"x\"`", "\tName string `json:\"x\"`")},
		{"unexported", typeSrc("\tid int64 `json:\"id\"`")},
		{"embedded", typeSrc("\tChild `json:\"child\"`")},
		{"unknown type", typeSrc("\tX Nope `json:\"x\"`")},
		{"unknown rule", typeSrc("\tID int64 `json:\"id\" validate:\"frobnicate\"`")},
		{"dup rule", typeSrc("\tID int64 `json:\"id\" validate:\"min=1,min=2\"`")},
		{"min>max", typeSrc("\tID int64 `json:\"id\" validate:\"min=5,max=1\"`")},
		{"minlen>maxlen", typeSrc("\tName string `json:\"name\" validate:\"minlen=5,maxlen=1\"`")},
		{"bad enum", typeSrc("\tRole int32 `json:\"role\" validate:\"enum=1|x\"`")},
		{"empty oneof", typeSrc("\tName string `json:\"name\" validate:\"oneof=\"`")},
		{"bad int param", typeSrc("\tID int64 `json:\"id\" validate:\"min=1=2\"`")},
		{"negative maxitems", typeSrc("\tTags []string `json:\"tags\" validate:\"maxitems=-1\"`")},
		{"wrong kind rule", typeSrc("\tID int64 `json:\"id\" validate:\"utf8\"`")},
		{"nested on scalar", typeSrc("\tID int64 `json:\"id\" validate:\"nested\"`")},
		{"ptr struct no nested", typeSrc("\tChild *Child `json:\"child\"`")},
		{"no params allowed", typeSrc("\tBalance float64 `json:\"b\" validate:\"finite=1\"`")},
	}
	for _, bs := range badSchemas {
		_, err := buildPlan(bs.src, rt)
		ck(err != nil, "schema reject: "+bs.name)
	}

	// 9. GC: plans, nested values and validation results survive.
	keep := make([]*Account, 0, 200)
	for i := 0; i < 200; i++ {
		a := validAccount()
		a.ID = int64(i + 1)
		a.Tags = []string{fmt.Sprintf("t%d", i)}
		keep = append(keep, a)
	}
	gcStress()
	for i, a := range keep {
		ck(len(validateAccount(a, ap)) == 0, fmt.Sprintf("post-GC valid %d", i))
	}
	ck(ap.typ == reflect.TypeOf(Account{}), "plan type survives GC")

	// 10. Seeded torture: random single-field mutations produce the expected rule.
	type mutation struct {
		name string
		code string
		fn   func(a *Account)
	}
	muts := []mutation{
		{"id required", "required", func(a *Account) { a.ID = 0 }},
		{"id max", "max", func(a *Account) { a.ID = 2000000 }},
		{"name minlen", "minlen", func(a *Account) { a.Name = "a" }},
		{"balance finite", "finite", func(a *Account) { a.Balance = math.NaN() }},
		{"role enum", "enum", func(a *Account) { a.Role = 7 }},
		{"tags maxitems", "maxitems", func(a *Account) { a.Tags = []string{"a", "b", "c", "d"} }},
		{"tags eachminlen", "eachminlen", func(a *Account) { a.Tags = []string{""} }},
		{"limits keyprefix", "keyprefix", func(a *Account) { a.Limits = map[string]int64{"bad": 1} }},
		{"limits valmax", "valmax", func(a *Account) { a.Limits = map[string]int64{"lim_a": 11} }},
		{"note minlen", "minlen", func(a *Account) { s := ""; a.Note = &s }},
		{"scores eachfinite", "eachfinite", func(a *Account) { a.Scores = []float64{math.Inf(-1)} }},
		{"scores eachmax", "eachmax", func(a *Account) { a.Scores = []float64{101} }},
		{"child required", "required", func(a *Account) { a.Child = &Child{Code: 0, Name: "a"} }},
		{"child oneof", "oneof", func(a *Account) { a.Child = &Child{Code: 1, Name: "z"} }},
	}
	rounds := envInt("TORTURE_ROUNDS", 300)
	r := rand.New(rand.NewSource(seed))
	okRounds := 0
	for i := 0; i < rounds; i++ {
		mu := muts[r.Intn(len(muts))]
		a := validAccount()
		mu.fn(a)
		vs := validateAccount(a, ap)
		ok := len(vs) == 1 && vs[0].code == mu.code
		if !ok {
			ckSeed(false, fmt.Sprintf("torture %d %s: %s", i, mu.name, dump(vs)))
			continue
		}
		// Same value must validate identically before/after GC.
		if i%31 == 0 {
			gcStress()
			vs2 := validateAccount(a, ap)
			if !equalViolations(vs, vs2) {
				ckSeed(false, fmt.Sprintf("torture %d GC instability", i))
				continue
			}
		}
		okRounds++
	}
	ck(okRounds == rounds, "torture: all mutation cases matched")

	finish("l10_validator_descriptor_reflect")
}
