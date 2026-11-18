// l10_defaults_alias_copy: generated-style default value table where absent
// fields read immutable defaults. Mutable defaults (bytes/slice/map) are copied
// per read, setters copy on write, and explicit present zero/empty/nil values
// override defaults. The frozen table is shared by all messages.
package main

import (
	"bytes"
	"encoding/hex"
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
		b[len(b)-1] = byte(i * 17)
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

// ---------- defaults table ----------

type mode int32

const (
	modeA mode = 1
	modeB mode = 2
	modeC mode = 3
)

var modeNames = map[string]mode{"a": modeA, "b": modeB, "c": modeC}
var modeCanonical = map[mode]string{modeA: "A", modeB: "B", modeC: "C"}

type defKind int

const (
	defInt32 defKind = iota
	defFloat64
	defString
	defBytes
	defStrings
	defMap
	defEnum
)

type defEntry struct {
	field int
	name  string
	kind  defKind
	i32   int32
	f64   float64
	s     string
	b     []byte
	strs  []string
	m     map[string]int32
	mode  mode
}

var (
	errFrozenTable = fmt.Errorf("default table frozen")
	errDuplicate   = fmt.Errorf("duplicate default")
)

type defaults struct {
	byField map[int]*defEntry
	entries []*defEntry
	names   map[string]bool
	frozen  bool
}

func newDefaults() *defaults {
	return &defaults{byField: map[int]*defEntry{}, names: map[string]bool{}}
}

func (d *defaults) add(e *defEntry) error {
	if d.frozen {
		return errFrozenTable
	}
	if e.field < 1 || e.field > 31 {
		return fmt.Errorf("field %d out of range", e.field)
	}
	if e.name == "" {
		return fmt.Errorf("empty field name")
	}
	if _, ok := d.byField[e.field]; ok {
		return fmt.Errorf("%w: field %d", errDuplicate, e.field)
	}
	if d.names[e.name] {
		return fmt.Errorf("%w: name %s", errDuplicate, e.name)
	}
	d.byField[e.field] = e
	d.names[e.name] = true
	d.entries = append(d.entries, e)
	return nil
}

func (d *defaults) freeze() {
	sort.Slice(d.entries, func(i, j int) bool { return d.entries[i].field < d.entries[j].field })
	d.frozen = true
}

func parseModeDefault(raw string) (mode, error) {
	if m, ok := modeNames[raw]; ok {
		return m, nil
	}
	return 0, fmt.Errorf("unknown enum default %q", raw)
}

func parseDefaultLine(line string) (*defEntry, error) {
	fs := strings.Fields(line)
	if len(fs) != 4 {
		return nil, fmt.Errorf("malformed default line %q", line)
	}
	field, err := strconv.Atoi(fs[0])
	if err != nil {
		return nil, fmt.Errorf("bad field number %q", fs[0])
	}
	e := &defEntry{field: field, name: fs[1]}
	raw := fs[3]
	switch fs[2] {
	case "int32":
		v, err := strconv.ParseInt(raw, 10, 32)
		if err != nil {
			return nil, fmt.Errorf("%s: bad int32 %q", e.name, raw)
		}
		e.kind = defInt32
		e.i32 = int32(v)
	case "float64":
		v, err := strconv.ParseFloat(raw, 64)
		if err != nil {
			return nil, fmt.Errorf("%s: bad float %q", e.name, raw)
		}
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return nil, fmt.Errorf("%s: non-finite float", e.name)
		}
		e.kind = defFloat64
		e.f64 = v
	case "string":
		e.kind = defString
		if raw == `""` {
			e.s = ""
		} else {
			e.s = raw
		}
	case "bytes":
		e.kind = defBytes
		if raw == "-" {
			e.b = []byte{}
		} else {
			b, err := hex.DecodeString(raw)
			if err != nil {
				return nil, fmt.Errorf("%s: bad hex %q", e.name, raw)
			}
			e.b = b
		}
	case "strings":
		e.kind = defStrings
		if raw == "-" {
			e.strs = []string{}
		} else {
			e.strs = strings.Split(raw, ",")
		}
	case "map":
		e.kind = defMap
		e.m = map[string]int32{}
		if raw != "-" {
			seen := map[string]bool{}
			for _, kv := range strings.Split(raw, ",") {
				parts := strings.SplitN(kv, "=", 2)
				if len(parts) != 2 || parts[0] == "" {
					return nil, fmt.Errorf("%s: bad map entry %q", e.name, kv)
				}
				if seen[parts[0]] {
					return nil, fmt.Errorf("%s: duplicate map key %q", e.name, parts[0])
				}
				seen[parts[0]] = true
				v, err := strconv.ParseInt(parts[1], 10, 32)
				if err != nil {
					return nil, fmt.Errorf("%s: bad map value %q", e.name, parts[1])
				}
				e.m[parts[0]] = int32(v)
			}
		}
	case "enum":
		e.kind = defEnum
		m, err := parseModeDefault(raw)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", e.name, err)
		}
		e.mode = m
	default:
		return nil, fmt.Errorf("%s: unknown kind %q", e.name, fs[2])
	}
	return e, nil
}

func buildDefaults(lines []string) (*defaults, error) {
	d := newDefaults()
	for _, line := range lines {
		e, err := parseDefaultLine(line)
		if err != nil {
			return nil, err
		}
		if err := d.add(e); err != nil {
			return nil, err
		}
	}
	d.freeze()
	return d, nil
}

func defaultSpec() []string {
	return []string{
		"1 count int32 7",
		"2 ratio float64 1.5",
		"3 label string anon",
		"4 blob bytes deadbeef",
		"5 tags strings a,b,c",
		"6 limits map soft=1,hard=2",
		"7 mode enum b",
	}
}

// ---------- generated-style message ----------

type profile struct {
	presence uint64
	count    int32
	ratio    float64
	label    string
	blob     []byte
	tags     []string
	limits   map[string]int32
	mode     mode
}

func (p *profile) has(field int) bool { return p.presence&(1<<uint(field-1)) != 0 }
func (p *profile) mark(field int)     { p.presence |= 1 << uint(field-1) }

func (p *profile) SetCount(v int32)   { p.count = v; p.mark(1) }
func (p *profile) SetRatio(v float64) { p.ratio = v; p.mark(2) }
func (p *profile) SetLabel(v string)  { p.label = v; p.mark(3) }
func (p *profile) SetMode(v mode)     { p.mode = v; p.mark(7) }

func copyBytes(v []byte) []byte {
	if v == nil {
		return nil
	}
	out := make([]byte, len(v))
	copy(out, v)
	return out
}

func copyStrings(v []string) []string {
	if v == nil {
		return nil
	}
	out := make([]string, len(v))
	copy(out, v)
	return out
}

func copyLimits(v map[string]int32) map[string]int32 {
	if v == nil {
		return nil
	}
	out := make(map[string]int32, len(v))
	for k, val := range v {
		out[k] = val
	}
	return out
}

func (p *profile) SetBlob(v []byte)   { p.blob = copyBytes(v); p.mark(4) }
func (p *profile) SetTags(v []string) { p.tags = copyStrings(v); p.mark(5) }
func (p *profile) SetLimits(v map[string]int32) {
	p.limits = copyLimits(v)
	p.mark(6)
}

func (p *profile) GetCount(d *defaults) int32 {
	if p.has(1) {
		return p.count
	}
	return d.byField[1].i32
}

func (p *profile) GetRatio(d *defaults) float64 {
	if p.has(2) {
		return p.ratio
	}
	return d.byField[2].f64
}

func (p *profile) GetLabel(d *defaults) string {
	if p.has(3) {
		return p.label
	}
	return d.byField[3].s
}

func (p *profile) GetBlob(d *defaults) []byte {
	if p.has(4) {
		return p.blob
	}
	return copyBytes(d.byField[4].b)
}

func (p *profile) GetTags(d *defaults) []string {
	if p.has(5) {
		return p.tags
	}
	return copyStrings(d.byField[5].strs)
}

func (p *profile) GetLimits(d *defaults) map[string]int32 {
	if p.has(6) {
		return p.limits
	}
	return copyLimits(d.byField[6].m)
}

func (p *profile) GetMode(d *defaults) mode {
	if p.has(7) {
		return p.mode
	}
	return d.byField[7].mode
}

func (p *profile) mergeFrom(src *profile) {
	if src == nil {
		return
	}
	if src.has(1) {
		p.SetCount(src.count)
	}
	if src.has(2) {
		p.SetRatio(src.ratio)
	}
	if src.has(3) {
		p.SetLabel(src.label)
	}
	if src.has(4) {
		p.SetBlob(src.blob)
	}
	if src.has(5) {
		p.SetTags(src.tags)
	}
	if src.has(6) {
		p.SetLimits(src.limits)
	}
	if src.has(7) {
		p.SetMode(src.mode)
	}
}

func (p *profile) reset() { *p = profile{} }

// ---------- main ----------

func main() {
	seed = readSeed()

	// 1. Build the frozen default table.
	d, err := buildDefaults(defaultSpec())
	ck(err == nil, "build defaults")
	ck(len(d.entries) == 7 && d.entries[0].field == 1 && d.entries[6].field == 7, "default table sorted")
	ck(d.frozen, "table frozen after build")
	ck(d.add(&defEntry{field: 8, name: "x", kind: defInt32}) == errFrozenTable, "frozen table rejects additions")
	ck(modeCanonical[d.byField[7].mode] == "B", "enum default resolves")

	// 2. Zero message reads defaults with Has=false.
	var p profile
	ck(!p.has(1) && !p.has(4) && !p.has(6), "zero message has no presence")
	ck(p.GetCount(d) == 7 && p.GetRatio(d) == 1.5 && p.GetLabel(d) == "anon", "scalar defaults")
	ck(bytes.Equal(p.GetBlob(d), []byte{0xde, 0xad, 0xbe, 0xef}), "bytes default")
	ck(p.GetMode(d) == modeB, "enum default")
	blob0 := p.GetBlob(d)
	tags0 := p.GetTags(d)
	limits0 := p.GetLimits(d)
	ck(len(tags0) == 3 && tags0[0] == "a" && tags0[2] == "c", "strings default")
	ck(len(limits0) == 2 && limits0["soft"] == 1 && limits0["hard"] == 2, "map default")

	// 3. Mutable defaults are copied per read; mutation is not observable.
	blob0[0] = 0xFF
	blob1 := p.GetBlob(d)
	ck(blob1[0] == 0xde, "bytes default copy isolated")
	tags0[0] = "mutated"
	tags1 := p.GetTags(d)
	ck(tags1[0] == "a", "strings default copy isolated")
	limits0["soft"] = 99
	limits1 := p.GetLimits(d)
	ck(limits1["soft"] == 1, "map default copy isolated")
	ck(bytes.Equal(d.byField[4].b, []byte{0xde, 0xad, 0xbe, 0xef}), "table bytes unchanged")
	ck(d.byField[5].strs[0] == "a" && d.byField[6].m["soft"] == 1, "table slices/maps unchanged")

	// 4. Two messages never share mutable defaults.
	var p2 profile
	got := p2.GetBlob(d)
	got[1] = 0x00
	ck(p.GetBlob(d)[1] == 0xad && p2.GetBlob(d)[1] == 0xad, "default copies independent per message")

	// 5. Present zero/empty/nil overrides defaults, including presence semantics.
	var q profile
	q.SetCount(0)
	q.SetRatio(0)
	q.SetLabel("")
	q.SetBlob(nil)
	q.SetTags(nil)
	q.SetLimits(nil)
	q.SetMode(0)
	ck(q.has(1) && q.has(4) && q.has(6) && q.has(7), "explicit zero marks presence")
	ck(q.GetCount(d) == 0 && q.GetRatio(d) == 0 && q.GetLabel(d) == "", "explicit scalar zero wins")
	ck(q.GetBlob(d) == nil && q.GetTags(d) == nil && q.GetLimits(d) == nil, "explicit nil containers win")
	ck(q.GetMode(d) == 0, "explicit zero enum wins")
	// Empty-non-nil values stay non-nil.
	q.SetBlob([]byte{})
	q.SetTags([]string{})
	q.SetLimits(map[string]int32{})
	ck(q.GetBlob(d) != nil && len(q.GetBlob(d)) == 0, "explicit empty bytes non-nil")
	ck(q.GetTags(d) != nil && len(q.GetTags(d)) == 0, "explicit empty slice non-nil")
	ck(q.GetLimits(d) != nil && len(q.GetLimits(d)) == 0, "explicit empty map non-nil")

	// 6. Copy-on-set: caller mutations never reach the message.
	srcBlob := []byte{1, 2, 3}
	srcTags := []string{"x", "y"}
	srcLimits := map[string]int32{"k": 5}
	q.SetBlob(srcBlob)
	q.SetTags(srcTags)
	q.SetLimits(srcLimits)
	srcBlob[0] = 9
	srcTags[0] = "z"
	srcLimits["k"] = 99
	ck(q.GetBlob(d)[0] == 1 && q.GetTags(d)[0] == "x" && q.GetLimits(d)["k"] == 5, "setter copies input containers")
	// Present getters intentionally return the stored view; mutating it changes the message.
	q.GetBlob(d)[0] = 42
	ck(q.GetBlob(d)[0] == 42, "present getter returns live view")

	// 7. Merge: present source overrides, absent source leaves destination,
	//    and containers are deep-copied.
	var dst profile
	dst.SetCount(1)
	dst.SetLabel("dst")
	dst.SetBlob([]byte{7})
	var src profile
	src.SetCount(2)
	src.SetTags([]string{"s"})
	src.SetLimits(map[string]int32{"a": 1})
	dst.mergeFrom(&src)
	ck(dst.GetCount(d) == 2 && dst.GetLabel(d) == "dst" && dst.GetBlob(d)[0] == 7, "merge overrides present, keeps absent")
	ck(len(dst.GetTags(d)) == 1 && dst.GetLimits(d)["a"] == 1, "merge adds source containers")
	src.GetTags(d)[0] = "changed"
	src.GetLimits(d)["a"] = 2
	ck(dst.GetTags(d)[0] == "s" && dst.GetLimits(d)["a"] == 1, "merge deep-copies containers")
	dst.mergeFrom(nil)
	ck(dst.GetCount(d) == 2, "merge nil is a no-op")

	// 8. Reset returns to defaults; pre-reset default copies remain valid.
	saved := q.GetTags(d)
	q.reset()
	ck(!q.has(1) && !q.has(7) && q.GetCount(d) == 7 && q.GetMode(d) == modeB, "reset restores defaults")
	ck(q.GetBlob(d)[0] == 0xde, "reset restores bytes default")
	gcStress()
	ck(saved != nil && len(saved) > 0, "pre-reset snapshot survives GC")
	ck(q.GetTags(d)[0] == "a", "defaults stable after GC")

	// 9. Build error paths.
	badSpecs := [][]string{
		{"1 count int32 7", "1 other int32 8"},
		{"1 count int32 7", "2 count int32 8"},
		{"0 count int32 1"},
		{"32 count int32 1"},
		{"1 count int32 nope"},
		{"1 ratio float64 NaN"},
		{"1 ratio float64 Inf"},
		{"1 blob bytes zz"},
		{"1 tags strings"},
		{"1 limits map a"},
		{"1 limits map a=1,a=2"},
		{"1 limits map =1"},
		{"1 mode enum nope"},
		{"1 count bool 1"},
		{"1 count int32"},
	}
	for i, spec := range badSpecs {
		_, err := buildDefaults(spec)
		ck(err != nil, fmt.Sprintf("bad default spec %d rejected", i))
	}
	// Empty-declared mutable defaults stay non-nil and are copied.
	emptyD, err := buildDefaults([]string{"4 blob bytes -", "5 tags strings -", "6 limits map -"})
	ck(err == nil, "empty default spec builds")
	if err == nil {
		var ep profile
		ck(ep.GetBlob(emptyD) != nil && len(ep.GetBlob(emptyD)) == 0, "empty bytes default non-nil")
		ck(ep.GetTags(emptyD) != nil && len(ep.GetTags(emptyD)) == 0, "empty slice default non-nil")
		ck(ep.GetLimits(emptyD) != nil && len(ep.GetLimits(emptyD)) == 0, "empty map default non-nil")
	}

	// 10. GC with many messages and default copies.
	var keep []*profile
	for i := 0; i < 300; i++ {
		pp := &profile{}
		if i%3 == 0 {
			pp.SetBlob([]byte{byte(i), byte(i >> 8)})
			pp.SetTags([]string{fmt.Sprintf("t%d", i)})
		}
		if i%5 == 0 {
			pp.SetLimits(map[string]int32{fmt.Sprintf("k%d", i): int32(i)})
		}
		_ = pp.GetBlob(d)
		_ = pp.GetTags(d)
		_ = pp.GetLimits(d)
		keep = append(keep, pp)
	}
	gcStress()
	for i, pp := range keep {
		if i%3 == 0 {
			ck(pp.GetBlob(d)[0] == byte(i) && pp.GetTags(d)[0] == fmt.Sprintf("t%d", i), fmt.Sprintf("bulk mutable %d", i))
		} else {
			ck(pp.GetBlob(d)[0] == 0xde && pp.GetTags(d)[0] == "a", fmt.Sprintf("bulk default %d", i))
		}
		if i%5 == 0 {
			ck(pp.GetLimits(d)[fmt.Sprintf("k%d", i)] == int32(i), fmt.Sprintf("bulk map %d", i))
		}
	}

	// 11. Seeded torture against an explicit presence/model.
	rounds := envInt("TORTURE_ROUNDS", 300)
	r := rand.New(rand.NewSource(seed))
	okRounds := 0
	for i := 0; i < rounds; i++ {
		var pp profile
		present := [8]bool{}
		wantCount := int32(7)
		wantRatio := 1.5
		wantLabel := "anon"
		wantMode := modeB
		var wantBlob []byte = []byte{0xde, 0xad, 0xbe, 0xef}
		wantTags := []string{"a", "b", "c"}
		wantLimits := map[string]int32{"soft": 1, "hard": 2}
		if r.Intn(2) == 0 {
			present[1] = true
			wantCount = int32(r.Intn(200) - 100)
			pp.SetCount(wantCount)
		}
		if r.Intn(2) == 0 {
			present[2] = true
			wantRatio = float64(r.Intn(2000)-1000) / 8
			pp.SetRatio(wantRatio)
		}
		if r.Intn(2) == 0 {
			present[3] = true
			wantLabel = fmt.Sprintf("l%d", r.Intn(100))
			pp.SetLabel(wantLabel)
		}
		if r.Intn(2) == 0 {
			present[4] = true
			if r.Intn(2) == 0 {
				wantBlob = nil
			} else {
				wantBlob = []byte{byte(r.Intn(256)), byte(r.Intn(256))}
			}
			pp.SetBlob(wantBlob)
		}
		if r.Intn(2) == 0 {
			present[5] = true
			if r.Intn(2) == 0 {
				wantTags = nil
			} else {
				wantTags = []string{fmt.Sprintf("t%d", r.Intn(10))}
			}
			pp.SetTags(wantTags)
		}
		if r.Intn(2) == 0 {
			present[6] = true
			if r.Intn(2) == 0 {
				wantLimits = nil
			} else {
				wantLimits = map[string]int32{fmt.Sprintf("k%d", r.Intn(5)): int32(r.Intn(100))}
			}
			pp.SetLimits(wantLimits)
		}
		if r.Intn(2) == 0 {
			present[7] = true
			wantMode = mode(r.Intn(5))
			pp.SetMode(wantMode)
		}
		got := pp.GetCount(d)
		ok := got == wantCount
		ok = ok && pp.GetRatio(d) == wantRatio
		ok = ok && pp.GetLabel(d) == wantLabel
		ok = ok && pp.GetMode(d) == wantMode
		gb := pp.GetBlob(d)
		if (gb == nil) != (wantBlob == nil) || !bytes.Equal(gb, wantBlob) {
			ok = false
		}
		gt := pp.GetTags(d)
		if (gt == nil) != (wantTags == nil) || len(gt) != len(wantTags) {
			ok = false
		} else {
			for j := range gt {
				if gt[j] != wantTags[j] {
					ok = false
				}
			}
		}
		gl := pp.GetLimits(d)
		if (gl == nil) != (wantLimits == nil) || len(gl) != len(wantLimits) {
			ok = false
		} else {
			for k, v := range wantLimits {
				if gl[k] != v {
					ok = false
				}
			}
		}
		for f := 1; f <= 7; f++ {
			if pp.has(f) != present[f] {
				ok = false
			}
		}
		if !ok {
			ckSeed(false, fmt.Sprintf("torture round %d mismatch", i))
			continue
		}
		// Mutating an absent field's default must never change the table or next read.
		if !present[4] {
			gb[0] = 0
			ckSeed(pp.GetBlob(d)[0] == 0xde, "torture default blob isolation")
		}
		if !present[5] && len(gt) > 0 {
			gt[0] = "mut"
			ckSeed(pp.GetTags(d)[0] == "a", "torture default tags isolation")
		}
		if !present[6] && len(gl) > 0 {
			for k := range gl {
				gl[k] = 12345
			}
			ckSeed(pp.GetLimits(d)["soft"] == 1, "torture default map isolation")
		}
		if i%37 == 0 {
			gcStress()
		}
		okRounds++
	}
	ck(okRounds == rounds, "torture: all default rounds matched")

	finish("l10_defaults_alias_copy")
}
