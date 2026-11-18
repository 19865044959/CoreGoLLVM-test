// l10_unknown_json_preserve: generated-style JSON codec that can preserve
// unrecognized object fields (top-level and nested), canonicalize them, and
// re-emit them deterministically.  Three policies: strict, discard, preserve.
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
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
		b[len(b)-1] = byte(i * 7)
		gcSink = append(gcSink, b)
	}
	// Drop the sink so the garbage really becomes collectable.
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

type policy int

const (
	strictPolicy policy = iota
	discardPolicy
	preservePolicy
)

type child struct {
	id      int64
	note    string
	hasNote bool
	unknown map[string]json.RawMessage
}

type message struct {
	id        int64
	name      string
	tags      []string
	scores    map[string]int32
	child     *child
	hasName   bool
	hasTags   bool
	hasScores bool
	hasChild  bool
	unknown   map[string]json.RawMessage
}

// ---------- canonical unknown-value rendering ----------

func canonicalize(raw []byte) ([]byte, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, err
	}
	if dec.More() {
		return nil, fmt.Errorf("trailing data")
	}
	var buf bytes.Buffer
	if err := writeCanonical(&buf, v); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func writeCanonical(buf *bytes.Buffer, v any) error {
	switch x := v.(type) {
	case nil:
		buf.WriteString("null")
	case bool:
		if x {
			buf.WriteString("true")
		} else {
			buf.WriteString("false")
		}
	case string:
		b, err := json.Marshal(x)
		if err != nil {
			return err
		}
		buf.Write(b)
	case json.Number:
		buf.WriteString(x.String())
	case []any:
		buf.WriteByte('[')
		for i, e := range x {
			if i > 0 {
				buf.WriteByte(',')
			}
			if err := writeCanonical(buf, e); err != nil {
				return err
			}
		}
		buf.WriteByte(']')
	case map[string]any:
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		buf.WriteByte('{')
		for i, k := range keys {
			if i > 0 {
				buf.WriteByte(',')
			}
			kb, err := json.Marshal(k)
			if err != nil {
				return err
			}
			buf.Write(kb)
			buf.WriteByte(':')
			if err := writeCanonical(buf, x[k]); err != nil {
				return err
			}
		}
		buf.WriteByte('}')
	default:
		return fmt.Errorf("unsupported canonical type %T", v)
	}
	return nil
}

// ---------- decode ----------

func sortedKeys[T any](m map[string]T) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func parseRawObject(raw []byte) (map[string]json.RawMessage, error) {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil {
		return nil, err
	}
	if obj == nil {
		return nil, fmt.Errorf("expected JSON object")
	}
	return obj, nil
}

func decodeChild(raw []byte, p policy) (*child, error) {
	obj, err := parseRawObject(raw)
	if err != nil {
		return nil, fmt.Errorf("child: %w", err)
	}
	c := &child{unknown: map[string]json.RawMessage{}}
	for _, k := range sortedKeys(obj) {
		v := obj[k]
		switch k {
		case "id":
			if err := json.Unmarshal(v, &c.id); err != nil {
				return nil, fmt.Errorf("child.id: %w", err)
			}
		case "note":
			if err := json.Unmarshal(v, &c.note); err != nil {
				return nil, fmt.Errorf("child.note: %w", err)
			}
			c.hasNote = true
		default:
			if k == "" {
				return nil, fmt.Errorf("child: empty field name")
			}
			if p == strictPolicy {
				return nil, fmt.Errorf("child: unknown field %q", k)
			}
			if p == discardPolicy {
				continue
			}
			canon, err := canonicalize(v)
			if err != nil {
				return nil, fmt.Errorf("child.%s: %w", k, err)
			}
			c.unknown[k] = canon
		}
	}
	if len(c.unknown) == 0 {
		c.unknown = nil
	}
	return c, nil
}

func decodeTags(raw []byte) ([]string, error) {
	if string(raw) == "null" {
		return nil, nil
	}
	var elems []json.RawMessage
	if err := json.Unmarshal(raw, &elems); err != nil {
		return nil, err
	}
	out := make([]string, len(elems))
	for i, e := range elems {
		if string(e) == "null" {
			return nil, fmt.Errorf("tags[%d]: null element", i)
		}
		if err := json.Unmarshal(e, &out[i]); err != nil {
			return nil, fmt.Errorf("tags[%d]: %w", i, err)
		}
	}
	return out, nil
}

func decodeScores(raw []byte) (map[string]int32, error) {
	if string(raw) == "null" {
		return nil, nil
	}
	obj, err := parseRawObject(raw)
	if err != nil {
		return nil, err
	}
	out := make(map[string]int32, len(obj))
	for _, k := range sortedKeys(obj) {
		v := obj[k]
		if string(v) == "null" {
			return nil, fmt.Errorf("scores[%q]: null value", k)
		}
		var n int32
		if err := json.Unmarshal(v, &n); err != nil {
			return nil, fmt.Errorf("scores[%q]: %w", k, err)
		}
		out[k] = n
	}
	return out, nil
}

func decodeMessage(data []byte, p policy) (*message, error) {
	obj, err := parseRawObject(data)
	if err != nil {
		return nil, err
	}
	m := &message{unknown: map[string]json.RawMessage{}}
	for _, k := range sortedKeys(obj) {
		v := obj[k]
		switch k {
		case "id":
			if err := json.Unmarshal(v, &m.id); err != nil {
				return nil, fmt.Errorf("id: %w", err)
			}
		case "name":
			if err := json.Unmarshal(v, &m.name); err != nil {
				return nil, fmt.Errorf("name: %w", err)
			}
			m.hasName = true
		case "tags":
			tags, err := decodeTags(v)
			if err != nil {
				return nil, fmt.Errorf("tags: %w", err)
			}
			m.tags = tags
			m.hasTags = true
		case "scores":
			scores, err := decodeScores(v)
			if err != nil {
				return nil, fmt.Errorf("scores: %w", err)
			}
			m.scores = scores
			m.hasScores = true
		case "child":
			if string(v) == "null" {
				m.child = nil
				m.hasChild = true
				continue
			}
			c, err := decodeChild(v, p)
			if err != nil {
				return nil, err
			}
			m.child = c
			m.hasChild = true
		default:
			if k == "" {
				return nil, fmt.Errorf("empty field name")
			}
			if p == strictPolicy {
				return nil, fmt.Errorf("unknown field %q", k)
			}
			if p == discardPolicy {
				continue
			}
			canon, err := canonicalize(v)
			if err != nil {
				return nil, fmt.Errorf("unknown field %q: %w", k, err)
			}
			m.unknown[k] = canon
		}
	}
	if len(m.unknown) == 0 {
		m.unknown = nil
	}
	return m, nil
}

// ---------- encode ----------

func quoted(s string) []byte {
	b, _ := json.Marshal(s)
	return b
}

func qstr(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

func appendField(buf *bytes.Buffer, first *bool, name string, raw []byte) {
	if !*first {
		buf.WriteByte(',')
	}
	*first = false
	buf.Write(quoted(name))
	buf.WriteByte(':')
	buf.Write(raw)
}

func encodeChild(c *child) []byte {
	var buf bytes.Buffer
	buf.WriteByte('{')
	first := true
	appendField(&buf, &first, "id", []byte(strconv.FormatInt(c.id, 10)))
	if c.hasNote {
		appendField(&buf, &first, "note", quoted(c.note))
	}
	for _, k := range sortedKeys(c.unknown) {
		appendField(&buf, &first, k, c.unknown[k])
	}
	buf.WriteByte('}')
	return buf.Bytes()
}

func encodeMessage(m *message) ([]byte, error) {
	var buf bytes.Buffer
	buf.WriteByte('{')
	first := true
	appendField(&buf, &first, "id", []byte(strconv.FormatInt(m.id, 10)))
	if m.hasName {
		appendField(&buf, &first, "name", quoted(m.name))
	}
	if m.hasTags {
		var raw []byte
		if m.tags == nil {
			raw = []byte("null")
		} else {
			var b bytes.Buffer
			b.WriteByte('[')
			for i, t := range m.tags {
				if i > 0 {
					b.WriteByte(',')
				}
				b.Write(quoted(t))
			}
			b.WriteByte(']')
			raw = b.Bytes()
		}
		appendField(&buf, &first, "tags", raw)
	}
	if m.hasScores {
		var raw []byte
		if m.scores == nil {
			raw = []byte("null")
		} else {
			var b bytes.Buffer
			b.WriteByte('{')
			for i, k := range sortedKeys(m.scores) {
				if i > 0 {
					b.WriteByte(',')
				}
				b.Write(quoted(k))
				b.WriteByte(':')
				b.WriteString(strconv.FormatInt(int64(m.scores[k]), 10))
			}
			b.WriteByte('}')
			raw = b.Bytes()
		}
		appendField(&buf, &first, "scores", raw)
	}
	if m.hasChild {
		var raw []byte
		if m.child == nil {
			raw = []byte("null")
		} else {
			raw = encodeChild(m.child)
		}
		appendField(&buf, &first, "child", raw)
	}
	for _, k := range sortedKeys(m.unknown) {
		appendField(&buf, &first, k, m.unknown[k])
	}
	buf.WriteByte('}')
	return buf.Bytes(), nil
}

// ---------- merge/reset ----------

func cloneRawMap(src map[string]json.RawMessage) map[string]json.RawMessage {
	if src == nil {
		return nil
	}
	dst := make(map[string]json.RawMessage, len(src))
	for k, v := range src {
		dst[k] = append([]byte(nil), v...)
	}
	return dst
}

func cloneChild(c *child) *child {
	if c == nil {
		return nil
	}
	return &child{id: c.id, note: c.note, hasNote: c.hasNote, unknown: cloneRawMap(c.unknown)}
}

func cloneMessage(m *message) *message {
	if m == nil {
		return nil
	}
	out := &message{
		id:        m.id,
		name:      m.name,
		hasName:   m.hasName,
		hasTags:   m.hasTags,
		hasScores: m.hasScores,
		hasChild:  m.hasChild,
		child:     cloneChild(m.child),
		unknown:   cloneRawMap(m.unknown),
	}
	if m.tags != nil {
		out.tags = append([]string(nil), m.tags...)
	}
	if m.scores != nil {
		out.scores = make(map[string]int32, len(m.scores))
		for k, v := range m.scores {
			out.scores[k] = v
		}
	}
	return out
}

func mergeMessage(dst, src *message) {
	if src.hasName {
		dst.name = src.name
		dst.hasName = true
	}
	if src.hasTags {
		dst.tags = nil
		if src.tags != nil {
			dst.tags = append([]string(nil), src.tags...)
		}
		dst.hasTags = true
	}
	if src.hasScores {
		dst.scores = nil
		if src.scores != nil {
			dst.scores = make(map[string]int32, len(src.scores))
			for k, v := range src.scores {
				dst.scores[k] = v
			}
		}
		dst.hasScores = true
	}
	if src.hasChild {
		dst.child = cloneChild(src.child)
		dst.hasChild = true
	}
	if len(src.unknown) > 0 {
		if dst.unknown == nil {
			dst.unknown = map[string]json.RawMessage{}
		}
		for k, v := range src.unknown {
			dst.unknown[k] = append([]byte(nil), v...)
		}
	}
}

func resetMessage(m *message) {
	m.id = 0
	m.name = ""
	m.tags = nil
	m.scores = nil
	m.child = nil
	m.hasName = false
	m.hasTags = false
	m.hasScores = false
	m.hasChild = false
	m.unknown = nil
}

// ---------- random torture ----------

func randomJSONValue(r *rand.Rand, depth int) string {
	if depth <= 0 {
		switch r.Intn(4) {
		case 0:
			return strconv.Itoa(r.Intn(200) - 100)
		case 1:
			return qstr(fmt.Sprintf("s%d", r.Intn(50)))
		case 2:
			return "true"
		default:
			return "null"
		}
	}
	switch r.Intn(4) {
	case 0:
		return "[" + randomJSONValue(r, depth-1) + "," + randomJSONValue(r, depth-1) + "]"
	case 1:
		// Emit object keys in an intentionally unsorted order.
		keys := []string{"z", "a", "m", "b"}
		r.Shuffle(len(keys), func(i, j int) { keys[i], keys[j] = keys[j], keys[i] })
		n := 1 + r.Intn(3)
		var parts []string
		for i := 0; i < n; i++ {
			parts = append(parts, qstr(keys[i])+":"+randomJSONValue(r, depth-1))
		}
		return "{" + strings.Join(parts, ",") + "}"
	default:
		return randomJSONValue(r, 0)
	}
}

func randomObject(r *rand.Rand, depth int) string {
	var parts []string
	if r.Intn(2) == 0 {
		parts = append(parts, fmt.Sprintf(`"id":%d`, r.Intn(10000)))
	}
	if r.Intn(2) == 0 {
		parts = append(parts, `"name":`+qstr(fmt.Sprintf("n%d", r.Intn(100))))
	}
	if r.Intn(2) == 0 {
		parts = append(parts, `"tags":[`+qstr("x")+`,`+qstr("y")+`]`)
	}
	if r.Intn(2) == 0 {
		parts = append(parts, `"scores":{"b":2,"a":1}`)
	}
	if depth > 0 && r.Intn(3) == 0 {
		parts = append(parts, `"child":`+randomObject(r, depth-1))
	}
	n := 1 + r.Intn(3)
	for i := 0; i < n; i++ {
		parts = append(parts, qstr(fmt.Sprintf("u%d", i))+":"+randomJSONValue(r, 2))
	}
	r.Shuffle(len(parts), func(i, j int) { parts[i], parts[j] = parts[j], parts[i] })
	return "{" + strings.Join(parts, ",") + "}"
}

func runTorture(rounds int) {
	r := rand.New(rand.NewSource(seed))
	ok := 0
	for round := 0; round < rounds; round++ {
		raw := []byte(randomObject(r, 2))
		m, err := decodeMessage(raw, preservePolicy)
		if err != nil {
			ckSeed(false, fmt.Sprintf("torture round %d decode: %v", round, err))
			continue
		}
		enc1, err := encodeMessage(m)
		if err != nil {
			ckSeed(false, fmt.Sprintf("torture round %d encode: %v", round, err))
			continue
		}
		// Poison the input and drop references; preserved raw bytes must survive.
		for i := range raw {
			raw[i] = 0xEE
		}
		if round%17 == 0 {
			gcStress()
		}
		m2, err := decodeMessage(enc1, preservePolicy)
		if err != nil {
			ckSeed(false, fmt.Sprintf("torture round %d redecode: %v", round, err))
			continue
		}
		enc2, err := encodeMessage(m2)
		if err != nil {
			ckSeed(false, fmt.Sprintf("torture round %d reencode: %v", round, err))
			continue
		}
		if !bytes.Equal(enc1, enc2) {
			ckSeed(false, fmt.Sprintf("torture round %d canonical instability", round))
			continue
		}
		if m.hasScores {
			for k, v := range m.scores {
				if m2.scores[k] != v {
					ckSeed(false, fmt.Sprintf("torture round %d score %q lost", round, k))
				}
			}
		}
		ok++
	}
	ck(ok == rounds, "torture: all rounds stable")
}

// ---------- main ----------

func main() {
	seed = readSeed()

	// 1. Canonical unknown objects: keys sorted recursively, arrays in order.
	canonIn := []byte(`{"b":[3,{"d":4,"c":5}],"a":1}`)
	canon, err := canonicalize(canonIn)
	ck(err == nil, "canonicalize nested object")
	ck(string(canon) == `{"a":1,"b":[3,{"c":5,"d":4}]}`, "canonical nested key order")

	// 2. Strict policy rejects unknown fields (top-level and nested).
	_, err = decodeMessage([]byte(`{"id":1,"zzz":2}`), strictPolicy)
	ck(err != nil && strings.Contains(err.Error(), `unknown field "zzz"`), "strict rejects top-level unknown")
	_, err = decodeMessage([]byte(`{"id":1,"child":{"id":2,"nested_unknown":true}}`), strictPolicy)
	ck(err != nil && strings.Contains(err.Error(), `unknown field "nested_unknown"`), "strict rejects nested unknown")

	// 3. Discard policy drops unknown fields.
	mDiscard, err := decodeMessage([]byte(`{"id":1,"zzz":2,"child":{"id":2,"q":{"k":1}}}`), discardPolicy)
	ck(err == nil, "discard decodes")
	if err == nil {
		enc, _ := encodeMessage(mDiscard)
		ck(string(enc) == `{"id":1,"child":{"id":2}}`, "discard drops unknowns")
	}

	// 4. Preserve policy: golden canonical output.
	input := []byte(`{"id":7,"name":"a","tags":["x","y"],"scores":{"b":2,"a":1},"child":{"id":1,"note":"n","zz":{"q":1,"p":2}},"u2":{"b":[3,{"d":4,"c":5}],"a":1},"u1":null}`)
	m, err := decodeMessage(input, preservePolicy)
	ck(err == nil, "preserve decodes")
	golden := `{"id":7,"name":"a","tags":["x","y"],"scores":{"a":1,"b":2},"child":{"id":1,"note":"n","zz":{"p":2,"q":1}},"u1":null,"u2":{"a":1,"b":[3,{"c":5,"d":4}]}}`
	if err == nil {
		enc, e := encodeMessage(m)
		ck(e == nil && string(enc) == golden, "preserve golden re-encode")
		// Unknown payload must be independent of the input buffer.
		for i := range input {
			input[i] = 0x5A
		}
		gcStress()
		enc2, e := encodeMessage(m)
		ck(e == nil && string(enc2) == golden, "preserved unknown survives input poison + GC")
	}

	// 5. Duplicate unknown keys: last one wins, then canonicalized.
	mDup, err := decodeMessage([]byte(`{"id":1,"u":{"b":1,"a":2},"u":{"z":3,"c":4}}`), preservePolicy)
	ck(err == nil, "duplicate unknown decodes")
	if err == nil {
		enc, _ := encodeMessage(mDup)
		ck(string(enc) == `{"id":1,"u":{"c":4,"z":3}}`, "duplicate unknown last-wins canonical")
	}

	// 6. Duplicate known keys: last one wins (standard JSON object semantics).
	mKnownDup, err := decodeMessage([]byte(`{"id":1,"name":"first","name":"last"}`), preservePolicy)
	ck(err == nil && mKnownDup.name == "last", "duplicate known key last wins")

	// 7. null vs absent vs empty containers.
	mAbsent, err := decodeMessage([]byte(`{"id":1}`), preservePolicy)
	ck(err == nil, "absent containers decode")
	if err == nil {
		enc, _ := encodeMessage(mAbsent)
		ck(string(enc) == `{"id":1}`, "absent containers omitted")
	}
	mNull, err := decodeMessage([]byte(`{"id":1,"tags":null,"scores":null,"child":null}`), preservePolicy)
	ck(err == nil && mNull.hasTags && mNull.tags == nil && mNull.hasScores && mNull.scores == nil && mNull.hasChild && mNull.child == nil, "null containers preserved as present-nil")
	if err == nil {
		enc, _ := encodeMessage(mNull)
		ck(string(enc) == `{"id":1,"tags":null,"scores":null,"child":null}`, "null containers re-encoded as null")
	}
	mEmpty, err := decodeMessage([]byte(`{"id":1,"tags":[],"scores":{},"child":{"id":0}}`), preservePolicy)
	ck(err == nil && mEmpty.tags != nil && len(mEmpty.tags) == 0 && mEmpty.scores != nil && len(mEmpty.scores) == 0, "empty containers preserved non-nil")
	if err == nil {
		enc, _ := encodeMessage(mEmpty)
		ck(string(enc) == `{"id":1,"tags":[],"scores":{},"child":{"id":0}}`, "empty containers re-encoded")
	}

	// 8. Explicit zero scalar stays present and distinct from absent.
	mZero, err := decodeMessage([]byte(`{"id":0,"name":""}`), preservePolicy)
	ck(err == nil && mZero.hasName && mZero.name == "" && mZero.id == 0, "explicit zero preserved")
	if err == nil {
		enc, _ := encodeMessage(mZero)
		ck(string(enc) == `{"id":0,"name":""}`, "explicit zero re-encoded")
	}

	// 9. Merge and reset.
	base := &message{id: 1, name: "base", hasName: true, tags: []string{"t"}, hasTags: true, unknown: map[string]json.RawMessage{"a": []byte(`1`)}}
	src := &message{name: "src", hasName: true, scores: map[string]int32{"k": 9}, hasScores: true, unknown: map[string]json.RawMessage{"a": []byte(`2`), "b": []byte(`{"x":2,"y":1}`)}}
	mergeMessage(base, src)
	ck(base.name == "src" && base.tags != nil && len(base.tags) == 1 && base.scores["k"] == 9, "merge replaces present fields")
	ck(string(base.unknown["a"]) == "2" && string(base.unknown["b"]) == `{"x":2,"y":1}`, "merge unknown override")
	// Mutating the source afterwards must not affect the destination.
	src.name = "mut"
	src.scores["k"] = 100
	src.unknown["b"][0] = 'X'
	ck(base.name == "src" && base.scores["k"] == 9 && string(base.unknown["b"]) == `{"x":2,"y":1}`, "merge copies payloads")
	resetMessage(base)
	ck(base.id == 0 && !base.hasName && !base.hasTags && !base.hasScores && base.unknown == nil && base.name == "", "reset clears everything")
	gcStress()
	ck(base.unknown == nil, "reset object stable after GC")

	// 10. Invalid inputs.
	badCases := []string{
		`{"id":1,"u":`,
		`{"id":1,"u":{bad}}`,
		`{"id":1,"":1}`,
		`{"id":1,"scores":{"x":null}}`,
		`{"id":1,"scores":{"x":9999999999}}`,
		`{"id":1,"tags":[null]}`,
		`{"id":1,"tags":"no"}`,
		`{"id":1} trailing`,
		`[]`,
		`null`,
	}
	for i, b := range badCases {
		_, err := decodeMessage([]byte(b), preservePolicy)
		ck(err != nil, fmt.Sprintf("bad input %d rejected", i))
	}

	// 11. GC with many preserved unknowns.
	var keep []*message
	for i := 0; i < 200; i++ {
		raw := []byte(fmt.Sprintf(`{"id":%d,"u":{"k%d":%d,"a":{"z":1,"b":2}}}`, i, i, i*3))
		mm, err := decodeMessage(raw, preservePolicy)
		ck(err == nil, "bulk decode")
		keep = append(keep, mm)
	}
	gcStress()
	for i, mm := range keep {
		enc, _ := encodeMessage(mm)
		want := fmt.Sprintf(`{"id":%d,"u":{"a":{"b":2,"z":1},"k%d":%d}}`, i, i, i*3)
		ck(string(enc) == want, fmt.Sprintf("bulk preserved %d after GC", i))
	}

	// 12. Seeded torture (P2): random unsorted objects survive canonical roundtrips.
	rounds := envInt("TORTURE_ROUNDS", 250)
	runTorture(rounds)
	gcStress()

	finish("l10_unknown_json_preserve")
}
