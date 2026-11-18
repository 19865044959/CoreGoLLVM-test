// L10: 生成风格字段校验器：required/length/range/enum/repeated/map/nested/
// optional/跨字段规则，错误路径与顺序确定；GC 后校验结果稳定。
package main

import (
	"fmt"
	"math"
	"math/rand"
	"os"
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

// ---- 生成风格消息与校验器 ----

type item struct {
	Name  string
	Count int32
}

type payload struct {
	ID    string
	Name  string
	Email string
	Age   int64
	Score float64
	Kind  int32
	Tags  []string
	Attrs map[string]string
	Item  *item
	Blob  []byte
	Opt   *int32
	Mode  int32
	Text  string
	Num   int32
}

func emailOK(s string) bool {
	if len(s) > 64 || strings.ContainsAny(s, " \t\r\n") {
		return false
	}
	at := strings.IndexByte(s, '@')
	if at <= 0 || at != strings.LastIndexByte(s, '@') || at == len(s)-1 {
		return false
	}
	dot := strings.IndexByte(s[at+1:], '.')
	return dot > 0 && at+1+dot < len(s)-1
}

func (m *payload) Validate() []string {
	if m == nil {
		return []string{"message: nil"}
	}
	var errs []string
	if m.ID == "" {
		errs = append(errs, "id: required")
	} else if len(m.ID) < 3 || len(m.ID) > 16 {
		errs = append(errs, "id: length must be in [3,16]")
	} else if !strings.HasPrefix(m.ID, "id-") {
		errs = append(errs, "id: prefix must be id-")
	}

	if strings.TrimSpace(m.Name) == "" {
		errs = append(errs, "name: required")
	} else if len(m.Name) > 32 {
		errs = append(errs, "name: length must be <= 32")
	}

	if m.Email != "" && !emailOK(m.Email) {
		errs = append(errs, "email: invalid")
	}

	if m.Age < 0 || m.Age > 150 {
		errs = append(errs, "age: must be in [0,150]")
	}

	if math.IsNaN(m.Score) || math.IsInf(m.Score, 0) {
		errs = append(errs, "score: must be finite")
	} else if m.Score < -1 || m.Score > 1 {
		errs = append(errs, "score: must be in [-1,1]")
	}

	switch m.Kind {
	case 1, 2, 4, 8:
	default:
		errs = append(errs, "kind: unknown value")
	}

	if len(m.Tags) < 1 || len(m.Tags) > 4 {
		errs = append(errs, "tags: must have 1..4 items")
	}
	for i, t := range m.Tags {
		if t == "" {
			errs = append(errs, fmt.Sprintf("tags[%d]: empty", i))
		}
	}

	if len(m.Attrs) > 3 {
		errs = append(errs, "attrs: must have at most 3 entries")
	}
	keys := make([]string, 0, len(m.Attrs))
	for k := range m.Attrs {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if k == "" {
			errs = append(errs, "attrs[]: empty key")
		}
		if m.Attrs[k] == "" {
			errs = append(errs, fmt.Sprintf("attrs[%s]: empty value", k))
		}
	}

	if m.Item != nil {
		if strings.TrimSpace(m.Item.Name) == "" {
			errs = append(errs, "item.name: required")
		}
		if m.Item.Count < 0 {
			errs = append(errs, "item.count: must be >= 0")
		}
	}

	if len(m.Blob) > 8 {
		errs = append(errs, "blob: must be at most 8 bytes")
	}

	if m.Opt != nil && *m.Opt < 0 {
		errs = append(errs, "opt: must be >= 0")
	}

	switch m.Mode {
	case 0, 1, 2:
	default:
		errs = append(errs, "mode: unknown value")
	}

	if m.Mode == 1 && m.Text == "" {
		errs = append(errs, "text: required when mode=text")
	} else if len(m.Text) > 16 {
		errs = append(errs, "text: length must be <= 16")
	}
	if m.Mode == 2 && (m.Num < 0 || m.Num > 100) {
		errs = append(errs, "num: must be in [0,100] when mode=num")
	}
	return errs
}

// 独立模型：逐字段用不同的代码组织方式重写规则，用于随机对账。
func modelValidate(m *payload) []string {
	if m == nil {
		return []string{"message: nil"}
	}
	out := make([]string, 0, 8)
	add := func(s string) { out = append(out, s) }

	if len(m.ID) == 0 {
		add("id: required")
	} else {
		switch {
		case len(m.ID) < 3:
			add("id: length must be in [3,16]")
		case len(m.ID) > 16:
			add("id: length must be in [3,16]")
		case m.ID[:3] != "id-":
			add("id: prefix must be id-")
		}
	}

	name := strings.TrimSpace(m.Name)
	if name == "" {
		add("name: required")
	} else if len(m.Name) > 32 {
		add("name: length must be <= 32")
	}

	if len(m.Email) > 0 {
		ok := len(m.Email) <= 64 && !strings.ContainsAny(m.Email, " \t\r\n")
		parts := strings.Split(m.Email, "@")
		if len(parts) != 2 || len(parts[0]) == 0 || len(parts[1]) < 3 {
			ok = false
		} else if strings.LastIndexByte(parts[1], '.') <= 0 || strings.HasSuffix(parts[1], ".") {
			ok = false
		}
		if !ok {
			add("email: invalid")
		}
	}

	if m.Age < 0 || m.Age > 150 {
		add("age: must be in [0,150]")
	}

	if math.IsNaN(m.Score) {
		add("score: must be finite")
	} else if math.IsInf(m.Score, 0) {
		add("score: must be finite")
	} else if m.Score >= -1 && m.Score <= 1 {
		// ok
	} else {
		add("score: must be in [-1,1]")
	}

	if m.Kind != 1 && m.Kind != 2 && m.Kind != 4 && m.Kind != 8 {
		add("kind: unknown value")
	}

	if n := len(m.Tags); n == 0 || n > 4 {
		add("tags: must have 1..4 items")
	}
	for i := 0; i < len(m.Tags); i++ {
		if len(m.Tags[i]) == 0 {
			add(fmt.Sprintf("tags[%d]: empty", i))
		}
	}

	if len(m.Attrs) > 3 {
		add("attrs: must have at most 3 entries")
	}
	ks := make([]string, 0, len(m.Attrs))
	for k := range m.Attrs {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	for _, k := range ks {
		v := m.Attrs[k]
		if len(k) == 0 {
			add("attrs[]: empty key")
		}
		if len(v) == 0 {
			add(fmt.Sprintf("attrs[%s]: empty value", k))
		}
	}

	if m.Item != nil {
		if len(strings.TrimSpace(m.Item.Name)) == 0 {
			add("item.name: required")
		}
		if m.Item.Count < 0 {
			add("item.count: must be >= 0")
		}
	}

	if len(m.Blob) > 8 {
		add("blob: must be at most 8 bytes")
	}

	if m.Opt != nil {
		if *m.Opt < 0 {
			add("opt: must be >= 0")
		}
	}

	if m.Mode < 0 || m.Mode > 2 {
		add("mode: unknown value")
	}
	if m.Mode == 1 && len(m.Text) == 0 {
		add("text: required when mode=text")
	} else if len(m.Text) > 16 {
		add("text: length must be <= 16")
	}
	if m.Mode == 2 {
		if m.Num < 0 || m.Num > 100 {
			add("num: must be in [0,100] when mode=num")
		}
	}
	return out
}

func dump(m *payload) string {
	if m == nil {
		return "<nil>"
	}
	s := fmt.Sprintf("id=%q name=%q email=%q age=%d score=%v kind=%d tags=%v blob=%x mode=%d text=%q num=%d",
		m.ID, m.Name, m.Email, m.Age, m.Score, m.Kind, m.Tags, m.Blob, m.Mode, m.Text, m.Num)
	if m.Opt == nil {
		s += " opt=<nil>"
	} else {
		s += fmt.Sprintf(" opt=%d", *m.Opt)
	}
	if m.Item == nil {
		s += " item=<nil>"
	} else {
		s += fmt.Sprintf(" item=%q/%d", m.Item.Name, m.Item.Count)
	}
	keys := make([]string, 0, len(m.Attrs))
	for k := range m.Attrs {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		s += " " + k + "=" + m.Attrs[k]
	}
	return s
}

func validPayload() *payload {
	opt := int32(0)
	return &payload{
		ID: "id-123", Name: "Alice", Email: "a@b.co", Age: 30, Score: 0.5, Kind: 4,
		Tags:  []string{"x", "y"},
		Attrs: map[string]string{"a": "1"},
		Item:  &item{Name: "i", Count: 0},
		Blob:  []byte{1, 2},
		Opt:   &opt,
		Mode:  1,
		Text:  "hello",
		Num:   0,
	}
}

func int32p(v int32) *int32 { return &v }

func expectErrs(m *payload, want ...string) {
	got := m.Validate()
	if strings.Join(got, "|") != strings.Join(want, "|") {
		ck(false, fmt.Sprintf("validation mismatch got=%v want=%v", got, want))
	}
}

func randPayload(r *rand.Rand) *payload {
	m := &payload{}
	ids := []string{"", "ab", "id-1", "xx-123", "id-1234567890123456", "id-ok"}
	m.ID = ids[r.Intn(len(ids))]
	names := []string{"", "   ", "Bob", strings.Repeat("n", 40)}
	m.Name = names[r.Intn(len(names))]
	emails := []string{"", "a@b.co", "a@b", "a b@c.de", "a@@b.co", "x@y.z"}
	m.Email = emails[r.Intn(len(emails))]
	m.Age = int64(r.Intn(200) - 20)
	scores := []float64{0, 0.5, -1, 1, 1.5, -1.5, math.NaN(), math.Inf(1), math.Inf(-1)}
	m.Score = scores[r.Intn(len(scores))]
	kinds := []int32{0, 1, 2, 3, 4, 8, 9}
	m.Kind = kinds[r.Intn(len(kinds))]
	tagPool := []string{"", "a", "bb", "c"}
	tn := r.Intn(6)
	if r.Intn(4) != 0 {
		m.Tags = make([]string, tn)
		for i := range m.Tags {
			m.Tags[i] = tagPool[r.Intn(len(tagPool))]
		}
	}
	if r.Intn(3) != 0 {
		m.Attrs = make(map[string]string)
		an := r.Intn(5)
		keys := []string{"", "a", "b", "z", "k"}
		vals := []string{"", "1", "v"}
		for i := 0; i < an; i++ {
			m.Attrs[keys[r.Intn(len(keys))]] = vals[r.Intn(len(vals))]
		}
	}
	if r.Intn(3) == 0 {
		m.Item = &item{Name: []string{"", "x", " "}[r.Intn(3)], Count: int32(r.Intn(7) - 3)}
	}
	if r.Intn(2) == 0 {
		m.Blob = make([]byte, r.Intn(12))
	}
	if r.Intn(2) == 0 {
		m.Opt = int32p(int32(r.Intn(5) - 2))
	}
	m.Mode = int32(r.Intn(5) - 1)
	texts := []string{"", "ok", strings.Repeat("t", 20)}
	m.Text = texts[r.Intn(len(texts))]
	m.Num = int32(r.Intn(160) - 30)
	return m
}

func main() {
	curSeed = 1
	if s := os.Getenv("UAF_SEED"); s != "" {
		if v, err := strconv.ParseInt(s, 10, 64); err == nil {
			curSeed = v
		}
	}

	// 1. 合法消息与 nil receiver。
	good := validPayload()
	ck(len(good.Validate()) == 0, "valid message has no errors")
	ck(len(modelValidate(good)) == 0, "model accepts valid message")
	var nilMsg *payload
	expectErrs(nilMsg, "message: nil")

	// 2. 单字段错误：错误文本与短路规则。
	expectErrs(func() *payload { m := validPayload(); m.ID = ""; return m }(), "id: required")
	expectErrs(func() *payload { m := validPayload(); m.ID = "ab"; return m }(), "id: length must be in [3,16]")
	expectErrs(func() *payload { m := validPayload(); m.ID = strings.Repeat("x", 17); return m }(), "id: length must be in [3,16]")
	expectErrs(func() *payload { m := validPayload(); m.ID = "xx-123"; return m }(), "id: prefix must be id-")
	expectErrs(func() *payload { m := validPayload(); m.Name = "   "; return m }(), "name: required")
	expectErrs(func() *payload { m := validPayload(); m.Name = strings.Repeat("n", 33); return m }(), "name: length must be <= 32")
	expectErrs(func() *payload { m := validPayload(); m.Email = "a@b"; return m }(), "email: invalid")
	expectErrs(func() *payload { m := validPayload(); m.Email = "a b@c.de"; return m }(), "email: invalid")
	expectErrs(func() *payload { m := validPayload(); m.Email = "a@@b.co"; return m }(), "email: invalid")
	expectErrs(func() *payload { m := validPayload(); m.Age = -1; return m }(), "age: must be in [0,150]")
	expectErrs(func() *payload { m := validPayload(); m.Age = 151; return m }(), "age: must be in [0,150]")
	expectErrs(func() *payload { m := validPayload(); m.Score = math.NaN(); return m }(), "score: must be finite")
	expectErrs(func() *payload { m := validPayload(); m.Score = math.Inf(-1); return m }(), "score: must be finite")
	expectErrs(func() *payload { m := validPayload(); m.Score = -1.25; return m }(), "score: must be in [-1,1]")
	expectErrs(func() *payload { m := validPayload(); m.Kind = 3; return m }(), "kind: unknown value")
	expectErrs(func() *payload { m := validPayload(); m.Tags = nil; return m }(), "tags: must have 1..4 items")
	expectErrs(func() *payload { m := validPayload(); m.Tags = []string{"a", "b", "c", "d", "e"}; return m }(), "tags: must have 1..4 items")
	expectErrs(func() *payload { m := validPayload(); m.Tags = []string{"a", "", "c"}; return m }(), "tags[1]: empty")
	expectErrs(func() *payload {
		m := validPayload()
		m.Attrs = map[string]string{"a": "1", "b": "2", "c": "3", "d": "4"}
		return m
	}(), "attrs: must have at most 3 entries")
	expectErrs(func() *payload { m := validPayload(); m.Attrs = map[string]string{"": "x"}; return m }(), "attrs[]: empty key")
	expectErrs(func() *payload { m := validPayload(); m.Attrs = map[string]string{"a": ""}; return m }(), "attrs[a]: empty value")
	expectErrs(func() *payload { m := validPayload(); m.Item = &item{Name: "", Count: -1}; return m }(),
		"item.name: required", "item.count: must be >= 0")
	expectErrs(func() *payload { m := validPayload(); m.Blob = make([]byte, 9); return m }(), "blob: must be at most 8 bytes")
	expectErrs(func() *payload { m := validPayload(); m.Opt = int32p(-1); return m }(), "opt: must be >= 0")
	if len(func() []string { m := validPayload(); m.Opt = int32p(0); return m.Validate() }()) != 0 {
		ck(false, "explicit zero optional is valid")
	}
	expectErrs(func() *payload { m := validPayload(); m.Mode = 3; return m }(), "mode: unknown value")
	expectErrs(func() *payload { m := validPayload(); m.Mode = 1; m.Text = ""; return m }(), "text: required when mode=text")
	expectErrs(func() *payload { m := validPayload(); m.Text = strings.Repeat("t", 17); return m }(), "text: length must be <= 16")
	expectErrs(func() *payload { m := validPayload(); m.Mode = 2; m.Num = -1; return m }(), "num: must be in [0,100] when mode=num")
	expectErrs(func() *payload { m := validPayload(); m.Mode = 2; m.Num = 101; return m }(), "num: must be in [0,100] when mode=num")
	if errs := func() []string { m := validPayload(); m.Mode = 0; m.Num = 999; return m.Validate() }(); len(errs) != 0 {
		ck(false, "mode=0 ignores num range")
	}

	// 3. 多错误顺序按字段声明序；map 内按 key 排序。
	multi := &payload{
		ID: "", Name: "Bob", Age: 200, Kind: 9, Tags: []string{"a", ""},
		Attrs: map[string]string{"z": "", "a": "", "m": "ok"},
		Mode:  2, Num: -5,
	}
	want := []string{
		"id: required",
		"age: must be in [0,150]",
		"kind: unknown value",
		"tags[1]: empty",
		"attrs[a]: empty value",
		"attrs[z]: empty value",
		"num: must be in [0,100] when mode=num",
	}
	got := multi.Validate()
	if strings.Join(got, "|") != strings.Join(want, "|") {
		ck(false, fmt.Sprintf("multi-error order got=%v want=%v", got, want))
	}
	if strings.Join(modelValidate(multi), "|") != strings.Join(got, "|") {
		ck(false, "model multi-error order mismatch")
	}

	// 4. 校验不修改消息。
	before := dump(multi)
	_ = multi.Validate()
	ck(dump(multi) == before, "Validate does not mutate message")
	gm := validPayload()
	gb := dump(gm)
	_ = gm.Validate()
	ck(dump(gm) == gb, "Validate does not mutate valid message")

	// 5. GC：错误串、合法消息与字段视图在 GC 后一致。
	errsKeep := multi.Validate()
	optView := good.Opt
	itemView := good.Item
	gcSink = append(gcSink, []byte(errsKeep[0]), good.Blob)
	gcStress()
	ck(strings.Join(multi.Validate(), "|") == strings.Join(errsKeep, "|"), "errors survive GC")
	ck(len(good.Validate()) == 0, "valid message still valid after GC")
	ck(optView != nil && *optView == 0 && itemView.Name == "i", "field views survive GC")
	ck(dump(multi) == before, "message content survives GC")

	// 6. UAF_SEED 随机消息：与独立模型逐字符串对账。
	r := rand.New(rand.NewSource(curSeed))
	zeros, nonzeros := 0, 0
	for i := 0; i < 500; i++ {
		var m *payload
		switch {
		case i%10 == 0:
			m = validPayload()
		case i%11 == 1:
			m = &payload{}
		default:
			m = randPayload(r)
		}
		got := m.Validate()
		want := modelValidate(m)
		ckSeed(strings.Join(got, "|") == strings.Join(want, "|"),
			fmt.Sprintf("round %d got=%v want=%v", i, got, want))
		if len(got) == 0 {
			zeros++
		} else {
			nonzeros++
		}
		if i%37 == 0 {
			snap := dump(m)
			gcSink = append(gcSink, []byte(strings.Join(got, "|")))
			gcStress()
			ckSeed(strings.Join(m.Validate(), "|") == strings.Join(got, "|"),
				fmt.Sprintf("round %d validation after GC", i))
			ckSeed(dump(m) == snap, fmt.Sprintf("round %d message after GC", i))
		}
	}
	ck(zeros > 0 && nonzeros > 0, "random corpus covers valid and invalid messages")

	finish("l10_field_validation")
}
