// l10_enum_alias_wire: generated enum tables (aliases, negative/sparse values)
// combined with wire and JSON codecs that accept unknown enum numbers and keep
// them intact (open-enum semantics), plus deterministic unknown-value maps.
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
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
		b[len(b)-1] = byte(i * 5)
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

type color int32

const (
	colorUnspecified color = 0
	colorRed         color = 1
	colorCrimson     color = 1 // alias of RED
	colorGreen       color = 2
	colorBlue        color = 3
	colorMagenta     color = 7
)

type level int32

const (
	levelMin  level = math.MinInt32
	levelLow2 level = -2
	levelLow  level = -1
	levelNone level = 0
	levelHigh level = 10
	levelMax  level = math.MaxInt32
)

type enumDecl struct {
	v    int32
	name string
}

var colorDecl = []enumDecl{
	{int32(colorUnspecified), "COLOR_UNSPECIFIED"},
	{int32(colorRed), "COLOR_RED"},
	{int32(colorCrimson), "COLOR_CRIMSON"},
	{int32(colorGreen), "COLOR_GREEN"},
	{int32(colorBlue), "COLOR_BLUE"},
	{int32(colorMagenta), "COLOR_MAGENTA"},
}

var levelDecl = []enumDecl{
	{int32(levelMin), "LEVEL_MIN"},
	{int32(levelLow2), "LEVEL_MINUS_TWO"},
	{int32(levelLow), "LEVEL_MINUS_ONE"},
	{int32(levelNone), "LEVEL_NONE"},
	{int32(levelHigh), "LEVEL_HIGH"},
	{int32(levelMax), "LEVEL_MAX"},
}

type enumTable struct {
	canonical map[int32]string
	byName    map[string]int32
	valid     map[int32]struct{}
	unique    []int32
	aliases   int
}

func buildEnumTable(decl []enumDecl) *enumTable {
	t := &enumTable{
		canonical: map[int32]string{},
		byName:    map[string]int32{},
		valid:     map[int32]struct{}{},
	}
	for _, d := range decl {
		if _, ok := t.canonical[d.v]; !ok {
			t.canonical[d.v] = d.name
		} else {
			t.aliases++
		}
		t.byName[d.name] = d.v
		t.valid[d.v] = struct{}{}
	}
	for v := range t.valid {
		t.unique = append(t.unique, v)
	}
	sort.Slice(t.unique, func(i, j int) bool { return t.unique[i] < t.unique[j] })
	return t
}

func (t *enumTable) name(v int32) string {
	if s, ok := t.canonical[v]; ok {
		return s
	}
	return fmt.Sprintf("UNKNOWN(%d)", v)
}

func (t *enumTable) isKnown(v int32) bool {
	_, ok := t.valid[v]
	return ok
}

// ---------- JSON codec ----------

func marshalEnumJSON(t *enumTable, v int32) []byte {
	if s, ok := t.canonical[v]; ok {
		b, _ := json.Marshal(s)
		return b
	}
	return []byte(strconv.FormatInt(int64(v), 10))
}

func unmarshalEnumJSON(t *enumTable, raw []byte) (int32, error) {
	s := strings.TrimSpace(string(raw))
	if s == "" || s == "null" {
		return 0, fmt.Errorf("enum: null or empty")
	}
	if s[0] == '"' {
		var name string
		if err := json.Unmarshal(raw, &name); err != nil {
			return 0, fmt.Errorf("enum: %w", err)
		}
		v, ok := t.byName[name]
		if !ok {
			return 0, fmt.Errorf("enum: unknown name %q", name)
		}
		return v, nil
	}
	var n int64
	if err := json.Unmarshal(raw, &n); err != nil {
		return 0, fmt.Errorf("enum: %w", err)
	}
	if n < math.MinInt32 || n > math.MaxInt32 {
		return 0, fmt.Errorf("enum: number out of int32 range")
	}
	return int32(n), nil
}

// ---------- wire codec ----------

type enumMsg struct {
	c     color
	l     level
	flags []color
}

func putUvarint(b []byte, v uint64) []byte {
	for v >= 0x80 {
		b = append(b, byte(v)|0x80)
		v >>= 7
	}
	return append(b, byte(v))
}

func readUvarint(b []byte) (uint64, int, error) {
	var v uint64
	for i := 0; i < 10; i++ {
		if i >= len(b) {
			return 0, 0, fmt.Errorf("truncated varint")
		}
		c := b[i]
		if i == 9 && c > 1 {
			return 0, 0, fmt.Errorf("varint overflow")
		}
		v |= uint64(c&0x7f) << (7 * uint(i))
		if c < 0x80 {
			return v, i + 1, nil
		}
	}
	return 0, 0, fmt.Errorf("varint overflow")
}

func putTag(b []byte, num int32, wt int) []byte {
	return putUvarint(b, uint64(num)<<3|uint64(wt))
}

func decodeEnumNumber(v uint64) (int32, error) {
	iv := int64(v)
	if iv < math.MinInt32 || iv > math.MaxInt32 {
		return 0, fmt.Errorf("enum value %d out of int32 range", iv)
	}
	return int32(iv), nil
}

func encodeEnumMsg(m *enumMsg) []byte {
	var b []byte
	b = putTag(b, 1, 0)
	b = putUvarint(b, uint64(int64(m.c)))
	b = putTag(b, 2, 0)
	b = putUvarint(b, uint64(int64(m.l)))
	for _, f := range m.flags {
		b = putTag(b, 3, 0)
		b = putUvarint(b, uint64(int64(f)))
	}
	return b
}

func decodeEnumMsg(data []byte) (*enumMsg, error) {
	out := &enumMsg{}
	i := 0
	for i < len(data) {
		v, n, err := readUvarint(data[i:])
		if err != nil {
			return nil, err
		}
		i += n
		num := int32(v >> 3)
		wt := int(v & 7)
		if num <= 0 {
			return nil, fmt.Errorf("invalid field number")
		}
		if num >= 1 && num <= 3 && wt != 0 {
			return nil, fmt.Errorf("field %d: unexpected wire type %d", num, wt)
		}
		switch num {
		case 1, 2, 3:
			ev, m, err := readUvarint(data[i:])
			if err != nil {
				return nil, err
			}
			i += m
			val, err := decodeEnumNumber(ev)
			if err != nil {
				return nil, err
			}
			switch num {
			case 1:
				out.c = color(val)
			case 2:
				out.l = level(val)
			case 3:
				out.flags = append(out.flags, color(val))
			}
		default:
			// Skip unknown fields by wire type.
			switch wt {
			case 0:
				_, m, err := readUvarint(data[i:])
				if err != nil {
					return nil, err
				}
				i += m
			case 1:
				if len(data)-i < 8 {
					return nil, fmt.Errorf("truncated fixed64")
				}
				i += 8
			case 2:
				l, m, err := readUvarint(data[i:])
				if err != nil {
					return nil, err
				}
				i += m
				if l > uint64(len(data)-i) {
					return nil, fmt.Errorf("truncated bytes")
				}
				i += int(l)
			case 5:
				if len(data)-i < 4 {
					return nil, fmt.Errorf("truncated fixed32")
				}
				i += 4
			default:
				return nil, fmt.Errorf("unsupported wire type %d", wt)
			}
		}
	}
	return out, nil
}

func classify(vals []color, t *enumTable) (known, unknown int, uniq []int32) {
	seen := map[int32]struct{}{}
	for _, v := range vals {
		if t.isKnown(int32(v)) {
			known++
		} else {
			unknown++
			if _, ok := seen[int32(v)]; !ok {
				seen[int32(v)] = struct{}{}
				uniq = append(uniq, int32(v))
			}
		}
	}
	sort.Slice(uniq, func(i, j int) bool { return uniq[i] < uniq[j] })
	return
}

func encodeCountMap(t *enumTable, counts map[color]int32) []byte {
	keys := make([]int32, 0, len(counts))
	for k := range counts {
		keys = append(keys, int32(k))
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
	var b bytes.Buffer
	b.WriteByte('{')
	for i, k := range keys {
		if i > 0 {
			b.WriteByte(',')
		}
		key := t.canonical[k]
		if key == "" {
			key = strconv.FormatInt(int64(k), 10)
		}
		kb, _ := json.Marshal(key)
		b.Write(kb)
		b.WriteByte(':')
		b.WriteString(strconv.FormatInt(int64(counts[color(k)]), 10))
	}
	b.WriteByte('}')
	return b.Bytes()
}

func equalEnumMsg(a, b *enumMsg) bool {
	if a.c != b.c || a.l != b.l || len(a.flags) != len(b.flags) {
		return false
	}
	for i := range a.flags {
		if a.flags[i] != b.flags[i] {
			return false
		}
	}
	return true
}

func main() {
	seed = readSeed()

	colors := buildEnumTable(colorDecl)
	levels := buildEnumTable(levelDecl)

	// 1. Alias table semantics.
	ck(colors.canonical[1] == "COLOR_RED", "first declaration wins canonical name")
	ck(colors.aliases == 1, "one alias detected")
	ck(colors.byName["COLOR_CRIMSON"] == 1, "alias name resolves to value")
	ck(len(colors.unique) == 5, "unique enum value count")
	ck(colors.unique[0] == 0 && colors.unique[len(colors.unique)-1] == 7, "unique values sorted")
	ck(levels.unique[0] == math.MinInt32 && levels.unique[len(levels.unique)-1] == math.MaxInt32, "negative/sparse enum bounds")
	ck(colors.name(1) == "COLOR_RED" && colors.name(42) == "UNKNOWN(42)", "String fallback for unknown")
	ck(levels.name(math.MinInt32) == "LEVEL_MIN", "negative enum String")

	// 2. JSON field codec: canonical names, aliases on input, raw numbers for unknown.
	jsonCases := []struct {
		t    *enumTable
		v    int32
		want string
	}{
		{colors, 1, `"COLOR_RED"`},
		{colors, 42, `42`},
		{levels, -3, `-3`},
		{levels, math.MinInt32 + 1, `-2147483647`},
	}
	for i, tc := range jsonCases {
		got := string(marshalEnumJSON(tc.t, tc.v))
		ck(got == tc.want, fmt.Sprintf("json marshal case %d", i))
	}
	if v, err := unmarshalEnumJSON(colors, []byte(`"COLOR_CRIMSON"`)); err != nil || v != 1 {
		ck(false, "json alias decode")
	}
	if v, err := unmarshalEnumJSON(colors, []byte(`42`)); err != nil || v != 42 {
		ck(false, "json unknown numeric decode")
	}
	if v, err := unmarshalEnumJSON(levels, []byte(`-2147483648`)); err != nil || v != math.MinInt32 {
		ck(false, "json min int32 decode")
	}
	badJSON := []string{`"NOPE"`, `null`, `2147483648`, `-2147483649`, `"COLOR_RED"x`, `[]`, `true`}
	for i, s := range badJSON {
		_, err := unmarshalEnumJSON(colors, []byte(s))
		ck(err != nil, fmt.Sprintf("bad enum json %d rejected", i))
	}

	// 3. Wire golden: known values use canonical one-byte varints.
	m := &enumMsg{c: colorRed, l: levelHigh, flags: []color{colorGreen, colorBlue}}
	wire := encodeEnumMsg(m)
	wantWire := []byte{0x08, 0x01, 0x10, 0x0a, 0x18, 0x02, 0x18, 0x03}
	ck(bytes.Equal(wire, wantWire), "wire golden for known enums")
	back, err := decodeEnumMsg(wire)
	ck(err == nil && equalEnumMsg(back, m), "wire roundtrip known enums")

	// 4. Negative enum values use the 10-byte sign-extended varint.
	neg := &enumMsg{c: colorBlue, l: levelLow, flags: []color{colorRed}}
	nw := encodeEnumMsg(neg)
	ck(len(nw) == 1+1+1+10+1+1, "negative enum varint length")
	back, err = decodeEnumMsg(nw)
	ck(err == nil && equalEnumMsg(back, neg), "negative enum roundtrip")
	// Boundary values min/max int32.
	edges := &enumMsg{c: color(colorMagenta), l: levelMin, flags: []color{color(levelMin), color(levelMax)}}
	eb := encodeEnumMsg(edges)
	back, err = decodeEnumMsg(eb)
	ck(err == nil && equalEnumMsg(back, edges), "min/max int32 enum roundtrip")

	// 5. Unknown enum numbers survive wire re-encoding in order.
	unk := &enumMsg{c: color(42), l: level(-3), flags: []color{colorRed, color(42), color(99), color(42)}}
	uw := encodeEnumMsg(unk)
	gcStress()
	back, err = decodeEnumMsg(uw)
	ck(err == nil && equalEnumMsg(back, unk), "unknown enum values preserved through GC")
	ck(bytes.Equal(encodeEnumMsg(back), uw), "unknown enum values re-encode identically")
	known, unknown, uniq := classify(unk.flags, colors)
	ck(known == 1 && unknown == 3 && len(uniq) == 2 && uniq[0] == 42 && uniq[1] == 99, "unknown enum classification")

	// 6. Out-of-int32 wire values are rejected.
	outOfRange := [][]byte{
		{0x08, 0x80, 0x80, 0x80, 0x80, 0x10},                               // 1<<32
		{0x08, 0xff, 0xff, 0xff, 0xff, 0x0f},                               // 0xffffffff
		{0x08, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x7f}, // huge
		{0x10, 0x80, 0x80, 0x80, 0x80, 0x08},                               // level 1<<31
	}
	for i, b := range outOfRange {
		_, err := decodeEnumMsg(b)
		ck(err != nil, fmt.Sprintf("out-of-range enum payload %d rejected", i))
	}
	// Known field with wrong wire type is rejected rather than skipped.
	_, err = decodeEnumMsg([]byte{0x0d, 1, 2, 3, 4})
	ck(err != nil, "known enum field wrong wire type rejected")
	_, err = decodeEnumMsg([]byte{0x08, 0x80})
	ck(err != nil, "truncated enum varint rejected")

	// 7. Unknown enum map emission is sorted and uses numeric keys.
	counts := map[color]int32{color(99): 2, colorRed: 5, color(42): 1}
	gotCounts := string(encodeCountMap(colors, counts))
	ck(gotCounts == `{"COLOR_RED":5,"42":1,"99":2}`, "unknown enum map deterministic order")

	// 8. JSON + wire combination: JSON unknown numbers -> wire -> JSON.
	var all []*enumMsg
	for i := 0; i < 200; i++ {
		vals := []color{color(1), color(i), color(-i), color(i * 7)}
		msg := &enumMsg{c: color(i), l: level(-i - 1), flags: vals}
		all = append(all, msg)
	}
	gcStress()
	for i, msg := range all {
		enc := encodeEnumMsg(msg)
		dec, err := decodeEnumMsg(enc)
		ck(err == nil && equalEnumMsg(dec, msg), fmt.Sprintf("bulk wire %d", i))
		if i%31 == 0 {
			// JSON values must survive an independent roundtrip as well.
			for _, v := range []int32{int32(msg.c), int32(msg.l)} {
				js := marshalEnumJSON(colors, v)
				got, err := unmarshalEnumJSON(colors, js)
				ck(err == nil && got == v, fmt.Sprintf("bulk json %d/%d", i, v))
			}
		}
	}

	// 9. Seeded random probe over known/unknown/edge values.
	r := rand.New(rand.NewSource(seed))
	for round := 0; round < 400; round++ {
		vals := make([]color, 0, 5)
		for j := 0; j < 1+r.Intn(5); j++ {
			switch r.Intn(4) {
			case 0:
				vals = append(vals, color(colors.unique[r.Intn(len(colors.unique))]))
			case 1:
				vals = append(vals, color(r.Intn(100)))
			case 2:
				vals = append(vals, color(r.Int31()-50))
			default:
				vals = append(vals, color(-r.Int31()))
			}
		}
		msg := &enumMsg{c: vals[0], l: level(r.Int31() - (1 << 30)), flags: vals}
		enc := encodeEnumMsg(msg)
		dec, err := decodeEnumMsg(enc)
		ckSeed(err == nil && equalEnumMsg(dec, msg), fmt.Sprintf("random round %d", round))
		if round%41 == 0 {
			gcStress()
		}
	}

	finish("l10_enum_alias_wire")
}
