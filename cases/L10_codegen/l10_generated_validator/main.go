// L10: 生成式校验器：从紧凑生成 spec 构建规则计划、类型/参数校验、
// 确定顺序的违规集合、嵌套/oneof/跨字段、递归深度与环检测、GC 与随机变异。
package main

import (
	"fmt"
	"math/rand"
	"os"
	"regexp"
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

// ---- 消息 ----

type contactEmail struct{ V string }
type contactPhone struct{ V string }

type address struct {
	City string
	Zip  string
}

type user struct {
	Name    string
	Age     int32
	Score   float64
	Email   string
	Role    int32
	Tags    []string
	Labels  map[string]int32
	Address *address
	Contact any
	Start   int32
	End     int32
	Next    *user
}

// ---- schema ----

type fieldKind int

const (
	fkString fieldKind = iota
	fkInt32
	fkFloat64
	fkEnumInt
	fkRepeatedString
	fkMapStrInt32
	fkMessage
	fkOneof
)

type schema struct {
	name   string
	fields map[string]fieldKind
}

var schemas = map[string]*schema{
	"user": {name: "user", fields: map[string]fieldKind{
		"name": fkString, "age": fkInt32, "score": fkFloat64, "email": fkString,
		"role": fkEnumInt, "tags": fkRepeatedString, "labels": fkMapStrInt32,
		"address": fkMessage, "contact": fkOneof, "start": fkInt32, "end": fkInt32,
		"next": fkMessage,
	}},
	"address": {name: "address", fields: map[string]fieldKind{
		"city": fkString, "zip": fkString,
	}},
}

// ---- 规则与计划 ----

type rule struct {
	field  string
	op     string
	i1     int
	i2     int
	f1     float64
	f2     float64
	s1     string
	re     *regexp.Regexp
	enums  []int32
	target string
	other  string
}

type validatorPlan struct {
	name     string
	schema   *schema
	rules    []rule
	nested   map[string]*validatorPlan
	maxDepth int
	seenRule map[string]struct{}
}

func paramCount(rest []string, want int) bool { return len(rest) == want }

func parseIntParam(s string) (int, error) {
	n, err := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
	if err != nil {
		return 0, err
	}
	return int(n), nil
}

func parseRule(sc *schema, line string, plan *validatorPlan) (rule, error) {
	var r rule
	parts := strings.Split(line, "|")
	if len(parts) < 2 {
		return r, fmt.Errorf("bad rule %q", line)
	}
	r.field = strings.TrimSpace(parts[0])
	r.op = strings.TrimSpace(parts[1])
	kind, ok := sc.fields[r.field]
	if !ok {
		return r, fmt.Errorf("unknown field %q in %s", r.field, sc.name)
	}
	rest := parts[2:]
	key := r.field + "|" + r.op
	if _, dup := plan.seenRule[key]; dup {
		return r, fmt.Errorf("duplicate rule %q", key)
	}
	switch r.op {
	case "required":
		if !paramCount(rest, 0) || (kind != fkString && kind != fkMessage && kind != fkOneof) {
			return r, fmt.Errorf("required not applicable to %s", r.field)
		}
	case "oneof_required":
		if !paramCount(rest, 0) || kind != fkOneof {
			return r, fmt.Errorf("oneof_required not applicable to %s", r.field)
		}
	case "strlen":
		if !paramCount(rest, 2) || kind != fkString {
			return r, fmt.Errorf("strlen not applicable to %s", r.field)
		}
		i1, err1 := parseIntParam(rest[0])
		i2, err2 := parseIntParam(rest[1])
		if err1 != nil || err2 != nil || i1 < 0 || i1 > i2 {
			return r, fmt.Errorf("bad strlen params for %s", r.field)
		}
		r.i1, r.i2 = i1, i2
	case "regex":
		if !paramCount(rest, 1) || kind != fkString {
			return r, fmt.Errorf("regex not applicable to %s", r.field)
		}
		re, err := regexp.Compile(rest[0])
		if err != nil {
			return r, err
		}
		r.re = re
	case "prefix", "suffix", "contains":
		if !paramCount(rest, 1) || kind != fkString {
			return r, fmt.Errorf("%s not applicable to %s", r.op, r.field)
		}
		r.s1 = rest[0]
	case "intrange":
		if !paramCount(rest, 2) || kind != fkInt32 {
			return r, fmt.Errorf("intrange not applicable to %s", r.field)
		}
		i1, err1 := parseIntParam(rest[0])
		i2, err2 := parseIntParam(rest[1])
		if err1 != nil || err2 != nil || i1 > i2 {
			return r, fmt.Errorf("bad intrange params for %s", r.field)
		}
		r.i1, r.i2 = i1, i2
	case "floatrange":
		if !paramCount(rest, 2) || kind != fkFloat64 {
			return r, fmt.Errorf("floatrange not applicable to %s", r.field)
		}
		f1, err1 := strconv.ParseFloat(strings.TrimSpace(rest[0]), 64)
		f2, err2 := strconv.ParseFloat(strings.TrimSpace(rest[1]), 64)
		if err1 != nil || err2 != nil || f1 > f2 {
			return r, fmt.Errorf("bad floatrange params for %s", r.field)
		}
		r.f1, r.f2 = f1, f2
	case "enum":
		if !paramCount(rest, 1) || kind != fkEnumInt {
			return r, fmt.Errorf("enum not applicable to %s", r.field)
		}
		for _, p := range strings.Split(rest[0], ",") {
			n, err := parseIntParam(p)
			if err != nil {
				return r, fmt.Errorf("bad enum value %q", p)
			}
			r.enums = append(r.enums, int32(n))
		}
		if len(r.enums) == 0 {
			return r, fmt.Errorf("empty enum list")
		}
	case "repeated_min", "repeated_max":
		if !paramCount(rest, 1) || kind != fkRepeatedString {
			return r, fmt.Errorf("%s not applicable to %s", r.op, r.field)
		}
		n, err := parseIntParam(rest[0])
		if err != nil || n < 0 {
			return r, fmt.Errorf("bad count for %s", r.field)
		}
		r.i1 = n
	case "each_strlen":
		if !paramCount(rest, 2) || kind != fkRepeatedString {
			return r, fmt.Errorf("each_strlen not applicable to %s", r.field)
		}
		i1, err1 := parseIntParam(rest[0])
		i2, err2 := parseIntParam(rest[1])
		if err1 != nil || err2 != nil || i1 < 0 || i1 > i2 {
			return r, fmt.Errorf("bad each_strlen params for %s", r.field)
		}
		r.i1, r.i2 = i1, i2
	case "map_min", "map_max":
		if !paramCount(rest, 1) || kind != fkMapStrInt32 {
			return r, fmt.Errorf("%s not applicable to %s", r.op, r.field)
		}
		n, err := parseIntParam(rest[0])
		if err != nil || n < 0 {
			return r, fmt.Errorf("bad count for %s", r.field)
		}
		r.i1 = n
	case "map_key_prefix":
		if !paramCount(rest, 1) || kind != fkMapStrInt32 {
			return r, fmt.Errorf("map_key_prefix not applicable to %s", r.field)
		}
		r.s1 = rest[0]
	case "map_val_range":
		if !paramCount(rest, 2) || kind != fkMapStrInt32 {
			return r, fmt.Errorf("map_val_range not applicable to %s", r.field)
		}
		i1, err1 := parseIntParam(rest[0])
		i2, err2 := parseIntParam(rest[1])
		if err1 != nil || err2 != nil || i1 > i2 {
			return r, fmt.Errorf("bad map_val_range params for %s", r.field)
		}
		r.i1, r.i2 = i1, i2
	case "nested":
		if !paramCount(rest, 1) || kind != fkMessage {
			return r, fmt.Errorf("nested not applicable to %s", r.field)
		}
		r.target = rest[0]
		if _, ok := schemas[r.target]; !ok {
			return r, fmt.Errorf("unknown nested schema %q", r.target)
		}
	case "le_field":
		if !paramCount(rest, 1) || kind != fkInt32 {
			return r, fmt.Errorf("le_field not applicable to %s", r.field)
		}
		r.other = rest[0]
		ok, ok2 := sc.fields[r.other]
		if !ok2 || ok != fkInt32 || r.other == r.field {
			return r, fmt.Errorf("bad le_field target %q", r.other)
		}
	default:
		return r, fmt.Errorf("unknown op %q", r.op)
	}
	plan.seenRule[key] = struct{}{}
	return r, nil
}

func buildPlan(lines []string) (map[string]*validatorPlan, error) {
	type section struct {
		name  string
		lines []string
	}
	var secs []section
	idx := make(map[string]int)
	var cur = -1
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "message=") {
			name := strings.TrimSpace(strings.TrimPrefix(line, "message="))
			if _, ok := schemas[name]; !ok {
				return nil, fmt.Errorf("unknown message schema %q", name)
			}
			if _, dup := idx[name]; dup {
				return nil, fmt.Errorf("duplicate message section %q", name)
			}
			idx[name] = len(secs)
			secs = append(secs, section{name: name})
			cur = len(secs) - 1
			continue
		}
		if cur < 0 {
			return nil, fmt.Errorf("rule before message section")
		}
		secs[cur].lines = append(secs[cur].lines, line)
	}
	plans := make(map[string]*validatorPlan, len(secs))
	for _, s := range secs {
		plans[s.name] = &validatorPlan{
			name:     s.name,
			schema:   schemas[s.name],
			nested:   make(map[string]*validatorPlan),
			maxDepth: 8,
			seenRule: make(map[string]struct{}),
		}
	}
	for _, s := range secs {
		p := plans[s.name]
		for _, line := range s.lines {
			r, err := parseRule(schemas[s.name], line, p)
			if err != nil {
				return nil, err
			}
			p.rules = append(p.rules, r)
		}
	}
	for _, p := range plans {
		for _, r := range p.rules {
			if r.op != "nested" {
				continue
			}
			target, ok := plans[r.target]
			if !ok {
				return nil, fmt.Errorf("nested target %q not defined", r.target)
			}
			p.nested[r.field] = target
		}
	}
	return plans, nil
}

// ---- 校验执行 ----

type violation struct {
	path string
	code string
}

func addViolation(out *[]violation, path, code string) {
	*out = append(*out, violation{path: path, code: code})
}

func joinPath(prefix, field string) string {
	if prefix == "" {
		return field
	}
	return prefix + "." + field
}

func (p *validatorPlan) applyString(value, field, path string, out *[]violation) {
	for _, r := range p.rules {
		if r.field != field {
			continue
		}
		switch r.op {
		case "required":
			if value == "" {
				addViolation(out, path, "required")
			}
		case "strlen":
			if len(value) < r.i1 || len(value) > r.i2 {
				addViolation(out, path, "strlen")
			}
		case "regex":
			if !r.re.MatchString(value) {
				addViolation(out, path, "regex")
			}
		case "prefix":
			if !strings.HasPrefix(value, r.s1) {
				addViolation(out, path, "prefix")
			}
		case "suffix":
			if !strings.HasSuffix(value, r.s1) {
				addViolation(out, path, "suffix")
			}
		case "contains":
			if !strings.Contains(value, r.s1) {
				addViolation(out, path, "contains")
			}
		}
	}
}

func (p *validatorPlan) applyInt(value int64, field, path string, out *[]violation) {
	for _, r := range p.rules {
		if r.field != field {
			continue
		}
		switch r.op {
		case "intrange":
			if value < int64(r.i1) || value > int64(r.i2) {
				addViolation(out, path, "intrange")
			}
		case "enum":
			ok := false
			for _, e := range r.enums {
				if int64(e) == value {
					ok = true
					break
				}
			}
			if !ok {
				addViolation(out, path, "enum")
			}
		}
	}
}

func (p *validatorPlan) applyFloat(value float64, field, path string, out *[]violation) {
	for _, r := range p.rules {
		if r.field != field || r.op != "floatrange" {
			continue
		}
		if value < r.f1 || value > r.f2 {
			addViolation(out, path, "floatrange")
		}
	}
}

func (p *validatorPlan) applySlice(values []string, field, path string, out *[]violation) {
	for _, r := range p.rules {
		if r.field != field {
			continue
		}
		switch r.op {
		case "repeated_min":
			if len(values) < r.i1 {
				addViolation(out, path, "repeated-min")
			}
		case "repeated_max":
			if len(values) > r.i1 {
				addViolation(out, path, "repeated-max")
			}
		case "each_strlen":
			for i, v := range values {
				if len(v) < r.i1 || len(v) > r.i2 {
					addViolation(out, fmt.Sprintf("%s[%d]", path, i), "strlen")
				}
			}
		}
	}
}

func (p *validatorPlan) applyMap(values map[string]int32, field, path string, out *[]violation) {
	keys := make([]string, 0, len(values))
	for k := range values {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, r := range p.rules {
		if r.field != field {
			continue
		}
		switch r.op {
		case "map_min":
			if len(values) < r.i1 {
				addViolation(out, path, "map-min")
			}
		case "map_max":
			if len(values) > r.i1 {
				addViolation(out, path, "map-max")
			}
		case "map_key_prefix":
			for _, k := range keys {
				if !strings.HasPrefix(k, r.s1) {
					addViolation(out, fmt.Sprintf("%s[%s]", path, k), "map-key-prefix")
				}
			}
		case "map_val_range":
			for _, k := range keys {
				v := int64(values[k])
				if v < int64(r.i1) || v > int64(r.i2) {
					addViolation(out, fmt.Sprintf("%s[%s]", path, k), "map-val-range")
				}
			}
		}
	}
}

func oneofSet(v any) bool {
	switch x := v.(type) {
	case nil:
		return false
	case *contactEmail:
		return x != nil
	case *contactPhone:
		return x != nil
	}
	return true
}

func (p *validatorPlan) walkUser(u *user, path string, depth int, seen map[any]bool, out *[]violation) {
	if depth > p.maxDepth {
		addViolation(out, path, "max-depth")
		return
	}
	if seen[u] {
		addViolation(out, path, "cycle")
		return
	}
	seen[u] = true
	defer delete(seen, u)

	p.applyString(u.Name, "name", joinPath(path, "name"), out)
	p.applyString(u.Email, "email", joinPath(path, "email"), out)
	p.applyInt(int64(u.Age), "age", joinPath(path, "age"), out)
	p.applyInt(int64(u.Role), "role", joinPath(path, "role"), out)
	p.applyInt(int64(u.Start), "start", joinPath(path, "start"), out)
	p.applyInt(int64(u.End), "end", joinPath(path, "end"), out)
	p.applyFloat(u.Score, "score", joinPath(path, "score"), out)
	p.applySlice(u.Tags, "tags", joinPath(path, "tags"), out)
	p.applyMap(u.Labels, "labels", joinPath(path, "labels"), out)

	for field, child := range p.nested {
		switch field {
		case "address":
			if u.Address != nil {
				child.walkAddress(u.Address, joinPath(path, field), depth, seen, out)
			}
		case "next":
			if u.Next != nil {
				child.walkUser(u.Next, joinPath(path, field), depth+1, seen, out)
			}
		}
	}
	for _, r := range p.rules {
		switch r.op {
		case "oneof_required":
			if !oneofSet(u.Contact) {
				addViolation(out, joinPath(path, "contact"), "oneof-required")
			}
		case "le_field":
			if u.Start > u.End {
				addViolation(out, joinPath(path, r.field), "cross-le")
			}
		}
	}
}

func (p *validatorPlan) walkAddress(a *address, path string, depth int, seen map[any]bool, out *[]violation) {
	if depth > p.maxDepth {
		addViolation(out, path, "max-depth")
		return
	}
	if seen[a] {
		addViolation(out, path, "cycle")
		return
	}
	seen[a] = true
	defer delete(seen, a)
	p.applyString(a.City, "city", joinPath(path, "city"), out)
	p.applyString(a.Zip, "zip", joinPath(path, "zip"), out)
}

func (p *validatorPlan) validate(u *user) []violation {
	if u == nil {
		return nil
	}
	var out []violation
	p.walkUser(u, "", 0, make(map[any]bool), &out)
	sort.Slice(out, func(i, j int) bool {
		if out[i].path != out[j].path {
			return out[i].path < out[j].path
		}
		return out[i].code < out[j].code
	})
	return out
}

// ---- 测试辅助 ----

func validUser() *user {
	return &user{
		Name:    "alice",
		Age:     30,
		Score:   88.5,
		Email:   "alice@example.com",
		Role:    1,
		Tags:    []string{"a", "bb"},
		Labels:  map[string]int32{"env_a": 1},
		Address: &address{City: "Paris", Zip: "12345"},
		Contact: &contactEmail{V: "alice@example.com"},
		Start:   1,
		End:     2,
	}
}

func v(path, code string) violation { return violation{path: path, code: code} }

func sortedViolations(in []violation) []violation {
	out := append([]violation(nil), in...)
	sort.Slice(out, func(i, j int) bool {
		if out[i].path != out[j].path {
			return out[i].path < out[j].path
		}
		return out[i].code < out[j].code
	})
	return out
}

func equalViolations(a, b []violation) bool {
	a, b = sortedViolations(a), sortedViolations(b)
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

func dumpViolations(vs []violation) string {
	vs = sortedViolations(vs)
	var b strings.Builder
	for _, x := range vs {
		fmt.Fprintf(&b, "%s:%s;", x.path, x.code)
	}
	return b.String()
}

func expectViolations(plan *validatorPlan, u *user, want []violation, msg string) {
	got := plan.validate(u)
	ck(equalViolations(got, want), msg+" got="+dumpViolations(got)+" want="+dumpViolations(want))
}

func expectPlanError(name string, lines []string) {
	_, err := buildPlan(lines)
	ck(err != nil, "expected plan error: "+name)
}

func planLines() []string {
	return []string{
		"message=address",
		"city|required",
		"city|strlen|1|20",
		"zip|required",
		"zip|regex|^[0-9]{5}$",
		"message=user",
		"name|required",
		"name|strlen|2|12",
		"age|intrange|0|130",
		"score|floatrange|0|100",
		"email|regex|^[a-z0-9._%+\\-]+@[a-z0-9.\\-]+\\.[a-z]{2,}$",
		"role|enum|1,2",
		"tags|repeated_min|1",
		"tags|repeated_max|5",
		"tags|each_strlen|1|8",
		"labels|map_min|1",
		"labels|map_max|3",
		"labels|map_key_prefix|env_",
		"labels|map_val_range|0|9",
		"address|nested|address",
		"contact|oneof_required",
		"start|intrange|0|1000",
		"end|intrange|0|1000",
		"start|le_field|end",
		"next|nested|user",
	}
}

func main() {
	seed := int64(1)
	if v := os.Getenv("UAF_SEED"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			seed = n
		}
	}
	curSeed = seed

	plans, err := buildPlan(planLines())
	ck(err == nil, "build plan")
	userPlan, ok1 := plans["user"]
	addrPlan, ok2 := plans["address"]
	ck(ok1 && ok2 && len(plans) == 2, "plan sections")
	if ok1 && ok2 {
		ck(len(userPlan.rules) == 19, "user rule count")
		ck(len(addrPlan.rules) == 4, "address rule count")
		ck(userPlan.nested["address"] == addrPlan, "address nested link")
		ck(userPlan.nested["next"] == userPlan, "recursive nested link")
	}

	// 1. 合法消息零违规；nil 顶层零违规；address 缺席不触发嵌套规则。
	expectViolations(userPlan, validUser(), nil, "valid user")
	ck(len(userPlan.validate(nil)) == 0, "nil top-level")
	noAddr := validUser()
	noAddr.Address = nil
	expectViolations(userPlan, noAddr, nil, "nil address allowed")
	typedNilContact := validUser()
	typedNilContact.Contact = (*contactEmail)(nil)
	expectViolations(userPlan, typedNilContact, []violation{v("contact", "oneof-required")}, "typed-nil contact")

	// 2. 全违规组合与稳定排序。
	bad := &user{
		Name:    "",
		Age:     -1,
		Score:   101,
		Email:   "bad",
		Role:    0,
		Tags:    []string{},
		Labels:  map[string]int32{"x": 10},
		Address: &address{},
		Contact: nil,
		Start:   10,
		End:     5,
	}
	wantAll := []violation{
		v("address.city", "required"),
		v("address.city", "strlen"),
		v("address.zip", "required"),
		v("address.zip", "regex"),
		v("age", "intrange"),
		v("contact", "oneof-required"),
		v("email", "regex"),
		v("labels[x]", "map-key-prefix"),
		v("labels[x]", "map-val-range"),
		v("name", "required"),
		v("name", "strlen"),
		v("role", "enum"),
		v("score", "floatrange"),
		v("start", "cross-le"),
		v("tags", "repeated-min"),
	}
	expectViolations(userPlan, bad, wantAll, "all invalid")

	// 3. 单独规则：长度、范围、枚举、容器、map 键前缀、跨字段。
	cases := []struct {
		name string
		mut  func(*user)
		want []violation
	}{
		{"short name", func(u *user) { u.Name = "a" }, []violation{v("name", "strlen")}},
		{"long name", func(u *user) { u.Name = strings.Repeat("x", 13) }, []violation{v("name", "strlen")}},
		{"age high", func(u *user) { u.Age = 131 }, []violation{v("age", "intrange")}},
		{"score low", func(u *user) { u.Score = -0.1 }, []violation{v("score", "floatrange")}},
		{"bad role", func(u *user) { u.Role = 3 }, []violation{v("role", "enum")}},
		{"tags max", func(u *user) { u.Tags = []string{"a", "b", "c", "d", "e", "f"} }, []violation{v("tags", "repeated-max")}},
		{"tag too long", func(u *user) { u.Tags = []string{"ok", "toolongxx"} }, []violation{v("tags[1]", "strlen")}},
		{"labels max", func(u *user) { u.Labels = map[string]int32{"env_a": 1, "env_b": 2, "env_c": 3, "env_d": 4} }, []violation{v("labels", "map-max")}},
		{"labels prefix", func(u *user) { u.Labels = map[string]int32{"bad": 1} }, []violation{v("labels[bad]", "map-key-prefix")}},
		{"labels value", func(u *user) { u.Labels = map[string]int32{"env_x": 10} }, []violation{v("labels[env_x]", "map-val-range")}},
		{"address city long", func(u *user) { u.Address = &address{City: "way-too-long-city-name", Zip: "12345"} }, []violation{v("address.city", "strlen")}},
		{"address zip", func(u *user) { u.Address = &address{City: "Paris", Zip: "12"} }, []violation{v("address.zip", "regex")}},
		{"bad start/end", func(u *user) { u.Start = 9; u.End = 8 }, []violation{v("start", "cross-le")}},
	}
	for _, c := range cases {
		u := validUser()
		c.mut(u)
		expectViolations(userPlan, u, c.want, c.name)
	}

	// 4. 嵌套 next：违规路径带前缀；合法 next 不产生违规。
	withNext := validUser()
	withNext.Next = validUser()
	expectViolations(userPlan, withNext, nil, "valid next")
	withNext.Next.Name = ""
	expectViolations(userPlan, withNext, []violation{
		v("next.name", "required"), v("next.name", "strlen"),
	}, "nested next violations")
	withNext.Next.Next = validUser()
	withNext.Next.Next.Age = -1
	expectViolations(userPlan, withNext, []violation{
		v("next.name", "required"), v("next.name", "strlen"),
		v("next.next.age", "intrange"),
	}, "two-level nested")

	// 5. 深度限制与环检测。
	chain := validUser()
	cur := chain
	for i := 0; i < 11; i++ {
		n := validUser()
		cur.Next = n
		cur = n
	}
	depthPath := strings.Repeat("next.", 8) + "next"
	expectViolations(userPlan, chain, []violation{v(depthPath, "max-depth")}, "max depth")
	cyc := validUser()
	cyc.Next = cyc
	expectViolations(userPlan, cyc, []violation{v("next", "cycle")}, "self cycle")
	a := validUser()
	b := validUser()
	a.Next = b
	b.Next = a
	expectViolations(userPlan, a, []violation{v("next.next", "cycle")}, "two-node cycle")

	// 6. 计划构建错误。
	expectPlanError("unknown op", []string{"message=user", "name|bogus"})
	expectPlanError("unknown field", []string{"message=user", "nope|required"})
	expectPlanError("kind mismatch", []string{"message=user", "age|strlen|1|2"})
	expectPlanError("bad int param", []string{"message=user", "name|strlen|a|2"})
	expectPlanError("min greater than max", []string{"message=user", "name|strlen|5|2"})
	expectPlanError("bad regex", []string{"message=user", "email|regex|("})
	expectPlanError("unknown nested", []string{"message=user", "address|nested|missing"})
	expectPlanError("unknown cross field", []string{"message=user", "start|le_field|missing"})
	expectPlanError("duplicate rule", []string{"message=user", "name|required", "name|required"})
	expectPlanError("rule before section", []string{"name|required"})
	expectPlanError("unknown message", []string{"message=nope", "name|required"})
	expectPlanError("duplicate message", []string{"message=user", "message=user"})
	expectPlanError("oneof on string", []string{"message=user", "name|oneof_required"})
	expectPlanError("empty enum", []string{"message=user", "role|enum|"})
	expectPlanError("negative count", []string{"message=user", "tags|repeated_min|-1"})

	// 7. GC：保留计划与消息，丢弃临时对象后校验结果稳定。
	gcUser := validUser()
	before := dumpViolations(userPlan.validate(gcUser))
	tblPlan := userPlan
	userPlan = nil
	gcStress()
	after := dumpViolations(tblPlan.validate(gcUser))
	ck(before == after && before == "", "validation stable across GC")
	keepViolations := tblPlan.validate(bad)
	bad = nil
	gcStress()
	ck(len(keepViolations) == 15, "violations survive GC")

	// 8. 随机变异：合法基线上叠加 1..4 个互不冲突的变异，校验违规集合。
	r := rand.New(rand.NewSource(seed))
	type mutation struct {
		group int
		name  string
		apply func(*user)
		want  []violation
	}
	mutations := []mutation{
		{0, "empty name", func(u *user) { u.Name = "" }, []violation{v("name", "required"), v("name", "strlen")}},
		{0, "short name", func(u *user) { u.Name = "a" }, []violation{v("name", "strlen")}},
		{0, "valid name", func(u *user) { u.Name = "bob" }, nil},
		{1, "age low", func(u *user) { u.Age = -1 }, []violation{v("age", "intrange")}},
		{1, "age valid", func(u *user) { u.Age = 130 }, nil},
		{2, "score high", func(u *user) { u.Score = 100.5 }, []violation{v("score", "floatrange")}},
		{2, "score valid", func(u *user) { u.Score = 0 }, nil},
		{3, "bad email", func(u *user) { u.Email = "bad" }, []violation{v("email", "regex")}},
		{3, "valid email", func(u *user) { u.Email = "x@y.io" }, nil},
		{4, "bad role", func(u *user) { u.Role = 0 }, []violation{v("role", "enum")}},
		{4, "valid role", func(u *user) { u.Role = 2 }, nil},
		{5, "empty tags", func(u *user) { u.Tags = nil }, []violation{v("tags", "repeated-min")}},
		{5, "long tag", func(u *user) { u.Tags = []string{"ok", "toolongxx"} }, []violation{v("tags[1]", "strlen")}},
		{5, "valid tags", func(u *user) { u.Tags = []string{"t"} }, nil},
		{6, "empty labels", func(u *user) { u.Labels = nil }, []violation{v("labels", "map-min")}},
		{6, "bad key", func(u *user) { u.Labels = map[string]int32{"x": 1} }, []violation{v("labels[x]", "map-key-prefix")}},
		{6, "bad val", func(u *user) { u.Labels = map[string]int32{"env_x": 10} }, []violation{v("labels[env_x]", "map-val-range")}},
		{6, "valid labels", func(u *user) { u.Labels = map[string]int32{"env_k": 9} }, nil},
		{7, "nil address", func(u *user) { u.Address = nil }, nil},
		{7, "empty address", func(u *user) { u.Address = &address{} }, []violation{
			v("address.city", "required"), v("address.city", "strlen"),
			v("address.zip", "required"), v("address.zip", "regex"),
		}},
		{7, "bad zip", func(u *user) { u.Address = &address{City: "Paris", Zip: "1"} }, []violation{v("address.zip", "regex")}},
		{7, "valid address", func(u *user) { u.Address = &address{City: "Rome", Zip: "00000"} }, nil},
		{8, "nil contact", func(u *user) { u.Contact = nil }, []violation{v("contact", "oneof-required")}},
		{8, "valid contact", func(u *user) { u.Contact = &contactPhone{V: "1"} }, nil},
		{9, "bad order", func(u *user) { u.Start = 7; u.End = 3 }, []violation{v("start", "cross-le")}},
		{9, "valid order", func(u *user) { u.Start = 3; u.End = 3 }, nil},
		{10, "bad next", func(u *user) {
			n := validUser()
			n.Name = ""
			u.Next = n
		}, []violation{v("next.name", "required"), v("next.name", "strlen")}},
		{10, "valid next", func(u *user) { u.Next = validUser() }, nil},
	}
	for i := 0; i < 200; i++ {
		u := validUser()
		// 随机化合法值，保证基线本身零违规。
		u.Name = "user" + strconv.Itoa(r.Intn(10))
		u.Age = int32(r.Intn(131))
		u.Score = float64(r.Intn(1001)) / 10
		u.Email = "u" + strconv.Itoa(r.Intn(100)) + "@ex.com"
		u.Role = int32(1 + r.Intn(2))
		nTags := 1 + r.Intn(3)
		u.Tags = u.Tags[:0]
		for j := 0; j < nTags; j++ {
			u.Tags = append(u.Tags, "t"+strconv.Itoa(r.Intn(100)))
		}
		u.Labels = make(map[string]int32)
		nLabels := 1 + r.Intn(3)
		for j := 0; j < nLabels; j++ {
			u.Labels["env_"+strconv.Itoa(j)] = int32(r.Intn(10))
		}
		u.Address = &address{City: "City" + strconv.Itoa(r.Intn(100)), Zip: fmt.Sprintf("%05d", r.Intn(100000))}
		u.Contact = &contactEmail{V: "x"}
		s := int32(r.Intn(500))
		u.Start = s
		u.End = s + int32(r.Intn(500))
		u.Next = nil
		// 选择互不冲突的变异组。
		byGroup := make(map[int][]mutation)
		for _, m := range mutations {
			byGroup[m.group] = append(byGroup[m.group], m)
		}
		groups := []int{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10}
		r.Shuffle(len(groups), func(a, b int) { groups[a], groups[b] = groups[b], groups[a] })
		nMut := 1 + r.Intn(4)
		var want []violation
		for j := 0; j < nMut && j < len(groups); j++ {
			opts := byGroup[groups[j]]
			m := opts[r.Intn(len(opts))]
			m.apply(u)
			want = append(want, m.want...)
		}
		got := tblPlan.validate(u)
		ckSeed(equalViolations(got, want), "random mutation validation got="+dumpViolations(got)+" want="+dumpViolations(want))
		if i%25 == 24 {
			gcStress()
			ckSeed(equalViolations(tblPlan.validate(u), want), "random survives GC")
		}
	}

	finish("l10_generated_validator")
}
