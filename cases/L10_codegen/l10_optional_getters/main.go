// L10: 生成式 optional 字段 getter：指针 presence + 默认值表、显式零值可区分、
// nil receiver 安全、方法值跨 GC、随机 presence 组合与描述符表覆盖校验。
package main

import (
	"bytes"
	"fmt"
	"math/rand"
	"os"
	"reflect"
	"runtime"
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

type optEnum int32

const (
	OptEnumZero optEnum = 0
	OptEnumOne  optEnum = 1
	OptEnumTwo  optEnum = 2
)

const (
	defID     int32   = 42
	defCount  int64   = -7
	defRatio  float64 = 3.25
	defActive bool    = true
	defLabel          = "unset-label"
	defMode           = OptEnumTwo
)

var defBlob = []byte{0xde, 0xad, 0xbe, 0xef}

type optChild struct {
	Name   string
	Weight int64
}

// optMsg 模拟 protoc-gen-go 的 optional 字段：标量用指针表示 presence。
type optMsg struct {
	ID     *int32
	Count  *int64
	Ratio  *float64
	Active *bool
	Label  *string
	Mode   *optEnum
	Blob   []byte
	Child  *optChild
}

func (m *optMsg) GetID() int32 {
	if m != nil && m.ID != nil {
		return *m.ID
	}
	return defID
}

func (m *optMsg) GetCount() int64 {
	if m != nil && m.Count != nil {
		return *m.Count
	}
	return defCount
}

func (m *optMsg) GetRatio() float64 {
	if m != nil && m.Ratio != nil {
		return *m.Ratio
	}
	return defRatio
}

func (m *optMsg) GetActive() bool {
	if m != nil && m.Active != nil {
		return *m.Active
	}
	return defActive
}

func (m *optMsg) GetLabel() string {
	if m != nil && m.Label != nil {
		return *m.Label
	}
	return defLabel
}

func (m *optMsg) GetMode() optEnum {
	if m != nil && m.Mode != nil {
		return *m.Mode
	}
	return defMode
}

func (m *optMsg) GetBlob() []byte {
	if m != nil && m.Blob != nil {
		return m.Blob
	}
	return defBlob
}

func (m *optMsg) GetChild() *optChild {
	if m != nil {
		return m.Child
	}
	return nil
}

func (m *optMsg) HasID() bool     { return m != nil && m.ID != nil }
func (m *optMsg) HasCount() bool  { return m != nil && m.Count != nil }
func (m *optMsg) HasRatio() bool  { return m != nil && m.Ratio != nil }
func (m *optMsg) HasActive() bool { return m != nil && m.Active != nil }
func (m *optMsg) HasLabel() bool  { return m != nil && m.Label != nil }
func (m *optMsg) HasMode() bool   { return m != nil && m.Mode != nil }
func (m *optMsg) HasBlob() bool   { return m != nil && m.Blob != nil }
func (m *optMsg) HasChild() bool  { return m != nil && m.Child != nil }

type optFieldDesc struct {
	Name string
	Kind string
	Def  string
}

var optFieldTable = [...]optFieldDesc{
	{"ID", "int32", "42"},
	{"Count", "int64", "-7"},
	{"Ratio", "float64", "3.25"},
	{"Active", "bool", "true"},
	{"Label", "string", "unset-label"},
	{"Mode", "enum", "2"},
	{"Blob", "bytes", "de ad be ef"},
	{"Child", "message", "nil"},
}

const optFieldCount = 8

// 编译期断言：描述符表必须覆盖全部字段。
var _ [optFieldCount - len(optFieldTable)]struct{}

func i32p(v int32) *int32      { return &v }
func i64p(v int64) *int64      { return &v }
func f64p(v float64) *float64  { return &v }
func boolp(v bool) *bool       { return &v }
func strp(v string) *string    { return &v }
func enump(v optEnum) *optEnum { return &v }

type optExpect struct {
	has     [8]bool
	id      int32
	count   int64
	ratio   float64
	active  bool
	label   string
	mode    optEnum
	blob    []byte
	blobNil bool
	child   *optChild
}

func expectZero() optExpect {
	return optExpect{id: defID, count: defCount, ratio: defRatio, active: defActive,
		label: defLabel, mode: defMode, blob: defBlob, blobNil: true}
}

func buildRandom(r *rand.Rand) (*optMsg, optExpect) {
	m := &optMsg{}
	e := expectZero()
	// state: 0=absent, 1=显式零值, 2=非零值
	switch r.Intn(3) {
	case 1:
		m.ID = i32p(0)
		e.has[0], e.id = true, 0
	case 2:
		v := int32(r.Intn(1<<31-1) - (1 << 30))
		m.ID = i32p(v)
		e.has[0], e.id = true, v
	}
	switch r.Intn(3) {
	case 1:
		m.Count = i64p(0)
		e.has[1], e.count = true, 0
	case 2:
		v := r.Int63n(1<<52) - (1 << 51)
		m.Count = i64p(v)
		e.has[1], e.count = true, v
	}
	switch r.Intn(3) {
	case 1:
		m.Ratio = f64p(0)
		e.has[2], e.ratio = true, 0
	case 2:
		v := float64(r.Intn(2001)-1000) / 8
		m.Ratio = f64p(v)
		e.has[2], e.ratio = true, v
	}
	switch r.Intn(3) {
	case 1:
		m.Active = boolp(false)
		e.has[3], e.active = true, false
	case 2:
		m.Active = boolp(true)
		e.has[3], e.active = true, true
	}
	switch r.Intn(3) {
	case 1:
		m.Label = strp("")
		e.has[4], e.label = true, ""
	case 2:
		v := fmt.Sprintf("label-%d", r.Intn(10000))
		m.Label = strp(v)
		e.has[4], e.label = true, v
	}
	switch r.Intn(3) {
	case 1:
		m.Mode = enump(OptEnumZero)
		e.has[5], e.mode = true, OptEnumZero
	case 2:
		v := optEnum(r.Intn(3))
		m.Mode = enump(v)
		e.has[5], e.mode = true, v
	}
	switch r.Intn(3) {
	case 1:
		m.Blob = []byte{}
		e.has[6], e.blob, e.blobNil = true, []byte{}, false
	case 2:
		b := make([]byte, r.Intn(20))
		for i := range b {
			b[i] = byte(r.Intn(256))
		}
		m.Blob = b
		e.has[6], e.blob, e.blobNil = true, b, false
	}
	switch r.Intn(3) {
	case 2:
		c := &optChild{Name: fmt.Sprintf("c%d", r.Intn(1000)), Weight: r.Int63n(1 << 40)}
		m.Child = c
		e.has[7], e.child = true, c
	}
	return m, e
}

func checkMsg(m *optMsg, e optExpect, tag string) {
	ckSeed(m.GetID() == e.id && m.HasID() == e.has[0], tag+" id")
	ckSeed(m.GetCount() == e.count && m.HasCount() == e.has[1], tag+" count")
	ckSeed(m.GetRatio() == e.ratio && m.HasRatio() == e.has[2], tag+" ratio")
	ckSeed(m.GetActive() == e.active && m.HasActive() == e.has[3], tag+" active")
	ckSeed(m.GetLabel() == e.label && m.HasLabel() == e.has[4], tag+" label")
	ckSeed(m.GetMode() == e.mode && m.HasMode() == e.has[5], tag+" mode")
	gotBlob := m.GetBlob()
	ckSeed(bytes.Equal(gotBlob, e.blob) && m.HasBlob() == e.has[6], tag+" blob")
	if e.has[6] {
		ckSeed((gotBlob == nil) == e.blobNil, tag+" blob nilness")
	} else {
		ckSeed(gotBlob != nil && bytes.Equal(gotBlob, defBlob), tag+" default blob")
	}
	ckSeed((m.GetChild() == e.child) && m.HasChild() == e.has[7], tag+" child")
}

func main() {
	curSeed = 1
	if s := os.Getenv("UAF_SEED"); s != "" {
		if v, err := strconv.ParseInt(s, 10, 64); err == nil {
			curSeed = v
		}
	}

	// 1. 零值与 nil receiver：全部返回默认值，presence 为 false。
	var nilMsg *optMsg
	ck(nilMsg.GetID() == defID && nilMsg.GetCount() == defCount && nilMsg.GetRatio() == defRatio, "nil receiver scalar defaults")
	ck(nilMsg.GetActive() == defActive && nilMsg.GetLabel() == defLabel && nilMsg.GetMode() == defMode, "nil receiver more defaults")
	ck(nilMsg.GetBlob() != nil && bytes.Equal(nilMsg.GetBlob(), defBlob), "nil receiver byte default")
	ck(nilMsg.GetChild() == nil, "nil receiver message default")
	ck(!nilMsg.HasID() && !nilMsg.HasCount() && !nilMsg.HasRatio() && !nilMsg.HasActive(), "nil receiver has false")
	ck(!nilMsg.HasLabel() && !nilMsg.HasMode() && !nilMsg.HasBlob() && !nilMsg.HasChild(), "nil receiver has false 2")

	var zero optMsg
	ck(zero.GetID() == defID && zero.GetCount() == defCount && zero.GetRatio() == defRatio &&
		zero.GetActive() == defActive && zero.GetLabel() == defLabel && zero.GetMode() == defMode, "zero value defaults")
	ck(!zero.HasID() && !zero.HasBlob() && !zero.HasChild(), "zero value presence")

	// 2. 显式零值：presence 为 true，getter 返回零而非默认。
	z := &optMsg{
		ID: i32p(0), Count: i64p(0), Ratio: f64p(0), Active: boolp(false),
		Label: strp(""), Mode: enump(OptEnumZero), Blob: []byte{}, Child: &optChild{},
	}
	ck(z.GetID() == 0 && z.HasID(), "explicit zero int32")
	ck(z.GetCount() == 0 && z.HasCount(), "explicit zero int64")
	ck(z.GetRatio() == 0 && z.HasRatio(), "explicit zero float64")
	ck(z.GetActive() == false && z.HasActive(), "explicit false bool")
	ck(z.GetLabel() == "" && z.HasLabel(), "explicit empty string")
	ck(z.GetMode() == OptEnumZero && z.HasMode(), "explicit zero enum")
	ck(z.GetBlob() != nil && len(z.GetBlob()) == 0 && z.HasBlob(), "explicit empty bytes presence")
	ck(z.GetChild() != nil && z.HasChild(), "explicit empty child presence")

	// 3. 非零值 + GC：指针字段、字节切片、嵌套结构全部可读。
	m := &optMsg{
		ID: i32p(-123), Count: i64p(1 << 50), Ratio: f64p(-0.5), Active: boolp(true),
		Label: strp("hello"), Mode: enump(OptEnumOne), Blob: []byte{9, 8, 7},
		Child: &optChild{Name: "kid", Weight: 99},
	}
	gcSink = append(gcSink, m.Blob)
	gcStress()
	ck(m.GetID() == -123 && m.GetCount() == 1<<50 && m.GetRatio() == -0.5 && m.GetActive(), "fixed getters after GC")
	ck(m.GetLabel() == "hello" && m.GetMode() == OptEnumOne, "fixed enum/string after GC")
	ck(bytes.Equal(m.GetBlob(), []byte{9, 8, 7}) && m.HasBlob(), "fixed bytes after GC")
	ck(m.GetChild() != nil && m.GetChild().Name == "kid" && m.GetChild().Weight == 99, "fixed child after GC")

	// 4. 默认值描述符表与 struct 字段逐一对应。
	rt := reflect.TypeOf(optMsg{})
	ck(rt.NumField() == optFieldCount, "struct field count matches descriptor")
	for i := 0; i < optFieldCount; i++ {
		ck(rt.Field(i).Name == optFieldTable[i].Name, "descriptor field name")
	}
	ck(optFieldTable[0].Def == strconv.Itoa(int(defID)), "descriptor id default")
	ck(optFieldTable[1].Def == strconv.FormatInt(defCount, 10), "descriptor count default")
	ck(optFieldTable[2].Def == "3.25", "descriptor ratio default")
	ck(optFieldTable[3].Def == strconv.FormatBool(defActive), "descriptor bool default")
	ck(optFieldTable[4].Def == defLabel, "descriptor string default")
	ck(optFieldTable[5].Def == strconv.Itoa(int(defMode)), "descriptor enum default")
	ck(optFieldTable[6].Def == fmt.Sprintf("% x", defBlob), "descriptor bytes default")

	// 5. 默认值在 GC 与垃圾分配后保持稳定。
	blobHash := func(b []byte) int {
		h := 0
		for _, v := range b {
			h = h*31 + int(v)
		}
		return h
	}
	h0 := blobHash(defBlob)
	for i := 0; i < 40; i++ {
		gcSink = append(gcSink[:0], make([]byte, 1024))
		if i%10 == 0 {
			runtime.GC()
		}
		_ = nilMsg.GetID()
		_ = nilMsg.GetLabel()
		_ = nilMsg.GetBlob()
	}
	ck(blobHash(defBlob) == h0 && bytes.Equal(defBlob, []byte{0xde, 0xad, 0xbe, 0xef}), "default blob stable")
	ck(nilMsg.GetLabel() == "unset-label" && nilMsg.GetMode() == OptEnumTwo, "default scalars stable")

	// 6. 方法值捕获 receiver / nil receiver，跨 GC 调用。
	mm := &optMsg{ID: i32p(55)}
	getID := mm.GetID
	hasID := mm.HasID
	getBlob := mm.GetBlob
	nilGetID := nilMsg.GetID
	nilHasBlob := nilMsg.HasBlob
	gcStress()
	ck(getID() == 55 && hasID(), "method value keeps receiver")
	ck(bytes.Equal(getBlob(), defBlob), "method value default blob")
	ck(nilGetID() == defID && !nilHasBlob(), "method value nil receiver")

	// 7. UAF_SEED 随机 presence 组合；GC 后按期望模型复核。
	r := rand.New(rand.NewSource(curSeed))
	type held struct {
		msg *optMsg
		exp optExpect
	}
	var kept []held
	for iter := 0; iter < 300; iter++ {
		msg, exp := buildRandom(r)
		checkMsg(msg, exp, "random pre-GC")
		if iter%31 == 0 {
			keep := held{msg: msg, exp: exp}
			kept = append(kept, keep)
			gcStress()
			checkMsg(msg, exp, "random post-GC")
		}
	}
	gcSink = append(gcSink, defBlob)
	runtime.GC()
	for i := range kept {
		checkMsg(kept[i].msg, kept[i].exp, fmt.Sprintf("retained %d", i))
	}

	finish("l10_optional_getters")
}
