// L10: 生成风格枚举映射表：正/反向 map、别名（allow_alias）、稀疏值域、
// JSON/text 编解码、未知值回退与 GC 后全表稳定性。
package main

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"runtime"
	"sort"
	"strconv"
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

// ---- 生成风格枚举 ----

type Status int32

const (
	StatusUnknown  Status = 0
	StatusActive   Status = 1
	StatusEnabled  Status = 1 // allow_alias
	StatusPaused   Status = 2
	StatusDeleted  Status = -1
	StatusArchived Status = 1000
)

type Level int32

const (
	LevelZero   Level = 0
	LevelNeg100 Level = -100
	LevelSeven  Level = 7
	LevelBig    Level = 1000000
	LevelHuge   Level = 1 << 30
)

type Mode int32

const (
	ModeNone      Mode = 0
	ModeRead      Mode = 1
	ModeWrite     Mode = 2
	ModeRW        Mode = 3
	ModeReadWrite Mode = 3 // allow_alias
)

type aliasEntry struct {
	Name  string
	Value int32
}

type enumDef struct {
	Kind      string
	Fallback  string
	Entries   []aliasEntry // 声明顺序，含别名
	Canonical map[int32]string
	Reverse   map[string]int32
	All       []int32
}

func newEnumDef(kind, fallback string, entries []aliasEntry) *enumDef {
	d := &enumDef{
		Kind:      kind,
		Fallback:  fallback,
		Entries:   entries,
		Canonical: make(map[int32]string, len(entries)),
		Reverse:   make(map[string]int32, len(entries)),
	}
	seen := make(map[int32]bool, len(entries))
	for _, e := range entries {
		d.Reverse[e.Name] = e.Value
		if !seen[e.Value] {
			seen[e.Value] = true
			d.Canonical[e.Value] = e.Name // 首个声明名获胜
			d.All = append(d.All, e.Value)
		}
	}
	return d
}

var (
	statusDef = newEnumDef("Status", "Status", []aliasEntry{
		{"STATUS_UNKNOWN", 0},
		{"STATUS_ACTIVE", 1},
		{"STATUS_ENABLED", 1},
		{"STATUS_PAUSED", 2},
		{"STATUS_DELETED", -1},
		{"STATUS_ARCHIVED", 1000},
	})
	levelDef = newEnumDef("Level", "Level", []aliasEntry{
		{"LEVEL_ZERO", 0},
		{"LEVEL_NEG_100", -100},
		{"LEVEL_SEVEN", 7},
		{"LEVEL_BIG", 1000000},
		{"LEVEL_HUGE", 1 << 30},
	})
	modeDef = newEnumDef("Mode", "Mode", []aliasEntry{
		{"MODE_NONE", 0},
		{"MODE_READ", 1},
		{"MODE_WRITE", 2},
		{"MODE_RW", 3},
		{"MODE_READWRITE", 3},
	})
)

func (d *enumDef) name(v int32) string {
	if n, ok := d.Canonical[v]; ok {
		return n
	}
	return d.Fallback + "(" + strconv.FormatInt(int64(v), 10) + ")"
}

func (d *enumDef) lookup(name string) (int32, bool) {
	// 生成代码通常直接查反向 map；随机测试同时核对按声明序的线性模型。
	v, ok := d.Reverse[name]
	return v, ok
}

func (d *enumDef) lookupLinear(name string) (int32, bool) {
	for _, e := range d.Entries {
		if e.Name == name {
			return e.Value, true
		}
	}
	return 0, false
}

func (d *enumDef) valid(v int32) bool {
	_, ok := d.Canonical[v]
	return ok
}

func (d *enumDef) allValues() []int32 {
	out := make([]int32, len(d.All))
	copy(out, d.All)
	return out
}

func (d *enumDef) text(v int32) []byte {
	if n, ok := d.Canonical[v]; ok {
		return []byte(n)
	}
	return []byte(strconv.FormatInt(int64(v), 10))
}

func (d *enumDef) parseText(b []byte) (int32, error) {
	s := string(b)
	if v, ok := d.lookupLinear(s); ok {
		return v, nil
	}
	if n, err := strconv.ParseInt(s, 10, 32); err == nil {
		return int32(n), nil
	}
	return 0, fmt.Errorf("%s: unknown name %q", d.Kind, s)
}

func (s Status) String() string                  { return statusDef.name(int32(s)) }
func (s Status) Valid() bool                     { return statusDef.valid(int32(s)) }
func (s Status) MarshalText() ([]byte, error)    { return statusDef.text(int32(s)), nil }
func (s *Status) UnmarshalText(b []byte) error {
	v, err := statusDef.parseText(b)
	if err != nil {
		return err
	}
	*s = Status(v)
	return nil
}

func (l Level) String() string               { return levelDef.name(int32(l)) }
func (l Level) Valid() bool                  { return levelDef.valid(int32(l)) }
func (l Level) MarshalText() ([]byte, error) { return levelDef.text(int32(l)), nil }
func (l *Level) UnmarshalText(b []byte) error {
	v, err := levelDef.parseText(b)
	if err != nil {
		return err
	}
	*l = Level(v)
	return nil
}

func (m Mode) String() string               { return modeDef.name(int32(m)) }
func (m Mode) Valid() bool                  { return modeDef.valid(int32(m)) }
func (m Mode) MarshalText() ([]byte, error) { return modeDef.text(int32(m)), nil }
func (m *Mode) UnmarshalText(b []byte) error {
	v, err := modeDef.parseText(b)
	if err != nil {
		return err
	}
	*m = Mode(v)
	return nil
}

func enumChecksum() uint64 {
	var h uint64 = 1469598103934665603
	mix := func(s string) {
		for i := 0; i < len(s); i++ {
			h ^= uint64(s[i])
			h *= 1099511628211
		}
	}
	for _, d := range []*enumDef{statusDef, levelDef, modeDef} {
		mix(d.Kind)
		names := make([]string, 0, len(d.Entries))
		for _, e := range d.Entries {
			names = append(names, e.Name)
		}
		sort.Strings(names)
		for _, n := range names {
			mix(n)
			mix(strconv.FormatInt(int64(d.Reverse[n]), 10))
		}
		for _, v := range d.All {
			mix(d.Canonical[v])
			mix(strconv.FormatInt(int64(v), 10))
		}
	}
	return h
}

type wireMsg struct {
	S Status `json:"s"`
	L Level  `json:"l"`
	M Mode   `json:"m"`
}

func main() {
	curSeed = 1
	if s := os.Getenv("UAF_SEED"); s != "" {
		if v, err := strconv.ParseInt(s, 10, 64); err == nil {
			curSeed = v
		}
	}

	// 1. 表结构：别名、首名获胜、唯一值列表。
	ck(len(statusDef.Entries) == 6 && len(statusDef.Reverse) == 6, "status alias table size")
	ck(len(statusDef.Canonical) == 5 && len(statusDef.All) == 5, "status canonical/all size")
	ck(statusDef.Canonical[1] == "STATUS_ACTIVE", "status first declaration wins alias")
	ck(statusDef.Reverse["STATUS_ENABLED"] == 1 && statusDef.Reverse["STATUS_ACTIVE"] == 1, "status alias lookup")
	ck(modeDef.Canonical[3] == "MODE_RW" && modeDef.Reverse["MODE_READWRITE"] == 3, "mode alias")
	ck(len(levelDef.All) == 5 && levelDef.valid(1<<30) && levelDef.valid(-100), "level sparse values")
	ck(!levelDef.valid(1) && !levelDef.valid(-101) && !levelDef.valid(1<<30+1), "level invalid values")

	for _, d := range []*enumDef{statusDef, levelDef, modeDef} {
		names := make([]string, 0, len(d.Entries))
		seen := make(map[string]bool, len(d.Entries))
		for _, e := range d.Entries {
			if seen[e.Name] {
				ck(false, d.Kind+" duplicate alias name")
			}
			seen[e.Name] = true
			names = append(names, e.Name)
		}
		sort.Strings(names)
		for _, n := range names {
			mv, mok := d.lookup(n)
			lv, lok := d.lookupLinear(n)
			if !mok || !lok || mv != lv {
				ck(false, d.Kind+" reverse map disagrees with declaration table")
			}
		}
		vals := d.allValues()
		for i, v := range vals {
			if !d.valid(v) || d.name(v) == "" {
				ck(false, d.Kind+" all-values entry not valid/canonical")
			}
			for j := 0; j < i; j++ {
				if vals[j] == v {
					ck(false, d.Kind+" duplicate value in all-values")
				}
			}
		}
	}

	// 2. 合法值：String/Valid/text 往返；未知值回退。
	for _, d := range []*enumDef{statusDef, levelDef, modeDef} {
		for _, v := range d.All {
			name, ok := d.Canonical[v]
			ck(ok, d.Kind+" valid value has no canonical name")
			ck(d.name(v) == name, d.Kind+" String uses canonical name")
			ck(d.valid(v), d.Kind+" Valid true")
			txt := d.text(v)
			ck(string(txt) == name, d.Kind+" MarshalText canonical")
			back, err := d.parseText(txt)
			ck(err == nil && back == v, d.Kind+" MarshalText/UnmarshalText roundtrip")
			alias, hasAlias := "", false
			for _, e := range d.Entries {
				if e.Value == v && e.Name != name {
					alias, hasAlias = e.Name, true
					break
				}
			}
			if hasAlias {
				backAlias, errAlias := d.parseText([]byte(alias))
				ck(errAlias == nil && backAlias == v, d.Kind+" alias text decodes to same value")
			}
		}
	}
	unknowns := []int32{-2, 3, 4, 999, 1001, 1<<30 + 1}
	for _, d := range []*enumDef{statusDef, levelDef, modeDef} {
		for _, v := range unknowns {
			if d.valid(v) {
				continue
			}
			wantSuffix := "(" + strconv.FormatInt(int64(v), 10) + ")"
			got := d.name(v)
			ck(len(got) > len(wantSuffix) && got[len(got)-len(wantSuffix):] == wantSuffix,
				d.Kind+" unknown value fallback")
			ck(!d.valid(v), d.Kind+" unknown value invalid")
			back, err := d.parseText([]byte(strconv.FormatInt(int64(v), 10)))
			ck(err == nil && back == v, d.Kind+" unknown decimal roundtrip")
		}
	}

	// 3. JSON：规范名输出、别名输入归一到规范名、未知数值保留、坏名报错。
	m := wireMsg{S: StatusEnabled, L: LevelSeven, M: ModeReadWrite}
	jb, err := json.Marshal(m)
	ck(err == nil, "json marshal valid enum message")
	ck(string(jb) == `{"s":"STATUS_ACTIVE","l":"LEVEL_SEVEN","m":"MODE_RW"}`, "json canonical output")
	var m2 wireMsg
	ck(json.Unmarshal([]byte(`{"s":"STATUS_ENABLED","l":"LEVEL_NEG_100","m":"MODE_READWRITE"}`), &m2) == nil,
		"json alias input")
	ck(m2.S == StatusActive && m2.L == LevelNeg100 && m2.M == ModeRW, "json alias normalizes to value")
	jb2, _ := json.Marshal(m2)
	ck(string(jb2) == `{"s":"STATUS_ACTIVE","l":"LEVEL_NEG_100","m":"MODE_RW"}`, "json re-marshal canonical")
	var m3 wireMsg
	ck(json.Unmarshal([]byte(`{"s":"99","l":"-77","m":"1234"}`), &m3) == nil, "json unknown numeric strings")
	jb3, _ := json.Marshal(m3)
	ck(string(jb3) == `{"s":"99","l":"-77","m":"1234"}`, "json unknown numeric preserved")
	var m4 wireMsg
	ck(json.Unmarshal([]byte(`{"s":"NOPE"}`), &m4) != nil, "json bad enum name rejected")
	ck(json.Unmarshal([]byte(`{"l":"level_seven"}`), &m4) != nil, "json case-sensitive name rejected")

	// 4. GC：保留名称串、解码结果与 JSON 字节，GC 后全表与视图不变。
	before := enumChecksum()
	namesView := make([]string, 0, 16)
	for _, d := range []*enumDef{statusDef, levelDef, modeDef} {
		for _, e := range d.Entries {
			namesView = append(namesView, e.Name)
		}
	}
	sort.Strings(namesView)
	retained := []*wireMsg{&m, &m2, &m3}
	jsonView := append([]byte(nil), jb...)
	gcSink = append(gcSink, jsonView)
	gcStress()
	ck(enumChecksum() == before, "enum table checksum stable across GC")
	ck(len(namesView) == 16 && namesView[0] == "LEVEL_BIG" && namesView[len(namesView)-1] == "STATUS_UNKNOWN",
		"retained name views survive GC")
	ck(retained[0].S == StatusActive && retained[1].L == LevelNeg100 && retained[2].M == 1234,
		"retained decoded enums survive GC")
	ck(string(jsonView) == `{"s":"STATUS_ACTIVE","l":"LEVEL_SEVEN","m":"MODE_RW"}`, "retained json bytes survive GC")
	jbAfter, _ := json.Marshal(m)
	ck(string(jbAfter) == string(jsonView), "json encode stable after GC")

	// 5. UAF_SEED 随机探测：名称反查、数值命名、文本编解码、JSON 往返。
	r := rand.New(rand.NewSource(curSeed))
	defs := []*enumDef{statusDef, levelDef, modeDef}
	probes := make([]string, 0, 32)
	for _, d := range defs {
		for _, e := range d.Entries {
			probes = append(probes, e.Name)
		}
	}
	probes = append(probes, "", "STATUS_ACTIVE ", " STATUS_ACTIVE", "status_active", "LEVEL_ZERO0",
		"-1", "0", "1", "7", "1000", "1073741824", "2147483647", "-2147483648", "0x10", "+5")

	for i := 0; i < 4000; i++ {
		if i%500 == 0 {
			gcStress()
			ckSeed(enumChecksum() == before, fmt.Sprintf("random round %d checksum after GC", i))
		}
		di := r.Intn(len(defs))
		d := defs[di]
		switch r.Intn(4) {
		case 0: // 名称反查（含虚实）
			name := probes[r.Intn(len(probes))]
			v, ok := d.lookup(name)
			lv, lok := d.lookupLinear(name)
			ckSeed(ok == lok && v == lv, fmt.Sprintf("round %d %s lookup %q", i, d.Kind, name))
		case 1: // 值命名：合法值取规范名，否则回退
			var v int32
			if r.Intn(2) == 0 {
				v = d.All[r.Intn(len(d.All))]
			} else {
				v = int32(r.Intn(3000) - 1500)
			}
			if n, ok := d.Canonical[v]; ok {
				ckSeed(d.name(v) == n && d.valid(v), fmt.Sprintf("round %d %s name(%d)", i, d.Kind, v))
			} else {
				ckSeed(d.name(v) == d.Fallback+"("+strconv.FormatInt(int64(v), 10)+")" && !d.valid(v),
					fmt.Sprintf("round %d %s fallback(%d)", i, d.Kind, v))
			}
		case 2: // 文本往返
			var v int32
			if r.Intn(3) != 0 {
				v = int32(r.Intn(3000) - 1500)
			} else {
				v = d.All[r.Intn(len(d.All))]
			}
			back, err := d.parseText(d.text(v))
			ckSeed(err == nil && back == v, fmt.Sprintf("round %d %s text(%d)", i, d.Kind, v))
			if _, bad := d.lookupLinear(string(d.text(v)) + "X"); bad {
				ckSeed(false, fmt.Sprintf("round %d %s mutated name accepted", i, d.Kind))
			}
		default: // JSON 往返
			w := wireMsg{
				S: Status(int32(r.Intn(2000) - 1000)),
				L: Level(int32(r.Intn(21) - 10)),
				M: Mode(int32(r.Intn(6))),
			}
			b, err := json.Marshal(w)
			ckSeed(err == nil, fmt.Sprintf("round %d json marshal", i))
			var back wireMsg
			err = json.Unmarshal(b, &back)
			ckSeed(err == nil && back.S == w.S && back.L == w.L && back.M == w.M,
				fmt.Sprintf("round %d json roundtrip %s", i, string(b)))
		}
	}

	// 6. AllValues 返回副本：修改返回值不影响内部表。
	vals := statusDef.allValues()
	if len(vals) > 0 {
		vals[0] = 12345
	}
	ck(statusDef.All[0] == 0 && statusDef.valid(0), "AllValues returns a copy")
	ck(enumChecksum() == before, "final checksum unchanged")

	finish("l10_enum_mapping")
}
