// L10: oneof 的 JSON 判别式 codec：分支 codec 表、kind+value 信封、
// 未知/重复键拒绝、typed-nil/null 语义、转义与 base64、输入释放后 GC。
package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"os"
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

// ---- 分支类型 ----

type uText struct{ V string }
type uCount struct{ V int64 }
type uBlob struct{ V []byte }
type uFlag struct{ V bool }
type uEnum struct{ V int32 }

type leaf struct {
	S string `json:"s"`
}

type uNested struct{ V *leaf }

func branchOf(v any) (string, any, bool) {
	switch x := v.(type) {
	case *uText:
		if x == nil {
			return "", nil, false
		}
		return "text", x.V, true
	case *uCount:
		if x == nil {
			return "", nil, false
		}
		return "count", x.V, true
	case *uBlob:
		if x == nil {
			return "", nil, false
		}
		return "blob", x.V, true
	case *uFlag:
		if x == nil {
			return "", nil, false
		}
		return "flag", x.V, true
	case *uEnum:
		if x == nil {
			return "", nil, false
		}
		return "enum", x.V, true
	case *uNested:
		if x == nil || x.V == nil {
			return "", nil, false
		}
		return "nested", x.V, true
	}
	return "", nil, false
}

// ---- 分支 codec 表 ----

type branchCodec struct {
	name   string
	number int32
	encode func(any) ([]byte, error)
	decode func([]byte) (any, error)
}

var branchTable = []branchCodec{
	{"text", 1,
		func(v any) ([]byte, error) { return json.Marshal(v.(string)) },
		func(raw []byte) (any, error) {
			var s string
			if err := json.Unmarshal(raw, &s); err != nil {
				return nil, err
			}
			return &uText{V: s}, nil
		}},
	{"count", 2,
		func(v any) ([]byte, error) { return []byte(strconv.FormatInt(v.(int64), 10)), nil },
		func(raw []byte) (any, error) {
			n, err := strconv.ParseInt(string(bytes.TrimSpace(raw)), 10, 64)
			if err != nil {
				return nil, err
			}
			return &uCount{V: n}, nil
		}},
	{"blob", 3,
		func(v any) ([]byte, error) { return json.Marshal(v.([]byte)) },
		func(raw []byte) (any, error) {
			var b []byte
			if err := json.Unmarshal(raw, &b); err != nil {
				return nil, err
			}
			if b == nil {
				b = []byte{}
			}
			return &uBlob{V: b}, nil
		}},
	{"flag", 4,
		func(v any) ([]byte, error) { return []byte(strconv.FormatBool(v.(bool))), nil },
		func(raw []byte) (any, error) {
			var b bool
			if err := json.Unmarshal(raw, &b); err != nil {
				return nil, err
			}
			return &uFlag{V: b}, nil
		}},
	{"enum", 5,
		func(v any) ([]byte, error) { return []byte(strconv.FormatInt(int64(v.(int32)), 10)), nil },
		func(raw []byte) (any, error) {
			n, err := strconv.ParseInt(string(bytes.TrimSpace(raw)), 10, 32)
			if err != nil {
				return nil, err
			}
			return &uEnum{V: int32(n)}, nil
		}},
	{"nested", 6,
		func(v any) ([]byte, error) { return json.Marshal(v.(*leaf)) },
		func(raw []byte) (any, error) {
			if len(bytes.TrimSpace(raw)) == 0 || bytes.TrimSpace(raw)[0] != '{' {
				return nil, errors.New("nested value must be object")
			}
			var l leaf
			if err := json.Unmarshal(raw, &l); err != nil {
				return nil, err
			}
			return &uNested{V: &l}, nil
		}},
}

var branchByName = func() map[string]*branchCodec {
	m := make(map[string]*branchCodec, len(branchTable))
	for i := range branchTable {
		m[branchTable[i].name] = &branchTable[i]
	}
	return m
}()

func encodeUnion(u any) ([]byte, error) {
	kind, val, ok := branchOf(u)
	if !ok {
		return []byte("null"), nil
	}
	b, ok := branchByName[kind]
	if !ok {
		return nil, fmt.Errorf("unknown branch %q", kind)
	}
	vb, err := b.encode(val)
	if err != nil {
		return nil, err
	}
	kb, err := json.Marshal(kind)
	if err != nil {
		return nil, err
	}
	var out bytes.Buffer
	out.WriteString(`{"kind":`)
	out.Write(kb)
	out.WriteString(`,"value":`)
	out.Write(vb)
	out.WriteByte('}')
	return out.Bytes(), nil
}

var errBadUnion = errors.New("malformed oneof envelope")

func decodeUnion(data []byte) (any, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	if tok == nil { // JSON null
		if _, err := dec.Token(); err != io.EOF {
			return nil, errBadUnion
		}
		return nil, nil
	}
	delim, ok := tok.(json.Delim)
	if !ok || delim != '{' {
		return nil, errBadUnion
	}
	var kindRaw, valueRaw json.RawMessage
	kindSeen, valueSeen := false, false
	for dec.More() {
		kt, err := dec.Token()
		if err != nil {
			return nil, err
		}
		key, ok := kt.(string)
		if !ok {
			return nil, errBadUnion
		}
		var raw json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			return nil, err
		}
		switch key {
		case "kind":
			if kindSeen {
				return nil, errors.New("duplicate kind")
			}
			kindSeen = true
			kindRaw = raw
		case "value":
			if valueSeen {
				return nil, errors.New("duplicate value")
			}
			valueSeen = true
			valueRaw = raw
		default:
			return nil, fmt.Errorf("unknown envelope key %q", key)
		}
	}
	end, err := dec.Token()
	if err != nil {
		return nil, err
	}
	if d, ok := end.(json.Delim); !ok || d != '}' {
		return nil, errBadUnion
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, errBadUnion
	}
	if !kindSeen || !valueSeen {
		return nil, errors.New("missing kind or value")
	}
	trimmedKind := bytes.TrimSpace(kindRaw)
	if bytes.Equal(trimmedKind, []byte("null")) {
		return nil, errors.New("kind must be string")
	}
	var kind string
	if err := json.Unmarshal(kindRaw, &kind); err != nil {
		return nil, err
	}
	if bytes.Equal(bytes.TrimSpace(valueRaw), []byte("null")) {
		return nil, nil // oneof 值为 null 视为未设置
	}
	b, ok := branchByName[kind]
	if !ok {
		return nil, fmt.Errorf("unknown branch %q", kind)
	}
	return b.decode(valueRaw)
}

func equalUnion(a, b any) bool {
	ka, va, oka := branchOf(a)
	kb, vb, okb := branchOf(b)
	if oka != okb {
		return false
	}
	if !oka {
		return true
	}
	if ka != kb {
		return false
	}
	switch ka {
	case "text":
		return va.(string) == vb.(string)
	case "count":
		return va.(int64) == vb.(int64)
	case "blob":
		return bytes.Equal(va.([]byte), vb.([]byte))
	case "flag":
		return va.(bool) == vb.(bool)
	case "enum":
		return va.(int32) == vb.(int32)
	case "nested":
		return va.(*leaf).S == vb.(*leaf).S
	}
	return false
}

func expectDecodeError(name, input string) {
	_, err := decodeUnion([]byte(input))
	ck(err != nil, "expected decode error: "+name)
}

func main() {
	seed := int64(1)
	if v := os.Getenv("UAF_SEED"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			seed = n
		}
	}
	curSeed = seed

	// 1. 分支表完整性。
	ck(len(branchTable) == 6, "branch table size")
	nums := make(map[int32]string, 6)
	for _, b := range branchTable {
		if _, ok := nums[b.number]; ok {
			ck(false, "duplicate branch number")
		}
		nums[b.number] = b.name
	}
	ck(len(nums) == 6 && nums[1] == "text" && nums[6] == "nested", "branch numbers unique")

	// 2. 确定性编码（含空值、base64、转义）。
	golden := []struct {
		u    any
		want string
	}{
		{&uText{V: "hi"}, `{"kind":"text","value":"hi"}`},
		{&uText{V: `a"b\n`}, `{"kind":"text","value":"a\"b\\n"}`},
		{&uCount{V: 0}, `{"kind":"count","value":0}`},
		{&uCount{V: -9223372036854775808}, `{"kind":"count","value":-9223372036854775808}`},
		{&uFlag{V: false}, `{"kind":"flag","value":false}`},
		{&uBlob{V: []byte{}}, `{"kind":"blob","value":""}`},
		{&uBlob{V: []byte{0xde, 0xad}}, `{"kind":"blob","value":"3q0="}`},
		{&uEnum{V: 99}, `{"kind":"enum","value":99}`},
		{&uNested{V: &leaf{S: "x"}}, `{"kind":"nested","value":{"s":"x"}}`},
		{&uNested{V: &leaf{}}, `{"kind":"nested","value":{"s":""}}`},
		{nil, `null`},
		{(*uText)(nil), `null`},
		{&uNested{V: nil}, `null`},
	}
	for _, g := range golden {
		enc, err := encodeUnion(g.u)
		ck(err == nil && string(enc) == g.want, "golden encode "+g.want)
	}

	// 3. 全分支往返，包含空字符串/空 blob/嵌套空对象。
	values := []any{
		&uText{V: ""}, &uText{V: "hello"},
		&uCount{V: 0}, &uCount{V: -7},
		&uBlob{V: []byte{}}, &uBlob{V: []byte{1, 2, 3}},
		&uFlag{V: true}, &uFlag{V: false},
		&uEnum{V: 0}, &uEnum{V: -1},
		&uNested{V: &leaf{S: ""}}, &uNested{V: &leaf{S: "n"}},
		nil,
	}
	for _, u := range values {
		enc, err := encodeUnion(u)
		ck(err == nil, "encode branch")
		got, err := decodeUnion(enc)
		ck(err == nil && equalUnion(got, u), "roundtrip branch "+string(enc))
		enc2, err := encodeUnion(got)
		ck(err == nil && bytes.Equal(enc2, enc), "canonical re-encode")
	}

	// 4. 键顺序无关、空白容忍、跨分支替换、null 语义。
	got, err := decodeUnion([]byte(" { \"value\" : { \"s\" : \"z\" } , \"kind\" : \"nested\" } "))
	ck(err == nil && equalUnion(got, &uNested{V: &leaf{S: "z"}}), "key order and whitespace")
	got, err = decodeUnion([]byte(`{"kind":"text","value":"a"}`))
	ck(err == nil && equalUnion(got, &uText{V: "a"}), "decode text")
	got, err = decodeUnion([]byte(`{"kind":"count","value":1}`))
	ck(err == nil && equalUnion(got, &uCount{V: 1}), "cross branch count")
	if got, err := decodeUnion([]byte("null")); err != nil || got != nil {
		ck(false, "top-level null")
	}
	for _, input := range []string{
		`{"kind":"text","value":null}`,
		`{"kind":"blob","value":null}`,
		`{"kind":"nested","value":null}`,
	} {
		if got, err := decodeUnion([]byte(input)); err != nil || got != nil {
			ck(false, "value null clears")
		}
	}

	// 5. 错误路径。
	expectDecodeError("empty", "")
	expectDecodeError("invalid json", "{")
	expectDecodeError("not object", `[]`)
	expectDecodeError("string", `"x"`)
	expectDecodeError("missing kind", `{"value":"x"}`)
	expectDecodeError("missing value", `{"kind":"text"}`)
	expectDecodeError("null kind", `{"kind":null,"value":"x"}`)
	expectDecodeError("non-string kind", `{"kind":1,"value":"x"}`)
	expectDecodeError("unknown kind", `{"kind":"nope","value":1}`)
	expectDecodeError("unknown key", `{"kind":"text","value":"x","extra":1}`)
	expectDecodeError("duplicate kind", `{"kind":"text","kind":"count","value":1}`)
	expectDecodeError("duplicate value", `{"kind":"text","value":"a","value":"b"}`)
	expectDecodeError("text wrong type", `{"kind":"text","value":5}`)
	expectDecodeError("count float", `{"kind":"count","value":1.5}`)
	expectDecodeError("count string", `{"kind":"count","value":"5"}`)
	expectDecodeError("count overflow", `{"kind":"count","value":9223372036854775808}`)
	expectDecodeError("flag number", `{"kind":"flag","value":1}`)
	expectDecodeError("blob bad base64", `{"kind":"blob","value":"!!"}`)
	expectDecodeError("enum string", `{"kind":"enum","value":"ONE"}`)
	expectDecodeError("enum float", `{"kind":"enum","value":1.0}`)
	expectDecodeError("nested number", `{"kind":"nested","value":3}`)
	expectDecodeError("trailing data", `{"kind":"text","value":"a"} extra`)
	expectDecodeError("trailing null", "null null")

	// 6. 输入释放/改写与 GC：解码出的字符串/blob 不别名输入。
	src := []byte(`{"kind":"blob","value":"3q0="}`)
	kept, err := decodeUnion(src)
	ck(err == nil && equalUnion(kept, &uBlob{V: []byte{0xde, 0xad}}), "blob decode")
	for i := range src {
		src[i] = 'x'
	}
	src = nil
	gcStress()
	ck(equalUnion(kept, &uBlob{V: []byte{0xde, 0xad}}), "blob survives input clobber")
	keptEnc, _ := encodeUnion(kept)
	kept = nil
	gcStress()
	decoded, err := decodeUnion(keptEnc)
	ck(err == nil && equalUnion(decoded, &uBlob{V: []byte{0xde, 0xad}}), "encoded bytes survive GC")

	// 7. 随机 oneof 编码/解码。
	r := rand.New(rand.NewSource(seed))
	for i := 0; i < 200; i++ {
		var u any
		switch r.Intn(8) {
		case 0:
			u = nil
		case 1:
			u = (*uText)(nil)
		case 2:
			u = &uText{V: []string{"", "x", "quote\"", "hé\n"}[r.Intn(4)]}
		case 3:
			u = &uCount{V: int64(r.Intn(1<<40) - 1<<39)}
		case 4:
			n := r.Intn(6)
			b := make([]byte, n)
			for j := range b {
				b[j] = byte(r.Intn(256))
			}
			u = &uBlob{V: b}
		case 5:
			u = &uFlag{V: r.Intn(2) == 1}
		case 6:
			u = &uEnum{V: int32(r.Intn(200) - 100)}
		case 7:
			u = &uNested{V: &leaf{S: []string{"", "leaf", "a\"b"}[r.Intn(3)]}}
		}
		enc, err := encodeUnion(u)
		ckSeed(err == nil, "random encode")
		back, err := decodeUnion(enc)
		ckSeed(err == nil, "random decode")
		ckSeed(equalUnion(back, u), "random semantic roundtrip")
		enc2, err := encodeUnion(back)
		ckSeed(err == nil && bytes.Equal(enc2, enc), "random canonical encode")
		if i%25 == 24 {
			gcStress()
			re, err := decodeUnion(enc)
			ckSeed(err == nil && equalUnion(re, u), "random survives GC")
		}
	}

	finish("l10_oneof_codec")
}
